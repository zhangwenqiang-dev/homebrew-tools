package connectmac

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const DefaultStateDir = "~/.connectmac/state"

var stateProfileLocks sync.Map

type State struct {
	Profile               string    `json:"profile"`
	PID                   int       `json:"pid"`
	StartedAt             time.Time `json:"started_at"`
	Target                string    `json:"target"`
	Tunnels               []Tunnel  `json:"tunnels"`
	IdentityFile          string    `json:"identity_file,omitempty"`
	SSHCommandFingerprint string    `json:"ssh_command_fingerprint,omitempty"`
	ProcessStartMarker    string    `json:"process_start_marker,omitempty"`
}

type ProcessIdentity struct {
	Command     string
	StartMarker string
}

type StateManager struct {
	Dir                string
	IsRunning          func(pid int) bool
	InspectProcess     func(pid int) (ProcessIdentity, error)
	TerminateProcess   func(State, func(State) error, func(int) error) error
	Preflight          func() error
	SyncDirectory      func(string) error
	CommandMatches     func(string, string) bool
	FingerprintMatches func(string, string) bool
}

func (s State) Matches(profile Profile) bool {
	if !s.matchesLegacyProfile(profile) {
		return false
	}
	identityFile, err := normalizedIdentityFile(profile.IdentityFile)
	if err != nil || s.IdentityFile != identityFile {
		return false
	}
	if len(s.Tunnels) != len(profile.Tunnels) {
		return false
	}
	for i, tunnel := range s.Tunnels {
		current := profile.Tunnels[i]
		if tunnel.LocalPort != current.LocalPort ||
			tunnel.RemoteHost != current.RemoteHost ||
			tunnel.RemotePort != current.RemotePort {
			return false
		}
	}
	return true
}

func (s State) matchesLegacyProfile(profile Profile) bool {
	if s.Profile != profile.Name || s.Target != fmt.Sprintf("%s@%s", profile.User, profile.Host) {
		return false
	}
	if len(s.Tunnels) != len(profile.Tunnels) {
		return false
	}
	for i, tunnel := range s.Tunnels {
		current := profile.Tunnels[i]
		if tunnel.LocalPort != current.LocalPort ||
			tunnel.RemoteHost != current.RemoteHost ||
			tunnel.RemotePort != current.RemotePort {
			return false
		}
	}
	return true
}

func NewStateManager(dir string) StateManager {
	return StateManager{
		Dir:              dir,
		IsRunning:        ProcessRunning,
		InspectProcess:   InspectProcess,
		TerminateProcess: terminateVerifiedProcess,
		Preflight:        tunnelLifecyclePreflight,
		SyncDirectory:    syncDirectory,
		CommandMatches: func(actual, expected string) bool {
			return normalizeSSHCommand(actual) == normalizeSSHCommand(expected)
		},
		FingerprintMatches: func(actual, fingerprint string) bool {
			return commandFingerprint(actual) == fingerprint
		},
	}
}

func (m StateManager) WithProfileLock(profile string, fn func() error) error {
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	lockPath := filepath.Join(dir, profile+".lock")
	value, _ := stateProfileLocks.LoadOrStore(lockPath, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	return withFileLock(lockPath, fn)
}

func (m StateManager) VerifyManagedProcess(state State) error {
	if state.SSHCommandFingerprint == "" {
		return legacyStateError("SSH command fingerprint")
	}
	if state.ProcessStartMarker == "" {
		return legacyStateError("process start marker")
	}
	if m.InspectProcess == nil {
		return errors.New("process identity inspection is unavailable")
	}
	identity, err := m.InspectProcess(state.PID)
	if err != nil {
		return fmt.Errorf("inspect pid %d: %w", state.PID, err)
	}
	if m.FingerprintMatches == nil {
		return errors.New("process command matching is unavailable")
	}
	if !m.FingerprintMatches(identity.Command, state.SSHCommandFingerprint) {
		return fmt.Errorf("pid %d command does not match the CM-managed SSH tunnel", state.PID)
	}
	if identity.StartMarker != state.ProcessStartMarker {
		return fmt.Errorf("pid %d start marker does not match the CM-managed SSH tunnel", state.PID)
	}
	return nil
}

func (m StateManager) VerifyExpectedManagedProcess(state State, sshArgs []string) error {
	expectedFingerprint := commandFingerprint(expectedSSHCommand(sshArgs))
	if state.SSHCommandFingerprint != expectedFingerprint {
		return fmt.Errorf("pid %d recorded command does not match the expected SSH tunnel command", state.PID)
	}
	return m.VerifyManagedProcess(state)
}

func (m StateManager) InspectExpectedProcess(pid int, sshArgs []string) (ProcessIdentity, error) {
	if m.InspectProcess == nil {
		return ProcessIdentity{}, errors.New("process identity inspection is unavailable")
	}
	identity, err := m.InspectProcess(pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("inspect pid %d: %w", pid, err)
	}
	expected := expectedSSHCommand(sshArgs)
	if m.CommandMatches == nil {
		return ProcessIdentity{}, errors.New("process command matching is unavailable")
	}
	if !m.CommandMatches(identity.Command, expected) {
		return ProcessIdentity{}, fmt.Errorf("pid %d command does not match expected SSH tunnel command", pid)
	}
	if identity.StartMarker == "" {
		return ProcessIdentity{}, fmt.Errorf("pid %d has an empty process start marker", pid)
	}
	return identity, nil
}

func (m StateManager) PreflightTunnelLifecycle() error {
	if m.Preflight == nil {
		return errors.New("tunnel lifecycle capability preflight is unavailable")
	}
	return m.Preflight()
}

func legacyStateError(missing string) error {
	return fmt.Errorf("legacy state has no %s and cannot be safely managed; manually verify and terminate the old SSH process, or remove the stale state only after the process is gone", missing)
}

func (m StateManager) TerminateManagedProcess(state State, stop func(int) error) error {
	if m.TerminateProcess == nil {
		return errors.New("verified process termination is unavailable")
	}
	return m.TerminateProcess(state, m.VerifyManagedProcess, stop)
}

func (m StateManager) Save(state State) error {
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, state.Profile+".json")
	temp, err := os.CreateTemp(dir, "."+state.Profile+".json.tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temporary state file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary state file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	cleanup = false
	if m.SyncDirectory == nil {
		return errors.New("state directory sync is unavailable")
	}
	if err := m.SyncDirectory(dir); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}

func (m StateManager) Load(profile string) (State, bool, error) {
	path, err := m.statePath(profile)
	if err != nil {
		return State{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, false, nil
		}
		return State{}, false, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

func (m StateManager) List() ([]State, error) {
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	states := []State{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		profile := entry.Name()[:len(entry.Name())-len(".json")]
		state, ok, err := m.Load(profile)
		if err != nil {
			return nil, err
		}
		if ok {
			if m.IsRunning != nil && !m.IsRunning(state.PID) {
				continue
			}
			if err := m.VerifyManagedProcess(state); err != nil {
				continue
			}
			states = append(states, state)
		}
	}
	return states, nil
}

func (m StateManager) Remove(profile string) error {
	path, err := m.statePath(profile)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.SyncDirectory == nil {
		return errors.New("state directory sync is unavailable")
	}
	if err := m.SyncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}

func (m StateManager) statePath(profile string) (string, error) {
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profile+".json"), nil
}

func NewState(profile Profile, pid int, identity ProcessIdentity) State {
	identityFile, _ := normalizedIdentityFile(profile.IdentityFile)
	sshArgs, _ := SSHArgs(profile)
	return State{
		Profile:               profile.Name,
		PID:                   pid,
		StartedAt:             time.Now(),
		Target:                fmt.Sprintf("%s@%s", profile.User, profile.Host),
		Tunnels:               profile.Tunnels,
		IdentityFile:          identityFile,
		SSHCommandFingerprint: commandFingerprint(expectedSSHCommand(sshArgs)),
		ProcessStartMarker:    identity.StartMarker,
	}
}

func normalizedIdentityFile(path string) (string, error) {
	expanded, err := ExpandPath(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve identity file path %s: %w", expanded, err)
	}
	return filepath.Clean(absolute), nil
}

func commandFingerprint(command string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(normalizeSSHCommand(command))))
}

func expectedSSHCommand(sshArgs []string) string {
	return normalizeSSHCommand(strings.Join(append([]string{"ssh"}, sshArgs...), " "))
}

func normalizeSSHCommand(command string) string {
	command = strings.TrimSpace(command)
	firstSpace := strings.IndexByte(command, ' ')
	executable := command
	rest := ""
	if firstSpace >= 0 {
		executable = command[:firstSpace]
		rest = command[firstSpace:]
	}
	if filepath.Base(executable) == "ssh" {
		executable = "ssh"
	}
	return executable + rest
}

func InspectProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("invalid pid %d", pid)
	}
	output, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ProcessIdentity{}, err
	}
	command := strings.TrimSpace(string(output))
	if command == "" {
		return ProcessIdentity{}, errors.New("empty process command")
	}
	startMarker, err := processStartMarker(pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read process start marker: %w", err)
	}
	return ProcessIdentity{Command: command, StartMarker: startMarker}, nil
}

func ProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return processSignalMeansRunning(err)
}

func processSignalMeansRunning(err error) bool {
	return err == nil || errors.Is(err, syscall.EPERM)
}
