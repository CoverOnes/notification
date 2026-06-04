package comms_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustJSON marshals v to JSON, failing the test on error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	return b
}
