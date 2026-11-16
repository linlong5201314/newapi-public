package model

import (
	"errors"
	"fmt"
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
	// Kind 绑定类型：password=账号密码（账号型反代）、oauth=授权登录（粘贴授权码/Token）、gateway=配套网关（如 magpie，一用户一实例）
	Kind         string    `json:"kind" gorm:"type:varchar(16);not null;default:'password'"`
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

// SelfAccountCred 单条账号凭据。
type SelfAccountCred struct {
	Account  string `json:"account"`
	Password string `json:"password"`
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
		masks = append(masks, maskAccount(c.Account))
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
