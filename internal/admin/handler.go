package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/service/search"
)

func AdminListUsersHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
		resp, err := GetAdminSrv().ListAllUsers(c.Request.Context(), page, size)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

func AdminPromoteUserHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		type promoteReq struct {
			UserId uint `json:"user_id" form:"user_id" binding:"required"`
			// Role 目标角色（user/merchant/admin），缺省 admin 兼容旧调用方
			Role string `json:"role" form:"role"`
		}
		req, ok := response.Bind[promoteReq](c)
		if !ok {
			return
		}
		if req.Role == "" {
			req.Role = user.RoleAdmin
		}
		if err := GetAdminSrv().PromoteUser(c.Request.Context(), req.UserId, req.Role); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"promoted": req.UserId, "role": req.Role})
	}
}

// BootstrapAdminHandler 仅当系统无 admin 时可用
func BootstrapAdminHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := GetAdminSrv().BootstrapPromoteSelf(c.Request.Context()); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"ok": true})
	}
}

// AdminBackfillProductIndexHandler 把 DB 中所有 product 灌一遍到 ES (一次性运维操作)
func AdminBackfillProductIndexHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		batch, _ := strconv.Atoi(c.DefaultQuery("batch", "200"))
		n, err := search.BackfillFromDB(c.Request.Context(), batch)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"indexed": n})
	}
}
