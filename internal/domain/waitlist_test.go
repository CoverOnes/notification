package domain_test

import (
	"strings"
	"testing"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWaitlistEntry(t *testing.T) {
	tests := []struct {
		name         string
		email        string
		company      string
		interestedIn string
		source       string
		wantErr      error
		wantEmailOut string // expected normalized email (trimmed)
	}{
		// Happy paths
		{
			name:         "valid minimal email",
			email:        "alice@example.com",
			wantEmailOut: "alice@example.com",
		},
		{
			name:         "email with leading/trailing whitespace is trimmed",
			email:        "  alice@example.com  ",
			wantEmailOut: "alice@example.com",
		},
		{
			name:         "valid email with optional fields",
			email:        "bob@example.com",
			company:      "Acme Corp",
			interestedIn: "risk",
			source:       "web-form",
			wantEmailOut: "bob@example.com",
		},
		{
			name:         "company at exactly 200 runes is accepted",
			email:        "x@example.com",
			company:      strings.Repeat("a", 200),
			wantEmailOut: "x@example.com",
		},
		{
			name:         "interestedIn at exactly 200 runes is accepted",
			email:        "y@example.com",
			interestedIn: strings.Repeat("b", 200),
			wantEmailOut: "y@example.com",
		},

		// Email error paths
		{
			name:    "empty email returns ErrWaitlistInvalidEmail",
			email:   "",
			wantErr: domain.ErrWaitlistInvalidEmail,
		},
		{
			name:    "whitespace-only email returns ErrWaitlistInvalidEmail",
			email:   "   ",
			wantErr: domain.ErrWaitlistInvalidEmail,
		},
		{
			name:    "email without @ returns ErrWaitlistInvalidEmail",
			email:   "notanemail",
			wantErr: domain.ErrWaitlistInvalidEmail,
		},
		{
			name:    "email without domain dot returns ErrWaitlistInvalidEmail",
			email:   "user@nodot",
			wantErr: domain.ErrWaitlistInvalidEmail,
		},
		{
			name:    "email longer than 320 chars returns ErrWaitlistInvalidEmail",
			email:   strings.Repeat("a", 316) + "@x.co",
			wantErr: domain.ErrWaitlistInvalidEmail,
		},

		// Control-char / null-byte error paths
		{
			name:    "email with null byte returns ErrWaitlistInvalidInput",
			email:   "user\x00@example.com",
			wantErr: domain.ErrWaitlistInvalidInput,
		},
		{
			name:    "email with newline returns ErrWaitlistInvalidInput",
			email:   "user\n@example.com",
			wantErr: domain.ErrWaitlistInvalidInput,
		},
		{
			name:    "email with carriage return returns ErrWaitlistInvalidInput",
			email:   "user\r@example.com",
			wantErr: domain.ErrWaitlistInvalidInput,
		},
		{
			name:    "email with ASCII control char 0x01 returns ErrWaitlistInvalidInput",
			email:   "user\x01@example.com",
			wantErr: domain.ErrWaitlistInvalidInput,
		},
		{
			name:    "company with null byte returns ErrWaitlistInvalidInput",
			email:   "ok@example.com",
			company: "Acme\x00Corp",
			wantErr: domain.ErrWaitlistInvalidInput,
		},
		{
			name:    "company with newline returns ErrWaitlistInvalidInput",
			email:   "ok@example.com",
			company: "Acme\nCorp",
			wantErr: domain.ErrWaitlistInvalidInput,
		},
		{
			name:         "company with tab is accepted (tab is allowed per §5.4)",
			email:        "ok@example.com",
			company:      "Acme\tCorp",
			wantEmailOut: "ok@example.com",
		},
		{
			name:    "company exceeds 200 runes returns ErrWaitlistInvalidInput",
			email:   "ok@example.com",
			company: strings.Repeat("a", 201),
			wantErr: domain.ErrWaitlistInvalidInput,
		},
		{
			name:         "interestedIn with control char returns ErrWaitlistInvalidInput",
			email:        "ok@example.com",
			interestedIn: "risk\x05tools",
			wantErr:      domain.ErrWaitlistInvalidInput,
		},
		{
			name:         "interestedIn exceeds 200 runes returns ErrWaitlistInvalidInput",
			email:        "ok@example.com",
			interestedIn: strings.Repeat("b", 201),
			wantErr:      domain.ErrWaitlistInvalidInput,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := domain.NewWaitlistEntry(tc.email, tc.company, tc.interestedIn, tc.source)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, entry)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, entry)
			assert.Equal(t, tc.wantEmailOut, entry.Email)
			// ID must be set.
			assert.NotEmpty(t, entry.ID)
			// CreatedAt must be set.
			assert.False(t, entry.CreatedAt.IsZero())
		})
	}
}
