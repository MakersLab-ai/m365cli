package mail

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewFileAttachmentDerivesNameAndType(t *testing.T) {
	a, err := NewFileAttachment("/tmp/uva/UVA 10-2026.PDF", []byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "UVA 10-2026.PDF" || a.ContentType != "application/pdf" {
		t.Errorf("got %+v", a)
	}
	if b, _ := NewFileAttachment("x.unknownext", []byte("1")); b.ContentType != "application/octet-stream" {
		t.Errorf("fallback type = %q", b.ContentType)
	}
}

func TestNewFileAttachmentRejectsEmptyAndHuge(t *testing.T) {
	if _, err := NewFileAttachment("a.pdf", nil); err == nil {
		t.Error("empty file accepted")
	}
	if _, err := NewFileAttachment("a.pdf", make([]byte, MaxAttachmentSize+1)); err == nil || !strings.Contains(err.Error(), "150 MB") {
		t.Errorf("huge file: err = %v", err)
	}
}

func TestUploadChunkSizeIsMultipleOf320KiB(t *testing.T) {
	if UploadChunkSize%(320*1024) != 0 || UploadChunkSize > 4*1024*1024 {
		t.Errorf("chunk size %d violates Graph's upload-session rules", UploadChunkSize)
	}
}

func TestBuildUploadSessionCarriesSize(t *testing.T) {
	raw, _ := BuildUploadSession(FileAttachment{Name: "a.pdf", ContentType: "application/pdf", Data: make([]byte, 42)})
	var got struct {
		Item struct {
			Size int    `json:"size"`
			Type string `json:"attachmentType"`
		} `json:"AttachmentItem"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Item.Size != 42 || got.Item.Type != "file" {
		t.Errorf("session = %s", raw)
	}
}
