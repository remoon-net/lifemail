package settings

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"remoon.net/lifemail/internal/store"
)

func newTestStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func writeTestCert(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "example.com"},
		DNSNames:              []string{"example.com"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestPerDomainTLS(t *testing.T) {
	s, db := newTestStore(t)
	certPath, keyPath := writeTestCert(t, t.TempDir())

	domains := []Domain{{Name: "Example.com", TLSCert: certPath, TLSKey: keyPath}}
	cfg, err := s.Update(Patch{Domains: &domains})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Domains) != 1 || cfg.Domains[0].Name != "example.com" ||
		cfg.Domains[0].TLSCert != certPath || cfg.Domains[0].TLSKey != keyPath {
		t.Fatalf("domains = %#v", cfg.Domains)
	}
	if s.TLSConfig() == nil {
		t.Fatal("TLS should be enabled")
	}
	cert, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
	if err != nil || cert == nil {
		t.Fatalf("GetCertificate = %v, %v", cert, err)
	}

	// Certificates survive a reload from the database.
	reloaded, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TLSConfig() == nil {
		t.Fatal("TLS should be enabled after reload")
	}
	if _, err := reloaded.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"}); err != nil {
		t.Fatalf("certificate not reloaded: %v", err)
	}

	// Invalid certificate paths are rejected before they reach the database.
	bad := []Domain{{Name: "bad.example", TLSCert: "/does/not/exist.pem", TLSKey: "/does/not/exist.key"}}
	if _, err := s.Update(Patch{Domains: &bad}); err == nil {
		t.Fatal("expected invalid certificate error")
	}
	// Half-configured TLS is rejected too.
	half := []Domain{{Name: "half.example", TLSCert: certPath}}
	if _, err := s.Update(Patch{Domains: &half}); err == nil {
		t.Fatal("expected missing tls_key error")
	}
}

func TestLegacyDomainList(t *testing.T) {
	s, db := newTestStore(t)
	if err := db.SetSetting(KeyDomains, "Example.com, example.org"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	names := s.Snapshot().DomainNames()
	if len(names) != 2 || names[0] != "example.com" || names[1] != "example.org" {
		t.Fatalf("names = %#v", names)
	}
}
