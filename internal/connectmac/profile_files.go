package connectmac

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func ConfigDir(configPath string) (string, error) {
	path, err := ExpandPath(configPath)
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func ProfilesDir(configPath string) (string, error) {
	dir, err := ConfigDir(configPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "profiles"), nil
}

func ProfileFilePath(configPath, profileName string) (string, error) {
	dir, err := ProfilesDir(configPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profileName+".yaml"), nil
}

func WriteProfileFile(configPath string, profile Profile) (string, error) {
	if profile.Name == "" {
		return "", fmt.Errorf("profile name is required")
	}
	path, err := ProfileFilePath(configPath, profile.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create profiles dir: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("profile file already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return path, os.WriteFile(path, []byte(FormatProfileFile(profile)), 0o600)
}

func OverwriteProfileFile(configPath string, profile Profile) (string, error) {
	if profile.Name == "" {
		return "", fmt.Errorf("profile name is required")
	}
	path, err := ProfileFilePath(configPath, profile.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create profiles dir: %w", err)
	}
	return path, os.WriteFile(path, []byte(FormatProfileFile(profile)), 0o600)
}

func RemoveProfileFile(configPath, profileName string) (string, error) {
	path, err := ProfileFilePath(configPath, profileName)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("profile %q is not managed as %s", profileName, path)
		}
		return "", err
	}
	return path, nil
}

func RenameProfileFile(configPath, oldName, newName string) (string, string, error) {
	oldPath, err := ProfileFilePath(configPath, oldName)
	if err != nil {
		return "", "", err
	}
	newPath, err := ProfileFilePath(configPath, newName)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(newPath); err == nil {
		return "", "", fmt.Errorf("profile file already exists: %s", newPath)
	}
	data, err := os.ReadFile(oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("profile %q is not managed as %s", oldName, oldPath)
		}
		return "", "", err
	}
	cfg, err := ParseConfig(string(data))
	if err != nil {
		return "", "", err
	}
	profile, ok := cfg.Profiles[oldName]
	if !ok {
		return "", "", fmt.Errorf("profile file %s does not contain profile %q", oldPath, oldName)
	}
	profile.Name = newName
	if err := os.WriteFile(newPath, []byte(FormatProfileFile(profile)), 0o600); err != nil {
		return "", "", err
	}
	if err := os.Remove(oldPath); err != nil {
		return "", "", err
	}
	return oldPath, newPath, nil
}

func ImportProfileFile(configPath, sourcePath string) ([]string, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	cfg, err := ParseConfig(string(data))
	if err != nil {
		return nil, err
	}
	if len(cfg.Profiles) == 0 {
		return nil, fmt.Errorf("no profiles found in %s", sourcePath)
	}
	names := sortedProfileNames(cfg)
	var written []string
	for _, name := range names {
		profile := cfg.Profiles[name]
		path, err := WriteProfileFile(configPath, profile)
		if err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

func ImportProfileDir(configPath, sourceDir string) ([]string, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			files = append(files, filepath.Join(sourceDir, name))
		}
	}
	sort.Strings(files)
	var written []string
	for _, file := range files {
		paths, err := ImportProfileFile(configPath, file)
		if err != nil {
			return written, err
		}
		written = append(written, paths...)
	}
	return written, nil
}

func OpenProfileInEditor(path string) error {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("VISUAL"))
	}
	if editor == "" {
		return fmt.Errorf("EDITOR is not set")
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func FormatProfileFile(profile Profile) string {
	var b strings.Builder
	fmt.Fprintln(&b, "profiles:")
	writeProfileYAML(&b, profile, "  ")
	return b.String()
}

func writeProfileYAML(b *strings.Builder, profile Profile, indent string) {
	fmt.Fprintf(b, "%s%s:\n", indent, profile.Name)
	writeStringField(b, indent+"  ", "description", profile.Description)
	writeStringField(b, indent+"  ", "user", profile.User)
	writeStringField(b, indent+"  ", "host", profile.Host)
	writeStringField(b, indent+"  ", "identity_file", profile.IdentityFile)
	writeSyncYAML(b, profile.Sync, indent+"  ")
	writeVNCYAML(b, profile.VNC, indent+"  ")
	writeAWSYAML(b, profile.AWS, indent+"  ")
	writeTunnelsYAML(b, profile.Tunnels, indent+"  ")
}

func writeSyncYAML(b *strings.Builder, sync SyncConfig, indent string) {
	if emptySyncDirection(sync.Push) && emptySyncDirection(sync.Pull) {
		return
	}
	fmt.Fprintf(b, "%ssync:\n", indent)
	writeSyncDirectionYAML(b, "push", sync.Push, indent+"  ")
	writeSyncDirectionYAML(b, "pull", sync.Pull, indent+"  ")
}

func writeSyncDirectionYAML(b *strings.Builder, name string, direction SyncDirection, indent string) {
	if emptySyncDirection(direction) {
		return
	}
	fmt.Fprintf(b, "%s%s:\n", indent, name)
	writeStringList(b, indent+"  ", "includes", direction.Includes)
	writeStringList(b, indent+"  ", "excludes", direction.Excludes)
}

func emptySyncDirection(direction SyncDirection) bool {
	return len(direction.Includes) == 0 && len(direction.Excludes) == 0
}

func writeVNCYAML(b *strings.Builder, vnc VNCConfig, indent string) {
	if vnc.Username == "" {
		return
	}
	fmt.Fprintf(b, "%svnc:\n", indent)
	writeStringField(b, indent+"  ", "username", vnc.Username)
}

func writeAWSYAML(b *strings.Builder, aws AWSConfig, indent string) {
	if emptyAWSConfig(aws) {
		return
	}
	fmt.Fprintf(b, "%saws:\n", indent)
	writeStringField(b, indent+"  ", "profile", aws.Profile)
	writeStringField(b, indent+"  ", "region", aws.Region)
	writeStringField(b, indent+"  ", "short_name", aws.ShortName)
	writeStringField(b, indent+"  ", "resource_name", aws.ResourceName)
	writeStringField(b, indent+"  ", "creator", aws.Creator)
	writeStringField(b, indent+"  ", "account_email", aws.AccountEmail)
	if aws.AMI.MacX86 != "" || aws.AMI.MacARM != "" {
		fmt.Fprintf(b, "%s  ami:\n", indent)
		writeStringField(b, indent+"    ", "mac_x86", aws.AMI.MacX86)
		writeStringField(b, indent+"    ", "mac_arm", aws.AMI.MacARM)
	}
	writeStringField(b, indent+"  ", "key_name", aws.KeyName)
	writeStringField(b, indent+"  ", "subnet_id", aws.SubnetID)
	if len(aws.SubnetsByAZ) > 0 {
		fmt.Fprintf(b, "%s  subnets_by_az:\n", indent)
		keys := make([]string, 0, len(aws.SubnetsByAZ))
		for key := range aws.SubnetsByAZ {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			writeStringField(b, indent+"    ", key, aws.SubnetsByAZ[key])
		}
	}
	writeStringField(b, indent+"  ", "security_group_id", aws.SecurityGroupID)
	writeStringField(b, indent+"  ", "elastic_ip_allocation_id", aws.ElasticIPAllocationID)
	writeStringField(b, indent+"  ", "elastic_ip_public_ip", aws.ElasticIPPublicIP)
	if aws.ElasticIPOwnerTag.Key != "" || aws.ElasticIPOwnerTag.Value != "" {
		fmt.Fprintf(b, "%s  elastic_ip_owner_tag:\n", indent)
		writeStringField(b, indent+"    ", "key", aws.ElasticIPOwnerTag.Key)
		writeStringField(b, indent+"    ", "value", aws.ElasticIPOwnerTag.Value)
	}
	writeStringList(b, indent+"  ", "availability_zone_ids", aws.AvailabilityZoneIDs)
	writeStringList(b, indent+"  ", "instance_type_priority", aws.InstanceTypePriority)
	if aws.AllowIntelFallback {
		fmt.Fprintf(b, "%s  allow_intel_fallback: true\n", indent)
	}
}

func emptyAWSConfig(aws AWSConfig) bool {
	return aws.Profile == "" && aws.Region == "" && aws.AccountEmail == "" &&
		aws.KeyName == "" && aws.SecurityGroupID == "" && aws.ElasticIPAllocationID == ""
}

func writeTunnelsYAML(b *strings.Builder, tunnels []Tunnel, indent string) {
	if len(tunnels) == 0 {
		return
	}
	fmt.Fprintf(b, "%stunnels:\n", indent)
	for _, tunnel := range tunnels {
		fmt.Fprintf(b, "%s  - local_port: %d\n", indent, tunnel.LocalPort)
		writeStringField(b, indent+"    ", "remote_host", tunnel.RemoteHost)
		fmt.Fprintf(b, "%s    remote_port: %d\n", indent, tunnel.RemotePort)
	}
}

func writeStringField(b *strings.Builder, indent, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s%s: %s\n", indent, key, quoteYAMLString(value))
}

func writeStringList(b *strings.Builder, indent, key string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s%s:\n", indent, key)
	for _, value := range values {
		fmt.Fprintf(b, "%s  - %s\n", indent, quoteYAMLString(value))
	}
}

func quoteYAMLString(value string) string {
	if value == "" {
		return `""`
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
