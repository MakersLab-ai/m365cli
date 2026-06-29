// Package graphbackend implements the backend interfaces against Microsoft
// Graph by composing the thin *graph.Client. It owns the Graph-specific
// knowledge that used to live in the CLI command layer: the REST suffixes,
// the $select projections, and the {value} unwrap. The allowlist choke point
// stays in internal/graph and is reused unchanged — this package only builds
// paths and shapes responses.
package graphbackend

import (
	"encoding/json"
	"fmt"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/graph"
)

// Compile-time assertion that *Backend satisfies the backend.Backend contract.
var _ backend.Backend = (*Backend)(nil)

// Backend adapts a *graph.Client to the backend.Backend interface.
type Backend struct {
	c *graph.Client
}

// New wraps a configured Graph client as a backend.Backend.
func New(c *graph.Client) *Backend { return &Backend{c: c} }

func (b *Backend) Mail() backend.MailService         { return mailSvc{c: b.c} }
func (b *Backend) Calendar() backend.CalendarService { return calSvc{c: b.c} }
func (b *Backend) Contacts() backend.ContactService  { return contactSvc{c: b.c} }
func (b *Backend) Drive() backend.DriveService       { return driveSvc{c: b.c} }
func (b *Backend) Sites() backend.SiteService        { return siteSvc{c: b.c} }

// unwrapValue extracts the `value` array from a Graph collection response,
// matching the CLI's former emitGraphValue/postAndEmitValue/emitSiteValue.
func unwrapValue(body []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	var page struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse Graph response: %w", err)
	}
	return page.Value, nil
}
