package controller

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

// DiagnoseRootProxyPool exposes only role and non-secret resolution diagnostics.
// It exists so a failed public-pool selection can be distinguished from a
// credential/upstream failure without consuming another one-time account grant.
func DiagnoseRootProxyPool(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "root only"})
		return
	}
	uid := c.GetInt("id")
	role := 0
	if user, err := model.GetUserById(uid, false); err == nil {
		role = user.Role
	}
	mode := c.DefaultQuery("mode", "public_pool")
	settings := dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: mode}}
	proxy, err := model.ResolveRootChannelProxyPolicy(uid, "diagnostic", settings)
	result := gin.H{"user_id": uid, "role": role, "mode": mode, "ok": err == nil}
	if err != nil {
		result["error"] = fmt.Sprintf("%T: %v", err, err)
	} else {
		result["proxy_prefix"] = proxy[:min(24, len(proxy))]
	}
	common.ApiSuccess(c, result)
}
