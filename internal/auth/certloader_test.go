package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestCertPEM generates a self-signed RSA cert + PKCS8 key and writes them,
// concatenated, to a temp .pem file (the format CertFromPEM expects).
func writeTestCertPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "m365cli-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	var buf []byte
	buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)

	p := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(p, buf, 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}
	return p
}

func TestLoadCredentialFromValidPEM(t *testing.T) {
	if _, err := LoadCredential(writeTestCertPEM(t)); err != nil {
		t.Fatalf("LoadCredential: unexpected error for valid PEM: %v", err)
	}
}

func TestLoadCredentialMissingFile(t *testing.T) {
	if _, err := LoadCredential("/no/such/cert.pem"); err == nil {
		t.Fatal("LoadCredential: expected error for missing file")
	}
}

func TestLoadCredentialRejectsGarbage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(p, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredential(p); err == nil {
		t.Fatal("LoadCredential: expected error for non-PEM content")
	}
}
