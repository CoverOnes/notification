package email_test

import (
	"context"
	"testing"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/comms/email"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEmailSender_factory(t *testing.T) {
	tests := []struct {
		name      string
		cfg       email.Config
		wantErr   bool
		sendErrIs error // expected Send() error sentinel (nil = no Send check)
	}{
		{
			name:      "stub provider returns dev-log sender (Send is a no-op)",
			cfg:       email.Config{Provider: "stub"},
			sendErrIs: nil,
		},
		{
			name:      "empty provider defaults to dev-log sender",
			cfg:       email.Config{Provider: ""},
			sendErrIs: nil,
		},
		{
			name:    "smtp without host fails fast",
			cfg:     email.Config{Provider: "smtp", From: "a@b.com"},
			wantErr: true,
		},
		{
			name:    "smtp without from fails fast",
			cfg:     email.Config{Provider: "smtp", Host: "smtp.example.com"},
			wantErr: true,
		},
		{
			name: "smtp with host+from constructs",
			cfg:  email.Config{Provider: "smtp", Host: "smtp.example.com", Port: 587, From: "a@b.com"},
		},
		{
			name:      "ses stub: Send returns ErrProviderNotIntegrated",
			cfg:       email.Config{Provider: "ses"},
			sendErrIs: comms.ErrProviderNotIntegrated,
		},
		{
			name:      "sendgrid stub: Send returns ErrProviderNotIntegrated",
			cfg:       email.Config{Provider: "sendgrid"},
			sendErrIs: comms.ErrProviderNotIntegrated,
		},
		{
			name:    "unknown provider errors",
			cfg:     email.Config{Provider: "carrier-pigeon"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender, err := email.NewEmailSender(&tc.cfg)

			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, sender)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, sender)

			if tc.sendErrIs != nil {
				sendErr := sender.Send(context.Background(), comms.EmailMessage{To: "a@b.com", Subject: "s", TextBody: "t"})
				assert.ErrorIs(t, sendErr, tc.sendErrIs)
			}
		})
	}
}

func TestDevLogSender_Send_noError(t *testing.T) {
	s := email.NewDevLogSender()
	err := s.Send(context.Background(), comms.EmailMessage{To: "user@example.com", Subject: "hi", TextBody: "body"})
	require.NoError(t, err, "dev-log sender must never error")
}
