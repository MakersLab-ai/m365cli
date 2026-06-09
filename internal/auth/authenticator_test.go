package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenFetchesOnceThenServesFromCache(t *testing.T) {
	calls := 0
	a := &Authenticator{
		key:   "tenant|scope",
		cache: NewFileCache(filepath.Join(t.TempDir(), "t.json")),
		fetch: func(context.Context) (string, time.Time, error) {
			calls++
			return "tok", time.Now().Add(time.Hour), nil
		},
	}

	for i := 0; i < 3; i++ {
		got, err := a.Token(context.Background())
		if err != nil || got != "tok" {
			t.Fatalf("Token() = (%q, %v)", got, err)
		}
	}
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 (cache should serve the rest)", calls)
	}
}

func TestTokenRefetchesWhenCachedTokenExpired(t *testing.T) {
	calls := 0
	a := &Authenticator{
		key:   "tenant|scope",
		cache: NewFileCache(filepath.Join(t.TempDir(), "t.json")),
		fetch: func(context.Context) (string, time.Time, error) {
			calls++
			return "tok", time.Now().Add(-time.Hour), nil // already expired
		},
	}
	_, _ = a.Token(context.Background())
	_, _ = a.Token(context.Background())
	if calls != 2 {
		t.Errorf("fetch called %d times, want 2 (expired token must not be cached)", calls)
	}
}

func TestTokenPropagatesFetchError(t *testing.T) {
	a := &Authenticator{
		key:   "k",
		cache: NewFileCache(filepath.Join(t.TempDir(), "t.json")),
		fetch: func(context.Context) (string, time.Time, error) {
			return "", time.Time{}, errors.New("AADSTS700027: cert invalid")
		},
	}
	if _, err := a.Token(context.Background()); err == nil {
		t.Error("Token must propagate the fetch error")
	}
}
