package template

import (
	"context"
	"fmt"

	"github.com/CoverOnes/notification/internal/comms"
)

// DefaultTemplates are the Phase 0 seed templates. They are upserted on boot
// (when comms is enabled) so a fresh deployment has a working email-verify and
// phone-OTP template without a manual data load.
//
// Body variables use Go template syntax ({{.var}}). The renderer fails closed on
// any variable the caller does not supply, so the documented vars below are the
// contract for each template:
//
//	email_verify : .verifyURL
//	phone_otp    : .code .ttlMinutes
//
// The email_verify body ports the user service's verification email content
// (clickable CTA + raw-token fallback). It is HTML — html/template auto-escapes
// every variable value at render time.
var DefaultTemplates = []comms.Template{
	{
		Channel:    comms.ChannelEmail,
		TemplateID: "email_verify",
		Locale:     "en",
		Subject:    "Verify your CoverOnes account",
		Body: `<p>Welcome to CoverOnes!</p>` +
			`<p>Please verify your email address by clicking the button below:</p>` +
			`<p><a href="{{.verifyURL}}">Verify my email</a></p>` +
			`<p>If the button does not work, copy this link into your browser:<br>{{.verifyURL}}</p>` +
			`<p>This link is single-use and expires soon. If you did not create this ` +
			`account you can safely ignore this message.</p>`,
	},
	{
		Channel:    comms.ChannelSMS,
		TemplateID: "phone_otp",
		Locale:     "en",
		Subject:    "", // SMS has no subject
		Body:       `Your CoverOnes verification code is {{.code}}. It expires in {{.ttlMinutes}} minutes. Do not share this code.`,
	},
}

// Seed upserts the DefaultTemplates into the store. It is idempotent: re-running
// it bumps each template's version but does not duplicate rows. Returns the first
// error encountered.
func Seed(ctx context.Context, store *Store) error {
	for i := range DefaultTemplates {
		tpl := DefaultTemplates[i]
		if err := store.Upsert(ctx, &tpl); err != nil {
			return fmt.Errorf("seed template %s/%s: %w", tpl.Channel, tpl.TemplateID, err)
		}
	}

	return nil
}
