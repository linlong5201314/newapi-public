package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetMagpiePlugins exposes catalog import sources and host-local CLI commands.
// Reading or copying this catalog never installs plugins or authorizes accounts.
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
