package trust

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSSHHostKeyTrust(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pub := mustSSHKey(t)
	hostport := "example.com:22"

	// First check: untrusted, and pending should be stashed.
	trusted, fp := st.CheckSSH(hostport, pub)
	if trusted {
		t.Fatal("new host key must not be trusted by default")
	}
	if fp == "" {
		t.Fatal("fingerprint should be returned for the prompt")
	}

	// Trusting a wrong fingerprint must fail.
	if err := st.TrustPending(hostport, "SHA256:wrong"); err == nil {
		t.Fatal("trusting a non-matching fingerprint must fail")
	}

	// Trust the presented fingerprint.
	if err := st.TrustPending(hostport, fp); err != nil {
		t.Fatal(err)
	}
	trusted, _ = st.CheckSSH(hostport, pub)
	if !trusted {
		t.Fatal("host key should be trusted after explicit decision")
	}

	// A different key for the same host must NOT be trusted.
	other := mustSSHKey(t)
	trusted, _ = st.CheckSSH(hostport, other)
	if trusted {
		t.Fatal("changed host key must not match the stored fingerprint")
	}

	// Persistence: reopen the store from the same data directory.
	dir := filepath.Dir(st.path)
	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	trusted, _ = st2.CheckSSH(hostport, pub)
	if !trusted {
		t.Fatal("trust decision must persist across restarts")
	}
}

func TestCertPinTrust(t *testing.T) {
	st, _ := Open(t.TempDir())
	cert := mustCert(t)
	hostport := "ftp.example.com:21"

	trusted, fp := st.CheckCert(hostport, cert)
	if trusted {
		t.Fatal("unknown certificate must not be trusted silently")
	}
	if err := st.TrustPending(hostport, fp); err != nil {
		t.Fatal(err)
	}
	trusted, _ = st.CheckCert(hostport, cert)
	if !trusted {
		t.Fatal("certificate should be pinned after explicit trust")
	}
}

func TestListAndRemove(t *testing.T) {
	st, _ := Open(t.TempDir())
	pub := mustSSHKey(t)
	_, fp := st.CheckSSH("h:22", pub)
	if err := st.TrustPending("h:22", fp); err != nil {
		t.Fatal(err)
	}
	if len(st.List()) != 1 {
		t.Fatal("expected 1 trust entry")
	}
	if err := st.Remove("h:22"); err != nil {
		t.Fatal(err)
	}
	if len(st.List()) != 0 {
		t.Fatal("expected 0 entries after remove")
	}
}

func mustSSHKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func mustCert(t *testing.T) *x509.Certificate {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ftp.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"ftp.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	_ = tls.VersionTLS12
	return cert
}
