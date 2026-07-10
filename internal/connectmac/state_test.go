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

func TestProcessSignalMeansRunningTreatsPermissionDeniedAsRunning(t *testing.T) {
	if !processSignalMeansRunning(syscall.EPERM) {
		t.Fatalf("EPERM should be treated as running")
	}
	if processSignalMeansRunning(errors.New("missing")) {
		t.Fatalf("generic errors should not be treated as running")
	}
}
