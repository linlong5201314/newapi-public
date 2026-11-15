package model

import (
	"os"

	"github.com/QuantumNous/new-api/common"
)

// MailOutbox 是「平台自建邮箱投递队列」。
//
// 背景：Railway 免费实例封禁出站 SMTP(25/465/587)，应用直接连 smtp.qq.com 会一直挂住，
// 注册验证码发不出去。这里把待发邮件落库，由面板(本机常驻, 国内网络可直连 QQ SMTP)
// 轮询 mail_outbox 并用本机 SMTP 发送。
//
// 启用条件：环境变量 MAIL_OUTBOX_MODE=1。
type MailOutbox struct {
	Id         int64  `json:"id" gorm:"primaryKey"`
	Receiver   string `json:"receiver" gorm:"type:text;not null"`
	Subject    string `json:"subject" gorm:"type:text;not null"`
	Content    string `json:"content" gorm:"type:text;not null"`
	Status     int    `json:"status" gorm:"default:0;index:idx_mail_outbox_status"` // 0=pending 1=sent 2=failed
	Attempts   int    `json:"attempts" gorm:"default:0"`
	LastError  string `json:"last_error" gorm:"type:text"`
	CreatedAt  int64  `json:"created_at" gorm:"index:idx_mail_outbox_status"`
	SentAt     int64  `json:"sent_at"`
	CreatorTag string `json:"creator_tag" gorm:"type:text"`
}

func (MailOutbox) TableName() string {
	return "mail_outbox"
}

// InitMailOutbox 在开启 MAIL_OUTBOX_MODE 时接管 common.SendEmail 的投递。
func InitMailOutbox() {
	if os.Getenv("MAIL_OUTBOX_MODE") != "1" {
		return
	}
	common.MailOutboxSink = func(subject string, receiver string, content string) error {
		row := &MailOutbox{
			Receiver:  receiver,
			Subject:   subject,
			Content:   content,
			Status:    0,
			CreatedAt: common.GetTimestamp(),
		}
		if err := DB.Create(row).Error; err != nil {
			return err
		}
		common.SysLog(common.LogText("mail queued to outbox id=%d receiver=%s", row.Id, receiver))
		return nil
	}
	common.SysLog(common.LogText("mail outbox mode enabled: emails are queued for the local panel sender"))
}
