package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestZCodeStartPlanEntitlements(t *testing.T) {
	InitHttpClient()
	client := GetHttpClient()
	old := *client
	t.Cleanup(func() { *client = old })
	now := time.Now().Unix()
	valid := fmt.Sprintf(`{"code":0,"data":{"server_time":%d,"plans":[{"user_plan_id":"own-plan","plan_id":"start-plan","status":"active","starts_at":%d,"ends_at":%d}],"balances":[{"user_plan_id":"own-plan","plan_id":"start-plan","capabilities":["model:GLM-5.3-Flash","model:GLM-5.3-Flash","model:foreign-model"],"remaining_units":100,"available_units":90,"reserved_units":10}]}}`, now, now-10, now+3600)
	for _, tc := range []struct {
		name, old, next string
		want            bool
	}{
		{"active", "", "", true},
		{"expired", `"ends_at":` + fmt.Sprint(now+3600), `"ends_at":` + fmt.Sprint(now-1), false},
		{"future", `"starts_at":` + fmt.Sprint(now-10), `"starts_at":` + fmt.Sprint(now+100), false},
		{"inactive", `"status":"active"`, `"status":"expired"`, false},
		{"foreign-bucket", `"user_plan_id":"own-plan","plan_id":"start-plan","capabilities"`, `"user_plan_id":"other-plan","plan_id":"start-plan","capabilities"`, false},
		{"no-capability", `"model:GLM-5.3-Flash"`, `"not-a-model"`, false},
		{"zero", `"available_units":90`, `"available_units":0`, false},
		{"negative", `"available_units":90`, `"available_units":-1`, false},
		{"nan", `"available_units":90`, `"available_units":"NaN"`, false},
		{"missing-quota", `"remaining_units":100,"available_units":90,`, ``, false},
		{"bad-time", `"starts_at":` + fmt.Sprint(now-10), `"starts_at":"bad-time"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := valid
			if tc.old != "" {
				body = strings.ReplaceAll(body, tc.old, tc.next)
			}
			calls := 0
			client.Transport = zcodeTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "zcode.z.ai", req.URL.Host)
				require.Equal(t, "/api/v1/zcode-plan/billing/balance", req.URL.Path)
				require.Equal(t, ZCodeAppVersion, req.URL.Query().Get("app_version"))
				require.Equal(t, "Bearer private-plan-fixture", req.Header.Get("Authorization"))
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			models, err := ValidateZCodeStartPlan(context.Background(), "", &ZCodeCredential{Provider: "bigmodel", UserID: "owner", Token: "private-plan-fixture", ExpiresAt: now + 7200})
			if tc.want {
				require.NoError(t, err)
				require.Equal(t, []string{"GLM-5.3-Flash"}, models)
			} else {
				require.Error(t, err)
				require.Empty(t, models)
				require.NotContains(t, err.Error(), "private-plan-fixture")
			}
			require.Equal(t, 1, calls)
		})
	}
	client.Transport = zcodeTestTransport(func(req *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Date", time.Unix(now+4000, 0).UTC().Format(http.TimeFormat))
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(valid)), Request: req}, nil
	})
	_, err := ValidateZCodeStartPlan(context.Background(), "", &ZCodeCredential{Provider: "zai", UserID: "owner", Token: "private-plan-fixture", ExpiresAt: now + 7200})
	require.ErrorIs(t, err, ErrZCodePlanUnavailable)
}

func TestZCodeStartPlanRejectsAuthenticationAndRedirect(t *testing.T) {
	InitHttpClient()
	client := GetHttpClient()
	old := *client
	t.Cleanup(func() { *client = old })
	for _, status := range []int{302, 401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			client.Transport = zcodeTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				h := make(http.Header)
				h.Set("Location", "https://foreign.invalid/leak")
				return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(`{"msg":"private-error"}`)), Request: r}, nil
			})
			_, err := ValidateZCodeStartPlan(context.Background(), "", &ZCodeCredential{Provider: "bigmodel", UserID: "owner", Token: "private-token", ExpiresAt: time.Now().Add(time.Hour).Unix()})
			require.Error(t, err)
			require.Equal(t, 1, calls)
			require.NotContains(t, err.Error(), "private-error")
		})
	}
}
