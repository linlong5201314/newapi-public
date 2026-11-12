package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerProxyPoolRoutes 注册用户出口 IP 池路由组 /api/proxy-pool。
//
// 全部端点仅对已登录用户开放，数据按 user_id 隔离：
// 节点增删改查/连通性探测、订阅同步、统计卡与 mihomo 配置导出。
func registerProxyPoolRoutes(apiRouter *gin.RouterGroup) {
	proxyRoute := apiRouter.Group("/proxy-pool")
	proxyRoute.Use(middleware.UserAuth())
	{
		proxyRoute.GET("/endpoints", controller.GetProxyEndpoints)
		proxyRoute.POST("/endpoints", controller.CreateProxyEndpoint)
		proxyRoute.PATCH("/endpoints/:id", controller.UpdateProxyEndpoint)
		proxyRoute.DELETE("/endpoints/:id", controller.DeleteProxyEndpoint)
		proxyRoute.POST("/endpoints/:id/test", controller.TestProxyEndpoint)

		proxyRoute.GET("/subscriptions", controller.GetProxySubscriptions)
		proxyRoute.POST("/subscriptions", controller.CreateProxySubscription)
		proxyRoute.POST("/subscriptions/:id/sync", controller.SyncProxySubscription)
		proxyRoute.DELETE("/subscriptions/:id", controller.DeleteProxySubscription)

		proxyRoute.GET("/summary", controller.GetProxyPoolSummary)
		proxyRoute.GET("/export", controller.ExportProxyPoolConfig)
	}
}
