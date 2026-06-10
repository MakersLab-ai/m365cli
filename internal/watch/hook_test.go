package watch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHookClientPostsBodyAndBearer(t *testing.T) {
	var gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewHookClient(srv.URL, "tok123")
	if err := c.Post(context.Background(), []byte(`{"x":1}`)); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if gotAuth != "Bearer tok123" || gotCT != "application/json" || gotBody != `{"x":1}` {
		t.Errorf("auth=%q ct=%q body=%q", gotAuth, gotCT, gotBody)
	}
}

func TestHookClientNoBearerWhenTokenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	_ = NewHookClient(srv.URL, "").Post(context.Background(), []byte(`{}`))
	if gotAuth != "" {
		t.Errorf("Authorization must be empty when no token, got %q", gotAuth)
	}
}

func TestHookClientErrorsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := NewHookClient(srv.URL, "").Post(context.Background(), []byte(`{}`)); err == nil {
		t.Error("expected error on 500")
	}
}
