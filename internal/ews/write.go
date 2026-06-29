package ews

import (
	"bytes"
	"context"
	"fmt"
)

// OutMessage is a neutral outgoing message (the backend maps mail.Message onto
// it). Bodies are sent as plain text, matching the Graph backend.
type OutMessage struct {
	Subject string
	Body    string
	To      []string
	Cc      []string
}

// --- CreateItem (send / draft) ---

type createItemResponse struct {
	Messages []createResponseMessage `xml:"ResponseMessages>CreateItemResponseMessage"`
}

type createResponseMessage struct {
	ResponseClass string       `xml:"ResponseClass,attr"`
	ResponseCode  string       `xml:"ResponseCode"`
	MessageText   string       `xml:"MessageText"`
	Items         []xmlMessage `xml:"Items>Message"`
}

// SendMessage creates and sends a message as the (impersonated) mailbox,
// saving a copy to Sent Items. SendAndSaveCopy returns no item id by design.
func (c *Client) SendMessage(ctx context.Context, mailbox string, m OutMessage) error {
	if err := c.requireAllowed(mailbox); err != nil {
		return err
	}
	env, err := c.post(ctx, createItemEnvelope(mailbox, m, "SendAndSaveCopy", "sentitems"))
	if err != nil {
		return err
	}
	_, err = createResult(env)
	return err
}

// CreateDraft saves an unsent draft in the mailbox's Drafts folder and returns
// its EWS item id (SaveOnly returns the id; the send dispositions do not).
func (c *Client) CreateDraft(ctx context.Context, mailbox string, m OutMessage) (string, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return "", err
	}
	env, err := c.post(ctx, createItemEnvelope(mailbox, m, "SaveOnly", "drafts"))
	if err != nil {
		return "", err
	}
	rm, err := createResult(env)
	if err != nil {
		return "", err
	}
	if len(rm.Items) == 0 {
		return "", fmt.Errorf("ews: CreateItem (SaveOnly) returned no item id")
	}
	return rm.Items[0].ItemID.ID, nil
}

func createResult(env *envelope) (createResponseMessage, error) {
	if env.Body.CreateItemResponse == nil || len(env.Body.CreateItemResponse.Messages) == 0 {
		return createResponseMessage{}, fmt.Errorf("ews: empty CreateItem response")
	}
	rm := env.Body.CreateItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return createResponseMessage{}, fmt.Errorf("ews CreateItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	return rm, nil
}

// createItemEnvelope builds a CreateItem request. The Message child order is
// fixed by the EWS schema (Subject, Body, then recipients) — out-of-order
// children are rejected with ErrorSchemaValidation.
func createItemEnvelope(mailbox string, m OutMessage, disposition, savedFolder string) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:CreateItem MessageDisposition="%s">`+
		`<m:SavedItemFolderId><t:DistinguishedFolderId Id="%s"/></m:SavedItemFolderId>`+
		`<m:Items><t:Message>`+
		`<t:Subject>%s</t:Subject>`+
		`<t:Body BodyType="Text">%s</t:Body>`,
		disposition, savedFolder, esc(m.Subject), esc(m.Body))
	writeRecipients(&b, "ToRecipients", m.To)
	writeRecipients(&b, "CcRecipients", m.Cc)
	b.WriteString(`</t:Message></m:Items></m:CreateItem></soap:Body></soap:Envelope>`)
	return b.Bytes()
}

func writeRecipients(b *bytes.Buffer, tag string, addrs []string) {
	if len(addrs) == 0 {
		return
	}
	fmt.Fprintf(b, "<t:%s>", tag)
	for _, a := range addrs {
		fmt.Fprintf(b, "<t:Mailbox><t:EmailAddress>%s</t:EmailAddress></t:Mailbox>", esc(a))
	}
	fmt.Fprintf(b, "</t:%s>", tag)
}

// --- FindItem with a free-text query (AQS) ---

// Search runs a free-text inbox search (EWS QueryString / AQS). QueryString is
// mutually exclusive with EWS Restrictions but coexists with SortOrder, and per
// the schema must come last in FindItem (after ParentFolderIds).
func (c *Client) Search(ctx context.Context, mailbox, query string, max int) ([]Item, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return nil, err
	}
	env, err := c.post(ctx, findSearchEnvelope(mailbox, query, max))
	if err != nil {
		return nil, err
	}
	return itemsFromFind(env)
}

func findSearchEnvelope(mailbox, query string, max int) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:FindItem Traversal="Shallow">`+
		`<m:ItemShape><t:BaseShape>IdOnly</t:BaseShape><t:AdditionalProperties>`+
		`<t:FieldURI FieldURI="item:Subject"/>`+
		`<t:FieldURI FieldURI="message:From"/>`+
		`<t:FieldURI FieldURI="item:DateTimeReceived"/>`+
		`<t:FieldURI FieldURI="message:IsRead"/>`+
		`</t:AdditionalProperties></m:ItemShape>`+
		`<m:IndexedPageItemView MaxEntriesReturned="%d" Offset="0" BasePoint="Beginning"/>`+
		`<m:SortOrder><t:FieldOrder Order="Descending"><t:FieldURI FieldURI="item:DateTimeReceived"/></t:FieldOrder></m:SortOrder>`+
		`<m:ParentFolderIds><t:DistinguishedFolderId Id="inbox"/></m:ParentFolderIds>`+
		`<m:QueryString>%s</m:QueryString>`+
		`</m:FindItem></soap:Body></soap:Envelope>`, max, esc(query))
	return b.Bytes()
}
