package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// registerExtLinkRoutes 注册外链模块管理 API 组 /api/extlink（M4）。
//
// 蓝图 docs/M4-外链模块设计.md §2：写操作 RootAuth（带审计）；GET /enabled
// 对登录用户公开（首页卡片/侧栏/站内页数据源，仅返回 enabled 的）。
// ExtLinkEnabled=false 时整组 404（含 enabled 读口，防止关停后配置外泄）。
func registerExtLinkRoutes(apiRouter *gin.RouterGroup) {
	extLinkRoute := apiRouter.Group("/extlink")
	extLinkRoute.Use(extLinkEnabledGuard())
	{
		// 登录用户可读启用列表（静态段优先于 :id 匹配）。
		extLinkRoute.GET("/enabled", middleware.UserAuth(), controller.GetEnabledExtLinks)
		// 管理端 CRUD：RootAuth。
		admin := extLinkRoute.Group("")
		admin.Use(middleware.RootAuth())
		{
			admin.GET("/", controller.GetExtLinks)
			admin.GET("/:id", controller.GetExtLink)
			admin.POST("/", controller.AddExtLink)
			admin.PUT("/", controller.UpdateExtLink)
			admin.DELETE("/:id", controller.DeleteExtLink)
		}
	}
}

// SetAppProxyRouter 注册 /app/:slug 同域反代组（M4）。
// 必须在 SetWebRouter 的 NoRoute 之前挂到 engine 上，否则被前端 SPA 兜底吃掉。
// 免登录公开（一期统一可见）；全部 HTTP 方法都反代（目标站表单提交不跑丢）。
// assets.IndexPage 注入上下文：iframe/newtab 模式命中时回 SPA 外壳，
// 保证站内查看页 /app/<slug> 硬刷新可用（不反代目标站）。
func SetAppProxyRouter(router *gin.Engine, assets WebAssets) {
	appRoute := router.Group("/app")
	appRoute.Use(middleware.RouteTag("extlink_app"))
	appRoute.Use(func(c *gin.Context) {
		c.Set("ext_link_spa_index", assets.IndexPage)
		c.Next()
	})
	{
		appRoute.Any("/:slug", controller.ExtLinkAppProxy)
		appRoute.Any("/:slug/*path", controller.ExtLinkAppProxy)
	}
}

// extLinkEnabledGuard 平台总开关：ExtLinkEnabled=false 时全组 404。
func extLinkEnabledGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !operation_setting.ExtLinkEnabled {
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
