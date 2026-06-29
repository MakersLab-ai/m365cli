package ews

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const createSendSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body><m:CreateItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
  <m:ResponseMessages><m:CreateItemResponseMessage ResponseClass="Success">
   <m:ResponseCode>NoError</m:ResponseCode><m:Items/>
  </m:CreateItemResponseMessage></m:ResponseMessages></m:CreateItemResponse></soap:Body></soap:Envelope>`

const createDraftSuccess = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body><m:CreateItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
   xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
  <m:ResponseMessages><m:CreateItemResponseMessage ResponseClass="Success">
   <m:ResponseCode>NoError</m:ResponseCode>
   <m:Items><t:Message><t:ItemId Id="DRAFT-1" ChangeKey="CQ=="/></t:Message></m:Items>
  </m:CreateItemResponseMessage></m:ResponseMessages></m:CreateItemResponse></soap:Body></soap:Envelope>`

func TestSendMessageBuildsCreateItem(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, createSendSuccess)
	})
	err := c.SendMessage(context.Background(), mbx, OutMessage{
		Subject: "Hi", Body: "Hello & <bye>", To: []string{"x@y.com"}, Cc: []string{"c@y.com"},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	for _, want := range []string{
		`<m:CreateItem MessageDisposition="SendAndSaveCopy">`,
		`<t:DistinguishedFolderId Id="sentitems"/>`,
		// schema-fixed order: Subject, Body, To, Cc; recipients use EmailAddress; body XML-escaped
		`<t:Subject>Hi</t:Subject><t:Body BodyType="Text">Hello &amp; &lt;bye&gt;</t:Body>` +
			`<t:ToRecipients><t:Mailbox><t:EmailAddress>x@y.com</t:EmailAddress></t:Mailbox></t:ToRecipients>` +
			`<t:CcRecipients><t:Mailbox><t:EmailAddress>c@y.com</t:EmailAddress></t:Mailbox></t:CcRecipients>`,
	} {
		if !strings.Contains(*body, want) {
			t.Errorf("request missing %q\nbody: %s", want, *body)
		}
	}
}

func TestSendMessageOmitsEmptyCc(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, createSendSuccess)
	})
	if err := c.SendMessage(context.Background(), mbx, OutMessage{Subject: "s", Body: "b", To: []string{"x@y.com"}}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if strings.Contains(*body, "CcRecipients") {
		t.Errorf("empty cc should be omitted\nbody: %s", *body)
	}
}

func TestCreateDraftSaveOnlyReturnsID(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, createDraftSuccess)
	})
	id, err := c.CreateDraft(context.Background(), mbx, OutMessage{Subject: "s", Body: "b", To: []string{"x@y.com"}})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if !strings.Contains(*body, `MessageDisposition="SaveOnly"`) || !strings.Contains(*body, `Id="drafts"`) {
		t.Errorf("draft request wrong\nbody: %s", *body)
	}
	if id != "DRAFT-1" {
		t.Errorf("draft id = %q, want DRAFT-1", id)
	}
}

func TestCreateItemErrorIsLoud(t *testing.T) {
	const errResp = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>
 <m:CreateItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
  <m:ResponseMessages><m:CreateItemResponseMessage ResponseClass="Error">
   <m:MessageText>Recipient invalid.</m:MessageText><m:ResponseCode>ErrorInvalidRecipients</m:ResponseCode>
  </m:CreateItemResponseMessage></m:ResponseMessages></m:CreateItemResponse></soap:Body></soap:Envelope>`
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, errResp) })
	err := c.SendMessage(context.Background(), mbx, OutMessage{Subject: "s", Body: "b", To: []string{"x@y.com"}})
	if err == nil || !strings.Contains(err.Error(), "ErrorInvalidRecipients") {
		t.Fatalf("want loud CreateItem error, got %v", err)
	}
}

func TestSearchBuildsQueryStringLast(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, findItemSuccess)
	})
	items, err := c.Search(context.Background(), mbx, "subject:invoice", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(*body, `<m:QueryString>subject:invoice</m:QueryString></m:FindItem>`) {
		t.Errorf("QueryString must be last in FindItem\nbody: %s", *body)
	}
	// QueryString must come AFTER ParentFolderIds (schema order).
	if strings.Index(*body, "ParentFolderIds") > strings.Index(*body, "QueryString") {
		t.Errorf("QueryString must follow ParentFolderIds\nbody: %s", *body)
	}
	if len(items) != 1 || items[0].Subject != "Quarterly report" {
		t.Errorf("search parse wrong: %+v", items)
	}
}

func TestSendRefusesOutOfScope(t *testing.T) {
	hit := false
	c, _ := testClient(t, func(http.ResponseWriter, *http.Request) { hit = true })
	if err := c.SendMessage(context.Background(), "intruder@example.com", OutMessage{To: []string{"x@y.com"}}); err == nil {
		t.Fatal("expected out-of-scope send error")
	}
	if hit {
		t.Error("server must NOT be called for a disallowed mailbox")
	}
}
