package sms_test

import (
	"context"
	"testing"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/comms/sms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSMSSender_factory(t *testing.T) {
	tests := []struct {
		name      string
		cfg       sms.Config
		wantErr   bool
		sendErrIs error
	}{
		{
			name:      "stub provider returns dev stub (Send is a no-op)",
			cfg:       sms.Config{Provider: "stub"},
			sendErrIs: nil,
		},
		{
			name:      "empty provider defaults to dev stub",
			cfg:       sms.Config{Provider: ""},
			sendErrIs: nil,
		},
		{
			name:    "aws-sns without region fails fast",
			cfg:     sms.Config{Provider: "aws-sns"},
			wantErr: true,
		},
		{
			name:      "aws-sns with region: Send returns ErrProviderNotIntegrated",
			cfg:       sms.Config{Provider: "aws-sns", Region: "ap-southeast-1"},
			sendErrIs: comms.ErrProviderNotIntegrated,
		},
		{
			name:    "huawei without creds fails fast",
			cfg:     sms.Config{Provider: "huawei", Region: "host:443"},
			wantErr: true,
		},
		{
			name:      "huawei with creds+region: Send returns ErrProviderNotIntegrated",
			cfg:       sms.Config{Provider: "huawei", APIKey: "k", APISecret: "s", Region: "host:443"},
			sendErrIs: comms.ErrProviderNotIntegrated,
		},
		{
			name:    "chunghwa without creds fails fast",
			cfg:     sms.Config{Provider: "chunghwa", Region: "https://gw"},
			wantErr: true,
		},
		{
			name:      "chunghwa with creds+gateway: Send returns ErrProviderNotIntegrated",
			cfg:       sms.Config{Provider: "chunghwa", APIKey: "u", APISecret: "p", Region: "https://gw"},
			sendErrIs: comms.ErrProviderNotIntegrated,
		},
		{
			name:    "unknown provider errors",
			cfg:     sms.Config{Provider: "smoke-signal"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender, err := sms.NewSMSSender(&tc.cfg)

			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, sender)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, sender)

			if tc.sendErrIs != nil {
				sendErr := sender.Send(context.Background(), "+15551234567", "hello")
				assert.ErrorIs(t, sendErr, tc.sendErrIs)
			}
		})
	}
}

func TestStubSender_Send_noError(t *testing.T) {
	s := sms.NewStubSender()
	require.NoError(t, s.Send(context.Background(), "+15551234567", "your code is 123456"))
}

func TestSpySender_records(t *testing.T) {
	spy := &sms.SpySender{}

	require.NoError(t, spy.Send(context.Background(), "+1", "a"))
	require.NoError(t, spy.Send(context.Background(), "+2", "b"))

	assert.Equal(t, 2, spy.Count())
	calls := spy.Calls()
	require.Len(t, calls, 2)
	assert.Equal(t, "+1", calls[0].To)
	assert.Equal(t, "b", calls[1].Message)
}
