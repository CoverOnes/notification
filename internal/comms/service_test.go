package comms_test

import (
	"context"
	"errors"
	"testing"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/comms/sms"
	"github.com/CoverOnes/notification/internal/comms/template"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSendLog is an in-memory comms.SendLogStore for orchestrator unit tests.
type fakeSendLog struct {
	rows    map[string]*comms.SendLogRow // keyed by idempotency_key
	results []fakeResult
}

type fakeResult struct {
	id            uuid.UUID
	status        string
	providerMsgID string
	err           error
}

func newFakeSendLog() *fakeSendLog {
	return &fakeSendLog{rows: map[string]*comms.SendLogRow{}}
}

func (f *fakeSendLog) Reserve(_ context.Context, r *comms.SendLogRow) (*comms.SendLogRow, bool, error) {
	if existing, ok := f.rows[r.IdempotencyKey]; ok {
		return existing, false, nil
	}

	r.ID = uuid.New()
	r.Status = comms.StatusPending
	f.rows[r.IdempotencyKey] = r

	return r, true, nil
}

func (f *fakeSendLog) MarkResult(_ context.Context, id uuid.UUID, status, providerMsgID string, sendErr error) error {
	f.results = append(f.results, fakeResult{id: id, status: status, providerMsgID: providerMsgID, err: sendErr})

	return nil
}

// fakeTemplates is an in-memory comms.TemplateStore.
type fakeTemplates struct {
	tpl *comms.Template
	err error
}

func (f *fakeTemplates) Get(_ context.Context, _ comms.Channel, _, _ string) (*comms.Template, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.tpl, nil
}

func smsTemplate() *comms.Template {
	return &comms.Template{
		Channel:    comms.ChannelSMS,
		TemplateID: "phone_otp",
		Body:       "Your code is {{.code}}.",
	}
}

func baseReq() comms.SendRequest {
	return comms.SendRequest{
		Channel:        comms.ChannelSMS,
		To:             "+15551234567",
		TemplateID:     "phone_otp",
		IdempotencyKey: "idem-1",
		Vars:           map[string]string{"code": "424242"},
	}
}

func newService(sl comms.SendLogStore, tpls comms.TemplateStore, smsSender comms.SMSSender) *comms.Service {
	return comms.NewService(&comms.ServiceDeps{
		Templates:   tpls,
		Renderer:    template.New(),
		SendLog:     sl,
		SMSSender:   smsSender,
		SMSProvider: "stub",
	})
}

func TestService_Send_happyPath(t *testing.T) {
	sl := newFakeSendLog()
	spy := &sms.SpySender{}
	svc := newService(sl, &fakeTemplates{tpl: smsTemplate()}, spy)

	res, err := svc.Send(context.Background(), baseReq())
	require.NoError(t, err)

	assert.Equal(t, comms.StatusSent, res.Status)
	assert.False(t, res.Deduped)
	require.Equal(t, 1, spy.Count(), "provider must be called exactly once")
	assert.Equal(t, "Your code is 424242.", spy.Calls()[0].Message)
	assert.Equal(t, "+15551234567", spy.Calls()[0].To)

	// send_log row stores sha256(recipient), never plaintext.
	row := sl.rows["idem-1"]
	require.NotNil(t, row)
	assert.Equal(t, comms.HashRecipient("+15551234567"), row.ToHash)
	assert.NotContains(t, string(row.ToHash), "5551234567")
}

func TestService_Send_dedup(t *testing.T) {
	sl := newFakeSendLog()
	spy := &sms.SpySender{}
	svc := newService(sl, &fakeTemplates{tpl: smsTemplate()}, spy)

	_, err := svc.Send(context.Background(), baseReq())
	require.NoError(t, err)

	// Second send with the SAME idempotency key must dedup: no second provider call.
	res2, err := svc.Send(context.Background(), baseReq())
	require.NoError(t, err)
	assert.True(t, res2.Deduped, "duplicate idempotency key must be deduped")
	assert.Equal(t, 1, spy.Count(), "provider must NOT be called again on dedup")
}

func TestService_Send_errors(t *testing.T) {
	tests := []struct {
		name    string
		req     comms.SendRequest
		tpls    comms.TemplateStore
		sender  comms.SMSSender
		wantErr error
	}{
		{
			name:    "validation: unknown channel",
			req:     comms.SendRequest{Channel: "TELEPATHY", To: "x", TemplateID: "t", IdempotencyKey: "k"},
			tpls:    &fakeTemplates{tpl: smsTemplate()},
			sender:  &sms.SpySender{},
			wantErr: comms.ErrValidation,
		},
		{
			name:    "validation: empty recipient",
			req:     comms.SendRequest{Channel: comms.ChannelSMS, To: "", TemplateID: "t", IdempotencyKey: "k"},
			tpls:    &fakeTemplates{tpl: smsTemplate()},
			sender:  &sms.SpySender{},
			wantErr: comms.ErrValidation,
		},
		{
			name:    "validation: empty idempotency key",
			req:     comms.SendRequest{Channel: comms.ChannelSMS, To: "+1", TemplateID: "t", IdempotencyKey: ""},
			tpls:    &fakeTemplates{tpl: smsTemplate()},
			sender:  &sms.SpySender{},
			wantErr: comms.ErrValidation,
		},
		{
			name:    "template not found is recorded + returned",
			req:     baseReq(),
			tpls:    &fakeTemplates{err: comms.ErrTemplateNotFound},
			sender:  &sms.SpySender{},
			wantErr: comms.ErrTemplateNotFound,
		},
		{
			name:    "missing var fails closed",
			req:     comms.SendRequest{Channel: comms.ChannelSMS, To: "+1", TemplateID: "phone_otp", IdempotencyKey: "k", Vars: map[string]string{}},
			tpls:    &fakeTemplates{tpl: smsTemplate()},
			sender:  &sms.SpySender{},
			wantErr: comms.ErrMissingVar,
		},
		{
			name:    "provider error surfaces (and is recorded FAILED)",
			req:     baseReq(),
			tpls:    &fakeTemplates{tpl: smsTemplate()},
			sender:  &sms.SpySender{Err: errors.New("gateway down token=SECRET")},
			wantErr: nil, // a raw provider error, not a sentinel
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sl := newFakeSendLog()
			svc := newService(sl, tc.tpls, tc.sender)

			_, err := svc.Send(context.Background(), tc.req)
			require.Error(t, err)

			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			}

			// Any failure AFTER reservation must be recorded as FAILED in send_log.
			if len(sl.rows) > 0 {
				var sawFailed bool
				for _, r := range sl.results {
					if r.status == comms.StatusFailed {
						sawFailed = true
					}
				}
				assert.True(t, sawFailed, "post-reserve failure must record FAILED status")
			}
		})
	}
}

func TestService_Send_providerNotIntegrated(t *testing.T) {
	// EMAIL with a stub that returns ErrProviderNotIntegrated.
	sl := newFakeSendLog()
	svc := comms.NewService(&comms.ServiceDeps{
		Templates:   &fakeTemplates{tpl: &comms.Template{Channel: comms.ChannelEmail, TemplateID: "x", Subject: "s", Body: "<p>hi</p>"}},
		Renderer:    template.New(),
		SendLog:     sl,
		EmailSender: notIntegratedEmail{},
	})

	req := comms.SendRequest{Channel: comms.ChannelEmail, To: "a@b.com", TemplateID: "x", IdempotencyKey: "k"}

	_, err := svc.Send(context.Background(), req)
	require.Error(t, err)
	assert.True(t, comms.IsProviderUnavailable(err), "must be classifiable as provider-unavailable")
}

// notIntegratedEmail always returns ErrProviderNotIntegrated.
type notIntegratedEmail struct{}

func (notIntegratedEmail) Send(_ context.Context, _ comms.EmailMessage) error {
	return comms.ErrProviderNotIntegrated
}
