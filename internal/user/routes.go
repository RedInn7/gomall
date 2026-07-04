package user

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	// 用户操作
	public.POST("user/register", UserRegisterHandler())
	// 登录：IP 维度滑动窗口限频（撞库防御第一层）。站在 bcrypt 前面，
	// 超限请求不消耗 250ms 的密码校验 CPU；正常用户一分钟登不满 10 次
	public.POST("user/login",
		middleware.SlidingWindow(middleware.SlidingWindowOption{
			Scope: "login", Window: time.Minute, Limit: 10, ByUser: false,
		}),
		UserLoginHandler())

	// 用户操作
	authed.POST("user/update", UserUpdateHandler())
	authed.GET("user/show_info", ShowUserInfoHandler())
	authed.POST("user/send_email", SendEmailHandler())
	authed.GET("user/valid_email", ValidEmailHandler())
	authed.POST("user/following", UserFollowingHandler())
	authed.POST("user/unfollowing", UserUnFollowingHandler())
	authed.POST("user/avatar", UploadAvatarHandler()) // 上传头像
}
