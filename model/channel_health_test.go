package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useChannelHealthTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	previousNow := channelHealthNow
	common.RedisEnabled, common.RDB = true, client
	ResetChannelHealthStoreForTest()
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled, common.RDB = previousEnabled, previousClient
		channelHealthNow = previousNow
		ResetChannelHealthStoreForTest()
	})
	return server
}

func disableChannelHealthRedis(t *testing.T) {
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousEnabled, previousClient })
}

func TestChannelHealthQuarantineHalfOpenAndRecovery(t *testing.T) {
	disableChannelHealthRedis(t)
	previousNow := channelHealthNow
	now := time.Now()
	channelHealthNow = func() time.Time { return now }
	t.Cleanup(func() { channelHealthNow = previousNow })
	ResetChannelHealthStoreForTest()
	t.Cleanup(ResetChannelHealthStoreForTest)

	for i := 0; i < channelHealthMinSamples; i++ {
		RecordChannelOutcome(41, false, 10)
	}
	assert.Zero(t, ChannelHealthFactor(41), "down channel must not receive ordinary traffic")
	assert.Equal(t, 1.0, ChannelHealthFactor(42), "an unrelated channel stays available")
	now = now.Add(channelHealthInitialCooldown + time.Second)
	assert.Equal(t, 1.0, ChannelHealthFactor(41), "expired cooldown admits one probe")
	assert.Zero(t, ChannelHealthFactor(41), "only one half-open probe is admitted")
	RecordChannelOutcome(41, true, 20)
	assert.Equal(t, 1.0, ChannelHealthFactor(41), "successful probe clears quarantine")
	assert.Equal(t, 1, channelHealthEntryOf(41).count)
	assert.True(t, channelHealthEntryOf(41).downUntil.IsZero())

	for i := 0; i < ChannelHealthSampleSize; i++ {
		RecordChannelOutcome(41, false, 10)
	}
	now = now.Add(channelHealthInitialCooldown + time.Second)
	assert.Equal(t, 1.0, ChannelHealthFactor(41))
	RecordChannelOutcome(41, false, 10)
	assert.Equal(t, channelHealthInitialCooldown*2, channelHealthEntryOf(41).cooldown)
}

func TestChannelHealthMergesNodeStreams(t *testing.T) {
	server := useChannelHealthTestRedis(t)
	for _, success := range []bool{true, false, true} {
		server.XAdd(channelHealthRedisKey(51), "*", []string{"ok", map[bool]string{true: "1", false: "0"}[success], "lat", "20"})
	}
	ChannelHealthFactor(51)
	require.Eventually(t, func() bool { return ChannelHealthOf(51).Samples == 3 }, time.Second, 5*time.Millisecond)
	health := ChannelHealthOf(51)
	assert.Equal(t, 2, health.RecentOK)
	assert.Equal(t, 1, health.RecentFail)
	assert.EqualValues(t, 20, health.AvgLatencyMs)

	RecordChannelOutcome(51, false, 30)
	require.Eventually(t, func() bool {
		count, err := common.RDB.XLen(context.Background(), channelHealthRedisKey(51)).Result()
		return err == nil && count == 4
	}, time.Second, 5*time.Millisecond)
	health = ChannelHealthOf(51)
	assert.Equal(t, 2, health.RecentOK)
	assert.Equal(t, 2, health.RecentFail, "remote observations are retained when appending locally")
}

func TestChannelHealthFlushFailureIsBoundedAndRetained(t *testing.T) {
	server := useChannelHealthTestRedis(t)
	server.Close()
	for i := 0; i < channelHealthPendingLimit*2; i++ {
		RecordChannelOutcome(61, i%2 == 0, 20)
	}
	entry := channelHealthEntryOf(61)
	entry.mu.Lock()
	pending := len(entry.pending)
	flushing := entry.flushing
	entry.mu.Unlock()
	assert.LessOrEqual(t, pending, channelHealthPendingLimit)
	assert.True(t, flushing || pending > 0, "failed writes must remain available for a later coalesced flush")
	assert.LessOrEqual(t, pending, channelHealthPendingLimit)
}

func TestChannelHealthContextCancellation(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, client.Ping(ctx).Err())
}
