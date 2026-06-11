package user

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

func UserRegisterHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UserRegisterReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		// 参数校验
		if len(req.Key) != consts.EncryptMoneyKeyLength {
			err := errors.New("key长度错误,必须是6位数")
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.UserRegister(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// UserLoginHandler 用户登陆接口
func UserLoginHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UserServiceReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.UserLogin(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func UserUpdateHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UserInfoUpdateReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.UserInfoUpdate(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusInternalServerError, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func ShowUserInfoHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UserInfoShowReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.UserInfoShow(ctx.Request.Context())
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusInternalServerError, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func UploadAvatarHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UserServiceReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}
		file, fileHeader, _ := ctx.Request.FormFile("file")
		if fileHeader == nil {
			err := errors.New(e.GetMsg(e.ErrorUploadFile))
			ctx.JSON(consts.IlleageRequest, response.ErrorResponse(ctx, err))
			log.LogrusObj.Infoln(err)
			return
		}
		fileSize := fileHeader.Size

		l := GetUserSrv()
		resp, err := l.UserAvatarUpload(ctx.Request.Context(), file, fileSize, &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusInternalServerError, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func SendEmailHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req SendEmailServiceReq

		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.SendEmail(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusInternalServerError, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func UserFollowingHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UserFollowingReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.UserFollow(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusInternalServerError, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func UserUnFollowingHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req UserUnFollowingReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.UserUnFollow(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusInternalServerError, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func ValidEmailHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req ValidEmailServiceReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusBadRequest, response.ErrorResponse(ctx, err))
			return
		}

		l := GetUserSrv()
		resp, err := l.Valid(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusInternalServerError, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}
