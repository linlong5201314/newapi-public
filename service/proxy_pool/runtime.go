package proxy_pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	// 让出口轮换能用上内核端口：节点全是客户端协议时，URL 列表本来为空。
	model.SetExtraProxyURLProvider(func(userId int) []string {
		return defaultManager.ProxyURLs(userId)
	})
	// 当通过本地 mihomo 端口请求失败时，自动切换到下一个可用节点（无感熔断）。
	model.SetKernelFailoverProvider(func(userId int, proxyURL string) bool {
		return defaultManager.FailoverOnError(userId, proxyURL)
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

// ProxyURLs 该用户可用内核出口；只要内核在跑且该用户有可见节点（自己的或公开的），就返回共享端口。
func (m *Manager) ProxyURLs(userId int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.plan == nil || m.plan.Proxies == 0 {
		return nil
	}
	if len(m.visibleNodesLocked(userId)) == 0 {
		return nil
	}
	port := m.plan.SharedPort
	if port <= 0 {
		port = mihomoBasePort
	}
	return []string{fmt.Sprintf("http://127.0.0.1:%d", port)}
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
		m.lastErr = "no client-type nodes enabled; kernel not started"
		return m.statusLocked(binary), nil
	}

	cmd := exec.Command(binary, "-f", cfgPath, "-d", dir)
	cmd.Stdout = &logWriter{}
	cmd.Stderr = &logWriter{}
	if err := cmd.Start(); err != nil {
		m.lastErr = err.Error()
		return m.statusLocked(binary), err
	}
	m.cmd = cmd
	m.startedAt = time.Now()
	m.lastErr = ""
	if cmd.Process != nil {
		_ = os.WriteFile(filepath.Join(dir, "mihomo.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}

	deadline := time.Now().Add(mihomoWaitTime)
	for time.Now().Before(deadline) {
		if m.controllerReady() {
			break
		}
		time.Sleep(120 * time.Millisecond)
	}
	return m.statusLocked(binary), nil
}

// Select 切换出站节点（校验用户只能选择自己的私有节点或公共池公开节点，禁止切他人私有节点）。
func (m *Manager) Select(userId int, nodeName string) error {
	m.mu.Lock()
	valid := m.visibleNodesLocked(userId)
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
	body, _ := json.Marshal(map[string]string{"name": nodeName})
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
	candidates := m.visibleNodesLocked(userId)
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
	running := m.cmd != nil && m.cmd.Process != nil
	m.mu.Unlock()

	if !running || nodeName == "" {
		if _, err := ReloadAll(); err != nil {
			return 0, err
		}
		m.mu.Lock()
		if m.plan != nil {
			nodeName = m.plan.EndpointNodeName[endpointID]
		}
		m.mu.Unlock()
	}
	if nodeName == "" {
		return 0, fmt.Errorf("node #%d is not loaded in mihomo kernel", endpointID)
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
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	var payload struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
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

func (m *Manager) stopLocked() {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_, _ = m.cmd.Process.Wait()
	}
	m.cmd = nil
	// 清理可能残留的旧 PID 进程，防止端口 23456/23459 被占
	if m.dir != "" {
		if raw, err := os.ReadFile(filepath.Join(m.dir, "mihomo.pid")); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil && pid > 0 {
				if proc, ferr := os.FindProcess(pid); ferr == nil && proc != nil {
					_ = proc.Kill()
				}
			}
		}
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
	if m.plan != nil {
		status.Proxies = m.plan.Proxies
		status.PublicCount = len(m.plan.PublicNodes)
		status.Ports = m.plan.Ports
		status.Groups = m.plan.Groups
	}
	if m.cmd != nil && m.cmd.Process != nil {
		status.Running = true
		status.StartedAt = m.startedAt.Unix()
	}
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
	return resp.StatusCode < 400
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

// ReloadAll 便捷入口：拉全量节点并重载内核。
func ReloadAll() (Status, error) {
	users, err := UsersWithClientNodes()
	if err != nil {
		return ManagerInstance().Status(), err
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
		if count, err := model.CountHaizhuEndpoints(); err == nil && count == 0 {
			if _, statErr := os.Stat(HaizhuDefaultExtractDir); statErr == nil {
				if n, ierr := ImportHaizhuNodes(1, "full"); ierr == nil {
					common.SysLog(fmt.Sprintf("[proxy-pool] auto-imported %d Haizhu public nodes", n))
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
