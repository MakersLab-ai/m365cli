package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
)

// GraphDefaultScope is the app-only scope for Microsoft Graph. App permissions
// are resolved by the ".default" scope from the app registration + admin consent.
const GraphDefaultScope = "https://graph.microsoft.com/.default"

// Authenticator returns app-only access tokens, served from the on-disk cache
// when valid and acquired via the certificate assertion otherwise.
type Authenticator struct {
	key   string
	cache *FileCache
	fetch func(context.Context) (string, time.Time, error)
}

// Token returns a valid access token, refreshing via fetch when the cache has no
// live token for this (tenant, scope).
func (a *Authenticator) Token(ctx context.Context) (string, error) {
	if tok, ok := a.cache.Get(a.key); ok {
		return tok, nil
	}
	tok, expiresAt, err := a.fetch(ctx)
	if err != nil {
		return "", err
	}
	_ = a.cache.Put(a.key, tok, expiresAt) // cache best-effort; never fail the call
	return tok, nil
}

// NewGraphAuthenticator wires a certificate-backed MSAL confidential client for
// the Microsoft Graph .default scope, caching tokens at cachePath.
func NewGraphAuthenticator(tenantID, clientID, certPath, cachePath string) (*Authenticator, error) {
	cred, err := LoadCredential(certPath)
	if err != nil {
		return nil, err
	}
	authority := "https://login.microsoftonline.com/" + tenantID
	client, err := confidential.New(authority, clientID, cred, confidential.WithX5C())
	if err != nil {
		return nil, fmt.Errorf("init confidential client: %w", err)
	}
	scopes := []string{GraphDefaultScope}

	return &Authenticator{
		key:   tenantID + "|" + GraphDefaultScope,
		cache: NewFileCache(cachePath),
		fetch: func(ctx context.Context) (string, time.Time, error) {
			res, err := client.AcquireTokenByCredential(ctx, scopes)
			if err != nil {
				return "", time.Time{}, fmt.Errorf("acquire token: %w", err)
			}
			return res.AccessToken, res.ExpiresOn, nil
		},
	}, nil
}
