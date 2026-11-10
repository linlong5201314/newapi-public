// store.go plan 缓存：10 分钟有效 planId。Redis 可用时跨节点共享，否则进程内。
package nodedeploy

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// PlanTTL plan 有效期（蓝图 §7：10 分钟内有效）。
const PlanTTL = 10 * time.Minute

const planRedisPrefix = "node:plan:"

type planKind string

const (
	planKindDeploy   planKind = "deploy"
	planKindFailover planKind = "failover"
)

type planEnvelope struct {
	Kind      planKind        `json:"kind"`
	ExpiresAt int64           `json:"expiresAt"`
	Payload   json.RawMessage `json:"payload"`
}

// Store plan 缓存（Redis 优先 + 进程内兜底）。
type Store struct {
	mu  sync.Mutex
	mem map[string]planEnvelope
}

// DefaultStore 进程级默认缓存。
var DefaultStore = NewStore()

// NewStore 创建缓存。
func NewStore() *Store {
	return &Store{mem: make(map[string]planEnvelope)}
}

func (s *Store) put(kind planKind, planId string, expiresAt int64, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	envelope := planEnvelope{Kind: kind, ExpiresAt: expiresAt, Payload: encoded}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if common.RedisEnabled && common.RDB != nil {
		ttl := time.Until(time.Unix(expiresAt, 0))
		if ttl <= 0 {
			ttl = PlanTTL
		}
		return common.RedisSet(planRedisPrefix+planId, string(raw), ttl)
	}
	s.mu.Lock()
	s.mem[planId] = envelope
	s.mu.Unlock()
	return nil
}

func (s *Store) get(kind planKind, planId string, out any) error {
	var envelope planEnvelope
	found := false
	if common.RedisEnabled && common.RDB != nil {
		if raw, err := common.RedisGet(planRedisPrefix + planId); err == nil {
			if json.Unmarshal([]byte(raw), &envelope) == nil {
				found = true
			}
		}
	}
	if !found {
		s.mu.Lock()
		envelope, found = s.mem[planId]
		s.mu.Unlock()
	}
	if !found {
		return ErrPlanExpired
	}
	if envelope.ExpiresAt > 0 && time.Now().Unix() > envelope.ExpiresAt {
		s.Delete(planId)
		return ErrPlanExpired
	}
	if envelope.Kind != kind {
		return ErrPlanExpired
	}
	return json.Unmarshal(envelope.Payload, out)
}

// Delete 删除 plan（apply 后一次性消费）。
func (s *Store) Delete(planId string) {
	if common.RedisEnabled && common.RDB != nil {
		_ = common.RedisDel(planRedisPrefix + planId)
	}
	s.mu.Lock()
	delete(s.mem, planId)
	s.mu.Unlock()
}

// PutDeploy 缓存部署 plan。
func (s *Store) PutDeploy(plan *Plan) error {
	return s.put(planKindDeploy, plan.PlanId, plan.ExpiresAt, plan)
}

// PutFailover 缓存 failover plan。
func (s *Store) PutFailover(plan *FailoverPlan) error {
	return s.put(planKindFailover, plan.PlanId, plan.ExpiresAt, plan)
}

// GetDeploy 读取部署 plan。
func (s *Store) GetDeploy(planId string) (*Plan, error) {
	var plan Plan
	if err := s.get(planKindDeploy, planId, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// GetFailover 读取 failover plan。
func (s *Store) GetFailover(planId string) (*FailoverPlan, error) {
	var plan FailoverPlan
	if err := s.get(planKindFailover, planId, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
