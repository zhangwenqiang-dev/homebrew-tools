package connectmac

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `
profiles:
  xcode-vnc:
    description: Apple account: user@example.com
    user: user
    host: mac-host.example.com
    identity_file: ~/.ssh/example.pem
    sync:
      push:
        excludes:
          - xcuserdata
          - .svn
          - .git
          - .DS_Store
      pull:
        excludes:
          - .DS_Store
    vnc:
      username: mac-user
    aws:
      profile: cm-xcode
      region: us-west-2
      creator: Xiao Chen
      account_email: user@example.com
      ami:
        mac_x86: ami-0538568e5d3653bea
        mac_arm: ami-063755aadeb97329a
      key_name: example-key
      subnet_id: "<subnet-id>"
      security_group_id: "<security-group-id>"
      elastic_ip_allocation_id: "<elastic-ip-allocation-id>"
      elastic_ip_owner_tag:
        key: Apple
        value: user@example.com
      availability_zone_ids:
        - usw2-az1
        - usw2-az2
      instance_type_priority:
        - mac2.metal
        - mac2-m2.metal
      allow_intel_fallback: false
    tunnels:
      - local_port: 5900
        remote_host: localhost
        remote_port: 5900
`

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(sampleConfig)
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	profile, ok := cfg.Profile("xcode-vnc")
	if !ok {
		t.Fatal("expected profile xcode-vnc")
	}
	if profile.User != "user" {
		t.Fatalf("user = %q, want user", profile.User)
	}
	if profile.IdentityFile != "~/.ssh/example.pem" {
		t.Fatalf("identity_file = %q", profile.IdentityFile)
	}
	if len(profile.Tunnels) != 1 {
		t.Fatalf("tunnel count = %d, want 1", len(profile.Tunnels))
	}
	if len(profile.Sync.Push.Excludes) != 4 {
		t.Fatalf("push exclude count = %d, want 4", len(profile.Sync.Push.Excludes))
	}
	if len(profile.Sync.Pull.Excludes) != 1 {
		t.Fatalf("pull exclude count = %d, want 1", len(profile.Sync.Pull.Excludes))
	}
	if profile.VNC.Username != "mac-user" {
		t.Fatalf("vnc username = %q, want mac-user", profile.VNC.Username)
	}
	if profile.AWS.Profile != "cm-xcode" {
		t.Fatalf("aws profile = %q, want cm-xcode", profile.AWS.Profile)
	}
	if profile.AWS.Creator != "Xiao Chen" {
		t.Fatalf("aws creator = %q, want Xiao Chen", profile.AWS.Creator)
	}
	if profile.AWS.AMI.MacX86 != "ami-0538568e5d3653bea" {
		t.Fatalf("aws ami mac_x86 = %q", profile.AWS.AMI.MacX86)
	}
	if profile.AWS.AMI.MacARM != "ami-063755aadeb97329a" {
		t.Fatalf("aws ami mac_arm = %q", profile.AWS.AMI.MacARM)
	}
	if len(profile.AWS.AvailabilityZoneIDs) != 2 {
		t.Fatalf("availability zone count = %d, want 2", len(profile.AWS.AvailabilityZoneIDs))
	}
	if len(profile.AWS.InstanceTypePriority) != 2 {
		t.Fatalf("instance type priority count = %d, want 2", len(profile.AWS.InstanceTypePriority))
	}
	if profile.AWS.AllowIntelFallback {
		t.Fatal("allow_intel_fallback = true, want false")
	}
	if profile.Tunnels[0].LocalPort != 5900 || profile.Tunnels[0].RemoteHost != "localhost" || profile.Tunnels[0].RemotePort != 5900 {
		t.Fatalf("unexpected tunnel: %+v", profile.Tunnels[0])
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExpandPath("~/.ssh/key.pem")
	if err != nil {
		t.Fatalf("ExpandPath returned error: %v", err)
	}
	want := filepath.Join(home, ".ssh", "key.pem")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigMergesProfilesDirectory(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(`
profiles:
  base:
    description: Base profile
`), 0o600); err != nil {
		t.Fatal(err)
	}
	profilesDir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "extra.yaml"), []byte(`
profiles:
  extra:
    description: Extra profile
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(config)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if _, ok := cfg.Profile("base"); !ok {
		t.Fatal("expected base profile")
	}
	if _, ok := cfg.Profile("extra"); !ok {
		t.Fatal("expected extra profile from profiles dir")
	}
}

func TestLoadConfigRejectsDuplicateProfilesDirectoryProfile(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(`
profiles:
  duplicate:
    description: Main
`), 0o600); err != nil {
		t.Fatal(err)
	}
	profilesDir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "duplicate.yaml"), []byte(`
profiles:
  duplicate:
    description: Duplicate
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(config)
	if err == nil || !strings.Contains(err.Error(), "duplicate profile") {
		t.Fatalf("expected duplicate profile error, got %v", err)
	}
}

func TestLoadConfigDefaultsAWSUser(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(`
profiles:
  aws-only:
    aws:
      profile: website
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(config)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	profile, ok := cfg.Profile("aws-only")
	if !ok {
		t.Fatal("expected aws-only profile")
	}
	if profile.User != "ec2-user" {
		t.Fatalf("user = %q, want ec2-user", profile.User)
	}
	if profile.IdentityFile != "" {
		t.Fatalf("identity_file = %q, want empty", profile.IdentityFile)
	}
}

func TestLoadConfigDefaultsAWSHostFromElasticIP(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(`
profiles:
  west:
    aws:
      profile: website
      region: us-west-2
      elastic_ip_public_ip: 192.0.2.10
  east:
    aws:
      profile: website
      region: us-east-1
      elastic_ip_public_ip: 198.51.100.20
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(config)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	west, ok := cfg.Profile("west")
	if !ok {
		t.Fatal("expected west profile")
	}
	if west.Host != "ec2-192-0-2-10.us-west-2.compute.amazonaws.com" {
		t.Fatalf("west host = %q", west.Host)
	}
	east, ok := cfg.Profile("east")
	if !ok {
		t.Fatal("expected east profile")
	}
	if east.Host != "ec2-198-51-100-20.us-east-1.compute.amazonaws.com" {
		t.Fatalf("east host = %q", east.Host)
	}
}

func TestEC2HostFromPublicIPRegionRejectsInvalidInput(t *testing.T) {
	if got := EC2HostFromPublicIPRegion("not-an-ip", "us-west-2"); got != "" {
		t.Fatalf("host = %q, want empty", got)
	}
	if got := EC2HostFromPublicIPRegion("192.0.2.10", ""); got != "" {
		t.Fatalf("host = %q, want empty", got)
	}
}
