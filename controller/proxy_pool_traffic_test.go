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

func TestProxyPoolSummaryDatabaseFailureContract(t *testing.T) {
	old := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = old })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/proxy-pool/summary", nil)
	c.Set("id", 1)
	GetProxyPoolSummary(c)
	assert.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
		Message string         `json:"message"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.NotEmpty(t, response.Message)
	for _, key := range []string{"endpoint_total", "endpoint_enabled", "usable_url_count", "subscription_total", "client_node_count", "public_node_count", "haizhu_node_count", "kernel_loaded_count", "probed_healthy_count"} {
		value, present := response.Data[key]
		assert.True(t, present, key)
		assert.Equal(t, float64(0), value, key)
	}
}

func TestParseSubscriptionUserInfo(t *testing.T) {
	cases := []struct {
		header string
		used   int64
		total  int64
		hasExp bool
	}{
		{"upload=1024; download=2048; total=107374182400; expire=1798761600", 3072, 107374182400, true},
		{"upload=0;download=0;total=0;expire=0", 0, 0, false},
		{"", 0, 0, false},
		{"total=5368709120", 0, 5368709120, false},
	}
	for _, c := range cases {
		got := parseSubscriptionUserInfo(c.header)
		if got.Used != c.used || got.Total != c.total {
			t.Fatalf("header %q: used=%d total=%d, want used=%d total=%d", c.header, got.Used, got.Total, c.used, c.total)
		}
		if (got.ExpireAt != nil) != c.hasExp {
			t.Fatalf("header %q: expire presence=%v, want %v", c.header, got.ExpireAt != nil, c.hasExp)
		}
	}
}
