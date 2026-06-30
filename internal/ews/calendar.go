package ews

import (
	"bytes"
	"context"
	"fmt"
)

// Event is a neutral calendar event. Start/End are EWS dateTime strings (UTC).
type Event struct {
	ID        string
	Subject   string
	Start     string
	End       string
	Location  string
	Organizer Address
	IsAllDay  bool
	Body      *Body
	Attendees []Address
}

// EventInput is a neutral event to create or patch. Start/End must already be
// UTC ISO 8601 (e.g. 2026-11-02T14:00:00Z). Empty fields are skipped on update.
type EventInput struct {
	Subject   string
	Body      string
	Start     string
	End       string
	Location  string
	Attendees []string
}

type xmlItemID struct {
	ID        string `xml:"Id,attr"`
	ChangeKey string `xml:"ChangeKey,attr"`
}

type xmlCalendarItem struct {
	ItemID    xmlItemID    `xml:"ItemId"`
	Subject   string       `xml:"Subject"`
	Start     string       `xml:"Start"`
	End       string       `xml:"End"`
	Location  string       `xml:"Location"`
	IsAllDay  bool         `xml:"IsAllDayEvent"`
	Organizer xmlMailboxes `xml:"Organizer"`
	Body      *xmlBody     `xml:"Body"`
	Required  []xmlMailbox `xml:"RequiredAttendees>Attendee>Mailbox"`
	Optional  []xmlMailbox `xml:"OptionalAttendees>Attendee>Mailbox"`
}

// updateItemResponse reuses the create response message shape (it also returns
// the item id), but is wrapped in UpdateItemResponseMessage.
type updateItemResponse struct {
	Messages []createResponseMessage `xml:"ResponseMessages>UpdateItemResponseMessage"`
}

// simpleItemResponse covers operations whose success message carries only a
// response code (DeleteItem).
type simpleItemResponse struct {
	Messages []struct {
		ResponseClass string `xml:"ResponseClass,attr"`
		ResponseCode  string `xml:"ResponseCode"`
		MessageText   string `xml:"MessageText"`
	} `xml:"ResponseMessages>DeleteItemResponseMessage"`
}

func (x xmlCalendarItem) toEvent() Event {
	ev := Event{
		ID: x.ItemID.ID, Subject: x.Subject, Start: x.Start, End: x.End,
		Location: x.Location, IsAllDay: x.IsAllDay, Organizer: x.Organizer.Mailbox.addr(),
		Attendees: mailboxes(append(append([]xmlMailbox{}, x.Required...), x.Optional...)),
	}
	if x.Body != nil {
		ev.Body = &Body{Type: x.Body.Type, Content: x.Body.Content}
	}
	return ev
}

const calendarFields = `<t:FieldURI FieldURI="item:Subject"/>` +
	`<t:FieldURI FieldURI="calendar:Start"/>` +
	`<t:FieldURI FieldURI="calendar:End"/>` +
	`<t:FieldURI FieldURI="calendar:Location"/>` +
	`<t:FieldURI FieldURI="calendar:Organizer"/>` +
	`<t:FieldURI FieldURI="calendar:IsAllDayEvent"/>`

// ListEvents lists calendar events. With both start and end it uses CalendarView
// (expands recurrences); otherwise it lists the recurring masters.
func (c *Client) ListEvents(ctx context.Context, mailbox, start, end string, max int) ([]Event, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return nil, err
	}
	env, err := c.post(ctx, listEventsEnvelope(mailbox, start, end, max))
	if err != nil {
		return nil, err
	}
	if env.Body.FindItemResponse == nil || len(env.Body.FindItemResponse.Messages) == 0 {
		return nil, fmt.Errorf("ews: empty FindItem response")
	}
	rm := env.Body.FindItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return nil, fmt.Errorf("ews FindItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	out := make([]Event, 0, len(rm.CalEvents))
	for _, x := range rm.CalEvents {
		out = append(out, x.toEvent())
	}
	return out, nil
}

// GetEvent fetches one event (with body and attendees).
func (c *Client) GetEvent(ctx context.Context, mailbox, id string) (Event, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return Event{}, err
	}
	env, err := c.post(ctx, getEventEnvelope(mailbox, id))
	if err != nil {
		return Event{}, err
	}
	if env.Body.GetItemResponse == nil || len(env.Body.GetItemResponse.Messages) == 0 {
		return Event{}, fmt.Errorf("ews: empty GetItem response")
	}
	rm := env.Body.GetItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return Event{}, fmt.Errorf("ews GetItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	if len(rm.CalEvents) == 0 {
		return Event{}, fmt.Errorf("ews: event %q not found", id)
	}
	return rm.CalEvents[0].toEvent(), nil
}

// CreateEvent creates an event and returns its new item id.
func (c *Client) CreateEvent(ctx context.Context, mailbox string, in EventInput) (string, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return "", err
	}
	env, err := c.post(ctx, createEventEnvelope(mailbox, in))
	if err != nil {
		return "", err
	}
	if env.Body.CreateItemResponse == nil || len(env.Body.CreateItemResponse.Messages) == 0 {
		return "", fmt.Errorf("ews: empty CreateItem response")
	}
	rm := env.Body.CreateItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return "", fmt.Errorf("ews CreateItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	if len(rm.CalEvents) == 0 {
		return "", fmt.Errorf("ews: CreateItem returned no calendar item id")
	}
	return rm.CalEvents[0].ItemID.ID, nil
}

// UpdateEvent patches the provided fields of an event. EWS UpdateItem needs the
// current ChangeKey, so it first fetches the item to read it.
func (c *Client) UpdateEvent(ctx context.Context, mailbox, id string, in EventInput) (string, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return "", err
	}
	changeKey, err := c.eventChangeKey(ctx, mailbox, id)
	if err != nil {
		return "", err
	}
	env, err := c.post(ctx, updateEventEnvelope(mailbox, id, changeKey, in))
	if err != nil {
		return "", err
	}
	if env.Body.UpdateItemResponse == nil || len(env.Body.UpdateItemResponse.Messages) == 0 {
		return "", fmt.Errorf("ews: empty UpdateItem response")
	}
	rm := env.Body.UpdateItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return "", fmt.Errorf("ews UpdateItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	if len(rm.CalEvents) > 0 {
		return rm.CalEvents[0].ItemID.ID, nil
	}
	return id, nil
}

// DeleteEvent moves an event to Deleted Items and notifies attendees.
func (c *Client) DeleteEvent(ctx context.Context, mailbox, id string) error {
	if err := c.requireAllowed(mailbox); err != nil {
		return err
	}
	env, err := c.post(ctx, deleteEventEnvelope(mailbox, id))
	if err != nil {
		return err
	}
	if env.Body.DeleteItemResponse == nil || len(env.Body.DeleteItemResponse.Messages) == 0 {
		return fmt.Errorf("ews: empty DeleteItem response")
	}
	rm := env.Body.DeleteItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return fmt.Errorf("ews DeleteItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	return nil
}

// eventChangeKey fetches the current ChangeKey for an event (UpdateItem needs it).
func (c *Client) eventChangeKey(ctx context.Context, mailbox, id string) (string, error) {
	env, err := c.post(ctx, getEventEnvelope(mailbox, id))
	if err != nil {
		return "", err
	}
	if env.Body.GetItemResponse == nil || len(env.Body.GetItemResponse.Messages) == 0 ||
		len(env.Body.GetItemResponse.Messages[0].CalEvents) == 0 {
		return "", fmt.Errorf("ews: event %q not found", id)
	}
	return env.Body.GetItemResponse.Messages[0].CalEvents[0].ItemID.ChangeKey, nil
}

// --- request builders ---

func listEventsEnvelope(mailbox, start, end string, max int) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	b.WriteString(`<m:FindItem Traversal="Shallow"><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape>` +
		`<t:AdditionalProperties>` + calendarFields + `</t:AdditionalProperties></m:ItemShape>`)
	if start != "" && end != "" {
		fmt.Fprintf(&b, `<m:CalendarView MaxEntriesReturned="%d" StartDate="%s" EndDate="%s"/>`, max, esc(start), esc(end))
	} else {
		fmt.Fprintf(&b, `<m:IndexedPageItemView MaxEntriesReturned="%d" Offset="0" BasePoint="Beginning"/>`, max)
	}
	b.WriteString(`<m:ParentFolderIds><t:DistinguishedFolderId Id="calendar"/></m:ParentFolderIds>` +
		`</m:FindItem></soap:Body></soap:Envelope>`)
	return b.Bytes()
}

func getEventEnvelope(mailbox, id string) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:GetItem><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape><t:AdditionalProperties>`+
		calendarFields+`<t:FieldURI FieldURI="item:Body"/>`+
		`<t:FieldURI FieldURI="calendar:RequiredAttendees"/>`+
		`<t:FieldURI FieldURI="calendar:OptionalAttendees"/>`+
		`</t:AdditionalProperties></m:ItemShape>`+
		`<m:ItemIds><t:ItemId Id="%s"/></m:ItemIds></m:GetItem></soap:Body></soap:Envelope>`, esc(id))
	return b.Bytes()
}

func createEventEnvelope(mailbox string, in EventInput) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	b.WriteString(`<m:CreateItem SendMeetingInvitations="SendToAllAndSaveCopy">` +
		`<m:SavedItemFolderId><t:DistinguishedFolderId Id="calendar"/></m:SavedItemFolderId>` +
		`<m:Items><t:CalendarItem>`)
	// Schema order: Subject, Body, Start, End, IsAllDayEvent, Location, RequiredAttendees.
	fmt.Fprintf(&b, `<t:Subject>%s</t:Subject>`, esc(in.Subject))
	if in.Body != "" {
		fmt.Fprintf(&b, `<t:Body BodyType="Text">%s</t:Body>`, esc(in.Body))
	}
	fmt.Fprintf(&b, `<t:Start>%s</t:Start><t:End>%s</t:End>`, esc(in.Start), esc(in.End))
	if in.Location != "" {
		fmt.Fprintf(&b, `<t:Location>%s</t:Location>`, esc(in.Location))
	}
	if len(in.Attendees) > 0 {
		b.WriteString(`<t:RequiredAttendees>`)
		for _, a := range in.Attendees {
			fmt.Fprintf(&b, `<t:Attendee><t:Mailbox><t:EmailAddress>%s</t:EmailAddress></t:Mailbox></t:Attendee>`, esc(a))
		}
		b.WriteString(`</t:RequiredAttendees>`)
	}
	b.WriteString(`</t:CalendarItem></m:Items></m:CreateItem></soap:Body></soap:Envelope>`)
	return b.Bytes()
}

func updateEventEnvelope(mailbox, id, changeKey string, in EventInput) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:UpdateItem MessageDisposition="SaveOnly" ConflictResolution="AutoResolve" `+
		`SendMeetingInvitationsOrCancellations="SendToAllAndSaveCopy"><m:ItemChanges><t:ItemChange>`+
		`<t:ItemId Id="%s" ChangeKey="%s"/><t:Updates>`, esc(id), esc(changeKey))
	setField := func(fieldURI, inner string) {
		fmt.Fprintf(&b, `<t:SetItemField><t:FieldURI FieldURI="%s"/><t:CalendarItem>%s</t:CalendarItem></t:SetItemField>`, fieldURI, inner)
	}
	if in.Subject != "" {
		setField("item:Subject", fmt.Sprintf(`<t:Subject>%s</t:Subject>`, esc(in.Subject)))
	}
	if in.Body != "" {
		setField("item:Body", fmt.Sprintf(`<t:Body BodyType="Text">%s</t:Body>`, esc(in.Body)))
	}
	if in.Start != "" {
		setField("calendar:Start", fmt.Sprintf(`<t:Start>%s</t:Start>`, esc(in.Start)))
	}
	if in.End != "" {
		setField("calendar:End", fmt.Sprintf(`<t:End>%s</t:End>`, esc(in.End)))
	}
	if in.Location != "" {
		setField("calendar:Location", fmt.Sprintf(`<t:Location>%s</t:Location>`, esc(in.Location)))
	}
	b.WriteString(`</t:Updates></t:ItemChange></m:ItemChanges></m:UpdateItem></soap:Body></soap:Envelope>`)
	return b.Bytes()
}

func deleteEventEnvelope(mailbox, id string) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:DeleteItem DeleteType="MoveToDeletedItems" SendMeetingCancellations="SendToAllAndSaveCopy">`+
		`<m:ItemIds><t:ItemId Id="%s"/></m:ItemIds></m:DeleteItem></soap:Body></soap:Envelope>`, esc(id))
	return b.Bytes()
}
