package clearing

import "github.com/gin-gonic/gin"

// RegisterRoutes 只在管理员路由组挂载异常款人工处置接口。
func RegisterRoutes(_, _, _, admin *gin.RouterGroup) {
	admin.GET("payment-anomalies", AdminListPaymentAnomaliesHandler())
	admin.GET("payment-anomalies/:id", AdminGetPaymentAnomalyHandler())
	// 即使 target_status=refunded，也只新增审计记录，不触发资金划转。
	admin.POST("payment-anomalies/:id/status-transitions", AdminRecordPaymentAnomalyTransitionHandler())
}
