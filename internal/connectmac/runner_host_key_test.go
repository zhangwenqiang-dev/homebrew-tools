package connectmac

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestScanHostKeyPrimarySuccessDoesNotFallback(t *testing.T) {
	env := installHostKeyScanFakes(t)
	key := testHostKeyRecord(t, "scan.example")
	t.Setenv("CM_TEST_KEYSCAN_MODE", "key")
	t.Setenv("CM_TEST_KEYSCAN_KEY", key)
	t.Setenv("CM_TEST_SSH_MODE", "fail-if-called")

	got, err := (ExecRunner{}).ScanHostKey(context.Background(), "scan.example")
	if err != nil {
		t.Fatalf("ScanHostKey() error = %v", err)
	}
	if got != key {
		t.Fatalf("ScanHostKey() = %q, want %q", got, key)
	}
	if _, err := os.Stat(env.sshArgs); !os.IsNotExist(err) {
		t.Fatalf("fallback invoked, stat error = %v", err)
	}
}

func TestScanHostKeyPrimaryNonzeroWithValidKeyDoesNotFallback(t *testing.T) {
	env := installHostKeyScanFakes(t)
	key := testHostKeyRecord(t, "scan.example")
	t.Setenv("CM_TEST_KEYSCAN_MODE", "key-exit-1")
	t.Setenv("CM_TEST_KEYSCAN_KEY", key)
	t.Setenv("CM_TEST_SSH_MODE", "fail-if-called")

	got, err := (ExecRunner{}).ScanHostKey(context.Background(), "scan.example")
	if err != nil {
		t.Fatalf("ScanHostKey() error = %v", err)
	}
	if got != key {
		t.Fatalf("ScanHostKey() = %q, want %q", got, key)
	}
	if _, err := os.Stat(env.sshArgs); !os.IsNotExist(err) {
		t.Fatalf("fallback invoked, stat error = %v", err)
	}
}

func TestScanHostKeyAcceptsAndNormalizesPlainHosts(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		normalized string
	}{
		{name: "DNS", host: "EC2-1-2-3-4.compute-1.amazonaws.com", normalized: "ec2-1-2-3-4.compute-1.amazonaws.com"},
		{name: "IPv4", host: "192.0.2.10", normalized: "192.0.2.10"},
		{name: "raw IPv6", host: "2001:0db8::10", normalized: "2001:db8::10"},
		{name: "bracketed IPv6", host: "[2001:0db8::10]", normalized: "2001:db8::10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := installHostKeyScanFakes(t)
			key := testHostKeyRecord(t, tt.normalized)
			t.Setenv("CM_TEST_KEYSCAN_MODE", "key")
			t.Setenv("CM_TEST_KEYSCAN_KEY", key)

			got, err := (ExecRunner{}).ScanHostKey(context.Background(), tt.host)
			if err != nil || got != key {
				t.Fatalf("ScanHostKey(%q) = %q, %v; want %q", tt.host, got, err, key)
			}
			assertKeyscanArgs(t, env.keyscanArgs, tt.normalized)
		})
	}
}

func TestScanHostKeyRejectsUnsafeHostsBeforeExecution(t *testing.T) {
	tests := []string{
		"-oProxyCommand=evil",
		"host.example:22",
		"[2001:db8::1]:22",
		" user.example",
		"user.example\n-oProxyCommand=evil",
		"user@host.example",
		"ssh://host.example",
		"host.example,other.example",
		"ProxyCommand=evil",
		"192.0.2.999",
	}
	for _, host := range tests {
		t.Run(host, func(t *testing.T) {
			env := installHostKeyScanFakes(t)
			if got, err := (ExecRunner{}).ScanHostKey(context.Background(), host); err == nil || got != "" {
				t.Fatalf("ScanHostKey(%q) = %q, %v; want validation failure", host, got, err)
			}
			for _, path := range []string{env.keyscanArgs, env.sshArgs} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("command executed for rejected host %q: %s", host, path)
				}
			}
		})
	}
}

func TestValidHostKeyRecordsEnforcesTrustBoundary(t *testing.T) {
	valid := strings.TrimSuffix(testHostKeyRecord(t, "scan.example"), "\n")
	fields := strings.Fields(valid)
	skEd25519 := testSKED25519HostKeyRecord(t, "scan.example")
	skECDSA := testSKECDSAHostKeyRecord(t, "scan.example")
	tests := []struct {
		name string
		line string
		want bool
	}{
		{name: "matching", line: valid, want: true},
		{name: "keyscan bracket form", line: "[scan.example]:22 " + fields[1] + " " + fields[2], want: true},
		{name: "mismatched host", line: "other.example " + fields[1] + " " + fields[2]},
		{name: "marker", line: "@cert-authority scan.example " + fields[1] + " " + fields[2]},
		{name: "SK Ed25519", line: skEd25519, want: true},
		{name: "SK ECDSA P-256", line: skECDSA, want: true},
		{name: "unsupported", line: "scan.example ssh-dss " + fields[2]},
		{name: "malformed base64", line: "scan.example ssh-ed25519 not-base64!"},
		{name: "trailing record data", line: valid + " unsafe-comment"},
		{name: "host list", line: "scan.example,other.example " + fields[1] + " " + fields[2]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validHostKeyRecords([]byte(tt.line+"\n"), "scan.example")
			if (got != "") != tt.want {
				t.Fatalf("validHostKeyRecords(%q) = %q, want accepted=%t", tt.line, got, tt.want)
			}
		})
	}
}

func TestScanHostKeyFailedOrEmptyPrimaryUsesFallback(t *testing.T) {
	for _, mode := range []string{"fail", "empty"} {
		t.Run(mode, func(t *testing.T) {
			env := installHostKeyScanFakes(t)
			key := testHostKeyRecord(t, "scan.example")
			t.Setenv("CM_TEST_KEYSCAN_MODE", mode)
			t.Setenv("CM_TEST_SSH_MODE", "key")
			t.Setenv("CM_TEST_SSH_KEY", key)

			got, err := (ExecRunner{}).ScanHostKey(context.Background(), "scan.example")
			if err != nil || got != key {
				t.Fatalf("ScanHostKey() = %q, %v; want key", got, err)
			}
			assertSafeSSHArgs(t, env.sshArgs, "scan.example")
		})
	}
}

func TestScanHostKeyFallbackNonzeroWithValidKeySucceeds(t *testing.T) {
	installHostKeyScanFakes(t)
	key := testHostKeyRecord(t, "scan.example")
	t.Setenv("CM_TEST_KEYSCAN_MODE", "fail")
	t.Setenv("CM_TEST_SSH_MODE", "key-exit-255")
	t.Setenv("CM_TEST_SSH_KEY", key)

	got, err := (ExecRunner{}).ScanHostKey(context.Background(), "scan.example")
	if err != nil || got != key {
		t.Fatalf("ScanHostKey() = %q, %v; want key despite exit 255", got, err)
	}
}

func TestScanHostKeyEmptyFallbackFailsWithSanitizedDiagnostic(t *testing.T) {
	installHostKeyScanFakes(t)
	t.Setenv("CM_TEST_KEYSCAN_MODE", "fail")
	t.Setenv("CM_TEST_SSH_MODE", "empty")
	t.Setenv("CM_TEST_SECRET_OUTPUT", "user@example.com /private/tmp/secret ssh-ed25519 AAAAKEY")

	got, err := (ExecRunner{}).ScanHostKey(context.Background(), "scan.example")
	if err == nil || got != "" {
		t.Fatalf("ScanHostKey() = %q, %v; want failure", got, err)
	}
	message := err.Error()
	for _, want := range []string{"primary: exit 1", "fallback: no valid keys"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q missing %q", message, want)
		}
	}
	for _, secret := range []string{"user@example.com", "/private/tmp/secret", "ssh-ed25519", "AAAAKEY"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error leaked %q: %s", secret, message)
		}
	}
	if len(message) > 160 {
		t.Fatalf("diagnostic is not bounded: %d bytes", len(message))
	}
}

func TestScanHostKeyCleansUpTemporaryArtifacts(t *testing.T) {
	env := installHostKeyScanFakes(t)
	t.Setenv("TMPDIR", env.tempRoot)
	t.Setenv("CM_TEST_KEYSCAN_MODE", "empty")
	t.Setenv("CM_TEST_SSH_MODE", "empty")

	_, _ = (ExecRunner{}).ScanHostKey(context.Background(), "scan.example")
	entries, err := os.ReadDir(env.tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "connectmac-host-key-") {
			t.Fatalf("temporary scan directory remains: %s", entry.Name())
		}
	}
}

func TestScanHostKeyHonorsCancellation(t *testing.T) {
	env := installHostKeyScanFakes(t)
	t.Setenv("TMPDIR", env.tempRoot)
	t.Setenv("CM_TEST_KEYSCAN_MODE", "empty")
	t.Setenv("CM_TEST_SSH_MODE", "block")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := (ExecRunner{}).ScanHostKey(ctx, "scan.example")
	if err != context.DeadlineExceeded {
		t.Fatalf("ScanHostKey() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
	assertTemporaryHostKeyModes(t, env.sshModes)
	assertNoHostKeyScanTempDirs(t, env.tempRoot)
}

type hostKeyScanFakeEnv struct {
	tempRoot    string
	keyscanArgs string
	sshArgs     string
	sshModes    string
}

func installHostKeyScanFakes(t *testing.T) hostKeyScanFakeEnv {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ssh-keyscan", "ssh"} {
		if err := os.Symlink(executable, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	keyscanArgs := filepath.Join(root, "ssh-keyscan.args")
	sshArgs := filepath.Join(root, "ssh.args")
	sshModes := filepath.Join(root, "ssh.modes")
	t.Setenv("PATH", bin)
	t.Setenv("CM_TEST_HOST_KEY_HELPER", "1")
	t.Setenv("CM_TEST_KEYSCAN_ARGS", keyscanArgs)
	t.Setenv("CM_TEST_SSH_ARGS", sshArgs)
	t.Setenv("CM_TEST_SSH_MODES", sshModes)
	return hostKeyScanFakeEnv{tempRoot: root, keyscanArgs: keyscanArgs, sshArgs: sshArgs, sshModes: sshModes}
}

func assertKeyscanArgs(t *testing.T, path, host string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Split(string(raw), "\x00"), []string{"-T", "5", "--", host}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ssh-keyscan argv = %#v, want %#v", got, want)
	}
}

func assertTemporaryHostKeyModes(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "0700\n0600\n"; got != want {
		t.Fatalf("temporary modes = %q, want %q", got, want)
	}
}

func assertNoHostKeyScanTempDirs(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "connectmac-host-key-") {
			t.Fatalf("temporary scan directory remains: %s", entry.Name())
		}
	}
}

func assertSafeSSHArgs(t *testing.T, path, host string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(string(raw), "\x00")
	knownHosts := optionValue(got, "UserKnownHostsFile=")
	if knownHosts == "" {
		t.Fatalf("missing temporary known_hosts in argv: %q", got)
	}
	want := []string{
		"-F", "/dev/null", "-n", "-T",
		"-o", "BatchMode=yes",
		"-o", "PreferredAuthentications=none",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ClearAllForwardings=yes",
		"-o", "ProxyCommand=none",
		"--", host, "true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ssh argv = %#v, want %#v", got, want)
	}
	if strings.Contains(strings.Join(got, " "), "IdentityFile") {
		t.Fatalf("ssh argv contains identity file: %q", got)
	}
}

func optionValue(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func testHostKeyRecord(t *testing.T, host string) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return host + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + "\n"
}

func testSKED25519HostKeyRecord(t *testing.T, host string) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyType = "sk-ssh-ed25519@openssh.com"
	blob := ssh.Marshal(struct {
		Type        string
		PublicKey   []byte
		Application string
	}{keyType, public, "ssh:test"})
	return host + " " + keyType + " " + base64.StdEncoding.EncodeToString(blob)
}

func testSKECDSAHostKeyRecord(t *testing.T, host string) string {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyType = "sk-ecdsa-sha2-nistp256@openssh.com"
	blob := ssh.Marshal(struct {
		Type        string
		Curve       string
		PublicKey   []byte
		Application string
	}{keyType, "nistp256", elliptic.Marshal(elliptic.P256(), private.X, private.Y), "ssh:test"})
	return host + " " + keyType + " " + base64.StdEncoding.EncodeToString(blob)
}

func TestMain(m *testing.M) {
	if os.Getenv("CM_TEST_HOST_KEY_HELPER") != "1" {
		os.Exit(m.Run())
	}
	name := filepath.Base(os.Args[0])
	switch name {
	case "ssh-keyscan":
		_ = os.WriteFile(os.Getenv("CM_TEST_KEYSCAN_ARGS"), []byte(strings.Join(os.Args[1:], "\x00")), 0o600)
		runHostKeyFake(os.Getenv("CM_TEST_KEYSCAN_MODE"), os.Getenv("CM_TEST_KEYSCAN_KEY"), "")
	case "ssh":
		args := os.Args[1:]
		_ = os.WriteFile(os.Getenv("CM_TEST_SSH_ARGS"), []byte(strings.Join(args, "\x00")), 0o600)
		knownHosts := optionValue(args, "UserKnownHostsFile=")
		recordTemporaryHostKeyModes(knownHosts)
		runHostKeyFake(os.Getenv("CM_TEST_SSH_MODE"), os.Getenv("CM_TEST_SSH_KEY"), knownHosts)
	}
	os.Exit(2)
}

func runHostKeyFake(mode, key, knownHosts string) {
	switch mode {
	case "key", "key-exit-1", "key-exit-255":
		if knownHosts == "" {
			_, _ = os.Stdout.WriteString(key)
		} else {
			_ = os.WriteFile(knownHosts, []byte(key), 0o600)
		}
		if mode == "key-exit-1" {
			os.Exit(1)
		}
		if mode == "key-exit-255" {
			os.Exit(255)
		}
		os.Exit(0)
	case "fail":
		_, _ = os.Stderr.WriteString(os.Getenv("CM_TEST_SECRET_OUTPUT"))
		os.Exit(1)
	case "fail-if-called":
		os.Exit(99)
	case "block":
		time.Sleep(time.Minute)
		os.Exit(0)
	default:
		_, _ = os.Stderr.WriteString(os.Getenv("CM_TEST_SECRET_OUTPUT"))
		os.Exit(0)
	}
}

func recordTemporaryHostKeyModes(knownHosts string) {
	fileInfo, fileErr := os.Stat(knownHosts)
	dirInfo, dirErr := os.Stat(filepath.Dir(knownHosts))
	if fileErr != nil || dirErr != nil {
		return
	}
	modes := fmt.Sprintf("%04o\n%04o\n", dirInfo.Mode().Perm(), fileInfo.Mode().Perm())
	_ = os.WriteFile(os.Getenv("CM_TEST_SSH_MODES"), []byte(modes), 0o600)
}
