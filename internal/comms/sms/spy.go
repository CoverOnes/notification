package sms

import (
	"context"
	"sync"

	"github.com/CoverOnes/notification/internal/comms"
)

// SpySender is a test double SMSSender that records each Send call so a test can
// assert the send count and the rendered message WITHOUT a real gateway. It is
// concurrency-safe. Set Err to make Send return a canned error.
//
// It lives in a non-_test.go file so the service-layer test (a different
// package) can import it; it has zero production wiring.
type SpySender struct {
	mu    sync.Mutex
	calls []SpyCall
	// Err, when non-nil, is returned by every Send (after recording the call).
	Err error
}

// SpyCall is a single recorded Send invocation.
type SpyCall struct {
	To      string
	Message string
}

// Ensure SpySender satisfies the interface.
var _ comms.SMSSender = (*SpySender)(nil)

// Send records the call and returns s.Err (nil by default).
func (s *SpySender) Send(_ context.Context, to, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, SpyCall{To: to, Message: message})

	return s.Err
}

// Calls returns a copy of the recorded calls.
func (s *SpySender) Calls() []SpyCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]SpyCall, len(s.calls))
	copy(out, s.calls)

	return out
}

// Count returns the number of Send invocations recorded.
func (s *SpySender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.calls)
}
