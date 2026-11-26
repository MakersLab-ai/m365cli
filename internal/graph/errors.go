package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIError is a non-2xx Graph response. It is typed so callers can react to
// specific failures (a lost delta sync state, throttling) without matching on
// error strings. Error() never contains the request's query string: delta and
// next links carry opaque sync tokens that must not end up in logs.
type APIError struct {
	Method     string
	Path       string // redacted: query string replaced by a short hash
	Status     int
	StatusText string
	Code       string // Graph error.code, e.g. "SyncStateNotFound"
	Message    string // Graph error.message (or a capped raw body when not JSON)
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	detail := e.Message
	if e.Code != "" {
		detail = e.Code + ": " + e.Message
	}
	return fmt.Sprintf("graph %s: %s: %s", e.Path, e.StatusText, detail)
}

// HTTPStatus returns the response status code.
func (e *APIError) HTTPStatus() int { return e.Status }

// SyncStateLost reports whether Graph says the delta sync state a request
// referred to is gone, so the link can never succeed again and the only way
// out is a fresh prime:
//   - any 410 Gone, whatever the body (observed with SyncStateNotFound; the
//     delta query overview documents 410 as "restart with a full sync");
//   - any 4xx whose error.code is syncStateNotFound, resyncRequired or
//     syncStateInvalid (case-insensitive; Microsoft documents "a 40X-series
//     error with error codes such as syncStateNotFound").
func (e *APIError) SyncStateLost() bool {
	if e.Status == http.StatusGone {
		return true
	}
	if e.Status < 400 || e.Status >= 500 {
		return false
	}
	for _, c := range lostStateCodes {
		if strings.EqualFold(e.Code, c) {
			return true
		}
	}
	return false
}

var lostStateCodes = []string{"syncStateNotFound", "resyncRequired", "syncStateInvalid"}

// RetryAfterDuration is the server-requested wait (Retry-After) for 429/503,
// zero when absent or unparsable.
func (e *APIError) RetryAfterDuration() time.Duration { return e.RetryAfter }

// IsSyncStateLost reports whether err (or anything it wraps) is a lost delta
// sync state (see APIError.SyncStateLost).
func IsSyncStateLost(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.SyncStateLost()
}

const maxErrorBody = 300

func newAPIError(method, path string, resp *http.Response, body []byte, now time.Time) *APIError {
	e := &APIError{
		Method: method, Path: RedactURL(path),
		Status: resp.StatusCode, StatusText: resp.Status,
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := string(body)
	if json.Unmarshal(body, &env) == nil && env.Error.Code != "" {
		e.Code, msg = env.Error.Code, env.Error.Message
	}
	if len(msg) > maxErrorBody {
		msg = msg[:maxErrorBody] + "…"
	}
	e.Message = msg
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		e.RetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"), now)
	}
	return e
}

// ParseRetryAfter parses a Retry-After header: delta-seconds or an HTTP-date.
// Returns 0 when absent, unparsable or in the past.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// RedactURL removes the query string (delta/skip tokens, filters) from a URL or
// path and replaces it with an 8-hex-digit hash so two log lines about the same
// link can still be correlated: "https://h/p?#3fa2c1d0".
func RedactURL(raw string) string {
	i := strings.IndexByte(raw, '?')
	if i < 0 {
		return raw
	}
	return raw[:i] + "?#" + ShortHash(raw[i+1:])
}

// ShortHash is the first 8 hex digits of sha256(s).
func ShortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// redactTransportError strips the query string from the URL carried by a
// *url.Error (net/http includes the full request URL, tokens included, in
// transport errors such as timeouts and connection resets).
func redactTransportError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = RedactURL(ue.URL)
	}
	return err
}
