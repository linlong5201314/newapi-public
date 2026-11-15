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
}
