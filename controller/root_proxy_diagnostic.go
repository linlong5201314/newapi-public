package controller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
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
		if c.Query("probe") == "raccoon" {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://xiaohuanxiong.com/api/web/auth/v1/user_info", nil)
			client, clientErr := service.GetAccountAuthHTTPClient(proxy, "")
			if clientErr != nil {
				result["probe_error"] = clientErr.Error()
			} else {
				resp, probeErr := client.Do(req)
				if probeErr != nil {
					result["probe_error"] = probeErr.Error()
				} else {
					result["probe_status"] = resp.StatusCode
					resp.Body.Close()
				}
			}
		}
	}
	common.ApiSuccess(c, result)
}
