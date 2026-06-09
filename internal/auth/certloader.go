// Package auth implements app-only (client-credentials) authentication against
// Microsoft Entra ID using a certificate assertion, plus a 0600 on-disk token
// cache keyed by (tenant, scope). There is no user login or consent flow.
package auth

import (
	"fmt"
	"os"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/confidential"
)

// LoadCredential reads the certificate+key PEM at path and builds an MSAL
// credential for certificate (x5c) assertion. The PEM must contain both the
// public certificate and its private key.
func LoadCredential(path string) (confidential.Credential, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return confidential.Credential{}, fmt.Errorf("read certificate %s: %w", path, err)
	}
	certs, key, err := confidential.CertFromPEM(pemData, "")
	if err != nil {
		return confidential.Credential{}, fmt.Errorf("parse certificate %s: %w", path, err)
	}
	cred, err := confidential.NewCredFromCert(certs, key)
	if err != nil {
		return confidential.Credential{}, fmt.Errorf("build credential from %s: %w", path, err)
	}
	return cred, nil
}
