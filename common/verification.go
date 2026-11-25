package common

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type verificationValue struct {
	code string
	time time.Time
}

const (
	EmailVerificationPurpose = "v"
	PasswordResetPurpose     = "r"
)

var verificationMutex sync.Mutex
var verificationMap map[string]verificationValue
var verificationMapMaxSize = 1000
var VerificationValidMinutes = 10
var VerificationMaxAttempts = 5

const consumeVerificationScript = `
local stored = redis.call('GET', KEYS[1])
if not stored then return 0 end
local attempts = redis.call('INCR', KEYS[2])
redis.call('EXPIRE', KEYS[2], ARGV[2])
if attempts > tonumber(ARGV[3]) then return -1 end
if stored ~= ARGV[1] then return -1 end
redis.call('DEL', KEYS[1], KEYS[2])
return 1`

func verificationRedisKey(key string, purpose string) string {
	digest := sha256.Sum256([]byte(purpose + ":" + key))
	return "verification:" + hex.EncodeToString(digest[:])
}

func verificationCodeDigest(code string) string {
	digest := sha256.Sum256([]byte(code))
	return hex.EncodeToString(digest[:])
}

func randomVerificationSalt() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return uuid.NewString()
	}
	return hex.EncodeToString(raw[:])
}

func GenerateVerificationCode(length int) string {
	code := uuid.New().String()
	code = strings.Replace(code, "-", "", -1)
	if length == 0 {
		return code
	}
	return code[:length]
}

func StoreVerificationCodeWithKey(key string, code string, purpose string) error {
	if RedisEnabled {
		if RDB == nil {
			return fmt.Errorf("verification storage is unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := RDB.Set(ctx, verificationRedisKey(key, purpose), verificationCodeDigest(randomVerificationSalt()+code), time.Duration(VerificationValidMinutes)*time.Minute).Result()
		return err
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap[purpose+key] = verificationValue{
		code: code,
		time: time.Now(),
	}
	if len(verificationMap) > verificationMapMaxSize {
		removeExpiredPairs()
		if len(verificationMap) > verificationMapMaxSize {
			for key := range verificationMap {
				delete(verificationMap, key)
				break
			}
		}
	}
	return nil
}

func VerifyCodeWithKey(key string, code string, purpose string) bool {
	if RedisEnabled && RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stored, err := RDB.Get(ctx, verificationRedisKey(key, purpose)).Result()
		if err != nil || stored == "" {
			return false
		}
		result, err := RDB.Eval(ctx, consumeVerificationScript, []string{verificationRedisKey(key, purpose), verificationRedisKey(key, purpose) + ":attempts"}, stored, int(time.Duration(VerificationValidMinutes)*time.Minute/time.Second), VerificationMaxAttempts).Int()
		return err == nil && result == 1
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	value, okay := verificationMap[purpose+key]
	now := time.Now()
	if !okay || int(now.Sub(value.time).Seconds()) >= VerificationValidMinutes*60 {
		return false
	}
	return code == value.code
}

func DeleteKey(key string, purpose string) {
	if RedisEnabled && RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = RDB.Del(ctx, verificationRedisKey(key, purpose), verificationRedisKey(key, purpose)+":attempts").Err()
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationMap, purpose+key)
}

// no lock inside, so the caller must lock the verificationMap before calling!
func removeExpiredPairs() {
	now := time.Now()
	for key := range verificationMap {
		if int(now.Sub(verificationMap[key].time).Seconds()) >= VerificationValidMinutes*60 {
			delete(verificationMap, key)
		}
	}
}

func init() {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap = make(map[string]verificationValue)
}
