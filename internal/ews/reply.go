package ews

import (
	"bytes"
	"context"
	"fmt"
)

// Reply sends a reply (or reply-all) to the referenced message as the
// impersonated mailbox, saving a copy to Sent Items. EWS resolves the original
// server-side from its item id; only the id is needed (ChangeKey is optional and
// not stable, so it is omitted).
func (c *Client) Reply(ctx context.Context, mailbox, refItemID, body string, replyAll bool) error {
	if err := c.requireAllowed(mailbox); err != nil {
		return err
	}
	env, err := c.post(ctx, replyEnvelope(mailbox, refItemID, body, replyAll, "SendAndSaveCopy", "sentitems"))
	if err != nil {
		return err
	}
	_, err = createResult(env)
	return err
}

// CreateReplyDraft saves an unsent reply draft and returns its item id.
func (c *Client) CreateReplyDraft(ctx context.Context, mailbox, refItemID, body string, replyAll bool) (string, error) {
	if err := c.requireAllowed(mailbox); err != nil {
		return "", err
	}
	env, err := c.post(ctx, replyEnvelope(mailbox, refItemID, body, replyAll, "SaveOnly", "drafts"))
	if err != nil {
		return "", err
	}
	rm, err := createResult(env)
	if err != nil {
		return "", err
	}
	if len(rm.Items) == 0 {
		return "", fmt.Errorf("ews: reply draft returned no item id")
	}
	return rm.Items[0].ItemID.ID, nil
}

// replyEnvelope builds a CreateItem with a ReplyToItem/ReplyAllToItem response
// object. Child order is schema-fixed: ReferenceItemId before NewBodyContent.
func replyEnvelope(mailbox, refItemID, body string, replyAll bool, disposition, savedFolder string) []byte {
	tag := "ReplyToItem"
	if replyAll {
		tag = "ReplyAllToItem"
	}
	var b bytes.Buffer
	b.WriteString(envelopeHead(mailbox))
	fmt.Fprintf(&b, `<m:CreateItem MessageDisposition="%s">`+
		`<m:SavedItemFolderId><t:DistinguishedFolderId Id="%s"/></m:SavedItemFolderId>`+
		`<m:Items><t:%s>`+
		`<t:ReferenceItemId Id="%s"/>`+
		`<t:NewBodyContent BodyType="Text">%s</t:NewBodyContent>`+
		`</t:%s></m:Items></m:CreateItem></soap:Body></soap:Envelope>`,
		disposition, savedFolder, tag, esc(refItemID), esc(body), tag)
	return b.Bytes()
}
