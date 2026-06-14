package payment

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

// 链上支付代币与计价配置走环境变量（与 WEB3_* 一致）。
//   - WEB3_PAY_TOKEN          : usdc(默认) | eth
//   - WEB3_USDC_DECIMALS      : USDC 代币精度，默认 6
//   - WEB3_ETH_CENTS_PER_ETH  : ETH 计价，1 ETH 折多少法币分（如 300000 = $3000/ETH）。
//     走 ETH 必填；稳定币 USDC 与法币 1:1 无需喂价。
//   - WEB3_AMOUNT_TOLERANCE_BPS: 允许的链上金额下浮容差（基点），默认 50 = 0.5%，吸收 ETH 报价滑点。
const (
	envWeb3PayToken     = "WEB3_PAY_TOKEN"
	envWeb3USDCDecimals = "WEB3_USDC_DECIMALS"
	envWeb3CentsPerETH  = "WEB3_ETH_CENTS_PER_ETH"
	envWeb3ToleranceBps = "WEB3_AMOUNT_TOLERANCE_BPS"
	tokenUSDC           = "usdc"
	tokenETH            = "eth"
	defaultUSDCDecimals = 6
	defaultToleranceBps = 50
	weiDecimals         = 18
	fiatDecimals        = 2 // 法币以「分」为最小单位
)

var (
	// ErrWeb3AmountMismatch 链上确认金额不足以覆盖订单应付金额（防少付）。
	ErrWeb3AmountMismatch = errors.New("链上确认金额低于订单应付")
	// ErrWeb3PriceNotConfigured 走 ETH 但未配置喂价。
	ErrWeb3PriceNotConfigured = errors.New("Web3 ETH 支付未配置 WEB3_ETH_CENTS_PER_ETH 喂价")
	// ErrWeb3BuyerMismatch 链上 buyer 与签名 park 阶段写入的钱包地址不一致（防越权结算）。
	ErrWeb3BuyerMismatch = errors.New("链上 buyer 与 park 钱包地址不匹配")
	// ErrWeb3PendingMissing park 占位缺失（过期 / 从未签名）。无可信绑定来源，拒绝结算。
	ErrWeb3PendingMissing = errors.New("Web3 park 占位缺失，无法校验 buyer 绑定")
)

func web3PayToken() string {
	if t := strings.TrimSpace(strings.ToLower(os.Getenv(envWeb3PayToken))); t != "" {
		return t
	}
	return tokenUSDC
}

func web3USDCDecimals() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(envWeb3USDCDecimals))); err == nil && v > 0 {
		return v
	}
	return defaultUSDCDecimals
}

func web3ToleranceBps() int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(envWeb3ToleranceBps)), 10, 64); err == nil && v >= 0 {
		return v
	}
	return defaultToleranceBps
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// expectedTokenBaseUnits 把订单应付法币分换算成所选代币的最小单位（USDC 6 位 / ETH wei）。
//   - USDC（稳定币≈法币）：units = cents * 10^(decimals-2)，1:1 不需喂价。
//   - ETH：wei = cents * 10^18 / centsPerETH，需喂价。
func expectedTokenBaseUnits(payableCents int64) (*big.Int, error) {
	cents := big.NewInt(payableCents)
	switch web3PayToken() {
	case tokenETH:
		centsPerETH, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(envWeb3CentsPerETH)), 10, 64)
		if err != nil || centsPerETH <= 0 {
			return nil, ErrWeb3PriceNotConfigured
		}
		// wei = cents * 10^18 / centsPerETH
		wei := new(big.Int).Mul(cents, pow10(weiDecimals))
		wei.Div(wei, big.NewInt(centsPerETH))
		return wei, nil
	default: // usdc
		scale := web3USDCDecimals() - fiatDecimals
		if scale < 0 {
			scale = 0
		}
		return new(big.Int).Mul(cents, pow10(scale)), nil
	}
}

// verifyOnchainAmount 校验链上确认金额是否覆盖订单应付（允许超付，按容差允许 ETH 报价小幅下浮）。
func verifyOnchainAmount(payableCents int64, onchainAmount string) error {
	got, ok := new(big.Int).SetString(strings.TrimSpace(onchainAmount), 10)
	if !ok || got.Sign() < 0 {
		return errors.New("链上金额解析失败")
	}
	want, err := expectedTokenBaseUnits(payableCents)
	if err != nil {
		return err
	}
	// 容差下限：min = want * (10000 - bps) / 10000
	bps := web3ToleranceBps()
	minWant := new(big.Int).Mul(want, big.NewInt(10000-bps))
	minWant.Div(minWant, big.NewInt(10000))
	if got.Cmp(minWant) < 0 {
		// 纯函数不打日志（调用方 SettleConfirmedOrder / 消费者会记），便于隔离单测、避免依赖全局 logger。
		return fmt.Errorf("%w: got=%s want>=%s token=%s", ErrWeb3AmountMismatch, got, minWant, web3PayToken())
	}
	return nil
}

var (
	web3SettleSrvIns  *Web3SettleSrv
	web3SettleSrvOnce sync.Once
)

type Web3SettleSrv struct{}

func GetWeb3SettleSrv() *Web3SettleSrv {
	web3SettleSrvOnce.Do(func() { web3SettleSrvIns = &Web3SettleSrv{} })
	return web3SettleSrvIns
}

// verifyBuyerBinding 强校验链上 buyer 地址 == 签名 park 阶段写入 Redis 的钱包地址。
// 这是把“链下签名授权”真正绑定到“链上结算”的关键一环：缺了它，任何人只要凑出
// 金额匹配的事件就能结算任意订单。占位缺失（过期 / 未签名）同样拒绝，避免无绑定结算。
//
// 注：当前只能校验 buyer 字段。代币合约地址 / recipient（收款方）字段 escrow 合约
// 的 PaymentConfirmed 事件并未 emit，链下无法核对——需合约补 token/recipient 字段后
// 才能完整校验“钱付给了正确的收款地址、且是预期代币”。
func verifyBuyerBinding(ctx context.Context, orderID uint, onchainBuyer string) error {
	parked, err := cache.RedisClient.HGet(ctx, cache.Web3PendingKey(orderID), "addr").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			log.LogrusObj.Errorf("web3 settle reject order=%d: park placeholder missing", orderID)
			return ErrWeb3PendingMissing
		}
		// Redis 抖动属可重试错误，向上抛由消费者 Nack 重排，不静默放行（防绕过绑定校验）。
		return fmt.Errorf("read web3 pending addr order=%d: %w", orderID, err)
	}
	if !strings.EqualFold(strings.TrimSpace(parked), strings.TrimSpace(onchainBuyer)) {
		log.LogrusObj.Errorf("web3 settle reject order=%d: onchain buyer=%s != parked addr=%s", orderID, onchainBuyer, parked)
		return ErrWeb3BuyerMismatch
	}
	return nil
}

// SettleConfirmedOrder 结算一笔链上已确认的订单（ETH/USDC）。
// 由 listener 监听到 escrow 合约 PaymentConfirmed 事件后触发：校验 buyer 绑定 + 金额 →
// 标记已付 + 扣库存 + 商品归属转移 + 卖家入账 + 复式记账（卖家 credit / 平台清算账户 debit）
// + outbox order.paid。
// 幂等：订单 WaitPay 守卫 + (order_id,direction) 唯一索引，链上事件重投安全。
func (s *Web3SettleSrv) SettleConfirmedOrder(ctx context.Context, orderID uint, onchainBuyer, onchainAmount string) error {
	// 先做 buyer 绑定校验：链上 buyer 必须等于签名 park 阶段写入的钱包地址，否则拒绝结算。
	// 放在事务外、最前面，确保未通过绑定校验时不触碰任何账务 / 库存状态。
	if err := verifyBuyerBinding(ctx, orderID, onchainBuyer); err != nil {
		return err
	}

	var (
		paidProductID uint
		paidNum       int
	)
	err := orderpkg.NewOrderDao(ctx).Transaction(func(tx *gorm.DB) error {
		order, err := orderpkg.NewOrderDaoByDB(tx).GetOrderByIdOnly(orderID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if order.Type != consts.OrderWaitPay {
			log.LogrusObj.Infof("web3 settle skip: order=%d already settled (type=%d)", orderID, order.Type)
			return nil
		}

		num := order.Num
		bossID := order.BossID
		buyerID := order.UserID
		productID := order.ProductID
		paidProductID = productID
		paidNum = num

		payable := order.Money * int64(num)
		if order.PromoRuleID != 0 {
			payable = order.FinalCents
		}

		// 校验链上金额覆盖订单应付（按所选代币精度换算），不足直接拒绝、不结算。
		if err := verifyOnchainAmount(payable, onchainAmount); err != nil {
			return err
		}

		userDao := user.NewUserDaoByDB(tx)
		ledgerDao := money.NewLedgerDaoByDB(tx)

		boss, err := userDao.GetUserByIdForUpdate(bossID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		bossMoney, err := boss.DecryptMoney()
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		bossBalanceAfter := bossMoney + payable
		boss.Money = strconv.FormatInt(bossBalanceAfter, 10)
		if boss.Money, err = boss.EncryptMoney(); err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if err = userDao.UpdateUserById(bossID, boss); err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if err = ledgerDao.AppendTransaction(bossID, order.ID, money.DirectionCredit, payable, bossBalanceAfter, money.BizTypeWeb3Pay); err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		// 对手方：平台对外清算账户记 debit（链上资金入口），保持借贷平衡 + (order_id,debit) 幂等。
		if err = ledgerDao.AppendTransaction(money.ExternalClearingUserID, order.ID, money.DirectionDebit, payable, 0, money.BizTypeWeb3Pay); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		prod, err := product.NewProductDaoByDB(tx).GetProductById(productID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		ok, err := product.NewProductDaoWithDB(tx).DeductStock(productID, num)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if !ok {
			return errors.New("存在超卖问题")
		}

		paidOK, err := orderpkg.NewOrderDaoByDB(tx).MarkOrderPaidWithCheck(order.ID, buyerID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if !paidOK {
			return errors.New("订单状态已变更，无法重复支付")
		}

		buyer, err := userDao.GetUserById(buyerID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		productUser := product.Product{
			Name:          prod.Name,
			CategoryID:    prod.CategoryID,
			Title:         prod.Title,
			Info:          prod.Info,
			ImgPath:       prod.ImgPath,
			Price:         prod.Price,
			DiscountPrice: prod.DiscountPrice,
			Num:           num,
			OnSale:        false,
			BossID:        buyerID,
			BossName:      buyer.UserName,
			BossAvatar:    buyer.Avatar,
		}
		if err = product.NewProductDaoByDB(tx).CreateProduct(&productUser); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderPaid", "order.paid", order.ID,
			events.OrderPaid{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    buyerID,
				ProductID: productID,
				Num:       num,
			},
		)
	})
	if err != nil {
		log.LogrusObj.Errorf("web3 settle order=%d failed: %v", orderID, err)
		return err
	}

	if paidProductID > 0 && paidNum > 0 {
		if cErr := cache.CommitReservation(ctx, paidProductID, int64(paidNum)); cErr != nil {
			log.LogrusObj.Errorf("commit reservation failed product=%d num=%d err=%v", paidProductID, paidNum, cErr)
		}
	}
	// 结算成功后清掉 Redis pending 占位（best-effort）。
	if delErr := cache.DelWeb3Pending(ctx, orderID); delErr != nil {
		log.LogrusObj.Warnf("del web3 pending placeholder order=%d err=%v", orderID, delErr)
	}
	return nil
}
