// Package email provides EmailSender implementations for the comms module:
// a real SMTP provider (wneessen/go-mail), a dev-log sender for local use, and
// not-yet-integrated stubs for SES and SendGrid. NewEmailSender selects one by
// provider name.
//
// Self-contained: this package imports only the parent comms package and the
// standard library + go-mail. It knows nothing of the notification inbox.
package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/CoverOnes/notification/internal/comms"
)

// Config carries the non-secret + secret SMTP connection parameters. Secrets
// (Username / Password) come from the environment only — NEVER from the DB
// provider_settings jsonb (backend-security-design §4).
type Config struct {
	// Provider selects the implementation: "smtp" | "ses" | "sendgrid".
	// "" or "stub" selects the dev-log sender.
	Provider string

	// SMTP connection parameters (used when Provider == "smtp").
	Host       string
	Port       int
	Username   string
	Password   string
	From       string
	AppBaseURL string

	// SendTimeout bounds a single send. Zero falls back to defaultSendTimeout.
	SendTimeout time.Duration
}

const defaultSendTimeout = 10 * time.Second

// sendTimeout returns the effective per-send timeout.
func (c *Config) sendTimeout() time.Duration {
	if c.SendTimeout <= 0 {
		return defaultSendTimeout
	}

	return c.SendTimeout
}

// NewEmailSender builds an EmailSender from cfg.Provider:
//
//	"smtp"      → SMTPSender (real delivery via go-mail)
//	"ses"       → SES stub (errProviderNotIntegrated on Send)
//	"sendgrid"  → SendGrid stub (errProviderNotIntegrated on Send)
//	"" | "stub" → DevLogSender (logs a redacted line; never sends)
//
// A "smtp" provider with no Host configured is a boot error (fail-fast) so a
// real provider is never silently a no-op. cfg is taken by pointer (the struct
// carries credentials and is constructed once at boot).
func NewEmailSender(cfg *Config) (comms.EmailSender, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "stub":
		return NewDevLogSender(), nil
	case "smtp":
		return NewSMTPSender(cfg)
	case "ses":
		return newSESSender(cfg), nil
	case "sendgrid":
		return newSendGridSender(cfg), nil
	default:
		return nil, fmt.Errorf("email: unknown provider %q (want smtp|ses|sendgrid|stub)", cfg.Provider)
	}
}

// DevLogSender is the development EmailSender. It never sends anything; it logs
// the recipient domain + subject at DEBUG (recipient local-part redacted) so a
// developer can confirm the send path fired without a real SMTP server.
type DevLogSender struct{}

// NewDevLogSender returns a DevLogSender.
func NewDevLogSender() *DevLogSender { return &DevLogSender{} }

// Ensure DevLogSender satisfies the interface.
var _ comms.EmailSender = (*DevLogSender)(nil)

// Send logs the message metadata (no body, recipient partially redacted).
func (s *DevLogSender) Send(_ context.Context, msg comms.EmailMessage) error {
	slog.Debug(
		"comms.email dev-log send (no real delivery)",
		"to", redactEmail(msg.To),
		"subject", msg.Subject,
		"html_len", len(msg.HTMLBody),
		"text_len", len(msg.TextBody),
	)

	return nil
}

// redactEmail returns a privacy-preserving rendering of an email address:
// the local part is replaced with "[REDACTED]" so logs never carry full PII.
func redactEmail(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return "[REDACTED]"
	}

	return "[REDACTED]" + addr[at:]
}
