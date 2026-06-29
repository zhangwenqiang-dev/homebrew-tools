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
	got, err := RsyncPullArgs(profile, "~/Desktop/App.ipa", ".", SyncFilters{Excludes: []string{".DS_Store"}})
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
	got, err := RsyncPushArgs(profile, "/tmp/project", "~/Downloads/", SyncFilters{Excludes: []string{"xcuserdata", ".git"}})
	if err != nil {
		t.Fatalf("RsyncPushArgs returned error: %v", err)
	}
	key := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-avzP",
		"-e", "ssh -i " + key,
		"--exclude", "xcuserdata",
		"--exclude", ".git",
		"/tmp/project",
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
	got, err := RsyncPushArgs(profile, "/tmp/project", filepath.Join(home, "Documents")+"/", SyncFilters{})
	if err != nil {
		t.Fatalf("RsyncPushArgs returned error: %v", err)
	}
	key := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-avzP",
		"-e", "ssh -i " + key,
		"/tmp/project",
		"user@mac-host.example.com:~/Documents/",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestRsyncArgsIncludeOnlyAddsFinalExcludeAll(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := validProfile("~/.ssh/example.pem")
	got, err := RsyncPushArgs(profile, "/tmp/project", "~/Downloads/", SyncFilters{
		Includes: []string{"Sources/***", "*.xcodeproj/***"},
		Excludes: []string{"DerivedData", ".git"},
	})
	if err != nil {
		t.Fatalf("RsyncPushArgs returned error: %v", err)
	}
	key := filepath.Join(home, ".ssh", "example.pem")
	want := []string{
		"-avzP",
		"-e", "ssh -i " + key,
		"--include", "Sources/***",
		"--include", "*.xcodeproj/***",
		"--exclude", "DerivedData",
		"--exclude", ".git",
		"--exclude", "*",
		"/tmp/project",
		"user@mac-host.example.com:~/Downloads/",
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
