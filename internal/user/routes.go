package user

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	// 用户操作
	public.POST("user/register", UserRegisterHandler())
	public.POST("user/login", UserLoginHandler())

	// 用户操作
	authed.POST("user/update", UserUpdateHandler())
	authed.GET("user/show_info", ShowUserInfoHandler())
	authed.POST("user/send_email", SendEmailHandler())
	authed.GET("user/valid_email", ValidEmailHandler())
	authed.POST("user/following", UserFollowingHandler())
	authed.POST("user/unfollowing", UserUnFollowingHandler())
	authed.POST("user/avatar", UploadAvatarHandler()) // 上传头像
}
