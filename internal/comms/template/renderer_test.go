package template_test

import (
	"strings"
	"testing"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/comms/template"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderer_Render(t *testing.T) {
	r := template.New()

	tests := []struct {
		name        string
		tpl         *comms.Template
		vars        map[string]string
		wantErr     error
		wantSubject string
		wantBody    string
		bodyHas     string // substring assertion when exact match is impractical
	}{
		{
			name: "happy: SMS plain text renders with vars",
			tpl: &comms.Template{
				Channel:    comms.ChannelSMS,
				TemplateID: "phone_otp",
				Body:       "Your code is {{.code}}. Expires in {{.ttl}} min.",
			},
			vars:     map[string]string{"code": "123456", "ttl": "5"},
			wantBody: "Your code is 123456. Expires in 5 min.",
		},
		{
			name: "happy: EMAIL renders subject + html body",
			tpl: &comms.Template{
				Channel:    comms.ChannelEmail,
				TemplateID: "email_verify",
				Subject:    "Welcome {{.name}}",
				Body:       "<p>Hi {{.name}}</p>",
			},
			vars:        map[string]string{"name": "Ada"},
			wantSubject: "Welcome Ada",
			wantBody:    "<p>Hi Ada</p>",
		},
		{
			name: "edge: html-escape — injection in a var value is escaped",
			tpl: &comms.Template{
				Channel:    comms.ChannelEmail,
				TemplateID: "email_verify",
				Body:       "<p>{{.name}}</p>",
			},
			vars:    map[string]string{"name": `<script>alert('x')</script>`},
			bodyHas: "&lt;script&gt;",
		},
		{
			name: "error: missing var fails closed (never '<no value>')",
			tpl: &comms.Template{
				Channel:    comms.ChannelSMS,
				TemplateID: "phone_otp",
				Body:       "Your code is {{.code}}",
			},
			vars:    map[string]string{}, // 'code' absent
			wantErr: comms.ErrMissingVar,
		},
		{
			name: "error: missing var with nil vars map fails closed",
			tpl: &comms.Template{
				Channel:    comms.ChannelEmail,
				TemplateID: "email_verify",
				Body:       "<p>{{.token}}</p>",
			},
			vars:    nil,
			wantErr: comms.ErrMissingVar,
		},
		{
			name: "error: rendered SMS body over the cap is rejected",
			tpl: &comms.Template{
				Channel:    comms.ChannelSMS,
				TemplateID: "huge",
				Body:       "{{.big}}",
			},
			vars:    map[string]string{"big": strings.Repeat("x", 2000)},
			wantErr: comms.ErrRenderTooLarge,
		},
		{
			name:    "error: nil template is a validation error",
			tpl:     nil,
			vars:    map[string]string{},
			wantErr: comms.ErrValidation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject, body, err := r.Render(tc.tpl, tc.vars)

			if tc.wantErr != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.wantErr)
				// On error nothing is leaked.
				assert.Empty(t, subject)
				assert.Empty(t, body)

				return
			}

			require.NoError(t, err)

			if tc.wantSubject != "" {
				assert.Equal(t, tc.wantSubject, subject)
			}

			if tc.wantBody != "" {
				assert.Equal(t, tc.wantBody, body)
			}

			if tc.bodyHas != "" {
				assert.Contains(t, body, tc.bodyHas)
				// Prove the raw injection payload is NOT present unescaped.
				assert.NotContains(t, body, "<script>")
			}
		})
	}
}
