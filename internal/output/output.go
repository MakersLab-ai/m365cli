// Package output renders the stable JSON envelope written to stdout. Data goes
// to stdout; human-facing hints and progress belong on stderr (see cmd helpers).
package output

import (
	"encoding/json"
	"io"
)

// Envelope is the top-level shape of every --json response. Exactly one of Data
// or Error is set.
type Envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *ErrorInfo `json:"error,omitempty"`
}

// ErrorInfo carries a machine-stable error payload.
type ErrorInfo struct {
	Message string `json:"message"`
}

// WriteJSON writes a success envelope wrapping data, terminated by a newline.
func WriteJSON(w io.Writer, data any) error {
	return encode(w, Envelope{OK: true, Data: data})
}

// WriteError writes a failure envelope carrying err's message.
func WriteError(w io.Writer, err error) error {
	return encode(w, Envelope{OK: false, Error: &ErrorInfo{Message: err.Error()}})
}

func encode(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env) // Encode appends a trailing newline
}
