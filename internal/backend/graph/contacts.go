package graphbackend

import (
	"context"
	"fmt"
	"net/url"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/contacts"
	"github.com/MakersLab-ai/m365cli/internal/graph"
)

type contactSvc struct{ c *graph.Client }

const contactSelect = "id,displayName,givenName,surname,emailAddresses,mobilePhone,businessPhones"

func (s contactSvc) List(ctx context.Context, mailbox string, opts backend.ListOpts) ([]byte, error) {
	suffix := fmt.Sprintf("contacts?$top=%d&$select=%s", opts.Max, contactSelect)
	return unwrapValue(s.c.GetForMailbox(ctx, mailbox, suffix))
}

func (s contactSvc) Get(ctx context.Context, mailbox, id string) ([]byte, error) {
	return s.c.GetForMailbox(ctx, mailbox, "contacts/"+url.PathEscape(id)+"?$select="+contactSelect)
}

func (s contactSvc) Add(ctx context.Context, mailbox string, c contacts.Contact) ([]byte, error) {
	payload, err := contacts.BuildContact(c)
	if err != nil {
		return nil, err
	}
	return s.c.PostForMailbox(ctx, mailbox, "contacts", payload)
}
