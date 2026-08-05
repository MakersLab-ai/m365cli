package mail

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
)

// SpliceIntoQuoted puts a reply body at the TOP of the quoted original that a
// createReply / createReplyAll draft already carries — the shape an Outlook
// reply has: the new text first, the original thread below it, with its inline
// images, signatures and formatting untouched.
//
// Why this exists: the draft Graph hands back is not empty. It already contains
// the quoted original. Writing the reply as the *whole* body — what this CLI
// did until 2026-08-05 — REPLACES that quote, so the thread the reader expects
// under the answer is gone, and every inline image with it. Reported by Gerald
// Katterbauer as "mal ohne Thread, mal ohne Bilder" (GC 975b01a9).
//
// The quote is an HTML document, so the reply cannot simply be prepended:
// anything before <html> lands outside the rendered body. The insertion point
// is therefore the first <body …> tag. A bare fragment without <body> is
// concatenated, which renders the same.
func SpliceIntoQuoted(reply, quoted string) string {
	if strings.TrimSpace(quoted) == "" {
		return reply
	}
	if at, ok := afterBodyOpen(quoted); ok {
		return quoted[:at] + reply + quoted[at:]
	}
	return reply + quoted
}

// afterBodyOpen returns the offset just past the opening <body …> tag.
func afterBodyOpen(doc string) (int, bool) {
	i := strings.Index(strings.ToLower(doc), "<body")
	if i < 0 {
		return 0, false
	}
	// The tag ends at the first '>' after it. An attribute value containing a
	// literal '>' would misplace the splice by a few characters; Outlook does
	// not emit one, and the quote itself is never at risk either way.
	j := strings.Index(doc[i:], ">")
	if j < 0 {
		return 0, false
	}
	return i + j + 1, true
}

// InlineImage is a picture that travels inside the message body, referenced
// from the HTML as <img src="cid:CONTENT-ID">. Signature logos are the reason
// this exists: the quoted original brings its own images along, but the block
// the agent writes on top has none unless they are attached explicitly.
type InlineImage struct {
	Name        string // file name shown in the attachment list
	ContentID   string // the cid: the body references
	ContentType string // image/png, image/jpeg, …
	Data        []byte
}

// NewInlineImage builds an InlineImage from a content id and the file's bytes,
// deriving name and MIME type from the path.
func NewInlineImage(contentID, path string, data []byte) (InlineImage, error) {
	if strings.TrimSpace(contentID) == "" {
		return InlineImage{}, fmt.Errorf("inline image needs a content id (cid=path)")
	}
	if len(data) == 0 {
		return InlineImage{}, fmt.Errorf("inline image %q is empty", path)
	}
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return InlineImage{
		Name:        filepath.Base(path),
		ContentID:   contentID,
		ContentType: ct,
		Data:        data,
	}, nil
}

// BuildInlineAttachment renders the Graph fileAttachment payload that adds an
// inline image to an existing draft (POST /messages/{id}/attachments).
func BuildInlineAttachment(img InlineImage) ([]byte, error) {
	if strings.TrimSpace(img.ContentID) == "" {
		return nil, fmt.Errorf("inline image needs a content id")
	}
	return json.Marshal(map[string]any{
		"@odata.type":  "#microsoft.graph.fileAttachment",
		"name":         img.Name,
		"contentType":  img.ContentType,
		"contentId":    img.ContentID,
		"isInline":     true,
		"contentBytes": base64.StdEncoding.EncodeToString(img.Data),
	})
}
