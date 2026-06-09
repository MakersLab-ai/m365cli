package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// refreshSkew treats a token as expired this long before its real expiry, so we
// never hand out a token that would die mid-request.
const refreshSkew = 30 * time.Second

// FileCache is a 0600 JSON token cache keyed by an opaque string (the caller
// uses "tenant|scope"). The CLI is short-lived, so persisting tokens across
// invocations avoids a token request on every command.
type FileCache struct {
	path string
}

type cachedToken struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// NewFileCache returns a cache backed by the file at path (created on first Put).
func NewFileCache(path string) *FileCache {
	return &FileCache{path: path}
}

// Get returns the cached access token for key if present and not within the
// refresh-skew window of expiry.
func (c *FileCache) Get(key string) (string, bool) {
	entries := c.load()
	tok, ok := entries[key]
	if !ok {
		return "", false
	}
	if time.Now().Add(refreshSkew).After(tok.ExpiresAt) {
		return "", false
	}
	return tok.AccessToken, true
}

// Put stores accessToken for key with its expiry and persists the file at 0600.
func (c *FileCache) Put(key, accessToken string, expiresAt time.Time) error {
	entries := c.load()
	entries[key] = cachedToken{AccessToken: accessToken, ExpiresAt: expiresAt}

	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o600)
}

// load reads the cache file, returning an empty map if it is absent or corrupt.
func (c *FileCache) load() map[string]cachedToken {
	entries := map[string]cachedToken{}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return entries
	}
	_ = json.Unmarshal(data, &entries) // corrupt cache → treat as empty
	return entries
}
