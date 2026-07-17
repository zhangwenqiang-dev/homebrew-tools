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
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	localAgentServerRenewBefore = 30 * 24 * time.Hour
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
	dir := filepath.Join(home, ".connectmac", "local-agent", "tls")
	return localAgentTLSMaterial{
		Dir:            dir,
		CACertPath:     filepath.Join(dir, "ca.pem"),
		CAKeyPath:      filepath.Join(dir, "ca-key.pem"),
		ServerCertPath: filepath.Join(dir, "server.pem"),
		ServerKeyPath:  filepath.Join(dir, "server-key.pem"),
	}
}

// ensureLocalAgentTLS creates or repairs localhost TLS material. It does not
// modify an existing certificate or key until all replacement bytes exist.
func ensureLocalAgentTLS(home string, now time.Time) (localAgentTLSMaterial, bool, error) {
	material := localAgentTLSPaths(home)
	if err := ensureLocalAgentTLSDir(material.Dir); err != nil {
		return material, false, err
	}

	ca, err := loadLocalAgentTLSCA(material, now)
	if err != nil {
		ca, err = generateLocalAgentTLSCA(now)
		if err != nil {
			return material, false, err
		}
		serverCert, serverKey, err := generateLocalAgentTLSServer(ca, now)
		if err != nil {
			return material, false, err
		}
		caCertPEM, caKeyPEM, err := encodeLocalAgentTLSCA(ca)
		if err != nil {
			return material, false, err
		}
		serverCertPEM, serverKeyPEM, err := encodeLocalAgentTLSServer(serverCert, serverKey)
		if err != nil {
			return material, false, err
		}
		if err := writeLocalAgentTLSMaterial(material, caCertPEM, caKeyPEM, serverCertPEM, serverKeyPEM); err != nil {
			return material, false, err
		}
		return material, true, nil
	}

	if serverNeedsLocalAgentTLSRenewal(material, ca, now) {
		serverCert, serverKey, err := generateLocalAgentTLSServer(ca, now)
		if err != nil {
			return material, false, err
		}
		serverCertPEM, serverKeyPEM, err := encodeLocalAgentTLSServer(serverCert, serverKey)
		if err != nil {
			return material, false, err
		}
		if err := writeLocalAgentTLSServer(material, serverCertPEM, serverKeyPEM); err != nil {
			return material, false, err
		}
		return material, true, nil
	}

	changed, err := ensureLocalAgentTLSPermissions(material)
	if err != nil {
		return material, false, err
	}
	return material, changed, nil
}

func loadLocalAgentTLS(home string, now time.Time) (localAgentTLSMaterial, error) {
	material := localAgentTLSPaths(home)
	ca, err := loadLocalAgentTLSCA(material, now)
	if err != nil {
		return material, err
	}
	if err := validateLocalAgentTLSServer(material, ca, now); err != nil {
		return material, err
	}
	return material, nil
}

func localAgentCAFingerprint(path string) (string, error) {
	certificate, err := readLocalAgentTLSCertificate(path)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(certificate.Raw)
	return hex.EncodeToString(sum[:]), nil
}

func ensureLocalAgentTLSDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create local-agent TLS directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("chmod local-agent TLS directory: %w", err)
	}
	return nil
}

func ensureLocalAgentTLSPermissions(material localAgentTLSMaterial) (bool, error) {
	changed := false
	for _, file := range []struct {
		path string
		mode os.FileMode
	}{
		{material.CACertPath, 0o644},
		{material.CAKeyPath, 0o600},
		{material.ServerCertPath, 0o644},
		{material.ServerKeyPath, 0o600},
	} {
		info, err := os.Stat(file.path)
		if err != nil {
			return false, fmt.Errorf("stat local-agent TLS file %s: %w", file.path, err)
		}
		if info.Mode().Perm() == file.mode {
			continue
		}
		if err := os.Chmod(file.path, file.mode); err != nil {
			return false, fmt.Errorf("chmod local-agent TLS file %s: %w", file.path, err)
		}
		changed = true
	}
	return changed, nil
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
		return fmt.Errorf("invalid local-agent CA certificate time: %w", err)
	}
	if !certificate.IsCA || !certificate.BasicConstraintsValid {
		return fmt.Errorf("local-agent CA certificate is not a valid CA")
	}
	if certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return fmt.Errorf("local-agent CA certificate cannot sign certificates")
	}
	if !localAgentTLSCertificateMatchesKey(certificate, key) {
		return fmt.Errorf("local-agent CA certificate does not match its private key")
	}
	if err := certificate.CheckSignatureFrom(certificate); err != nil {
		return fmt.Errorf("verify local-agent CA self-signature: %w", err)
	}
	return nil
}

func serverNeedsLocalAgentTLSRenewal(material localAgentTLSMaterial, ca localAgentTLSCA, now time.Time) bool {
	serverCertificate, err := readLocalAgentTLSCertificate(material.ServerCertPath)
	if err != nil {
		return true
	}
	if err := validateLocalAgentTLSServer(material, ca, now); err != nil {
		return true
	}
	return serverCertificate.NotAfter.Sub(now) <= localAgentServerRenewBefore
}

func validateLocalAgentTLSServer(material localAgentTLSMaterial, ca localAgentTLSCA, now time.Time) error {
	certificate, err := readLocalAgentTLSCertificate(material.ServerCertPath)
	if err != nil {
		return err
	}
	key, err := readLocalAgentTLSECDSAKey(material.ServerKeyPath)
	if err != nil {
		return err
	}
	if err := validateLocalAgentTLSCertificateTime(certificate, now); err != nil {
		return fmt.Errorf("invalid local-agent server certificate time: %w", err)
	}
	if !localAgentTLSCertificateMatchesKey(certificate, key) {
		return fmt.Errorf("local-agent server certificate does not match its private key")
	}
	if !localAgentTLSServerNamesValid(certificate) {
		return fmt.Errorf("local-agent server certificate has invalid subject alternative names")
	}
	if !localAgentTLSServerUsageValid(certificate) {
		return fmt.Errorf("local-agent server certificate is not valid for server authentication")
	}
	if err := certificate.CheckSignatureFrom(ca.certificate); err != nil {
		return fmt.Errorf("verify local-agent server signature: %w", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.certificate)
	if _, err := certificate.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: now,
		DNSName:     "localhost",
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return fmt.Errorf("verify local-agent server certificate chain: %w", err)
	}
	return nil
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
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ConnectMac Local Agent CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
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
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
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

func writeLocalAgentTLSMaterial(material localAgentTLSMaterial, caCert, caKey, serverCert, serverKey []byte) error {
	for _, file := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{material.CACertPath, caCert, 0o644},
		{material.CAKeyPath, caKey, 0o600},
		{material.ServerCertPath, serverCert, 0o644},
		{material.ServerKeyPath, serverKey, 0o600},
	} {
		if err := writeLocalAgentTLSFile(material.Dir, file.path, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func writeLocalAgentTLSServer(material localAgentTLSMaterial, certificate, key []byte) error {
	if err := writeLocalAgentTLSFile(material.Dir, material.ServerCertPath, certificate, 0o644); err != nil {
		return err
	}
	return writeLocalAgentTLSFile(material.Dir, material.ServerKeyPath, key, 0o600)
}

func writeLocalAgentTLSFile(dir, path string, data []byte, mode os.FileMode) error {
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

func readLocalAgentTLSCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local-agent certificate %s: %w", path, err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return nil, fmt.Errorf("parse local-agent certificate %s: expected one PEM certificate", path)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse local-agent certificate %s: %w", path, err)
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
		return nil, fmt.Errorf("parse local-agent private key %s: expected one PEM block", path)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		parsed, pkcs8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if pkcs8Err != nil {
			return nil, fmt.Errorf("parse local-agent private key %s: %w", path, err)
		}
		var ok bool
		key, ok = parsed.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("parse local-agent private key %s: expected ECDSA key", path)
		}
	}
	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("parse local-agent private key %s: expected P-256 key", path)
	}
	return key, nil
}
