package sms

import (
	"context"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
)

// SNSSender is an extension slot for Amazon SNS SMS publishing. It satisfies
// comms.SMSSender but is NOT yet integrated — Send returns
// comms.ErrProviderNotIntegrated until implemented.
//
// # To integrate AWS SNS SMS
//
//  1. Add dependency github.com/aws/aws-sdk-go-v2/service/sns.
//  2. In newSNSSender: build aws.Config (region = cfg.Region, credentials from
//     the default chain — env / IAM role; NEVER hard-coded), construct sns.Client.
//  3. In Send: call Publish with PhoneNumber = to (E.164 "+{country}{number}"),
//     Message = message, and MessageAttributes:
//     "AWS.SNS.SMS.SenderID" = cfg.SenderID,
//     "AWS.SNS.SMS.SMSType"  = "Transactional".
//     Wrap in context.WithTimeout(ctx, cfg.sendTimeout()).
//  4. Map AWS errors to a generic, provider-opaque error.
//
// Required config (env-only secrets, REPLACE_ME in .env.example):
//   - NOTIFICATION_SMS_REGION    — AWS region, e.g. "ap-southeast-1"
//   - NOTIFICATION_SMS_SENDER_ID  — registered alphanumeric sender id
//   - AWS credentials via the standard chain (env / IAM role)
type SNSSender struct {
	cfg Config
}

// Ensure SNSSender satisfies the interface at compile time.
var _ comms.SMSSender = (*SNSSender)(nil)

// newSNSSender constructs a (not-yet-integrated) SNSSender. Fails fast when the
// region is absent so a real provider is never a silent no-op.
func newSNSSender(cfg *Config) (*SNSSender, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("sms/aws-sns: NOTIFICATION_SMS_REGION is required")
	}

	return &SNSSender{cfg: *cfg}, nil
}

// Send is a TODO extension point. See the struct doc for integration guidance.
func (s *SNSSender) Send(_ context.Context, _, _ string) error {
	return fmt.Errorf("sms/aws-sns: %w", comms.ErrProviderNotIntegrated)
}
