// Command server starts the CoverOnes notification microservice.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CoverOnes/notification/internal/comms"
	commsemail "github.com/CoverOnes/notification/internal/comms/email"
	commsprovider "github.com/CoverOnes/notification/internal/comms/provider"
	commssendlog "github.com/CoverOnes/notification/internal/comms/sendlog"
	commssms "github.com/CoverOnes/notification/internal/comms/sms"
	commstemplate "github.com/CoverOnes/notification/internal/comms/template"
	"github.com/CoverOnes/notification/internal/config"
	"github.com/CoverOnes/notification/internal/events"
	"github.com/CoverOnes/notification/internal/handler"
	"github.com/CoverOnes/notification/internal/platform/logger"
	"github.com/CoverOnes/notification/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "perform a liveness check against /healthz and exit 0/1")
	flag.Parse()

	// Docker HEALTHCHECK mode: GET /healthz and exit immediately.
	if *healthcheck {
		if err := runHealthCheck(); err != nil {
			slog.Error("healthcheck failed", "err", err)
			os.Exit(1)
		}

		os.Exit(0)
	}

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// runHealthCheck issues a GET to the local /healthz endpoint.
func runHealthCheck() error {
	port := os.Getenv("NOTIFICATION_PORT")
	if port == "" {
		port = "8084"
	}

	url := fmt.Sprintf("http://127.0.0.1:%s/healthz", port)

	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(url) //nolint:noctx // healthcheck is a one-shot process; no request context needed
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}

	defer resp.Body.Close() //nolint:errcheck // best-effort close on healthcheck response

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	return nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Logger — JSON to stdout (CONVENTIONS §5).
	log := logger.New(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Postgres pool (CONVENTIONS §12).
	// cfg.PostgresSchema is "" by default (public schema); set NOTIFICATION_DB_SCHEMA
	// to isolate this service within a shared Aiven database.
	// cfg.DBMaxConns/DBMinConns are configurable via NOTIFICATION_DB_MAX_CONNS/DB_MIN_CONNS
	// (default 10/2) to allow tuning per-service connection budgets on shared Aiven plans.
	pool, err := postgres.NewPool(ctx, cfg.PostgresDSN, cfg.PostgresSchema, postgres.PoolConfig{
		MaxConns: int32(cfg.DBMaxConns), // validated ≥ 0 by config.validate(); safe to int32
		MinConns: int32(cfg.DBMinConns), // validated ≥ 0 by config.validate(); safe to int32
	})
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}

	defer pool.Close()

	slog.Info("postgres connected")

	// Redis client (optional — nil means consumer disabled + in-process rate limiter).
	var redisClient *redis.Client

	if cfg.RedisURL != "" {
		opts, parseErr := redis.ParseURL(cfg.RedisURL)
		if parseErr != nil {
			return fmt.Errorf("parse redis url: %w", parseErr)
		}

		redisClient = redis.NewClient(opts)

		pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
		defer pingCancel()

		if pingErr := redisClient.Ping(pingCtx).Err(); pingErr != nil {
			slog.Warn("redis ping failed; event consumer and rate limiting will use noop/fallback", "err", pingErr)
			redisClient = nil
		} else {
			slog.Info("redis connected")
		}
	}

	// Store layer.
	notifStore := postgres.NewNotificationStore(pool)
	waitlistStore := postgres.NewWaitlistStore(pool)

	// Comms module — DORMANT by default (NOTIFICATION_COMMS_ENABLED=false). When
	// disabled, commsSvc is nil: no comms routes, no comms event subscription —
	// the service behaves exactly as the pure-inbox service did.
	var commsSvc comms.CommsService

	var commsEventHandler events.CommsEventHandler

	if cfg.Comms.Enabled {
		svc, err := buildCommsService(ctx, pool, cfg)
		if err != nil {
			return fmt.Errorf("init comms module: %w", err)
		}

		commsSvc = svc
		commsEventHandler = comms.NewEventHandler(svc, []byte(cfg.EventHMACSecret))

		slog.Info(
			"comms module enabled",
			"email_provider", cfg.Comms.EmailProvider,
			"sms_provider", cfg.Comms.SMSProvider,
		)
	}

	// Redis event consumer — runs in background goroutine with independent context.
	// context.Background() derivative ensures the consumer is not canceled by
	// HTTP request context expiry (goroutine context rule — backend-security-design §goroutine).
	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()

	kycHMACSecret := []byte(cfg.EventHMACSecret)

	var consumer *events.Consumer
	if commsEventHandler != nil {
		consumer = events.NewConsumerWithComms(redisClient, notifStore, commsEventHandler, kycHMACSecret)
	} else {
		consumer = events.NewConsumer(redisClient, notifStore, kycHMACSecret)
	}

	go consumer.Run(consumerCtx)

	// Router.
	r := handler.NewRouter(&handler.RouterConfig{
		Store:               notifStore,
		WaitlistStore:       waitlistStore,
		Pool:                pool,
		Redis:               redisClient,
		GatewayHMACSecret:   cfg.GatewayHMACSecret,
		UserRateLimitPerMin: cfg.UserRateLimitPerMin,
		UserRateLimitBurst:  cfg.UserRateLimitBurst,
		GatewayCIDR:         cfg.GatewayCIDR,
		CommsService:        commsSvc,
		S2STokenMap:         cfg.Comms.S2STokenMap,
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("server starting", "addr", srv.Addr)

		if listenErr := srv.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			slog.Error("server listen error", "err", listenErr)
			os.Exit(1)
		}
	}()

	<-quit
	slog.Info("shutting down gracefully")

	// Cancel consumer first, then HTTP server.
	consumerCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
		return fmt.Errorf("server shutdown: %w", shutdownErr)
	}

	slog.Info("server stopped")

	return nil
}

// buildCommsService constructs the comms orchestrator and its stores/providers
// from config, and seeds the default templates. Secrets are sourced from config
// (env-only); the provider factories fail fast on a misconfigured real provider.
func buildCommsService(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) (*comms.Service, error) {
	templateStore := commstemplate.NewStore(pool)
	sendLogStore := commssendlog.NewStore(pool)
	renderer := commstemplate.New()

	// Seed default templates (idempotent — bumps version, never duplicates).
	if err := commstemplate.Seed(ctx, templateStore); err != nil {
		return nil, fmt.Errorf("seed comms templates: %w", err)
	}

	emailSender, err := commsemail.NewEmailSender(&commsemail.Config{
		Provider:    cfg.Comms.EmailProvider,
		Host:        cfg.Comms.EmailSMTPHost,
		Port:        cfg.Comms.EmailSMTPPort,
		Username:    cfg.Comms.EmailSMTPUser,
		Password:    cfg.Comms.EmailSMTPPass,
		From:        cfg.Comms.EmailFrom,
		AppBaseURL:  cfg.Comms.EmailAppBaseURL,
		SendTimeout: cfg.Comms.SendTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("init email sender: %w", err)
	}

	smsSender, err := commssms.NewSMSSender(&commssms.Config{
		Provider:    cfg.Comms.SMSProvider,
		SenderID:    cfg.Comms.SMSSenderID,
		Region:      cfg.Comms.SMSRegion,
		APIKey:      cfg.Comms.SMSAPIKey,
		APISecret:   cfg.Comms.SMSAPISecret,
		SendTimeout: cfg.Comms.SendTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("init sms sender: %w", err)
	}

	// Provider-settings store is wired (admin/non-secret knobs); not yet consulted
	// in the Phase 0 send path (provider is selected by config). Constructed here
	// so the table has a typed accessor and the wiring is exercised.
	_ = commsprovider.NewStore(pool)

	return comms.NewService(&comms.ServiceDeps{
		Templates:     templateStore,
		Renderer:      renderer,
		SendLog:       sendLogStore,
		EmailSender:   emailSender,
		SMSSender:     smsSender,
		EmailProvider: cfg.Comms.EmailProvider,
		SMSProvider:   cfg.Comms.SMSProvider,
		SendTimeout:   cfg.Comms.SendTimeout,
	}), nil
}
