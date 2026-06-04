// Package sms provides SMSSender implementations for the comms module: a dev
// stub (logs a redacted line, never sends), not-yet-integrated provider stubs
// (AWS SNS, Huawei Cloud SMS, Chunghwa Telecom), and a test spy. NewSMSSender
// selects one by provider name.
//
// Self-contained: imports only the parent comms package + the standard library.
// Written fresh for the comms module (NOT copied from the kyc service).
package sms

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/CoverOnes/notification/internal/comms"
)

// Config carries SMS provider parameters. Secrets (APIKey / APISecret) come from
// the environment ONLY — never from the DB provider_settings jsonb
// (backend-security-design §4).
type Config struct {
	// Provider selects the implementation:
	// "stub" (or "") | "aws-sns" | "huawei" | "chunghwa".
	Provider string

	// SenderID is the alphanumeric / short-code sender shown to recipients.
	SenderID string
	// Region is the provider region or endpoint host (provider-specific meaning).
	Region string

	// APIKey / APISecret are credentials (env-only).
	APIKey    string
	APISecret string

	// SendTimeout bounds a single send (consumed by real providers once
	// integrated; see the per-provider stub docs). Zero means "use the default".
	SendTimeout time.Duration
}

// NewSMSSender builds an SMSSender from cfg.Provider:
//
//	"" | "stub" → StubSender (dev slog, phone redacted; never sends)
//	"aws-sns"   → AWS SNS stub (errProviderNotIntegrated on Send)
//	"huawei"    → Huawei Cloud SMS stub (errProviderNotIntegrated on Send)
//	"chunghwa"  → Chunghwa Telecom stub (errProviderNotIntegrated on Send)
//
// Each real provider stub fails fast in its constructor when required
// credentials are absent, so a real provider is never silently a no-op. cfg is
// taken by pointer (it carries credentials and is constructed once at boot).
func NewSMSSender(cfg *Config) (comms.SMSSender, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "stub":
		return NewStubSender(), nil
	case "aws-sns":
		return newSNSSender(cfg)
	case "huawei":
		return newHuaweiSender(cfg)
	case "chunghwa":
		return newChunghwaSender(cfg)
	default:
		return nil, fmt.Errorf("sms: unknown provider %q (want stub|aws-sns|huawei|chunghwa)", cfg.Provider)
	}
}

// StubSender is the development SMSSender. It never sends; it logs at DEBUG with
// the phone number REDACTED (backend-security-design §3/§4) so a developer can
// confirm the path fired without a real gateway.
type StubSender struct{}

// NewStubSender returns a StubSender.
func NewStubSender() *StubSender { return &StubSender{} }

// Ensure StubSender satisfies the interface.
var _ comms.SMSSender = (*StubSender)(nil)

// Send logs the send at DEBUG with the recipient redacted and the message length
// only (never the message body — it may contain an OTP).
func (s *StubSender) Send(_ context.Context, _, message string) error {
	slog.Debug(
		"comms.sms dev-stub send (no real delivery)",
		"to", "[REDACTED]",
		"message_len", len(message),
	)

	return nil
}
