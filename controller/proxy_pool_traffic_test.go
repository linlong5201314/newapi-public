package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

func TestProxyEndpointPaginationContract(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	old := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = old; _ = sqlDB.Close() })
	require.NoError(t, model.EnsureProxyPoolSchema())
	rows := []model.ProxyEndpoint{
		{UserId: 1, Name: "own", Kind: "http", Url: "http://fixture:secret@127.0.0.1:1", Enabled: true},
		{UserId: 2, Name: "other private", Kind: "http", Url: "http://127.0.0.1:2", Enabled: true},
		{UserId: 2, Name: "public", Kind: "http", Url: "http://fixture:secret@127.0.0.1:3", Enabled: true, Public: true},
	}
	require.NoError(t, db.Create(&rows).Error)
	for _, tc := range []struct {
		query         string
		ids           []int
		offset, limit int
		more, invalid bool
	}{
		{ids: []int{rows[0].Id, rows[2].Id}, limit: 600},
		{query: "?limit=1", ids: []int{rows[0].Id}, limit: 1, more: true},
		{query: "?offset=1&limit=1", ids: []int{rows[2].Id}, offset: 1, limit: 1},
		{query: "?offset=2&limit=1", ids: []int{}, offset: 2, limit: 1},
		{query: "?offset=-1", invalid: true},
		{query: "?offset=bad", invalid: true},
		{query: "?limit=0", invalid: true},
		{query: "?limit=601", invalid: true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("id", 1)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/proxy-pool/endpoints"+tc.query, nil)
			GetProxyEndpoints(c)
			var response struct {
				Success bool `json:"success"`
				Data    []struct {
					Id int `json:"id"`
				} `json:"data"`
				Pagination struct {
					Offset   int  `json:"offset"`
					Limit    int  `json:"limit"`
					Returned int  `json:"returned"`
					HasMore  bool `json:"has_more"`
				} `json:"pagination"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, !tc.invalid, response.Success)
			if tc.invalid {
				return
			}
			ids := make([]int, 0, len(response.Data))
			for _, row := range response.Data {
				ids = append(ids, row.Id)
			}
			assert.Equal(t, tc.ids, ids)
			assert.Equal(t, tc.offset, response.Pagination.Offset)
			assert.Equal(t, tc.limit, response.Pagination.Limit)
			assert.Equal(t, len(tc.ids), response.Pagination.Returned)
			assert.Equal(t, tc.more, response.Pagination.HasMore)
			assert.NotContains(t, w.Body.String(), "secret")
			assert.NotContains(t, w.Body.String(), "other private")
		})
	}
}
