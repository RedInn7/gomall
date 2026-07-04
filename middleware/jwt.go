package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/jwt"
)

// tokenVersionLookup 按 uid 查用户当前 token 版本号，由组合根（routes.NewRouter）注入。
// 依赖倒置：middleware 不 import 领域包（同 rbac.go 的 roleLookup，避免 import 环）。
var tokenVersionLookup func(ctx context.Context, userId uint) (uint, error)

// SetTokenVersionLookup 注入版本号查询实现，进程启动时调用一次。
func SetTokenVersionLookup(fn func(ctx context.Context, userId uint) (uint, error)) {
	tokenVersionLookup = fn
}

type tokenVerCacheEntry struct {
	ver     uint
	expires time.Time
}

var (
	tokenVerCache    sync.Map
	tokenVerCacheTTL = 60 * time.Second
)

// currentTokenVersion 带短 TTL 内存缓存，命中时零 DB 开销。
// 版本号 bump 走 InvalidateTokenVersionCache 主动清缓存，撤销即时生效，不等 TTL。
func currentTokenVersion(ctx context.Context, userId uint) (uint, error) {
	if v, ok := tokenVerCache.Load(userId); ok {
		entry := v.(tokenVerCacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.ver, nil
		}
	}
	if tokenVersionLookup == nil { // 未注入即拒绝（fail-closed）：撤销机制失联时不能放行
		return 0, errors.New("jwt: token version lookup not configured")
	}
	ver, err := tokenVersionLookup(ctx, userId)
	if err != nil {
		return 0, err
	}
	tokenVerCache.Store(userId, tokenVerCacheEntry{ver: ver, expires: time.Now().Add(tokenVerCacheTTL)})
	return ver, nil
}

// InvalidateTokenVersionCache 版本号 bump 后调用，让该用户的旧 token 立即失效。
// 竞态边界：若 currentTokenVersion 的"读库→写缓存"恰好横跨 bump+本清理，旧版本号
// 会被缓存至多 TTL（60s）——撤销延迟上限是 TTL，不是绝对零。窗口为毫秒级，可接受。
// ponytail: 单进程内存缓存；多实例部署时需换 Redis 或 pub/sub 广播失效。
func InvalidateTokenVersionCache(userId uint) {
	tokenVerCache.Delete(userId)
}

// AuthMiddleware token验证中间件
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var code int
		code = e.SUCCESS
		accessToken := c.GetHeader("access_token")
		refreshToken := c.GetHeader("refresh_token")
		if accessToken == "" {
			code = e.InvalidParams
			c.JSON(200, gin.H{
				"status": code,
				"msg":    e.GetMsg(code),
				"data":   "Token不能为空",
			})
			c.Abort()
			return
		}
		newAccessToken, newRefreshToken, err := util.ParseRefreshToken(accessToken, refreshToken)
		if err != nil {
			code = e.ErrorAuthCheckTokenFail
			log.LogrusObj.Infoln("ParseRefreshToken 错误，err:", err)
		}
		if code != e.SUCCESS {
			c.JSON(200, gin.H{
				"status": code,
				"msg":    e.GetMsg(code),
				"data":   "鉴权失败",
				"error":  err.Error(),
			})
			c.Abort()
			return
		}
		claims, err := util.ParseToken(newAccessToken)
		if err != nil {
			log.LogrusObj.Infoln("ParseToken 错误，err:", err)
			code = e.ErrorAuthCheckTokenFail
			c.JSON(200, gin.H{
				"status": code,
				"msg":    e.GetMsg(code),
				"data":   err.Error(),
			})
			c.Abort()
			return
		}
		// 撤销检查：claims 版本号必须等于用户当前版本号。改密码/强制下线 bump 后，
		// 旧 token（以及用旧 token 续签出的新 token）在这里被拒，只能重新登录。
		curVer, err := currentTokenVersion(c.Request.Context(), claims.ID)
		if err != nil || claims.TokenVersion != curVer {
			if err != nil {
				log.LogrusObj.Errorln("token version lookup 错误，err:", err)
			}
			code = e.ErrorAuthCheckTokenFail
			c.JSON(200, gin.H{
				"status": code,
				"msg":    e.GetMsg(code),
				"data":   "登录已失效，请重新登录",
			})
			c.Abort()
			return
		}
		SetToken(c, newAccessToken, newRefreshToken)
		c.Request = c.Request.WithContext(ctl.NewContext(c.Request.Context(), &ctl.UserInfo{Id: claims.ID}))
		ctl.InitUserInfo(c.Request.Context())
		c.Next()
	}
}

func SetToken(c *gin.Context, accessToken, refreshToken string) {
	secure := IsHttps(c)
	c.Header(consts.AccessTokenHeader, accessToken)
	c.Header(consts.RefreshTokenHeader, refreshToken)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(consts.AccessTokenHeader, accessToken, consts.MaxAge, "/", "", secure, true)
	c.SetCookie(consts.RefreshTokenHeader, refreshToken, consts.MaxAge, "/", "", secure, true)
}

// 判断是否https
func IsHttps(c *gin.Context) bool {
	if c.GetHeader(consts.HeaderForwardedProto) == "https" || c.Request.TLS != nil {
		return true
	}
	return false
}
