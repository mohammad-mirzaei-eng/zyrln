package mitm

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func generateTestCA(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	certPath = filepath.Join(dir, "ca.pem")
	keyPath = filepath.Join(dir, "ca-key.pem")
	if err := GenerateCA(certPath, keyPath); err != nil {
		t.Fatalf("GenerateCA failed: %v", err)
	}
	return certPath, keyPath
}

func TestGenerateCA_CreatesFiles(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %s to exist: %v", p, err)
		}
	}
}

func TestGenerateCA_ValidPEM(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	certPEM, _ := os.ReadFile(certPath)
	keyPEM, _ := os.ReadFile(keyPath)
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Errorf("generated cert/key pair is invalid: %v", err)
	}
}

func TestGenerateCA_IsCA(t *testing.T) {
	certPath, _ := generateTestCA(t)
	certPEM, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if !cert.IsCA {
		t.Error("expected IsCA = true")
	}
	if !cert.BasicConstraintsValid {
		t.Error("expected BasicConstraintsValid = true")
	}
}

func TestGenerateCA_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "subdir", "ca.pem")
	keyPath := filepath.Join(dir, "subdir", "ca-key.pem")
	if err := GenerateCA(certPath, keyPath); err != nil {
		t.Fatalf("GenerateCA failed: %v", err)
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("cert not created: %v", err)
	}
}

func TestLoadCA_RoundTrip(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	ca, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA failed: %v", err)
	}
	if ca == nil {
		t.Fatal("LoadCA returned nil")
	}
}

func TestLoadCA_MissingCert(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadCA(filepath.Join(dir, "no.pem"), filepath.Join(dir, "no-key.pem"))
	if err == nil {
		t.Error("expected error for missing cert file")
	}
}

func TestLoadCA_InvalidCertPEM(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "bad.pem")
	keyPath := filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, []byte("not a pem"), 0644)
	os.WriteFile(keyPath, []byte("not a pem"), 0644)
	_, err := LoadCA(certPath, keyPath)
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}

func TestCertForHost_ReturnsValidCert(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	ca, _ := LoadCA(certPath, keyPath)

	cert, err := ca.CertForHost("example.com")
	if err != nil {
		t.Fatalf("CertForHost failed: %v", err)
	}

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject.CommonName != "example.com" {
		t.Errorf("CommonName = %q, want example.com", parsed.Subject.CommonName)
	}
}

func TestCertForHost_CachesResult(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	ca, _ := LoadCA(certPath, keyPath)

	cert1, _ := ca.CertForHost("cache-test.com")
	cert2, _ := ca.CertForHost("cache-test.com")
	if cert1 != cert2 {
		t.Error("expected same pointer for cached host cert")
	}
}

func TestCertForHost_DifferentHosts(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	ca, _ := LoadCA(certPath, keyPath)

	cert1, _ := ca.CertForHost("host-a.com")
	cert2, _ := ca.CertForHost("host-b.com")
	if cert1 == cert2 {
		t.Error("expected different certs for different hosts")
	}
}

func TestCertForHost_IPAddress(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	ca, _ := LoadCA(certPath, keyPath)

	cert, err := ca.CertForHost("127.0.0.1")
	if err != nil {
		t.Fatalf("CertForHost for IP failed: %v", err)
	}

	parsed, _ := x509.ParseCertificate(cert.Certificate[0])
	if len(parsed.IPAddresses) == 0 {
		t.Error("expected IP SANs for IP host")
	}
	if len(parsed.DNSNames) != 0 {
		t.Error("expected no DNS SANs for IP host")
	}
}

func TestLoadCA_RSABackwardCompat(t *testing.T) {
	// Generate a legacy RSA CA key pair and verify LoadCA accepts it.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-rsa-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &rsaKey.PublicKey, rsaKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	certFile, _ := os.Create(certPath)
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	keyFile, _ := os.Create(keyPath)
	pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})
	keyFile.Close()

	ca, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA with RSA key failed: %v", err)
	}
	if ca == nil {
		t.Fatal("LoadCA returned nil")
	}
	// Verify it can still sign leaf certs.
	if _, err := ca.CertForHost("rsa-test.com"); err != nil {
		t.Errorf("CertForHost with RSA CA failed: %v", err)
	}
}

func TestCertForHost_SignedByCA(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	ca, _ := LoadCA(certPath, keyPath)

	leafCert, _ := ca.CertForHost("signed-check.com")
	parsed, _ := x509.ParseCertificate(leafCert.Certificate[0])

	pool := x509.NewCertPool()
	caCertPEM, _ := os.ReadFile(certPath)
	pool.AppendCertsFromPEM(caCertPEM)

	if _, err := parsed.Verify(x509.VerifyOptions{
		DNSName: "signed-check.com",
		Roots:   pool,
	}); err != nil {
		t.Errorf("leaf cert not trusted by CA: %v", err)
	}
}

func TestGetCertificate(t *testing.T) {
	certPath, keyPath := generateTestCA(t)
	ca, _ := LoadCA(certPath, keyPath)

	cert := ca.GetCertificate()
	if cert == nil {
		t.Fatal("GetCertificate returned nil")
	}
	if cert.SerialNumber == nil {
		t.Error("GetCertificate returned certificate with no serial number")
	}
}
