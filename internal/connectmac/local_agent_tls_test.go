package connectmac

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

func TestLocalAgentTLSCAConstraintsRejectArbitraryNamesAndIntermediates(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	ca := parseLocalAgentTLSCertificate(t, material.CACertPath)
	if !ca.MaxPathLenZero || !ca.PermittedDNSDomainsCritical || !equalStrings(ca.PermittedDNSDomains, []string{"localhost"}) {
		t.Fatalf("unexpected CA constraints: MaxPathLenZero=%v critical=%v DNS=%#v", ca.MaxPathLenZero, ca.PermittedDNSDomainsCritical, ca.PermittedDNSDomains)
	}
	if !hasLocalAgentTLSIPConstraint(ca.PermittedIPRanges, "127.0.0.1/32") || !hasLocalAgentTLSIPConstraint(ca.PermittedIPRanges, "::1/128") || len(ca.PermittedIPRanges) != 2 {
		t.Fatalf("unexpected CA IP constraints: %#v", ca.PermittedIPRanges)
	}

	caKey, err := readLocalAgentTLSECDSAKey(material.CAKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	arbitraryLeaf, arbitraryKey := createLocalAgentTLSTestCertificate(t, localAgentTLSTestNow, ca, caKey, false, "example.com")
	assertLocalAgentTLSVerificationFails(t, arbitraryLeaf, ca, nil, localAgentTLSTestNow, "example.com")

	intermediate, intermediateKey := createLocalAgentTLSTestCertificate(t, localAgentTLSTestNow, ca, caKey, true, "")
	leaf, _ := createLocalAgentTLSTestCertificate(t, localAgentTLSTestNow, intermediate, intermediateKey, false, "localhost")
	pool := x509.NewCertPool()
	pool.AddCert(intermediate)
	assertLocalAgentTLSVerificationFails(t, leaf, ca, pool, localAgentTLSTestNow, "localhost")
	_ = arbitraryKey
}

func TestLocalAgentTLSRejectsClientAuthOnlyStagedCA(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	before := readLocalAgentTLSFiles(t, material)
	fingerprintBefore, err := localAgentCAFingerprint(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	localAgentTLSStageHook = func(staged localAgentTLSMaterial) error {
		ca := createLocalAgentTLSClientAuthCA(t, localAgentTLSTestNow.AddDate(0, 11, 0))
		serverCertificate, serverKey, err := generateLocalAgentTLSServer(ca, localAgentTLSTestNow.AddDate(0, 11, 0))
		if err != nil {
			return err
		}
		caCertificatePEM, caKeyPEM, err := encodeLocalAgentTLSCA(ca)
		if err != nil {
			return err
		}
		serverCertificatePEM, serverKeyPEM, err := encodeLocalAgentTLSServer(serverCertificate, serverKey)
		if err != nil {
			return err
		}
		return writeLocalAgentTLSFiles(staged, caCertificatePEM, caKeyPEM, serverCertificatePEM, serverKeyPEM)
	}
	t.Cleanup(func() { localAgentTLSStageHook = nil })
	if _, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow.AddDate(0, 11, 0)); err == nil {
		t.Fatal("ensure accepted a staged ClientAuth-only CA")
	}
	localAgentTLSStageHook = nil
	after := readLocalAgentTLSFiles(t, material)
	for path, want := range before {
		if got := after[path]; !bytes.Equal(got, want) {
			t.Fatalf("rejected staged CA changed %s", path)
		}
	}
	fingerprintAfter, err := localAgentCAFingerprint(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprintAfter != fingerprintBefore {
		t.Fatalf("rejected staged CA changed fingerprint: before=%s after=%s", fingerprintBefore, fingerprintAfter)
	}
}

func TestLocalAgentTLSRollsBackFailedDirectoryReplacement(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	before := readLocalAgentTLSFiles(t, material)
	localAgentTLSTransactionHook = func(stage string) error {
		if stage == "after-backup" {
			return errors.New("injected replacement failure")
		}
		return nil
	}
	t.Cleanup(func() { localAgentTLSTransactionHook = nil })
	if _, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow.AddDate(0, 11, 0)); err == nil {
		t.Fatal("expected injected directory replacement failure")
	}
	localAgentTLSTransactionHook = nil
	after := readLocalAgentTLSFiles(t, material)
	for path, want := range before {
		if got := after[path]; !bytes.Equal(got, want) {
			t.Fatalf("rollback changed %s", path)
		}
	}
	if _, err := loadLocalAgentTLS(home, localAgentTLSTestNow); err != nil {
		t.Fatalf("rolled back material is invalid: %v", err)
	}
}

func TestLocalAgentTLSRecoversValidBackup(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	backup := material.Dir + ".backup"
	if err := os.Rename(material.Dir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(material.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(material.ServerCertPath, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("recovering the valid backup must report changed")
	}
	if _, err := loadLocalAgentTLS(home, localAgentTLSTestNow); err != nil {
		t.Fatalf("recovered backup is invalid: %v", err)
	}
}

func TestLocalAgentTLSLoadRecoversValidBackup(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	backup := material.Dir + ".backup"
	if err := os.Rename(material.Dir, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLocalAgentTLS(home, localAgentTLSTestNow); err != nil {
		t.Fatalf("loadLocalAgentTLS did not recover a valid backup: %v", err)
	}
	if _, err := os.Stat(material.Dir); err != nil {
		t.Fatalf("active TLS directory was not restored: %v", err)
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("TLS backup was not consumed: %v", err)
	}
}

func TestLocalAgentTLSLoadWaitsForRenewalTransaction(t *testing.T) {
	home := t.TempDir()
	_, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	continueTransaction := make(chan struct{})
	localAgentTLSTransactionHook = func(stage string) error {
		if stage == "after-backup" {
			close(entered)
			<-continueTransaction
		}
		return nil
	}
	t.Cleanup(func() { localAgentTLSTransactionHook = nil })
	ensureDone := make(chan error, 1)
	go func() {
		_, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow.AddDate(0, 11, 0))
		ensureDone <- err
	}()
	<-entered
	loadDone := make(chan error, 1)
	go func() {
		_, err := loadLocalAgentTLS(home, localAgentTLSTestNow.AddDate(0, 11, 0))
		loadDone <- err
	}()
	select {
	case err := <-loadDone:
		t.Fatalf("load returned during an active replacement: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(continueTransaction)
	if err := <-ensureDone; err != nil {
		t.Fatal(err)
	}
	if err := <-loadDone; err != nil {
		t.Fatalf("load after renewal: %v", err)
	}
}

func TestLocalAgentTLSStagingDirectoryModeIgnoresUmask(t *testing.T) {
	home := t.TempDir()
	_, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	previousUmask := syscall.Umask(0o777)
	t.Cleanup(func() { syscall.Umask(previousUmask) })
	localAgentTLSStageHook = func(staged localAgentTLSMaterial) error {
		info, err := os.Stat(staged.Dir)
		if err != nil {
			return err
		}
		if got := info.Mode().Perm(); got != 0o700 {
			return fmt.Errorf("staging mode = %o, want 700", got)
		}
		return errors.New("stop after staging mode check")
	}
	t.Cleanup(func() { localAgentTLSStageHook = nil })
	if _, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow.AddDate(0, 11, 0)); err == nil {
		t.Fatal("expected staging hook to stop replacement")
	}
}

func TestLocalAgentTLSConcurrentEnsureProducesOneValidSet(t *testing.T) {
	home := t.TempDir()
	const callers = 8
	start := make(chan struct{})
	errs := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loadLocalAgentTLS(home, localAgentTLSTestNow); err != nil {
		t.Fatalf("concurrent ensure left invalid material: %v", err)
	}
}

func TestLocalAgentTLSRepairsUnreadableKeyBeforeReuse(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	before := readLocalAgentTLSFiles(t, material)
	fingerprintBefore, err := localAgentCAFingerprint(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(material.CAKeyPath, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(material.ServerKeyPath, 0o000); err != nil {
		t.Fatal(err)
	}
	_, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("mode repair must report changed")
	}
	assertLocalAgentTLSModes(t, material)
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("mode repair changed %s", path)
		}
	}
	fingerprintAfter, err := localAgentCAFingerprint(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprintAfter != fingerprintBefore {
		t.Fatalf("unreadable-key repair rotated CA: before=%s after=%s", fingerprintBefore, fingerprintAfter)
	}
}

func TestLocalAgentTLSPropagatesUnexpectedFilesystemErrors(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(material.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(material.Dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow); err == nil || changed {
		t.Fatalf("unexpected filesystem error must propagate without regeneration: changed=%v err=%v", changed, err)
	}
}

func TestLocalAgentTLSRejectsManagedPathSymlink(t *testing.T) {
	for _, name := range []string{"connectmac", "tls", "staging", "backup"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			target := t.TempDir()
			parent := filepath.Join(home, ".connectmac", "local-agent")
			path := filepath.Join(home, ".connectmac")
			if name != "connectmac" {
				if err := os.MkdirAll(parent, 0o700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(parent, "tls")
				if name == "staging" {
					path += ".staging"
				}
				if name == "backup" {
					path += ".backup"
				}
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			if _, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow); err == nil {
				t.Fatal("managed path symlink must be rejected")
			}
			if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
				t.Fatalf("symlink target was modified: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestLocalAgentTLSDoesNotReuseCAThatCannotIssueFullServerLifetime(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	caBefore, err := os.ReadFile(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	nearExpiry := localAgentTLSTestNow.AddDate(9, 0, 0)
	_, changed, err := ensureLocalAgentTLS(home, nearExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("CA without a full server lifetime remaining must rotate")
	}
	caAfter, err := os.ReadFile(material.CACertPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(caAfter, caBefore) {
		t.Fatal("near-expiry CA was reused")
	}
	leaf := parseLocalAgentTLSCertificate(t, material.ServerCertPath)
	ca := parseLocalAgentTLSCertificate(t, material.CACertPath)
	if leaf.NotAfter.After(ca.NotAfter) {
		t.Fatalf("leaf expires after issuer: leaf=%s CA=%s", leaf.NotAfter, ca.NotAfter)
	}
}

func TestLocalAgentTLSServerRenewalBoundary(t *testing.T) {
	home := t.TempDir()
	material, _, err := ensureLocalAgentTLS(home, localAgentTLSTestNow)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(material.ServerCertPath)
	if err != nil {
		t.Fatal(err)
	}
	_, changed, err := ensureLocalAgentTLS(home, localAgentTLSTestNow.AddDate(0, 11, -1))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("server with more than 30 days remaining must be reused")
	}
	after, err := os.ReadFile(material.ServerCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("server renewed before the 30-day boundary")
	}
}

func TestLocalAgentTLSPKCS8ParseErrorIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.pem")
	der := []byte{1, 2, 3}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	_, want := x509.ParsePKCS8PrivateKey(der)
	_, err := readLocalAgentTLSECDSAKey(path)
	if err == nil || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("key parse error = %v, want PKCS#8 error %v", err, want)
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

func hasLocalAgentTLSIPConstraint(ranges []*net.IPNet, want string) bool {
	for _, ipRange := range ranges {
		if ipRange.String() == want {
			return true
		}
	}
	return false
}

func equalStrings(got, want []string) bool {
	return len(got) == len(want) && (len(got) == 0 || got[0] == want[0])
}

func createLocalAgentTLSTestCertificate(t *testing.T, now time.Time, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, isCA bool, dnsName string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(0, 1, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if isCA {
		template.IsCA = true
		template.BasicConstraintsValid = true
		template.KeyUsage |= x509.KeyUsageCertSign
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{dnsName}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func assertLocalAgentTLSVerificationFails(t *testing.T, certificate, ca *x509.Certificate, intermediates *x509.CertPool, now time.Time, dnsName string) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		DNSName:       dnsName,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Fatal("certificate verification unexpectedly succeeded")
	}
}

func createLocalAgentTLSClientAuthCA(t *testing.T, now time.Time) localAgentTLSCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, ipv4Range, err := net.ParseCIDR("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	_, ipv6Range, err := net.ParseCIDR("::1/128")
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:                big.NewInt(2),
		Subject:                     pkix.Name{CommonName: "Client-only test CA"},
		NotBefore:                   now.Add(-time.Minute),
		NotAfter:                    now.AddDate(2, 0, 0),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:                 []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"localhost"},
		PermittedIPRanges:           []*net.IPNet{ipv4Range, ipv6Range},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return localAgentTLSCA{certificate: certificate, key: key}
}
