package clearing

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPaymentAnomalyRoutesAreAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	public := router.Group("/api/v1")
	authed := public.Group("/")
	merchant := authed.Group("/")
	adminGroup := authed.Group("/admin")
	RegisterRoutes(public, authed, merchant, adminGroup)

	want := map[string]struct{}{
		"GET /api/v1/admin/payment-anomalies":                         {},
		"GET /api/v1/admin/payment-anomalies/:id":                     {},
		"POST /api/v1/admin/payment-anomalies/:id/status-transitions": {},
	}
	for _, route := range router.Routes() {
		delete(want, route.Method+" "+route.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing admin-only payment anomaly routes: %v", want)
	}
}
