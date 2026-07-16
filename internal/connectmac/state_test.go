package connectmac

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func testProcessIdentity(command, startMarker string) ProcessIdentity {
	return ProcessIdentity{Command: command, StartMarker: startMarker}
}

func TestStateSaveLoadAndRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return pid == 1234 }
	state := NewState(validProfile("/tmp/key.pem"), 1234, testProcessIdentity("ssh test", "start-1234"))
	if err := manager.Save(state); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	got, ok, err := manager.Load("xcode-vnc")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected state to exist")
	}
	if got.PID != 1234 || got.Profile != "xcode-vnc" {
		t.Fatalf("unexpected state: %+v", got)
	}
	if err := manager.Remove("xcode-vnc"); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	_, ok, err = manager.Load("xcode-vnc")
	if err != nil {
		t.Fatalf("Load after remove returned error: %v", err)
	}
	if ok {
		t.Fatal("expected state to be removed")
	}
}

func TestStateLoadReturnsStalePIDWithoutDeletingState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return false }
	if err := manager.Save(NewState(validProfile("/tmp/key.pem"), 9876, testProcessIdentity("ssh test", "start-9876"))); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	state, ok, err := manager.Load("xcode-vnc")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !ok || state.PID != 9876 {
		t.Fatalf("stale state = %+v, ok=%t", state, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "xcode-vnc.json")); err != nil {
		t.Fatalf("read-only load deleted stale state: %v", err)
	}
}

func TestStateList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return pid == 44 }
	state := NewState(validProfile("/tmp/key.pem"), 44, testProcessIdentity("ssh test", "start-44"))
	if err := manager.Save(state); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	manager.InspectProcess = func(pid int) (ProcessIdentity, error) {
		sshArgs, err := SSHArgs(validProfile("/tmp/key.pem"))
		if err != nil {
			return ProcessIdentity{}, err
		}
		return testProcessIdentity(expectedSSHCommand(sshArgs), "start-44"), nil
	}
	states, err := manager.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("state count = %d, want 1", len(states))
	}
}

func TestStateListOmitsLivePIDWithCommandMismatchWithoutDeletingState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return true }
	state := NewState(validProfile("/tmp/key.pem"), 44, testProcessIdentity("ssh test", "start-44"))
	if err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	manager.InspectProcess = func(pid int) (ProcessIdentity, error) {
		return testProcessIdentity("unrelated --serve", "start-44"), nil
	}

	states, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("mismatched states = %+v, want none", states)
	}
	if _, err := os.Stat(filepath.Join(dir, "xcode-vnc.json")); err != nil {
		t.Fatalf("read-only list deleted mismatched state: %v", err)
	}
}

func TestStateListOmitsLivePIDWithStartMarkerMismatchWithoutDeletingState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return true }
	state := NewState(validProfile("/tmp/key.pem"), 44, testProcessIdentity("ssh test", "start-44"))
	if err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	manager.InspectProcess = func(pid int) (ProcessIdentity, error) {
		sshArgs, err := SSHArgs(validProfile("/tmp/key.pem"))
		if err != nil {
			return ProcessIdentity{}, err
		}
		return testProcessIdentity(expectedSSHCommand(sshArgs), "different-start"), nil
	}

	states, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("mismatched states = %+v, want none", states)
	}
	if _, err := os.Stat(filepath.Join(dir, "xcode-vnc.json")); err != nil {
		t.Fatalf("read-only list deleted mismatched state: %v", err)
	}
}

func TestStateListOmitsStalePIDWithoutDeletingState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return false }
	if err := manager.Save(NewState(validProfile("/tmp/key.pem"), 44, testProcessIdentity("ssh test", "start-44"))); err != nil {
		t.Fatal(err)
	}
	states, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("stale states = %+v, want none", states)
	}
	if _, err := os.Stat(filepath.Join(dir, "xcode-vnc.json")); err != nil {
		t.Fatalf("read-only list deleted stale state: %v", err)
	}
}

func TestStateMatchesProfile(t *testing.T) {
	profile := validProfile("/tmp/key.pem")
	state := NewState(profile, 44, testProcessIdentity("ssh test", "start-44"))

	if !state.Matches(profile) {
		t.Fatal("expected state to match the profile it was created from")
	}
}

func TestStateDoesNotMatchChangedHostOrTunnels(t *testing.T) {
	profile := validProfile("/tmp/key.pem")
	state := NewState(profile, 44, testProcessIdentity("ssh test", "start-44"))

	changedHost := profile
	changedHost.Host = "replacement.example.com"
	if state.Matches(changedHost) {
		t.Fatal("state should not match a changed host")
	}

	changedLocalPort := profile
	changedLocalPort.Tunnels = append([]Tunnel(nil), profile.Tunnels...)
	changedLocalPort.Tunnels[0].LocalPort = 5901
	if state.Matches(changedLocalPort) {
		t.Fatal("state should not match a changed local port")
	}

	changedRemoteHost := profile
	changedRemoteHost.Tunnels = append([]Tunnel(nil), profile.Tunnels...)
	changedRemoteHost.Tunnels[0].RemoteHost = "127.0.0.1"
	if state.Matches(changedRemoteHost) {
		t.Fatal("state should not match a changed remote host")
	}

	changedRemotePort := profile
	changedRemotePort.Tunnels = append([]Tunnel(nil), profile.Tunnels...)
	changedRemotePort.Tunnels[0].RemotePort = 5901
	if state.Matches(changedRemotePort) {
		t.Fatal("state should not match a changed remote port")
	}
}

func TestStateDoesNotMatchChangedIdentityFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	profile := validProfile("~/.ssh/first.pem")
	state := NewState(profile, 44, testProcessIdentity("ssh test", "start-44"))

	expanded := profile
	expanded.IdentityFile = filepath.Join(os.Getenv("HOME"), ".ssh", "first.pem")
	if !state.Matches(expanded) {
		t.Fatal("expanded and normalized identity paths should match")
	}

	changed := profile
	changed.IdentityFile = "~/.ssh/second.pem"
	if state.Matches(changed) {
		t.Fatal("state should not match a changed identity file")
	}
}

func TestProcessSignalMeansRunningTreatsPermissionDeniedAsRunning(t *testing.T) {
	if !processSignalMeansRunning(syscall.EPERM) {
		t.Fatalf("EPERM should be treated as running")
	}
	if processSignalMeansRunning(errors.New("missing")) {
		t.Fatalf("generic errors should not be treated as running")
	}
}

func TestStateSaveAtomicallyReplacesExistingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	first := NewState(validProfile("/tmp/key.pem"), 1, testProcessIdentity("ssh first", "start-1"))
	second := NewState(validProfile("/tmp/key.pem"), 2, testProcessIdentity("ssh second", "start-2"))
	if err := manager.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := manager.Save(second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "xcode-vnc.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"pid": 2`) || strings.Contains(string(data), `"pid": 1`) {
		t.Fatalf("state file was not atomically replaced: %s", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file left after successful save: %s", entry.Name())
		}
	}
}

func TestStateSaveCleansTemporaryFileAfterRenameFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(filepath.Join(dir, "xcode-vnc.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewStateManager(dir)
	err := manager.Save(NewState(validProfile("/tmp/key.pem"), 1, testProcessIdentity("ssh test", "start-1")))
	if err == nil || !strings.Contains(err.Error(), "replace state file") {
		t.Fatalf("Save error = %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file left after failed save: %s", entry.Name())
		}
	}
}

func TestStateSaveReportsDirectorySyncFailureAfterRename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.SyncDirectory = func(path string) error {
		if path != dir {
			t.Fatalf("sync path = %q, want %q", path, dir)
		}
		return errors.New("sync failed")
	}
	state := NewState(validProfile("/tmp/key.pem"), 1, testProcessIdentity("ssh test", "start-1"))
	err := manager.Save(state)
	if err == nil || !strings.Contains(err.Error(), "sync state directory: sync failed") {
		t.Fatalf("Save error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "xcode-vnc.json")); statErr != nil {
		t.Fatalf("renamed state file missing after directory sync failure: %v", statErr)
	}
}

func TestStateRemoveSyncsContainingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	state := NewState(validProfile("/tmp/key.pem"), 1, testProcessIdentity("ssh test", "start-1"))
	if err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	var synced string
	manager.SyncDirectory = func(path string) error {
		synced = path
		return nil
	}

	if err := manager.Remove("xcode-vnc"); err != nil {
		t.Fatal(err)
	}
	if synced != dir {
		t.Fatalf("synced directory = %q, want %q", synced, dir)
	}
}

func TestStateRemoveReportsDirectorySyncFailureAfterUnlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	state := NewState(validProfile("/tmp/key.pem"), 1, testProcessIdentity("ssh test", "start-1"))
	if err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	manager.SyncDirectory = func(path string) error {
		return errors.New("sync failed")
	}

	err := manager.Remove("xcode-vnc")
	if err == nil || !strings.Contains(err.Error(), "sync state directory: sync failed") {
		t.Fatalf("Remove error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "xcode-vnc.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state file still exists after unlink: %v", statErr)
	}
}

func TestInspectExpectedProcessRejectsUnrelatedReusedPID(t *testing.T) {
	manager := NewStateManager(t.TempDir())
	manager.InspectProcess = func(pid int) (ProcessIdentity, error) {
		return testProcessIdentity("unrelated --serve", "start-55"), nil
	}
	if _, err := manager.InspectExpectedProcess(55, []string{"-N", "user@example.com"}); err == nil ||
		!strings.Contains(err.Error(), "does not match expected SSH tunnel command") {
		t.Fatalf("InspectExpectedProcess error = %v", err)
	}
}

func TestNormalizeSSHCommandCanonicalizesExecutableOnly(t *testing.T) {
	tests := map[string]string{
		" /usr/bin/ssh -N user@example.com ": "ssh -N user@example.com",
		"/opt/bin/ssh\t-N":                   "/opt/bin/ssh\t-N",
		"ssh -N":                             "ssh -N",
		"/usr/bin/not-ssh -N":                "/usr/bin/not-ssh -N",
	}
	for input, want := range tests {
		if got := normalizeSSHCommand(input); got != want {
			t.Errorf("normalizeSSHCommand(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizedIdentityFileExpandsAndCleansPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := normalizedIdentityFile("~/.ssh/../ssh/id.pem")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "ssh", "id.pem")
	if got != want {
		t.Fatalf("normalizedIdentityFile = %q, want %q", got, want)
	}
}

func TestParseLinuxProcStartMarkerHandlesSpacesAndParenthesesInCommand(t *testing.T) {
	fields := []string{
		"R", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"10", "11", "12", "13", "14", "15", "16", "17", "18", "424242",
	}
	got, err := parseLinuxProcStartMarker(77, "77 (ssh worker) child) "+strings.Join(fields, " "))
	if err != nil {
		t.Fatal(err)
	}
	if got != "424242" {
		t.Fatalf("start marker = %q, want 424242", got)
	}
}

func TestParseLinuxProcStartMarkerRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"", "77 ssh", "77 (ssh) R 1 2"} {
		if _, err := parseLinuxProcStartMarker(77, input); err == nil {
			t.Fatalf("parseLinuxProcStartMarker(%q) unexpectedly succeeded", input)
		}
	}
}
