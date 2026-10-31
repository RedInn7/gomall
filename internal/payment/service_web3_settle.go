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
	"github.com/RedInn7/gomall/internal/clearing"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

// 链上支付代币与计价配置走环境变量（与 WEB3_* 一致）。订单金额是人民币分，两种代币都要喂人民币汇率。
//   - WEB3_PAY_TOKEN          : usdc(默认) | eth
//   - WEB3_USDC_DECIMALS      : USDC 代币精度，默认 6
//   - WEB3_USDC_CENTS_PER_USDC: USDC 计价，1 USDC 折多少人民币分（如 720 = ¥7.20/USDC）。
//     走 USDC 必填：USDC 锚定美元，不能把人民币分按 1:1 当美分收。
//   - WEB3_ETH_CENTS_PER_ETH  : ETH 计价，1 ETH 折多少人民币分（如 2160000 = ¥21600/ETH）。走 ETH 必填。
//   - WEB3_AMOUNT_TOLERANCE_BPS: 允许的链上金额下浮容差（基点），默认 50 = 0.5%，吸收报价滑点。
const (
	envWeb3PayToken     = "WEB3_PAY_TOKEN"
	envWeb3USDCDecimals = "WEB3_USDC_DECIMALS"
	envWeb3CentsPerUSDC = "WEB3_USDC_CENTS_PER_USDC"
	envWeb3CentsPerETH  = "WEB3_ETH_CENTS_PER_ETH"
	envWeb3ToleranceBps = "WEB3_AMOUNT_TOLERANCE_BPS"
	tokenUSDC           = "usdc"
	tokenETH            = "eth"
	defaultUSDCDecimals = 6
	defaultToleranceBps = 50
	weiDecimals         = 18
	// web3ClearingCurrency 清算单币种：订单人民币分，与钱包 / Stripe / Xcash 一致。
	web3ClearingCurrency = "CNY"
)

var (
	// ErrWeb3AmountMismatch 链上确认金额不足以覆盖订单应付金额（防少付）。
	ErrWeb3AmountMismatch = errors.New("链上确认金额低于订单应付")
	// ErrWeb3PriceNotConfigured 走 ETH 但未配置喂价。
	ErrWeb3PriceNotConfigured = errors.New("Web3 ETH 支付未配置 WEB3_ETH_CENTS_PER_ETH 喂价")
	// ErrWeb3USDCRateNotConfigured 走 USDC 但未配置人民币汇率。
	ErrWeb3USDCRateNotConfigured = errors.New("Web3 USDC 支付未配置 WEB3_USDC_CENTS_PER_USDC 人民币汇率")
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

// expectedTokenBaseUnits 把订单应付人民币分换算成所选代币的最小单位（USDC 6 位 / ETH wei）：
// units = cents * 10^decimals / centsPerToken，两种代币都按配置的人民币汇率换算，向下取整。
func expectedTokenBaseUnits(payableCents int64) (*big.Int, error) {
	envRate, decimals, errMissing := envWeb3CentsPerUSDC, web3USDCDecimals(), ErrWeb3USDCRateNotConfigured
	if web3PayToken() == tokenETH {
		envRate, decimals, errMissing = envWeb3CentsPerETH, weiDecimals, ErrWeb3PriceNotConfigured
	}
	centsPerToken, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(envRate)), 10, 64)
	if err != nil || centsPerToken <= 0 {
		return nil, errMissing
	}
	units := new(big.Int).Mul(big.NewInt(payableCents), pow10(decimals))
	return units.Div(units, big.NewInt(centsPerToken)), nil
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

// SettleConfirmedOrder 清算一笔链上已确认的订单（ETH/USDC）。
// 由 listener 监听到 escrow 合约 PaymentConfirmed 事件后触发：校验 buyer 绑定 + 金额 →
// 标记已付 + 扣库存 + 商品归属转移 + 资金进入商户托管 + outbox order.paid。
// 幂等：订单 WaitPay 守卫 + (order_id,direction) 唯一索引，链上事件重投安全。
func (s *Web3SettleSrv) SettleConfirmedOrder(ctx context.Context, orderID uint, onchainBuyer, onchainAmount, txHash string) error {
	// 首次成功后 pending 会被删除；同一 tx 的 MQ/链上事件重放必须先从持久化清算单识别，
	// 不能再次依赖一次性的 Redis buyer 绑定，否则会把正常重放误判成 PendingMissing。
	alreadyCleared, err := clearing.IsProviderCleared(ctx, orderID, clearing.ChannelWeb3, txHash)
	if err != nil {
		return err
	}
	if alreadyCleared {
		return nil
	}
	// 先做 buyer 绑定校验：链上 buyer 必须等于签名 park 阶段写入的钱包地址，否则拒绝结算。
	// 放在事务外、最前面，确保未通过绑定校验时不触碰任何账务 / 库存状态。
	if err := verifyBuyerBinding(ctx, orderID, onchainBuyer); err != nil {
		return err
	}

	var (
		paidProductID uint
		paidNum       int
	)
	err = orderpkg.NewOrderDao(ctx).Transaction(func(tx *gorm.DB) error {
		order, err := orderpkg.NewOrderDaoByDB(tx).GetOrderByIdOnly(orderID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if order.Type != consts.OrderWaitPay {
			matched, recordErr := clearing.RecordExternalDuplicateTx(
				tx, order, clearing.ChannelWeb3, txHash, onchainAmount, strings.ToUpper(web3PayToken()),
			)
			if recordErr != nil {
				return recordErr
			}
			if !matched {
				log.LogrusObj.Errorf("web3 duplicate payment recorded for refund review: order=%d tx=%s", orderID, txHash)
			}
			return nil
		}

		paidProductID = order.ProductID
		paidNum = order.Num

		// 实付口径统一走 orderPayableCents（命中促销取折后 FinalCents），与余额 / Stripe 路径一致。
		payable := orderPayableCents(order)

		// 校验链上金额覆盖订单应付（按所选代币精度换算），不足直接拒绝、不结算。
		if err := verifyOnchainAmount(payable, onchainAmount); err != nil {
			return err
		}

		// 链上已收资金从外部清算账户转入商户托管；履约完成前不增加卖家可用余额。
		// 清算金额是按汇率核过的订单人民币分，币种记 CNY 与金额同口径；实收代币以 tx hash 为准。
		if err = clearing.RecordClearedTx(tx, order, clearing.ChannelWeb3, txHash, web3ClearingCurrency, nil); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		// 资金已进入托管，余下"扣库存 → 标记已付 → 商品归属转移 → outbox order.paid"走共享尾段。
		return finishPaymentConfirmationTx(tx, order)
	})
	if err != nil {
		log.LogrusObj.Errorf("web3 settle order=%d failed: %v", orderID, err)
		// 链上已到账却确定性无法履约：改记异常单并返回成功，消费者 ack，不再热重投。
		return recordUnfulfillable(orderpkg.NewOrderDao(ctx).DB, err, orderID, clearing.ChannelWeb3,
			txHash, onchainAmount, strings.ToUpper(web3PayToken()))
	}

	commitReservationBestEffort(ctx, paidProductID, paidNum)
	// 结算成功后清掉 Redis pending 占位（best-effort）。
	if delErr := cache.DelWeb3Pending(ctx, orderID); delErr != nil {
		log.LogrusObj.Warnf("del web3 pending placeholder order=%d err=%v", orderID, delErr)
	}
	return nil
}
