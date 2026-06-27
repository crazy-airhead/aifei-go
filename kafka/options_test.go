package kafka

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildSASLMechanism(t *testing.T) {
	for _, m := range []string{"", "plain", "PLAIN", "scram-sha-256", "scram-sha256", "scram256", "scram-sha-512", "scram512"} {
		mech, err := buildSASLMechanism(&SASLConfig{Mechanism: m, User: "u", Password: "p"})
		if err != nil || mech == nil {
			t.Errorf("mechanism %q: want ok, got err=%v mech=%v", m, err, mech)
		}
	}
	if _, err := buildSASLMechanism(&SASLConfig{Mechanism: "oauth"}); err == nil {
		t.Error("want error for unsupported mechanism")
	}
}

func TestBuildProducerOptsDefaults(t *testing.T) {
	// nil config should not panic and should yield the default opts.
	opts := buildProducerOpts(nil)
	if len(opts) < 2 {
		t.Fatalf("want at least default producer opts, got %d", len(opts))
	}
	// Explicit linger/maxAttempts are applied.
	opts = buildProducerOpts(&ProducerConfig{LingerMs: 10, MaxAttempts: 5})
	if len(opts) != 4 {
		t.Fatalf("want 4 opts (acks, compression, linger, retries), got %d", len(opts))
	}
}

func TestBuildConsumerOptsAutoCommitMarks(t *testing.T) {
	// Default autocommit (nil) enables AutoCommitMarks for at-least-once.
	opts := buildConsumerOpts(&ConsumerConfig{GroupID: "g"}, []string{"t"})
	if len(opts) < 6 {
		t.Fatalf("want >=6 consumer opts, got %d", len(opts))
	}
	// Explicit disableAutoCommit path still builds.
	opts = buildConsumerOpts(&ConsumerConfig{GroupID: "g", AutoCommit: &AutoCommitConfig{Enable: false}}, []string{"t"})
	if len(opts) == 0 {
		t.Fatal("want consumer opts")
	}
}

func TestBuildTLSConfigErrors(t *testing.T) {
	// mTLS requires both cert and key.
	if _, err := buildTLSConfig(&TLSConfig{Enabled: true, CertFile: "x"}); err == nil {
		t.Error("want error when only certFile set")
	}
	// Nonexistent CA file.
	if _, err := buildTLSConfig(&TLSConfig{Enabled: true, CAFile: "/no/such/ca.pem"}); err == nil {
		t.Error("want error for missing ca file")
	}
}

func TestBuildTLSConfigHappy(t *testing.T) {
	certPath, keyPath := selfSignedCert(t)

	// CA + mTLS keypair, parsed from real files.
	cfg, err := buildTLSConfig(&TLSConfig{
		Enabled:            true,
		CAFile:             certPath,
		CertFile:           certPath,
		KeyFile:            keyPath,
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs not set from ca file")
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("want 1 client cert, got %d", len(cfg.Certificates))
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify not honored")
	}
}

// selfSignedCert writes a self-signed EC certificate and its key to temp files
// and returns their paths. The cert is a CA, so it also serves as the CA file.
func selfSignedCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}
