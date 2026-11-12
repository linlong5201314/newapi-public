package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerFreeSourcePoolRoutes 注册公共免费模型提交池路由组 /api/free_source_pools。
//
// 该池面向全体登录用户（不依赖 SelfChannelEnabled 平台开关）：
// 列表/提交/评分/评论均需登录；删除仅提交者或管理员。
func registerFreeSourcePoolRoutes(apiRouter *gin.RouterGroup) {
	poolRoute := apiRouter.Group("/free_source_pools")
	poolRoute.Use(middleware.UserAuth())
	{
		poolRoute.GET("", controller.GetFreeSourcePools)
		poolRoute.GET("/:id", controller.GetFreeSourcePoolDetail)
		poolRoute.POST("", controller.SubmitFreeSourcePool)
		poolRoute.DELETE("/:id", controller.DeleteFreeSourcePool)
		poolRoute.POST("/:id/rate", controller.RateFreeSourcePool)
		poolRoute.POST("/:id/review", controller.ReviewFreeSourcePool)
		poolRoute.GET("/:id/reviews", controller.GetFreeSourcePoolReviews)
		poolRoute.POST("/:id/recommended", middleware.AdminAuth(), controller.ToggleFreeSourcePoolRecommended)
	}
}
