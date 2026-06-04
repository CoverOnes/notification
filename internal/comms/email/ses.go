package email

import (
	"context"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
)

// SESSender is an extension slot for Amazon SES. It satisfies comms.EmailSender
// but is NOT yet integrated — Send returns comms.ErrProviderNotIntegrated until
// implemented.
//
// # To integrate Amazon SES (v2 API)
//
//  1. Add dependency github.com/aws/aws-sdk-go-v2/service/sesv2.
//  2. In newSESSender: build an aws.Config (region from cfg / env, credentials
//     from the default chain — env / IAM role; NEVER hard-coded), construct an
//     sesv2.Client, store it.
//  3. In Send: call SendEmail with Destination{ToAddresses: [em.To]} and a
//     Content{Simple:{Subject, Body:{Text: em.TextBody, Html: em.HTMLBody}}}.
//     From = cfg.From (a verified SES identity). Wrap the call in
//     context.WithTimeout(ctx, cfg.sendTimeout()).
//  4. Map AWS errors to a generic error (do NOT leak the raw SDK error to the
//     API client; the service redacts last_error but keep messages provider-opaque).
//
// Required env (REPLACE_ME in .env.example, env-only — never DB):
//   - NOTIFICATION_EMAIL_SMTP_FROM    — reused as the SES verified From identity
//   - AWS credentials via the standard chain (AWS_REGION + IAM role / keys)
type SESSender struct {
	cfg Config
}

// Ensure SESSender satisfies the interface at compile time.
var _ comms.EmailSender = (*SESSender)(nil)

// newSESSender constructs a (not-yet-integrated) SESSender.
func newSESSender(cfg *Config) *SESSender {
	return &SESSender{cfg: *cfg}
}

// Send is a TODO extension point. See the struct doc for integration guidance.
func (s *SESSender) Send(_ context.Context, _ comms.EmailMessage) error {
	return fmt.Errorf("email/ses: %w", comms.ErrProviderNotIntegrated)
}
