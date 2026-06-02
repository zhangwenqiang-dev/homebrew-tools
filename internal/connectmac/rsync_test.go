package connectmac

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRsyncPullArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile("~/.ssh/example.pem")
	got, err := RsyncPullArgs(profile, "~/Desktop/App.ipa", ".", []string{".DS_Store"})
	if err != nil {
		t.Fatalf("RsyncPullArgs returned error: %v", err)
	}
	key := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-avzP",
		"-e", "ssh -i " + key,
		"--exclude", ".DS_Store",
		"user@mac-host.example.com:~/Desktop/App.ipa",
		".",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestRsyncPushArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile("~/.ssh/example.pem")
	got, err := RsyncPushArgs(profile, "/tmp/project.zip", "~/Downloads/")
	if err != nil {
		t.Fatalf("RsyncPushArgs returned error: %v", err)
	}
	key := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-avzP",
		"-e", "ssh -i " + key,
		"/tmp/project.zip",
		"user@mac-host.example.com:~/Downloads/",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestRsyncPushArgsNormalizesShellExpandedHomeRemoteDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile("~/.ssh/example.pem")
	got, err := RsyncPushArgs(profile, "/tmp/project.zip", filepath.Join(home, "Documents")+"/")
	if err != nil {
		t.Fatalf("RsyncPushArgs returned error: %v", err)
	}
	key := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-avzP",
		"-e", "ssh -i " + key,
		"/tmp/project.zip",
		"user@mac-host.example.com:~/Documents/",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestNormalizeRemotePathKeepsRemoteAbsolutePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := NormalizeRemotePath("/var/tmp/uploads/")
	if got != "/var/tmp/uploads/" {
		t.Fatalf("path = %q", got)
	}
}
