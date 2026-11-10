// site_custom.go custom 通用签到模板（api 模式）：
// {名称, 端点, 方法, 请求模板, 成功判定 JSONPath, 凭据}，用户可自建任意站点签到。
package checkin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// customConfig custom 站点配置（存 CheckinAccount.CredJson）。
type customConfig struct {
	Name        string            `json:"name"`         // 展示名
	Endpoint    string            `json:"endpoint"`     // 签到端点（SSRF 校验）
	Method      string            `json:"method"`       // GET | POST（默认 POST）
	Body        string            `json:"body"`         // 请求模板（原文发送；空=无 body）
	Headers     map[string]string `json:"headers"`      // 自定义头（凭据放这里，如 Cookie/Authorization）
	SuccessPath string            `json:"success_path"` // 成功判定 JSONPath（如 "code" == 0 或 "data.checked_in" == true）
	Expect      string            `json:"expect"`       // 期望值（缺省按真值判定："0"/"true"/非空）
}

// customHTTP custom 站点专用 client。
var customHTTP = &http.Client{Timeout: 60 * time.Second}

// runCustom 执行 custom 通用模板签到。
func runCustom(acc *model.CheckinAccount) *RunResult {
	var cfg customConfig
	if err := json.Unmarshal([]byte(model.DecryptCheckinCred(acc.CredJson)), &cfg); err != nil {
		return &RunResult{Success: false, Message: "custom 配置解析失败（需 JSON: endpoint/method/body/headers/success_path）: " + err.Error(), Balance: -1}
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return &RunResult{Success: false, Message: "custom 配置缺 endpoint", Balance: -1}
	}
	if err := ValidateExternalURL(cfg.Endpoint); err != nil {
		return &RunResult{Success: false, Message: "endpoint 不合法（SSRF 防护）: " + err.Error(), Balance: -1}
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodGet && method != http.MethodPost {
		return &RunResult{Success: false, Message: "method 仅支持 GET/POST", Balance: -1}
	}
	var body io.Reader
	if cfg.Body != "" && method == http.MethodPost {
		body = strings.NewReader(cfg.Body)
	}
	req, err := http.NewRequest(method, cfg.Endpoint, body)
	if err != nil {
		return &RunResult{Success: false, Message: err.Error(), Balance: -1}
	}
	hasCT := false
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
		if strings.EqualFold(k, "Content-Type") {
			hasCT = true
		}
	}
	if !hasCT && cfg.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := customHTTP.Do(req)
	if err != nil {
		return &RunResult{Success: false, Message: "请求失败: " + err.Error(), Balance: -1}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return &RunResult{Success: false, Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200)), Balance: -1}
	}
	ok, verdict := judgeCustomSuccess(cfg, raw)
	if !ok {
		return &RunResult{Success: false, Message: "成功判定未通过: " + verdict, Balance: -1}
	}
	name := cfg.Name
	if name == "" {
		name = "custom"
	}
	return &RunResult{Success: true, Message: fmt.Sprintf("%s 签到成功（%s）", name, verdict), Balance: -1}
}

// judgeCustomSuccess 按 success_path + expect 判定响应 JSON。
// success_path 为空时仅按 HTTP 2xx 判成功（已在调用方保证）。
func judgeCustomSuccess(cfg customConfig, raw []byte) (bool, string) {
	if strings.TrimSpace(cfg.SuccessPath) == "" {
		return true, "HTTP 2xx"
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, "响应非 JSON，无法按 " + cfg.SuccessPath + " 判定"
	}
	val, found := jsonPathGet(doc, cfg.SuccessPath)
	if !found {
		return false, cfg.SuccessPath + " 不存在"
	}
	if cfg.Expect != "" {
		return scalarEqual(val, cfg.Expect), fmt.Sprintf("%s=%v (expect %s)", cfg.SuccessPath, val, cfg.Expect)
	}
	// 缺省真值判定：bool true / 数字 0 / 非空字符串均视为成功。
	switch v := val.(type) {
	case bool:
		return v, fmt.Sprintf("%s=%v", cfg.SuccessPath, v)
	case float64:
		return v == 0, fmt.Sprintf("%s=%v", cfg.SuccessPath, v)
	case string:
		return v != "", fmt.Sprintf("%s=%q", cfg.SuccessPath, v)
	case nil:
		return false, cfg.SuccessPath + " 为 null"
	}
	return true, fmt.Sprintf("%s=%v", cfg.SuccessPath, val)
}

// jsonPathGet 极简 JSONPath（dot 路径：a.b.c；数组下标 a.b.0.c）。
func jsonPathGet(doc any, path string) (any, bool) {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return nil, false
		}
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// scalarEqual 标量宽松相等（字符串化后比较）。
func scalarEqual(val any, expect string) bool {
	switch v := val.(type) {
	case string:
		return v == expect
	case bool:
		return strconv.FormatBool(v) == strings.ToLower(expect)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64) == expect
	case nil:
		return expect == "null" || expect == ""
	default:
		return fmt.Sprintf("%v", v) == expect
	}
}
