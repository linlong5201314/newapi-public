package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

func TestChannelCacheEventReachesSecondInstance(t *testing.T) {
	server := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: server.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })
	previousRDB, previousEnabled := common.RDB, common.RedisEnabled
	common.RedisEnabled = true
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		common.RDB, common.RedisEnabled = previousRDB, previousEnabled
	})
	sub := clientB.Subscribe(ctx, channelCacheEventChannel)
	t.Cleanup(func() { _ = sub.Close() })
	common.RDB = clientA
	publishChannelCacheChange()
	select {
	case <-sub.Channel():
	case <-time.After(2 * time.Second):
		t.Fatal("second instance did not receive channel cache invalidation")
	}
}
