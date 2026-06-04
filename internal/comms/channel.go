// Package comms is the self-contained outbound messaging module for the
// notification service ("Comms Module" Phase 0). It OWNS outbound comms across
// channels (email / SMS, with push / inapp / LINE reserved) behind pluggable
// per-channel providers, DB-backed templates, a send-log with idempotency, and
// delivery receipts.
//
// DESIGN INVARIANT (litmus test): this package and its sub-packages
// (email, sms, template, provider, sendlog) MUST be liftable into a different
// product as one self-contained unit. It therefore imports NOTHING from the
// notification inbox domain (internal/domain, internal/store) nor from any
// user/kyc internals. It depends only on the standard library, pgx, uuid and
// (for email) wneessen/go-mail.
package comms

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Channel is an outbound delivery channel. The five values below are the only
// permitted channels (mirrored by the CHECK constraint on comms_* tables).
type Channel string

const (
	// ChannelEmail is transactional/marketing email.
	ChannelEmail Channel = "EMAIL"
	// ChannelSMS is short message service (text) delivery.
	ChannelSMS Channel = "SMS"
	// ChannelPush is mobile push notification (reserved — no provider in Phase 0).
	ChannelPush Channel = "PUSH"
	// ChannelInApp is in-product inbox delivery (reserved — no provider in Phase 0).
	ChannelInApp Channel = "INAPP"
	// ChannelLINE is LINE messaging (reserved — no provider in Phase 0).
	ChannelLINE Channel = "LINE"
)

// Valid reports whether c is one of the five known channels.
func (c Channel) Valid() bool {
	switch c {
	case ChannelEmail, ChannelSMS, ChannelPush, ChannelInApp, ChannelLINE:
		return true
	default:
		return false
	}
}

// Status values for a send_log row (mirror the CHECK constraint).
const (
	// StatusPending is set before a send is attempted.
	StatusPending = "PENDING"
	// StatusSent is set after a provider accepts the message.
	StatusSent = "SENT"
	// StatusFailed is set when a send attempt fails (retryable).
	StatusFailed = "FAILED"
	// StatusDead is set when a send is abandoned after exhausting retries.
	StatusDead = "DEAD"
)

// Sentinel errors surfaced by the comms module. They are typed so callers
// (handlers, event path) can map them to stable API error codes WITHOUT leaking
// provider internals.
var (
	// ErrProviderNotIntegrated is returned by a provider stub whose Send is not
	// yet implemented (e.g. ses, sendgrid, aws-sns). The service surfaces this as
	// PROVIDER_UNAVAILABLE without echoing which provider or why.
	ErrProviderNotIntegrated = errors.New("comms: provider not integrated")

	// ErrTemplateNotFound is returned when no template matches (channel, id, locale)
	// even after locale fallback.
	ErrTemplateNotFound = errors.New("comms: template not found")

	// ErrMissingVar is returned by the renderer when a template references a
	// variable that the caller did not supply (fail-closed — never ship "<no value>").
	ErrMissingVar = errors.New("comms: template references an undefined variable")

	// ErrValidation is returned for an invalid SendRequest (bad channel, empty
	// recipient/template, oversized field, etc.). Maps to VALIDATION_ERROR 400.
	ErrValidation = errors.New("comms: validation error")

	// ErrRenderTooLarge is returned when a rendered subject/body exceeds the cap.
	ErrRenderTooLarge = errors.New("comms: rendered message exceeds size cap")
)

// EmailMessage is a fully-rendered email ready for an EmailSender.
type EmailMessage struct {
	To       string
	Subject  string
	TextBody string
	HTMLBody string
}

// EmailSender delivers a rendered EmailMessage over a concrete provider.
type EmailSender interface {
	Send(ctx context.Context, msg EmailMessage) error
}

// SMSSender delivers a rendered text message to a phone number.
type SMSSender interface {
	Send(ctx context.Context, to, message string) error
}

// Template is a DB-backed message template.
type Template struct {
	Channel    Channel
	TemplateID string
	Locale     string
	Subject    string
	Body       string
	UpdatedAt  time.Time
}

// TemplateStore loads templates by (channel, templateID, locale) with locale
// fallback to the default locale.
type TemplateStore interface {
	Get(ctx context.Context, channel Channel, templateID, locale string) (*Template, error)
}

// Renderer renders a Template against a set of variables, returning the rendered
// subject and body. Implementations MUST fail closed on a missing variable and
// MUST escape per-channel (html/template for HTML email).
type Renderer interface {
	Render(tpl *Template, vars map[string]string) (subject, body string, err error)
}

// SendRequest is a single outbound send instruction. It is the input for both
// the S2S API path and the Redis event path; both converge on CommsService.Send.
type SendRequest struct {
	Channel        Channel
	To             string
	TemplateID     string
	Locale         string
	Vars           map[string]string
	IdempotencyKey string
	UserID         *uuid.UUID
}

// SendResult is the outcome of a Send. Deduped is true when an existing send_log
// row matched the idempotency key (no new provider call was made).
type SendResult struct {
	SendID  uuid.UUID
	Status  string
	Deduped bool
}

// CommsService orchestrates a send end-to-end: dedup → load+render template →
// resolve provider → Sender.Send → persist send_log.
type CommsService interface {
	Send(ctx context.Context, req SendRequest) (SendResult, error)
}

// SendLogRow is a single send-log record (metadata only — no PII, no body).
// It lives in the root package so both the orchestrator (service.go) and the
// persistence layer (sendlog package) reference one shape without an import cycle.
type SendLogRow struct {
	ID             uuid.UUID
	IdempotencyKey string
	Channel        Channel
	Provider       string
	TemplateID     string
	UserID         *uuid.UUID
	ToHash         []byte
	Status         string
	Attempts       int
	ProviderMsgID  string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SendLogStore is the persistence contract the orchestrator depends on. The
// concrete implementation lives in internal/comms/sendlog. Declared as an
// interface so the orchestrator can be unit-tested with a fake (no DB).
type SendLogStore interface {
	// Reserve atomically claims an idempotency key by inserting a PENDING row.
	// Returns (existing, claimed=false) when the key already existed.
	Reserve(ctx context.Context, r *SendLogRow) (existing *SendLogRow, claimed bool, err error)
	// MarkResult updates a reserved row to its terminal status after a send.
	MarkResult(ctx context.Context, id uuid.UUID, status, providerMsgID string, sendErr error) error
}
