package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Logger writes timestamped, redacted watch log lines to stderr (or any
// writer) and rate-limits identical error lines.
//
// Line formats (kept compatible with existing log scrapers that grep
// '^watch ' for errors and 'mail watch: polling [' for the scope):
//
//	watch <mailbox>: <error> [ts=2026-11-26T11:00:00Z]
//	m365 mail watch: <info> [ts=2026-11-26T11:00:00Z]
//
// Nothing logged here may contain a delta/next link query, a bearer token or a
// message body: every line goes through Redact.
type Logger struct {
	W     io.Writer
	Now   func() time.Time
	Every time.Duration // identical errors are logged at most once per Every (default 1m)

	mu   sync.Mutex
	last map[string]*rlEntry
}

type rlEntry struct {
	at         time.Time
	suppressed int
}

// NewLogger returns a Logger writing to w.
func NewLogger(w io.Writer) *Logger { return &Logger{W: w} }

func (l *Logger) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Logger) ts() string { return l.now().UTC().Format(time.RFC3339) }

// Infof logs an informational line (not rate-limited, not an error).
func (l *Logger) Infof(format string, args ...any) {
	msg := Redact(fmt.Sprintf(format, args...))
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.W, "m365 mail watch: %s [ts=%s]\n", msg, l.ts())
}

// Errorf logs an error line for mailbox. An identical line (after redaction)
// within Every is suppressed and counted; the next emitted line reports the
// count.
func (l *Logger) Errorf(mailbox, format string, args ...any) {
	msg := Redact(fmt.Sprintf(format, args...))
	every := l.Every
	if every <= 0 {
		every = time.Minute
	}
	now := l.now()
	key := mailbox + "\x00" + msg
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil || len(l.last) > 1000 {
		l.last = map[string]*rlEntry{}
	}
	e := l.last[key]
	if e != nil && now.Sub(e.at) < every {
		e.suppressed++
		return
	}
	suffix := ""
	if e != nil && e.suppressed > 0 {
		suffix = fmt.Sprintf(" (repeated %d more times since %s)", e.suppressed, e.at.UTC().Format(time.RFC3339))
	}
	l.last[key] = &rlEntry{at: now}
	fmt.Fprintf(l.W, "watch %s: %s%s [ts=%s]\n", mailbox, msg, suffix, now.UTC().Format(time.RFC3339))
}

var (
	reURLQuery = regexp.MustCompile(`(https?://[^\s"'?<>]+)\?([^\s"'<>]*)`)
	reToken    = regexp.MustCompile(`(?i)(\$?(?:deltatoken|skiptoken))=[^&\s"'<>]+`)
	reBearer   = regexp.MustCompile(`(?i)(bearer)\s+[A-Za-z0-9._~+/=-]+`)
)

// Redact removes delta/skip tokens, URL query strings and bearer tokens from s.
// A URL's query is replaced by an 8-hex-digit hash so the same link can still
// be correlated across lines.
func Redact(s string) string {
	s = strings.ReplaceAll(s, "\n", "; ") // errors.Join → one log line
	s = reURLQuery.ReplaceAllStringFunc(s, func(m string) string {
		sub := reURLQuery.FindStringSubmatch(m)
		return sub[1] + "?#" + shortHash(sub[2])
	})
	s = reToken.ReplaceAllString(s, "$1=<redacted>")
	s = reBearer.ReplaceAllString(s, "$1 <redacted>")
	return s
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}
