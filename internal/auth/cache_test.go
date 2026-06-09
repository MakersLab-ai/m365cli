package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestCache(t *testing.T) (*FileCache, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tokens.json")
	return NewFileCache(p), p
}

func TestCachePutGetRoundtrip(t *testing.T) {
	c, _ := newTestCache(t)
	if err := c.Put("tenant|scope", "tok-abc", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := c.Get("tenant|scope")
	if !ok || got != "tok-abc" {
		t.Errorf("Get = (%q, %v), want (tok-abc, true)", got, ok)
	}
}

func TestCacheMissReturnsFalse(t *testing.T) {
	c, _ := newTestCache(t)
	if _, ok := c.Get("absent"); ok {
		t.Error("Get on absent key must return false")
	}
}

func TestCacheExpiredTokenNotReturned(t *testing.T) {
	c, _ := newTestCache(t)
	if err := c.Put("k", "stale", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k"); ok {
		t.Error("expired token must not be returned")
	}
}

func TestCacheTreatsNearExpiryAsExpired(t *testing.T) {
	c, _ := newTestCache(t)
	// Within the refresh skew window — must be treated as expired to avoid
	// handing out a token that dies mid-request.
	if err := c.Put("k", "soon", time.Now().Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k"); ok {
		t.Error("token inside the skew window must be treated as expired")
	}
}

func TestCacheIsolatesByKey(t *testing.T) {
	c, _ := newTestCache(t)
	if err := c.Put("keyA", "a", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("keyB"); ok {
		t.Error("keys must be isolated")
	}
}

func TestCacheFileIsMode0600(t *testing.T) {
	c, p := newTestCache(t)
	if err := c.Put("k", "v", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file mode = %o, want 600", perm)
	}
}

func TestCachePersistsAcrossInstances(t *testing.T) {
	c, p := newTestCache(t)
	if err := c.Put("k", "persisted", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A fresh cache over the same file (simulating a new CLI process) must read it.
	got, ok := NewFileCache(p).Get("k")
	if !ok || got != "persisted" {
		t.Errorf("reopened cache Get = (%q, %v), want (persisted, true)", got, ok)
	}
}
