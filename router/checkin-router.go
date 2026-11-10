package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// registerCheckinRoutes 注册签到中心路由组 /api/checkin（M3）。
// 全部端点挂 UserAuth + owner 强制；平台开关 CheckinEnabled=false 时整组 404。
func registerCheckinRoutes(apiRouter *gin.RouterGroup) {
	checkinRoute := apiRouter.Group("/checkin")
	checkinRoute.Use(middleware.UserAuth())
	checkinRoute.Use(checkinEnabledGuard())
	{
		checkinRoute.GET("/sites", controller.GetCheckinSites)
		checkinRoute.GET("/accounts", controller.GetCheckinAccounts)
		checkinRoute.POST("/accounts", controller.AddCheckinAccount)
		checkinRoute.PUT("/accounts/:id", controller.UpdateCheckinAccount)
		checkinRoute.DELETE("/accounts/:id", controller.DeleteCheckinAccount)
		checkinRoute.POST("/accounts/:id/run", controller.RunCheckinAccount)
		checkinRoute.GET("/accounts/:id/logs", controller.GetCheckinAccountLogs)
	}
}

// checkinEnabledGuard 平台总开关：CheckinEnabled=false 时全组 404。
func checkinEnabledGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !operation_setting.CheckinEnabled {
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
