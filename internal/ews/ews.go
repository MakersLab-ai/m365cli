// Package ews is a thin Exchange Web Services (SOAP/XML) client for on-premise
// Exchange. It is the EWS counterpart of internal/graph: the central mailbox
// allowlist choke point. Every mailbox-scoped call goes through here and is
// refused for out-of-scope mailboxes before any network I/O.
//
// Auth is NTLM with a domain service account (go-ntlmssp); the account reaches
// each target mailbox via EWS ExchangeImpersonation. The client returns neutral
// Go structs (Item); mapping to the JSON output contract is the backend's job.
package ews

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Azure/go-ntlmssp"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

// Client talks EWS to a single on-premise endpoint as one service account.
type Client struct {
	URL      string
	User     string
	password string
	cfg      *config.Config
	http     *http.Client
}

// New builds an NTLM-authenticated EWS client. password is the service
// account's secret (already read from ews_password_file); it is never logged.
func New(cfg *config.Config, password string) *Client {
	return &Client{
		URL:      cfg.EWSURL,
		User:     cfg.EWSUser,
		password: password,
		cfg:      cfg,
		http:     NewNTLMClient(),
	}
}

// NewNTLMClient returns an *http.Client whose transport performs the NTLM
// handshake. NTLM is connection-bound, so keep-alives must stay enabled (the
// default) and the client must be reused across requests.
func NewNTLMClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: ntlmssp.Negotiator{
			RoundTripper: &http.Transport{},
		},
	}
}

// SetHTTPClient overrides the transport (used by tests to inject a plain client
// pointed at an httptest server, bypassing NTLM).
func (c *Client) SetHTTPClient(h *http.Client) { c.http = h }

// post sends a SOAP envelope and returns the parsed response envelope. It fails
// loudly on HTTP errors and SOAP faults; ResponseMessage-level errors are left
// for the caller to inspect per operation.
func (c *Client) post(ctx context.Context, body []byte) (*envelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// go-ntlmssp converts these Basic credentials into the NTLM handshake.
	req.SetBasicAuth(c.User, c.password)
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// A plain NTLM auth failure is a bodyless 401; surface it distinctly.
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("ews auth failed (401) for user %q — check ews_user / ews_password_file and impersonation rights", c.User)
	}

	var env envelope
	// Faults arrive with HTTP 500 and a soap:Fault body; try to parse either way.
	if xmlErr := xml.Unmarshal(respBody, &env); xmlErr != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("ews http %s: %s", resp.Status, truncate(respBody))
		}
		return nil, fmt.Errorf("ews: parse response: %w", xmlErr)
	}
	if f := env.Body.Fault; f != nil {
		code := f.Detail.ResponseCode
		if code == "" {
			code = f.FaultCode
		}
		msg := f.Detail.Message
		if msg == "" {
			msg = f.FaultString
		}
		return nil, fmt.Errorf("ews fault %s: %s", code, msg)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ews http %s: %s", resp.Status, truncate(respBody))
	}
	return &env, nil
}

// requireAllowed enforces the mailbox allowlist before any network I/O — the
// EWS choke point, mirroring internal/graph. Fail-closed: empty allowlist denies.
func (c *Client) requireAllowed(mailbox string) error {
	if !c.cfg.IsMailboxAllowed(mailbox) {
		return fmt.Errorf("mailbox %q is not in allowed_mailboxes (out of scope)", mailbox)
	}
	return nil
}

func truncate(b []byte) string {
	const max = 512
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
