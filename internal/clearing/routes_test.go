package clearing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/middleware"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
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

func TestPaymentAnomalyRoutesRejectNonAdminRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	roles := map[uint]string{97001: user.RoleUser, 97002: user.RoleMerchant}
	for id := range roles {
		middleware.InvalidateRoleCache(id)
	}
	middleware.SetRoleLookup(func(_ context.Context, userID uint) (string, error) {
		return roles[userID], nil
	})
	t.Cleanup(func() {
		for id := range roles {
			middleware.InvalidateRoleCache(id)
		}
		middleware.SetRoleLookup(nil)
	})

	router := gin.New()
	public := router.Group("/api/v1")
	authed := public.Group("/")
	authed.Use(func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.GetHeader("X-Test-User-ID"), 10, 64)
		c.Request = c.Request.WithContext(ctl.NewContext(c.Request.Context(), &ctl.UserInfo{Id: uint(id)}))
		c.Next()
	})
	merchant := authed.Group("/")
	adminGroup := authed.Group("/admin")
	adminGroup.Use(middleware.RequireRole(user.RoleAdmin))
	RegisterRoutes(public, authed, merchant, adminGroup)

	for userID, role := range roles {
		t.Run(role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/payment-anomalies", nil)
			req.Header.Set("X-Test-User-ID", strconv.FormatUint(uint64(userID), 10))
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			var body struct {
				Status int `json:"status"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Status != e.ErrorAuthInsufficientAuthority {
				t.Fatalf("status=%d body=%s", body.Status, resp.Body.String())
			}
		})
	}
}
