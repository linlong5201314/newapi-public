package proxy_pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 内核（mihomo）常驻进程管理：生成配置 → 拉起子进程 → 暴露每个用户的本地 mixed 口 →
// 通过 external-controller 切节点。二进制不在时保持「未启用」状态，不阻塞任何接口。

const (
	mihomoCtrlPort = 23459
	mihomoBasePort = 23456
	mihomoWaitTime = 6 * time.Second
)

// Manager 全局单例：一台 mihomo 服务所有用户，每用户一个 listener。
type Manager struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	plan      *RuntimePlan
	cfgPath   string
	dir       string
	lastErr   string
	startedAt time.Time
}

var defaultManager = &Manager{}

// ManagerInstance 返回全局内核管理器。
func ManagerInstance() *Manager { return defaultManager }

func init() {
	// 让出口轮换能用上内核端口：节点全是客户端协议时，URL 列表本来为空。
	model.SetExtraProxyURLProvider(func(userId int) []string {
		return defaultManager.ProxyURLs(userId)
	})
}

// BinaryPath 返回 mihomo 可执行文件路径；找不到返回空串。
func (m *Manager) BinaryPath() string {
	if custom := strings.TrimSpace(os.Getenv("MIHOMO_PATH")); custom != "" {
		return custom
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
	Available  bool           `json:"available"`
	Running    bool           `json:"running"`
	Binary     string         `json:"binary"`
	Proxies    int            `json:"proxies"`
	LastError  string         `json:"last_error"`
	StartedAt  int64          `json:"started_at"`
	Ports      map[int]int    `json:"ports"`
	Groups     map[int]string `json:"groups"`
	ConfigPath string         `json:"config_path"`
}

// Status 返回当前状态。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	binary := m.BinaryPath()
	status := Status{
		Available:  binary != "",
		Binary:     binary,
		LastError:  m.lastErr,
		ConfigPath: m.cfgPath,
		Ports:      map[int]int{},
		Groups:     map[int]string{},
	}
	if m.plan != nil {
		status.Proxies = m.plan.Proxies
		status.Ports = m.plan.Ports
		status.Groups = m.plan.Groups
	}
	if m.cmd != nil && m.cmd.Process != nil {
		status.Running = true
		status.StartedAt = m.startedAt.Unix()
	}
	return status
}

// ProxyURLs 该用户可用内核出口；没有 listener 时返回 nil。
func (m *Manager) ProxyURLs(userId int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.plan == nil {
		return nil
	}
	port, ok := m.plan.Ports[userId]
	if !ok {
		return nil
	}
	return []string{fmt.Sprintf("http://127.0.0.1:%d", port)}
}

// NodeNames 该用户在内核里的节点名（可选）。
func (m *Manager) NodeNames(userId int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.plan == nil {
		return nil
	}
	return append([]string(nil), m.plan.NodeNames[userId]...)
}

// Reload 用最新节点重建配置并（重）启动内核。
func (m *Manager) Reload(users []RuntimeUser) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	binary := m.BinaryPath()
	if binary == "" {
		m.lastErr = "mihomo binary not found; set MIHOMO_PATH or put mihomo in PATH"
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
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, plan.YAML, 0o600); err != nil {
		m.lastErr = err.Error()
		return m.statusLocked(binary), err
	}

	m.stopLocked()
	m.cfgPath = cfgPath
	m.plan = plan

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

	// mihomo 启动是异步的：等 controller 起来，避免前端立刻切节点失败。
	deadline := time.Now().Add(mihomoWaitTime)
	for time.Now().Before(deadline) {
		if m.controllerReady() {
			break
		}
		time.Sleep(120 * time.Millisecond)
	}
	return m.statusLocked(binary), nil
}

// Select 切换某用户当前出站节点（节点名必须是 NodeNames 里的）。
func (m *Manager) Select(userId int, nodeName string) error {
	m.mu.Lock()
	group, ok := "", false
	if m.plan != nil {
		group, ok = m.plan.Groups[userId]
	}
	valid := append([]string(nil), m.plan.NodeNames[userId]...)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("user %d has no kernel listener", userId)
	}
	if nodeName == "" || !containsString(valid, nodeName) {
		return fmt.Errorf("unknown node %q", nodeName)
	}
	body, _ := json.Marshal(map[string]string{"name": nodeName})
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("http://127.0.0.1:%d/proxies/%s", mihomoCtrlPort, group),
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
}

func (m *Manager) statusLocked(binary string) Status {
	status := Status{
		Available:  binary != "",
		Binary:     binary,
		LastError:  m.lastErr,
		ConfigPath: m.cfgPath,
		Ports:      map[int]int{},
		Groups:     map[int]string{},
	}
	if m.plan != nil {
		status.Proxies = m.plan.Proxies
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

// UsersWithClientNodes 汇总「有启用且带客户端节点的用户」视图，供 Reload 使用。
// 数据来自 proxy_endpoints 表，按用户分组。
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
			Name:   ep.Name,
			Kind:   ep.Kind,
			URL:    ep.Url,
			RawURI: ep.RawUri,
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

func io_logWriter() *logWriter { return &logWriter{} }

// logWriter 把 mihomo 输出写进系统日志，不落明文节点信息。
type logWriter struct{}

func (w *logWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line != "" {
		common.SysLog("[mihomo] " + line)
	}
	return len(p), nil
}
