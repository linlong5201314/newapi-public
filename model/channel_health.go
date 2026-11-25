package model

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// Channel health is a routing hint, not a durable ledger. Each observation is
// appended to a bounded Redis stream so independent nodes cannot overwrite each
// other's recent samples. Local routing never waits for Redis.
const (
	ChannelHealthSampleSize      = 5
	channelHealthRedisTTL        = time.Hour
	channelHealthCacheTTL        = 15 * time.Second
	channelHealthMinSamples      = 3
	channelHealthEventMaxLen     = 1000
	channelHealthPendingLimit    = 256
	channelHealthInitialCooldown = 30 * time.Second
	channelHealthMaxCooldown     = 5 * time.Minute
)

type ChannelHealth struct {
	ChannelId    int     `json:"channel_id"`
	Samples      int     `json:"samples"`
	RecentOK     int     `json:"recent_ok"`
	RecentFail   int     `json:"recent_fail"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	Factor       float64 `json:"factor"`
	State        string  `json:"state"`
}

type channelHealthObservation struct {
	success   bool
	latencyMs int64
}

type channelHealthEntry struct {
	mu            sync.Mutex
	recent        [ChannelHealthSampleSize]bool
	count         int
	next          int
	latSum        int64
	latCount      int
	loadedAt      time.Time
	hydrating     bool
	lastEventID   string
	pending       []channelHealthObservation
	flushing      bool
	flushCancel   context.CancelFunc
	flushDone     chan struct{}
	downUntil     time.Time
	cooldown      time.Duration
	probeInFlight bool
	probeToken    string
	probeStarted  time.Time
}

var (
	channelHealthMu    sync.RWMutex
	channelHealthStore = make(map[int]*channelHealthEntry)
	channelHealthNow   = time.Now
)

func ResetChannelHealthStoreForTest() {
	channelHealthMu.Lock()
	entries := make([]*channelHealthEntry, 0, len(channelHealthStore))
	for _, entry := range channelHealthStore {
		entries = append(entries, entry)
	}
	channelHealthStore = make(map[int]*channelHealthEntry)
	channelHealthMu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		cancel := entry.flushCancel
		done := entry.flushDone
		entry.flushCancel, entry.flushDone = nil, nil
		entry.pending = nil
		entry.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	}
}

func channelHealthEntryOf(id int) *channelHealthEntry {
	channelHealthMu.RLock()
	entry := channelHealthStore[id]
	channelHealthMu.RUnlock()
	if entry != nil {
		return entry
	}
	channelHealthMu.Lock()
	defer channelHealthMu.Unlock()
	if entry = channelHealthStore[id]; entry == nil {
		entry = &channelHealthEntry{}
		channelHealthStore[id] = entry
	}
	return entry
}

func (entry *channelHealthEntry) appendLocked(success bool, latencyMs int64) {
	entry.recent[entry.next] = success
	entry.next = (entry.next + 1) % ChannelHealthSampleSize
	if entry.count < ChannelHealthSampleSize {
		entry.count++
	}
	if success && latencyMs > 0 {
		entry.latSum += latencyMs
		entry.latCount++
	}
}

func RecordChannelOutcome(channelId int, success bool, latencyMs int64) {
	if channelId <= 0 {
		return
	}
	entry := channelHealthEntryOf(channelId)
	entry.mu.Lock()
	now := channelHealthNow()
	if entry.probeInFlight || !entry.downUntil.IsZero() {
		probeToken := entry.probeToken
		entry.probeInFlight = false
		entry.probeToken = ""
		if probeToken != "" && common.RedisEnabled && common.RDB != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_ = common.RDB.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{"chstat:probe:" + strconv.Itoa(channelId)}, probeToken).Err()
			cancel()
		}
		if success {
			entry.downUntil = time.Time{}
			entry.cooldown = 0
			entry.recent = [ChannelHealthSampleSize]bool{}
			entry.count, entry.next = 0, 0
			entry.latSum, entry.latCount = 0, 0
		} else {
			if entry.cooldown == 0 {
				entry.cooldown = channelHealthInitialCooldown
			} else {
				entry.cooldown = min(entry.cooldown*2, channelHealthMaxCooldown)
			}
			entry.downUntil = now.Add(entry.cooldown)
		}
	}
	entry.appendLocked(success, latencyMs)
	if entry.count >= channelHealthMinSamples {
		ok := 0
		for i := 0; i < entry.count; i++ {
			if entry.recent[i] {
				ok++
			}
		}
		if ok == 0 && entry.downUntil.IsZero() {
			entry.cooldown = channelHealthInitialCooldown
			entry.downUntil = now.Add(entry.cooldown)
		}
	}
	if len(entry.pending) < channelHealthPendingLimit {
		entry.pending = append(entry.pending, channelHealthObservation{success: success, latencyMs: latencyMs})
	} else {
		// Backpressure retains the most recent routing evidence, rather than
		// spawning an unbounded goroutine for every request during Redis failure.
		copy(entry.pending, entry.pending[1:])
		entry.pending[len(entry.pending)-1] = channelHealthObservation{success: success, latencyMs: latencyMs}
	}
	start := !entry.flushing && common.RedisEnabled && common.RDB != nil
	workerCtx := context.Background()
	var cancel context.CancelFunc
	if start {
		entry.flushing = true
		workerCtx, cancel = context.WithCancel(context.Background())
		entry.flushCancel = cancel
		entry.flushDone = make(chan struct{})
	}
	entry.mu.Unlock()
	if start {
		go flushChannelHealth(channelId, entry, workerCtx)
	}
}

func channelHealthRedisKey(channelId int) string {
	return "chstat:v2:" + strconv.Itoa(channelId)
}

func flushChannelHealth(channelId int, entry *channelHealthEntry, workerCtx context.Context) {
	done := entry.flushDone
	defer close(done)
	defer func() {
		entry.mu.Lock()
		entry.flushing = false
		entry.flushCancel, entry.flushDone = nil, nil
		entry.mu.Unlock()
	}()
	for {
		entry.mu.Lock()
		if len(entry.pending) == 0 {
			entry.mu.Unlock()
			return
		}
		batch := append([]channelHealthObservation(nil), entry.pending...)
		entry.mu.Unlock()
		if !common.RedisEnabled || common.RDB == nil {
			return
		}
		ctx, cancel := context.WithTimeout(workerCtx, 2*time.Second)
		pipe := common.RDB.TxPipeline()
		for _, observation := range batch {
			ok, latency := "0", "0"
			if observation.success {
				ok = "1"
				if observation.latencyMs > 0 {
					latency = strconv.FormatInt(observation.latencyMs, 10)
				}
			}
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: channelHealthRedisKey(channelId), MaxLen: channelHealthEventMaxLen, Approx: true, Values: map[string]any{"ok": ok, "lat": latency}})
		}
		pipe.Expire(ctx, channelHealthRedisKey(channelId), channelHealthRedisTTL)
		_, err := pipe.Exec(ctx)
		cancel()
		entry.mu.Lock()
		if err == nil {
			entry.pending = entry.pending[len(batch):]
		}
		if len(entry.pending) == 0 {
			entry.mu.Unlock()
			return
		}
		entry.mu.Unlock()
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-workerCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// scheduleChannelHealthHydration coalesces asynchronous reads. It is never
// called while holding channelSyncLock or entry.mu.
func scheduleChannelHealthHydration(channelId int, entry *channelHealthEntry) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	entry.mu.Lock()
	if entry.hydrating || !entry.loadedAt.IsZero() && channelHealthNow().Sub(entry.loadedAt) < channelHealthCacheTTL {
		entry.mu.Unlock()
		return
	}
	entry.hydrating = true
	entry.mu.Unlock()
	go hydrateChannelHealth(channelId, entry)
}

func hydrateChannelHealth(channelId int, entry *channelHealthEntry) {
	entry.mu.Lock()
	lastID := entry.lastEventID
	if lastID == "" {
		lastID = "0"
	}
	entry.mu.Unlock()
	if !common.RedisEnabled || common.RDB == nil {
		entry.mu.Lock()
		entry.hydrating = false
		entry.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	events, err := common.RDB.XRangeN(ctx, channelHealthRedisKey(channelId), lastID, "+", 500).Result()
	cancel()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.hydrating = false
	entry.loadedAt = channelHealthNow()
	if err != nil {
		return
	}
	for _, event := range events {
		if event.ID == lastID {
			continue
		}
		entry.appendLocked(event.Values["ok"] == "1", parseHealthLatency(event.Values["lat"]))
		entry.lastEventID = event.ID
	}
}

func parseHealthLatency(value any) int64 {
	switch value := value.(type) {
	case string:
		result, _ := strconv.ParseInt(value, 10, 64)
		return result
	case int64:
		return value
	default:
		return 0
	}
}

func (entry *channelHealthEntry) factorLocked(channelId int, now time.Time, reserveProbe bool) float64 {
	if !entry.downUntil.IsZero() {
		if now.Before(entry.downUntil) {
			return 0
		}
		if entry.probeInFlight {
			return 0
		}
		if reserveProbe {
			if common.RedisEnabled && common.RDB != nil {
				probeToken := common.GetUUID()
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				acquired, err := common.RDB.SetNX(ctx, "chstat:probe:"+strconv.Itoa(channelId), probeToken, 30*time.Second).Result()
				cancel()
				if err != nil {
					// Redis coordination outage must not permanently quarantine a
					// channel. Local probe reservation remains the fallback.
				} else if !acquired {
					return 0
				}
				entry.probeToken = probeToken
			}
			entry.probeStarted = now
			entry.probeInFlight = true
		}
		return 1
	}
	if entry.count < channelHealthMinSamples {
		return 1
	}
	okCount := 0
	for i := 0; i < entry.count; i++ {
		if entry.recent[i] {
			okCount++
		}
	}
	if okCount == 0 {
		return 0
	}
	rate := float64(okCount) / float64(entry.count) * 100
	switch {
	case rate < 60:
		return 0.35
	case rate < 90:
		return 0.7
	default:
		return 1
	}
}

// channelHealthCandidates excludes quarantined channels before any priority or
// weight comparison. A single candidate remains available for direct recovery.
func channelHealthCandidates(ids []int) []int {
	if len(ids) <= 1 {
		return ids
	}
	available := make([]int, 0, len(ids))
	for _, id := range ids {
		if ChannelHealthFactor(id) > 0 {
			available = append(available, id)
		}
	}
	return available
}

func ChannelHealthOf(channelId int) ChannelHealth {
	if channelId <= 0 {
		return ChannelHealth{State: "unknown", Factor: 1}
	}
	entry := channelHealthEntryOf(channelId)
	// Run the coalesced scheduler after releasing the cache read lock. Redis
	// hydration remains asynchronous, but lock order must not depend on a future
	// scheduler implementation.
	defer scheduleChannelHealthHydration(channelId, entry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	okCount := 0
	for i := 0; i < entry.count; i++ {
		if entry.recent[i] {
			okCount++
		}
	}
	health := ChannelHealth{ChannelId: channelId, Samples: entry.count, RecentOK: okCount, RecentFail: entry.count - okCount, State: "unknown", Factor: 1}
	if entry.count > 0 {
		health.SuccessRate = float64(okCount) / float64(entry.count) * 100
	}
	if entry.latCount > 0 {
		health.AvgLatencyMs = entry.latSum / int64(entry.latCount)
	}
	if !entry.downUntil.IsZero() {
		health.State = "down"
		if channelHealthNow().Before(entry.downUntil) || entry.probeInFlight {
			health.Factor = 0
		}
		return health
	}
	if entry.count < channelHealthMinSamples {
		return health
	}
	health.Factor = entry.factorLocked(channelId, channelHealthNow(), false)
	switch {
	case health.Factor == 0:
		health.State = "down"
	case health.SuccessRate < 90:
		health.State = "degraded"
	default:
		health.State = "healthy"
	}
	return health
}

func ChannelHealthBatch(channelIds []int) map[int]ChannelHealth {
	result := make(map[int]ChannelHealth, len(channelIds))
	for _, id := range channelIds {
		if id > 0 {
			result[id] = ChannelHealthOf(id)
		}
	}
	return result
}

func channelHealthFactor(channelId int, reserveProbe bool) float64 {
	if channelId <= 0 {
		return 1
	}
	entry := channelHealthEntryOf(channelId)
	// Run the coalesced scheduler after releasing the cache read lock. Redis
	// hydration remains asynchronous, but lock order must not depend on a future
	// scheduler implementation.
	defer scheduleChannelHealthHydration(channelId, entry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.factorLocked(channelId, channelHealthNow(), reserveProbe)
}

func ChannelHealthFactor(channelId int) float64 {
	return channelHealthFactor(channelId, true)
}

func ChannelHealthFactorNoReservation(channelId int) float64 {
	return channelHealthFactor(channelId, false)
}

// ResetChannelHealthForTest restores health after an explicit operator enable
// or edit, allowing a fresh observation rather than a stale quarantine window.
func ResetChannelHealth(channelId int) {
	entry := channelHealthEntryOf(channelId)
	entry.mu.Lock()
	entry.downUntil = time.Time{}
	entry.cooldown = 0
	entry.probeInFlight = false
	entry.recent = [ChannelHealthSampleSize]bool{}
	entry.count, entry.next = 0, 0
	entry.mu.Unlock()
}
