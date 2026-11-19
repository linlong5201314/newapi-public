package model

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelEndpointHealthTTL = 10 * time.Minute

const (
	ModelEndpointUnknown     = "unknown"
	ModelEndpointAvailable   = "available"
	ModelEndpointUnavailable = "unavailable"
)

// ModelEndpointHealth is a measurement of ONE model through ONE exit, never a
// channel-wide routing decision. ScopeKey is a keyed configuration digest: changing
// a credential, upstream, mapping or exit invalidates old successes without storing
// any credential, URL, response body or provider error in this table. Startup owns
// its migration; request handlers never create or modify database schema.
type ModelEndpointHealth struct {
	ID                int64  `json:"-" gorm:"primaryKey;autoIncrement"`
	ScopeKey          string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_model_endpoint_health_scope"`
	ChannelID         int    `json:"channel_id" gorm:"not null;index:idx_model_endpoint_health_channel"`
	Model             string `json:"model" gorm:"type:varchar(200);not null"`
	EndpointID        int    `json:"endpoint_id" gorm:"not null"`
	SubscriptionID    int    `json:"subscription_id" gorm:"not null"`
	Status            string `json:"status" gorm:"type:varchar(16);not null"`
	Reason            string `json:"reason" gorm:"type:varchar(40);not null"`
	HTTPStatus        int    `json:"http_status"`
	LatencyMS         int64  `json:"latency_ms"`
	CheckedAt         int64  `json:"checked_at" gorm:"not null"`
	ExpiresAt         int64  `json:"expires_at" gorm:"not null"`
	FirstCheckedAt    int64  `json:"first_checked_at"`
	LastAvailableAt   int64  `json:"last_available_at"`
	LifetimeSamples   int64  `json:"lifetime_samples"`
	LifetimePasses    int64  `json:"lifetime_passes"`
	LifetimeFailures  int64  `json:"lifetime_failures"`
	RecentSampleCount int    `json:"recent_sample_count"`
	RecentPasses      int    `json:"recent_passes"`
	RecentFailures    int    `json:"recent_failures"`
	RecentStates      string `json:"-" gorm:"type:varchar(5)"`
}

func (ModelEndpointHealth) TableName() string { return "model_endpoint_health" }

// ModelEndpointHealthScope prevents a stale success surviving a configuration
// change. The digest is deliberately not an API field or an authentication token.
func ModelEndpointHealthScope(channel *Channel, modelName string, endpoint *ProxyEndpoint, proxyURL string) string {
	mac := hmac.New(sha256.New, []byte("model-endpoint-health-v1:"+common.SessionSecret))
	parts := []string{fmt.Sprint(channel.Id), fmt.Sprint(channel.OwnerId), fmt.Sprint(channel.Type), modelName, channel.Key, channel.GetBaseURL(), channel.Other, channel.OtherSettings}
	for _, value := range []*string{channel.Setting, channel.ParamOverride, channel.HeaderOverride, channel.ModelMapping} {
		if value == nil {
			parts = append(parts, "")
		} else {
			parts = append(parts, *value)
		}
	}
	if endpoint != nil {
		parts = append(parts, fmt.Sprint(endpoint.Id), endpoint.Kind, endpoint.Url, endpoint.RawUri)
	} else {
		parts = append(parts, "configured", proxyURL)
	}
	for _, part := range parts {
		fmt.Fprintf(mac, "%d:%s", len(part), part)
	}
	return fmt.Sprintf("%x", mac.Sum(nil))
}

// ModelEndpointReasonAllowed is the complete public/persisted error vocabulary.
// Provider messages may contain credentials, request bodies and upstream URLs.
func ModelEndpointReasonAllowed(reason string) bool {
	switch reason {
	case "passed", "not_tested", "stale", "access_denied", "rate_limited", "quota_exhausted", "model_not_found", "upstream_error", "invalid_response", "output_limit_exceeded", "transport_error", "timeout", "cancelled", "unsupported", "unsafe_configuration", "endpoint_unavailable", "not_run", "local_rejection":
		return true
	default:
		return false
	}
}

func RecordModelEndpointHealth(ctx context.Context, sample ModelEndpointHealth) (ModelEndpointHealth, error) {
	if len(sample.ScopeKey) != 64 || sample.ChannelID <= 0 || sample.Model == "" || len(sample.Model) > 200 || sample.EndpointID < 0 || !ModelEndpointReasonAllowed(sample.Reason) {
		return sample, fmt.Errorf("invalid model endpoint measurement")
	}
	symbol := "U"
	switch sample.Status {
	case ModelEndpointAvailable:
		symbol = "A"
	case ModelEndpointUnavailable:
		symbol = "F"
	case ModelEndpointUnknown:
	default:
		return sample, fmt.Errorf("invalid model endpoint status")
	}
	if sample.CheckedAt <= 0 {
		sample.CheckedAt = time.Now().Unix()
	}
	sample.ExpiresAt = sample.CheckedAt + int64(ModelEndpointHealthTTL/time.Second)
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := ModelEndpointHealth{ScopeKey: sample.ScopeKey, ChannelID: sample.ChannelID, Model: sample.Model, EndpointID: sample.EndpointID, SubscriptionID: sample.SubscriptionID, Status: ModelEndpointUnknown, Reason: "not_tested"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var stored ModelEndpointHealth
		if err := lockForUpdate(tx).Where("scope_key = ?", sample.ScopeKey).First(&stored).Error; err != nil {
			return err
		}
		// A slower overlapping request must not overwrite a newer measurement.
		if stored.CheckedAt > sample.CheckedAt {
			sample = stored
			return nil
		}
		sample.ID = stored.ID
		sample.FirstCheckedAt = stored.FirstCheckedAt
		if sample.FirstCheckedAt == 0 {
			sample.FirstCheckedAt = sample.CheckedAt
		}
		sample.LastAvailableAt = stored.LastAvailableAt
		sample.LifetimeSamples = stored.LifetimeSamples + 1
		sample.LifetimePasses, sample.LifetimeFailures = stored.LifetimePasses, stored.LifetimeFailures
		if symbol == "A" {
			sample.LifetimePasses++
			sample.LastAvailableAt = sample.CheckedAt
		}
		if symbol == "F" {
			sample.LifetimeFailures++
		}
		sample.RecentStates = stored.RecentStates + symbol
		if len(sample.RecentStates) > 5 {
			sample.RecentStates = sample.RecentStates[len(sample.RecentStates)-5:]
		}
		sample.RecentSampleCount = len(sample.RecentStates)
		sample.RecentPasses = strings.Count(sample.RecentStates, "A")
		sample.RecentFailures = strings.Count(sample.RecentStates, "F")
		return tx.Model(&stored).Select("*").Updates(&sample).Error
	})
	return sample, err
}

func GetModelEndpointHealth(ctx context.Context, scopeKey string, channelID int, modelName string, endpointID, subscriptionID int) (ModelEndpointHealth, error) {
	sample := ModelEndpointHealth{ScopeKey: scopeKey, ChannelID: channelID, Model: modelName, EndpointID: endpointID, SubscriptionID: subscriptionID, Status: ModelEndpointUnknown, Reason: "not_tested"}
	var stored ModelEndpointHealth
	err := DB.WithContext(ctx).Where("scope_key = ?", scopeKey).First(&stored).Error
	if err == gorm.ErrRecordNotFound {
		return sample, nil
	}
	if err != nil {
		return sample, err
	}
	if stored.ExpiresAt <= time.Now().Unix() {
		stored.Status, stored.Reason = ModelEndpointUnknown, "stale"
	}
	return stored, nil
}
