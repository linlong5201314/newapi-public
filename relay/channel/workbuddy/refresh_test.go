// refresh_test.go WorkBuddy 刷新单飞并发安全单测：
// N 个并发刷新同一凭据 → 上游只收到 1 次 refresh（RT 反重放红线）。
package workbuddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// refreshMockServer 模拟 workbuddy 刷新端点 + 信封格式。
func refreshMockServer(t *testing.T, hits *atomic.Int32, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/plugin/auth/token/refresh" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		hits.Add(1)
		if delay > 0 {
			time.Sleep(delay)
		}
		if r.Header.Get("X-Refresh-Token") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":12153,"msg":"refresh token is invalid"}`))
			return
		}
		resp := map[string]any{
			"code": 0,
			"msg":  "OK",
			"data": map[string]any{
				"accessToken":  "at-new-1",
				"refreshToken": "rt-new-1",
				"expiresIn":    5184000,
				"domain":       "www.codebuddy.cn",
			},
		}
		raw, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
}

func TestRefreshSingleflightConcurrent(t *testing.T) {
	var hits atomic.Int32
	srv := refreshMockServer(t, &hits, 50*time.Millisecond)
	defer srv.Close()

	cred := &Cred{
		AccessToken:  "at-old",
		RefreshToken: "rt-old",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(), // 已过期 → 必刷
		BaseOverride: srv.URL,
	}

	const n = 20
	var wg sync.WaitGroup
	results := make([]*Cred, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// 每个 goroutine 独立 cred 副本（模拟并发请求各自解析的凭据快照）
			c := *cred
			out, err := RefreshIfNeeded(0, &c)
			results[idx] = out
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d refresh failed: %v", i, errs[i])
		}
		if results[i].AccessToken != "at-new-1" {
			t.Fatalf("goroutine %d got wrong token: %s", i, results[i].AccessToken)
		}
		if results[i].RefreshToken != "rt-new-1" {
			t.Fatalf("goroutine %d rt not rotated: %s", i, results[i].RefreshToken)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream refresh called %d times, want 1 (singleflight broken — RT anti-replay violated)", got)
	}
}

func TestRefreshSkippedWhenFresh(t *testing.T) {
	var hits atomic.Int32
	srv := refreshMockServer(t, &hits, 0)
	defer srv.Close()
	cred := &Cred{
		AccessToken:  "at-ok",
		RefreshToken: "rt-ok",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(), // 新鲜 → 不刷
		BaseOverride: srv.URL,
	}
	out, err := RefreshIfNeeded(0, cred)
	if err != nil {
		t.Fatal(err)
	}
	if out.AccessToken != "at-ok" {
		t.Fatalf("fresh cred mutated: %s", out.AccessToken)
	}
	if hits.Load() != 0 {
		t.Fatalf("fresh cred triggered upstream refresh")
	}
}

func TestRefreshSessionDeadPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":12153,"msg":"Offline user session not found"}`))
	}))
	defer srv.Close()
	cred := &Cred{
		AccessToken:  "at-old",
		RefreshToken: "rt-dead",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
		BaseOverride: srv.URL,
	}
	_, err := RefreshIfNeeded(0, cred)
	if err == nil {
		t.Fatalf("session dead refresh should fail")
	}
	var ue *Error
	if !asError(err, &ue) || ue.Kind != ErrSessionDead {
		t.Fatalf("error not classified session_dead: %v", err)
	}
}
