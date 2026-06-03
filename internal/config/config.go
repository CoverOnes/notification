// Package config handles environment-first configuration loading for the notification service.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

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
}

// Load reads configuration from environment variables (prefix NOTIFICATION_).
func Load() (*Config, error) {
	v := viper.New()

	v.SetEnvPrefix("NOTIFICATION")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	bindings := map[string]string{
		"port":            "NOTIFICATION_PORT",
		"postgres_dsn":    "NOTIFICATION_POSTGRES_DSN",
		"postgres_schema": "NOTIFICATION_DB_SCHEMA",
		"db_max_conns":    "NOTIFICATION_DB_MAX_CONNS",
		"db_min_conns":    "NOTIFICATION_DB_MIN_CONNS",
		"redis_url":       "NOTIFICATION_REDIS_URL",
		"log_level":       "NOTIFICATION_LOG_LEVEL",
		"env":             "NOTIFICATION_ENV",
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

	if len(errs) > 0 {
		return errors.New("config validation failed: " + strings.Join(errs, "; "))
	}

	return nil
}

// IsDev reports whether the service is running in development mode.
func (c *Config) IsDev() bool {
	return strings.EqualFold(c.Env, "development")
}
