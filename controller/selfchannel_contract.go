package controller

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// prepareSelfChannelUpdate preserves omitted catalog and masked proxy credentials.
// Ownership is checked by the caller before this function can inspect stored data.
func prepareSelfChannelUpdate(userID int, original *model.Channel, request map[string]any) error {
	if raw, provided := request["setting"]; provided {
		text, ok := raw.(string)
		if !ok {
			return fmt.Errorf("invalid channel settings")
		}
		fields := map[string]any{}
		if text != "" {
			if err := common.UnmarshalJsonStr(text, &fields); err != nil || fields == nil {
				return fmt.Errorf("invalid channel settings")
			}
		}
		settings := original.GetSetting()
		var policy *dto.ChannelProxyPolicy
		if rawPolicy, exists := fields["proxy_policy"]; exists {
			encoded, err := common.Marshal(rawPolicy)
			if err != nil {
				return fmt.Errorf("invalid channel proxy policy")
			}
			if err := common.Unmarshal(encoded, &policy); err != nil {
				return fmt.Errorf("invalid channel proxy policy")
			}
		} else {
			policy = settings.ProxyPolicy
		}
		proxy, _ := fields["proxy"].(string)
		proxy = strings.TrimSpace(proxy)
		if strings.Contains(proxy, "***") {
			return fmt.Errorf("masked proxy credentials cannot be saved")
		}
		if proxy == "" && (policy == nil || policy.Mode == "fixed") {
			proxy = settings.Proxy
		}
		if policy != nil && policy.Mode != "fixed" {
			proxy = ""
		}
		if err := model.ValidateSelfChannelProxyPolicy(userID, policy, proxy); err != nil {
			return err
		}
		fields["proxy"] = proxy
		if policy != nil {
			fields["proxy_policy"] = policy
		}
		encoded, err := common.Marshal(fields)
		if err != nil {
			return err
		}
		request["setting"] = string(encoded)
	}
	// Some editors send other_info with only currently selected models. Always keep
	// the complete discovered catalog independently, including on a no-op save.
	if raw, provided := request["other_info"]; provided {
		text, ok := raw.(string)
		if !ok {
			return fmt.Errorf("invalid channel metadata")
		}
		fields := map[string]any{}
		if text != "" {
			if err := common.UnmarshalJsonStr(text, &fields); err != nil || fields == nil {
				return fmt.Errorf("invalid channel metadata")
			}
		}
		originalInfo := original.GetOtherInfo()
		for key, value := range originalInfo {
			if _, exists := fields[key]; !exists {
				fields[key] = value
			}
		}
		current := *original
		encoded, err := common.Marshal(fields)
		if err != nil {
			return err
		}
		current.OtherInfo = string(encoded)
		fields["allModels"] = mergeModelCatalogs(DefaultModelsForChannel(original), DefaultModelsForChannel(&current))
		if original.Type == constant.ChannelTypeZCode {
			fields["allModels"] = originalInfo["allModels"]
		}
		// The provider identity is server-owned, not an escape hatch for free filtering.
		if templateID, ok := originalInfo["templateId"]; ok {
			fields["templateId"] = templateID
		}
		encoded, err = common.Marshal(fields)
		if err != nil {
			return err
		}
		request["other_info"] = string(encoded)
	}
	return nil
}

// sanitizeSelfChannel removes secrets but keeps all non-secret editable settings.
// Empty fixed proxy on the next save means keep the stored credential.
func sanitizeSelfChannel(channel *model.Channel) {
	channel.Key = ""
	settings := channel.GetSetting()
	settings.ProxyPolicy = effectiveSelfChannelProxyPolicy(settings)
	settings.Proxy = ""
	channel.SetSetting(settings)
	// Device cookies and provider access tokens are never channel display metadata.
	info := channel.GetOtherInfo()
	delete(info, "deviceProfile")
	delete(info, "device_profile")
	channel.SetOtherInfo(info)
	clearChannelInfo(channel)
}

// effectiveSelfChannelProxyPolicy preserves legacy routing before secrets are redacted.
func effectiveSelfChannelProxyPolicy(setting dto.ChannelSettings) *dto.ChannelProxyPolicy {
	if setting.ProxyPolicy != nil {
		return setting.ProxyPolicy
	}
	mode := "fixed"
	switch strings.TrimSpace(setting.Proxy) {
	case "", "userpool":
		mode = "user_pool"
	case "direct":
		mode = "direct"
	}
	return &dto.ChannelProxyPolicy{Mode: mode}
}

func freeModelCallContract(baseURL, templateID string) string {
	switch liveFreeCatalogProvider(baseURL, templateID) {
	case "opencode":
		return "opencode_client_only"
	case "openrouter":
		return "openai_chat_completions_api_key_required"
	default:
		return "upstream"
	}
}
