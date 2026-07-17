package connectmac

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const localAgentServerRenewBefore = 30 * 24 * time.Hour

var (
	errInvalidLocalAgentTLS = errors.New("invalid local-agent TLS material")

	// localAgentTLSTransactionHook makes transaction rollback deterministic in tests.
	localAgentTLSTransactionHook func(string) error

	// localAgentTLSStageHook permits deterministic staged-set validation tests.
	localAgentTLSStageHook func(localAgentTLSMaterial) error
)

type localAgentTLSMaterial struct {
	Dir            string
	CACertPath     string
	CAKeyPath      string
	ServerCertPath string
	ServerKeyPath  string
}

type localAgentTLSCA struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
}

func localAgentTLSPaths(home string) localAgentTLSMaterial {
	return localAgentTLSPathsForDir(filepath.Join(home, ".connectmac", "local-agent", "tls"))
}

func localAgentTLSPathsForDir(dir string) localAgentTLSMaterial {
	return localAgentTLSMaterial{
		Dir:            dir,
		CACertPath:     filepath.Join(dir, "ca.pem"),
		CAKeyPath:      filepath.Join(dir, "ca-key.pem"),
		ServerCertPath: filepath.Join(dir, "server.pem"),
		ServerKeyPath:  filepath.Join(dir, "server-key.pem"),
	}
}

// ensureLocalAgentTLS creates or repairs localhost TLS material. A replacement
// is assembled in a sibling directory, then swapped as one complete set.
func ensureLocalAgentTLS(home string, now time.Time) (localAgentTLSMaterial, bool, error) {
	material := localAgentTLSPaths(home)
	parent, err := ensureLocalAgentTLSParent(home)
	if err != nil {
		return material, false, err
	}
	release, err := lockLocalAgentTLS(parent)
	if err != nil {
		return material, false, err
	}
	defer release()

	recovered, err := recoverLocalAgentTLS(material, now)
	if err != nil {
		return material, false, err
	}
	permissionsChanged, err := repairLocalAgentTLSPermissions(material)
	if err != nil {
		return material, false, err
	}
	changed := recovered || permissionsChanged

	ca, err := loadLocalAgentTLSCA(material, now)
	if err != nil || !localAgentTLSCAHasFullServerLifetime(ca, now) {
		if err != nil && !localAgentTLSRecoverableError(err) {
			return material, false, err
		}
		return replaceLocalAgentTLSWithNewCA(material, now)
	}

	serverCertificate, err := validateLocalAgentTLSServerCertificate(material, ca, now)
	if err == nil && serverCertificate.NotAfter.Sub(now) > localAgentServerRenewBefore {
		return material, changed, nil
	}
	if err != nil && !localAgentTLSRecoverableError(err) {
		return material, false, err
	}
	return replaceLocalAgentTLSServer(material, ca, now)
}

func loadLocalAgentTLS(home string, now time.Time) (localAgentTLSMaterial, error) {
	material := localAgentTLSPaths(home)
	parent, err := ensureLocalAgentTLSParent(home)
	if err != nil {
		return material, err
	}
	release, err := lockLocalAgentTLS(parent)
	if err != nil {
		return material, err
	}
	defer release()
	if _, err := recoverLocalAgentTLS(material, now); err != nil {
		return material, err
	}
	return loadLocalAgentTLSUnlocked(home, material, now)
}

func loadLocalAgentTLSUnlocked(home string, material localAgentTLSMaterial, now time.Time) (localAgentTLSMaterial, error) {
	if err := checkLocalAgentTLSReadPaths(home, material); err != nil {
		return material, err
	}
	if err := validateLocalAgentTLSSet(material, now); err != nil {
		return material, err
	}
	return material, nil
}

func validateLocalAgentTLSSet(material localAgentTLSMaterial, now time.Time) error {
	if err := checkLocalAgentTLSMaterialPaths(material); err != nil {
		return err
	}
	ca, err := loadLocalAgentTLSCA(material, now)
	if err != nil {
		return err
	}
	if _, err := validateLocalAgentTLSServerCertificate(material, ca, now); err != nil {
		return err
	}
	return nil
}

func localAgentCAFingerprint(path string) (string, error) {
	certificate, err := readLocalAgentTLSCertificate(path)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(certificate.Raw)
	return hex.EncodeToString(sum[:]), nil
}

func ensureLocalAgentTLSParent(home string) (string, error) {
	connectmacDir, err := ensureLocalAgentTLSDirectory(filepath.Join(home, ".connectmac"))
	if err != nil {
		return "", err
	}
	return ensureLocalAgentTLSDirectory(filepath.Join(connectmacDir, "local-agent"))
}

func ensureLocalAgentTLSDirectory(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create local-agent TLS directory %s: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return "", fmt.Errorf("inspect local-agent TLS directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refuse symlink in local-agent TLS path: %s", path)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("local-agent TLS path is not a directory: %s", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return "", fmt.Errorf("chmod local-agent TLS directory %s: %w", path, err)
	}
	return path, nil
}

func lockLocalAgentTLS(parent string) (func(), error) {
	path := filepath.Join(parent, "tls.lock")
	if err := checkLocalAgentTLSNonSymlink(path, false); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open local-agent TLS lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("chmod local-agent TLS lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock local-agent TLS material: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func recoverLocalAgentTLS(material localAgentTLSMaterial, now time.Time) (bool, error) {
	staging := material.Dir + ".staging"
	backup := material.Dir + ".backup"
	for _, path := range []string{material.Dir, staging, backup} {
		if err := checkLocalAgentTLSNonSymlink(path, true); err != nil {
			return false, err
		}
	}
	changed := false
	if exists, err := localAgentTLSPathExists(staging); err != nil {
		return false, err
	} else if exists {
		if err := os.RemoveAll(staging); err != nil {
			return false, fmt.Errorf("remove stale local-agent TLS staging directory: %w", err)
		}
		if err := syncDirectory(filepath.Dir(material.Dir)); err != nil {
			return false, fmt.Errorf("sync local-agent TLS parent directory: %w", err)
		}
		changed = true
	}
	backupExists, err := localAgentTLSPathExists(backup)
	if err != nil || !backupExists {
		return changed, err
	}
	backupValid, err := localAgentTLSMaterialUsable(localAgentTLSPathsForDir(backup), now)
	if err != nil {
		return false, err
	}
	activeValid, err := localAgentTLSMaterialUsable(material, now)
	if err != nil {
		return false, err
	}
	if !activeValid && backupValid {
		if exists, err := localAgentTLSPathExists(material.Dir); err != nil {
			return false, err
		} else if exists {
			if err := os.RemoveAll(material.Dir); err != nil {
				return false, fmt.Errorf("remove incomplete local-agent TLS directory: %w", err)
			}
		}
		if err := os.Rename(backup, material.Dir); err != nil {
			return false, fmt.Errorf("restore local-agent TLS backup: %w", err)
		}
		if err := syncDirectory(filepath.Dir(material.Dir)); err != nil {
			return false, fmt.Errorf("sync local-agent TLS parent directory: %w", err)
		}
		return true, nil
	}
	if err := os.RemoveAll(backup); err != nil {
		return false, fmt.Errorf("remove completed local-agent TLS backup: %w", err)
	}
	if err := syncDirectory(filepath.Dir(material.Dir)); err != nil {
		return false, fmt.Errorf("sync local-agent TLS parent directory: %w", err)
	}
	return true, nil
}

func localAgentTLSMaterialUsable(material localAgentTLSMaterial, now time.Time) (bool, error) {
	if exists, err := localAgentTLSPathExists(material.Dir); err != nil || !exists {
		return false, err
	}
	if err := checkLocalAgentTLSMaterialPaths(material); err != nil {
		return false, err
	}
	ca, err := loadLocalAgentTLSCA(material, now)
	if err != nil {
		if localAgentTLSRecoverableError(err) {
			return false, nil
		}
		return false, err
	}
	if _, err := validateLocalAgentTLSServerCertificate(material, ca, now); err != nil {
		if localAgentTLSRecoverableError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func repairLocalAgentTLSPermissions(material localAgentTLSMaterial) (bool, error) {
	exists, err := localAgentTLSPathExists(material.Dir)
	if err != nil || !exists {
		return false, err
	}
	if err := checkLocalAgentTLSNonSymlink(material.Dir, true); err != nil {
		return false, err
	}
	changed := false
	if changedDir, err := chmodLocalAgentTLSPath(material.Dir, 0o700, true); err != nil {
		return false, err
	} else {
		changed = changed || changedDir
	}
	for _, file := range []struct {
		path string
		mode os.FileMode
	}{
		{material.CACertPath, 0o644},
		{material.CAKeyPath, 0o600},
		{material.ServerCertPath, 0o644},
		{material.ServerKeyPath, 0o600},
	} {
		exists, err := localAgentTLSPathExists(file.path)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		if changedFile, err := chmodLocalAgentTLSPath(file.path, file.mode, false); err != nil {
			return false, err
		} else {
			changed = changed || changedFile
		}
	}
	return changed, nil
}

func chmodLocalAgentTLSPath(path string, mode os.FileMode, wantDirectory bool) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect local-agent TLS path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("refuse symlink in local-agent TLS path: %s", path)
	}
	if info.IsDir() != wantDirectory {
		return false, invalidLocalAgentTLSError("local-agent TLS path has unexpected type: %s", path)
	}
	if !wantDirectory && !info.Mode().IsRegular() {
		return false, invalidLocalAgentTLSError("local-agent TLS file is not regular: %s", path)
	}
	if info.Mode().Perm() == mode {
		return false, nil
	}
	if err := os.Chmod(path, mode); err != nil {
		return false, fmt.Errorf("chmod local-agent TLS path %s: %w", path, err)
	}
	return true, nil
}

func replaceLocalAgentTLSWithNewCA(material localAgentTLSMaterial, now time.Time) (localAgentTLSMaterial, bool, error) {
	ca, err := generateLocalAgentTLSCA(now)
	if err != nil {
		return material, false, err
	}
	serverCertificate, serverKey, err := generateLocalAgentTLSServer(ca, now)
	if err != nil {
		return material, false, err
	}
	caCertificatePEM, caKeyPEM, err := encodeLocalAgentTLSCA(ca)
	if err != nil {
		return material, false, err
	}
	serverCertificatePEM, serverKeyPEM, err := encodeLocalAgentTLSServer(serverCertificate, serverKey)
	if err != nil {
		return material, false, err
	}
	if err := replaceLocalAgentTLSMaterial(material, caCertificatePEM, caKeyPEM, serverCertificatePEM, serverKeyPEM, now); err != nil {
		return material, false, err
	}
	return material, true, nil
}

func replaceLocalAgentTLSServer(material localAgentTLSMaterial, ca localAgentTLSCA, now time.Time) (localAgentTLSMaterial, bool, error) {
	caCertificatePEM, err := os.ReadFile(material.CACertPath)
	if err != nil {
		return material, false, fmt.Errorf("read local-agent CA certificate for renewal: %w", err)
	}
	caKeyPEM, err := os.ReadFile(material.CAKeyPath)
	if err != nil {
		return material, false, fmt.Errorf("read local-agent CA private key for renewal: %w", err)
	}
	serverCertificate, serverKey, err := generateLocalAgentTLSServer(ca, now)
	if err != nil {
		return material, false, err
	}
	serverCertificatePEM, serverKeyPEM, err := encodeLocalAgentTLSServer(serverCertificate, serverKey)
	if err != nil {
		return material, false, err
	}
	if err := replaceLocalAgentTLSMaterial(material, caCertificatePEM, caKeyPEM, serverCertificatePEM, serverKeyPEM, now); err != nil {
		return material, false, err
	}
	return material, true, nil
}

func replaceLocalAgentTLSMaterial(material localAgentTLSMaterial, caCertificate, caKey, serverCertificate, serverKey []byte, now time.Time) error {
	parent := filepath.Dir(material.Dir)
	staging := material.Dir + ".staging"
	backup := material.Dir + ".backup"
	for _, path := range []string{material.Dir, staging, backup} {
		if err := checkLocalAgentTLSNonSymlink(path, true); err != nil {
			return err
		}
	}
	if exists, err := localAgentTLSPathExists(staging); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("local-agent TLS staging directory still exists: %s", staging)
	}
	if err := os.Mkdir(staging, 0o700); err != nil {
		return fmt.Errorf("create local-agent TLS staging directory: %w", err)
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("chmod local-agent TLS staging directory: %w", err)
	}
	staged := localAgentTLSPathsForDir(staging)
	if err := writeLocalAgentTLSFiles(staged, caCertificate, caKey, serverCertificate, serverKey); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := runLocalAgentTLSStageHook(staged); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := syncDirectory(staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("sync local-agent TLS staging directory: %w", err)
	}
	if err := validateLocalAgentTLSSet(staged, now); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("validate staged local-agent TLS material: %w", err)
	}

	activeExists, err := localAgentTLSPathExists(material.Dir)
	if err != nil {
		return err
	}
	if activeExists {
		if err := os.Rename(material.Dir, backup); err != nil {
			return fmt.Errorf("backup local-agent TLS material: %w", err)
		}
		if err := syncDirectory(parent); err != nil {
			return rollbackLocalAgentTLSReplacement(parent, material.Dir, backup, staging, err)
		}
	}
	if err := runLocalAgentTLSTransactionHook("after-backup"); err != nil {
		if activeExists {
			return rollbackLocalAgentTLSReplacement(parent, material.Dir, backup, staging, err)
		}
		return err
	}
	if err := os.Rename(staging, material.Dir); err != nil {
		if activeExists {
			return rollbackLocalAgentTLSReplacement(parent, material.Dir, backup, staging, err)
		}
		return fmt.Errorf("activate local-agent TLS material: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("sync activated local-agent TLS parent directory: %w", err)
	}
	if err := runLocalAgentTLSTransactionHook("after-activate"); err != nil {
		return err
	}
	if activeExists {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove replaced local-agent TLS backup: %w", err)
		}
		if err := syncDirectory(parent); err != nil {
			return fmt.Errorf("sync local-agent TLS parent directory: %w", err)
		}
	}
	return nil
}

func rollbackLocalAgentTLSReplacement(parent, active, backup, staging string, cause error) error {
	if err := os.Rename(backup, active); err != nil {
		return fmt.Errorf("%w; rollback local-agent TLS material: %v", cause, err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("%w; sync rolled back local-agent TLS parent directory: %v", cause, err)
	}
	_ = os.RemoveAll(staging)
	return cause
}

func runLocalAgentTLSTransactionHook(stage string) error {
	if localAgentTLSTransactionHook == nil {
		return nil
	}
	return localAgentTLSTransactionHook(stage)
}

func runLocalAgentTLSStageHook(material localAgentTLSMaterial) error {
	if localAgentTLSStageHook == nil {
		return nil
	}
	return localAgentTLSStageHook(material)
}

func writeLocalAgentTLSFiles(material localAgentTLSMaterial, caCertificate, caKey, serverCertificate, serverKey []byte) error {
	for _, file := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{material.CACertPath, caCertificate, 0o644},
		{material.CAKeyPath, caKey, 0o600},
		{material.ServerCertPath, serverCertificate, 0o644},
		{material.ServerKeyPath, serverKey, 0o600},
	} {
		if err := writeLocalAgentTLSFile(material.Dir, file.path, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func writeLocalAgentTLSFile(dir, path string, data []byte, mode os.FileMode) error {
	if err := checkLocalAgentTLSNonSymlink(dir, true); err != nil {
		return err
	}
	if err := checkLocalAgentTLSNonSymlink(path, false); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary local-agent TLS file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return fmt.Errorf("chmod temporary local-agent TLS file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary local-agent TLS file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary local-agent TLS file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary local-agent TLS file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace local-agent TLS file: %w", err)
	}
	cleanup = false
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync local-agent TLS directory: %w", err)
	}
	return nil
}

func loadLocalAgentTLSCA(material localAgentTLSMaterial, now time.Time) (localAgentTLSCA, error) {
	certificate, err := readLocalAgentTLSCertificate(material.CACertPath)
	if err != nil {
		return localAgentTLSCA{}, err
	}
	key, err := readLocalAgentTLSECDSAKey(material.CAKeyPath)
	if err != nil {
		return localAgentTLSCA{}, err
	}
	if err := validateLocalAgentTLSCA(certificate, key, now); err != nil {
		return localAgentTLSCA{}, err
	}
	return localAgentTLSCA{certificate: certificate, key: key}, nil
}

func validateLocalAgentTLSCA(certificate *x509.Certificate, key *ecdsa.PrivateKey, now time.Time) error {
	if err := validateLocalAgentTLSCertificateTime(certificate, now); err != nil {
		return invalidLocalAgentTLSError("invalid local-agent CA certificate time: %v", err)
	}
	if !certificate.IsCA || !certificate.BasicConstraintsValid {
		return invalidLocalAgentTLSError("local-agent CA certificate is not a valid CA")
	}
	if certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return invalidLocalAgentTLSError("local-agent CA certificate cannot sign certificates")
	}
	if !localAgentTLSCAUsageValid(certificate) {
		return invalidLocalAgentTLSError("local-agent CA certificate has incompatible extended key usage")
	}
	if !localAgentTLSCAConstraintsValid(certificate) {
		return invalidLocalAgentTLSError("local-agent CA certificate has invalid name constraints")
	}
	if !localAgentTLSCertificateMatchesKey(certificate, key) {
		return invalidLocalAgentTLSError("local-agent CA certificate does not match its private key")
	}
	if err := certificate.CheckSignatureFrom(certificate); err != nil {
		return invalidLocalAgentTLSError("verify local-agent CA self-signature: %v", err)
	}
	return nil
}

func localAgentTLSCAUsageValid(certificate *x509.Certificate) bool {
	if len(certificate.UnknownExtKeyUsage) != 0 {
		return false
	}
	for _, usage := range certificate.ExtKeyUsage {
		if usage != x509.ExtKeyUsageAny && usage != x509.ExtKeyUsageServerAuth {
			return false
		}
	}
	return true
}

func localAgentTLSCAConstraintsValid(certificate *x509.Certificate) bool {
	if !certificate.MaxPathLenZero || !certificate.PermittedDNSDomainsCritical || len(certificate.PermittedDNSDomains) != 1 || certificate.PermittedDNSDomains[0] != "localhost" {
		return false
	}
	if len(certificate.ExcludedDNSDomains) != 0 || len(certificate.ExcludedIPRanges) != 0 || len(certificate.PermittedEmailAddresses) != 0 || len(certificate.ExcludedEmailAddresses) != 0 || len(certificate.PermittedURIDomains) != 0 || len(certificate.ExcludedURIDomains) != 0 || len(certificate.PermittedIPRanges) != 2 {
		return false
	}
	want := map[string]bool{"127.0.0.1/32": false, "::1/128": false}
	for _, ipRange := range certificate.PermittedIPRanges {
		if _, ok := want[ipRange.String()]; !ok {
			return false
		}
		want[ipRange.String()] = true
	}
	return want["127.0.0.1/32"] && want["::1/128"]
}

func localAgentTLSCAHasFullServerLifetime(ca localAgentTLSCA, now time.Time) bool {
	return ca.certificate.NotAfter.After(now.AddDate(1, 0, 0))
}

func validateLocalAgentTLSServerCertificate(material localAgentTLSMaterial, ca localAgentTLSCA, now time.Time) (*x509.Certificate, error) {
	certificate, err := readLocalAgentTLSCertificate(material.ServerCertPath)
	if err != nil {
		return nil, err
	}
	key, err := readLocalAgentTLSECDSAKey(material.ServerKeyPath)
	if err != nil {
		return nil, err
	}
	if err := validateLocalAgentTLSCertificateTime(certificate, now); err != nil {
		return nil, invalidLocalAgentTLSError("invalid local-agent server certificate time: %v", err)
	}
	if !localAgentTLSCertificateMatchesKey(certificate, key) {
		return nil, invalidLocalAgentTLSError("local-agent server certificate does not match its private key")
	}
	if !localAgentTLSServerNamesValid(certificate) {
		return nil, invalidLocalAgentTLSError("local-agent server certificate has invalid subject alternative names")
	}
	if !localAgentTLSServerUsageValid(certificate) {
		return nil, invalidLocalAgentTLSError("local-agent server certificate is not valid for server authentication")
	}
	if err := certificate.CheckSignatureFrom(ca.certificate); err != nil {
		return nil, invalidLocalAgentTLSError("verify local-agent server signature: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.certificate)
	if _, err := certificate.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: now,
		DNSName:     "localhost",
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return nil, invalidLocalAgentTLSError("verify local-agent server certificate chain: %v", err)
	}
	return certificate, nil
}

func validateLocalAgentTLSCertificateTime(certificate *x509.Certificate, now time.Time) error {
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return fmt.Errorf("certificate is not valid at %s", now.Format(time.RFC3339))
	}
	return nil
}

func localAgentTLSCertificateMatchesKey(certificate *x509.Certificate, key *ecdsa.PrivateKey) bool {
	publicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	return ok && publicKey.Curve == elliptic.P256() && key.Curve == elliptic.P256() && publicKey.X.Cmp(key.X) == 0 && publicKey.Y.Cmp(key.Y) == 0
}

func localAgentTLSServerNamesValid(certificate *x509.Certificate) bool {
	if len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != "localhost" || len(certificate.IPAddresses) != 2 {
		return false
	}
	want := map[string]bool{"127.0.0.1": false, "::1": false}
	for _, ip := range certificate.IPAddresses {
		if _, ok := want[ip.String()]; !ok {
			return false
		}
		want[ip.String()] = true
	}
	return want["127.0.0.1"] && want["::1"]
}

func localAgentTLSServerUsageValid(certificate *x509.Certificate) bool {
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			return true
		}
	}
	return false
}

func generateLocalAgentTLSCA(now time.Time) (localAgentTLSCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return localAgentTLSCA{}, fmt.Errorf("generate local-agent CA private key: %w", err)
	}
	serial, err := localAgentTLSSerialNumber()
	if err != nil {
		return localAgentTLSCA{}, err
	}
	_, ipv4Range, _ := net.ParseCIDR("127.0.0.1/32")
	_, ipv6Range, _ := net.ParseCIDR("::1/128")
	template := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: "ConnectMac Local Agent CA"},
		NotBefore:                   now.Add(-time.Minute),
		NotAfter:                    now.AddDate(10, 0, 0),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"localhost"},
		PermittedIPRanges:           []*net.IPNet{ipv4Range, ipv6Range},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return localAgentTLSCA{}, fmt.Errorf("create local-agent CA certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return localAgentTLSCA{}, fmt.Errorf("parse generated local-agent CA certificate: %w", err)
	}
	return localAgentTLSCA{certificate: certificate, key: key}, nil
}

func generateLocalAgentTLSServer(ca localAgentTLSCA, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate local-agent server private key: %w", err)
	}
	serial, err := localAgentTLSSerialNumber()
	if err != nil {
		return nil, nil, err
	}
	notAfter := now.AddDate(1, 0, 0)
	if ca.certificate.NotAfter.Before(notAfter) {
		notAfter = ca.certificate.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("create local-agent server certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated local-agent server certificate: %w", err)
	}
	return certificate, key, nil
}

func localAgentTLSSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	for {
		serial, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return nil, fmt.Errorf("generate local-agent certificate serial number: %w", err)
		}
		if serial.Sign() > 0 {
			return serial, nil
		}
	}
}

func encodeLocalAgentTLSCA(ca localAgentTLSCA) ([]byte, []byte, error) {
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.certificate.Raw})
	keyDER, err := x509.MarshalECPrivateKey(ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal local-agent CA private key: %w", err)
	}
	return certificatePEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func encodeLocalAgentTLSServer(certificate *x509.Certificate, key *ecdsa.PrivateKey) ([]byte, []byte, error) {
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal local-agent server private key: %w", err)
	}
	return certificatePEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func readLocalAgentTLSCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local-agent certificate %s: %w", path, err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return nil, invalidLocalAgentTLSError("parse local-agent certificate %s: expected one PEM certificate", path)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, invalidLocalAgentTLSError("parse local-agent certificate %s: %v", path, err)
	}
	return certificate, nil
}

func readLocalAgentTLSECDSAKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local-agent private key %s: %w", path, err)
	}
	block, rest := pem.Decode(data)
	if block == nil || len(rest) != 0 {
		return nil, invalidLocalAgentTLSError("parse local-agent private key %s: expected one PEM block", path)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		parsed, pkcs8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if pkcs8Err != nil {
			return nil, invalidLocalAgentTLSError("parse local-agent private key %s: %v", path, pkcs8Err)
		}
		var ok bool
		key, ok = parsed.(*ecdsa.PrivateKey)
		if !ok {
			return nil, invalidLocalAgentTLSError("parse local-agent private key %s: expected ECDSA key", path)
		}
	}
	if key.Curve != elliptic.P256() {
		return nil, invalidLocalAgentTLSError("parse local-agent private key %s: expected P-256 key", path)
	}
	return key, nil
}

func invalidLocalAgentTLSError(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errInvalidLocalAgentTLS}, args...)...)
}

func localAgentTLSRecoverableError(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, errInvalidLocalAgentTLS)
}

func localAgentTLSPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect local-agent TLS path %s: %w", path, err)
	}
	return true, nil
}

func checkLocalAgentTLSNonSymlink(path string, directory bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect local-agent TLS path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse symlink in local-agent TLS path: %s", path)
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("local-agent TLS path is not a directory: %s", path)
	}
	return nil
}

func checkLocalAgentTLSReadPaths(home string, material localAgentTLSMaterial) error {
	for _, path := range []string{filepath.Join(home, ".connectmac"), filepath.Join(home, ".connectmac", "local-agent"), material.Dir} {
		if err := checkLocalAgentTLSNonSymlink(path, true); err != nil {
			return err
		}
	}
	return checkLocalAgentTLSMaterialPaths(material)
}

func checkLocalAgentTLSMaterialPaths(material localAgentTLSMaterial) error {
	if err := checkLocalAgentTLSNonSymlink(material.Dir, true); err != nil {
		return err
	}
	for _, path := range []string{material.CACertPath, material.CAKeyPath, material.ServerCertPath, material.ServerKeyPath} {
		if err := checkLocalAgentTLSNonSymlink(path, false); err != nil {
			return err
		}
	}
	return nil
}
