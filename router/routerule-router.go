package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerRouteRuleRoutes 注册用户级路由规则 API 组 /api/route_rule（M2）。
//
// 蓝图 docs/M2-路由与免费源设计.md §1.4：全部端点挂 UserAuth，
// handler 内部强制 UserId=当前用户。复用自服务渠道的平台总开关
// （SelfChannelEnabled=false 时整组 404，同属"我的中转站"功能面）。
func registerRouteRuleRoutes(apiRouter *gin.RouterGroup) {
	routeRuleRoute := apiRouter.Group("/route_rule")
	routeRuleRoute.Use(middleware.UserAuth())
	routeRuleRoute.Use(selfChannelEnabledGuard())
	{
		routeRuleRoute.GET("/", controller.GetRouteRules)
		routeRuleRoute.GET("/:id", controller.GetRouteRule)
		routeRuleRoute.POST("/", controller.AddRouteRule)
		routeRuleRoute.PUT("/", controller.UpdateRouteRule)
		routeRuleRoute.DELETE("/:id", controller.DeleteRouteRule)
		routeRuleRoute.POST("/:id/toggle", controller.ToggleRouteRule)
	}
}
