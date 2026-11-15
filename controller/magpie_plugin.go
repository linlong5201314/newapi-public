package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetMagpiePlugins 返回精选的 magpie provider 插件目录，供前端展示与一键复制安装命令。
func GetMagpiePlugins(c *gin.Context) {
	items := model.ListMagpiePlugins(c.Query("q"))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":    items,
			"total":    len(items),
			"upstream": "https://github.com/topics/magpie-plugin",
		},
	})
}
