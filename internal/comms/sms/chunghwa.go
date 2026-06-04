package sms

import (
	"context"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
)

// ChunghwaSender is an extension slot for Chunghwa Telecom (中華電信) SMS — the
// "EMG" / mPro SMS HTTP gateway used for Taiwan delivery. It satisfies
// comms.SMSSender but is NOT yet integrated — Send returns
// comms.ErrProviderNotIntegrated until implemented.
//
// # To integrate Chunghwa Telecom SMS (mPro / EMG HTTP gateway)
//
//  1. No SDK — net/http GET/POST to the mPro gateway endpoint.
//  2. In Send, build the request params:
//     username = cfg.APIKey, password = cfg.APISecret (account credentials),
//     dstaddr  = to (local "09xxxxxxxx" or E.164 depending on the contract),
//     smbody   = message (Big5 or UTF-8 per the account encoding setting),
//     from     = cfg.SenderID (if the contract supports a sender id).
//  3. POST to cfg.Region (the gateway base URL, e.g.
//     "https://emome.net/SmExpress/SmExpressGet.ashx") over TLS.
//     Parse the response status line; a non-success statuscode = error.
//     Wrap in context.WithTimeout(ctx, cfg.sendTimeout()).
//  4. NOTE: encoding matters for Traditional Chinese bodies — confirm the
//     account's charset before shipping.
//
// Required config (env-only secrets, REPLACE_ME in .env.example):
//   - NOTIFICATION_SMS_API_KEY     — mPro account username
//   - NOTIFICATION_SMS_API_SECRET  — mPro account password
//   - NOTIFICATION_SMS_REGION      — gateway base URL
//   - NOTIFICATION_SMS_SENDER_ID   — optional sender id (if contract supports it)
type ChunghwaSender struct {
	cfg Config
}

// Ensure ChunghwaSender satisfies the interface at compile time.
var _ comms.SMSSender = (*ChunghwaSender)(nil)

// newChunghwaSender constructs a (not-yet-integrated) ChunghwaSender. Fails fast
// when credentials or the gateway URL are absent.
func newChunghwaSender(cfg *Config) (*ChunghwaSender, error) {
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("sms/chunghwa: NOTIFICATION_SMS_API_KEY and NOTIFICATION_SMS_API_SECRET are required")
	}

	if cfg.Region == "" {
		return nil, fmt.Errorf("sms/chunghwa: NOTIFICATION_SMS_REGION must be the mPro gateway base URL")
	}

	return &ChunghwaSender{cfg: *cfg}, nil
}

// Send is a TODO extension point. See the struct doc for integration guidance.
func (s *ChunghwaSender) Send(_ context.Context, _, _ string) error {
	return fmt.Errorf("sms/chunghwa: %w", comms.ErrProviderNotIntegrated)
}
