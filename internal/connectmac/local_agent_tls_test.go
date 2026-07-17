package connectmac

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"testing"
	"time"
)

var localAgentTLSTestNow = time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)

func TestLocalAgentTLSGeneratesValidMaterial(t *testing.T) {
	home := t.TempDir()
	material, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first generation must report changed")
	}

	assertLocalAgentTLSModes(t, material)
	assertLocalAgentTLSCertificate(t, material, localAgentTLSTestNow)
	if _, err := loadLocalAgentTLS(home, localAgentTLSTestNow); err != nil {
		t.Fatalf("loadLocalAgentTLS: %v", err)
	}
	fingerprint, err := localAgentCAFingerprint(material.CACertPath)
	if err != nil {
		t.Fatalf("localAgentCAFingerprint: %v", err)
	}
	if len(fingerprint) != 40 {
		t.Fatalf("CA fingerprint length = %d, want SHA-1 hex", len(fingerprint))
	}
}

func TestLocalAgentTLSReusesValidMaterial(t *testing.T) {
	home := t.TempDir()
	first, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first generation must report changed")
	}
	before := readLocalAgentTLSFiles(t, first)

	second, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("valid material must be reused")
	}
	after := readLocalAgentTLSFiles(t, second)
	for path, want := range before {
		if got := after[path]; !bytes.Equal(got, want) {
			t.Fatalf("%s changed during reuse", path)
		}
	}
}

func TestLocalAgentTLSRenewsExpiringServerWithoutReplacingCA(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	caBefore, err := os.ReadFile(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	caKeyBefore, err := os.ReadFile(material.CAKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	serverBefore, err := os.ReadFile(material.ServerCertPath)
	if err != nil {
		t.Fatal(err)
	}

	_, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow.AddDate(0, 11, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("server with 30 days remaining must renew")
	}
	caAfter, err := os.ReadFile(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	caKeyAfter, err := os.ReadFile(material.CAKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	serverAfter, err := os.ReadFile(material.ServerCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(caAfter, caBefore) || !bytes.Equal(caKeyAfter, caKeyBefore) {
		t.Fatal("renewing the server certificate replaced a valid CA")
	}
	if bytes.Equal(serverAfter, serverBefore) {
		t.Fatal("server certificate was not renewed")
	}
	assertLocalAgentTLSCertificate(t, material, localAgentTLSTestNow.AddDate(0, 11, 0))
}

func TestLocalAgentTLSRepairsCorruptOrPartialMaterial(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, material localAgentTLSMaterial)
	}{
		{
			name: "corrupt server certificate",
			prepare: func(t *testing.T, material localAgentTLSMaterial) {
				t.Helper()
				if err := os.WriteFile(material.ServerCertPath, []byte("not a certificate"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing server key",
			prepare: func(t *testing.T, material localAgentTLSMaterial) {
				t.Helper()
				if err := os.Remove(material.ServerKeyPath); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
			if err != nil {
				t.Fatal(err)
			}
			caBefore, err := os.ReadFile(material.CACertPath)
			if err != nil {
				t.Fatal(err)
			}
			tt.prepare(t, material)

			_, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Fatal("invalid material must be repaired")
			}
			caAfter, err := os.ReadFile(material.CACertPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(caAfter, caBefore) {
				t.Fatal("repairing invalid server material replaced a valid CA")
			}
			assertLocalAgentTLSCertificate(t, material, localAgentTLSTestNow)
		})
	}
}

func TestLocalAgentTLSReplacesCorruptCA(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(material.CACertPath, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("corrupt CA material must be replaced")
	}
	assertLocalAgentTLSCertificate(t, material, localAgentTLSTestNow)
}

func TestLocalAgentTLSLoadRejectsExpiredServerCertificate(t *testing.T) {
	home := t.TempDir()
	_, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadLocalAgentTLS(home, localAgentTLSTestNow.AddDate(1, 0, 0)); err == nil {
		t.Fatal("loadLocalAgentTLS accepted an expired server certificate")
	}
}

func assertLocalAgentTLSModes(t *testing.T, material localAgentTLSMaterial) {
	t.Helper()
	assertLocalAgentTLSMode(t, material.Dir, 0o700)
	assertLocalAgentTLSMode(t, material.CAKeyPath, 0o600)
	assertLocalAgentTLSMode(t, material.ServerKeyPath, 0o600)
	assertLocalAgentTLSMode(t, material.CACertPath, 0o644)
	assertLocalAgentTLSMode(t, material.ServerCertPath, 0o644)
}

func assertLocalAgentTLSMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func assertLocalAgentTLSCertificate(t *testing.T, material localAgentTLSMaterial, now time.Time) {
	t.Helper()
	leaf := parseLocalAgentTLSCertificate(t, material.ServerCertPath)
	if len(leaf.DNSNames) == 0 || leaf.DNSNames[0] != "localhost" {
		t.Fatalf("DNSNames = %#v", leaf.DNSNames)
	}
	for _, want := range []string{"127.0.0.1", "::1"} {
		if !localAgentTLSCertificateHasIP(leaf, want) {
			t.Fatalf("missing IP SAN %s", want)
		}
	}
	if !hasLocalAgentTLSUsage(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Fatalf("server EKU = %#v, want server auth", leaf.ExtKeyUsage)
	}
	if leaf.NotAfter.Sub(now) < 364*24*time.Hour {
		t.Fatalf("server validity = %s, want about one year", leaf.NotAfter.Sub(now))
	}
	ca := parseLocalAgentTLSCertificate(t, material.CACertPath)
	if !ca.IsCA || ca.NotAfter.Sub(now) < 9*365*24*time.Hour {
		t.Fatalf("CA certificate is not a long-lived CA: IsCA=%v NotAfter=%s", ca.IsCA, ca.NotAfter)
	}
}

func parseLocalAgentTLSCertificate(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatalf("%s does not contain exactly one certificate", path)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func localAgentTLSCertificateHasIP(certificate *x509.Certificate, want string) bool {
	wantIP := net.ParseIP(want)
	for _, ip := range certificate.IPAddresses {
		if ip.Equal(wantIP) {
			return true
		}
	}
	return false
}

func hasLocalAgentTLSUsage(usages []x509.ExtKeyUsage, want x509.ExtKeyUsage) bool {
	for _, usage := range usages {
		if usage == want {
			return true
		}
	}
	return false
}

func readLocalAgentTLSFiles(t *testing.T, material localAgentTLSMaterial) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	for _, path := range []string{material.CACertPath, material.CAKeyPath, material.ServerCertPath, material.ServerKeyPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = data
	}
	return files
}
