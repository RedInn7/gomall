package v1

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

// IdempotencyTokenHandler 颁发幂等 token，5 分钟有效
func IdempotencyTokenHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := ctl.GetUserInfo(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}

		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			log.LogrusObj.Errorln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		token := hex.EncodeToString(buf)

		key := cache.IdempotencyTokenKey(user.Id, token)
		if err := cache.IssueIdempotencyToken(c.Request.Context(), key); err != nil {
			log.LogrusObj.Errorln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		if err := cache.SetTokenTTL(c.Request.Context(), key); err != nil {
			log.LogrusObj.Errorln(err)
		}

		c.JSON(http.StatusOK, ctl.RespSuccess(c, gin.H{
			"idempotency_key": token,
			"ttl_seconds":     int(cache.IdempotencyTokenTTL.Seconds()),
		}))
	}
}
