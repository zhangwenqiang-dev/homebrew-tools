package connectmac

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const DefaultConfigPath = "~/.connectmac/config.yaml"
const DefaultAWSUser = "ec2-user"

type Config struct {
	Defaults ProfileDefaults
	Profiles map[string]Profile
}

type ProfileDefaults struct {
	User         string
	IdentityFile string
	AWS          AWSDefaults
}

type AWSDefaults struct {
	Creator string
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
	AWS          AWSConfig
}

type VNCConfig struct {
	Username string
}

type AWSConfig struct {
	Profile               string
	Region                string
	ShortName             string
	ResourceName          string
	Creator               string
	AccountEmail          string
	AMI                   AWSAMIConfig
	KeyName               string
	SubnetID              string
	SecurityGroupID       string
	ElasticIPAllocationID string
	ElasticIPPublicIP     string
	ElasticIPOwnerTag     AWSTagConfig
	AvailabilityZoneIDs   []string
	InstanceTypePriority  []string
	AllowIntelFallback    bool
}

type AWSAMIConfig struct {
	MacX86 string
	MacARM string
}

type AWSTagConfig struct {
	Key   string
	Value string
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
	profilesDir := filepath.Join(filepath.Dir(expanded), "profiles")
	dirCfg, err := LoadProfilesDir(profilesDir)
	if err != nil {
		return Config{}, err
	}
	if err := mergeConfigs(&cfg, dirCfg, profilesDir); err != nil {
		return Config{}, err
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

func LoadProfilesDir(dir string) (Config, error) {
	cfg := Config{Profiles: map[string]Profile{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read profiles dir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read profile file %s: %w", path, err)
		}
		fileCfg, err := ParseConfig(string(data))
		if err != nil {
			return Config{}, fmt.Errorf("parse profile file %s: %w", path, err)
		}
		if err := mergeConfigs(&cfg, fileCfg, path); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func mergeConfigs(dst *Config, src Config, source string) error {
	if dst.Profiles == nil {
		dst.Profiles = map[string]Profile{}
	}
	if err := mergeDefaults(&dst.Defaults, src.Defaults, source); err != nil {
		return err
	}
	for name, profile := range src.Profiles {
		if _, exists := dst.Profiles[name]; exists {
			return fmt.Errorf("duplicate profile %q in %s", name, source)
		}
		dst.Profiles[name] = profile
	}
	return nil
}

func mergeDefaults(dst *ProfileDefaults, src ProfileDefaults, source string) error {
	if err := mergeDefaultString(&dst.User, src.User, "user", source); err != nil {
		return err
	}
	if err := mergeDefaultString(&dst.IdentityFile, src.IdentityFile, "identity_file", source); err != nil {
		return err
	}
	return mergeDefaultString(&dst.AWS.Creator, src.AWS.Creator, "aws.creator", source)
}

func mergeDefaultString(dst *string, src, name, source string) error {
	if src == "" {
		return nil
	}
	if *dst != "" && *dst != src {
		return fmt.Errorf("conflicting default %q in %s", name, source)
	}
	*dst = src
	return nil
}

func ParseConfig(data string) (Config, error) {
	cfg := Config{Profiles: map[string]Profile{}}
	var current *Profile
	var currentTunnel *Tunnel
	inDefaults := false
	inDefaultsAWS := false
	inProfiles := false
	inTunnels := false
	inSync := false
	syncDirection := ""
	inSyncExcludes := false
	inVNC := false
	inAWS := false
	inAWSAMI := false
	inAWSEIPOwnerTag := false
	awsList := ""

	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		indent := leadingSpaces(raw)
		line := strings.TrimSpace(raw)

		switch {
		case indent == 0 && line == "defaults:":
			inDefaults = true
			inProfiles = false
			current = nil
			currentTunnel = nil
			inDefaultsAWS = false
			inTunnels = false
			inSync = false
			syncDirection = ""
			inSyncExcludes = false
			inVNC = false
			inAWS = false
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
			continue
		case indent == 0 && line == "profiles:":
			inDefaults = false
			inDefaultsAWS = false
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
			inAWS = false
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
			continue
		}

		if inDefaults && indent == 2 && line == "aws:" {
			inDefaultsAWS = true
			continue
		}

		if inDefaults && inDefaultsAWS && indent == 4 {
			if err := applyDefaultAWSField(&cfg.Defaults.AWS, line); err != nil {
				return Config{}, err
			}
			continue
		}

		if inDefaults && indent == 2 {
			inDefaultsAWS = false
			if err := applyDefaultField(&cfg.Defaults, line); err != nil {
				return Config{}, err
			}
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
			inAWS = false
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
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
			inAWS = false
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
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
			inAWS = false
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
			cfg.Profiles[current.Name] = *current
			continue
		}

		if indent == 4 && line == "aws:" {
			if currentTunnel != nil {
				current.Tunnels = append(current.Tunnels, *currentTunnel)
				currentTunnel = nil
			}
			inTunnels = false
			inSync = false
			syncDirection = ""
			inSyncExcludes = false
			inVNC = false
			inAWS = true
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
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

		if inAWS && indent == 6 && line == "ami:" {
			inAWSAMI = true
			inAWSEIPOwnerTag = false
			awsList = ""
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inAWS && indent == 6 && line == "elastic_ip_owner_tag:" {
			inAWSAMI = false
			inAWSEIPOwnerTag = true
			awsList = ""
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inAWS && indent == 6 && (line == "availability_zone_ids:" || line == "instance_type_priority:") {
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = strings.TrimSuffix(line, ":")
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inAWS && indent == 6 {
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
			if err := applyAWSField(current, line); err != nil {
				return Config{}, err
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inAWS && inAWSAMI && indent == 8 {
			if err := applyAWSAMIField(current, line); err != nil {
				return Config{}, err
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inAWS && inAWSEIPOwnerTag && indent == 8 {
			if err := applyAWSTagField(&current.AWS.ElasticIPOwnerTag, line); err != nil {
				return Config{}, err
			}
			cfg.Profiles[current.Name] = *current
			continue
		}

		if inAWS && awsList != "" && indent == 8 && strings.HasPrefix(line, "- ") {
			item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "- ")), `"'`)
			if item == "" {
				return Config{}, fmt.Errorf("aws %s item cannot be empty", awsList)
			}
			switch awsList {
			case "availability_zone_ids":
				current.AWS.AvailabilityZoneIDs = append(current.AWS.AvailabilityZoneIDs, item)
			case "instance_type_priority":
				current.AWS.InstanceTypePriority = append(current.AWS.InstanceTypePriority, item)
			default:
				return Config{}, fmt.Errorf("unsupported aws list %q", awsList)
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
			inAWS = false
			inAWSAMI = false
			inAWSEIPOwnerTag = false
			awsList = ""
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

func NormalizeIdentityFileInput(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "/") || value == "~" {
		return value
	}
	if !strings.HasSuffix(value, ".pem") {
		value += ".pem"
	}
	return "~/.ssh/" + value
}

func (c Config) Profile(name string) (Profile, bool) {
	p, ok := c.Profiles[name]
	if ok {
		p.ApplyDefaults(c.Defaults)
	}
	return p, ok
}

func (c *Config) ApplyDefaults() {
	for name, profile := range c.Profiles {
		profile.ApplyDefaults(c.Defaults)
		c.Profiles[name] = profile
	}
}

func (p *Profile) ApplyDefaults(defaults ProfileDefaults) {
	if p.User == "" {
		p.User = defaults.User
	}
	if p.IdentityFile == "" {
		p.IdentityFile = defaults.IdentityFile
	}
	if p.AWS.Creator == "" {
		p.AWS.Creator = defaults.AWS.Creator
	}
	if p.User == "" && p.AWS.Profile != "" {
		p.User = DefaultAWSUser
	}
	if p.Host == "" && p.AWS.Profile != "" {
		p.Host = EC2HostFromPublicIPRegion(p.AWS.ElasticIPPublicIP, p.AWS.Region)
	}
}

func EC2HostFromPublicIPRegion(publicIP, region string) string {
	if publicIP == "" || region == "" {
		return ""
	}
	ip := net.ParseIP(publicIP)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	dashedIP := strings.ReplaceAll(publicIP, ".", "-")
	return fmt.Sprintf("ec2-%s.%s.compute.amazonaws.com", dashedIP, region)
}

func applyDefaultField(defaults *ProfileDefaults, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "user":
		defaults.User = value
	case "identity_file":
		defaults.IdentityFile = value
	default:
		return fmt.Errorf("unsupported defaults field %q", key)
	}
	return nil
}

func applyDefaultAWSField(defaults *AWSDefaults, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "creator":
		defaults.Creator = value
	default:
		return fmt.Errorf("unsupported defaults aws field %q", key)
	}
	return nil
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

func applyAWSField(p *Profile, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "profile":
		p.AWS.Profile = value
	case "region":
		p.AWS.Region = value
	case "short_name":
		p.AWS.ShortName = value
	case "resource_name":
		p.AWS.ResourceName = value
	case "creator":
		p.AWS.Creator = value
	case "creator_name":
		// Legacy no-op. Use creator for the full creator display name.
	case "creator_date", "creator_time":
		// Legacy no-op. AWS already records resource creation and launch times.
	case "account_email":
		p.AWS.AccountEmail = value
	case "key_name":
		p.AWS.KeyName = value
	case "subnet_id":
		p.AWS.SubnetID = value
	case "security_group_id":
		p.AWS.SecurityGroupID = value
	case "elastic_ip_allocation_id":
		p.AWS.ElasticIPAllocationID = value
	case "elastic_ip_public_ip":
		p.AWS.ElasticIPPublicIP = value
	case "allow_intel_fallback":
		p.AWS.AllowIntelFallback, err = strconv.ParseBool(value)
	default:
		return fmt.Errorf("unsupported aws field %q", key)
	}
	if err != nil {
		return fmt.Errorf("%s must be true or false", key)
	}
	return nil
}

func applyAWSAMIField(p *Profile, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "mac_x86":
		p.AWS.AMI.MacX86 = value
	case "mac_arm":
		p.AWS.AMI.MacARM = value
	default:
		return fmt.Errorf("unsupported aws ami field %q", key)
	}
	return nil
}

func applyAWSTagField(tag *AWSTagConfig, line string) error {
	key, value, err := splitField(line)
	if err != nil {
		return err
	}
	switch key {
	case "key":
		tag.Key = value
	case "value":
		tag.Value = value
	default:
		return fmt.Errorf("unsupported aws tag field %q", key)
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
