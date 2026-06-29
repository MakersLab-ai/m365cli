package ews

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

// --- reply ---

func TestReplyBuildsReplyToItem(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, createSendSuccess)
	})
	if err := c.Reply(context.Background(), mbx, "REF-1", "thanks", false); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	want := `<m:Items><t:ReplyToItem><t:ReferenceItemId Id="REF-1"/><t:NewBodyContent BodyType="Text">thanks</t:NewBodyContent></t:ReplyToItem></m:Items>`
	if !strings.Contains(*body, want) {
		t.Errorf("reply body wrong\ngot:  %s\nwant substring: %s", *body, want)
	}
	if !strings.Contains(*body, `MessageDisposition="SendAndSaveCopy"`) {
		t.Errorf("reply should send+save: %s", *body)
	}
}

func TestReplyAllUsesReplyAllToItem(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, createSendSuccess) })
	if err := c.Reply(context.Background(), mbx, "REF-2", "ok", true); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(*body, "<t:ReplyAllToItem>") {
		t.Errorf("reply-all should use ReplyAllToItem: %s", *body)
	}
}

func TestCreateReplyDraftReturnsID(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, createDraftSuccess) })
	id, err := c.CreateReplyDraft(context.Background(), mbx, "REF-3", "draft", false)
	if err != nil {
		t.Fatalf("CreateReplyDraft: %v", err)
	}
	if !strings.Contains(*body, `MessageDisposition="SaveOnly"`) {
		t.Errorf("reply draft should SaveOnly: %s", *body)
	}
	if id != "DRAFT-1" {
		t.Errorf("draft id = %q", id)
	}
}

// --- attachments ---

const getItemAttachmentsSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:GetItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:GetItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode><m:Items>
   <t:Message><t:ItemId Id="MID" ChangeKey="CQ=="/><t:Attachments>
    <t:FileAttachment><t:AttachmentId Id="ATT-1"/><t:Name>invoice.pdf</t:Name>
     <t:ContentType>application/pdf</t:ContentType><t:Size>48213</t:Size></t:FileAttachment>
   </t:Attachments></t:Message></m:Items></m:GetItemResponseMessage></m:ResponseMessages></m:GetItemResponse></soap:Body></soap:Envelope>`

func TestListAttachments(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, getItemAttachmentsSuccess) })
	atts, err := c.ListAttachments(context.Background(), mbx, "MID")
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if !strings.Contains(*body, `<t:FieldURI FieldURI="item:Attachments"/>`) {
		t.Errorf("request should ask for item:Attachments: %s", *body)
	}
	if len(atts) != 1 || atts[0].ID != "ATT-1" || atts[0].Name != "invoice.pdf" ||
		atts[0].ContentType != "application/pdf" || atts[0].Size != 48213 {
		t.Errorf("attachment parse wrong: %+v", atts)
	}
}

func TestGetAttachmentDecodesBase64(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("PDFBYTES"))
	resp := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:GetAttachmentResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:GetAttachmentResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode><m:Attachments>
   <t:FileAttachment><t:AttachmentId Id="ATT-1"/><t:Name>invoice.pdf</t:Name>
    <t:ContentType>application/pdf</t:ContentType><t:Content>` + payload + `</t:Content></t:FileAttachment>
  </m:Attachments></m:GetAttachmentResponseMessage></m:ResponseMessages></m:GetAttachmentResponse></soap:Body></soap:Envelope>`
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, resp) })
	ac, err := c.GetAttachment(context.Background(), mbx, "ATT-1")
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if !strings.Contains(*body, `<m:AttachmentShape/><m:AttachmentIds><t:AttachmentId Id="ATT-1"/>`) {
		t.Errorf("request order wrong (shape before ids): %s", *body)
	}
	if ac.Name != "invoice.pdf" || string(ac.Bytes) != "PDFBYTES" {
		t.Errorf("attachment content wrong: %+v bytes=%q", ac, ac.Bytes)
	}
}

// --- calendar ---

const calListSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:FindItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:FindItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:RootFolder TotalItemsInView="1" IncludesLastItemInRange="true"><t:Items>
    <t:CalendarItem><t:ItemId Id="EV-1" ChangeKey="DW=="/><t:Subject>Planning</t:Subject>
     <t:Start>2026-06-30T14:00:00Z</t:Start><t:End>2026-06-30T15:00:00Z</t:End>
     <t:Location>Room 7</t:Location><t:IsAllDayEvent>false</t:IsAllDayEvent>
     <t:Organizer><t:Mailbox><t:Name>Jane</t:Name></t:Mailbox></t:Organizer></t:CalendarItem>
   </t:Items></m:RootFolder></m:FindItemResponseMessage></m:ResponseMessages></m:FindItemResponse></soap:Body></soap:Envelope>`

func TestListEventsCalendarView(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, calListSuccess) })
	evs, err := c.ListEvents(context.Background(), mbx, "2026-06-29T00:00:00Z", "2026-07-06T00:00:00Z", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if !strings.Contains(*body, `<m:CalendarView MaxEntriesReturned="100" StartDate="2026-06-29T00:00:00Z" EndDate="2026-07-06T00:00:00Z"/>`) {
		t.Errorf("CalendarView wrong: %s", *body)
	}
	if !strings.Contains(*body, `<t:DistinguishedFolderId Id="calendar"/>`) {
		t.Errorf("should target calendar folder: %s", *body)
	}
	if len(evs) != 1 || evs[0].ID != "EV-1" || evs[0].Subject != "Planning" ||
		evs[0].Start != "2026-06-30T14:00:00Z" || evs[0].Location != "Room 7" {
		t.Errorf("event parse wrong: %+v", evs)
	}
}

func TestCreateEventOrderAndID(t *testing.T) {
	resp := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:CreateItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:CreateItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:Items><t:CalendarItem><t:ItemId Id="EV-NEW" ChangeKey="DW=="/></t:CalendarItem></m:Items>
  </m:CreateItemResponseMessage></m:ResponseMessages></m:CreateItemResponse></soap:Body></soap:Envelope>`
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, resp) })
	id, err := c.CreateEvent(context.Background(), mbx, EventInput{
		Subject: "Sync", Body: "agenda", Start: "2026-11-02T14:00:00Z", End: "2026-11-02T15:00:00Z",
		Location: "Room 7", Attendees: []string{"a@x.com"},
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if id != "EV-NEW" {
		t.Errorf("id = %q", id)
	}
	// Schema order: Subject, Body, Start, End, Location, RequiredAttendees.
	want := `<t:CalendarItem><t:Subject>Sync</t:Subject><t:Body BodyType="Text">agenda</t:Body>` +
		`<t:Start>2026-11-02T14:00:00Z</t:Start><t:End>2026-11-02T15:00:00Z</t:End>` +
		`<t:Location>Room 7</t:Location><t:RequiredAttendees><t:Attendee><t:Mailbox><t:EmailAddress>a@x.com</t:EmailAddress></t:Mailbox></t:Attendee></t:RequiredAttendees></t:CalendarItem>`
	if !strings.Contains(*body, want) {
		t.Errorf("create order wrong\ngot:  %s\nwant: %s", *body, want)
	}
	if !strings.Contains(*body, `SendMeetingInvitations="SendToAllAndSaveCopy"`) {
		t.Errorf("missing SendMeetingInvitations: %s", *body)
	}
}

func TestUpdateEventFetchesChangeKeyThenSets(t *testing.T) {
	getResp := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:GetItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:GetItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:Items><t:CalendarItem><t:ItemId Id="EV-1" ChangeKey="CK-OLD"/></t:CalendarItem></m:Items>
  </m:GetItemResponseMessage></m:ResponseMessages></m:GetItemResponse></soap:Body></soap:Envelope>`
	updResp := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:UpdateItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:UpdateItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:Items><t:CalendarItem><t:ItemId Id="EV-1" ChangeKey="CK-NEW"/></t:CalendarItem></m:Items>
  </m:UpdateItemResponseMessage></m:ResponseMessages></m:UpdateItemResponse></soap:Body></soap:Envelope>`
	calls := 0
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 { // first POST is the GetItem that fetches the ChangeKey
			_, _ = io.WriteString(w, getResp)
			return
		}
		_, _ = io.WriteString(w, updResp)
	})
	newID, err := c.UpdateEvent(context.Background(), mbx, "EV-1", EventInput{Subject: "Renamed"})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if newID != "EV-1" {
		t.Errorf("newID = %q", newID)
	}
	if !strings.Contains(*body, `<t:ItemId Id="EV-1" ChangeKey="CK-OLD"/>`) {
		t.Errorf("update should carry fetched ChangeKey: %s", *body)
	}
	want := `<t:SetItemField><t:FieldURI FieldURI="item:Subject"/><t:CalendarItem><t:Subject>Renamed</t:Subject></t:CalendarItem></t:SetItemField>`
	if !strings.Contains(*body, want) {
		t.Errorf("update SetItemField wrong: %s", *body)
	}
}

func TestDeleteEvent(t *testing.T) {
	resp := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:DeleteItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"><m:ResponseMessages>
  <m:DeleteItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
  </m:DeleteItemResponseMessage></m:ResponseMessages></m:DeleteItemResponse></soap:Body></soap:Envelope>`
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, resp) })
	if err := c.DeleteEvent(context.Background(), mbx, "EV-1"); err != nil {
		t.Fatalf("DeleteEvent: %v", err)
	}
	if !strings.Contains(*body, `DeleteType="MoveToDeletedItems"`) || !strings.Contains(*body, `<t:ItemId Id="EV-1"/>`) {
		t.Errorf("delete request wrong: %s", *body)
	}
}

// --- sync (watch) ---

const syncSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:SyncFolderItemsResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:SyncFolderItemsResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:SyncState>STATE-2</m:SyncState><m:IncludesLastItemInRange>true</m:IncludesLastItemInRange>
   <m:Changes>
    <t:Create><t:Message><t:ItemId Id="N-1" ChangeKey="CQ=="/><t:Subject>Budget</t:Subject>
     <t:DateTimeReceived>2026-06-29T09:14:22Z</t:DateTimeReceived>
     <t:ConversationId Id="CONV-1"/>
     <t:From><t:Mailbox><t:Name>Dan</t:Name><t:EmailAddress>dan@x.com</t:EmailAddress></t:Mailbox></t:From>
     <t:IsRead>false</t:IsRead></t:Message></t:Create>
    <t:Delete><t:ItemId Id="OLD-9" ChangeKey="CK"/></t:Delete>
   </m:Changes></m:SyncFolderItemsResponseMessage></m:ResponseMessages></m:SyncFolderItemsResponse></soap:Body></soap:Envelope>`

func TestSyncInboxFirstRunNoSyncState(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, syncSuccess) })
	page, err := c.SyncInbox(context.Background(), mbx, "", 512)
	if err != nil {
		t.Fatalf("SyncInbox: %v", err)
	}
	if strings.Contains(*body, "<m:SyncState>") {
		t.Errorf("first run must omit SyncState: %s", *body)
	}
	if !strings.Contains(*body, `<m:MaxChangesReturned>512</m:MaxChangesReturned>`) {
		t.Errorf("MaxChangesReturned required: %s", *body)
	}
	if page.SyncState != "STATE-2" || page.More {
		t.Errorf("page state wrong: %+v", page)
	}
	if len(page.Changed) != 1 || page.Changed[0].ID != "N-1" || page.Changed[0].ConversationID != "CONV-1" ||
		page.Changed[0].From.Address != "dan@x.com" {
		t.Errorf("changed parse wrong: %+v", page.Changed)
	}
	if len(page.Removed) != 1 || page.Removed[0] != "OLD-9" {
		t.Errorf("removed parse wrong: %+v", page.Removed)
	}
}

func TestSyncInboxIncrementalSendsSyncState(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, syncSuccess) })
	if _, err := c.SyncInbox(context.Background(), mbx, "STATE-1", 512); err != nil {
		t.Fatalf("SyncInbox: %v", err)
	}
	if !strings.Contains(*body, `<m:SyncFolderId><t:DistinguishedFolderId Id="inbox"/></m:SyncFolderId><m:SyncState>STATE-1</m:SyncState>`) {
		t.Errorf("incremental must send SyncState after SyncFolderId: %s", *body)
	}
}
