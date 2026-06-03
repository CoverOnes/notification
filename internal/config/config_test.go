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
