package comms_test

import (
	"context"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEvent() *comms.SendRequestedEvent {
	uid := uuid.New()

	return &comms.SendRequestedEvent{
		EventID:    uuid.New(),
		OccurredAt: time.Now().UTC(),
		Version:    1,
		Data: comms.SendRequestedData{
			Channel:        comms.ChannelSMS,
			To:             "+15551234567",
			TemplateID:     "phone_otp",
			Vars:           map[string]string{"code": "123456"},
			IdempotencyKey: "idem-evt-1",
			UserID:         &uid,
		},
	}
}

func TestSendRequested_signVerify(t *testing.T) {
	secret := []byte("a-32-char-min-shared-event-secret!")

	evt := makeEvent()
	_, err := comms.SignSendRequested(evt, secret)
	require.NoError(t, err)
	require.NotEmpty(t, evt.Signature)

	assert.True(t, comms.VerifySendRequested(evt, secret), "valid signature must verify")
}

func TestSendRequested_verifyFails(t *testing.T) {
	secret := []byte("a-32-char-min-shared-event-secret!")

	t.Run("unsigned event is dropped", func(t *testing.T) {
		evt := makeEvent() // no Signature
		assert.False(t, comms.VerifySendRequested(evt, secret))
	})

	t.Run("wrong secret fails", func(t *testing.T) {
		evt := makeEvent()
		_, err := comms.SignSendRequested(evt, secret)
		require.NoError(t, err)
		assert.False(t, comms.VerifySendRequested(evt, []byte("different-secret-different-secret!")))
	})

	t.Run("tampered data invalidates signature", func(t *testing.T) {
		evt := makeEvent()
		_, err := comms.SignSendRequested(evt, secret)
		require.NoError(t, err)

		// Tamper the recipient AFTER signing → signature must no longer verify.
		evt.Data.To = "+19999999999"
		assert.False(t, comms.VerifySendRequested(evt, secret), "tampered data must fail verification")
	})
}

func TestSendRequested_toSendRequest(t *testing.T) {
	t.Run("uses provided idempotency key", func(t *testing.T) {
		evt := makeEvent()
		req := evt.ToSendRequest()
		assert.Equal(t, "idem-evt-1", req.IdempotencyKey)
		assert.Equal(t, comms.ChannelSMS, req.Channel)
	})

	t.Run("falls back to eventId when idempotency key absent", func(t *testing.T) {
		evt := makeEvent()
		evt.Data.IdempotencyKey = ""
		req := evt.ToSendRequest()
		assert.Equal(t, "evt:"+evt.EventID.String(), req.IdempotencyKey)
	})
}

// recordingService records the last Send for the event-handler test.
type recordingService struct {
	called int
	lastTo string
}

//nolint:gocritic // hugeParam: value receiver is fixed by the comms.CommsService interface
func (r *recordingService) Send(_ context.Context, req comms.SendRequest) (comms.SendResult, error) {
	r.called++
	r.lastTo = req.To

	return comms.SendResult{SendID: uuid.New(), Status: comms.StatusSent}, nil
}

func TestEventHandler_dropsForged(t *testing.T) {
	secret := []byte("a-32-char-min-shared-event-secret!")

	t.Run("valid signed event is dispatched", func(t *testing.T) {
		svc := &recordingService{}
		h := comms.NewEventHandler(svc, secret)

		evt := makeEvent()
		_, err := comms.SignSendRequested(evt, secret)
		require.NoError(t, err)

		h.Handle(context.Background(), mustJSON(t, evt))
		assert.Equal(t, 1, svc.called, "valid event must be dispatched")
		assert.Equal(t, "+15551234567", svc.lastTo)
	})

	t.Run("forged (unsigned) event is dropped", func(t *testing.T) {
		svc := &recordingService{}
		h := comms.NewEventHandler(svc, secret)

		evt := makeEvent() // no signature
		h.Handle(context.Background(), mustJSON(t, evt))
		assert.Equal(t, 0, svc.called, "forged event must NOT be dispatched")
	})

	t.Run("malformed payload is dropped", func(t *testing.T) {
		svc := &recordingService{}
		h := comms.NewEventHandler(svc, secret)

		h.Handle(context.Background(), []byte("{not json"))
		assert.Equal(t, 0, svc.called)
	})
}
