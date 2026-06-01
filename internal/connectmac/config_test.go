package connectmac

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `
profiles:
  xcode-vnc:
    description: Apple account: user@example.com
    user: user
    host: mac-host.example.com
    identity_file: ~/.ssh/example.pem
    sync:
      push:
        excludes:
          - xcuserdata
          - .svn
          - .git
          - .DS_Store
      pull:
        excludes:
          - .DS_Store
    vnc:
      username: mac-user
    tunnels:
      - local_port: 5900
        remote_host: localhost
        remote_port: 5900
`

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(sampleConfig)
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	profile, ok := cfg.Profile("xcode-vnc")
	if !ok {
		t.Fatal("expected profile xcode-vnc")
	}
	if profile.User != "user" {
		t.Fatalf("user = %q, want user", profile.User)
	}
	if profile.IdentityFile != "~/.ssh/example.pem" {
		t.Fatalf("identity_file = %q", profile.IdentityFile)
	}
	if len(profile.Tunnels) != 1 {
		t.Fatalf("tunnel count = %d, want 1", len(profile.Tunnels))
	}
	if len(profile.Sync.Push.Excludes) != 4 {
		t.Fatalf("push exclude count = %d, want 4", len(profile.Sync.Push.Excludes))
	}
	if len(profile.Sync.Pull.Excludes) != 1 {
		t.Fatalf("pull exclude count = %d, want 1", len(profile.Sync.Pull.Excludes))
	}
	if profile.VNC.Username != "mac-user" {
		t.Fatalf("vnc username = %q, want mac-user", profile.VNC.Username)
	}
	if profile.Tunnels[0].LocalPort != 5900 || profile.Tunnels[0].RemoteHost != "localhost" || profile.Tunnels[0].RemotePort != 5900 {
		t.Fatalf("unexpected tunnel: %+v", profile.Tunnels[0])
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExpandPath("~/.ssh/key.pem")
	if err != nil {
		t.Fatalf("ExpandPath returned error: %v", err)
	}
	want := filepath.Join(home, ".ssh", "key.pem")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Fatalf("unexpected error: %v", err)
	}
}
