package ews

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/config"
)

const mbx = "agent@example.com"

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *string) {
	t.Helper()
	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{EWSURL: srv.URL, EWSUser: "DOM\\svc", AllowedMailboxes: []string{mbx}}
	c := New(cfg, "secret")
	c.SetHTTPClient(srv.Client())
	c.URL = srv.URL
	return c, &lastBody
}

const findItemSuccess = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body>
  <m:FindItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
                      xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
   <m:ResponseMessages>
    <m:FindItemResponseMessage ResponseClass="Success">
     <m:ResponseCode>NoError</m:ResponseCode>
     <m:RootFolder TotalItemsInView="1" IncludesLastItemInRange="true">
      <t:Items>
       <t:Message>
        <t:ItemId Id="AAA=" ChangeKey="CQ=="/>
        <t:Subject>Quarterly report</t:Subject>
        <t:DateTimeReceived>2026-06-28T14:03:22Z</t:DateTimeReceived>
        <t:From><t:Mailbox><t:Name>Jane Doe</t:Name></t:Mailbox></t:From>
        <t:IsRead>false</t:IsRead>
       </t:Message>
      </t:Items>
     </m:RootFolder>
    </m:FindItemResponseMessage>
   </m:ResponseMessages>
  </m:FindItemResponse>
 </soap:Body>
</soap:Envelope>`

func TestFindInboxParsesAndImpersonates(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, findItemSuccess)
	})

	items, err := c.FindInbox(context.Background(), mbx, 25)
	if err != nil {
		t.Fatalf("FindInbox: %v", err)
	}
	// request carries impersonation + the inbox FindItem shape
	for _, want := range []string{
		"<t:PrimarySmtpAddress>agent@example.com</t:PrimarySmtpAddress>",
		`<m:FindItem Traversal="Shallow">`,
		`MaxEntriesReturned="25"`,
		`<t:DistinguishedFolderId Id="inbox"/>`,
	} {
		if !strings.Contains(*body, want) {
			t.Errorf("request missing %q\nbody: %s", want, *body)
		}
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.ID != "AAA=" || it.Subject != "Quarterly report" || it.From.Name != "Jane Doe" ||
		it.Received != "2026-06-28T14:03:22Z" || it.IsRead {
		t.Errorf("parsed item wrong: %+v", it)
	}
}

const getItemSuccess = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body>
  <m:GetItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
                     xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
   <m:ResponseMessages>
    <m:GetItemResponseMessage ResponseClass="Success">
     <m:ResponseCode>NoError</m:ResponseCode>
     <m:Items>
      <t:Message>
       <t:ItemId Id="AAA=" ChangeKey="CQ=="/>
       <t:Subject>Quarterly report</t:Subject>
       <t:Body BodyType="Text">Q2 numbers attached.</t:Body>
       <t:DateTimeReceived>2026-06-28T14:03:22Z</t:DateTimeReceived>
       <t:ToRecipients>
        <t:Mailbox><t:Name>Service</t:Name><t:EmailAddress>agent@example.com</t:EmailAddress></t:Mailbox>
       </t:ToRecipients>
       <t:From><t:Mailbox><t:Name>Jane Doe</t:Name><t:EmailAddress>jane@contoso.com</t:EmailAddress></t:Mailbox></t:From>
       <t:IsRead>true</t:IsRead>
      </t:Message>
     </m:Items>
    </m:GetItemResponseMessage>
   </m:ResponseMessages>
  </m:GetItemResponse>
 </soap:Body>
</soap:Envelope>`

func TestGetMessageParsesBodyAndRecipients(t *testing.T) {
	c, body := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, getItemSuccess)
	})

	it, err := c.GetMessage(context.Background(), mbx, "AAA=")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if !strings.Contains(*body, `<t:ItemId Id="AAA="/>`) {
		t.Errorf("request missing item id\nbody: %s", *body)
	}
	if it.From.Address != "jane@contoso.com" || it.Body == nil || it.Body.Content != "Q2 numbers attached." ||
		it.Body.Type != "Text" || len(it.To) != 1 || it.To[0].Address != "agent@example.com" || !it.IsRead {
		t.Errorf("parsed item wrong: %+v body=%+v", it, it.Body)
	}
}

func TestResponseClassErrorIsLoud(t *testing.T) {
	const errResp = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body><m:FindItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">
  <m:ResponseMessages><m:FindItemResponseMessage ResponseClass="Error">
   <m:MessageText>Id is malformed.</m:MessageText><m:ResponseCode>ErrorInvalidIdMalformed</m:ResponseCode>
  </m:FindItemResponseMessage></m:ResponseMessages></m:FindItemResponse></soap:Body></soap:Envelope>`
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, errResp) })
	_, err := c.FindInbox(context.Background(), mbx, 10)
	if err == nil || !strings.Contains(err.Error(), "ErrorInvalidIdMalformed") {
		t.Fatalf("want loud ResponseClass error, got %v", err)
	}
}

func TestSoapFaultIsLoud(t *testing.T) {
	const fault = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
 <soap:Body><soap:Fault>
  <faultcode>soap:Client</faultcode><faultstring>schema invalid</faultstring>
  <detail><e:ResponseCode xmlns:e="http://schemas.microsoft.com/exchange/services/2006/errors">ErrorSchemaValidation</e:ResponseCode>
  <e:Message xmlns:e="http://schemas.microsoft.com/exchange/services/2006/errors">The request failed schema validation.</e:Message></detail>
 </soap:Fault></soap:Body></soap:Envelope>`
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, fault)
	})
	_, err := c.FindInbox(context.Background(), mbx, 10)
	if err == nil || !strings.Contains(err.Error(), "ErrorSchemaValidation") {
		t.Fatalf("want loud soap fault, got %v", err)
	}
}

func TestUnauthorizedIsDistinct(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.FindInbox(context.Background(), mbx, 10)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want distinct 401 error, got %v", err)
	}
}

func TestAllowlistRefusesBeforeIO(t *testing.T) {
	hit := false
	c, _ := testClient(t, func(http.ResponseWriter, *http.Request) { hit = true })
	if _, err := c.FindInbox(context.Background(), "intruder@example.com", 10); err == nil {
		t.Fatal("expected out-of-scope mailbox error")
	}
	if hit {
		t.Error("server must NOT be called for a disallowed mailbox")
	}
}
