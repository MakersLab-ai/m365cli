package ews

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
)

// Address is a neutral mail participant.
type Address struct {
	Name    string
	Address string
}

// Body is a neutral message body.
type Body struct {
	Type    string // "Text" or "HTML" as returned by EWS
	Content string
}

// Item is a neutral message, independent of EWS XML or Graph JSON. The backend
// maps it to the JSON output contract.
type Item struct {
	ID       string
	Subject  string
	From     Address
	To       []Address
	Cc       []Address
	Received string
	IsRead   bool
	Body     *Body // nil unless fetched (GetItem)
}

// --- response parse structs (matched by local name; EWS namespaces ignored) ---

type envelope struct {
	XMLName xml.Name
	Body    struct {
		Fault              *soapFault          `xml:"Fault"`
		FindItemResponse   *findItemResponse   `xml:"FindItemResponse"`
		GetItemResponse    *getItemResponse    `xml:"GetItemResponse"`
		CreateItemResponse *createItemResponse `xml:"CreateItemResponse"`
	} `xml:"Body"`
}

type soapFault struct {
	FaultCode   string `xml:"faultcode"`
	FaultString string `xml:"faultstring"`
	Detail      struct {
		ResponseCode string `xml:"ResponseCode"`
		Message      string `xml:"Message"`
	} `xml:"detail"`
}

type findItemResponse struct {
	Messages []findResponseMessage `xml:"ResponseMessages>FindItemResponseMessage"`
}

type findResponseMessage struct {
	ResponseClass string       `xml:"ResponseClass,attr"`
	ResponseCode  string       `xml:"ResponseCode"`
	MessageText   string       `xml:"MessageText"`
	Items         []xmlMessage `xml:"RootFolder>Items>Message"`
}

type getItemResponse struct {
	Messages []getResponseMessage `xml:"ResponseMessages>GetItemResponseMessage"`
}

type getResponseMessage struct {
	ResponseClass string       `xml:"ResponseClass,attr"`
	ResponseCode  string       `xml:"ResponseCode"`
	MessageText   string       `xml:"MessageText"`
	Items         []xmlMessage `xml:"Items>Message"`
}

type xmlMessage struct {
	ItemID struct {
		ID        string `xml:"Id,attr"`
		ChangeKey string `xml:"ChangeKey,attr"`
	} `xml:"ItemId"`
	Subject          string       `xml:"Subject"`
	DateTimeReceived string       `xml:"DateTimeReceived"`
	From             xmlMailboxes `xml:"From"`
	ToRecipients     []xmlMailbox `xml:"ToRecipients>Mailbox"`
	CcRecipients     []xmlMailbox `xml:"CcRecipients>Mailbox"`
	IsRead           bool         `xml:"IsRead"`
	Body             *xmlBody     `xml:"Body"`
}

type xmlMailboxes struct {
	Mailbox xmlMailbox `xml:"Mailbox"`
}

type xmlMailbox struct {
	Name         string `xml:"Name"`
	EmailAddress string `xml:"EmailAddress"`
}

type xmlBody struct {
	Type    string `xml:"BodyType,attr"`
	Content string `xml:",chardata"`
}

func (m xmlMailbox) addr() Address { return Address{Name: m.Name, Address: m.EmailAddress} }

func mailboxes(in []xmlMailbox) []Address {
	if len(in) == 0 {
		return nil
	}
	out := make([]Address, 0, len(in))
	for _, m := range in {
		out = append(out, m.addr())
	}
	return out
}

func (m xmlMessage) toItem() Item {
	it := Item{
		ID:       m.ItemID.ID,
		Subject:  m.Subject,
		From:     m.From.Mailbox.addr(),
		To:       mailboxes(m.ToRecipients),
		Cc:       mailboxes(m.CcRecipients),
		Received: m.DateTimeReceived,
		IsRead:   m.IsRead,
	}
	if m.Body != nil {
		it.Body = &Body{Type: m.Body.Type, Content: m.Body.Content}
	}
	return it
}

// --- operations ---

// FindInbox returns up to max most-recent messages in the target mailbox's
// Inbox (newest first). FindItem returns only the sender's display name, not
// the SMTP address — fetch a single message with GetMessage for the address.
func (c *Client) FindInbox(ctx context.Context, mailbox string, max int) ([]Item, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return nil, err
	}
	env, err := c.post(ctx, findInboxEnvelope(mailbox, max))
	if err != nil {
		return nil, err
	}
	return itemsFromFind(env)
}

// itemsFromFind validates a FindItem response and maps its messages to Items.
// Shared by FindInbox and Search (both use FindItem with the same ItemShape).
func itemsFromFind(env *envelope) ([]Item, error) {
	if env.Body.FindItemResponse == nil || len(env.Body.FindItemResponse.Messages) == 0 {
		return nil, fmt.Errorf("ews: empty FindItem response")
	}
	rm := env.Body.FindItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return nil, fmt.Errorf("ews FindItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	items := make([]Item, 0, len(rm.Items))
	for _, x := range rm.Items {
		items = append(items, x.toItem())
	}
	return items, nil
}

// GetMessage fetches one message (with body and recipients) by its EWS item id.
func (c *Client) GetMessage(ctx context.Context, mailbox, id string) (Item, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return Item{}, err
	}
	env, err := c.post(ctx, getItemEnvelope(mailbox, id))
	if err != nil {
		return Item{}, err
	}
	if env.Body.GetItemResponse == nil || len(env.Body.GetItemResponse.Messages) == 0 {
		return Item{}, fmt.Errorf("ews: empty GetItem response")
	}
	rm := env.Body.GetItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return Item{}, fmt.Errorf("ews GetItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	if len(rm.Items) == 0 {
		return Item{}, fmt.Errorf("ews: message %q not found", id)
	}
	return rm.Items[0].toItem(), nil
}

// --- request builders ---

const (
	nsSOAP = "http://schemas.xmlsoap.org/soap/envelope/"
	nsT    = "http://schemas.microsoft.com/exchange/services/2006/types"
	nsM    = "http://schemas.microsoft.com/exchange/services/2006/messages"
)

// envelopeHead opens a SOAP envelope with the standard namespaces and an
// ExchangeImpersonation header targeting the given mailbox.
func envelopeHead(mailbox string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<soap:Envelope xmlns:soap="%s" xmlns:t="%s" xmlns:m="%s">`+
		`<soap:Header>`+
		`<t:RequestServerVersion Version="Exchange2010_SP2"/>`+
		`<t:ExchangeImpersonation><t:ConnectingSID><t:PrimarySmtpAddress>%s</t:PrimarySmtpAddress></t:ConnectingSID></t:ExchangeImpersonation>`+
		`</soap:Header><soap:Body>`,
		nsSOAP, nsT, nsM, esc(mailbox))
}

func findInboxEnvelope(mailbox string, max int) []byte {
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
		`</m:FindItem></soap:Body></soap:Envelope>`, max)
	return b.Bytes()
}

func getItemEnvelope(mailbox, id string) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:GetItem>`+
		`<m:ItemShape><t:BaseShape>IdOnly</t:BaseShape><t:BodyType>Text</t:BodyType><t:AdditionalProperties>`+
		`<t:FieldURI FieldURI="item:Subject"/>`+
		`<t:FieldURI FieldURI="message:From"/>`+
		`<t:FieldURI FieldURI="message:ToRecipients"/>`+
		`<t:FieldURI FieldURI="message:CcRecipients"/>`+
		`<t:FieldURI FieldURI="item:DateTimeReceived"/>`+
		`<t:FieldURI FieldURI="message:IsRead"/>`+
		`<t:FieldURI FieldURI="item:Body"/>`+
		`</t:AdditionalProperties></m:ItemShape>`+
		`<m:ItemIds><t:ItemId Id="%s"/></m:ItemIds>`+
		`</m:GetItem></soap:Body></soap:Envelope>`, esc(id))
	return b.Bytes()
}

// esc XML-escapes a value destined for element text or an attribute.
func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
