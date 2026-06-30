package ews

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// Attachment is attachment metadata (no content).
type Attachment struct {
	ID          string
	Name        string
	ContentType string
	Size        int64
}

// AttachmentContent is a downloaded file attachment.
type AttachmentContent struct {
	Name        string
	ContentType string
	Bytes       []byte
}

type getAttachmentResponse struct {
	Messages []getAttachmentResponseMessage `xml:"ResponseMessages>GetAttachmentResponseMessage"`
}

type getAttachmentResponseMessage struct {
	ResponseClass string              `xml:"ResponseClass,attr"`
	ResponseCode  string              `xml:"ResponseCode"`
	MessageText   string              `xml:"MessageText"`
	File          []xmlFileAttachment `xml:"Attachments>FileAttachment"`
}

// ListAttachments returns the file and item attachment metadata on a message.
func (c *Client) ListAttachments(ctx context.Context, mailbox, msgID string) ([]Attachment, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return nil, err
	}
	env, err := c.post(ctx, getItemAttachmentsEnvelope(mailbox, msgID))
	if err != nil {
		return nil, err
	}
	if env.Body.GetItemResponse == nil || len(env.Body.GetItemResponse.Messages) == 0 {
		return nil, fmt.Errorf("ews: empty GetItem response")
	}
	rm := env.Body.GetItemResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return nil, fmt.Errorf("ews GetItem %s: %s", rm.ResponseCode, rm.MessageText)
	}
	if len(rm.Items) == 0 || rm.Items[0].Attachments == nil {
		return []Attachment{}, nil
	}
	att := rm.Items[0].Attachments
	out := make([]Attachment, 0, len(att.File)+len(att.Item))
	for _, f := range append(append([]xmlFileAttachment{}, att.File...), att.Item...) {
		out = append(out, Attachment{ID: f.ID.ID, Name: f.Name, ContentType: f.ContentType, Size: f.Size})
	}
	return out, nil
}

// GetAttachment downloads one file attachment by its attachment id. The mailbox
// is required for impersonation and the allowlist; item attachments (embedded
// messages) are not supported for download.
func (c *Client) GetAttachment(ctx context.Context, mailbox, attID string) (AttachmentContent, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return AttachmentContent{}, err
	}
	env, err := c.post(ctx, getAttachmentEnvelope(mailbox, attID))
	if err != nil {
		return AttachmentContent{}, err
	}
	if env.Body.GetAttachmentResponse == nil || len(env.Body.GetAttachmentResponse.Messages) == 0 {
		return AttachmentContent{}, fmt.Errorf("ews: empty GetAttachment response")
	}
	rm := env.Body.GetAttachmentResponse.Messages[0]
	if rm.ResponseClass != "Success" {
		return AttachmentContent{}, fmt.Errorf("ews GetAttachment %s: %s", rm.ResponseCode, rm.MessageText)
	}
	if len(rm.File) == 0 {
		return AttachmentContent{}, fmt.Errorf("ews: attachment %q is not a downloadable file attachment", attID)
	}
	fa := rm.File[0]
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(fa.Content))
	if err != nil {
		return AttachmentContent{}, fmt.Errorf("ews: decode attachment %q: %w", fa.Name, err)
	}
	return AttachmentContent{Name: fa.Name, ContentType: fa.ContentType, Bytes: raw}, nil
}

func getItemAttachmentsEnvelope(mailbox, id string) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:GetItem><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape>`+
		`<t:AdditionalProperties><t:FieldURI FieldURI="item:Attachments"/></t:AdditionalProperties>`+
		`</m:ItemShape><m:ItemIds><t:ItemId Id="%s"/></m:ItemIds></m:GetItem>`+
		`</soap:Body></soap:Envelope>`, esc(id))
	return b.Bytes()
}

func getAttachmentEnvelope(mailbox, attID string) []byte {
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:GetAttachment><m:AttachmentShape/>`+
		`<m:AttachmentIds><t:AttachmentId Id="%s"/></m:AttachmentIds></m:GetAttachment>`+
		`</soap:Body></soap:Envelope>`, esc(attID))
	return b.Bytes()
}
