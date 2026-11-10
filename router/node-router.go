// node-router.go M5 节点管理路由组 /api/node（全部 RootAuth；写操作在 controller 层审计）。
package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerNodeRoutes 注册节点管理路由。
func registerNodeRoutes(apiRouter *gin.RouterGroup) {
	nodeRoute := apiRouter.Group("/node")
	nodeRoute.Use(middleware.RootAuth())
	{
		// 静态段先注册（settings/audit/deploy），避免与 :id 的同层歧义。
		nodeRoute.GET("/settings", controller.GetNodeSettings)
		nodeRoute.PUT("/settings", controller.UpdateNodeSettings)
		nodeRoute.GET("/audit", controller.GetNodeAudit)
		nodeRoute.POST("/deploy/plan", controller.NodeDeployPlan)
		nodeRoute.POST("/deploy/apply", controller.NodeDeployApply)

		nodeRoute.GET("", controller.GetNodes)
		nodeRoute.POST("", controller.CreateNode)
		// 蓝图契约：PUT /api/node（id 放在 body）；同时兼容 PUT /api/node/:id。
		nodeRoute.PUT("", controller.UpdateNode)
		nodeRoute.PUT("/:id", controller.UpdateNode)
		nodeRoute.DELETE("/:id", controller.DeleteNode)
		nodeRoute.POST("/:id/check", controller.CheckNode)
		nodeRoute.POST("/:id/failover/plan", controller.NodeFailoverPlan)
		nodeRoute.POST("/:id/failover/apply", controller.NodeFailoverApply)
	}
}
