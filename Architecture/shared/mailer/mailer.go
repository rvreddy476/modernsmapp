// Package mailer provides a pluggable email sending abstraction.
//
// Use Mailer interface in services; wire SMTPMailer in production or NoopMailer in dev.
// Templates live under shared/mailer/templates/ and are rendered with html/template.
package mailer

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/smtp"
	"strings"
)

// Mailer is the transport-agnostic email sender.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Message is a rendered email ready to dispatch.
type Message struct {
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	HTMLBody    string
	TextBody    string // optional plain-text fallback
	FromName    string // overrides default from name
	FromAddr    string // overrides default from addr
	Attachments []Attachment
}

// Attachment is an inline file attachment.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ── NoopMailer ────────────────────────────────────────────────────────────
// NoopMailer logs messages instead of sending. Use in dev/test.
type NoopMailer struct{}

func (NoopMailer) Send(_ context.Context, msg Message) error {
	slog.Info("mailer.noop.send",
		"to", msg.To, "subject", msg.Subject, "attachments", len(msg.Attachments))
	return nil
}

// ── SMTPMailer ────────────────────────────────────────────────────────────
// SMTPMailer sends via SMTP (works with Mailgun, SES-SMTP, SendGrid-SMTP, etc.).
type SMTPMailer struct {
	Host      string // smtp.host.com
	Port      string // "587"
	Username  string
	Password  string
	FromName  string // default From name
	FromAddr  string // default From address
}

func NewSMTPMailerFromEnv(env map[string]string) *SMTPMailer {
	return &SMTPMailer{
		Host:     env["SMTP_HOST"],
		Port:     env["SMTP_PORT"],
		Username: env["SMTP_USERNAME"],
		Password: env["SMTP_PASSWORD"],
		FromName: envOr(env, "SMTP_FROM_NAME", "Postbook"),
		FromAddr: envOr(env, "SMTP_FROM_ADDR", "noreply@postbook.app"),
	}
}

func envOr(env map[string]string, k, def string) string {
	if v, ok := env[k]; ok && v != "" {
		return v
	}
	return def
}

func (m *SMTPMailer) Send(_ context.Context, msg Message) error {
	if len(msg.To) == 0 {
		return fmt.Errorf("mailer: no recipients")
	}
	fromName := msg.FromName
	if fromName == "" {
		fromName = m.FromName
	}
	fromAddr := msg.FromAddr
	if fromAddr == "" {
		fromAddr = m.FromAddr
	}

	body := buildMIME(fromName, fromAddr, msg)

	auth := smtp.PlainAuth("", m.Username, m.Password, m.Host)
	rcpts := append(append([]string{}, msg.To...), msg.Cc...)
	rcpts = append(rcpts, msg.Bcc...)

	addr := m.Host + ":" + m.Port
	if err := smtp.SendMail(addr, auth, fromAddr, rcpts, body); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// buildMIME composes a multipart/mixed message with optional attachments.
func buildMIME(fromName, fromAddr string, msg Message) []byte {
	const boundary = "atpost_mailer_boundary_0b4f7e"
	const altBoundary = "atpost_alt_boundary_a1b2c3"
	var b bytes.Buffer

	fmt.Fprintf(&b, "From: %s <%s>\r\n", fromName, fromAddr)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(msg.To, ","))
	if len(msg.Cc) > 0 {
		fmt.Fprintf(&b, "Cc: %s\r\n", strings.Join(msg.Cc, ","))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")

	if len(msg.Attachments) > 0 {
		fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)
		fmt.Fprintf(&b, "--%s\r\n", boundary)
	}

	// Alternative part (text + html)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", altBoundary)

	if msg.TextBody != "" {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", altBoundary, msg.TextBody)
	}
	if msg.HTMLBody != "" {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n", altBoundary, msg.HTMLBody)
	}
	fmt.Fprintf(&b, "--%s--\r\n", altBoundary)

	for _, att := range msg.Attachments {
		fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s; name=%q\r\n", att.ContentType, att.Filename)
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&b, "Content-Disposition: attachment; filename=%q\r\n\r\n", att.Filename)
		b.WriteString(base64Wrap(att.Data))
	}
	if len(msg.Attachments) > 0 {
		fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	}
	return b.Bytes()
}

// base64Wrap encodes data base64 and wraps at 76 chars (RFC 2045).
func base64Wrap(data []byte) string {
	const lineLen = 76
	enc := toBase64(data)
	var out strings.Builder
	for i := 0; i < len(enc); i += lineLen {
		end := i + lineLen
		if end > len(enc) {
			end = len(enc)
		}
		out.WriteString(enc[i:end])
		out.WriteString("\r\n")
	}
	return out.String()
}

func toBase64(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var buf bytes.Buffer
	n := len(data)
	for i := 0; i < n; i += 3 {
		var b1, b2, b3 byte
		b1 = data[i]
		if i+1 < n {
			b2 = data[i+1]
		}
		if i+2 < n {
			b3 = data[i+2]
		}
		buf.WriteByte(alphabet[b1>>2])
		buf.WriteByte(alphabet[((b1&0x03)<<4)|(b2>>4)])
		if i+1 < n {
			buf.WriteByte(alphabet[((b2&0x0f)<<2)|(b3>>6)])
		} else {
			buf.WriteByte('=')
		}
		if i+2 < n {
			buf.WriteByte(alphabet[b3&0x3f])
		} else {
			buf.WriteByte('=')
		}
	}
	return buf.String()
}

// ── Template rendering ────────────────────────────────────────────────────

// Render renders an HTML template string against data. Returns (subject, html, err)
// where subject is derived from the first <title>...</title> block if present.
func Render(tmpl string, data any) (subject, html string, err error) {
	t, err := template.New("msg").Parse(tmpl)
	if err != nil {
		return "", "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", "", err
	}
	html = buf.String()
	// Extract subject from <title>
	if i := strings.Index(html, "<title>"); i != -1 {
		if j := strings.Index(html[i:], "</title>"); j != -1 {
			subject = strings.TrimSpace(html[i+7 : i+j])
		}
	}
	return subject, html, nil
}
