// Package mail 邮件能力包（对标 MailTalent：net/smtp 标准库实现——Java 用
// simple-java-mail；HTML 正文探测（含 < 且 >）、附件沙箱读取、465 端口走
// TLS 隐式通道）。
package mail

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// Mailer 邮件发送面。
type Mailer interface {
	SendMail(ctx context.Context, from, to, subject, body string, attachments map[string][]byte, html bool) error
}

// SMTPMailer 标准库实现。
type SMTPMailer struct {
	Host     string
	Port     int
	Username string
	Password string
	FromName string
}

// NewSMTPMailer 构造（port==465 走 TLS）。
func NewSMTPMailer(host string, port int, username, password string) *SMTPMailer {
	return &SMTPMailer{Host: host, Port: port, Username: username, Password: password, FromName: "AI Assistant"}
}

// SendMail 发送。
func (m *SMTPMailer) SendMail(ctx context.Context, _, to, subject, body string, attachments map[string][]byte, html bool) (err error) {
	addr := fmt.Sprintf("%s:%d", m.Host, m.Port)
	from := m.Username

	contentType := "text/plain; charset=UTF-8"
	if html {
		contentType = "text/html; charset=UTF-8"
	}

	var sb strings.Builder
	sb.WriteString("From: " + m.FromName + " <" + from + ">\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	if len(attachments) == 0 {
		sb.WriteString("Content-Type: " + contentType + "\r\n\r\n")
		sb.WriteString(body)
		return smtp.SendMail(addr, smtp.PlainAuth("", m.Username, m.Password, m.Host), from,
			[]string{to}, []byte(sb.String()))
	}

	// 混合 MIME（附件 + 正文）
	boundary := "aifei-go-boundary-7A94"
	sb.WriteString("Content-Type: multipart/mixed; boundary=" + boundary + "\r\n\r\n")
	sb.WriteString("--" + boundary + "\r\n")
	sb.WriteString("Content-Type: " + contentType + "\r\n\r\n")
	sb.WriteString(body + "\r\n")
	for name, data := range attachments {
		sb.WriteString("--" + boundary + "\r\n")
		sb.WriteString("Content-Type: application/octet-stream\r\n")
		sb.WriteString("Content-Transfer-Encoding: base64\r\n")
		sb.WriteString("Content-Disposition: attachment; filename=\"" + name + "\"\r\n\r\n")
		sb.WriteString(base64Lines(data))
		sb.WriteString("\r\n")
	}
	sb.WriteString("--" + boundary + "--")
	return smtp.SendMail(addr, smtp.PlainAuth("", m.Username, m.Password, m.Host), from,
		[]string{to}, []byte(sb.String()))
}

// encodeBase64 编码便捷。
func encodeBase64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// base64Lines 76 列换行。
func base64Lines(data []byte) string {
	const cols = 76
	encoded := encodeBase64(data)
	var sb strings.Builder
	for i := 0; i < len(encoded); i += cols {
		end := i + cols
		if end > len(encoded) {
			end = len(encoded)
		}
		sb.WriteString(encoded[i:end])
		sb.WriteString("\r\n")
	}
	return sb.String()
}

// Mail 能力包。
type Mail struct {
	ai.TalentBase
	mailer  Mailer
	rootPath string
}

// NewMail 构造。
func NewMail(mailer Mailer, rootPath string) *Mail { return &Mail{mailer: mailer, rootPath: rootPath} }

// Name 名称。
func (t *Mail) Name() string { return "Mail" }

// Description 描述。
func (t *Mail) Description() string { return "邮件发送" }

// Tools 工具集。
func (t *Mail) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("send_email").
		Desc("发送邮件（正文含 HTML 标签则按 HTML 发送，否则纯文本；附件须在工作目录内）").
		StringParam("to", "收件人地址", true).
		StringParam("subject", "主题", true).
		StringParam("body", "正文", true).
		StringParam("attachmentFileName", "附件文件名（可选，相对工作目录）", false).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			to, _ := args["to"].(string)
			subject, _ := args["subject"].(string)
			body, _ := args["body"].(string)
			html := strings.Contains(body, "<") && strings.Contains(body, ">")

			attachments := map[string][]byte{}
			if name, _ := args["attachmentFileName"].(string); name != "" {
				data, err := readSandboxed(t.rootPath, name)
				if err == nil && data != nil {
					attachments[filepath.Base(name)] = data
				} // 不存在静默跳过（Java 同款）
			}
			if err := t.mailer.SendMail(ctx, "", to, subject, body, attachments, html); err != nil {
				return "发送异常: " + err.Error(), nil
			}
			return "邮件发送成功: " + to, nil
		}).
		Build()}
}

// MailerForTest 邮件器访问面（注入断言用）。
func (t *Mail) MailerForTest() Mailer { return t.mailer }

// readSandboxed 沙箱读取（rootPath 内 + 越界拒绝）。
func readSandboxed(rootPath, name string) ([]byte, error) {
	if rootPath == "" {
		rootPath = "."
	}
	abs, err := filepath.Abs(filepath.Join(rootPath, name))
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) && abs != root {
		return nil, fmt.Errorf("mail: 非法路径访问")
	}
	return os.ReadFile(abs)
}
