// Package config handles environment-first configuration loading for the notification service.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// schemaNameRe validates that a Postgres schema name only contains safe characters
// to prevent SQL injection when the name is interpolated into CREATE SCHEMA.
var schemaNameRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// Config holds all configuration for the notification service.
type Config struct {
	// Server
	Port int `mapstructure:"port"`

	// Postgres
	PostgresDSN string `mapstructure:"postgres_dsn"`

	// PostgresSchema is the optional Postgres schema to use (default: "" = public).
	// Set to "notification" when sharing one Aiven database across multiple services
	// so each service is isolated by schema rather than by database.
	// Only alphanumeric characters and underscores are allowed ([a-zA-Z0-9_]+).
	PostgresSchema string `mapstructure:"postgres_schema"`

	// DBMaxConns is the maximum number of connections in the pgxpool (default: 10).
	// Tune down when many services share a small Aiven plan.
	// Env: NOTIFICATION_DB_MAX_CONNS
	DBMaxConns int `mapstructure:"db_max_conns"`

	// DBMinConns is the minimum number of idle connections kept in the pgxpool (default: 2).
	// Env: NOTIFICATION_DB_MIN_CONNS
	DBMinConns int `mapstructure:"db_min_conns"`

	// Redis (optional — nil Redis = consumer disabled + in-process rate limiter)
	RedisURL string `mapstructure:"redis_url"`

	// Log level: DEBUG, INFO, WARN, ERROR
	LogLevel string `mapstructure:"log_level"`

	// Environment: development | production | test
	Env string `mapstructure:"env"`

	// EventHMACSecret is the shared secret used to verify inbound signed events
	// (comms.send_requested). Required in non-development when comms is enabled.
	EventHMACSecret string `mapstructure:"event_hmac_secret"`

	// Comms holds the dormant-by-default outbound-messaging (comms) module config.
	Comms CommsConfig `mapstructure:",squash"`
}

// CommsConfig holds the comms module configuration. The module is DORMANT unless
// Enabled is true (default false): when disabled the service registers nothing
// new and behaves exactly as the pure-inbox service did.
type CommsConfig struct {
	// Enabled turns the comms module on. Default false (dormant).
	Enabled bool `mapstructure:"comms_enabled"`

	// S2SToken is the shared bearer token the send API verifies (X-Service-Token).
	// Required (non-dev) when comms is enabled. Env-only.
	S2SToken string `mapstructure:"s2s_token"`

	// SendTimeout bounds a single provider send. Default 10s.
	SendTimeout time.Duration `mapstructure:"comms_send_timeout"`

	// Email provider config.
	EmailProvider   string `mapstructure:"email_provider"` // smtp | ses | sendgrid | stub
	EmailSMTPHost   string `mapstructure:"email_smtp_host"`
	EmailSMTPPort   int    `mapstructure:"email_smtp_port"`
	EmailFrom       string `mapstructure:"email_smtp_from"`
	EmailAppBaseURL string `mapstructure:"email_app_base_url"`
	EmailSMTPUser   string `mapstructure:"email_smtp_username"` // credential — env-only
	EmailSMTPPass   string `mapstructure:"email_smtp_password"` // credential — env-only

	// SMS provider config.
	SMSProvider  string `mapstructure:"sms_provider"` // stub | aws-sns | huawei | chunghwa
	SMSSenderID  string `mapstructure:"sms_sender_id"`
	SMSRegion    string `mapstructure:"sms_region"`
	SMSAPIKey    string `mapstructure:"sms_api_key"`    // credential — env-only
	SMSAPISecret string `mapstructure:"sms_api_secret"` // credential — env-only
}

// Load reads configuration from environment variables (prefix NOTIFICATION_).
func Load() (*Config, error) {
	v := viper.New()

	v.SetEnvPrefix("NOTIFICATION")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	//nolint:gosec // G101 false positive: these are env-var NAMES (e.g. NOTIFICATION_S2S_TOKEN, EVENT_HMAC_SECRET), not credential values
	bindings := map[string]string{
		"port":              "NOTIFICATION_PORT",
		"postgres_dsn":      "NOTIFICATION_POSTGRES_DSN",
		"postgres_schema":   "NOTIFICATION_DB_SCHEMA",
		"db_max_conns":      "NOTIFICATION_DB_MAX_CONNS",
		"db_min_conns":      "NOTIFICATION_DB_MIN_CONNS",
		"redis_url":         "NOTIFICATION_REDIS_URL",
		"log_level":         "NOTIFICATION_LOG_LEVEL",
		"env":               "NOTIFICATION_ENV",
		"event_hmac_secret": "EVENT_HMAC_SECRET",

		// Comms module (dormant by default).
		"comms_enabled":      "NOTIFICATION_COMMS_ENABLED",
		"s2s_token":          "NOTIFICATION_S2S_TOKEN",
		"comms_send_timeout": "NOTIFICATION_COMMS_SEND_TIMEOUT",

		"email_provider":      "NOTIFICATION_EMAIL_PROVIDER",
		"email_smtp_host":     "NOTIFICATION_EMAIL_SMTP_HOST",
		"email_smtp_port":     "NOTIFICATION_EMAIL_SMTP_PORT",
		"email_smtp_from":     "NOTIFICATION_EMAIL_SMTP_FROM",
		"email_app_base_url":  "NOTIFICATION_EMAIL_APP_BASE_URL",
		"email_smtp_username": "NOTIFICATION_EMAIL_SMTP_USERNAME",
		"email_smtp_password": "NOTIFICATION_EMAIL_SMTP_PASSWORD",

		"sms_provider":   "NOTIFICATION_SMS_PROVIDER",
		"sms_sender_id":  "NOTIFICATION_SMS_SENDER_ID",
		"sms_region":     "NOTIFICATION_SMS_REGION",
		"sms_api_key":    "NOTIFICATION_SMS_API_KEY",
		"sms_api_secret": "NOTIFICATION_SMS_API_SECRET",
	}

	for key, envKey := range bindings {
		if err := v.BindEnv(key, envKey); err != nil {
			return nil, fmt.Errorf("config bind %q: %w", key, err)
		}
	}

	v.SetDefault("port", 8084)
	v.SetDefault("log_level", "INFO")
	v.SetDefault("env", "development")
	v.SetDefault("db_max_conns", 10)
	v.SetDefault("db_min_conns", 2)
	v.SetDefault("comms_enabled", false)
	v.SetDefault("comms_send_timeout", 10*time.Second)
	v.SetDefault("email_provider", "stub")
	v.SetDefault("sms_provider", "stub")

	var cfg Config

	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []string

	if c.PostgresDSN == "" {
		errs = append(errs, "NOTIFICATION_POSTGRES_DSN is required")
	}

	if c.Port <= 0 || c.Port > 65535 {
		errs = append(errs, "NOTIFICATION_PORT must be 1-65535")
	}

	validLogLevels := map[string]bool{"DEBUG": true, "INFO": true, "WARN": true, "ERROR": true}
	if !validLogLevels[strings.ToUpper(c.LogLevel)] {
		errs = append(errs, "NOTIFICATION_LOG_LEVEL must be DEBUG|INFO|WARN|ERROR")
	}

	validEnvs := map[string]bool{"development": true, "production": true, "test": true}
	if !validEnvs[strings.ToLower(c.Env)] {
		errs = append(errs, "NOTIFICATION_ENV must be development|production|test")
	}

	if c.PostgresSchema != "" && !schemaNameRe.MatchString(c.PostgresSchema) {
		errs = append(errs, "NOTIFICATION_DB_SCHEMA must contain only [a-zA-Z0-9_] characters")
	}

	if c.DBMaxConns < 0 {
		errs = append(errs, "NOTIFICATION_DB_MAX_CONNS must be >= 0 (0 = use default of 10)")
	}

	if c.DBMinConns < 0 {
		errs = append(errs, "NOTIFICATION_DB_MIN_CONNS must be >= 0 (0 = use default of 2)")
	}

	if c.DBMaxConns > 0 && c.DBMinConns > 0 && c.DBMinConns > c.DBMaxConns {
		errs = append(errs, "NOTIFICATION_DB_MIN_CONNS must be <= NOTIFICATION_DB_MAX_CONNS")
	}

	errs = append(errs, c.validateComms()...)

	if len(errs) > 0 {
		return errors.New("config validation failed: " + strings.Join(errs, "; "))
	}

	return nil
}

// minS2STokenLen is the minimum NOTIFICATION_S2S_TOKEN length. Below this a
// brute-force against the shared send-API token becomes practical.
const minS2STokenLen = 24

// validEmailProviders / validSMSProviders are the accepted provider names.
var (
	validEmailProviders = map[string]bool{"smtp": true, "ses": true, "sendgrid": true, "stub": true, "": true}
	validSMSProviders   = map[string]bool{"stub": true, "aws-sns": true, "huawei": true, "chunghwa": true, "": true}
)

// validateComms enforces the comms module invariants. When the module is
// DISABLED nothing is validated (it is dormant and wires nothing). When enabled,
// secrets and provider selections are checked with fail-fast in non-dev so a
// misconfigured real provider can never silently no-op.
func (c *Config) validateComms() []string {
	if !c.Comms.Enabled {
		return nil
	}

	var errs []string

	dev := c.IsDev()

	emailProv := strings.ToLower(strings.TrimSpace(c.Comms.EmailProvider))
	if !validEmailProviders[emailProv] {
		errs = append(errs, "NOTIFICATION_EMAIL_PROVIDER must be smtp|ses|sendgrid|stub")
	}

	smsProv := strings.ToLower(strings.TrimSpace(c.Comms.SMSProvider))
	if !validSMSProviders[smsProv] {
		errs = append(errs, "NOTIFICATION_SMS_PROVIDER must be stub|aws-sns|huawei|chunghwa")
	}

	// S2S token: required when comms is enabled; length-checked in non-dev.
	switch {
	case c.Comms.S2SToken == "":
		errs = append(errs, "NOTIFICATION_S2S_TOKEN is required when comms is enabled")
	case !dev && len(c.Comms.S2SToken) < minS2STokenLen:
		errs = append(errs, fmt.Sprintf("NOTIFICATION_S2S_TOKEN must be at least %d characters", minS2STokenLen))
	}

	// Event HMAC secret: required in non-dev when comms is enabled (the event path
	// verifies signatures; an empty secret would make forgery trivial).
	if !dev && c.EventHMACSecret == "" {
		errs = append(errs, "EVENT_HMAC_SECRET is required when comms is enabled in non-development")
	}

	if !dev {
		errs = append(errs, c.validateCommsNonDev(emailProv, smsProv)...)
	}

	return errs
}

// validateCommsNonDev enforces the non-development fail-fast rules: a 'stub'
// provider is rejected and a real provider with empty credentials is rejected at
// boot (mirrors the kyc fail-fast pattern).
func (c *Config) validateCommsNonDev(emailProv, smsProv string) []string {
	var errs []string

	// Reject the dev-only stub providers in non-dev.
	if emailProv == "" || emailProv == "stub" {
		errs = append(errs, "NOTIFICATION_EMAIL_PROVIDER 'stub' is not allowed outside development")
	}

	if smsProv == "" || smsProv == "stub" {
		errs = append(errs, "NOTIFICATION_SMS_PROVIDER 'stub' is not allowed outside development")
	}

	// Real provider with empty credentials → boot error (no silent no-op).
	if emailProv == "smtp" {
		if c.Comms.EmailSMTPHost == "" {
			errs = append(errs, "NOTIFICATION_EMAIL_SMTP_HOST is required for the smtp email provider")
		}

		if c.Comms.EmailFrom == "" {
			errs = append(errs, "NOTIFICATION_EMAIL_SMTP_FROM is required for the smtp email provider")
		}
	}

	switch smsProv {
	case "aws-sns":
		if c.Comms.SMSRegion == "" {
			errs = append(errs, "NOTIFICATION_SMS_REGION is required for the aws-sns provider")
		}
	case "huawei", "chunghwa":
		if c.Comms.SMSAPIKey == "" || c.Comms.SMSAPISecret == "" {
			errs = append(errs, fmt.Sprintf("NOTIFICATION_SMS_API_KEY and NOTIFICATION_SMS_API_SECRET are required for the %s provider", smsProv))
		}

		if c.Comms.SMSRegion == "" {
			errs = append(errs, fmt.Sprintf("NOTIFICATION_SMS_REGION is required for the %s provider", smsProv))
		}
	}

	return errs
}

// IsDev reports whether the service is running in development mode.
func (c *Config) IsDev() bool {
	return strings.EqualFold(c.Env, "development")
}
