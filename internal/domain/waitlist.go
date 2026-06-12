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
// below 0x20 (excluding tab), the DEL character (0x7F), \r, \n, or a null byte.
// Per backend-security-design §5.4 and §2.1.
func controlCharCheck(s string) bool {
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\x00' {
			return true
		}

		if r < 0x20 && r != '\t' {
			return true
		}

		if r == 0x7F { // DEL
			return true
		}
	}

	return false
}

// containsAt returns true if the string contains exactly one @ with a non-empty
// local part (before @) and a domain part (after @) that contains a dot.
// Multiple @ signs are rejected: "a@b@c.com" is invalid even though LastIndex
// would have found a well-formed domain on the right side.
func containsAt(s string) bool {
	idx := strings.Index(s, "@")
	if idx <= 0 {
		return false
	}

	// Reject multiple @ signs — e.g. "a@b@c.com".
	if strings.Contains(s[idx+1:], "@") {
		return false
	}

	domain := s[idx+1:]

	return domain != "" && strings.Contains(domain, ".")
}

// validateTextField trims a raw optional field and returns a non-nil pointer only
// when the trimmed value is non-empty, valid (no control chars), and within the
// rune length cap. Whitespace-only input is treated as absent (returns nil, nil).
func validateTextField(raw string) (*string, error) {
	v := strings.TrimSpace(raw)

	if v == "" {
		return nil, nil
	}

	if controlCharCheck(v) {
		return nil, ErrWaitlistInvalidInput
	}

	if utf8.RuneCountInString(v) > maxFieldLen {
		return nil, ErrWaitlistInvalidInput
	}

	return &v, nil
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

	var err error

	if w.Company, err = validateTextField(rawCompany); err != nil {
		return nil, err
	}

	if w.InterestedIn, err = validateTextField(rawInterestedIn); err != nil {
		return nil, err
	}

	if w.Source, err = validateTextField(source); err != nil {
		return nil, err
	}

	return w, nil
}
