// Package graph is a thin Microsoft Graph REST client. It is the central choke
// point for the mailbox allowlist: every mailbox-scoped call goes through
// GetForMailbox, which refuses out-of-scope mailboxes before any network I/O
// (defense-in-depth on top of RBAC-for-Applications).
package graph

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

// TokenSource provides app-only bearer tokens for Graph.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Client talks to Microsoft Graph v1.0.
type Client struct {
	BaseURL string
	cfg     *config.Config
	tokens  TokenSource
	http    *http.Client
}

// New builds a Graph client bound to cfg's allowlists and a token source.
func New(cfg *config.Config, tokens TokenSource) *Client {
	return &Client{
		BaseURL: "https://graph.microsoft.com/v1.0",
		cfg:     cfg,
		tokens:  tokens,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// GetForMailbox performs GET /users/{mailbox}/{suffix} after verifying mailbox
// is permitted by allowed_mailboxes. Out-of-scope mailboxes fail before I/O.
func (c *Client) GetForMailbox(ctx context.Context, mailbox, suffix string) ([]byte, error) {
	path, err := c.mailboxPath(mailbox, suffix)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, path, nil)
}

// PostForMailbox performs POST /users/{mailbox}/{suffix} with a JSON payload,
// after the same allowlist check. Out-of-scope mailboxes fail before I/O.
func (c *Client) PostForMailbox(ctx context.Context, mailbox, suffix string, payload []byte) ([]byte, error) {
	path, err := c.mailboxPath(mailbox, suffix)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, path, payload)
}

// PatchForMailbox performs PATCH /users/{mailbox}/{suffix} with a JSON payload,
// after the same allowlist check. Used e.g. to set a draft reply's body.
func (c *Client) PatchForMailbox(ctx context.Context, mailbox, suffix string, payload []byte) ([]byte, error) {
	path, err := c.mailboxPath(mailbox, suffix)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPatch, path, payload)
}

// DeleteForMailbox performs DELETE /users/{mailbox}/{suffix} after the same
// allowlist check. Out-of-scope mailboxes fail before I/O.
func (c *Client) DeleteForMailbox(ctx context.Context, mailbox, suffix string) error {
	path, err := c.mailboxPath(mailbox, suffix)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodDelete, path, nil)
	return err
}

// PutRawForMailbox performs PUT /users/{mailbox}/{suffix} with a raw body and an
// explicit content type (e.g. uploading file bytes to a drive). Out-of-scope
// mailboxes fail before I/O.
func (c *Client) PutRawForMailbox(ctx context.Context, mailbox, suffix, contentType string, payload []byte) ([]byte, error) {
	path, err := c.mailboxPath(mailbox, suffix)
	if err != nil {
		return nil, err
	}
	return c.doRaw(ctx, http.MethodPut, path, contentType, payload)
}

// SearchSites performs GET /sites?search={query}. This is an intentionally
// UN-scoped discovery call (it does not consult allowed_sites): it only returns
// site metadata to help an operator find the site ID to add to the allowlist.
// Under Sites.Selected it returns the granted sites; broader directory search
// needs Sites.Read.All.
func (c *Client) SearchSites(ctx context.Context, query string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/sites?search="+url.QueryEscape(query), nil)
}

// GetForSite performs GET /sites/{siteID}/{suffix} after verifying siteID is
// permitted by allowed_sites. This is the SharePoint counterpart to the mailbox
// choke point; out-of-scope sites fail before I/O.
func (c *Client) GetForSite(ctx context.Context, siteID, suffix string) ([]byte, error) {
	path, err := c.sitePath(siteID, suffix)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, path, nil)
}

// MailboxDelta GETs a mailbox messages/delta resource: either an initial suffix
// (e.g. "mailFolders/inbox/messages/delta?$select=...") or an absolute
// deltaLink/nextLink returned by a previous call. It enforces the mailbox
// allowlist and, for absolute URLs, that they target the Graph host and the same
// mailbox — so a cursor can never be redirected off-tenant.
func (c *Client) MailboxDelta(ctx context.Context, mailbox, urlOrSuffix string) ([]byte, error) {
	if !c.cfg.IsMailboxAllowed(mailbox) {
		return nil, fmt.Errorf("mailbox %q is not in allowed_mailboxes (out of scope)", mailbox)
	}
	if strings.HasPrefix(urlOrSuffix, "http") {
		if !strings.HasPrefix(urlOrSuffix, "https://graph.microsoft.com/") {
			return nil, fmt.Errorf("delta url is not on the Microsoft Graph host")
		}
		if !strings.Contains(urlOrSuffix, "/users/"+url.PathEscape(mailbox)+"/") &&
			!strings.Contains(urlOrSuffix, "/users/"+mailbox+"/") {
			return nil, fmt.Errorf("delta url does not belong to mailbox %q", mailbox)
		}
		return c.do(ctx, http.MethodGet, urlOrSuffix, nil)
	}
	return c.do(ctx, http.MethodGet, "/users/"+url.PathEscape(mailbox)+"/"+urlOrSuffix, nil)
}

// mailboxPath enforces the allowlist and builds the scoped Graph path. This is
// the single choke point through which every mailbox-scoped call must pass.
func (c *Client) mailboxPath(mailbox, suffix string) (string, error) {
	if !c.cfg.IsMailboxAllowed(mailbox) {
		return "", fmt.Errorf("mailbox %q is not in allowed_mailboxes (out of scope)", mailbox)
	}
	return "/users/" + url.PathEscape(mailbox) + "/" + suffix, nil
}

// sitePath enforces allowed_sites and builds the scoped SharePoint path. Site
// IDs contain commas (host,siteGuid,webGuid); those are left intact (Graph
// expects them literally), only spaces would be escaped.
func (c *Client) sitePath(siteID, suffix string) (string, error) {
	if !c.cfg.IsSiteAllowed(siteID) {
		return "", fmt.Errorf("site %q is not in allowed_sites (out of scope)", siteID)
	}
	return "/sites/" + siteID + "/" + suffix, nil
}

// do issues a JSON request (Content-Type application/json for any body).
func (c *Client) do(ctx context.Context, method, path string, payload []byte) ([]byte, error) {
	return c.doRaw(ctx, method, path, "application/json", payload)
}

// doRaw issues an authenticated request with an explicit body content type and
// returns the body for 2xx responses, or an error carrying the status.
func (c *Client) doRaw(ctx context.Context, method, path, contentType string, payload []byte) ([]byte, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.BaseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("graph %s: %s: %s", path, resp.Status, string(respBody))
	}
	return respBody, nil
}
