package sms

import (
	"context"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
)

// HuaweiSender is an extension slot for the Huawei Cloud SMS API. It satisfies
// comms.SMSSender but is NOT yet integrated — Send returns
// comms.ErrProviderNotIntegrated until implemented.
//
// # To integrate Huawei Cloud SMS (POST /sms/batchSendSms/v1)
//
//  1. No SDK required — net/http with WSSE auth headers.
//  2. In Send, build the X-WSSE header:
//     nonce   := random hex
//     created := time.Now().UTC().Format(time.RFC3339)
//     digest  := base64(sha256(nonce + created + cfg.APISecret))
//     header  := UsernameToken Username=cfg.APIKey, PasswordDigest=digest,
//     Nonce=nonce, Created=created
//  3. Form-encode: from=cfg.SenderID (channel number), to=to (E.164),
//     templateParas=[message] (JSON array). NOTE: Huawei is template-based; the
//     message maps to templateParas, not a free-text body — a future iteration
//     may need a template-param send variant.
//  4. POST to "https://" + cfg.Region + "/sms/batchSendSms/v1" with
//     Content-Type application/x-www-form-urlencoded; non-"000000" code = error.
//     Wrap in context.WithTimeout(ctx, cfg.sendTimeout()).
//
// Required config (env-only secrets, REPLACE_ME in .env.example):
//   - NOTIFICATION_SMS_API_KEY     — Huawei App Key
//   - NOTIFICATION_SMS_API_SECRET  — Huawei App Secret
//   - NOTIFICATION_SMS_SENDER_ID   — channel number ("from")
//   - NOTIFICATION_SMS_REGION      — endpoint host, e.g. "smsapi.cn-north-4.myhuaweicloud.com:443"
type HuaweiSender struct {
	cfg Config
}

// Ensure HuaweiSender satisfies the interface at compile time.
var _ comms.SMSSender = (*HuaweiSender)(nil)

// newHuaweiSender constructs a (not-yet-integrated) HuaweiSender. Fails fast when
// credentials or the endpoint host are absent.
func newHuaweiSender(cfg *Config) (*HuaweiSender, error) {
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("sms/huawei: NOTIFICATION_SMS_API_KEY and NOTIFICATION_SMS_API_SECRET are required")
	}

	if cfg.Region == "" {
		return nil, fmt.Errorf("sms/huawei: NOTIFICATION_SMS_REGION must be the Huawei endpoint host")
	}

	return &HuaweiSender{cfg: *cfg}, nil
}

// Send is a TODO extension point. See the struct doc for integration guidance.
// Reminder: Huawei SMS is template-based; message maps to templateParas.
func (s *HuaweiSender) Send(_ context.Context, _, _ string) error {
	return fmt.Errorf("sms/huawei: %w", comms.ErrProviderNotIntegrated)
}
