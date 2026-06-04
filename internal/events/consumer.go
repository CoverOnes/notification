// Package events provides the Redis event consumer for the notification service.
// This service CONSUMES events (pub/sub), it does NOT publish.
package events

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/redis/go-redis/v9"
)

// inboxChannels is the set of Redis pub/sub channels mapped into the in-app
// inbox. CONVENTIONS §14: dotted lowercase <domain>.<event>.
var inboxChannels = []string{
	"kyc.tier_changed",
	"user.suspended",
	"marketplace.bid_received",
	"marketplace.bid_accepted",
	"workspace.milestone_reached",
	"workspace.contract_signed",
}

// commsSendRequestedChannel is the comms-module event channel. It is only
// subscribed when the comms module is enabled (a non-nil commsHandler is wired).
const commsSendRequestedChannel = "comms.send_requested"

// CommsEventHandler handles a verified comms.send_requested payload. It is
// satisfied by *comms.EventHandler. Declared here so the events package does not
// hard-depend on the comms module when comms is dormant.
type CommsEventHandler interface {
	Handle(ctx context.Context, payload []byte)
}

// Consumer subscribes to Redis event channels and maps them to Notifications.
// When commsHandler is non-nil it ALSO subscribes comms.send_requested and routes
// that channel to the comms module (HMAC-verified there); inbox channels are
// unaffected. When commsHandler is nil the consumer behaves exactly as before.
type Consumer struct {
	rdb          *redis.Client
	store        store.NotificationStore
	commsHandler CommsEventHandler
}

// NewConsumer creates a Consumer for the inbox channels only (comms dormant).
// If rdb is nil the consumer is a no-op (dev mode).
func NewConsumer(rdb *redis.Client, s store.NotificationStore) *Consumer {
	return &Consumer{rdb: rdb, store: s}
}

// NewConsumerWithComms creates a Consumer that additionally subscribes
// comms.send_requested and routes it to commsHandler. Used only when the comms
// module is enabled.
func NewConsumerWithComms(rdb *redis.Client, s store.NotificationStore, commsHandler CommsEventHandler) *Consumer {
	return &Consumer{rdb: rdb, store: s, commsHandler: commsHandler}
}

// channels returns the full subscribe set for this consumer (inbox + optionally
// the comms channel).
func (c *Consumer) channels() []string {
	if c.commsHandler == nil {
		return inboxChannels
	}

	return append(append([]string{}, inboxChannels...), commsSendRequestedChannel)
}

// Run starts the subscription loop. Blocks until ctx is canceled.
// Designed to run in a goroutine with a context.Background()-derived context so
// that it is not canceled when a request context expires.
// Resilient: bad/unknown payload -> slog.Warn + skip, NEVER crashes the loop.
func (c *Consumer) Run(ctx context.Context) {
	if c.rdb == nil {
		slog.Info("redis consumer disabled: no Redis client configured")
		<-ctx.Done()

		return
	}

	channels := c.channels()

	sub := c.rdb.Subscribe(ctx, channels...)
	defer func() {
		if err := sub.Close(); err != nil {
			slog.Warn("consumer: close subscription error", "err", err)
		}
	}()

	slog.Info("redis consumer started", "channels", channels)

	ch := sub.Channel()

	for {
		select {
		case <-ctx.Done():
			slog.Info("redis consumer stopping")
			return

		case msg, ok := <-ch:
			if !ok {
				slog.Warn("redis consumer channel closed; stopping")
				return
			}

			c.handle(ctx, msg)
		}
	}
}

// maxPayloadBytes is the maximum accepted event payload size (64 KiB).
// Payloads above this limit are logged and silently dropped to prevent DoS.
const maxPayloadBytes = 64 * 1024

// handle processes a single pub/sub message.
// All errors are logged as Warn and skipped to keep the loop alive.
func (c *Consumer) handle(ctx context.Context, msg *redis.Message) {
	if len(msg.Payload) > maxPayloadBytes {
		slog.Warn(
			"consumer: oversized event payload; skipping",
			"channel", msg.Channel,
			"size", len(msg.Payload),
		)

		return
	}

	// Route the comms-module channel to the comms handler (HMAC-verified there).
	// Inbox channels fall through to the notification mapping path below.
	if msg.Channel == commsSendRequestedChannel {
		if c.commsHandler != nil {
			c.commsHandler.Handle(ctx, []byte(msg.Payload))
		}

		return
	}

	var env domain.EventEnvelope

	if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
		slog.Warn(
			"consumer: malformed event payload; skipping",
			"channel", msg.Channel,
			"err", err,
		)

		return
	}

	n, err := domain.MapEventToNotification(msg.Channel, env)
	if err != nil {
		slog.Warn(
			"consumer: cannot map event to notification; skipping",
			"channel", msg.Channel,
			"event_id", env.EventID,
			"err", err,
		)

		return
	}

	// Guard: if the mapped notification's data blob exceeds the limit, drop it
	// rather than persisting potentially huge JSONB to the DB.
	if len(n.Data) > maxPayloadBytes {
		slog.Warn(
			"consumer: notification data exceeds size limit; clearing data before insert",
			"channel", msg.Channel,
			"event_id", env.EventID,
			"data_size", len(n.Data),
		)

		n.Data = nil
	}

	if insertErr := c.store.Insert(ctx, n); insertErr != nil {
		slog.Warn(
			"consumer: failed to insert notification; skipping",
			"channel", msg.Channel,
			"event_id", env.EventID,
			"err", insertErr,
		)

		return
	}

	slog.Info(
		"consumer: notification created",
		"channel", msg.Channel,
		"event_id", env.EventID,
		"user_id", n.UserID,
		"notification_id", n.ID,
	)
}
