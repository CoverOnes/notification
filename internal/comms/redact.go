package comms

import (
	"regexp"
	"strings"
)

// credentialPatterns is the ordered set of credential-shaped regexes scrubbed
// from any free-text that may be persisted (send_log.last_error,
// delivery_receipts.raw) — backend-security-design §3.1. Each match is replaced
// with a typed placeholder so the kind of leak is still auditable without the
// secret value itself. Even imperfect coverage is strictly better than none.
//
// NOTE: these patterns are deliberately broad and case-insensitive where the
// real-world token isn't case-sensitive. Ordering matters: more specific
// connection-string patterns run before the generic key/value ones so a DSN's
// embedded password is redacted as a DSN, not twice.
var credentialPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"postgres-dsn", regexp.MustCompile(`postgres(?:ql)?://[^:/\s]+:[^@\s]+@`)},
	{"mongodb-dsn", regexp.MustCompile(`mongodb(?:\+srv)?://[^:/\s]+:[^@\s]+@`)},
	{"redis-dsn", regexp.MustCompile(`redis://[^:/\s]+:[^@\s]+@`)},
	{"stripe-key", regexp.MustCompile(`sk_(?:live|test)_[0-9A-Za-z]+`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[0-9A-Za-z]{20,}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`)},
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"jwt-bearer", regexp.MustCompile(`(?i)Bearer\s+ey[A-Za-z0-9_\-.]+`)},
	{"password-kv", regexp.MustCompile(`(?i)password["']?\s*[=:]\s*["']?[^\s"',&]+`)},
	{"apikey-kv", regexp.MustCompile(`(?i)api[_-]?key["']?\s*[=:]\s*["']?[^\s"',&]+`)},
	{"token-kv", regexp.MustCompile(`(?i)(?:secret|(?:access[_-]?|auth[_-]?|bearer[_-]?)?token)["']?\s*[=:]\s*["']?[^\s"',&]+`)},
}

// RedactSecrets scrubs credential-shaped substrings from s, replacing each with
// a "[REDACTED:type]" placeholder. It is safe to call on any string before it is
// persisted or logged. The result is never longer than the input plus the
// placeholder overhead.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}

	out := s
	for _, p := range credentialPatterns {
		out = p.re.ReplaceAllString(out, "[REDACTED:"+p.name+"]")
	}

	return out
}

// maxErrorLen caps a persisted/last_error string so a hostile provider error
// cannot bloat a row. Applied after redaction.
const maxErrorLen = 1000

// SanitizeError redacts credentials, strips control characters and ANSI escape
// sequences, and caps length. Use this on any provider/transport error string
// before it lands in comms_send_log.last_error.
func SanitizeError(err error) string {
	if err == nil {
		return ""
	}

	return sanitizeText(err.Error(), maxErrorLen)
}

// maxReceiptLen caps a sanitized receipt/reason string.
const maxReceiptLen = 2000

// SanitizeText redacts credentials, strips control/ANSI sequences, and caps the
// length of an arbitrary provider-supplied text blob (e.g. a delivery-receipt
// body) — backend-security-design §5.4 / §6.6. Exported for the receipts path.
func SanitizeText(s string) string {
	return sanitizeText(s, maxReceiptLen)
}

// ansiEscapeRe matches CSI / OSC ANSI escape sequences which, if echoed to a
// terminal CLI listing, can rewrite the screen or inject control codes. The byte
// ranges are written as explicit hex (intermediate \x20-\x2f, final \x40-\x7e)
// to be unambiguous to the regexp engine and the linter.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// sanitizeText is the shared implementation: redact → strip ANSI → drop control
// chars (keep \t which is benign) → cap length (rune-safe).
func sanitizeText(s string, maxLen int) string {
	if s == "" {
		return s
	}

	s = RedactSecrets(s)
	s = ansiEscapeRe.ReplaceAllString(s, "")

	var b strings.Builder

	b.Grow(len(s))

	for _, r := range s {
		// Drop NUL, CR, LF and all C0 control chars except tab. These break
		// path/line/log semantics and enable log/terminal injection.
		if r == '\t' || r >= 0x20 {
			b.WriteRune(r)
		}
	}

	out := b.String()
	if len([]rune(out)) > maxLen {
		out = string([]rune(out)[:maxLen])
	}

	return out
}
