package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreRoundtrip(t *testing.T) {
	s := NewFileStore(t.TempDir())
	in := State{DeltaLink: "https://delta", Seen: []string{"a", "b"}}
	if err := s.Put("agent@x.com", "inbox", in); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get("agent@x.com", "inbox")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DeltaLink != "https://delta" || len(got.Seen) != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestFileStoreMissingIsEmptyNotError(t *testing.T) {
	s := NewFileStore(t.TempDir())
	got, err := s.Get("nobody@x.com", "inbox")
	if err != nil {
		t.Fatalf("Get on missing must not error: %v", err)
	}
	if got.DeltaLink != "" {
		t.Errorf("missing state must be empty, got %+v", got)
	}
}

func TestFileStoreIsolatesByMailboxAndFolder(t *testing.T) {
	s := NewFileStore(t.TempDir())
	_ = s.Put("a@x.com", "inbox", State{DeltaLink: "A"})
	got, _ := s.Get("a@x.com", "archive")
	if got.DeltaLink != "" {
		t.Error("different folder must be isolated")
	}
}

func TestFileStoreWritesMode0600(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)
	_ = s.Put("a@x.com", "inbox", State{DeltaLink: "A"})
	matches, _ := filepathGlob(dir)
	if len(matches) == 0 {
		t.Fatal("no state file written")
	}
	info, _ := os.Stat(matches[0])
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestTrimSeenCapsOldestFirst(t *testing.T) {
	got := trimSeen([]string{"a", "b", "c", "d"}, 2)
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Errorf("trimSeen = %v, want [c d]", got)
	}
}

func filepathGlob(dir string) ([]string, error) { return filepath.Glob(dir + "/*.json") }
