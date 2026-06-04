package email

import (
	"context"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
)

// SendGridSender is an extension slot for the SendGrid v3 Mail Send API. It
// satisfies comms.EmailSender but is NOT yet integrated — Send returns
// comms.ErrProviderNotIntegrated until implemented.
//
// # To integrate SendGrid (v3 /mail/send)
//
//  1. No SDK is strictly required — use net/http POST to
//     https://api.sendgrid.com/v3/mail/send with Authorization: Bearer <API key>.
//  2. In newSendGridSender: read the API key from the environment ONLY
//     (a dedicated NOTIFICATION_EMAIL_SENDGRID_API_KEY), store cfg + key.
//  3. In Send: build the v3 JSON body
//     {personalizations:[{to:[{email: em.To}]}], from:{email: cfg.From},
//     subject: em.Subject, content:[{type:"text/plain", value: em.TextBody},
//     {type:"text/html", value: em.HTMLBody}]}.
//     Wrap the call in context.WithTimeout(ctx, cfg.sendTimeout()); a 2xx is
//     success, otherwise map to a generic error (do not leak the API response).
//
// Required env (REPLACE_ME in .env.example, env-only — never DB):
//   - NOTIFICATION_EMAIL_SENDGRID_API_KEY — SendGrid API key (add when integrating)
//   - NOTIFICATION_EMAIL_SMTP_FROM        — reused as the verified From sender
type SendGridSender struct {
	cfg Config
}

// Ensure SendGridSender satisfies the interface at compile time.
var _ comms.EmailSender = (*SendGridSender)(nil)

// newSendGridSender constructs a (not-yet-integrated) SendGridSender.
func newSendGridSender(cfg *Config) *SendGridSender {
	return &SendGridSender{cfg: *cfg}
}

// Send is a TODO extension point. See the struct doc for integration guidance.
func (s *SendGridSender) Send(_ context.Context, _ comms.EmailMessage) error {
	return fmt.Errorf("email/sendgrid: %w", comms.ErrProviderNotIntegrated)
}
