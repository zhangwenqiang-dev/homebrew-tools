package connectmac

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProfileSuccess(t *testing.T) {
	key := writeSSHKey(t, 0o600)
	profile := validProfile(key)
	errs := NewValidatorForTest(nil).ValidateProfile(profile)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateProfileMissingKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile(filepath.Join(home, ".ssh", "missing.pem"))
	errs := NewValidatorForTest(nil).ValidateProfile(profile)
	if !containsError(errs, "identity file does not exist") {
		t.Fatalf("expected missing key error, got %v", errs)
	}
}

func TestValidateProfileBadKeyPermissions(t *testing.T) {
	key := writeSSHKey(t, 0o644)
	profile := validProfile(key)
	errs := NewValidatorForTest(nil).ValidateProfile(profile)
	if !containsError(errs, "permissions are too open") {
		t.Fatalf("expected permissions error, got %v", errs)
	}
}

func TestValidateProfileBusyPort(t *testing.T) {
	key := writeSSHKey(t, 0o600)
	profile := validProfile(key)
	validator := NewValidatorForTest(func(port int) error {
		return errString("local port 5900 is already in use")
	})
	errs := validator.ValidateProfile(profile)
	if !containsError(errs, "already in use") {
		t.Fatalf("expected port error, got %v", errs)
	}
}

func TestValidateProfileRejectsKeyOutsideSSHDir(t *testing.T) {
	key := writeKey(t, 0o600)
	profile := validProfile(key)
	errs := NewValidatorForTest(nil).ValidateProfile(profile)
	if !containsError(errs, "must be stored under") {
		t.Fatalf("expected ssh directory error, got %v", errs)
	}
}

func NewValidatorForTest(portChecker PortChecker) Validator {
	if portChecker == nil {
		portChecker = func(int) error { return nil }
	}
	return Validator{
		CheckPort:  portChecker,
		CheckSSH:   func() error { return nil },
		CheckRsync: func() error { return nil },
	}
}

func validProfile(key string) Profile {
	return Profile{
		Name:         "xcode-vnc",
		Description:  "Xcode Mac VNC tunnel",
		User:         "user",
		Host:         "mac-host.example.com",
		IdentityFile: key,
		Sync: SyncConfig{
			Push: SyncDirection{Excludes: []string{
				"xcuserdata",
				".svn",
				".git",
				".DS_Store",
			}},
			Pull: SyncDirection{Excludes: []string{
				".DS_Store",
			}},
		},
		VNC: VNCConfig{Username: "mac-user"},
		Tunnels: []Tunnel{{
			LocalPort:  5900,
			RemoteHost: "localhost",
			RemotePort: 5900,
		}},
	}
}

func writeKey(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, []byte("secret"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSSHKey(t *testing.T, mode os.FileMode) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, []byte("secret"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsError(errs []error, text string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), text) {
			return true
		}
	}
	return false
}

type errString string

func (e errString) Error() string { return string(e) }
