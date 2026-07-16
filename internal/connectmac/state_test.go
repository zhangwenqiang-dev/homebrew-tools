package connectmac

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStateSaveLoadAndRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return pid == 1234 }
	state := NewState(validProfile("/tmp/key.pem"), 1234)
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

func TestStateLoadIgnoresStalePID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return false }
	if err := manager.Save(NewState(validProfile("/tmp/key.pem"), 9876)); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	_, ok, err := manager.Load("xcode-vnc")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if ok {
		t.Fatal("expected stale state to be ignored")
	}
}

func TestStateList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	manager := NewStateManager(dir)
	manager.IsRunning = func(pid int) bool { return pid == 44 }
	if err := manager.Save(NewState(validProfile("/tmp/key.pem"), 44)); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	states, err := manager.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("state count = %d, want 1", len(states))
	}
}

func TestStateMatchesProfile(t *testing.T) {
	profile := validProfile("/tmp/key.pem")
	state := NewState(profile, 44)

	if !state.Matches(profile) {
		t.Fatal("expected state to match the profile it was created from")
	}
}

func TestStateDoesNotMatchChangedHostOrTunnels(t *testing.T) {
	profile := validProfile("/tmp/key.pem")
	state := NewState(profile, 44)

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

func TestProcessSignalMeansRunningTreatsPermissionDeniedAsRunning(t *testing.T) {
	if !processSignalMeansRunning(syscall.EPERM) {
		t.Fatalf("EPERM should be treated as running")
	}
	if processSignalMeansRunning(errors.New("missing")) {
		t.Fatalf("generic errors should not be treated as running")
	}
}
