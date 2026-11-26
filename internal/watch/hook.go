package watch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HookError is a non-2xx answer from the hook (transport errors are returned
// as they are).
type HookError struct {
	Status int
	msg    string
}

func (e *HookError) Error() string { return e.msg }

// HookStatus returns the hook's HTTP status.
func (e *HookError) HookStatus() int { return e.Status }

// HookClient POSTs the webhook payload to a downstream receiver.
type HookClient struct {
	url   string
	token string
	http  *http.Client
}

// NewHookClient returns a client for url with an optional bearer token.
func NewHookClient(url, token string) *HookClient {
	return &HookClient{url: url, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// Post sends payload as JSON; non-2xx responses are errors.
func (c *HookClient) Post(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &HookError{Status: resp.StatusCode, msg: fmt.Sprintf("hook POST %s: %s: %s", Redact(c.url), resp.Status, string(body))}
	}
	return nil
}
