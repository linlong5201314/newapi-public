package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMagpiePluginImportContract(t *testing.T) {
	t.Run("GitHub plugins use a supported explicit source", func(t *testing.T) {
		items := model.ListMagpiePlugins("MYCF711/magpie-marvis")
		require.Len(t, items, 1)
		assert.Equal(t, "magpie plugin add github:MYCF711/magpie-marvis", items[0].Install)
		assert.Equal(t, "https://github.com/MYCF711/magpie-marvis", items[0].ImportSource)
	})
	t.Run("scoped npm imports stay scoped and independent from caller mutation", func(t *testing.T) {
		items := model.ListMagpiePlugins("@magpie-community/opencode-orcaterm-auth")
		require.Len(t, items, 1)
		assert.Equal(t, "@magpie-community/opencode-orcaterm-auth", items[0].ImportSource)
		assert.Equal(t, "magpie plugin add @magpie-community/opencode-orcaterm-auth", items[0].Install)
		items[0].Install = "modified"
		assert.NotEqual(t, "modified", model.ListMagpiePlugins("orcaterm")[0].Install)
	})
	t.Run("unverified source does not offer a nonfunctional install command", func(t *testing.T) {
		items := model.ListMagpiePlugins("opencode-2-free")
		require.Len(t, items, 1)
		assert.Empty(t, items[0].Install)
		assert.Empty(t, items[0].ImportSource)
	})
	t.Run("empty filtered API response remains a JSON list", func(t *testing.T) {
		r := gin.New()
		r.GET("/plugins", GetMagpiePlugins)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plugins?q=does-not-exist", nil))
		require.Equal(t, http.StatusOK, w.Code)
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Items []model.MagpiePlugin `json:"items"`
				Total int                  `json:"total"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		assert.True(t, response.Success)
		assert.NotNil(t, response.Data.Items)
		assert.Zero(t, response.Data.Total)
	})
}
