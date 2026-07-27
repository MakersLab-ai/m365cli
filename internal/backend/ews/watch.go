package ewsbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MakersLab-ai/m365cli/internal/ews"
)

// maxSyncChanges is the EWS SyncFolderItems per-page cap (server max is 512).
const maxSyncChanges = 512

// watchMsg is the Graph-delta message projection the watch pipeline parses
// (internal/watch/payload.go graphMsg). The EWS backend produces it from one
// SyncFolderItems page so watch stays transport-neutral.
type watchMsg struct {
	ID               string      `json:"id"`
	ConversationID   string      `json:"conversationId"`
	Subject          string      `json:"subject"`
	BodyPreview      string      `json:"bodyPreview"`
	ReceivedDateTime string      `json:"receivedDateTime"`
	IsRead           bool        `json:"isRead"`
	From             jsonEmail   `json:"from"`
	ToRecipients     []jsonEmail `json:"toRecipients"`
	Body             struct {
		Content string `json:"content"`
	} `json:"body"`
}

// mailboxDelta runs one SyncFolderItems page and renders it as a Graph
// messages/delta document. The first-run cursor is the watch package's
// hardcoded "mailFolders/..." suffix (→ empty SyncState); any other cursor is a
// stored EWS SyncState. The returned @odata.deltaLink/@odata.nextLink carries
// the next SyncState.
func mailboxDelta(ctx context.Context, c *ews.Client, mailbox, urlOrCursor string) ([]byte, error) {
	syncState := urlOrCursor
	if urlOrCursor == "" || strings.HasPrefix(urlOrCursor, "mailFolders/") {
		syncState = "" // first run
	}

	page, err := c.SyncInbox(ctx, mailbox, syncState, maxSyncChanges)
	if err != nil {
		return nil, err
	}
	if page.SyncState == "" {
		// Without a cursor the watcher would re-prime every poll and drop mail.
		return nil, fmt.Errorf("ews watch: SyncFolderItems returned no SyncState")
	}

	value := make([]json.RawMessage, 0, len(page.Changed)+len(page.Removed))
	for _, it := range page.Changed {
		wm := watchMsg{
			ID: it.ID, ConversationID: it.ConversationID, Subject: it.Subject,
			ReceivedDateTime: it.Received, IsRead: it.IsRead, From: email(it.From),
			ToRecipients: emails(it.To),
		}
		if it.Body != nil {
			wm.Body.Content = it.Body.Content
			wm.BodyPreview = preview(it.Body.Content)
		}
		raw, err := json.Marshal(wm)
		if err != nil {
			return nil, err
		}
		value = append(value, raw)
	}
	for _, id := range page.Removed {
		raw, err := json.Marshal(map[string]any{"id": id, "@removed": map[string]string{"reason": "deleted"}})
		if err != nil {
			return nil, err
		}
		value = append(value, raw)
	}

	// More pages → @odata.nextLink; final page → @odata.deltaLink. Both carry the
	// SyncState the watcher will hand back on the next call.
	doc := map[string]any{"value": value}
	if page.More {
		doc["@odata.nextLink"] = page.SyncState
	} else {
		doc["@odata.deltaLink"] = page.SyncState
	}
	return json.Marshal(doc)
}

// preview synthesizes a Graph-like bodyPreview from the plain-text body.
func preview(body string) string {
	const max = 255
	s := strings.TrimSpace(body)
	if len(s) > max {
		return s[:max]
	}
	return s
}
