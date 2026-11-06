package mail

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
)

// SmallAttachmentLimit is the largest file Graph accepts in a single
// POST /messages/{id}/attachments. Anything bigger has to go through an upload
// session (createUploadSession + chunked PUTs).
const SmallAttachmentLimit = 3 * 1024 * 1024

// MaxAttachmentSize is Graph's ceiling for one file attachment on a message.
const MaxAttachmentSize = 150 * 1024 * 1024

// UploadChunkSize is the chunk size for upload sessions. Graph requires chunks
// to be a multiple of 320 KiB and recommends staying below 4 MiB.
const UploadChunkSize = 320 * 1024 * 10

// FileAttachment is a regular attachment: a file the recipient sees in the
// attachment list and can open or save (a PDF, a spreadsheet, …). It is the
// counterpart to InlineImage, which lives inside the body.
type FileAttachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// NewFileAttachment builds a FileAttachment from a file's bytes, deriving name
// and MIME type from the path.
func NewFileAttachment(path string, data []byte) (FileAttachment, error) {
	if len(data) == 0 {
		return FileAttachment{}, fmt.Errorf("attachment %q is empty", path)
	}
	if len(data) > MaxAttachmentSize {
		return FileAttachment{}, fmt.Errorf("attachment %q is %d bytes — Microsoft 365 accepts at most 150 MB per file", path, len(data))
	}
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return FileAttachment{Name: filepath.Base(path), ContentType: ct, Data: data}, nil
}

// BuildFileAttachment renders the Graph fileAttachment payload for a small
// file (POST /messages/{id}/attachments). isInline is false on purpose: Outlook
// hides inline attachments that the body does not reference.
func BuildFileAttachment(a FileAttachment) ([]byte, error) {
	return json.Marshal(map[string]any{
		"@odata.type":  "#microsoft.graph.fileAttachment",
		"name":         a.Name,
		"contentType":  a.ContentType,
		"isInline":     false,
		"contentBytes": base64.StdEncoding.EncodeToString(a.Data),
	})
}

// BuildUploadSession renders the createUploadSession payload for a large file
// (POST /messages/{id}/attachments/createUploadSession).
func BuildUploadSession(a FileAttachment) ([]byte, error) {
	return json.Marshal(map[string]any{
		"AttachmentItem": map[string]any{
			"attachmentType": "file",
			"name":           a.Name,
			"size":           len(a.Data),
			"contentType":    a.ContentType,
			"isInline":       false,
		},
	})
}
