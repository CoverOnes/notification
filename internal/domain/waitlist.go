package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Sentinel errors for waitlist operations.
var (
	// ErrWaitlistInvalidEmail is returned when the submitted email fails validation.
	ErrWaitlistInvalidEmail = errors.New("invalid email address")
	// ErrWaitlistInvalidInput is returned when a field contains disallowed content.
	ErrWaitlistInvalidInput = errors.New("invalid input")
)

// Waitlist is the domain object for a waitlist submission.
type Waitlist struct {
	ID           uuid.UUID
	Email        string
	Company      *string
	InterestedIn *string
	Source       *string
	CreatedAt    time.Time
}

// maxEmailLen is the maximum length of an RFC 5321 email address (local + @ + domain).
const maxEmailLen = 320

// maxFieldLen is the maximum rune count for optional free-text fields.
const maxFieldLen = 200

// controlCharCheck returns true if the string contains any ASCII control character
// below 0x20 (excluding tab) or contains \r or \n.
// Per backend-security-design §5.4 and §2.1.
func controlCharCheck(s string) bool {
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\x00' {
			return true
		}

		if r < 0x20 && r != '\t' {
			return true
		}
	}

	return false
}

// containsAt returns true if the string contains exactly one @ with non-empty
// local and domain parts (basic RFC sanity check).
func containsAt(s string) bool {
	idx := strings.LastIndex(s, "@")
	if idx <= 0 {
		return false
	}

	domain := s[idx+1:]

	return domain != "" && strings.Contains(domain, ".")
}

// NewWaitlistEntry validates and normalises raw input, returning a ready-to-persist
// Waitlist value. Source is set by the caller (e.g. "web-form") and is not
// validated for format beyond the control-char and length rules.
func NewWaitlistEntry(rawEmail, rawCompany, rawInterestedIn, source string) (*Waitlist, error) {
	email := strings.TrimSpace(rawEmail)

	if email == "" {
		return nil, ErrWaitlistInvalidEmail
	}

	if len(email) > maxEmailLen {
		return nil, ErrWaitlistInvalidEmail
	}

	if controlCharCheck(email) {
		return nil, ErrWaitlistInvalidInput
	}

	if !containsAt(email) {
		return nil, ErrWaitlistInvalidEmail
	}

	w := &Waitlist{
		ID:        uuid.New(),
		Email:     email,
		CreatedAt: time.Now().UTC(),
	}

	if rawCompany != "" {
		company := strings.TrimSpace(rawCompany)

		if controlCharCheck(company) {
			return nil, ErrWaitlistInvalidInput
		}

		if utf8.RuneCountInString(company) > maxFieldLen {
			return nil, ErrWaitlistInvalidInput
		}

		w.Company = &company
	}

	if rawInterestedIn != "" {
		interestedIn := strings.TrimSpace(rawInterestedIn)

		if controlCharCheck(interestedIn) {
			return nil, ErrWaitlistInvalidInput
		}

		if utf8.RuneCountInString(interestedIn) > maxFieldLen {
			return nil, ErrWaitlistInvalidInput
		}

		w.InterestedIn = &interestedIn
	}

	if source != "" {
		src := strings.TrimSpace(source)

		if controlCharCheck(src) {
			return nil, ErrWaitlistInvalidInput
		}

		w.Source = &src
	}

	return w, nil
}
