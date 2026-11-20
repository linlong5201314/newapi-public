package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerPublicPoolRoutes 公共池路由组 /api/public_pool。
// 全部挂 UserAuth：公共池是登录用户之间共享的，不对外匿名开放。
func registerPublicPoolRoutes(apiRouter *gin.RouterGroup) {
	pool := apiRouter.Group("/public_pool")
	pool.Use(middleware.UserAuth())
	{
		pool.GET("/models", controller.GetPublicPoolModels)
		pool.GET("/comments", controller.GetPublicPoolComments)
		pool.POST("/comments", middleware.CriticalRateLimit(), controller.AddPublicPoolComment)
	}
	admin := pool.Group("/admin/channels", middleware.RootAuth(), middleware.DisableCache())
	{
		admin.GET("", controller.ListPublicPoolAdminChannels)
		admin.GET("/:id", controller.GetPublicPoolAdminChannel)
		admin.POST("", middleware.CriticalRateLimit(), controller.AddPublicPoolAdminChannel)
		admin.PUT("/:id", middleware.CriticalRateLimit(), controller.UpdatePublicPoolAdminChannel)
		admin.DELETE("/:id", controller.DeletePublicPoolAdminChannel)
		admin.POST("/:id/copy", middleware.CriticalRateLimit(), controller.CopyPublicPoolAdminChannel)
		admin.POST("/:id/status", controller.SetPublicPoolAdminChannelStatus)
		// IP 维度可用性：root 对公共渠道按公共节点逐一实测模型可用性
		admin.GET("/:id/probe_endpoints", controller.ListPublicPoolProbeEndpoints)
		admin.POST("/:id/probe_all", middleware.CriticalRateLimit(), controller.PostPublicPoolProbeAllPublicEndpoints)
	}
}
