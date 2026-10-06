// channels.go P6-M9 通知渠道泛化：webhook（企微/钉钉/飞书，既有）之外补
// Telegram Bot 与 SMTP 邮件。渠道凭据是平台级设置（AES 落库），群上只存
// 投递目标（chat id / 收件人）。限速与 DLP 同既有链路（acquire 按群闸门，
// 内容在上游 deliver 已过 DLP）。
package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/custos-machina/backend/internal/modules/identity"
	"mime"
	"net/smtp"
	"regexp"
	"strings"
)

// 渠道取值（Telegram 已移除：国内不用，用户 2026-10-05 定调）。
const (
	ChannelWebhook = "webhook"
	ChannelSMTP    = "smtp"
)

// 平台级渠道凭据设置键（凭据 AES）。
const (
	settingSMTPHost = "notify.smtp_host"
	settingSMTPPort = "notify.smtp_port"
	settingSMTPUser = "notify.smtp_user"
	settingSMTPPass = "notify.smtp_pass" // AES
	settingSMTPFrom = "notify.smtp_from"
)

// ChannelSettingsOut 渠道设置视图（凭据只回"已配置"状态）。
type ChannelSettingsOut struct {
	SMTPHost       string `json:"smtpHost"`
	SMTPPort       string `json:"smtpPort"`
	SMTPUser       string `json:"smtpUser"`
	SMTPFrom       string `json:"smtpFrom"`
	SMTPConfigured bool   `json:"smtpConfigured"`
}

// 密文字段绑定（GCM AAD）：跨字段粘贴密文解不开（v0.12.1 复核 N4）
const (
	aadGroupWebhook   = "notify_groups.webhook"
	aadNotifySMTPPass = "platform_settings.notify_smtp_pass"
)

func (s *Service) setSetting(ctx context.Context, key, value string) error {
	return identity.UpsertSetting(s.db, ctx, key, value) // 方言安全 upsert
}

// SaveChannelSettings 保存渠道凭据（留空保留；token/pass AES）。
func (s *Service) SaveChannelSettings(ctx context.Context, smtpHost, smtpPort, smtpUser, smtpPass, smtpFrom string) error {
	if smtpHost != "" {
		if err := s.setSetting(ctx, settingSMTPHost, smtpHost); err != nil {
			return err
		}
	}
	if smtpPort != "" {
		if err := s.setSetting(ctx, settingSMTPPort, smtpPort); err != nil {
			return err
		}
	}
	if smtpUser != "" {
		if err := s.setSetting(ctx, settingSMTPUser, smtpUser); err != nil {
			return err
		}
	}
	if smtpFrom != "" {
		if err := s.setSetting(ctx, settingSMTPFrom, smtpFrom); err != nil {
			return err
		}
	}
	if smtpPass != "" {
		if s.cipher == nil {
			return fmt.Errorf("平台主密钥未配置，无法加密凭据")
		}
		enc, err := s.cipher.Encrypt(smtpPass, aadNotifySMTPPass)
		if err != nil {
			return err
		}
		if err := s.setSetting(ctx, settingSMTPPass, enc); err != nil {
			return err
		}
	}
	return nil
}

// ChannelSettings 读取渠道设置（不回明文凭据）。
func (s *Service) ChannelSettings(ctx context.Context) ChannelSettingsOut {
	out := ChannelSettingsOut{}
	out.SMTPHost, _, _ = s.setting(ctx, settingSMTPHost)
	out.SMTPPort, _, _ = s.setting(ctx, settingSMTPPort)
	out.SMTPUser, _, _ = s.setting(ctx, settingSMTPUser)
	out.SMTPFrom, _, _ = s.setting(ctx, settingSMTPFrom)
	if v, ok, _ := s.setting(ctx, settingSMTPPass); ok {
		out.SMTPConfigured = v != ""
	}
	return out
}

// SMTPConfig 一次发信所需的连接信息（Service.Send 解密后传入 sender）。
type SMTPConfig struct {
	Host, Port, User, Pass, From string
}

// SendSMTP 标准发信：标题 + 纯文本正文（markdown 记号降级剥离）。
func (s *sender) SendSMTP(ctx context.Context, groupID uint, cfg SMTPConfig, to, title, content string) error {
	if err := s.acquire(groupID); err != nil {
		return err
	}
	tos := []string{}
	for _, t := range strings.Split(to, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tos = append(tos, t)
		}
	}
	if len(tos) == 0 {
		return fmt.Errorf("收件人为空")
	}
	from := cfg.From
	if from == "" {
		from = cfg.User
	}
	subject := mime.QEncoding.Encode("utf-8", "["+title+"] CustosMachina")
	body := stripMarkdown(content)
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + strings.Join(tos, ", "),
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")

	addr := cfg.Host + ":" + cfg.Port
	var auth smtp.Auth
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	}
	// 465 端口需要隐式 TLS（SSL），587/25 用 STARTTLS；
	// net/smtp.SendMail 只支持 STARTTLS，不支持隐式 TLS
	if cfg.Port == "465" {
		return sendMailTLS(addr, cfg.Host, auth, from, tos, []byte(msg))
	}
	return smtp.SendMail(addr, auth, from, tos, []byte(msg))
}

// sendMailTLS 隐式 TLS 发信（465 端口：TLS 握手后走标准 SMTP 协议）。
func sendMailTLS(addr, host string, auth smtp.Auth, from string, tos []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("TLS 连接失败: %w", err)
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP 客户端创建失败: %w", err)
	}
	defer c.Close()
	if auth != nil {
		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err = c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM 失败: %w", err)
	}
	for _, to := range tos {
		if err = c.Rcpt(to); err != nil {
			return fmt.Errorf("RCPT TO %s 失败: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA 失败: %w", err)
	}
	if _, err = w.Write(msg); err != nil {
		return fmt.Errorf("写邮件失败: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("关闭写入失败: %w", err)
	}
	return c.Quit()
}

var mdBoldRe = regexp.MustCompile(`\*\*(.*?)\*\*`)
var mdCodeRe = regexp.MustCompile("`([^`]*)`")
var mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)

// stripMarkdown 邮件正文降级：剥离加粗/行内码/链接记号与标题井号。
func stripMarkdown(s string) string {
	s = mdLinkRe.ReplaceAllString(s, "$1（$2）")
	s = mdBoldRe.ReplaceAllString(s, "$1")
	s = mdCodeRe.ReplaceAllString(s, "$1")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimLeft(l, "#> ")
	}
	return strings.Join(lines, "\n")
}

// Send 渠道分派（Service 侧解密凭据后进入）。
func (s *Service) sendByChannel(ctx context.Context, group *Group, title, content string) error {
	switch group.Channel {
	case ChannelSMTP:
		var cfg SMTPConfig
		cfg.Host, _, _ = s.setting(ctx, settingSMTPHost)
		cfg.Port, _, _ = s.setting(ctx, settingSMTPPort)
		if cfg.Host == "" || cfg.Port == "" {
			return fmt.Errorf("SMTP 未配置（管理后台「通知群聊」通道设置）")
		}
		cfg.User, _, _ = s.setting(ctx, settingSMTPUser)
		cfg.From, _, _ = s.setting(ctx, settingSMTPFrom)
		if enc, ok, _ := s.setting(ctx, settingSMTPPass); ok {
			if dec, err := s.cipher.Decrypt(enc, aadNotifySMTPPass); err == nil {
				cfg.Pass = dec
			}
		}
		return s.sender.SendSMTP(ctx, group.ID, cfg, group.Target, title, content)
	default: // webhook（既有链路）
		webhook := group.Webhook
		if webhook != "" && s.cipher != nil {
			if dec, err := s.cipher.Decrypt(webhook, aadGroupWebhook); err == nil {
				webhook = dec
			}
		}
		if webhook == "" {
			return fmt.Errorf("群 %q 未配置 webhook", group.Name)
		}
		return s.sender.Send(ctx, group.ID, webhook, title, content)
	}
}
