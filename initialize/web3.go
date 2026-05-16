package initialize

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service/web3"
)

// InitWeb3Listener WEB3_RPC_URL / WEB3_ESCROW_ADDR 未配置时静默跳过；
// 链上事件不可用不影响主链路（DB 支付 + outbox 链路仍正常）
func InitWeb3Listener(ctx context.Context) {
	if err := web3.StartPaymentListener(ctx); err != nil {
		util.LogrusObj.Warnf("Web3 listener 启动失败: %v", err)
	}
}
