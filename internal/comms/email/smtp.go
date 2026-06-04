package email

import (
	"context"
	"fmt"
	"strings"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/wneessen/go-mail"
)

// SMTPSender is the production EmailSender backed by an SMTP server. The
// connection pattern mirrors the user service's mailer (wneessen/go-mail):
// the client is created once and dials lazily per DialAndSend, so no connection
// is held open between sends.
type SMTPSender struct {
	cfg    Config
	client *mail.Client
}

// Ensure SMTPSender satisfies the interface.
var _ comms.EmailSender = (*SMTPSender)(nil)

// NewSMTPSender constructs an SMTPSender. Fails fast when Host or From is empty
// so a misconfigured "smtp" provider can never be a silent no-op.
func NewSMTPSender(cfg *Config) (*SMTPSender, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("email/smtp: SMTP host is required")
	}

	if strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("email/smtp: from address is required")
	}

	opts := []mail.Option{
		mail.WithPort(cfg.Port),
		mail.WithTimeout(cfg.sendTimeout()),
		mail.WithTLSPolicy(mail.TLSOpportunistic),
	}

	// Only enable SMTP AUTH when credentials are supplied; local dev relays
	// (MailHog / Mailpit) accept unauthenticated submission.
	if cfg.Username != "" || cfg.Password != "" {
		opts = append(
			opts,
			mail.WithSMTPAuth(mail.SMTPAuthLogin),
			mail.WithUsername(cfg.Username),
			mail.WithPassword(cfg.Password),
		)
	}

	client, err := mail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("email/smtp: new client: %w", err)
	}

	return &SMTPSender{cfg: *cfg, client: client}, nil
}

// Send delivers a rendered EmailMessage. The plain-text part is the canonical
// body; the HTML part (already auto-escaped by html/template during rendering)
// is attached as the alternative. The send is bounded by ctx + the configured
// timeout so a hung SMTP server cannot block the caller indefinitely.
func (s *SMTPSender) Send(ctx context.Context, em comms.EmailMessage) error {
	msg := mail.NewMsg()
	if err := msg.From(s.cfg.From); err != nil {
		return fmt.Errorf("email/smtp: set from: %w", err)
	}

	if err := msg.To(em.To); err != nil {
		// Do not echo the recipient address into the error string.
		return fmt.Errorf("email/smtp: set recipient: %w", err)
	}

	msg.Subject(em.Subject)

	if em.TextBody != "" {
		msg.SetBodyString(mail.TypeTextPlain, em.TextBody)

		if em.HTMLBody != "" {
			msg.AddAlternativeString(mail.TypeTextHTML, em.HTMLBody)
		}
	} else {
		msg.SetBodyString(mail.TypeTextHTML, em.HTMLBody)
	}

	sendCtx, cancel := context.WithTimeout(ctx, s.cfg.sendTimeout())
	defer cancel()

	if err := s.client.DialAndSendWithContext(sendCtx, msg); err != nil {
		return fmt.Errorf("email/smtp: send: %w", err)
	}

	return nil
}
