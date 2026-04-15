package upstream

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

func generateTestCAPEM(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test CA"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:         true,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
}

func writeTestCA(t *testing.T) string {
	t.Helper()
	caContent := generateTestCAPEM(t)
	tmpFile, err := os.CreateTemp(t.TempDir(), "ca-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpFile.WriteString(caContent); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	return tmpFile.Name()
}

func TestBuildTLSConfigInsecure(t *testing.T) {
	cfg, err := BuildTLSConfig(TLSConfig{
		VerifyTLS:  false,
		ServerName: "example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify true")
	}
	if cfg.ServerName != "example.com" {
		t.Errorf("expected ServerName example.com, got %s", cfg.ServerName)
	}
}

func TestBuildTLSConfigSecure(t *testing.T) {
	cfg, err := BuildTLSConfig(TLSConfig{
		VerifyTLS:  true,
		ServerName: "proxy.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify false")
	}
}

func TestBuildTLSConfigCustomCA(t *testing.T) {
	caPath := writeTestCA(t)

	cfg, err := BuildTLSConfig(TLSConfig{
		VerifyTLS: true,
		CustomCA:  caPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Error("expected custom RootCAs pool")
	}
}

func TestBuildTLSConfigCustomCANotFound(t *testing.T) {
	_, err := BuildTLSConfig(TLSConfig{
		VerifyTLS: true,
		CustomCA:  "/nonexistent/ca.pem",
	})
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestBuildTLSConfigCustomCAInvalid(t *testing.T) {
	tmpFile, _ := os.CreateTemp(t.TempDir(), "bad-ca-*.pem")
	tmpFile.WriteString("not a valid PEM")
	tmpFile.Close()

	_, err := BuildTLSConfig(TLSConfig{
		VerifyTLS: true,
		CustomCA:  tmpFile.Name(),
	})
	if err == nil {
		t.Fatal("expected error for invalid CA file")
	}
}

func TestBuildTLSConfigCustomCAPool(t *testing.T) {
	caPath := writeTestCA(t)

	cfg, err := BuildTLSConfig(TLSConfig{
		VerifyTLS: true,
		CustomCA:  caPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected non-nil RootCAs pool from custom CA")
	}
}
