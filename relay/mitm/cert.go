package mitm

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CertAuthority is a local CA used for HTTPS MITM.
type CertAuthority struct {
	cert    *x509.Certificate
	key     crypto.Signer // *ecdsa.PrivateKey (new) or *rsa.PrivateKey (legacy)
	tlsCert tls.Certificate
	cache   map[string]*tls.Certificate
	mu      sync.Mutex
}

// GenerateCA writes a new local CA cert and key PEM to the given paths.
func GenerateCA(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Zyrln Local CA",
			Organization: []string{"Zyrln"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(3, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}

	for _, path := range []string{certPath, keyPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = certFile.Close()
		return err
	}
	if err := certFile.Close(); err != nil {
		return err
	}

	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		_ = keyFile.Close()
		return err
	}
	return keyFile.Close()
}

// LoadCA parses a CA cert + key PEM pair from disk.
func LoadCA(certPath, keyPath string) (*CertAuthority, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("invalid CA key PEM")
	}
	var key crypto.Signer
	switch keyBlock.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, err
		}
	case "RSA PRIVATE KEY":
		rsaKey, rsaErr := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if rsaErr != nil {
			return nil, rsaErr
		}
		key = rsaKey
	default:
		return nil, fmt.Errorf("unsupported CA key type: %s", keyBlock.Type)
	}

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	return &CertAuthority{
		cert:    cert,
		key:     key,
		tlsCert: tlsCert,
		cache:   map[string]*tls.Certificate{},
	}, nil
}

// GetCertificate returns the underlying X.509 certificate.
func (ca *CertAuthority) GetCertificate() *x509.Certificate {
	return ca.cert
}

// CertForHost returns (or generates) a leaf TLS cert signed by the CA for host.
func (ca *CertAuthority) CertForHost(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	if cert, ok := ca.cache[host]; ok {
		return cert, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 3, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
		template.DNSNames = nil
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, key.Public(), ca.key)
	if err != nil {
		return nil, err
	}

	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	ca.cache[host] = &tlsCert
	return &tlsCert, nil
}
