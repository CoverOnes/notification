package comms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// ChannelSendRequested is the Redis pub/sub channel the comms event path
// listens on (CONVENTIONS §14: dotted lowercase <domain>.<event>).
const ChannelSendRequested = "comms.send_requested"

// SendRequestedEvent is the envelope for a comms.send_requested event. It mirrors
// the platform envelope (eventId, occurredAt, version, data) PLUS a top-level
// HMAC-SHA256 signature so the consumer can verify authenticity and DROP forged
// events (mirrors the kyc redis_publisher HMAC contract).
//
// EVENT HMAC CONTRACT (publisher signs, this service verifies — keep identical):
//
//	canonical = eventId + "|" + occurredAt(RFC3339Nano,UTC) + "|" + version +
//	            "|" + lowercaseHex(sha256(rawData))
//	signature = lowercase hex of HMAC-SHA256(EVENT_HMAC_SECRET, canonical)
//
// Binding the signature to sha256(rawData) makes it data-agnostic: any tamper to
// the data payload invalidates the signature without the contract needing to know
// the data schema.
type SendRequestedEvent struct {
	EventID    uuid.UUID         `json:"eventId"`
	OccurredAt time.Time         `json:"occurredAt"`
	Version    int               `json:"version"`
	Data       SendRequestedData `json:"data"`
	Signature  string            `json:"signature"`
}

// SendRequestedData is the send instruction carried by the event.
type SendRequestedData struct {
	Channel        Channel           `json:"channel"`
	To             string            `json:"to"`
	TemplateID     string            `json:"templateId"`
	Locale         string            `json:"locale"`
	Vars           map[string]string `json:"vars"`
	IdempotencyKey string            `json:"idempotencyKey"`
	UserID         *uuid.UUID        `json:"userId,omitempty"`
}

// eventTimeLayout is the canonical time layout for both the JSON envelope and the
// HMAC canonical string (matches Go's default time.Time JSON encoding).
const eventTimeLayout = time.RFC3339Nano

// sendRequestedCanonical builds the canonical signing string for an event.
func sendRequestedCanonical(eventID uuid.UUID, occurredAt time.Time, version int, rawData []byte) string {
	dataHash := sha256.Sum256(rawData)

	return eventID.String() + "|" +
		occurredAt.UTC().Format(eventTimeLayout) + "|" +
		strconv.Itoa(version) + "|" +
		hex.EncodeToString(dataHash[:])
}

// computeSendRequestedSignature returns the lowercase-hex HMAC-SHA256 of the
// canonical string. Shared by signing and verification so they cannot diverge.
func computeSendRequestedSignature(eventID uuid.UUID, occurredAt time.Time, version int, rawData, secret []byte) string {
	canonical := sendRequestedCanonical(eventID, occurredAt, version, rawData)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(canonical))

	return hex.EncodeToString(mac.Sum(nil))
}

// SignSendRequested computes evt's signature over its data and stores it in
// evt.Signature. Returns the signature for convenience/testing. (Provided so the
// contract has one canonical implementation and tests can produce valid events.)
func SignSendRequested(evt *SendRequestedEvent, secret []byte) (string, error) {
	rawData, err := json.Marshal(evt.Data)
	if err != nil {
		return "", err
	}

	sig := computeSendRequestedSignature(evt.EventID, evt.OccurredAt, evt.Version, rawData, secret)
	evt.Signature = sig

	return sig, nil
}

// VerifySendRequested recomputes the signature from the event fields (re-marshaling
// the data) and compares it against evt.Signature in constant time. Returns false
// for a missing or mismatched signature — the consumer DROPS such events.
func VerifySendRequested(evt *SendRequestedEvent, secret []byte) bool {
	if evt.Signature == "" {
		return false
	}

	rawData, err := json.Marshal(evt.Data)
	if err != nil {
		return false
	}

	want := computeSendRequestedSignature(evt.EventID, evt.OccurredAt, evt.Version, rawData, secret)

	return hmac.Equal([]byte(want), []byte(evt.Signature))
}

// maxEventAge bounds replay of validly-signed comms.send_requested events. The
// HMAC proves authenticity but NOT freshness; without a time bound a captured
// envelope replays forever once its idempotency_key dedup row is purged at the
// send_log 30-day TTL. Accepting only recent events closes the window to minutes.
// CWE-294 (authentication bypass by capture-replay).
const maxEventAge = 5 * time.Minute

// eventClockSkew tolerates small future-dating of occurredAt from clock drift
// between the publisher and this consumer.
const eventClockSkew = time.Minute

// IsSendRequestedFresh reports whether evt.OccurredAt sits within the accepted
// freshness window relative to now: not older than maxEventAge (stale → replay)
// and not future-dated beyond eventClockSkew. now is injected so the check is a
// pure, independently testable function.
func IsSendRequestedFresh(evt *SendRequestedEvent, now time.Time) bool {
	age := now.Sub(evt.OccurredAt)

	return age <= maxEventAge && age >= -eventClockSkew
}

// ToSendRequest converts the event data into a SendRequest. The event's eventId
// is used as a secondary idempotency guard: if the data carries no idempotency
// key, the eventId is used so a replayed event still dedups.
func (e *SendRequestedEvent) ToSendRequest() SendRequest {
	idem := e.Data.IdempotencyKey
	if idem == "" {
		idem = "evt:" + e.EventID.String()
	}

	return SendRequest{
		Channel:        e.Data.Channel,
		To:             e.Data.To,
		TemplateID:     e.Data.TemplateID,
		Locale:         e.Data.Locale,
		Vars:           e.Data.Vars,
		IdempotencyKey: idem,
		UserID:         e.Data.UserID,
	}
}
