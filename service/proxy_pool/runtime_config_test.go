package proxy_pool

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// controlledPoolProxy ignores the requested destination and tunnels only to a local fixture.
func controlledPoolProxy(t *testing.T, marker string) *httptest.Server {
	t.Helper()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, marker) }))
	t.Cleanup(origin.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			_, _ = io.WriteString(w, marker)
			return
		}
		upstream, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		downstream, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		defer downstream.Close()
		defer upstream.Close()
		_, _ = buf.WriteString("HTTP/1.1 200 Connection Established" + string([]byte{13, 10, 13, 10}))
		_ = buf.Flush()
		done := make(chan struct{}, 1)
		go func() { _, _ = io.Copy(upstream, buf); _ = upstream.Close(); done <- struct{}{} }()
		_, _ = io.Copy(downstream, upstream)
		_ = downstream.Close()
		<-done
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

func TestMihomoIsolatedPrivateListeners(t *testing.T) {
	binary := (&Manager{}).BinaryPath()
	if binary == "" {
		t.Skip("mihomo binary unavailable")
	}
	first := controlledPoolProxy(t, "owner-one")
	second := controlledPoolProxy(t, "owner-two")
	base, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	basePort := base.Addr().(*net.TCPAddr).Port
	require.NoError(t, base.Close())
	if basePort > 65000 {
		t.Skip("ephemeral base port too high")
	}
	plan, err := BuildRuntimeConfig([]RuntimeUser{
		{UID: 1, Nodes: []RuntimeNode{{ID: 11, Name: "one", URL: first.URL}}},
		{UID: 2, Nodes: []RuntimeNode{{ID: 22, Name: "two", URL: second.URL}}},
	}, RuntimeOptions{BasePort: basePort, Controller: fmt.Sprintf("127.0.0.1:%d", basePort+2), Secret: "local-fixture-only"})
	require.NoError(t, err)
	var doc runtimeDoc
	require.NoError(t, yaml.Unmarshal(plan.YAML, &doc))
	for _, group := range doc.ProxyGroups {
		for _, name := range []string{plan.EndpointNodeName[11], plan.EndpointNodeName[22]} {
			assert.NotContains(t, fmt.Sprint(group["proxies"]), name)
		}
	}
	require.Len(t, doc.Listeners, 2)
	for _, listener := range doc.Listeners {
		assert.Equal(t, "127.0.0.1", listener["listen"])
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(config, plan.YAML, 0o600))
	cmd := exec.Command(binary, "-f", config, "-d", dir)
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("kernel failed to terminate")
		}
	})
	for id, expected := range map[int]string{11: "owner-one", 22: "owner-two"} {
		proxyURL, err := url.Parse(plan.EndpointURLs[id])
		require.NoError(t, err)
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: time.Second}
		var response *http.Response
		require.Eventually(t, func() bool { response, err = client.Get("http://controlled.example.invalid/test"); return err == nil }, 6*time.Second, 50*time.Millisecond)
		assert.Equal(t, http.StatusOK, response.StatusCode)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, err)
		assert.Equal(t, expected, string(body))
		proxyURL.User = nil
		unauthTransport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		unauthClient := &http.Client{Transport: unauthTransport, Timeout: time.Second}
		response, err = unauthClient.Get("http://controlled.example.invalid/test")
		require.NoError(t, err)
		assert.Equal(t, http.StatusProxyAuthRequired, response.StatusCode)
		_ = response.Body.Close()
		unauthTransport.CloseIdleConnections()
	}
}

func TestBuildRuntimeConfigSharedPortAndVisibility(t *testing.T) {
	clashJSON, err := encodeClashJSON(map[string]any{
		"type": "anytls", "server": "hk01.example.com", "port": 443,
		"password": "secret", "sni": "hk01.example.com", "name": "ignored",
	})
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	plan, err := BuildRuntimeConfig([]RuntimeUser{
		{UID: 7, Nodes: []RuntimeNode{
			{ID: 101, Name: "香港 01", Kind: "anytls", RawURI: clashJSON, Public: false},
			{ID: 102, Name: "直连 HTTP", Kind: "http", URL: "http://user:pw@10.0.0.1:8080", Public: true},
		}},
		{UID: 3, Nodes: []RuntimeNode{
			{ID: 103, Name: "新加坡 02", Kind: "ss", RawURI: clashJSON, Public: true},
		}},
		{UID: 9}, // 没私有节点，但能看到公开节点
	}, RuntimeOptions{BasePort: 23456, Controller: "127.0.0.1:23459"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if plan.Proxies != 3 {
		t.Fatalf("proxies = %d, want 3", plan.Proxies)
	}
	// 所有有可见节点的用户共用单一 mixed-port 23456
	if plan.Ports[3] != 23456 || plan.Ports[7] != 23456 {
		t.Fatalf("ports = %v, want shared 23456", plan.Ports)
	}
	// 用户 7 有 1 个私有 + 2 个公开 = 3 个可见节点；用户 3 只有 2 个公开节点（看不到用户 7 的私有节点）
	if len(plan.NodeNames[7]) != 3 {
		t.Fatalf("user 7 visible nodes = %v, want 3", plan.NodeNames[7])
	}
	if len(plan.NodeNames[3]) != 2 {
		t.Fatalf("user 3 visible nodes = %v, want 2 (private node of user 7 must be hidden)", plan.NodeNames[3])
	}

	yamlText := string(plan.YAML)
	for _, want := range []string{
		"u3·新加坡 02", "u7·香港 01", "u7·直连 HTTP",
		"type: anytls", "type: http", "mixed-port: 23456",
		"external-controller: 127.0.0.1:23459", "MATCH,PROXY",
	} {
		if !strings.Contains(yamlText, want) {
			t.Fatalf("config missing %q\n%s", want, yamlText)
		}
	}
}

func TestBuildRuntimeConfigEmptyUsers(t *testing.T) {
	plan, err := BuildRuntimeConfig(nil, RuntimeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.Proxies != 0 || len(plan.Ports) != 0 {
		t.Fatalf("empty plan should have no proxies/ports, got %d/%v", plan.Proxies, plan.Ports)
	}
	if !strings.Contains(string(plan.YAML), "rules:") {
		t.Fatalf("empty config should still be valid yaml:\n%s", plan.YAML)
	}
}

func TestManagerExitedProcessIsNotRunning(t *testing.T) {
	done := make(chan error, 1)
	done <- fmt.Errorf("controlled exit")
	m := &Manager{cmd: &exec.Cmd{Process: &os.Process{Pid: 123}}, processDone: done, ready: true}
	status := m.Status()
	assert.False(t, status.Running)
	assert.Contains(t, status.LastError, "controlled exit")
	m.Stop()
	assert.False(t, m.Status().Running)
}
