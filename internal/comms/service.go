package comms

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Service is the production CommsService orchestrator. It owns the send pipeline:
//
//	validate → reserve (idempotency) → load+render template → resolve sender →
//	Sender.Send → record terminal status in send_log.
//
// Providers are resolved per channel from the senders injected at construction.
// In Phase 0 there is exactly one email sender and one sms sender (selected by
// the factories from config); push / inapp / line have no sender and return a
// validation error if requested.
type Service struct {
	templates   TemplateStore
	renderer    Renderer
	sendLog     SendLogStore
	emailSender EmailSender
	smsSender   SMSSender
	emailName   string // provider name recorded in send_log
	smsName     string
	timeout     time.Duration
}

// ServiceDeps bundles the orchestrator dependencies.
type ServiceDeps struct {
	Templates   TemplateStore
	Renderer    Renderer
	SendLog     SendLogStore
	EmailSender EmailSender
	SMSSender   SMSSender
	// EmailProvider / SMSProvider are the provider names recorded in send_log
	// (e.g. "smtp", "stub", "aws-sns"). Defaulted when empty.
	EmailProvider string
	SMSProvider   string
	// SendTimeout bounds a single provider send. Zero falls back to 10s.
	SendTimeout time.Duration
}

const defaultSendTimeout = 10 * time.Second

// NewService constructs a Service. The sendLog dependency is taken as the
// concrete *sendlog.Store via the interface so callers wire the real store.
func NewService(deps *ServiceDeps) *Service {
	timeout := deps.SendTimeout
	if timeout <= 0 {
		timeout = defaultSendTimeout
	}

	emailName := deps.EmailProvider
	if emailName == "" {
		emailName = "stub"
	}

	smsName := deps.SMSProvider
	if smsName == "" {
		smsName = "stub"
	}

	return &Service{
		templates:   deps.Templates,
		renderer:    deps.Renderer,
		sendLog:     deps.SendLog,
		emailSender: deps.EmailSender,
		smsSender:   deps.SMSSender,
		emailName:   emailName,
		smsName:     smsName,
		timeout:     timeout,
	}
}

// Ensure Service satisfies the interface.
var _ CommsService = (*Service)(nil)

// maxRecipientLen / maxTemplateIDLen bound the request fields we accept.
const (
	maxRecipientLen      = 320 // RFC 5321 max email length; also generous for phone
	maxTemplateIDLen     = 128
	maxIdempotencyKeyLen = 200
	maxVars              = 50
)

// Send runs the full pipeline. Errors are typed sentinels (ErrValidation,
// ErrTemplateNotFound, ErrMissingVar, ErrProviderNotIntegrated, ...) so the
// caller maps them to stable API codes without leaking internals.
//
//nolint:gocritic // hugeParam: req is a value because the comms.CommsService interface fixes this signature
func (s *Service) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if err := validateRequest(&req); err != nil {
		return SendResult{}, err
	}

	provider := s.providerName(req.Channel)

	// 1. Reserve the idempotency key. A dedup hit short-circuits with the prior
	//    row's status — no template render, no provider call.
	reserved := &SendLogRow{
		IdempotencyKey: req.IdempotencyKey,
		Channel:        req.Channel,
		Provider:       provider,
		TemplateID:     req.TemplateID,
		UserID:         req.UserID,
		ToHash:         HashRecipient(req.To),
	}

	row, claimed, err := s.sendLog.Reserve(ctx, reserved)
	if err != nil {
		return SendResult{}, fmt.Errorf("comms: reserve: %w", err)
	}

	if !claimed {
		return SendResult{SendID: row.ID, Status: row.Status, Deduped: true}, nil
	}

	// 2. Load + render the template (fail-closed on missing vars).
	tpl, err := s.templates.Get(ctx, req.Channel, req.TemplateID, req.Locale)
	if err != nil {
		_ = s.markFailed(ctx, row.ID, err) // record the failure (sanitized)

		return SendResult{}, err
	}

	subject, body, err := s.renderer.Render(tpl, req.Vars)
	if err != nil {
		_ = s.markFailed(ctx, row.ID, err)

		return SendResult{}, err
	}

	// 3. Dispatch to the resolved provider, bounded by the send timeout.
	sendCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	sendErr := s.dispatch(sendCtx, &req, subject, body)
	if sendErr != nil {
		if markErr := s.sendLog.MarkResult(ctx, row.ID, StatusFailed, "", sendErr); markErr != nil {
			slog.Warn("comms: failed to record send failure", "err", markErr)
		}

		return SendResult{SendID: row.ID, Status: StatusFailed}, sendErr
	}

	// 4. Record success. We have no provider message id from the stub/dev senders;
	//    real providers will return one to thread through here in a later phase.
	if markErr := s.sendLog.MarkResult(ctx, row.ID, StatusSent, "", nil); markErr != nil {
		slog.Warn("comms: failed to record send success", "err", markErr)
	}

	return SendResult{SendID: row.ID, Status: StatusSent}, nil
}

// dispatch routes a rendered message to the channel's sender.
func (s *Service) dispatch(ctx context.Context, req *SendRequest, subject, body string) error {
	switch req.Channel {
	case ChannelEmail:
		if s.emailSender == nil {
			return fmt.Errorf("comms: email: %w", ErrProviderNotIntegrated)
		}

		return s.emailSender.Send(ctx, EmailMessage{
			To:       req.To,
			Subject:  subject,
			TextBody: body,
			HTMLBody: body,
		})
	case ChannelSMS:
		if s.smsSender == nil {
			return fmt.Errorf("comms: sms: %w", ErrProviderNotIntegrated)
		}

		return s.smsSender.Send(ctx, req.To, body)
	default:
		// PUSH / INAPP / LINE have no Phase 0 provider.
		return fmt.Errorf("comms: channel %s: %w", req.Channel, ErrProviderNotIntegrated)
	}
}

// providerName returns the provider recorded in send_log for a channel.
func (s *Service) providerName(ch Channel) string {
	switch ch {
	case ChannelEmail:
		return s.emailName
	case ChannelSMS:
		return s.smsName
	default:
		return "none"
	}
}

// markFailed records a pre-dispatch failure (template / render error) as FAILED.
func (s *Service) markFailed(ctx context.Context, id uuid.UUID, cause error) error {
	if markErr := s.sendLog.MarkResult(ctx, id, StatusFailed, "", cause); markErr != nil {
		slog.Warn("comms: failed to record pre-dispatch failure", "err", markErr)

		return markErr
	}

	return nil
}

// validateRequest enforces field-level invariants before any DB work.
func validateRequest(req *SendRequest) error {
	if !req.Channel.Valid() {
		return fmt.Errorf("%w: unknown channel %q", ErrValidation, req.Channel)
	}

	if req.To == "" || len(req.To) > maxRecipientLen {
		return fmt.Errorf("%w: recipient is required and must be <= %d chars", ErrValidation, maxRecipientLen)
	}

	if req.TemplateID == "" || len(req.TemplateID) > maxTemplateIDLen {
		return fmt.Errorf("%w: templateId is required and must be <= %d chars", ErrValidation, maxTemplateIDLen)
	}

	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > maxIdempotencyKeyLen {
		return fmt.Errorf("%w: idempotencyKey is required and must be <= %d chars", ErrValidation, maxIdempotencyKeyLen)
	}

	if len(req.Vars) > maxVars {
		return fmt.Errorf("%w: too many template variables (max %d)", ErrValidation, maxVars)
	}

	return nil
}

// IsProviderUnavailable reports whether err is (or wraps) a not-integrated /
// channel-unavailable condition — the handler maps this to PROVIDER_UNAVAILABLE.
func IsProviderUnavailable(err error) bool {
	return errors.Is(err, ErrProviderNotIntegrated)
}

// HashRecipient returns sha256(recipient). This is the ONLY representation of a
// recipient ever persisted by the comms module (backend-security-design §3.1) —
// the plaintext address/phone is never written to the DB.
func HashRecipient(recipient string) []byte {
	sum := sha256.Sum256([]byte(recipient))

	return sum[:]
}
