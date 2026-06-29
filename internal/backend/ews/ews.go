// Package ewsbackend implements the backend interfaces against an on-premise
// Exchange server via EWS (internal/ews). It maps EWS Items to the same
// Graph-shaped JSON the graph backend emits, so CLI consumers see one contract
// regardless of transport.
//
// This first slice implements mail List and Read. Every other operation returns
// backend.ErrUnsupported until the corresponding EWS capability is built.
package ewsbackend

import (
	"encoding/json"
	"strings"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/ews"
)

// Compile-time assertion that *Backend satisfies the backend.Backend contract.
var _ backend.Backend = (*Backend)(nil)

// Backend adapts an *ews.Client to the backend.Backend interface.
type Backend struct {
	c *ews.Client
}

// New wraps a configured EWS client as a backend.Backend.
func New(c *ews.Client) *Backend { return &Backend{c: c} }

func (b *Backend) Mail() backend.MailService         { return mailSvc{c: b.c} }
func (b *Backend) Calendar() backend.CalendarService { return calSvc{} }
func (b *Backend) Contacts() backend.ContactService  { return contactSvc{} }
func (b *Backend) Drive() backend.DriveService        { return driveSvc{} }
func (b *Backend) Sites() backend.SiteService         { return siteSvc{} }

// --- mapping to the Graph-shaped JSON contract ---

type jsonAddr struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type jsonEmail struct {
	EmailAddress jsonAddr `json:"emailAddress"`
}

type jsonBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// jsonMessageSummary mirrors the Graph message projection used by `mail list`
// ($select=id,subject,from,receivedDateTime,isRead). Note: EWS FindItem does
// not return the sender's SMTP address, so from.emailAddress.address is empty
// for list results — fetch a single message (Read) for the full address.
type jsonMessageSummary struct {
	ID               string    `json:"id"`
	Subject          string    `json:"subject"`
	From             jsonEmail `json:"from"`
	ReceivedDateTime string    `json:"receivedDateTime"`
	IsRead           bool      `json:"isRead"`
}

// jsonMessageFull mirrors the Graph projection used by `mail read`.
type jsonMessageFull struct {
	ID               string      `json:"id"`
	Subject          string      `json:"subject"`
	From             jsonEmail   `json:"from"`
	ToRecipients     []jsonEmail `json:"toRecipients"`
	CcRecipients     []jsonEmail `json:"ccRecipients"`
	ReceivedDateTime string      `json:"receivedDateTime"`
	IsRead           bool        `json:"isRead"`
	Body             jsonBody    `json:"body"`
}

func email(a ews.Address) jsonEmail {
	return jsonEmail{EmailAddress: jsonAddr{Name: a.Name, Address: a.Address}}
}

func emails(in []ews.Address) []jsonEmail {
	out := make([]jsonEmail, 0, len(in))
	for _, a := range in {
		out = append(out, email(a))
	}
	return out
}

func summariesJSON(items []ews.Item) ([]byte, error) {
	out := make([]jsonMessageSummary, 0, len(items))
	for _, it := range items {
		out = append(out, jsonMessageSummary{
			ID:               it.ID,
			Subject:          it.Subject,
			From:             email(it.From),
			ReceivedDateTime: it.Received,
			IsRead:           it.IsRead,
		})
	}
	return json.Marshal(out)
}

func messageJSON(it ews.Item) ([]byte, error) {
	full := jsonMessageFull{
		ID:               it.ID,
		Subject:          it.Subject,
		From:             email(it.From),
		ToRecipients:     emails(it.To),
		CcRecipients:     emails(it.Cc),
		ReceivedDateTime: it.Received,
		IsRead:           it.IsRead,
	}
	if it.Body != nil {
		// Graph reports contentType lowercase ("text"/"html"); EWS uses Title case.
		full.Body = jsonBody{ContentType: strings.ToLower(it.Body.Type), Content: it.Body.Content}
	}
	return json.Marshal(full)
}
