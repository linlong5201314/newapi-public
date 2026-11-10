package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// registerSelfChannelRoutes 注册自服务渠道路由组 /api/channel/self（多租户"我的中转站"）。
//
// 路由契约见 docs/多租户改造方案.md §3：全部端点挂 UserAuth，
// handler 内部强制 owner_id=当前用户。平台开关 SelfChannelEnabled=false
// 时整组 404（不暴露功能存在性）。
func registerSelfChannelRoutes(apiRouter *gin.RouterGroup) {
	selfChannelRoute := apiRouter.Group("/channel/self")
	selfChannelRoute.Use(middleware.UserAuth())
	selfChannelRoute.Use(selfChannelEnabledGuard())
	{
		selfChannelRoute.GET("/", controller.GetSelfChannels)
		selfChannelRoute.GET("/search", controller.SearchSelfChannels)
		// M2 免费源模板 + 免费额度（gin 静态段优先于 /:id 参数段）
		selfChannelRoute.GET("/free_templates", controller.GetFreeSourceTemplates)
		selfChannelRoute.POST("/from_template", controller.AddSelfChannelFromTemplate)
		selfChannelRoute.GET("/free_usage", controller.GetSelfFreeUsage)
		selfChannelRoute.GET("/:id", controller.GetSelfChannel)
		selfChannelRoute.POST("/", controller.AddSelfChannel)
		selfChannelRoute.PUT("/", controller.UpdateSelfChannel)
		selfChannelRoute.DELETE("/:id", controller.DeleteSelfChannel)
		selfChannelRoute.POST("/:id/status", controller.UpdateSelfChannelStatus)
		selfChannelRoute.POST("/test", controller.TestSelfChannel)
		selfChannelRoute.GET("/fetch_models/:id", controller.FetchSelfChannelModels)
		selfChannelRoute.POST("/fetch_models", controller.FetchSelfModels)
		selfChannelRoute.POST("/copy/:id", controller.CopySelfChannel)
		// M3 账号型渠道登录/导入（workbuddy OAuth 设备流 / trae 凭据导入）
		selfChannelRoute.POST("/oauth/start", controller.SelfOAuthStart)
		selfChannelRoute.GET("/oauth/poll", controller.SelfOAuthPoll)
		selfChannelRoute.POST("/import_trae", controller.SelfImportTrae)
	}
}

// selfChannelEnabledGuard 平台总开关：SelfChannelEnabled=false 时全组 404。
func selfChannelEnabledGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !operation_setting.SelfChannelEnabled {
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"message": "Not Found",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
