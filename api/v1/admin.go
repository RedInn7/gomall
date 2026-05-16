package v1

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/service"
	"github.com/RedInn7/gomall/service/search"
)

func AdminListUsersHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
		resp, err := service.GetAdminSrv().ListAllUsers(c.Request.Context(), page, size)
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

func AdminPromoteUserHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			UserId uint `json:"user_id" form:"user_id" binding:"required"`
		}
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		if err := service.GetAdminSrv().PromoteToAdmin(c.Request.Context(), req.UserId); err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, gin.H{"promoted": req.UserId}))
	}
}

// BootstrapAdminHandler 仅当系统无 admin 时可用
func BootstrapAdminHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.GetAdminSrv().BootstrapPromoteSelf(c.Request.Context()); err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, gin.H{"ok": true}))
	}
}

// AdminBackfillProductIndexHandler 把 DB 中所有 product 灌一遍到 ES (一次性运维操作)
func AdminBackfillProductIndexHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		batch, _ := strconv.Atoi(c.DefaultQuery("batch", "200"))
		n, err := search.BackfillFromDB(c.Request.Context(), batch)
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, gin.H{"indexed": n}))
	}
}
