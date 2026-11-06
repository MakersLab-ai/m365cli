package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/config"
)

func TestLoadAttachmentsRefusedOnEWSBeforeAnything(t *testing.T) {
	_, err := loadAttachments(&config.Config{Backend: "ews"}, []string{"/does/not/matter.pdf"})
	if !errors.Is(err, backend.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestLoadAttachmentsReadsFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "UVA.pdf")
	if err := os.WriteFile(p, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	atts, err := loadAttachments(&config.Config{}, []string{p})
	if err != nil || len(atts) != 1 || atts[0].Name != "UVA.pdf" || atts[0].ContentType != "application/pdf" {
		t.Fatalf("atts = %+v, err = %v", atts, err)
	}
	if _, err := loadAttachments(&config.Config{}, []string{filepath.Join(dir, "missing.pdf")}); err == nil {
		t.Fatal("missing file accepted")
	}
}
