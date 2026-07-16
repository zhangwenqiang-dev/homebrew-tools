package connectmac

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const DefaultStateDir = "~/.connectmac/state"

type State struct {
	Profile   string    `json:"profile"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Target    string    `json:"target"`
	Tunnels   []Tunnel  `json:"tunnels"`
}

type StateManager struct {
	Dir       string
	IsRunning func(pid int) bool
}

func (s State) Matches(profile Profile) bool {
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
	return StateManager{Dir: dir, IsRunning: ProcessRunning}
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
	return os.WriteFile(path, data, 0o600)
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
	if m.IsRunning != nil && !m.IsRunning(state.PID) {
		_ = os.Remove(path)
		return State{}, false, nil
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
	return err
}

func (m StateManager) statePath(profile string) (string, error) {
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profile+".json"), nil
}

func NewState(profile Profile, pid int) State {
	return State{
		Profile:   profile.Name,
		PID:       pid,
		StartedAt: time.Now(),
		Target:    fmt.Sprintf("%s@%s", profile.User, profile.Host),
		Tunnels:   profile.Tunnels,
	}
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
