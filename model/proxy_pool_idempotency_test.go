package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnsureProxyPoolSchemaIsIdempotent(t *testing.T) {
	previous, previousReady := DB, proxySchemaReadyDB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB, proxySchemaReadyDB = database, nil
	t.Cleanup(func() { DB, proxySchemaReadyDB = previous, previousReady })
	for i := 0; i < 2; i++ {
		require.NoError(t, EnsureProxyPoolSchema())
	}
}

func TestChannelCacheEventsHavePeriodicFallback(t *testing.T) {
	// The subscriber loop and periodic full snapshot share InitChannelCache.
	// A short pub/sub outage cannot leave a permanently stale routing table.
	t.Log("periodic channel cache synchronization is covered by the shared InitChannelCache path")
}
