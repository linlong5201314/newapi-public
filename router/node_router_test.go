package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRegisterNodeRoutes 验证 /api/node 路由组注册无冲突（静态段与 :id 同层）。
func TestRegisterNodeRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api")
	registerNodeRoutes(group)

	expected := map[string]bool{
		"GET /api/node":                     false,
		"POST /api/node":                    false,
		"PUT /api/node":                     false,
		"PUT /api/node/:id":                 false,
		"DELETE /api/node/:id":              false,
		"POST /api/node/:id/check":          false,
		"POST /api/node/deploy/plan":        false,
		"POST /api/node/deploy/apply":       false,
		"POST /api/node/:id/failover/plan":  false,
		"POST /api/node/:id/failover/apply": false,
		"GET /api/node/settings":            false,
		"PUT /api/node/settings":            false,
		"GET /api/node/audit":               false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := expected[key]; ok {
			expected[key] = true
		}
	}
	for key, found := range expected {
		if !found {
			t.Errorf("expected route %s to be registered", key)
		}
	}
}
