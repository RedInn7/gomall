package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// roleLookup 按 uid 查角色，由组合根（routes.NewRouter）注入。
// 依赖倒置：middleware 不 import 领域包，否则领域路由反向挂中间件时会成 import 环。
var roleLookup func(ctx context.Context, userId uint) (string, error)

// SetRoleLookup 注入角色查询实现，进程启动时调用一次。
func SetRoleLookup(fn func(ctx context.Context, userId uint) (string, error)) {
	roleLookup = fn
}

type roleCacheEntry struct {
	role    string
	expires time.Time
}

var (
	roleCache    sync.Map
	roleCacheTTL = 30 * time.Second
)

// lookupRole 带短 TTL 内存缓存，避免每个请求都打 DB
func lookupRole(ctx context.Context, userId uint) (string, error) {
	if v, ok := roleCache.Load(userId); ok {
		entry := v.(roleCacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.role, nil
		}
	}
	if roleLookup == nil { // 未注入即拒绝（fail-closed），而不是放行
		return "", errors.New("rbac: role lookup not configured")
	}
	role, err := roleLookup(ctx, userId)
	if err != nil {
		return "", err
	}
	if role == "" {
		role = "user"
	}
	roleCache.Store(userId, roleCacheEntry{role: role, expires: time.Now().Add(roleCacheTTL)})
	return role, nil
}

// RequireRole 角色访问控制中间件，允许列表内的任一角色通过
func RequireRole(allowed ...string) gin.HandlerFunc {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allowSet[r] = struct{}{}
	}
	return func(c *gin.Context) {
		user, err := ctl.GetUserInfo(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrorAuthCheckTokenFail,
				"msg":    e.GetMsg(e.ErrorAuthCheckTokenFail),
				"data":   "未识别用户身份",
			})
			c.Abort()
			return
		}
		role, err := lookupRole(c.Request.Context(), user.Id)
		if err != nil {
			log.LogrusObj.Errorln("rbac lookup role failed:", err)
			c.JSON(http.StatusOK, gin.H{
				"status": e.ERROR,
				"msg":    e.GetMsg(e.ERROR),
				"data":   "权限校验异常",
			})
			c.Abort()
			return
		}
		if _, ok := allowSet[role]; !ok {
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrorAuthInsufficientAuthority,
				"msg":    e.GetMsg(e.ErrorAuthInsufficientAuthority),
				"data":   "权限不足",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// InvalidateRoleCache 用户角色变更后调用，让缓存立即失效
func InvalidateRoleCache(userId uint) {
	roleCache.Delete(userId)
}
