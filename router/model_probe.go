package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterModelProbeRoutes is called by the API router. Model tests are explicit
// POSTs, protected by dashboard auth, the feature switch and per-user admission.
func RegisterModelProbeRoutes(apiRouter *gin.RouterGroup) {
	probes := apiRouter.Group("/channel/self/model_probe")
	probes.Use(middleware.UserAuth(), selfChannelEnabledGuard())
	probes.GET("/:id", controller.GetSelfModelProbe)
	probes.POST("/:id", middleware.UserCriticalRateLimit("model_endpoint_probe"), controller.PostSelfModelProbe)
}
