package proxy_pool

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 内核（mihomo）常驻进程管理：生成单实例共享配置（mixed-port 23456） → 拉起子进程 →
// 公共池公开节点全员可用、用户未公开私有节点仅本人可见可调 → 通过 external-controller 切节点与熔断。

const (
	mihomoCtrlPort = 23459
	mihomoBasePort = 23456
	mihomoWaitTime = 6 * time.Second
)

// HaizhuDefaultExtractDir 本机逆向提取的 HaizhuClash 0.14.3 节点目录。
const HaizhuDefaultExtractDir = `C:\Users\林龙\Desktop\逆向测试\haizhu测试\HaizhuClash_0.14.3_extract`

// Manager 全局单例：一台共享 mihomo 服务全站出口池。
type Manager struct {
	mu             sync.Mutex
	cmd            *exec.Cmd
	processDone    chan error
	ready          bool
	plan           *RuntimePlan
	cfgPath        string
	dir            string
	lastErr        string
	startedAt      time.Time
	selectedNode   string
	failoverCursor uint64
}

var defaultManager = &Manager{}

// ManagerInstance 返回全局内核管理器。
func ManagerInstance() *Manager { return defaultManager }

func init() {
	model.SetEndpointProxyURLProvider(defaultManager.EndpointProxyURL)
	model.SetSelectedEndpointProxyURLProvider(defaultManager.EnsureEndpointProxyURL)
	// 让出口轮换能用上内核端口：节点全是客户端协议时，URL 列表本来为空。
	model.SetExtraProxyURLProvider(func(userId int) []string {
		return defaultManager.ProxyURLs(userId)
	})
	// 当通过本地 mihomo 端口请求失败时，自动切换到下一个可用节点（无感熔断）。
	model.SetKernelFailoverProvider(func(userId int, proxyURL string) bool {
		return model.InvalidateStableProxyURL(proxyURL)
	})
}

// BinaryPath 返回 mihomo 可执行文件路径；优先环境变量、仓库内 bin/mihomo.exe，再查 PATH。
func (m *Manager) BinaryPath() string {
	if custom := strings.TrimSpace(os.Getenv("MIHOMO_PATH")); custom != "" {
		if _, err := os.Stat(custom); err == nil {
			return custom
		}
	}
	candidates := []string{
		filepath.Join("bin", "mihomo.exe"),
		filepath.Join("bin", "mihomo"),
		`C:\Users\林龙\Desktop\GitHub仓库修改\公共版本连接newapi\new-api\bin\mihomo.exe`,
		filepath.Join(HaizhuDefaultExtractDir, "mihomo.exe"),
	}
	for _, p := range candidates {
		if abs, err := filepath.Abs(p); err == nil {
			if st, statErr := os.Stat(abs); statErr == nil && !st.IsDir() {
				return abs
			}
		}
	}
	for _, name := range []string{"mihomo", "mihomo.exe", "clash-meta", "clash-meta.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// Status 内核状态快照。
type Status struct {
	Available    bool           `json:"available"`
	Running      bool           `json:"running"`
	Binary       string         `json:"binary"`
	Proxies      int            `json:"proxies"`
	PublicCount  int            `json:"public_count"`
	SelectedNode string         `json:"selected_node"`
	SharedPort   int            `json:"shared_port"`
	LastError    string         `json:"last_error"`
	StartedAt    int64          `json:"started_at"`
	Ports        map[int]int    `json:"ports"`
	Groups       map[int]string `json:"groups"`
	ConfigPath   string         `json:"config_path"`
}

// Status 返回当前状态。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(m.BinaryPath())
}

// EndpointProxyURL returns only a ready listener whose node data still matches DB.
func (m *Manager) EndpointProxyURL(endpoint model.ProxyEndpoint) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.runningLocked() || !m.ready || m.plan == nil {
		return "", fmt.Errorf("mihomo is not ready; reload the kernel")
	}
	key := stableHash(endpoint.Url + "\x00" + endpoint.RawUri)
	if m.plan.EndpointKeys[endpoint.Id] != key || m.plan.EndpointURLs[endpoint.Id] == "" {
		return "", fmt.Errorf("proxy endpoint is not loaded or changed; reload the kernel")
	}
	return m.plan.EndpointURLs[endpoint.Id], nil
}

// EnsureEndpointProxyURL hot-loads one authorized selection without restarting
// the process or evicting another account's listener. Inventory size does not
// determine listener count; at the hard bound callers fail closed until reload.
func (m *Manager) EnsureEndpointProxyURL(endpoint model.ProxyEndpoint) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.runningLocked() || !m.ready || m.plan == nil {
		return "", fmt.Errorf("mihomo is not ready; reload the kernel")
	}
	if endpoint.Id <= 0 || endpoint.UserId <= 0 || !endpoint.Enabled {
		return "", fmt.Errorf("proxy endpoint is disabled or invalid")
	}
	key := stableHash(endpoint.Url + "\x00" + endpoint.RawUri)
	if m.plan.EndpointKeys[endpoint.Id] == key && m.plan.EndpointURLs[endpoint.Id] != "" {
		return m.plan.EndpointURLs[endpoint.Id], nil
	}
	if m.plan.EndpointURLs[endpoint.Id] == "" && m.plan.Proxies >= maxKernelProxies {
		return "", fmt.Errorf("mihomo runtime listener capacity reached; reload the bounded runtime")
	}
	users := make([]RuntimeUser, 0, len(m.plan.Users)+1)
	for _, user := range m.plan.Users {
		next := RuntimeUser{UID: user.UID}
		for _, node := range user.Nodes {
			if node.ID != endpoint.Id {
				next.Nodes = append(next.Nodes, node)
			}
		}
		users = append(users, next)
	}
	users = append(users, RuntimeUser{UID: endpoint.UserId, Nodes: []RuntimeNode{{
		ID: endpoint.Id, Name: endpoint.Name, Kind: endpoint.Kind, URL: endpoint.Url, RawURI: endpoint.RawUri, Public: endpoint.Public,
	}}})
	plan, err := BuildRuntimeConfig(users, RuntimeOptions{
		BasePort: m.plan.SharedPort, Controller: m.plan.Controller, Secret: kernelSecret(), EndpointPorts: m.plan.EndpointPorts,
	})
	if err != nil {
		return "", fmt.Errorf("build on-demand proxy listener: %w", err)
	}
	if plan.EndpointKeys[endpoint.Id] != key || plan.EndpointURLs[endpoint.Id] == "" {
		return "", fmt.Errorf("proxy endpoint protocol or configuration is unsupported")
	}
	port := plan.EndpointPorts[endpoint.Id]
	if _, loaded := m.plan.EndpointPorts[endpoint.Id]; !loaded {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return "", fmt.Errorf("on-demand proxy listener port unavailable")
		}
		_ = listener.Close()
	}
	body, err := common.Marshal(map[string]string{"payload": string(plan.YAML)})
	if err != nil {
		return "", fmt.Errorf("encode on-demand proxy configuration: %w", err)
	}
	req, err := http.NewRequest(http.MethodPut, "http://"+m.plan.Controller+"/configs?force=true", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+kernelSecret())
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// A timed-out reload may have applied: do not serve an uncertain plan.
		m.ready = false
		m.lastErr = "mihomo on-demand reload outcome is unknown; reload the kernel"
		return "", fmt.Errorf("%s", m.lastErr)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("mihomo rejected on-demand proxy configuration: HTTP %d", resp.StatusCode)
	}
	if err := verifyEndpointListener(plan.EndpointURLs[endpoint.Id]); err != nil {
		m.ready = false
		m.lastErr = "mihomo on-demand listener authentication is unavailable; reload the kernel"
		return "", fmt.Errorf("%s", m.lastErr)
	}
	if err := os.WriteFile(m.cfgPath, plan.YAML, 0o600); err != nil {
		m.ready = false
		m.lastErr = "persist mihomo on-demand configuration failed; reload the kernel"
		return "", fmt.Errorf("%s", m.lastErr)
	}
	m.plan = plan
	return plan.EndpointURLs[endpoint.Id], nil
}

// verifyEndpointListener authenticates to a mixed listener using SOCKS5
// (RFC1929). It sends no CONNECT command and never contacts an upstream proxy.
func verifyEndpointListener(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.User == nil {
		return fmt.Errorf("invalid local endpoint listener")
	}
	username := parsed.User.Username()
	password, _ := parsed.User.Password()
	if len(username) == 0 || len(username) > 255 || len(password) == 0 || len(password) > 255 {
		return fmt.Errorf("invalid local endpoint credentials")
	}
	connection, err := net.DialTimeout("tcp", parsed.Host, time.Second)
	if err != nil {
		return fmt.Errorf("connect local endpoint listener")
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err := connection.Write([]byte{5, 1, 2}); err != nil {
		return err
	}
	var reply [2]byte
	if _, err := io.ReadFull(connection, reply[:]); err != nil {
		return err
	}
	if reply != [2]byte{5, 2} {
		return fmt.Errorf("local endpoint listener must require authentication")
	}
	auth := append([]byte{1, byte(len(username))}, []byte(username)...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, []byte(password)...)
	if _, err := connection.Write(auth); err != nil {
		return err
	}
	if _, err := io.ReadFull(connection, reply[:]); err != nil {
		return err
	}
	if reply != [2]byte{1, 0} {
		return fmt.Errorf("local endpoint listener rejected authentication")
	}
	return nil
}

// LoadedEndpointIDs returns listeners in THIS process only, never another node's
// plan or an exited kernel's last configuration. Callers must apply visibility.
func (m *Manager) LoadedEndpointIDs() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.runningLocked() || !m.ready || m.plan == nil {
		return nil
	}
	ids := make([]int, 0, len(m.plan.EndpointURLs))
	for id := range m.plan.EndpointURLs {
		ids = append(ids, id)
	}
	return ids
}

// ProxyURLs authorizes every endpoint against the current database before use.
func (m *Manager) ProxyURLs(userID int) []string {
	if model.DB == nil {
		return nil
	}
	var endpoints []model.ProxyEndpoint
	if err := model.DB.Where("(user_id = ? OR public = ?) AND enabled = ? AND url = '' AND fail_count < ?", userID, true, true, model.ProxyEndpointMaxFailCount).Find(&endpoints).Error; err != nil {
		return nil
	}
	var urls []string
	for _, endpoint := range endpoints {
		if proxyURL, err := m.EndpointProxyURL(endpoint); err == nil {
			urls = append(urls, proxyURL)
		}
	}
	return urls
}

// NodeNames 该用户可见的内核节点名（自己的私有节点 + 公共池公开节点；绝不包含他人私有节点）。
func (m *Manager) NodeNames(userId int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.visibleNodesLocked(userId)
}

func (m *Manager) visibleNodesLocked(userId int) []string {
	if m.plan == nil {
		return nil
	}
	if list, ok := m.plan.NodeNames[userId]; ok && len(list) > 0 {
		return append([]string(nil), list...)
	}
	if len(m.plan.PublicNodes) > 0 {
		return append([]string(nil), m.plan.PublicNodes...)
	}
	return nil
}

// Reload 用最新节点重建配置并（重）启动内核。
func (m *Manager) Reload(users []RuntimeUser) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	binary := m.BinaryPath()
	if binary == "" {
		m.lastErr = "mihomo binary not found; set MIHOMO_PATH or put mihomo in bin/"
		return m.statusLocked(binary), fmt.Errorf("%s", m.lastErr)
	}
	plan, err := BuildRuntimeConfig(users, RuntimeOptions{
		BasePort:   mihomoBasePort,
		Controller: fmt.Sprintf("127.0.0.1:%d", mihomoCtrlPort),
		Secret:     kernelSecret(),
		// Keep per-endpoint listener ports stable across kernel reloads. Reissuing
		// them would orphan proxy URLs already stored in account flows/channels.
		EndpointPorts: func() map[int]int {
			if m.plan != nil {
				return m.plan.EndpointPorts
			}
			return nil
		}(),
	})
	if err != nil {
		m.lastErr = err.Error()
		return m.statusLocked(binary), err
	}

	dir := m.workDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.lastErr = err.Error()
		return m.statusLocked(binary), err
	}
	ensureGeoDataFiles(dir)

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, plan.YAML, 0o600); err != nil {
		m.lastErr = err.Error()
		return m.statusLocked(binary), err
	}

	m.stopLocked()
	m.cfgPath = cfgPath
	m.plan = plan
	m.selectedNode = PublicFastGroupName

	if plan.Proxies == 0 {
		m.lastErr = "no supported nodes enabled; kernel not started"
		return m.statusLocked(binary), nil
	}

	// Reject occupied controller/listener ports instead of accepting a foreign kernel.
	ports := []int{mihomoCtrlPort, mihomoBasePort}
	for _, port := range plan.EndpointPorts {
		ports = append(ports, port)
	}
	for _, port := range ports {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			m.lastErr = fmt.Sprintf("mihomo port %d unavailable", port)
			return m.statusLocked(binary), fmt.Errorf("%s", m.lastErr)
		}
		_ = listener.Close()
	}
	cmd := exec.Command(binary, "-f", cfgPath, "-d", dir)
	cmd.WaitDelay = time.Second
	cmd.Stdout = &logWriter{}
	cmd.Stderr = &logWriter{}
	if err := cmd.Start(); err != nil {
		m.lastErr = err.Error()
		return m.statusLocked(binary), err
	}
	m.cmd = cmd
	done := make(chan error, 1)
	m.processDone = done
	go func() { done <- cmd.Wait() }()
	m.startedAt = time.Now()
	m.lastErr = ""
	if cmd.Process != nil {
		_ = os.WriteFile(filepath.Join(dir, "mihomo.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}

	deadline := time.Now().Add(mihomoWaitTime)
	for time.Now().Before(deadline) {
		if !m.runningLocked() {
			return m.statusLocked(binary), fmt.Errorf("%s", m.lastErr)
		}
		if m.controllerReady() {
			m.ready = true
			return m.statusLocked(binary), nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	m.stopLocked()
	m.lastErr = "mihomo controller did not become healthy before startup timeout"
	return m.statusLocked(binary), fmt.Errorf("%s", m.lastErr)
}

// Select 切换出站节点（校验用户只能选择自己的私有节点或公共池公开节点，禁止切他人私有节点）。
func (m *Manager) Select(userId int, nodeName string) error {
	m.mu.Lock()
	valid := []string(nil)
	if m.plan != nil {
		valid = append(valid, m.plan.PublicNodes...)
	}
	m.mu.Unlock()
	if len(valid) == 0 {
		return fmt.Errorf("no visible kernel nodes for user %d", userId)
	}
	if nodeName != PublicFastGroupName && (nodeName == "" || !containsString(valid, nodeName)) {
		return fmt.Errorf("unknown or unauthorized node %q", nodeName)
	}
	if err := m.putGroupSelection(SharedGroupName, nodeName); err != nil {
		return err
	}
	m.mu.Lock()
	m.selectedNode = nodeName
	m.mu.Unlock()
	return nil
}

func (m *Manager) putGroupSelection(group, nodeName string) error {
	body, err := common.Marshal(map[string]string{"name": nodeName})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("http://127.0.0.1:%d/proxies/%s", mihomoCtrlPort, url.PathEscape(group)),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret := kernelSecret(); secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mihomo rejected switch: HTTP %d", resp.StatusCode)
	}
	return nil
}

// FailoverOnError 当请求走本地 mihomo 端口失败时，自动切换到该用户可见的下一个节点（无感熔断）。
func (m *Manager) FailoverOnError(userId int, proxyURL string) bool {
	expected := fmt.Sprintf("127.0.0.1:%d", mihomoBasePort)
	if !strings.Contains(proxyURL, expected) {
		return false
	}
	m.mu.Lock()
	candidates := []string(nil)
	if m.plan != nil {
		candidates = append(candidates, m.plan.PublicNodes...)
	}
	m.mu.Unlock()
	if len(candidates) == 0 {
		return false
	}
	idx := int(atomic.AddUint64(&m.failoverCursor, 1)-1) % len(candidates)
	nextNode := candidates[idx]
	if err := m.putGroupSelection(SharedGroupName, nextNode); err != nil {
		return false
	}
	m.mu.Lock()
	m.selectedNode = nextNode
	m.mu.Unlock()
	return true
}

// ProbeEndpointDelay 通过 mihomo controller 实测某客户端节点的延迟（ms）。
func (m *Manager) ProbeEndpointDelay(endpointID int) (int, error) {
	m.mu.Lock()
	var nodeName string
	if m.plan != nil {
		nodeName = m.plan.EndpointNodeName[endpointID]
	}
	running := m.runningLocked() && m.ready
	m.mu.Unlock()

	if !running || nodeName == "" {
		return 0, fmt.Errorf("mihomo endpoint is unavailable; ask a root administrator to reload the kernel")
	}
	probeURL := fmt.Sprintf(
		"http://127.0.0.1:%d/proxies/%s/delay?timeout=6000&url=%s",
		mihomoCtrlPort,
		url.PathEscape(nodeName),
		url.QueryEscape("http://cp.cloudflare.com/generate_204"),
	)
	req, err := http.NewRequest(http.MethodGet, probeURL, nil)
	if err != nil {
		return 0, err
	}
	if secret := kernelSecret(); secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return 0, fmt.Errorf("read mihomo delay: %w", err)
	}
	var payload struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	if err := common.Unmarshal(raw, &payload); err != nil {
		return 0, fmt.Errorf("decode mihomo delay: %w", err)
	}
	if resp.StatusCode >= 300 || payload.Delay <= 0 {
		msg := strings.TrimSpace(payload.Message)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return 0, fmt.Errorf("delay probe failed: %s", msg)
	}
	return payload.Delay, nil
}

// Stop 关停内核（进程退出/重载配置时用）。
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

// runningLocked reaps an exited process; the buffered waiter never needs m.mu.
func (m *Manager) runningLocked() bool {
	if m.cmd == nil {
		return false
	}
	select {
	case err := <-m.processDone:
		m.cmd = nil
		m.ready = false
		m.lastErr = "mihomo process exited"
		if err != nil {
			m.lastErr += ": " + err.Error()
		}
		return false
	default:
		return m.cmd.Process != nil
	}
}

func (m *Manager) stopLocked() {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		if m.processDone != nil {
			<-m.processDone
		}
	}
	m.cmd = nil
	m.processDone = nil
	m.ready = false
	m.startedAt = time.Time{}
	// Never kill a persisted PID: it may have been reused by an unrelated process.
	if m.dir != "" {
		_ = os.Remove(filepath.Join(m.dir, "mihomo.pid"))
	}
}

func (m *Manager) statusLocked(binary string) Status {
	status := Status{
		Available:    binary != "",
		Binary:       binary,
		LastError:    m.lastErr,
		ConfigPath:   m.cfgPath,
		SelectedNode: m.selectedNode,
		SharedPort:   mihomoBasePort,
		Ports:        map[int]int{},
		Groups:       map[int]string{},
	}
	if m.runningLocked() && m.ready && m.plan != nil {
		status.Proxies = m.plan.Proxies
		status.PublicCount = len(m.plan.PublicNodes)
		status.Ports = m.plan.Ports
		status.Groups = m.plan.Groups
		status.Running = true
		status.StartedAt = m.startedAt.Unix()
	}
	status.LastError = m.lastErr
	return status
}

func (m *Manager) controllerReady() bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/version", mihomoCtrlPort), nil)
	if err != nil {
		return false
	}
	if secret := kernelSecret(); secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var payload struct {
		Version string `json:"version"`
	}
	return common.DecodeJson(io.LimitReader(resp.Body, 16<<10), &payload) == nil && payload.Version != ""
}

func (m *Manager) workDir() string {
	if m.dir != "" {
		return m.dir
	}
	base := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if base == "" {
		base = filepath.Join(os.TempDir(), "new-api")
	}
	if abs, err := filepath.Abs(filepath.Join(base, "mihomo")); err == nil {
		m.dir = abs
		return abs
	}
	m.dir = filepath.Join(os.TempDir(), "new-api-mihomo")
	return m.dir
}

func ensureGeoDataFiles(targetDir string) {
	donorDirs := []string{
		`C:\Users\林龙\Desktop\GitHub仓库修改\deepseek-v4flash - 部署\work\_mihomo_test`,
		filepath.Join("bin"),
	}
	for _, name := range []string{"geoip.metadb", "GeoSite.dat"} {
		dst := filepath.Join(targetDir, name)
		if st, err := os.Stat(dst); err == nil && st.Size() > 1024 {
			continue
		}
		for _, d := range donorDirs {
			src := filepath.Join(d, name)
			if data, err := os.ReadFile(src); err == nil && len(data) > 1024 {
				_ = os.WriteFile(dst, data, 0o644)
				break
			}
		}
	}
}

// kernelSecret controller 口令；用 CRYPTO_SECRET 派生，避免多节点共享明文。
func kernelSecret() string {
	seed := strings.TrimSpace(os.Getenv("CRYPTO_SECRET"))
	if seed == "" {
		seed = "new-api-mihomo"
	}
	return "mh-" + strconv.FormatUint(stableHash(seed), 16)
}

func stableHash(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// UsersWithClientNodes 汇总「有启用节点的用户」视图，供 Reload 使用。
// 数据来自 proxy_endpoints 表，按用户分组，保留 Public 标记与 Endpoint ID。
func UsersWithClientNodes() ([]RuntimeUser, error) {
	endpoints, err := model.ListAllEnabledProxyEndpoints()
	if err != nil {
		return nil, err
	}
	byUser := map[int][]RuntimeNode{}
	for _, ep := range endpoints {
		if ep.RawUri == "" && ep.Url == "" {
			continue
		}
		byUser[ep.UserId] = append(byUser[ep.UserId], RuntimeNode{
			ID:     ep.Id,
			Name:   ep.Name,
			Kind:   ep.Kind,
			URL:    ep.Url,
			RawURI: ep.RawUri,
			Public: ep.Public,
		})
	}
	uids := make([]int, 0, len(byUser))
	for uid := range byUser {
		uids = append(uids, uid)
	}
	sort.Ints(uids)
	users := make([]RuntimeUser, 0, len(uids))
	for _, uid := range uids {
		users = append(users, RuntimeUser{UID: uid, Nodes: byUser[uid]})
	}
	return users, nil
}

// ReloadAll rebuilds only bounded preload inventory; selected nodes may load on demand.
func ReloadAll() (Status, error) {
	users, err := UsersWithClientNodes()
	if err != nil {
		manager := ManagerInstance()
		manager.mu.Lock()
		manager.lastErr = "load proxy pool database: " + err.Error()
		status := manager.statusLocked(manager.BinaryPath())
		manager.mu.Unlock()
		return status, fmt.Errorf("load proxy pool database: %w", err)
	}
	return ManagerInstance().Reload(users)
}

// ImportHaizhuNodes 从本机 HaizhuClash 提取目录导入全部（full=7114）或精选（top=791）节点到公共池（public=true）。
func ImportHaizhuNodes(ownerUid int, mode string) (int, error) {
	if ownerUid <= 0 {
		ownerUid = 1
	}
	yamlFile := filepath.Join(HaizhuDefaultExtractDir, "nodes", "clash", "full.yaml")
	if strings.EqualFold(strings.TrimSpace(mode), "top") {
		yamlFile = filepath.Join(HaizhuDefaultExtractDir, "nodes", "clash", "top.yaml")
	}
	raw, err := os.ReadFile(yamlFile)
	if err != nil {
		// 兜底尝试 nodes.txt
		txtFile := filepath.Join(HaizhuDefaultExtractDir, "nodes", "nodes.txt")
		raw, err = os.ReadFile(txtFile)
		if err != nil {
			return 0, fmt.Errorf("read haizhu nodes failed: %w", err)
		}
	}
	nodes, _, parseErr := ParseSubscription(raw, "auto")
	if parseErr != nil {
		return 0, parseErr
	}
	if len(nodes) == 0 {
		return 0, fmt.Errorf("no valid nodes parsed from %s", yamlFile)
	}
	if err := model.DeleteHaizhuEndpoints(); err != nil {
		return 0, err
	}
	endpoints := make([]model.ProxyEndpoint, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			name = n.Kind
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		endpoints = append(endpoints, model.ProxyEndpoint{
			UserId:  ownerUid,
			Name:    name,
			Kind:    n.Kind,
			Url:     n.Url,
			RawUri:  n.RawUri,
			Region:  n.Region,
			Source:  "haizhu",
			Enabled: true,
			Public:  true,
			Weight:  1,
		})
	}
	if err := model.CreateProxyEndpoints(endpoints); err != nil {
		return 0, err
	}
	return len(endpoints), nil
}

// BootstrapBuiltinPool 启动时异步确保 Haizhu 节点已入公共池并拉起 mihomo 内核。
func BootstrapBuiltinPool() {
	go func() {
		time.Sleep(2 * time.Second)
		if count, err := model.CountHaizhuEndpoints(); err != nil {
			common.SysError("[proxy-pool] count builtin nodes: " + err.Error())
		} else if count == 0 {
			if _, statErr := os.Stat(HaizhuDefaultExtractDir); statErr == nil {
				if n, ierr := ImportHaizhuNodes(1, "full"); ierr == nil {
					common.SysLog(fmt.Sprintf("[proxy-pool] auto-imported %d Haizhu public nodes", n))
				} else {
					common.SysError("[proxy-pool] import builtin nodes: " + ierr.Error())
				}
			}
		}
		if _, err := ReloadAll(); err != nil {
			common.SysLog("[proxy-pool] initial mihomo start note: " + err.Error())
		}
	}()
}

// logWriter 把 mihomo 输出写进系统日志，不落明文节点信息。
type logWriter struct{}

func (w *logWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line != "" {
		common.SysLog("[mihomo] " + line)
	}
	return len(p), nil
}
