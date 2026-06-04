# notification

CoverOnes notification microservice — in-app notification inbox and outbound comms hub (email / SMS).

## What it does

- Delivers in-app notifications to users: list inbox, unread count, and mark-as-read.
- Consumes platform events from Redis and fans them out to the inbox and, when the comms module is enabled, to outbound channels.
- Ships an independent **Comms Module** (email + SMS) designed as a self-contained, liftable unit: DB-backed templates, pluggable per-channel providers, idempotent send-log, and delivery receipt tracking.
- Comms providers are runtime-selectable (SMTP or third-party for email; AWS SNS, Chunghwa Telecom, or Huawei for SMS).
- The comms module is dormant by default; the service behaves as a pure inbox service until it is enabled via configuration.

## Where it sits

Part of the `coverones_comms` database group. Reached by browsers exclusively through the API gateway (`:8080`); direct access at `:8095` in the local dev stack. CORS is handled at the gateway layer, not here.

## API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe (checks Postgres + Redis) |
| `GET` | `/v1/me/notifications` | List inbox notifications (authenticated) |
| `GET` | `/v1/me/notifications/unread-count` | Unread notification count |
| `POST` | `/v1/me/notifications/read-all` | Mark all notifications read |
| `POST` | `/v1/me/notifications/:id/read` | Mark a single notification read |
| `POST` | `/v1/comms/send` | Send a message via the comms module (service-to-service only; requires comms enabled) |
| `POST` | `/v1/comms/receipts/:provider` | Ingest delivery receipts from a provider webhook |

## Tech

| | |
|-|--|
| Language | Go 1.25 |
| Framework | Gin |
| Database | PostgreSQL (pgx v5) |
| Cache / event bus | Redis |
| Migrations | golang-migrate (embedded SQL) |

## Run locally

Start the full dev stack from `../dev-stack`:

```bash
cd ../dev-stack
task up
```

Then verify this service is ready:

```bash
task health
```

## Environment variables

| Variable | Description |
|----------|-------------|
| `NOTIFICATION_PORT` | HTTP listen port (default `8084`) |
| `NOTIFICATION_DB_DSN` | PostgreSQL connection string |
| `NOTIFICATION_DB_SCHEMA` | Postgres schema name (default `public`; use `notification` in shared Aiven cluster) |
| `NOTIFICATION_REDIS_URL` | Redis URL; omit to disable event consumer and rate limiting |
| `NOTIFICATION_LOG_LEVEL` | Log level (`debug` / `info` / `warn` / `error`) |
| `NOTIFICATION_COMMS_ENABLED` | Set to `true` to activate the comms module |
| `NOTIFICATION_COMMS_EMAIL_PROVIDER` | Email provider name |
| `NOTIFICATION_COMMS_SMS_PROVIDER` | SMS provider name |
| `NOTIFICATION_COMMS_*` | Provider credentials and sender settings (see `.env.example`) |
