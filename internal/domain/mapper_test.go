package domain_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEnvelope(t *testing.T, data any) domain.EventEnvelope {
	t.Helper()

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	return domain.EventEnvelope{
		EventID:    uuid.New(),
		OccurredAt: time.Now().UTC(),
		Version:    1,
		Data:       raw,
	}
}

func TestMapEventToNotification(t *testing.T) {
	userID := uuid.New()

	tests := []struct {
		name        string
		channel     string
		data        any
		wantType    domain.NotificationType
		wantErr     bool
		errContains string
	}{
		{
			name:    "kyc.tier_changed: happy path",
			channel: "kyc.tier_changed",
			data: domain.KYCTierChangedData{
				UserID:  userID,
				OldTier: 0,
				NewTier: 1,
			},
			wantType: domain.NotificationTypeKYCTierChanged,
		},
		{
			name:    "user.suspended: happy path",
			channel: "user.suspended",
			data: domain.UserSuspendedData{
				UserID: userID,
				Reason: "terms_violation",
			},
			wantType: domain.NotificationTypeAccountSuspended,
		},
		{
			name:    "marketplace.bid_received: happy path",
			channel: "marketplace.bid_received",
			data: domain.BidReceivedData{
				UserID:    userID,
				BidID:     uuid.New(),
				ListingID: uuid.New(),
			},
			wantType: domain.NotificationTypeBidReceived,
		},
		{
			name:    "marketplace.bid_accepted: happy path",
			channel: "marketplace.bid_accepted",
			data: domain.BidAcceptedData{
				UserID:    userID,
				BidID:     uuid.New(),
				ListingID: uuid.New(),
			},
			wantType: domain.NotificationTypeBidAccepted,
		},
		{
			name:    "workspace.milestone_reached: happy path",
			channel: "workspace.milestone_reached",
			data: domain.MilestoneReachedData{
				UserID:      userID,
				ContractID:  uuid.New(),
				MilestoneID: uuid.New(),
			},
			wantType: domain.NotificationTypeMilestoneReached,
		},
		{
			name:    "workspace.contract_signed: happy path",
			channel: "workspace.contract_signed",
			data: domain.ContractSignedData{
				UserID:     userID,
				ContractID: uuid.New(),
			},
			wantType: domain.NotificationTypeContractSigned,
		},
		{
			name:        "unknown channel returns error",
			channel:     "foo.unknown_event",
			data:        map[string]any{},
			wantErr:     true,
			errContains: "unknown channel",
		},
		{
			name:        "kyc.tier_changed: missing userId returns error",
			channel:     "kyc.tier_changed",
			data:        domain.KYCTierChangedData{UserID: uuid.Nil, OldTier: 0, NewTier: 1},
			wantErr:     true,
			errContains: "missing userId",
		},
		{
			name:        "user.suspended: missing userId returns error",
			channel:     "user.suspended",
			data:        domain.UserSuspendedData{UserID: uuid.Nil},
			wantErr:     true,
			errContains: "missing userId",
		},
		{
			name:     "kyc.tier_changed: malformed JSON returns error",
			channel:  "kyc.tier_changed",
			data:     nil,                                   // will produce null which doesn't Unmarshal to struct cleanly, or we use raw
			wantErr:  false,                                 // null unmarshals as zero value without error — test below separately
			wantType: domain.NotificationTypeKYCTierChanged, // userId will be uuid.Nil
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := makeEnvelope(t, tc.data)

			// Special handling for the nil userId via zero value
			if tc.name == "kyc.tier_changed: malformed JSON returns error" {
				// null data → zero value struct → uuid.Nil → error
				rawNull := json.RawMessage(`null`)
				env.Data = rawNull
				n, err := domain.MapEventToNotification(tc.channel, env)
				// null unmarshal into struct is ok (zeroed), but userId is uuid.Nil → error
				assert.Error(t, err)
				assert.Nil(t, n)
				return
			}

			n, err := domain.MapEventToNotification(tc.channel, env)

			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				assert.Nil(t, n)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, n)
			assert.Equal(t, tc.wantType, n.Type)
			assert.Equal(t, userID, n.UserID)
			assert.NotEqual(t, uuid.Nil, n.ID)
			assert.Equal(t, env.EventID, *n.SourceEventID)
			assert.NotEmpty(t, n.Title)
			assert.NotEmpty(t, n.Body)
		})
	}
}
