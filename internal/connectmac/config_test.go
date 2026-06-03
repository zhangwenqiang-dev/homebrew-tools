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
      creator: xc
      creator_name: Xiao Chen
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
	if profile.AWS.Creator != "xc" {
		t.Fatalf("aws creator = %q, want xc", profile.AWS.Creator)
	}
	if profile.AWS.CreatorName != "Xiao Chen" {
		t.Fatalf("aws creator_name = %q, want Xiao Chen", profile.AWS.CreatorName)
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
