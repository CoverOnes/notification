package comms

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// EventHandler processes comms.send_requested events from Redis. It HMAC-verifies
// the envelope and DROPS forged events, then converges on CommsService.Send.
// It is the event-path counterpart of the S2S HTTP handler; both paths run the
// same Send pipeline (idempotency dedup included).
type EventHandler struct {
	svc        CommsService
	hmacSecret []byte
}

// NewEventHandler builds an EventHandler. hmacSecret is the shared
// EVENT_HMAC_SECRET used to verify event authenticity.
func NewEventHandler(svc CommsService, hmacSecret []byte) *EventHandler {
	return &EventHandler{svc: svc, hmacSecret: hmacSecret}
}

// Handle parses, verifies, and dispatches a single comms.send_requested payload.
// All failures are logged at Warn and swallowed (the caller's subscribe loop must
// stay alive). A forged / unsigned event is dropped without dispatch.
func (h *EventHandler) Handle(ctx context.Context, payload []byte) {
	var evt SendRequestedEvent

	if err := json.Unmarshal(payload, &evt); err != nil {
		slog.Warn("comms event: malformed payload; dropping", "err", err)

		return
	}

	if !VerifySendRequested(&evt, h.hmacSecret) {
		// Forged or unsigned — DROP. Do not log the data (may carry PII).
		slog.Warn("comms event: signature verification failed; dropping forged event", "event_id", evt.EventID)

		return
	}

	if !IsSendRequestedFresh(&evt, time.Now()) {
		// Validly signed but stale or future-dated — possible capture-replay
		// (CWE-294). DROP; the HMAC alone does not bound freshness.
		slog.Warn(
			"comms event: stale/future-dated event; dropping (replay guard)",
			"event_id", evt.EventID,
			"occurred_at", evt.OccurredAt,
		)

		return
	}

	req := evt.ToSendRequest()

	res, err := h.svc.Send(ctx, req)
	if err != nil {
		// Errors are typed; never echo the rendered body or recipient.
		slog.Warn(
			"comms event: send failed",
			"event_id", evt.EventID,
			"channel", evt.Data.Channel,
			"err", err,
		)

		return
	}

	slog.Info(
		"comms event: processed",
		"event_id", evt.EventID,
		"channel", evt.Data.Channel,
		"send_id", res.SendID,
		"status", res.Status,
		"deduped", res.Deduped,
	)
}
