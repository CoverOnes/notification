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
			name: "comms disabled: no comms validation runs (dormant) — non-dev still needs HMAC secrets",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_COMMS_ENABLED":       "false",
				"EVENT_HMAC_SECRET":                validHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validHMAC,
			}),
			wantErr: false,
		},
		{
			name: "comms enabled in dev with stub providers + token map is valid",
			envs: merge(baseEnv(), map[string]string{ //nolint:gosec // G101: "dev-token" is a fake placeholder, not a real credential
				"NOTIFICATION_ENV":            "development",
				"NOTIFICATION_COMMS_ENABLED":  "true",
				"NOTIFICATION_S2S_TOKENS":     "user-service:dev-token",
				"NOTIFICATION_EMAIL_PROVIDER": "stub",
				"NOTIFICATION_SMS_PROVIDER":   "stub",
			}),
			wantErr: false,
		},
		{
			name: "comms enabled without S2S tokens fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":            "development",
				"NOTIFICATION_COMMS_ENABLED":  "true",
				"NOTIFICATION_EMAIL_PROVIDER": "stub",
				"NOTIFICATION_SMS_PROVIDER":   "stub",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_S2S_TOKENS is required",
		},
		{
			name: "comms enabled with malformed token map entry fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":            "development",
				"NOTIFICATION_COMMS_ENABLED":  "true",
				"NOTIFICATION_S2S_TOKENS":     "no-colon-here",
				"NOTIFICATION_EMAIL_PROVIDER": "stub",
				"NOTIFICATION_SMS_PROVIDER":   "stub",
			}),
			wantErr:     true,
			errContains: "invalid entry",
		},
		{
			name: "non-dev rejects stub email provider",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_COMMS_ENABLED":       "true",
				"NOTIFICATION_S2S_TOKENS":          "user-service:" + validToken,
				"EVENT_HMAC_SECRET":                validHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":      "stub",
				"NOTIFICATION_SMS_PROVIDER":        "aws-sns",
				"NOTIFICATION_SMS_REGION":          "ap-southeast-1",
			}),
			wantErr:     true,
			errContains: "EMAIL_PROVIDER 'stub' is not allowed outside development",
		},
		{
			name: "non-dev smtp provider without host/from fails fast",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_COMMS_ENABLED":       "true",
				"NOTIFICATION_S2S_TOKENS":          "user-service:" + validToken,
				"EVENT_HMAC_SECRET":                validHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":      "smtp",
				"NOTIFICATION_SMS_PROVIDER":        "aws-sns",
				"NOTIFICATION_SMS_REGION":          "ap-southeast-1",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_EMAIL_SMTP_HOST is required",
		},
		{
			name: "non-dev huawei provider without creds fails fast",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_COMMS_ENABLED":       "true",
				"NOTIFICATION_S2S_TOKENS":          "user-service:" + validToken,
				"EVENT_HMAC_SECRET":                validHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":      "smtp",
				"NOTIFICATION_EMAIL_SMTP_HOST":     "smtp.example.com",
				"NOTIFICATION_EMAIL_SMTP_FROM":     "no-reply@example.com",
				"NOTIFICATION_SMS_PROVIDER":        "huawei",
				"NOTIFICATION_SMS_REGION":          "smsapi.example.com:443",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_SMS_API_KEY and NOTIFICATION_SMS_API_SECRET are required",
		},
		{
			name: "non-dev short S2S token in map fails",
			envs: merge(baseEnv(), map[string]string{ //nolint:gosec // G101: "short" is a weak token testing length validation, not a real credential
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_COMMS_ENABLED":       "true",
				"NOTIFICATION_S2S_TOKENS":          "user-service:short",
				"EVENT_HMAC_SECRET":                validHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":      "smtp",
				"NOTIFICATION_EMAIL_SMTP_HOST":     "smtp.example.com",
				"NOTIFICATION_EMAIL_SMTP_FROM":     "no-reply@example.com",
				"NOTIFICATION_SMS_PROVIDER":        "aws-sns",
				"NOTIFICATION_SMS_REGION":          "ap-southeast-1",
			}),
			wantErr:     true,
			errContains: "must be at least",
		},
		{
			name: "non-dev fully-configured smtp + aws-sns + valid token map is valid",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_COMMS_ENABLED":       "true",
				"NOTIFICATION_S2S_TOKENS":          "user-service:" + validToken,
				"EVENT_HMAC_SECRET":                validHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":      "smtp",
				"NOTIFICATION_EMAIL_SMTP_HOST":     "smtp.example.com",
				"NOTIFICATION_EMAIL_SMTP_FROM":     "no-reply@example.com",
				"NOTIFICATION_SMS_PROVIDER":        "aws-sns",
				"NOTIFICATION_SMS_REGION":          "ap-southeast-1",
			}),
			wantErr: false,
		},
		{
			name: "non-dev multiple callers in token map all valid",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_COMMS_ENABLED":       "true",
				"NOTIFICATION_S2S_TOKENS":          "user-service:" + validToken + ",admin-service:this-is-a-32char-min-admin-token-value!",
				"EVENT_HMAC_SECRET":                validHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validHMAC,
				"NOTIFICATION_EMAIL_PROVIDER":      "smtp",
				"NOTIFICATION_EMAIL_SMTP_HOST":     "smtp.example.com",
				"NOTIFICATION_EMAIL_SMTP_FROM":     "no-reply@example.com",
				"NOTIFICATION_SMS_PROVIDER":        "aws-sns",
				"NOTIFICATION_SMS_REGION":          "ap-southeast-1",
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

// TestConfig_validateEventHMAC verifies that EVENT_HMAC_SECRET is required in
// non-dev regardless of whether the comms module is enabled.
func TestConfig_validateEventHMAC(t *testing.T) {
	const validGatewayHMAC = "valid-gateway-hmac-32bytes-012345"
	const validHMACVal = "valid-event-hmac-32bytes-0123456"

	tests := []struct {
		name        string
		envs        map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name: "non-dev: missing EVENT_HMAC_SECRET fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validGatewayHMAC,
			}),
			wantErr:     true,
			errContains: "EVENT_HMAC_SECRET is required in non-development",
		},
		{
			name: "non-dev: short EVENT_HMAC_SECRET fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validGatewayHMAC,
				"EVENT_HMAC_SECRET":                "short",
			}),
			wantErr:     true,
			errContains: "EVENT_HMAC_SECRET must be at least",
		},
		{
			name: "non-dev: valid EVENT_HMAC_SECRET passes",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validGatewayHMAC,
				"EVENT_HMAC_SECRET":                validHMACVal,
			}),
			wantErr: false,
		},
		{
			name: "dev: missing EVENT_HMAC_SECRET is allowed",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV": "development",
			}),
			wantErr: false,
		},
		{
			name: "non-dev: known dev-constant EVENT_HMAC_SECRET is rejected",
			envs: merge(baseEnv(), map[string]string{ //nolint:gosec // G101: value is the known-bad dev-constant being tested for rejection, not a real credential
				"NOTIFICATION_ENV":                 "production",
				"NOTIFICATION_GATEWAY_HMAC_SECRET": validGatewayHMAC,
				"EVENT_HMAC_SECRET":                "dev-shared-event-hmac-secret-min32-0123456789",
			}),
			wantErr:     true,
			errContains: "must not be a known development-default value",
		},
		{
			name: "dev: known dev-constant EVENT_HMAC_SECRET is allowed in dev",
			envs: merge(baseEnv(), map[string]string{ //nolint:gosec // G101: value is the known-bad dev-constant being tested as allowed in dev, not a real credential
				"NOTIFICATION_ENV":  "development",
				"EVENT_HMAC_SECRET": "dev-shared-event-hmac-secret-min32-0123456789",
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

// TestConfig_validateGatewayHMAC verifies that NOTIFICATION_GATEWAY_HMAC_SECRET
// is required in non-dev environments (§24.1 trust-C).
func TestConfig_validateGatewayHMAC(t *testing.T) {
	const validEventHMAC = "valid-event-hmac-32bytes-01234567"

	tests := []struct {
		name        string
		envs        map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name: "non-dev: missing NOTIFICATION_GATEWAY_HMAC_SECRET fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":  "production",
				"EVENT_HMAC_SECRET": validEventHMAC,
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_GATEWAY_HMAC_SECRET is required in non-development",
		},
		{
			name: "non-dev: short NOTIFICATION_GATEWAY_HMAC_SECRET fails",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"EVENT_HMAC_SECRET":                validEventHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": "tooshort",
			}),
			wantErr:     true,
			errContains: "NOTIFICATION_GATEWAY_HMAC_SECRET must be at least",
		},
		{
			name: "non-dev: valid NOTIFICATION_GATEWAY_HMAC_SECRET passes",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                 "production",
				"EVENT_HMAC_SECRET":                validEventHMAC,
				"NOTIFICATION_GATEWAY_HMAC_SECRET": "valid-gateway-hmac-secret-32bytes",
			}),
			wantErr: false,
		},
		{
			name: "dev: missing NOTIFICATION_GATEWAY_HMAC_SECRET is allowed",
			envs: merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV": "development",
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

// TestConfig_validateUserRateLimit verifies the per-user rate-limit validation:
// perMin < 0 is rejected, a positive perMin paired with burst <= 0 is rejected,
// perMin = 0 (disabled) passes without any burst constraint, and a valid pair passes.
func TestConfig_validateUserRateLimit(t *testing.T) {
	tests := []struct {
		name        string
		perMin      string
		burst       string
		wantErr     bool
		errContains string
	}{
		{
			name:        "perMin<0 is rejected",
			perMin:      "-1",
			burst:       "5",
			wantErr:     true,
			errContains: "NOTIFICATION_USER_RATE_LIMIT_PER_MIN must be >= 0",
		},
		{
			name:        "perMin>0 with burst<=0 is rejected",
			perMin:      "60",
			burst:       "0",
			wantErr:     true,
			errContains: "NOTIFICATION_USER_RATE_LIMIT_BURST must be > 0",
		},
		{
			name:    "perMin=0 (disabled) passes regardless of burst",
			perMin:  "0",
			burst:   "0",
			wantErr: false,
		},
		{
			name:    "valid perMin and burst passes",
			perMin:  "120",
			burst:   "20",
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := merge(baseEnv(), map[string]string{
				"NOTIFICATION_ENV":                     "development",
				"NOTIFICATION_USER_RATE_LIMIT_PER_MIN": tc.perMin,
				"NOTIFICATION_USER_RATE_LIMIT_BURST":   tc.burst,
			})
			for k, v := range env {
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
