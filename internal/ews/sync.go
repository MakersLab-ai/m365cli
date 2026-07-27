package ews

import (
	"bytes"
	"context"
	"fmt"
)

// SyncPage is one page of an incremental folder sync.
type SyncPage struct {
	Changed   []Item   // created + updated items
	Removed   []string // deleted item ids
	SyncState string   // opaque cursor to pass to the next SyncInbox call
	More      bool     // true if more pages remain (call again with SyncState)
}

type syncResponse struct {
	Messages []syncResponseMessage `xml:"ResponseMessages>SyncFolderItemsResponseMessage"`
}

type syncResponseMessage struct {
	ResponseClass string       `xml:"ResponseClass,attr"`
	ResponseCode  string       `xml:"ResponseCode"`
	MessageText   string       `xml:"MessageText"`
	SyncState     string       `xml:"SyncState"`
	IncludesLast  bool         `xml:"IncludesLastItemInRange"`
	Created       []xmlMessage `xml:"Changes>Create>Message"`
	Updated       []xmlMessage `xml:"Changes>Update>Message"`
	Deleted       []xmlIDAttr  `xml:"Changes>Delete>ItemId"`
}

// SyncInbox runs one SyncFolderItems page against the mailbox Inbox. Pass an
// empty syncState on the first call; persist the returned SyncState and pass it
// back on subsequent calls. The SyncState is opaque — store it verbatim.
func (c *Client) SyncInbox(ctx context.Context, mailbox, syncState string, max int) (SyncPage, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return SyncPage{}, err
	}
	env, err := c.post(ctx, syncEnvelope(mailbox, syncState, max))
	if err != nil {
		return SyncPage{}, err
	}
	if env.Body.SyncResponse == nil || len(env.Body.SyncResponse.Messages) == 0 {
		return SyncPage{}, fmt.Errorf("ews: empty SyncFolderItems response")
	}
	rm := env.Body.SyncResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return SyncPage{}, fmt.Errorf("ews SyncFolderItems %s: %s", rm.ResponseCode, rm.MessageText)
	}
	page := SyncPage{SyncState: rm.SyncState, More: !rm.IncludesLast}
	for _, x := range append(append([]xmlMessage{}, rm.Created...), rm.Updated...) {
		page.Changed = append(page.Changed, x.toItem())
	}
	for _, d := range rm.Deleted {
		page.Removed = append(page.Removed, d.ID)
	}
	return page, nil
}

func syncEnvelope(mailbox, syncState string, max int) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	// ItemShape order: BaseShape, BodyType, AdditionalProperties.
	b.WriteString(`<m:SyncFolderItems><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape>` +
		`<t:BodyType>Text</t:BodyType><t:AdditionalProperties>` +
		`<t:FieldURI FieldURI="item:Subject"/>` +
		`<t:FieldURI FieldURI="message:From"/>` +
		`<t:FieldURI FieldURI="message:ToRecipients"/>` +
		`<t:FieldURI FieldURI="item:DateTimeReceived"/>` +
		`<t:FieldURI FieldURI="message:IsRead"/>` +
		`<t:FieldURI FieldURI="message:ConversationId"/>` +
		`<t:FieldURI FieldURI="item:Body"/>` +
		`</t:AdditionalProperties></m:ItemShape>` +
		`<m:SyncFolderId><t:DistinguishedFolderId Id="inbox"/></m:SyncFolderId>`)
	// SyncState (if any) goes between SyncFolderId and MaxChangesReturned.
	if syncState != "" {
		fmt.Fprintf(&b, `<m:SyncState>%s</m:SyncState>`, esc(syncState))
	}
	fmt.Fprintf(&b, `<m:MaxChangesReturned>%d</m:MaxChangesReturned><m:SyncScope>NormalItems</m:SyncScope>`, max)
	b.WriteString(`</m:SyncFolderItems></soap:Body></soap:Envelope>`)
	return b.Bytes()
}
