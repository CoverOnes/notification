package domain_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenStatusChangedHex is the shared cross-repo HMAC-SHA256 golden vector.
// It must equal the kyc repo's goldenStatusChangedHex constant — any divergence
// means the publisher (kyc) and consumer (notification) have a contract mismatch.
//
// Fixed inputs (identical to kyc repo's TestSignStatusChanged_GoldenVector):
//
//	eventId   = "11111111-1111-1111-1111-111111111111"
//	occurredAt = time.RFC3339Nano parse of "2026-06-05T00:00:00Z" → "2026-06-05T00:00:00Z"
//	version   = 1
//	userId    = "22222222-2222-2222-2222-222222222222"
//	newStatus = "APPROVED"
//	newTier   = 2
//	secret    = "golden-test-secret-min-32-bytes-aaaaaaaa"
//
// canonical: "11111111-1111-1111-1111-111111111111|2026-06-05T00:00:00Z|1|22222222-2222-2222-2222-222222222222|APPROVED|2"
const goldenStatusChangedHex = "cd413437e380bdc7fe0d59e08e27a94b35ff417f6d7c9ca269df4d7bd86f0dd3"

const testEventHMACVal = "test-event-hmac-32bytes-01234567!"

// buildStatusChangedSig computes the canonical HMAC-SHA256 signature for a
// kyc.status_changed event, matching the shared contract in VerifyStatusChangedSignature.
func buildStatusChangedSig(secret []byte, env *domain.SignedEventEnvelope, data *domain.KYCStatusChangedData) string {
	canonical := strings.Join([]string{
		env.EventID.String(),
		env.OccurredAt.UTC().Format(time.RFC3339Nano),
		strconv.Itoa(env.Version),
		data.UserID.String(),
		data.NewStatus,
		strconv.Itoa(data.NewTier),
	}, "|")

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))

	return hex.EncodeToString(mac.Sum(nil))
}

func makeStatusChangedEnvelope(userID uuid.UUID, newStatus string, newTier int) (domain.SignedEventEnvelope, domain.KYCStatusChangedData) {
	data := domain.KYCStatusChangedData{
		UserID:       userID,
		OldStatus:    "PENDING",
		NewStatus:    newStatus,
		OldTier:      0,
		NewTier:      newTier,
		SubmissionID: uuid.New(),
		RequestID:    "req-test-123",
	}

	env := domain.SignedEventEnvelope{
		EventEnvelope: domain.EventEnvelope{
			EventID:    uuid.New(),
			OccurredAt: time.Now().UTC(),
			Version:    1,
		},
	}
	env.Signature = buildStatusChangedSig([]byte(testEventHMACVal), &env, &data)

	return env, data
}

// TestVerifyStatusChangedSignature_GoldenVector asserts that notification's
// VerifyStatusChangedSignature ACCEPTS the golden signature produced by kyc's
// SignStatusChanged for the same fixed inputs. The goldenStatusChangedHex constant
// is identical in both repos, proving byte-for-byte contract compatibility.
func TestVerifyStatusChangedSignature_GoldenVector(t *testing.T) {
	t.Parallel()

	goldenOccurredAt, err := time.Parse(time.RFC3339Nano, "2026-06-05T00:00:00Z")
	require.NoError(t, err)

	env := domain.SignedEventEnvelope{
		EventEnvelope: domain.EventEnvelope{
			EventID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			OccurredAt: goldenOccurredAt.UTC(),
			Version:    1,
		},
		Signature: goldenStatusChangedHex,
	}

	data := &domain.KYCStatusChangedData{
		UserID:    uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		NewStatus: "APPROVED",
		NewTier:   2,
	}

	secret := []byte("golden-test-secret-min-32-bytes-aaaaaaaa")

	ok := domain.VerifyStatusChangedSignature(&env, data, secret)
	assert.True(t, ok,
		"VerifyStatusChangedSignature must accept kyc's golden HMAC vector — contract mismatch if false")
}

func TestVerifyStatusChangedSignature(t *testing.T) {
	secret := []byte(testEventHMACVal)
	userID := uuid.New()

	validEnv, validData := makeStatusChangedEnvelope(userID, "APPROVED", 1)

	tests := []struct {
		name   string
		env    domain.SignedEventEnvelope
		data   domain.KYCStatusChangedData
		secret []byte
		wantOK bool
	}{
		{
			name:   "valid signature → true",
			env:    validEnv,
			data:   validData,
			secret: secret,
			wantOK: true,
		},
		{
			name:   "empty signature → false (drop)",
			env:    domain.SignedEventEnvelope{EventEnvelope: validEnv.EventEnvelope, Signature: ""},
			data:   validData,
			secret: secret,
			wantOK: false,
		},
		{
			name: "tampered newStatus → false",
			env: func() domain.SignedEventEnvelope {
				e := validEnv // copy
				return e
			}(),
			data: func() domain.KYCStatusChangedData {
				d := validData
				d.NewStatus = "REJECTED" // tampered
				return d
			}(),
			secret: secret,
			wantOK: false,
		},
		{
			name: "tampered newTier → false",
			env:  validEnv,
			data: func() domain.KYCStatusChangedData {
				d := validData
				d.NewTier = 99 // tampered
				return d
			}(),
			secret: secret,
			wantOK: false,
		},
		{
			name:   "wrong secret → false",
			env:    validEnv,
			data:   validData,
			secret: []byte("wrong-secret"),
			wantOK: false,
		},
		{
			name:   "empty secret → false",
			env:    validEnv,
			data:   validData,
			secret: []byte(""),
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			data := tc.data
			got := domain.VerifyStatusChangedSignature(&env, &data, tc.secret)
			assert.Equal(t, tc.wantOK, got)
		})
	}
}

func TestMapKYCStatusChanged(t *testing.T) {
	userID := uuid.New()

	baseEnv := domain.EventEnvelope{
		EventID:    uuid.New(),
		OccurredAt: time.Now().UTC(),
		Version:    1,
	}

	tests := []struct {
		name        string
		env         domain.EventEnvelope
		data        domain.KYCStatusChangedData
		wantErr     bool
		errContains string
		wantTitle   string
		wantType    domain.NotificationType
	}{
		{
			name: "APPROVED status → KYC Approved title",
			env:  baseEnv,
			data: domain.KYCStatusChangedData{
				UserID:    userID,
				NewStatus: "APPROVED",
				NewTier:   1,
			},
			wantTitle: "KYC Approved",
			wantType:  domain.NotificationTypeKYCStatusChanged,
		},
		{
			name: "REJECTED status → KYC Not Approved title",
			env:  baseEnv,
			data: domain.KYCStatusChangedData{
				UserID:    userID,
				NewStatus: "REJECTED",
			},
			wantTitle: "KYC Not Approved",
			wantType:  domain.NotificationTypeKYCStatusChanged,
		},
		{
			name: "unknown status → generic title",
			env:  baseEnv,
			data: domain.KYCStatusChangedData{
				UserID:    userID,
				NewStatus: "UNDER_REVIEW",
			},
			wantTitle: "KYC Status Updated",
			wantType:  domain.NotificationTypeKYCStatusChanged,
		},
		{
			name: "missing userId → error",
			env:  baseEnv,
			data: domain.KYCStatusChangedData{
				UserID:    uuid.Nil,
				NewStatus: "APPROVED",
			},
			wantErr:     true,
			errContains: "missing userId",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.data
			n, err := domain.MapKYCStatusChanged(tc.env, &data)

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
			assert.Equal(t, tc.wantTitle, n.Title)
			assert.Equal(t, tc.wantType, n.Type)
			assert.Equal(t, tc.data.UserID, n.UserID)
			assert.Equal(t, tc.env.EventID, *n.SourceEventID)
			assert.NotEqual(t, uuid.Nil, n.ID)
			assert.NotEmpty(t, n.Body)
			// PII §15: Data field must not contain raw event payload.
			assert.Nil(t, n.Data, "data field must be nil (no raw payload stored)")
		})
	}
}
