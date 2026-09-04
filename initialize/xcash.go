package initialize

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/RedInn7/gomall/internal/payment"
	util "github.com/RedInn7/gomall/pkg/utils/log"
)

const (
	xcashReconcileInterval = time.Minute
	xcashReconcileBatch    = 50
)

// InitXcashReconciler 定时补查等待付款的账单，弥补 Webhook 丢失。
// 未配置 Xcash 时静默跳过，不影响其他支付渠道启动。
func InitXcashReconciler(ctx context.Context) {
	if strings.TrimSpace(os.Getenv("XCASH_BASE_URL")) == "" {
		return
	}
	service := payment.GetXcashPaymentSrv()
	go func() {
		reconcile := func() {
			count, err := service.ReconcilePending(ctx, xcashReconcileBatch)
			if err != nil {
				util.LogrusObj.Warnf("Xcash 主动对账失败（本轮 %d 张）: %v", count, err)
			}
		}
		reconcile()
		ticker := time.NewTicker(xcashReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcile()
			}
		}
	}()
}
