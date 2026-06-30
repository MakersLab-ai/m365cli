package ewsbackend_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	ewsbackend "github.com/MakersLab-ai/m365cli/internal/backend/ews"
	"github.com/MakersLab-ai/m365cli/internal/config"
	"github.com/MakersLab-ai/m365cli/internal/ews"
	"github.com/MakersLab-ai/m365cli/internal/mail"
)

const mbx = "agent@example.com"

func newBackend(t *testing.T, resp string) backend.Backend {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{EWSURL: srv.URL, EWSUser: "DOM\\svc", AllowedMailboxes: []string{mbx}}
	c := ews.New(cfg, "secret")
	c.SetHTTPClient(srv.Client())
	c.URL = srv.URL
	return ewsbackend.New(c)
}

const findItemSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body><m:FindItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
  <m:ResponseMessages><m:FindItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:RootFolder TotalItemsInView="1" IncludesLastItemInRange="true"><t:Items>
    <t:Message><t:ItemId Id="AAA=" ChangeKey="CQ=="/><t:Subject>Quarterly report</t:Subject>
     <t:DateTimeReceived>2026-06-28T14:03:22Z</t:DateTimeReceived>
     <t:From><t:Mailbox><t:Name>Jane Doe</t:Name></t:Mailbox></t:From><t:IsRead>false</t:IsRead></t:Message>
   </t:Items></m:RootFolder></m:FindItemResponseMessage></m:ResponseMessages></m:FindItemResponse></soap:Body></soap:Envelope>`

const getItemSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body><m:GetItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
  <m:ResponseMessages><m:GetItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:Items><t:Message><t:ItemId Id="AAA=" ChangeKey="CQ=="/><t:Subject>Quarterly report</t:Subject>
    <t:Body BodyType="Text">Q2 numbers attached.</t:Body>
    <t:DateTimeReceived>2026-06-28T14:03:22Z</t:DateTimeReceived>
    <t:ToRecipients><t:Mailbox><t:Name>Service</t:Name><t:EmailAddress>agent@example.com</t:EmailAddress></t:Mailbox></t:ToRecipients>
    <t:From><t:Mailbox><t:Name>Jane Doe</t:Name><t:EmailAddress>jane@contoso.com</t:EmailAddress></t:Mailbox></t:From>
    <t:IsRead>true</t:IsRead></t:Message></m:Items></m:GetItemResponseMessage></m:ResponseMessages></m:GetItemResponse></soap:Body></soap:Envelope>`

// The expected JSON IS the Graph contract — the same shape the graph backend
// emits, so CLI consumers parse EWS output identically.
func TestMailListEmitsGraphShape(t *testing.T) {
	be := newBackend(t, findItemSuccess)
	got, err := be.Mail().List(context.Background(), mbx, backend.ListOpts{Max: 25})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := `[{"id":"AAA=","subject":"Quarterly report","from":{"emailAddress":{"name":"Jane Doe","address":""}},"receivedDateTime":"2026-06-28T14:03:22Z","isRead":false}]`
	if string(got) != want {
		t.Errorf("List JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestMailReadEmitsGraphShape(t *testing.T) {
	be := newBackend(t, getItemSuccess)
	got, err := be.Mail().Read(context.Background(), mbx, "AAA=")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := `{"id":"AAA=","subject":"Quarterly report","from":{"emailAddress":{"name":"Jane Doe","address":"jane@contoso.com"}},"toRecipients":[{"emailAddress":{"name":"Service","address":"agent@example.com"}}],"ccRecipients":[],"receivedDateTime":"2026-06-28T14:03:22Z","isRead":true,"body":{"contentType":"text","content":"Q2 numbers attached."}}`
	if string(got) != want {
		t.Errorf("Read JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

const createSendSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:CreateItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:CreateItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode><m:Items/>
  </m:CreateItemResponseMessage></m:ResponseMessages></m:CreateItemResponse></soap:Body></soap:Envelope>`

const createDraftSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:CreateItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:CreateItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:Items><t:Message><t:ItemId Id="DRAFT-1" ChangeKey="CQ=="/></t:Message></m:Items>
  </m:CreateItemResponseMessage></m:ResponseMessages></m:CreateItemResponse></soap:Body></soap:Envelope>`

func TestMailSendSucceeds(t *testing.T) {
	be := newBackend(t, createSendSuccess)
	if err := be.Mail().Send(context.Background(), mbx, mail.Message{Subject: "s", Body: "b", To: []string{"x@y.com"}}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestMailCreateDraftReturnsID(t *testing.T) {
	be := newBackend(t, createDraftSuccess)
	id, err := be.Mail().CreateDraft(context.Background(), mbx, mail.Message{Subject: "s", Body: "b", To: []string{"x@y.com"}})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if id != "DRAFT-1" {
		t.Errorf("draft id = %q, want DRAFT-1", id)
	}
}

func TestMailSearchEmitsGraphShape(t *testing.T) {
	be := newBackend(t, findItemSuccess)
	got, err := be.Mail().Search(context.Background(), mbx, backend.SearchOpts{Query: "subject:report", Max: 25})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := `[{"id":"AAA=","subject":"Quarterly report","from":{"emailAddress":{"name":"Jane Doe","address":""}},"receivedDateTime":"2026-06-28T14:03:22Z","isRead":false}]`
	if string(got) != want {
		t.Errorf("Search JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestUnimplementedReturnErrUnsupported(t *testing.T) {
	be := newBackend(t, findItemSuccess)
	ctx := context.Background()
	// Deferred/cloud-only surfaces still report ErrUnsupported.
	if _, err := be.Calendar().FreeBusy(ctx, mbx, backend.ScheduleQuery{}); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Calendar.FreeBusy: want ErrUnsupported, got %v", err)
	}
	if _, err := be.Contacts().List(ctx, mbx, backend.ListOpts{}); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Contacts.List: want ErrUnsupported, got %v", err)
	}
	if _, err := be.Drive().List(ctx, mbx, ""); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Drive.List: want ErrUnsupported, got %v", err)
	}
	if _, err := be.Sites().Search(ctx, "q"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("Sites.Search: want ErrUnsupported, got %v", err)
	}
}

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

func TestCalendarListEmitsGraphShape(t *testing.T) {
	be := newBackend(t, calListSuccess)
	got, err := be.Calendar().List(context.Background(), mbx, backend.CalListOpts{Start: "2026-06-29T00:00:00Z", End: "2026-07-06T00:00:00Z", Max: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := `[{"id":"EV-1","subject":"Planning","start":{"dateTime":"2026-06-30T14:00:00Z","timeZone":"UTC"},` +
		`"end":{"dateTime":"2026-06-30T15:00:00Z","timeZone":"UTC"},"location":{"displayName":"Room 7"},` +
		`"organizer":{"emailAddress":{"name":"Jane","address":""}},"attendees":[],"isAllDay":false}]`
	if string(got) != want {
		t.Errorf("calendar List JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

const syncSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:SyncFolderItemsResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:SyncFolderItemsResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>
   <m:SyncState>STATE-2</m:SyncState><m:IncludesLastItemInRange>true</m:IncludesLastItemInRange>
   <m:Changes>
    <t:Create><t:Message><t:ItemId Id="N-1" ChangeKey="CQ=="/><t:Subject>Budget</t:Subject>
     <t:DateTimeReceived>2026-06-29T09:14:22Z</t:DateTimeReceived><t:ConversationId Id="CONV-1"/>
     <t:From><t:Mailbox><t:Name>Dan</t:Name><t:EmailAddress>dan@x.com</t:EmailAddress></t:Mailbox></t:From>
     <t:IsRead>false</t:IsRead></t:Message></t:Create>
    <t:Delete><t:ItemId Id="OLD-9" ChangeKey="CK"/></t:Delete>
   </m:Changes></m:SyncFolderItemsResponseMessage></m:ResponseMessages></m:SyncFolderItemsResponse></soap:Body></soap:Envelope>`

func TestMailboxDeltaEmitsGraphDelta(t *testing.T) {
	be := newBackend(t, syncSuccess)
	// First-run cursor is the watch package's hardcoded suffix.
	got, err := be.Mail().MailboxDelta(context.Background(), mbx, "mailFolders/inbox/messages/delta?$select=id")
	if err != nil {
		t.Fatalf("MailboxDelta: %v", err)
	}
	want := `{"@odata.deltaLink":"STATE-2","value":[` +
		`{"id":"N-1","conversationId":"CONV-1","subject":"Budget","bodyPreview":"","receivedDateTime":"2026-06-29T09:14:22Z","isRead":false,"from":{"emailAddress":{"name":"Dan","address":"dan@x.com"}},"toRecipients":[],"body":{"content":""}},` +
		`{"@removed":{"reason":"deleted"},"id":"OLD-9"}` +
		`]}`
	if string(got) != want {
		t.Errorf("watch delta JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

const getItemAttachmentsSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:GetItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages>
  <m:GetItemResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode><m:Items>
   <t:Message><t:ItemId Id="MID" ChangeKey="CQ=="/><t:Attachments>
    <t:FileAttachment><t:AttachmentId Id="ATT-1"/><t:Name>invoice.pdf</t:Name>
     <t:ContentType>application/pdf</t:ContentType><t:Size>48213</t:Size></t:FileAttachment>
   </t:Attachments></t:Message></m:Items></m:GetItemResponseMessage></m:ResponseMessages></m:GetItemResponse></soap:Body></soap:Envelope>`

func TestAttachmentsEmitsGraphShape(t *testing.T) {
	be := newBackend(t, getItemAttachmentsSuccess)
	got, err := be.Mail().Attachments(context.Background(), mbx, "MID")
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	want := `[{"id":"ATT-1","name":"invoice.pdf","contentType":"application/pdf","size":48213}]`
	if string(got) != want {
		t.Errorf("attachments JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}
