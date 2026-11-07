package etcqlite

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/config"
)

func TestVerifiedMTLSAndCertificateIdentity(t *testing.T) {
	now := time.Now()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-root"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	makeCertificate := func(serial int64, name string, usage x509.ExtKeyUsage) ([]byte, []byte, tls.Certificate) {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature,
			DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, root, &key.PublicKey, rootKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		privateKey := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		pair, err := tls.X509KeyPair(certificate, privateKey)
		if err != nil {
			t.Fatal(err)
		}
		return certificate, privateKey, pair
	}
	_, _, serverPair := makeCertificate(2, "synthetic-server", x509.ExtKeyUsageServerAuth)
	clientCertificate, clientKey, _ := makeCertificate(3, "synthetic-inspector", x509.ExtKeyUsageClientAuth)
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(rootPEM)
	producer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "synthetic-inspector" {
			t.Error("verified client certificate identity missing")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Etcqlite-User") != "" {
			t.Error("certificate authentication was replaced with caller headers")
		}
		w.Header().Set("ETag", discoveryFixture().ETag)
		json.NewEncoder(w).Encode(discoveryFixture())
	}))
	producer.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverPair},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots,
	}
	producer.StartTLS()
	defer producer.Close()
	dir := t.TempDir()
	cfg := config.EtcqliteConfig{
		Endpoint: producer.URL, TLSCAFile: filepath.Join(dir, "ca.pem"),
		TLSCertFile: filepath.Join(dir, "client.pem"), TLSKeyFile: filepath.Join(dir, "client.key"),
	}
	for path, data := range map[string][]byte{cfg.TLSCAFile: rootPEM, cfg.TLSCertFile: clientCertificate, cfg.TLSKeyFile: clientKey} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Discover(context.Background()); err != nil {
		t.Fatalf("verified mTLS failed: %v", err)
	}
	client.Close()
	untrusted := cfg
	untrusted.TLSCAFile = ""
	client, err = NewClient(untrusted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Discover(context.Background()); err == nil {
		t.Fatal("untrusted server certificate was accepted")
	}
	client.Close()
	tokenOnly := config.EtcqliteConfig{Endpoint: producer.URL, TLSCAFile: cfg.TLSCAFile, AuthToken: "synthetic-token"}
	client, err = NewClient(tokenOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Discover(context.Background()); err == nil {
		t.Fatal("mTLS service accepted a token without a client certificate")
	}
	client.Close()
	if err := os.Chmod(cfg.TLSKeyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(cfg); err == nil {
		t.Fatal("world-readable client key was accepted")
	}
}
