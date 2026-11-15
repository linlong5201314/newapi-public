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
	Id           int       `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int       `json:"user_id" gorm:"not null;index:idx_sab_user"`
	Provider     string    `json:"provider" gorm:"type:varchar(64);not null;index:idx_sab_provider"`
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

func DeleteSelfBinding(userId, id int) error {
	res := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&SelfAccountBinding{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("binding not found")
	}
	return nil
}

func SetSelfBindingPublic(userId, id, public int) error {
	res := DB.Model(&SelfAccountBinding{}).Where("id = ? AND user_id = ?", id, userId).Update("public", public)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("binding not found")
	}
	return nil
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
