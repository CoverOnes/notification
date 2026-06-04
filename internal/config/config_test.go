package config_test

import (
	"testing"

	"github.com/CoverOnes/notification/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDSN is a placeholder DSN for config validation tests.
// Not a real credential — used only to satisfy the non-empty DSN requirement.
const testDSN = "postgres://localhost:5432/testdb?sslmode=disable"

func TestConfig_validate(t *testing.T) {
	tests := []struct {
		name        string
		envs        map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config loads successfully",
			envs: map[string]string{
				"NOTIFICATION_POSTGRES_DSN": testDSN,
				"NOTIFICATION_PORT":         "8084",
				"NOTIFICATION_LOG_LEVEL":    "INFO",
				"NOTIFICATION_ENV":          "development",
			},
			wantErr: false,
		},
		{
			name: "missing postgres DSN fails validation",
			envs: map[string]string{
				"NOTIFICATION_PORT":      "8084",
				"NOTIFICATION_LOG_LEVEL": "INFO",
				"NOTIFICATION_ENV":       "development",
			},
			wantErr:     true,
			errContains: "NOTIFICATION_POSTGRES_DSN is required",
		},
		{
			name: "invalid log level fails validation",
			envs: map[string]string{
				"NOTIFICATION_POSTGRES_DSN": testDSN,
				"NOTIFICATION_PORT":         "8084",
				"NOTIFICATION_LOG_LEVEL":    "VERBOSE",
				"NOTIFICATION_ENV":          "development",
			},
			wantErr:     true,
			errContains: "NOTIFICATION_LOG_LEVEL must be",
		},
		{
			name: "invalid env fails validation",
			envs: map[string]string{
				"NOTIFICATION_POSTGRES_DSN": testDSN,
				"NOTIFICATION_PORT":         "8084",
				"NOTIFICATION_LOG_LEVEL":    "INFO",
				"NOTIFICATION_ENV":          "staging",
			},
			wantErr:     true,
			errContains: "NOTIFICATION_ENV must be",
		},
		{
			name: "invalid port fails validation",
			envs: map[string]string{
				"NOTIFICATION_POSTGRES_DSN": testDSN,
				"NOTIFICATION_PORT":         "99999",
				"NOTIFICATION_LOG_LEVEL":    "INFO",
				"NOTIFICATION_ENV":          "development",
			},
			wantErr:     true,
			errContains: "NOTIFICATION_PORT must be",
		},
		{
			name: "custom db_max_conns and db_min_conns accepted",
			envs: map[string]string{
				"NOTIFICATION_POSTGRES_DSN": testDSN,
				"NOTIFICATION_PORT":         "8084",
				"NOTIFICATION_LOG_LEVEL":    "INFO",
				"NOTIFICATION_ENV":          "development",
				"NOTIFICATION_DB_MAX_CONNS": "5",
				"NOTIFICATION_DB_MIN_CONNS": "1",
			},
			wantErr: false,
		},
		{
			name: "db_min_conns greater than db_max_conns fails validation",
			envs: map[string]string{
				"NOTIFICATION_POSTGRES_DSN": testDSN,
				"NOTIFICATION_PORT":         "8084",
				"NOTIFICATION_LOG_LEVEL":    "INFO",
				"NOTIFICATION_ENV":          "development",
				"NOTIFICATION_DB_MAX_CONNS": "3",
				"NOTIFICATION_DB_MIN_CONNS": "5",
			},
			wantErr:     true,
			errContains: "NOTIFICATION_DB_MIN_CONNS must be <=",
		},
		{
			name: "zero db_max_conns uses default (no error)",
			envs: map[string]string{
				"NOTIFICATION_POSTGRES_DSN": testDSN,
				"NOTIFICATION_PORT":         "8084",
				"NOTIFICATION_LOG_LEVEL":    "INFO",
				"NOTIFICATION_ENV":          "development",
				"NOTIFICATION_DB_MAX_CONNS": "0",
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.envs {
				t.Setenv(k, v)
			}

			cfg, err := config.Load()

			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				assert.Nil(t, cfg)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)
		})
	}
}

// baseEnv returns a minimal valid env that callers extend per case.
func baseEnv() map[string]string {
	return map[string]string{
		"NOTIFICATION_POSTGRES_DSN": testDSN,
		"NOTIFICATION_PORT":         "8084",
		"NOTIFICATION_LOG_LEVEL":    "INFO",
	}
}

func TestConfig_validateComms(t *testing.T) {
	const validToken = "this-is-a-32char-min-s2s-token-value!" //nolint:gosec // G101: fake test token, not a real credential

	const validHMAC = "this-is-a-32char-min-event-secret!!"

	tests := []struct {
		name        string
		envs        map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name: "comms disabled: no comms validation runs (dormant)",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":           "production",
				"NOTIFICATION_COMMS_ENABLED": "false",
			}),
			wantErr: false,
		},
		{
			name: "comms enabled in dev with stub providers + token is valid",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":            "development",
				"NOTIFICATION_COMMS_ENABLED":  "true",
				"NOTIFICATION_S2S_TOKEN":      "dev-token",
				"NOTIFICATION_EMAIL_PROVIDER": "stub",
				"NOTIFICATION_SMS_PROVIDER":   "stub",
			}),
			wantErr: false,
		},
		{
			name: "comms enabled without S2S token fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":            "development",
				"NOTIFICATION_COMMS_ENABLED":  "true",
				"NOTIFICATION_EMAIL_PROVIDER": "stub",
				"NOTIFICATION_SMS_PROVIDER":   "stub",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_S2S_TOKEN is required",
		},
		{
			name: "non-dev rejects stub email provider",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":            "production",
				"NOTIFICATION_COMMS_ENABLED":  "true",
				"NOTIFICATION_S2S_TOKEN":      validToken,
				"EVENT_HMAC_SECRET":           validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER": "stub",
				"NOTIFICATION_SMS_PROVIDER":   "aws-sns",
				"NOTIFICATION_SMS_REGION":     "ap-southeast-1",
			}),
			wantErr:     true,
			errContains: "EMAIL_PROVIDER 'stub' is not allowed outside development",
		},
		{
			name: "non-dev smtp provider without host/from fails fast",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":            "production",
				"NOTIFICATION_COMMS_ENABLED":  "true",
				"NOTIFICATION_S2S_TOKEN":      validToken,
				"EVENT_HMAC_SECRET":           validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER": "smtp",
				"NOTIFICATION_SMS_PROVIDER":   "aws-sns",
				"NOTIFICATION_SMS_REGION":     "ap-southeast-1",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_EMAIL_SMTP_HOST is required",
		},
		{
			name: "non-dev huawei provider without creds fails fast",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":             "production",
				"NOTIFICATION_COMMS_ENABLED":   "true",
				"NOTIFICATION_S2S_TOKEN":       validToken,
				"EVENT_HMAC_SECRET":            validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":  "smtp",
				"NOTIFICATION_EMAIL_SMTP_HOST": "smtp.example.com",
				"NOTIFICATION_EMAIL_SMTP_FROM": "no-reply@example.com",
				"NOTIFICATION_SMS_PROVIDER":    "huawei",
				"NOTIFICATION_SMS_REGION":      "smsapi.example.com:443",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_SMS_API_KEY and NOTIFICATION_SMS_API_SECRET are required",
		},
		{
			name: "non-dev short S2S token fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":             "production",
				"NOTIFICATION_COMMS_ENABLED":   "true",
				"NOTIFICATION_S2S_TOKEN":       "short",
				"EVENT_HMAC_SECRET":            validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":  "smtp",
				"NOTIFICATION_EMAIL_SMTP_HOST": "smtp.example.com",
				"NOTIFICATION_EMAIL_SMTP_FROM": "no-reply@example.com",
				"NOTIFICATION_SMS_PROVIDER":    "aws-sns",
				"NOTIFICATION_SMS_REGION":      "ap-southeast-1",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_S2S_TOKEN must be at least",
		},
		{
			name: "non-dev fully-configured smtp + aws-sns is valid",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":             "production",
				"NOTIFICATION_COMMS_ENABLED":   "true",
				"NOTIFICATION_S2S_TOKEN":       validToken,
				"EVENT_HMAC_SECRET":            validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":  "smtp",
				"NOTIFICATION_EMAIL_SMTP_HOST": "smtp.example.com",
				"NOTIFICATION_EMAIL_SMTP_FROM": "no-reply@example.com",
				"NOTIFICATION_SMS_PROVIDER":    "aws-sns",
				"NOTIFICATION_SMS_REGION":      "ap-southeast-1",
			}),
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.envs {
				t.Setenv(k, v)
			}

			cfg, err := config.Load()

			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				assert.Nil(t, cfg)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)
		})
	}
}

// merge returns a new map combining base + extra (extra wins).
func merge(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}

	for k, v := range extra {
		out[k] = v
	}

	return out
}
