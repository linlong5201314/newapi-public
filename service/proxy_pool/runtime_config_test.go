package proxy_pool

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
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

// reserveMihomoFixturePorts checks every port, not just mixed-port. Keep them
// reserved until immediately before spawning mihomo to minimize Windows races.
func reserveMihomoFixturePorts(t *testing.T) (int, func()) {
	t.Helper()
	for range 16 {
		base, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := base.Addr().(*net.TCPAddr).Port
		reserved := []net.Listener{base}
		release := func() {
			for _, listener := range reserved {
				_ = listener.Close()
			}
		}
		if port > 65434 {
			release()
			continue
		}
		available := true
		for _, offset := range []int{2, 100, 101} {
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+offset))
			if err != nil {
				available = false
				break
			}
			reserved = append(reserved, listener)
		}
		if available {
			t.Cleanup(release)
			return port, release
		}
		release()
	}
	t.Fatal("cannot reserve a complete loopback mihomo fixture port set")
	return 0, func() {}
}

func TestMihomoIsolatedPrivateListeners(t *testing.T) {
	binary := (&Manager{}).BinaryPath()
	if binary == "" {
		t.Skip("mihomo binary unavailable")
	}
	first := controlledPoolProxy(t, "owner-one")
	second := controlledPoolProxy(t, "owner-two")
	basePort, release := reserveMihomoFixturePorts(t)
	controller := fmt.Sprintf("127.0.0.1:%d", basePort+2)
	plan, err := BuildRuntimeConfig([]RuntimeUser{
		{UID: 1, Nodes: []RuntimeNode{{ID: 11, Name: "one", URL: first.URL}}},
		{UID: 2, Nodes: []RuntimeNode{{ID: 22, Name: "two", URL: second.URL}}},
	}, RuntimeOptions{BasePort: basePort, Controller: controller, Secret: kernelSecret()})
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
	logPath := filepath.Join(dir, "mihomo.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	cmd := exec.Command(binary, "-f", config, "-d", dir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	done := make(chan struct{})
	processDone := make(chan error, 1)
	var processErr error
	release()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		require.NoError(t, err)
	}
	go func() { processErr = cmd.Wait(); processDone <- processErr; close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("kernel failed to terminate")
			}
		}
		_ = logFile.Close()
		if t.Failed() {
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Logf("read fixture kernel log: %v", err)
			} else {
				t.Logf("mihomo fixture log: %s", raw)
			}
		}
	})
	// Wait for authenticated controller state, not an arbitrary startup sleep.
	controllerTransport := &http.Transport{Proxy: nil}
	defer controllerTransport.CloseIdleConnections()
	controllerClient := &http.Client{Transport: controllerTransport, Timeout: 2 * time.Second}
	var readyErr error
	ready := assert.Eventually(t, func() bool {
		select {
		case <-done:
			readyErr = fmt.Errorf("mihomo exited before ready: %v", processErr)
			return false
		default:
		}
		req, err := http.NewRequest(http.MethodGet, "http://"+controller+"/version", nil)
		if err != nil {
			readyErr = err
			return false
		}
		req.Header.Set("Authorization", "Bearer "+kernelSecret())
		response, err := controllerClient.Do(req)
		if err != nil {
			readyErr = err
			return false
		}
		defer response.Body.Close()
		var payload struct {
			Version string `json:"version"`
		}
		if err := common.DecodeJson(io.LimitReader(response.Body, 16<<10), &payload); err != nil {
			readyErr = err
			return false
		}
		if response.StatusCode != http.StatusOK || payload.Version == "" {
			readyErr = fmt.Errorf("controller readiness HTTP %d version=%q", response.StatusCode, payload.Version)
			return false
		}
		readyErr = nil
		return true
	}, 15*time.Second, 100*time.Millisecond)
	require.True(t, ready, "controller readiness: %v", readyErr)
	for _, fixture := range []struct {
		id     int
		marker string
	}{{11, "owner-one"}, {22, "owner-two"}} {
		proxyURL, err := url.Parse(plan.EndpointURLs[fixture.id])
		require.NoError(t, err)
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		var exitErr error
		exitReady := assert.Eventually(t, func() bool {
			select {
			case <-done:
				exitErr = fmt.Errorf("mihomo exited: %v", processErr)
				return false
			default:
			}
			response, err := client.Get("http://127.0.0.1:9/test")
			if err != nil {
				exitErr = err
				return false
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<10))
			closeErr := response.Body.Close()
			if readErr != nil {
				exitErr = readErr
				return false
			}
			if closeErr != nil {
				exitErr = closeErr
				return false
			}
			if response.StatusCode != http.StatusOK || string(body) != fixture.marker {
				exitErr = fmt.Errorf("endpoint %d returned HTTP %d body=%q, want %q", fixture.id, response.StatusCode, body, fixture.marker)
				return false
			}
			exitErr = nil
			return true
		}, 10*time.Second, 100*time.Millisecond)
		require.True(t, exitReady, "isolated endpoint %d: %v", fixture.id, exitErr)
		proxyURL.User = nil
		unauthTransport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		unauthClient := &http.Client{Transport: unauthTransport, Timeout: 3 * time.Second}
		response, err := unauthClient.Get("http://127.0.0.1:9/test")
		require.NoError(t, err)
		assert.Equal(t, http.StatusProxyAuthRequired, response.StatusCode)
		require.NoError(t, response.Body.Close())
		unauthTransport.CloseIdleConnections()
	}

	// The actual running kernel starts with only two private listeners. Inventory
	// is intentionally larger than both the 300 preload and 600 display bounds.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	m := &Manager{cmd: cmd, processDone: processDone, ready: true, plan: plan, cfgPath: config}
	model.SetSelectedEndpointProxyURLProvider(m.EnsureEndpointProxyURL)
	t.Cleanup(func() {
		model.SetSelectedEndpointProxyURLProvider(defaultManager.EnsureEndpointProxyURL)
		model.DB, common.RedisEnabled = oldDB, oldRedis
		_ = sqlDB.Close()
	})
	require.NoError(t, model.EnsureProxyPoolSchema())
	rawURI, err := encodeClashJSON(map[string]any{
		"type": "http", "server": "127.0.0.1", "port": first.Listener.Addr().(*net.TCPAddr).Port,
	})
	require.NoError(t, err)
	rows := make([]model.ProxyEndpoint, 0, 602)
	for i := range 601 {
		rows = append(rows, model.ProxyEndpoint{Id: 1000 + i, UserId: 9, Name: fmt.Sprintf("US fixture %04d", i), Region: "US", Kind: "http", RawUri: rawURI, Source: "haizhu", Public: true, Enabled: true})
	}
	target := model.ProxyEndpoint{Id: 7114, UserId: 9, Name: "HK outside preload and list", Region: "HK", Kind: "http", RawUri: rawURI, Source: "haizhu", Public: true, Enabled: true}
	rows = append(rows, target)
	require.NoError(t, db.CreateInBatches(&rows, 100).Error)
	t.Setenv("PROXY_POOL_PRELOAD_LIMIT", "300")
	preloaded, err := model.ListAllEnabledProxyEndpoints()
	require.NoError(t, err)
	require.Len(t, preloaded, 300)
	assert.NotContains(t, fmt.Sprint(preloaded), target.Name)
	firstPage, hasMore, err := model.ListProxyEndpointsPage(3, 0, 600)
	require.NoError(t, err)
	require.Len(t, firstPage, 600)
	require.True(t, hasMore)
	lastPage, hasMore, err := model.ListProxyEndpointsPage(3, 600, 600)
	require.NoError(t, err)
	require.Len(t, lastPage, 2)
	assert.False(t, hasMore)
	assert.Equal(t, target.Id, lastPage[1].Id)

	// Establish an authenticated tunnel BEFORE hot reload and use it afterward.
	// Loading another node must not terminate or silently reroute an active account.
	existingURL, err := url.Parse(plan.EndpointURLs[11])
	require.NoError(t, err)
	active, err := net.DialTimeout("tcp", existingURL.Host, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = active.Close() })
	require.NoError(t, active.SetDeadline(time.Now().Add(10*time.Second)))
	password, _ := existingURL.User.Password()
	auth := base64.StdEncoding.EncodeToString([]byte(existingURL.User.Username() + ":" + password))
	_, err = fmt.Fprintf(active, "CONNECT fixture.invalid:80 HTTP/1.1\r\nHost: fixture.invalid:80\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	require.NoError(t, err)
	activeReader := bufio.NewReader(active)
	handshake, err := http.ReadResponse(activeReader, &http.Request{Method: http.MethodConnect})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, handshake.StatusCode)

	settings := dto.ChannelSettings{ProxyPolicy: &dto.ChannelProxyPolicy{Mode: "public_pool"}}
	selectedURL, err := model.ResolveStableChannelProxy(3, 0, "workbuddy:fixture-beyond-inventory", settings)
	require.NoError(t, err)
	assert.Equal(t, 3, m.plan.Proxies, "only the selected node is loaded, not 602 candidates")
	assert.Equal(t, plan.EndpointURLs[11], m.plan.EndpointURLs[11], "existing account listener never moves")
	assert.Equal(t, plan.EndpointURLs[22], m.plan.EndpointURLs[22])
	assert.Equal(t, selectedURL, m.plan.EndpointURLs[target.Id])

	_, err = fmt.Fprintf(active, "GET /still-bound HTTP/1.1\r\nHost: fixture.invalid\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	activeResponse, err := http.ReadResponse(activeReader, &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	activeBody, err := io.ReadAll(io.LimitReader(activeResponse.Body, 16<<10))
	require.NoError(t, err)
	require.NoError(t, activeResponse.Body.Close())
	require.NoError(t, active.Close())
	assert.Equal(t, "owner-one", string(activeBody), "active tunnel retains its original private exit")

	var current model.ProxyEndpoint
	require.NoError(t, db.First(&current, target.Id).Error)
	assert.Nil(t, current.LastOkAt, "loaded is not probed healthy")
	selected, err := url.Parse(selectedURL)
	require.NoError(t, err)
	selectedTransport := &http.Transport{Proxy: http.ProxyURL(selected)}
	t.Cleanup(selectedTransport.CloseIdleConnections)
	selectedClient := &http.Client{Transport: selectedTransport, Timeout: 3 * time.Second}
	response, err := selectedClient.Get("http://127.0.0.1:9/on-demand")
	require.NoError(t, err)
	actualBody, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "owner-one", string(actualBody))
	selected.User = nil
	unauthorizedTransport := &http.Transport{Proxy: http.ProxyURL(selected)}
	t.Cleanup(unauthorizedTransport.CloseIdleConnections)
	response, err = (&http.Client{Transport: unauthorizedTransport, Timeout: 3 * time.Second}).Get("http://127.0.0.1:9/on-demand")
	require.NoError(t, err)
	assert.Equal(t, http.StatusProxyAuthRequired, response.StatusCode)
	require.NoError(t, response.Body.Close())
	// Capacity is fail-closed, not listener eviction or a different exit.
	actualCount := m.plan.Proxies
	m.plan.Proxies = maxKernelProxies
	unloaded := rows[0]
	_, err = m.EnsureEndpointProxyURL(unloaded)
	require.ErrorContains(t, err, "capacity reached")
	assert.Len(t, m.LoadedEndpointIDs(), 3)
	repeated, err := model.ResolveStableChannelProxy(3, 0, "workbuddy:fixture-beyond-inventory", settings)
	require.NoError(t, err)
	assert.Equal(t, selectedURL, repeated)
	m.plan.Proxies = actualCount
	t.Log("602 inventory, page1=600, page2=2, preload=300 excludes id7114; public-pool id7114 request reached controlled local origin; only3 listeners; unauthenticated=407; cap fails closed; existing authenticated tunnel survives hot reload")
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
