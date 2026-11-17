package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// SelfAccountBinding 用户提交的上游账号（账号----密码 多行），用于账号授权型反代。
// 账号密文存 accounts_enc，明文只在离线上架/转发时解密使用；普通用户 API 只返回掩码。
type SelfAccountBinding struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId   int    `json:"user_id" gorm:"not null;index:idx_sab_user"`
	Provider string `json:"provider" gorm:"type:varchar(64);not null;index:idx_sab_provider"`
	// Kind 绑定类型：password=账号密码（账号型反代）、token=会话Token（密码登录被上游设备风控拦截时改用）、
	// mixed=账号密码与会话Token混填、oauth=授权登录（粘贴授权码/Token）、gateway=配套网关（如 magpie，一用户一实例）
	Kind string `json:"kind" gorm:"type:varchar(16);not null;default:'password'"`
	// Proxy 出口代理：''=跟随平台默认（用户出口池）、'direct'=强制直连、其它=固定代理 URL（http/https/socks5）。
	Proxy        string    `json:"proxy" gorm:"type:varchar(255);not null;default:''"`
	DisplayName  string    `json:"display_name" gorm:"type:varchar(64);default:''"`
	AccountsEnc  string    `json:"-" gorm:"type:text;not null"`
	AccountMask  string    `json:"account_mask" gorm:"type:text"`
	AccountCount int       `json:"account_count" gorm:"not null;default:0"`
	Public       int       `json:"public" gorm:"not null;default:0"`
	Status       string    `json:"status" gorm:"type:varchar(16);not null;default:'pending';index:idx_sab_status"`
	ChannelId    int       `json:"channel_id" gorm:"not null;default:0"`
	ServiceRef   string    `json:"service_ref" gorm:"type:varchar(128);default:''"`
	LastError    string    `json:"last_error" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DeepSeekDeviceProfile 每个账号专属的虚拟设备指纹与全套客户端请求头。
type DeepSeekDeviceProfile struct {
	DeviceId       string            `json:"device_id"`
	DsSessionId    string            `json:"ds_session_id"`
	HwWafSesId     string            `json:"hwwafsesid"`
	HwWafSesTime   string            `json:"hwwafsestime"`
	ClientPlatform string            `json:"x_client_platform"`
	ClientVersion  string            `json:"x_client_version"`
	AppVersion     string            `json:"x_app_version"`
	ClientLocale   string            `json:"x_client_locale"`
	UserAgent      string            `json:"user_agent"`
	Cookie         string            `json:"cookie"`
	Headers        map[string]string `json:"headers"`
}

// BuildDeepSeekDeviceProfile 根据账号或 Token 确定性派生一套唯一的虚拟设备指纹（同号固定、异号隔离）。
func BuildDeepSeekDeviceProfile(seed string) *DeepSeekDeviceProfile {
	trimmed := strings.TrimSpace(seed)
	if trimmed == "" {
		trimmed = "default-ds-seed"
	}
	h1 := sha256.Sum256([]byte("ds-dev-v1:" + trimmed))
	h2 := sha256.Sum256([]byte("ds-ses-v1:" + trimmed))
	hex1 := hex.EncodeToString(h1[:])
	hex2 := hex.EncodeToString(h2[:])

	// UUIDv4 形态的独立 device_id
	deviceId := fmt.Sprintf("%s-%s-4%s-a%s-%s",
		hex1[0:8], hex1[8:12], hex1[13:16], hex1[17:20], hex1[20:32])
	dsSessionId := hex2[0:32]
	hwWafId := hex1[32:50]
	// 固定在 2026 年内的合理时间戳但每号微调
	baseTs := int64(1762000000) + int64(h1[0])*86400 + int64(h1[1])*300
	hwWafTime := fmt.Sprintf("%d", baseTs)
	chromeBuild := 100 + int(h1[2])%90
	ua := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.6998.%d Safari/537.36", chromeBuild)
	cookie := fmt.Sprintf("HWWAFSESID=%s; HWWAFSESTIME=%s; ds_session_id=%s", hwWafId, hwWafTime, dsSessionId)

	headers := map[string]string{
		"User-Agent":        ua,
		"Accept":            "*/*",
		"Accept-Language":   "zh-CN,zh;q=0.9,en;q=0.8",
		"Origin":            "https://chat.deepseek.com",
		"Referer":           "https://chat.deepseek.com/",
		"x-client-platform": "web",
		"x-client-version":  "1.7.0",
		"x-app-version":     "20241129.1",
		"x-client-locale":   "zh_CN",
		"x-device-id":       deviceId,
		"Cookie":            cookie,
	}

	return &DeepSeekDeviceProfile{
		DeviceId:       deviceId,
		DsSessionId:    dsSessionId,
		HwWafSesId:     hwWafId,
		HwWafSesTime:   hwWafTime,
		ClientPlatform: "web",
		ClientVersion:  "1.7.0",
		AppVersion:     "20241129.1",
		ClientLocale:   "zh_CN",
		UserAgent:      ua,
		Cookie:         cookie,
		Headers:        headers,
	}
}

// SelfAccountCred 单条账号凭据（自带该号专属的虚拟设备指纹）。
type SelfAccountCred struct {
	Account  string                 `json:"account"`
	Password string                 `json:"password"`
	Device   *DeepSeekDeviceProfile `json:"device,omitempty"`
}

// ParseAccountLines 解析「账号----密码」多行文本，兼容 ---- / , / | / Tab 分隔，忽略空行与 # 注释。
func ParseAccountLines(raw string) ([]SelfAccountCred, error) {
	var out []SelfAccountCred
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := "----"
		if !strings.Contains(line, sep) {
			switch {
			case strings.Contains(line, "\t"):
				sep = "\t"
			case strings.Contains(line, "|"):
				sep = "|"
			case strings.Contains(line, ","):
				sep = ","
			default:
				return nil, fmt.Errorf("账号格式不对：%s（应为 账号----密码）", line)
			}
		}
		parts := strings.SplitN(line, sep, 2)
		acc := strings.TrimSpace(parts[0])
		pwd := strings.TrimSpace(parts[1])
		if acc == "" || pwd == "" {
			return nil, fmt.Errorf("账号或密码为空：%s", line)
		}
		key := strings.ToLower(acc)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, SelfAccountCred{Account: acc, Password: pwd})
	}
	if len(out) == 0 {
		return nil, errors.New("没有解析到任何账号，请按 账号----密码 每行一个填写")
	}
	if len(out) > 50 {
		return nil, errors.New("单个绑定最多 50 个账号")
	}
	return out, nil
}

func maskAccount(acc string) string {
	acc = strings.TrimSpace(acc)
	if len(acc) <= 4 {
		return acc[:1] + "***"
	}
	if i := strings.Index(acc, "@"); i > 2 {
		return acc[:2] + "***" + acc[i:]
	}
	return acc[:2] + "***" + acc[len(acc)-2:]
}

func (b *SelfAccountBinding) EncodeAccounts(creds []SelfAccountCred) error {
	for i := range creds {
		if creds[i].Device == nil {
			seed := creds[i].Account + "|" + creds[i].Password
			creds[i].Device = BuildDeepSeekDeviceProfile(seed)
		}
	}
	raw, err := common.Marshal(creds)
	if err != nil {
		return err
	}
	enc, err := common.EncryptSecret(string(raw))
	if err != nil {
		return err
	}
	b.AccountsEnc = enc
	b.AccountCount = len(creds)
	masks := make([]string, 0, len(creds))
	for _, c := range creds {
		label := c.Account
		if strings.TrimSpace(label) == "" {
			label = c.Password
		}
		devSuffix := ""
		if c.Device != nil && len(c.Device.DeviceId) >= 8 {
			devSuffix = "[dev:" + c.Device.DeviceId[:8] + "]"
		}
		masks = append(masks, maskAccount(label)+devSuffix)
	}
	b.AccountMask = strings.Join(masks, ", ")
	return nil
}

// DecodeAccounts 解密账号列表（仅服务端使用）。
func (b *SelfAccountBinding) DecodeAccounts() ([]SelfAccountCred, error) {
	if strings.TrimSpace(b.AccountsEnc) == "" {
		return nil, errors.New("empty binding")
	}
	raw := common.DecryptSecretOrLegacy(b.AccountsEnc)
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("decrypt binding failed")
	}
	var creds []SelfAccountCred
	if err := common.Unmarshal([]byte(raw), &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func CreateSelfBinding(b *SelfAccountBinding) error {
	if b.Provider == "" {
		return errors.New("provider required")
	}
	if b.Status == "" {
		b.Status = "pending"
	}
	return DB.Create(b).Error
}

func ListSelfBindings(userId int) ([]SelfAccountBinding, error) {
	var rows []SelfAccountBinding
	err := DB.Where("user_id = ?", userId).Order("id desc").Find(&rows).Error
	return rows, err
}

func ListAllBindings(limit int) ([]SelfAccountBinding, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows []SelfAccountBinding
	err := DB.Order("id desc").Limit(limit).Find(&rows).Error
	return rows, err
}

func DeleteSelfBinding(userId, id int) (int, error) {
	var row SelfAccountBinding
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errors.New("binding not found")
		}
		return 0, err
	}
	if err := DB.Delete(&SelfAccountBinding{}, row.Id).Error; err != nil {
		return 0, err
	}
	return row.ChannelId, nil
}

// DisableSelfChannel 把该用户绑定的渠道下线（status=3=手动禁用），避免删除绑定后渠道继续被路由。
func DisableSelfChannel(ownerId, channelId int) error {
	if channelId <= 0 {
		return nil
	}
	return DB.Model(&Channel{}).Where("id = ? AND owner_id = ?", channelId, ownerId).Update("status", 3).Error
}

func SetSelfBindingPublic(userId, id, public int) (int, error) {
	var row SelfAccountBinding
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errors.New("binding not found")
		}
		return 0, err
	}
	if err := DB.Model(&SelfAccountBinding{}).Where("id = ? AND user_id = ?", id, userId).Update("public", public).Error; err != nil {
		return 0, err
	}
	// 绑定公开状态与渠道可见性必须同步，否则“公开”只改了开关、模型仍不可被别人调用。
	if row.ChannelId > 0 {
		if err := DB.Model(&Channel{}).Where("id = ? AND owner_id = ?", row.ChannelId, userId).Update("visibility", public).Error; err != nil {
			return row.ChannelId, err
		}
	}
	return row.ChannelId, nil
}

func PendingBindings(limit int) ([]SelfAccountBinding, error) {
	if limit <= 0 {
		limit = 5
	}
	var rows []SelfAccountBinding
	err := DB.Where("status = ?", "pending").Order("id asc").Limit(limit).Find(&rows).Error
	return rows, err
}

func MarkBindingStatus(id int, status string, channelId int, serviceRef string, errMsg string) error {
	updates := map[string]any{"status": status, "last_error": errMsg}
	if channelId > 0 {
		updates["channel_id"] = channelId
	}
	if serviceRef != "" {
		updates["service_ref"] = serviceRef
	}
	return DB.Model(&SelfAccountBinding{}).Where("id = ?", id).Updates(updates).Error
}

func GetBindingById(id int) (*SelfAccountBinding, error) {
	var b SelfAccountBinding
	err := DB.Where("id = ?", id).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("binding not found")
	}
	return &b, err
}

// IsLikelySessionToken 判断第二段是「会话 token」而不是「密码」。
// 用途：ds2api 这类网页反代，上游对数据中心 IP 的账号密码登录会返回
// RISK_DEVICE_DETECTED，只能改用浏览器登录后拿到的会话 token（寿命数月，如 64 位 Base64 串）。
func IsLikelySessionToken(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 40 {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	// JWT 三段式，或 DeepSeek 的裸 token（如 SlCmRF5j+IX2LBorDe4nBY5OHI1F8bmSp2HEkCsAX1pPWJ38B7GOPyYhcqGzoCMD）
	if strings.HasPrefix(s, "eyJ") || strings.HasPrefix(s, "jt-") || strings.HasPrefix(s, "sk-") || strings.HasPrefix(s, "ink-") {
		return true
	}
	return strings.Count(s, ".") >= 2 || len(s) >= 48
}

var bearerExtractRe = regexp.MustCompile(`(?i)(?:authorization\s*[:=]\s*Bearer\s+|^Bearer\s+)([A-Za-z0-9+/=._-]{32,})`)
var jsonValueTokenRe = regexp.MustCompile(`"value"\s*:\s*"([A-Za-z0-9+/=._-]{32,})"`)

// ExtractBearerTokensFromRaw 从整段抓包请求头或 JSON 文本中自动提取 Bearer Token。
func ExtractBearerTokensFromRaw(raw string) []string {
	var tokens []string
	seen := map[string]bool{}
	for _, m := range bearerExtractRe.FindAllStringSubmatch(raw, -1) {
		if len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			tokens = append(tokens, m[1])
		}
	}
	for _, m := range jsonValueTokenRe.FindAllStringSubmatch(raw, -1) {
		if len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			tokens = append(tokens, m[1])
		}
	}
	return tokens
}

// ParseAccountOrTokenLines 解析账号型反代的凭据，一行一个，支持四种写法：
//
//	账号----密码      （走账号密码登录）
//	账号----会话Token （第二段够长且像 token 时自动识别，走 token 注入）
//	裸 Token          （整行就是一个 token，账号留空，由上游自行识别）
//	抓包请求头整段粘贴 （自动提取 authorization: Bearer <token>）
//
// 识别出来的 token 存在 Password 字段，并自动生成该号独立的虚拟设备指纹 Device。
func ParseAccountOrTokenLines(raw string) ([]SelfAccountCred, error) {
	var out []SelfAccountCred
	seen := map[string]bool{}

	// 如果用户直接粘贴了包含 authorization: Bearer xxx 的抓包文本，优先自动提取其中的 Token
	if extracted := ExtractBearerTokensFromRaw(raw); len(extracted) > 0 {
		for _, tok := range extracted {
			out = append(out, SelfAccountCred{
				Account:  "",
				Password: tok,
				Device:   BuildDeepSeekDeviceProfile(tok),
			})
		}
		return out, nil
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var acc, secret string
		if idx := strings.Index(line, "----"); idx >= 0 {
			acc = strings.TrimSpace(line[:idx])
			secret = strings.TrimSpace(line[idx+4:])
		} else if IsLikelySessionToken(line) {
			acc = ""
			secret = line
		} else {
			switch {
			case strings.Contains(line, "\t"):
				p := strings.SplitN(line, "\t", 2)
				acc, secret = strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
			case strings.Contains(line, "|"):
				p := strings.SplitN(line, "|", 2)
				acc, secret = strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
			default:
				return nil, fmt.Errorf("格式不对：%s（应为 账号----密码，或 账号----会话Token，或直接粘贴 Token / 抓包头）", line)
			}
		}
		if secret == "" {
			return nil, fmt.Errorf("密码/Token 为空：%s", line)
		}
		key := strings.ToLower(acc + "|" + secret)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, SelfAccountCred{
			Account:  acc,
			Password: secret,
			Device:   BuildDeepSeekDeviceProfile(acc + "|" + secret),
		})
	}
	if len(out) == 0 {
		return nil, errors.New("没有解析到任何凭据，请按 账号----密码 或 账号----会话Token 或直接粘贴 Token 填写")
	}
	if len(out) > 50 {
		return nil, errors.New("单个绑定最多 50 条凭据")
	}
	return out, nil
}

// ParseOAuthToken 解析授权登录凭据：一行一个授权码 / 回调链接 / Token，
// 形如 https://host/callback?code=xxxx、token=xxxx 或直接粘贴 token。
func ParseOAuthToken(raw string) ([]SelfAccountCred, error) {
	var out []SelfAccountCred
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		val := line
		for _, marker := range []string{"code=", "token=", "access_token=", "refresh_token=", "id_token="} {
			if idx := strings.Index(val, marker); idx >= 0 {
				rest := val[idx+len(marker):]
				if end := strings.IndexAny(rest, "&#"); end >= 0 {
					rest = rest[:end]
				}
				if rest != "" {
					val = rest
					break
				}
			}
		}
		if seen[val] {
			continue
		}
		seen[val] = true
		out = append(out, SelfAccountCred{Account: val, Password: ""})
	}
	if len(out) == 0 {
		return nil, errors.New("没有解析到授权凭据：请粘贴浏览器授权后的回调链接或 Token，每行一个")
	}
	if len(out) > 10 {
		return nil, errors.New("单个绑定最多 10 条授权凭据")
	}
	return out, nil
}

// ParseGatewayKeys 解析配套网关的上游凭据：每行「供应商----密钥」（也接受 \t / | 分隔），
// 例如 deepseek----sk-xxx；留空表示先开通网关、稍后再加。
func ParseGatewayKeys(raw string) ([]SelfAccountCred, error) {
	var out []SelfAccountCred
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := "----"
		if !strings.Contains(line, sep) {
			switch {
			case strings.Contains(line, "\t"):
				sep = "\t"
			case strings.Contains(line, "|"):
				sep = "|"
			default:
				return nil, fmt.Errorf("上游凭据格式不对：%s（应为 供应商----密钥，例如 deepseek----sk-xxx）", line)
			}
		}
		parts := strings.SplitN(line, sep, 2)
		name := strings.TrimSpace(parts[0])
		key := strings.TrimSpace(parts[1])
		if name == "" || key == "" {
			return nil, fmt.Errorf("供应商或密钥为空：%s", line)
		}
		k := strings.ToLower(name + "|" + key)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, SelfAccountCred{Account: name, Password: key})
	}
	if len(out) > 20 {
		return nil, errors.New("单个网关绑定最多 20 个上游凭据")
	}
	return out, nil
}
