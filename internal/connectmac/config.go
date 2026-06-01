package connectmac

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultConfigPath = "~/.connectmac/config.yaml"

type Config struct {
	Profiles map[string]Profile
}

type Profile struct {
	Name         string
	Description  string
	User         string
	Host         string
	IdentityFile string
	Tunnels      []Tunnel
	Sync         SyncConfig
	VNC          VNCConfig
}

type VNCConfig struct {
	Username string
}

type SyncConfig struct {
	Push SyncDirection
	Pull SyncDirection
}

type SyncDirection struct {
	Excludes []string
}

type Tunnel struct {
	LocalPort  int
	RemoteHost string
	RemotePort int
}

func LoadConfig(path string) (Config, error) {
	expanded, err := ExpandPath(path)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(expanded)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", expanded, err)
	}
	cfg, err := ParseConfig(string(data))
	if err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", expanded, err)
	}
	return cfg, nil
}

func ParseConfig(data string) (Config, error) {
	cfg := Config{Profiles: map[string]Profile{}}
	var current *Profile
	var currentTunnel *Tunnel
	inProfiles := false
	inTunnels := false
	inSync := false
	syncDirection := ""
	inSyncExcludes := false
	inVNC := false

	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		indent := leadingSpaces(raw)
		line := strings.TrimSpace(raw)

		switch {
		case indent == 0 && line == "profiles:":
			inProfiles = true
			continue
		case indent == 0:
			return Config{}, fmt.Errorf("unsupported top-level key %q", strings.TrimSuffix(line, ":"))
		case inProfiles && indent == 2 && strings.HasSuffix(line, ":"):
			name := strings.TrimSuffix(line, ":")
			profile := Profile{Name: name}
			cfg.Profiles[name] = profile
			current = &profile
			currentTunnel = nil
			inTunnels = false
			inSync = false
			syncDirection = ""
			inSyncExcludes = false
			inVNC = false
			continue
		}

		if current == nil {
			return Config{}, fmt.Errorf("profile field before profile name: %q", line)
		}

		if indent == 4 && line == "tunnels:" {
			inTunnels = true
			inSync = false
			syncDirection = ""
			inSyncExcludes = false
			inVNC = false
			currentTunnel = nil
			continue
		}

		if indent == 4 && line == "sync:" {
			if currentTunnel != nil {
				current.Tunnels = append(current.Tunnels, *currentTunnel)
				currentTunnel = nil
			}
			inTunnels = false
			inSync = true
			syncDirection = ""
			inSyncExcludes = false
			inVNC = false
			cfg.Profiles[current.Name] = *current
			continue
		}

		if indent == 4 && line == "vnc:" {
			if currentTunnel != nil {
				current.Tunnels = append(current.Tunnels, *currentTunnel)
				currentTunnel = nil
			}
			inTunnels = false
			inSync = false
			syncDirection = ""
			inSyncExcludes = false
			inVNC = true
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inSync && indent == 6 && (line == "push:" || line == "pull:") {
			syncDirection = strings.TrimSuffix(line, ":")
			inSyncExcludes = false
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inSync && indent == 8 && line == "excludes:" {
			if syncDirection == "" {
				return Config{}, fmt.Errorf("sync excludes before push or pull")
			}
			inSyncExcludes = true
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inSync && indent == 8 && line == "excludes: []" {
			if syncDirection == "" {
				return Config{}, fmt.Errorf("sync excludes before push or pull")
			}
			inSyncExcludes = false
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inTunnels && indent == 6 && strings.HasPrefix(line, "- ") {
			if currentTunnel != nil {
				current.Tunnels = append(current.Tunnels, *currentTunnel)
			}
			currentTunnel = &Tunnel{}
			field := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if field != "" {
				if err := applyTunnelField(currentTunnel, field); err != nil {
					return Config{}, err
				}
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inTunnels && indent == 8 {
			if currentTunnel == nil {
				return Config{}, fmt.Errorf("tunnel field before tunnel item: %q", line)
			}
			if err := applyTunnelField(currentTunnel, line); err != nil {
				return Config{}, err
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inSync && inSyncExcludes && indent == 10 && strings.HasPrefix(line, "- ") {
			item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "- ")), `"'`)
			if item == "" {
				return Config{}, fmt.Errorf("sync %s excludes item cannot be empty", syncDirection)
			}
			switch syncDirection {
			case "push":
				current.Sync.Push.Excludes = append(current.Sync.Push.Excludes, item)
			case "pull":
				current.Sync.Pull.Excludes = append(current.Sync.Pull.Excludes, item)
			default:
				return Config{}, fmt.Errorf("unsupported sync direction %q", syncDirection)
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inVNC && indent == 6 {
			if err := applyVNCField(current, line); err != nil {
				return Config{}, err
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		if indent == 4 {
			if currentTunnel != nil {
				current.Tunnels = append(current.Tunnels, *currentTunnel)
				currentTunnel = nil
			}
			inTunnels = false
			inSync = false
			syncDirection = ""
			inSyncExcludes = false
			inVNC = false
			if err := applyProfileField(current, line); err != nil {
				return Config{}, err
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		return Config{}, fmt.Errorf("unsupported line: %q", line)
	}

	if err := scanner.Err(); err != nil {
		return Config{}, err
	}
	if current != nil && currentTunnel != nil {
		current.Tunnels = append(current.Tunnels, *currentTunnel)
		cfg.Profiles[current.Name] = *current
	}
	return cfg, nil
}

func ExpandPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func (c Config) Profile(name string) (Profile, bool) {
	p, ok := c.Profiles[name]
	return p, ok
}

func applyVNCField(p *Profile, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "username":
		p.VNC.Username = value
	default:
		return fmt.Errorf("unsupported vnc field %q", key)
	}
	return nil
}

func applyProfileField(p *Profile, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "description":
		p.Description = value
	case "user":
		p.User = value
	case "host":
		p.Host = value
	case "identity_file":
		p.IdentityFile = value
	default:
		return fmt.Errorf("unsupported profile field %q", key)
	}
	return nil
}

func applyTunnelField(t *Tunnel, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "local_port":
		t.LocalPort, err = strconv.Atoi(value)
	case "remote_host":
		t.RemoteHost = value
	case "remote_port":
		t.RemotePort, err = strconv.Atoi(value)
	default:
		return fmt.Errorf("unsupported tunnel field %q", key)
	}
	if err != nil {
		return fmt.Errorf("%s must be a number", key)
	}
	return nil
}

func splitField(line string) (string, string, error) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", fmt.Errorf("expected key/value field, got %q", line)
	}
	key = strings.TrimSpace(key)
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	if key == "" {
		return "", "", fmt.Errorf("field key is empty")
	}
	return key, value, nil
}

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}
