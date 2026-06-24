package connectmac

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSSHArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile("~/.ssh/example.pem")
	got, err := SSHArgs(profile)
	if err != nil {
		t.Fatalf("SSHArgs returned error: %v", err)
	}
	wantKey := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-N",
		"-L", "5900:localhost:5900",
		"-i", wantKey,
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"user@mac-host.example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	_ = os.Unsetenv("HOME")
}

func TestTunnelSummary(t *testing.T) {
	got := TunnelSummary(Tunnel{LocalPort: 5900, RemoteHost: "localhost", RemotePort: 5900})
	if got != "localhost:5900 -> localhost:5900" {
		t.Fatalf("summary = %q", got)
	}
}

func TestInteractiveSSHArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile("~/.ssh/example.pem")
	got, err := InteractiveSSHArgs(profile)
	if err != nil {
		t.Fatalf("InteractiveSSHArgs returned error: %v", err)
	}
	wantKey := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-i", wantKey,
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"user@mac-host.example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestExecSSHArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile("~/.ssh/example.pem")
	got, err := ExecSSHArgs(profile, []string{"ls -ld ~/Downloads/Vitora && du -sh ~/Downloads/Vitora"})
	if err != nil {
		t.Fatalf("ExecSSHArgs returned error: %v", err)
	}
	wantKey := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-i", wantKey,
		"-o", "IdentitiesOnly=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"user@mac-host.example.com",
		"ls -ld ~/Downloads/Vitora && du -sh ~/Downloads/Vitora",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}
