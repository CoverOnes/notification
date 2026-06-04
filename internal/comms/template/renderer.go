// Package template renders comms templates against caller-supplied variables.
//
// Security posture (backend-security-design §2.1): template bodies are trusted
// operator-authored content, but the variable VALUES are caller/LLM-supplied and
// therefore hostile. We render through Go's text/template + html/template engines
// (NEVER fmt.Sprintf the values into a body) so:
//   - EMAIL HTMLBody is rendered with html/template → context-aware auto-escaping.
//   - All other bodies (SMS / plain text, email subject, email TextBody) are
//     rendered with text/template.
//   - A reference to a variable the caller did NOT supply fails CLOSED with
//     comms.ErrMissingVar — we never emit Go's "<no value>" sentinel.
//   - The rendered subject and body lengths are capped.
package template

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"

	"github.com/CoverOnes/notification/internal/comms"
)

// Limits on rendered output. SMS bodies in particular must stay short; email may
// be larger. These are belt-and-suspenders caps on top of provider limits.
const (
	maxSubjectLen = 998     // RFC 5322 line-length ceiling for a header
	maxBodyLen    = 100_000 // 100 KB rendered body cap (email HTML upper bound)
	maxSMSBodyLen = 1_600   // ~10 concatenated SMS segments
)

// Renderer implements comms.Renderer using html/template for EMAIL HTML and
// text/template everywhere else. It holds no state and is safe for concurrent use.
type Renderer struct{}

// New returns a ready-to-use Renderer.
func New() *Renderer { return &Renderer{} }

// Ensure Renderer satisfies the interface at compile time.
var _ comms.Renderer = (*Renderer)(nil)

// Render renders tpl.Subject and tpl.Body against vars. For EMAIL the body is
// treated as HTML and rendered with html/template (auto-escaping); the subject
// is always plain text. For every other channel both subject and body are plain
// text. A missing variable reference returns comms.ErrMissingVar; an
// over-cap result returns comms.ErrRenderTooLarge.
func (r *Renderer) Render(tpl *comms.Template, vars map[string]string) (subject, body string, err error) {
	if tpl == nil {
		return "", "", fmt.Errorf("%w: nil template", comms.ErrValidation)
	}

	// Subject is always plain text.
	if tpl.Subject != "" {
		subject, err = renderText("subject", tpl.Subject, vars)
		if err != nil {
			return "", "", err
		}
	}

	switch tpl.Channel {
	case comms.ChannelEmail:
		body, err = renderHTML(tpl.Body, vars)
	default:
		body, err = renderText("body", tpl.Body, vars)
	}

	if err != nil {
		return "", "", err
	}

	bodyCap := maxBodyLen
	if tpl.Channel == comms.ChannelSMS {
		bodyCap = maxSMSBodyLen
	}

	if len(subject) > maxSubjectLen || len(body) > bodyCap {
		return "", "", comms.ErrRenderTooLarge
	}

	return subject, body, nil
}

// renderText renders a plain-text template. "missingkey=error" makes an
// undefined variable a render error instead of emitting "<no value>".
func renderText(name, src string, vars map[string]string) (string, error) {
	t, err := texttemplate.New(name).Option("missingkey=error").Parse(src)
	if err != nil {
		return "", fmt.Errorf("%w: parse %s template", comms.ErrValidation, name)
	}

	var buf bytes.Buffer
	if execErr := t.Execute(&buf, varsMap(vars)); execErr != nil {
		return "", classifyExecErr(execErr)
	}

	return buf.String(), nil
}

// renderHTML renders an HTML template with context-aware auto-escaping.
func renderHTML(src string, vars map[string]string) (string, error) {
	t, err := htmltemplate.New("html").Option("missingkey=error").Parse(src)
	if err != nil {
		return "", fmt.Errorf("%w: parse html template", comms.ErrValidation)
	}

	var buf bytes.Buffer
	if execErr := t.Execute(&buf, varsMap(vars)); execErr != nil {
		return "", classifyExecErr(execErr)
	}

	return buf.String(), nil
}

// varsMap returns a non-nil map so a nil caller map still triggers
// missingkey=error on the FIRST referenced variable (rather than a nil panic).
func varsMap(vars map[string]string) map[string]string {
	if vars == nil {
		return map[string]string{}
	}

	return vars
}

// classifyExecErr maps a template execution error to comms.ErrMissingVar when it
// is the missingkey=error condition, otherwise wraps it as a validation error.
// We intentionally do NOT echo the offending variable name into the typed error
// payload that may reach a client; the detail stays in the wrapped error for logs.
func classifyExecErr(err error) error {
	if err == nil {
		return nil
	}

	// text/template + html/template both render the missingkey=error condition as
	// a message containing "map has no entry for key".
	if strings.Contains(err.Error(), "map has no entry for key") {
		return fmt.Errorf("%w: %s", comms.ErrMissingVar, redactKeyName(err.Error()))
	}

	return fmt.Errorf("%w: render failed", comms.ErrValidation)
}

// redactKeyName extracts only the missing key name from the engine error so the
// log line is useful but no template internals leak. Falls back to a generic
// note when the format is unexpected.
func redactKeyName(msg string) string {
	const marker = "map has no entry for key "
	if i := strings.Index(msg, marker); i >= 0 {
		return "missing var " + strings.TrimSpace(msg[i+len(marker):])
	}

	return "missing variable"
}
