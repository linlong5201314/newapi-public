// extlink_proxy.go M4 /app/:slug 同域反代入口（蓝图 docs/M4-外链模块设计.md §2）。
//
// 免登录公开（一期统一可见，与蓝图一致）。slug 不存在/disabled/总开关关闭一律
// 404——不反代、不泄露配置。mode=iframe/newtab 不反代，但命中时回 SPA 外壳，
// 让前端站内查看页（/_authenticated/app/$slug）在硬刷新时正常渲染。
package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	extlinksvc "github.com/QuantumNous/new-api/service/extlink"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// ExtLinkAppProxy GET/POST/... /app/:slug 与 /app/:slug/*path。
func ExtLinkAppProxy(c *gin.Context) {
	if !operation_setting.ExtLinkEnabled {
		extLinkNotFound(c)
		return
	}
	link, err := model.GetExtLinkBySlugCached(c.Param("slug"))
	if err != nil {
		common.SysError(common.LogText("extlink slug lookup failed: %s", err.Error()))
		extLinkNotFound(c)
		return
	}
	if link == nil {
		extLinkNotFound(c)
		return
	}
	if link.Mode != model.ExtLinkModeProxy {
		// iframe/newtab 模式不反代：只回 SPA 外壳，交给前端站内查看页处理。
		if fallback, ok := c.Get("ext_link_spa_index"); ok {
			if index, ok := fallback.([]byte); ok && len(index) > 0 {
				c.Header("Cache-Control", "no-cache")
				c.Data(http.StatusOK, "text/html; charset=utf-8", index)
				return
			}
		}
		extLinkNotFound(c)
		return
	}
	proxy, err := extlinksvc.NewReverseProxy(link)
	if err != nil {
		common.SysError(common.LogText("extlink proxy build failed: %s", err.Error()))
		extLinkNotFound(c)
		return
	}
	// 剥站内前缀：/app/<slug>/foo/bar → /foo/bar（目标 base path 由 SetURL 拼接）。
	if wild := c.Param("path"); wild != "" {
		c.Request.URL.Path = wild
	} else {
		c.Request.URL.Path = "/"
	}
	c.Request.URL.RawPath = ""
	proxy.ServeHTTP(c.Writer, c.Request)
}

func extLinkNotFound(c *gin.Context) {
	accept := c.GetHeader("Accept")
	if strings.Contains(accept, "text/html") {
		c.Data(http.StatusNotFound, "text/html; charset=utf-8",
			[]byte("<html><body><h3>404 - app not found or not proxied</h3></body></html>"))
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Not Found"})
}
