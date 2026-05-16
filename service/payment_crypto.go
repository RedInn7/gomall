package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	web3sig "github.com/RedInn7/gomall/pkg/web3/signature"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
	"github.com/RedInn7/gomall/types"
)

var (
	CryptoPaymentSrvIns  *CryptoPaymentSrv
	CryptoPaymentSrvOnce sync.Once
)

type CryptoPaymentSrv struct{}

func GetCryptoPaymentSrv() *CryptoPaymentSrv {
	CryptoPaymentSrvOnce.Do(func() {
		CryptoPaymentSrvIns = &CryptoPaymentSrv{}
	})
	return CryptoPaymentSrvIns
}

// signMessageTemplate 与钱包侧 personal_sign 的明文一一对应。
// chainID 必须放进消息，避免一条签名在 mainnet / L2 之间互通造成重放
const signMessageTemplate = "gomall:paydown:order=%d:nonce=%s:chain=%d"

// BuildSignMessage 业务消息模板，nonce 接口与签名校验都用这一份
func BuildSignMessage(orderID uint, nonce string, chainID uint64) string {
	return fmt.Sprintf(signMessageTemplate, orderID, nonce, chainID)
}

// IssueNonce 校验订单归属 + 状态 + 颁发一次性 nonce
func (s *CryptoPaymentSrv) IssueNonce(ctx context.Context, req *types.CryptoNonceReq) (*types.CryptoNonceResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}

	order, err := dao.NewOrderDao(ctx).GetOrderById(req.OrderId, u.Id)
	if err != nil {
		return nil, err
	}
	if order == nil || order.ID == 0 {
		return nil, errors.New("订单不存在")
	}
	if order.Type != consts.UnPaid {
		return nil, errors.New("订单状态非未支付")
	}

	nonce, err := randomNonce()
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	if err := cache.PutWeb3Nonce(ctx, u.Id, req.OrderId, nonce); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	// chain=0 占位；前端拿到后会把真实 chainID 替换进模板后再交给钱包签名。
	// 这里返回的模板仅供前端展示/拼接参考。
	msgPreview := BuildSignMessage(req.OrderId, nonce, 0)
	return &types.CryptoNonceResp{
		Nonce:         nonce,
		MessageToSign: msgPreview,
		ExpiresIn:     int(cache.Web3NonceTTL.Seconds()),
	}, nil
}

// VerifyAndPark 校验签名+消费 nonce+写 outbox+占位 pending
func (s *CryptoPaymentSrv) VerifyAndPark(ctx context.Context, req *types.CryptoPaydownReq) (*types.CryptoPaydownResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}

	order, err := dao.NewOrderDao(ctx).GetOrderById(req.OrderID, u.Id)
	if err != nil {
		return nil, err
	}
	if order == nil || order.ID == 0 {
		return nil, errors.New("订单不存在")
	}
	if order.Type != consts.UnPaid {
		return nil, errors.New("订单状态非未支付")
	}

	// 1) 一次性消费 nonce，原子 GET+DEL，防重放
	if err := cache.ConsumeWeb3Nonce(ctx, u.Id, req.OrderID, req.Nonce); err != nil {
		log.LogrusObj.Warnf("web3 nonce consume fail user=%d order=%d err=%v", u.Id, req.OrderID, err)
		return nil, err
	}

	// 2) 还原签名消息并做 EIP-191 校验
	msg := []byte(BuildSignMessage(req.OrderID, req.Nonce, req.ChainID))
	sigBytes, err := decodeSignature(req.Signature)
	if err != nil {
		return nil, fmt.Errorf("签名格式非法: %w", err)
	}
	ok, err := web3sig.VerifyPersonalSign(req.WalletAddr, msg, sigBytes)
	if err != nil {
		log.LogrusObj.Warnf("web3 signature verify error user=%d order=%d err=%v", u.Id, req.OrderID, err)
		return nil, errors.New("签名校验失败")
	}
	if !ok {
		log.LogrusObj.Warnf("web3 signature mismatch user=%d order=%d addr=%s", u.Id, req.OrderID, req.WalletAddr)
		return nil, errors.New("签名与钱包地址不匹配")
	}

	// 3) 同一事务里写 outbox web3.payment.pending，保证消息一定与业务校验“同生共死”
	walletAddr := web3sig.NormalizeAddress(req.WalletAddr)
	totalAmount := order.Money * int64(order.Num)
	err = dao.NewOrderDao(ctx).Transaction(func(tx *gorm.DB) error {
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "Web3PaymentPending", "web3.payment.pending", order.ID,
			events.Web3PaymentPending{
				OrderID:    order.ID,
				OrderNum:   order.OrderNum,
				UserID:     u.Id,
				ProductID:  order.ProductID,
				Num:        order.Num,
				Amount:     totalAmount,
				WalletAddr: walletAddr,
				ChainID:    req.ChainID,
				Nonce:      req.Nonce,
			},
		)
	})
	if err != nil {
		log.LogrusObj.Errorf("write outbox web3.payment.pending fail: %v", err)
		return nil, err
	}

	// 4) Redis pending 占位，30min TTL；链上 listener 收到 confirm 后会 DEL
	if err := cache.SetWeb3Pending(ctx, order.ID, walletAddr); err != nil {
		// 占位失败不阻塞主流程，outbox 已经把事件落了
		log.LogrusObj.Errorf("set web3 pending placeholder fail order=%d err=%v", order.ID, err)
	}

	return &types.CryptoPaydownResp{
		Status:  "pending",
		Message: "请在钱包确认转账，链上确认后订单将自动更新",
	}, nil
}

// randomNonce 16 字节随机数 → 32 字符 hex，足够防碰撞
func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// decodeSignature 兼容 0x 前缀和裸 hex
func decodeSignature(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return hexutil.Decode(s)
	}
	return hex.DecodeString(s)
}
