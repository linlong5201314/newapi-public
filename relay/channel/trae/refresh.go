// refresh.go Trae token 刷新（ExchangeToken 整族轮换）单飞 + 写回 channels.key。
// 注意：ExchangeToken 会踢同账号其他客户端下线——渠道表单必须向用户明示。
package trae

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// refreshSkew 请求前刷新判定窗口（提前 10 分钟视为将过期）。
const refreshSkew = 10 * time.Minute

// refreshFlight 同渠道一次 refresh 的单飞票据。
type refreshFlight struct {
	done chan struct{}
	cred *Cred
	err  error
}

var (
	refreshMu       sync.Mutex
	refreshInFlight = map[int]*refreshFlight{}

	channelCredLocks sync.Map // map[int]*sync.Mutex
)

// ChannelLock 取渠道级互斥锁（DB 写回串行）。
func ChannelLock(channelId int) *sync.Mutex {
	l, _ := channelCredLocks.LoadOrStore(channelId, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// RefreshIfNeeded 请求前调用：凭据将在 10min 内过期时刷新并写回。
func RefreshIfNeeded(channelId int, cred *Cred) (*Cred, error) {
	if !cred.NeedsRefresh(refreshSkew) {
		return cred, nil
	}
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("trae credential expired and has no refreshToken（请重新导入凭据）")
	}
	return refreshSingleflight(channelId, cred)
}

func refreshSingleflight(channelId int, cred *Cred) (*Cred, error) {
	key := channelId
	if key <= 0 {
		key = -int(hashString(cred.UID + "|" + cred.RefreshToken))
		if key == 0 {
			key = -1
		}
	}
	refreshMu.Lock()
	if f, ok := refreshInFlight[key]; ok {
		refreshMu.Unlock()
		<-f.done
		return f.cred, f.err
	}
	f := &refreshFlight{done: make(chan struct{})}
	refreshInFlight[key] = f
	refreshMu.Unlock()

	newCred, err := refreshWithLock(channelId, cred)

	f.cred, f.err = newCred, err
	close(f.done)
	refreshMu.Lock()
	delete(refreshInFlight, key)
	refreshMu.Unlock()
	return newCred, err
}

func refreshWithLock(channelId int, cred *Cred) (*Cred, error) {
	if channelId > 0 {
		lock := ChannelLock(channelId)
		lock.Lock()
		defer lock.Unlock()
		if latest, err := LoadChannelCred(channelId); err == nil && !latest.NeedsRefresh(refreshSkew) {
			return latest, nil
		} else if err == nil {
			cred = latest
		}
	}
	if common.RedisEnabled && channelId > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		lockKey := fmt.Sprintf("trae:refresh:%d", channelId)
		acquired, err := common.RDB.SetNX(ctx, lockKey, 1, 45*time.Second).Result()
		if err == nil && acquired {
			defer common.RDB.Del(ctx, lockKey)
			return doRefreshAndSave(channelId, cred)
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(800 * time.Millisecond)
			if latest, lerr := LoadChannelCred(channelId); lerr == nil && !latest.NeedsRefresh(refreshSkew) {
				return latest, nil
			}
		}
	}
	return doRefreshAndSave(channelId, cred)
}

func doRefreshAndSave(channelId int, cred *Cred) (*Cred, error) {
	newCred, err := NewClient(cred).refreshTokenOnce()
	if err != nil {
		return nil, err
	}
	if channelId > 0 {
		if err := SaveCred(channelId, newCred); err != nil {
			common.SysLog("trae refresh persist failed: " + err.Error())
			return nil, err
		}
	}
	return newCred, nil
}

// SaveCred 把刷新后的凭据写回 channels.key 并使渠道缓存失效。
// M5：写回前加密（Trae 属账号型渠道，CRYPTO_SECRET 派生 AES-GCM）。
func SaveCred(channelId int, cred *Cred) error {
	raw := cred.Marshal()
	if raw == "" {
		return fmt.Errorf("trae credential marshal failed")
	}
	raw = model.EncryptChannelKey(constant.ChannelTypeTrae, raw)
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channelId).Update("key", raw).Error; err != nil {
		return fmt.Errorf("trae credential persist: %w", err)
	}
	if common.MemoryCacheEnabled {
		if ch, err := model.GetChannelById(channelId, true); err == nil {
			model.CacheUpdateChannel(ch)
		}
	}
	return nil
}

// LoadChannelCred 从渠道记录读出凭据。
func LoadChannelCred(channelId int) (*Cred, error) {
	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		return nil, err
	}
	key := model.DecryptChannelKey(ch.Type, ch.Key)
	if ch.ChannelInfo.IsMultiKey {
		keys := ch.GetKeys()
		if len(keys) > 0 {
			key = keys[0]
		}
	}
	return ParseCred(key)
}

func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
