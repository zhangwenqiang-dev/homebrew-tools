package connectmac

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	foreground []string
	background []string
	startErr   error
	rsync      []string
	forgotHost string
	knownHost  string
	scannedKey string
	scanErr    error
	openedURL  string
	stopPID    int
}

func (r *fakeRunner) RunForeground(ctx context.Context, args []string) error {
	r.foreground = args
	return nil
}

func (r *fakeRunner) StartBackground(ctx context.Context, args []string) (int, error) {
	r.background = args
	if r.startErr != nil {
		return 0, r.startErr
	}
	return 55, nil
}

func (r *fakeRunner) Stop(pid int) error {
	r.stopPID = pid
	return nil
}

func (r *fakeRunner) RunRsync(ctx context.Context, args []string) error {
	r.rsync = args
	return nil
}

func (r *fakeRunner) KnownHostKey(ctx context.Context, host string) (string, error) {
	return r.knownHost, nil
}

func (r *fakeRunner) ScanHostKey(ctx context.Context, host string) (string, error) {
	if r.scanErr != nil {
		return r.scannedKey, r.scanErr
	}
	if r.scannedKey == "" {
		return "mac-host.example.com ssh-ed25519 AAAACURRENT\n", nil
	}
	return r.scannedKey, nil
}

func (r *fakeRunner) ForgetHost(ctx context.Context, host string) error {
	r.forgotHost = host
	return nil
}

func (r *fakeRunner) OpenURL(ctx context.Context, target string) error {
	r.openedURL = target
	return nil
}

func TestAppInitAndList(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"init", "--config", config}); code != 0 {
		t.Fatalf("init code = %d, err = %s", code, errOut.String())
	}
	if _, err := os.Stat(config); err != nil {
		t.Fatalf("expected config to be created: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"list", "--config", config}); code != 0 {
		t.Fatalf("list code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "xcode-vnc") {
		t.Fatalf("list output = %q", out.String())
	}
	if !strings.Contains(out.String(), "PROFILE") || !strings.Contains(out.String(), "DESCRIPTION") {
		t.Fatalf("list output missing table header = %q", out.String())
	}
}

func TestAppListFormatsProfilesAsTable(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"short": {
			Name:        "short",
			Description: "Apple account: user@example.com",
		},
		"long-profile-name": {
			Name: "long-profile-name",
		},
	}}
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.runList(cfg); code != 0 {
		t.Fatalf("list code = %d", code)
	}
	text := out.String()
	for _, want := range []string{"PROFILE", "DESCRIPTION", "long-profile-name  -", "short"} {
		if !strings.Contains(text, want) {
			t.Fatalf("list output missing %q: %q", want, text)
		}
	}
}

func TestAppVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	app.Version = "0.1.test"
	if code := app.Run(context.Background(), []string{"version"}); code != 0 {
		t.Fatalf("version code = %d, err = %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "cm 0.1.test" {
		t.Fatalf("version output = %q", out.String())
	}
	out.Reset()
	if code := app.Run(context.Background(), []string{"--version"}); code != 0 {
		t.Fatalf("--version code = %d, err = %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "cm 0.1.test" {
		t.Fatalf("--version output = %q", out.String())
	}
}

func TestAppMCPHelpExplainsStdioServer(t *testing.T) {
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"mcp", "--help"}); code != 0 {
		t.Fatalf("mcp help code = %d, err = %s", code, errOut.String())
	}
	for _, want := range []string{"cm mcp tools", "stdio MCP server", "does not print a tool list"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("mcp help missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppMCPToolsHumanReadable(t *testing.T) {
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"mcp", "tools"}); code != 0 {
		t.Fatalf("mcp tools code = %d, err = %s", code, errOut.String())
	}
	for _, want := range []string{"TOOL", "DESCRIPTION", "REQUIRED", "KEY PARAMS", "cm_mcp_guide", "cm_list_profiles", "cm_aws_destroy_mac_by_email", "apple_email"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("mcp tools missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppMCPToolsJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"mcp", "tools", "--json"}); code != 0 {
		t.Fatalf("mcp tools --json code = %d, err = %s", code, errOut.String())
	}
	var payload struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	if len(payload.Tools) != len(mcpTools()) {
		t.Fatalf("tool count = %d, want %d", len(payload.Tools), len(mcpTools()))
	}
	found := false
	for _, tool := range payload.Tools {
		if tool.Name == "cm_push" {
			found = true
			required, _ := tool.InputSchema["required"].([]interface{})
			if len(required) != 3 {
				t.Fatalf("cm_push required = %#v", required)
			}
		}
	}
	if !found {
		t.Fatalf("cm_push not found in tools: %#v", payload.Tools)
	}
}

func TestAppGuideShowsStepByStepTopics(t *testing.T) {
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"guide"}); code != 0 {
		t.Fatalf("guide code = %d, err = %s", code, errOut.String())
	}
	for _, want := range []string{"ConnectMac guide", "cm profile wizard", "cm next <profile-or-apple-email>", "AWS mutations always preview first"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("guide output missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"guide", "open"}); code != 0 {
		t.Fatalf("guide open code = %d, err = %s", code, errOut.String())
	}
	for _, want := range []string{"ConnectMac open-Mac guide", "cm aws open <apple-email>", "blocked: stop"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("guide open missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppCompletionProfiles(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"completion", "profiles", "--config", config}); code != 0 {
		t.Fatalf("completion profiles code = %d, err = %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "xcode-vnc" {
		t.Fatalf("profiles output = %q", out.String())
	}
}

func TestAppMemberCommands(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"member", "add", "--name", "王恒辉", "--email", "whh@example.com"}); code != 0 {
		t.Fatalf("member add code = %d, err = %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"member", "assign", "apple@example.com", "--member", "whh@example.com"}); code != 0 {
		t.Fatalf("member assign code = %d, err = %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"member", "list"}); code != 0 {
		t.Fatalf("member list code = %d, err = %s", code, errOut.String())
	}
	for _, want := range []string{"EMAIL", "whh@example.com", "王恒辉", "apple@example.com(owner)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("member list missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppWebProfilesAPI(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if _, err := app.MemberStore.AddMember("王恒辉", "whh@example.com", "operator"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := app.MemberStore.AssignMember("user@example.com", "whh@example.com", "owner"); err != nil {
		t.Fatalf("assign member: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp webAPIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not ok: %#v", resp)
	}
	if !strings.Contains(rec.Body.String(), "xcode-vnc") || !strings.Contains(rec.Body.String(), "user@example.com") || !strings.Contains(rec.Body.String(), "whh@example.com") {
		t.Fatalf("profiles body = %s", rec.Body.String())
	}
}

func TestAppWebProfileOwnerAPI(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if _, err := app.MemberStore.AddMember("旧负责人", "old@example.com", "operator"); err != nil {
		t.Fatalf("add old member: %v", err)
	}
	if _, err := app.MemberStore.AddMember("王恒辉", "whh@example.com", "operator"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := app.MemberStore.AssignMember("user@example.com", "old@example.com", "owner"); err != nil {
		t.Fatalf("assign old member: %v", err)
	}

	body := strings.NewReader(`{"profile":"xcode-vnc","member_email":"whh@example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/profile-owner/set", body)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"profile_name":"xcode-vnc"`) || !strings.Contains(rec.Body.String(), "whh@example.com") {
		t.Fatalf("profile owner set body = %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "whh@example.com") || strings.Contains(rec.Body.String(), "old@example.com") {
		t.Fatalf("profiles body = %s", rec.Body.String())
	}
}

func TestAppWebManagedProfilesAccessAPI(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)

	profileYAML := `profiles:
  managed-usw2:
    description: Apple account: managed@example.com
    host: ec2-1-2-3-4.us-west-2.compute.amazonaws.com
    aws:
      profile: cm-xcode
      region: us-west-2
      account_email: managed@example.com
`
	body := strings.NewReader(`{"profile_yaml":` + strconv.Quote(profileYAML) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/api/managed-profile/save", body)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if _, err := app.MemberStore.AddMember("User", "user@example.com", "operator"); err != nil {
		t.Fatalf("add user: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/managed-profiles?include_yaml=1", nil)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("member list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "managed-usw2") {
		t.Fatalf("ungranted member should not see profile: %s", rec.Body.String())
	}

	body = strings.NewReader(`{"profile":"managed-usw2","member_email":"admin@example.com","grant":true}`)
	req = httptest.NewRequest(http.MethodPost, "/api/managed-profile/access", body)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant status = %d, body = %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/managed-profiles?include_yaml=1", nil)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "managed-usw2") {
		t.Fatalf("granted member list body = %s status=%d", rec.Body.String(), rec.Code)
	}

	body = strings.NewReader(`{"profile":"managed-usw2","enabled":false}`)
	req = httptest.NewRequest(http.MethodPost, "/api/managed-profile/status", body)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/managed-profiles?include_yaml=1", nil)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled member list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "managed-usw2") {
		t.Fatalf("disabled profile should be hidden from member: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/managed-profiles?include_yaml=1", nil)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "managed-usw2") || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("admin should see disabled profile body = %s status=%d", rec.Body.String(), rec.Code)
	}

	body = strings.NewReader(`{"profile":"managed-usw2","enabled":true}`)
	req = httptest.NewRequest(http.MethodPost, "/api/managed-profile/status", body)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/managed-profiles?include_yaml=1", nil)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "managed-usw2") {
		t.Fatalf("re-enabled member list body = %s status=%d", rec.Body.String(), rec.Code)
	}
}

func TestAppWebMemberProfilesAPIReplacesMemberAccess(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)

	for _, profileYAML := range []string{
		`profiles:
  first-usw2:
    description: Apple account: first@example.com
    host: ec2-1-2-3-4.us-west-2.compute.amazonaws.com
    aws:
      profile: cm-xcode
      region: us-west-2
      account_email: first@example.com
`,
		`profiles:
  second-usw2:
    description: Apple account: second@example.com
    host: ec2-5-6-7-8.us-west-2.compute.amazonaws.com
    aws:
      profile: cm-xcode
      region: us-west-2
      account_email: second@example.com
`,
	} {
		body := strings.NewReader(`{"profile_yaml":` + strconv.Quote(profileYAML) + `}`)
		req := httptest.NewRequest(http.MethodPost, "/api/managed-profile/save", body)
		addWebAuth(t, &app, req, "admin")
		rec := httptest.NewRecorder()
		app.newWebHandler(config).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save profile status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}
	body := strings.NewReader(`{"member_email":"admin@example.com","profiles":["first-usw2","second-usw2"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/member/profiles", body)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("member profiles status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/managed-profiles?include_yaml=1", nil)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "first-usw2") || !strings.Contains(rec.Body.String(), "second-usw2") {
		t.Fatalf("member should see both profiles body = %s status=%d", rec.Body.String(), rec.Code)
	}

	body = strings.NewReader(`{"member_email":"admin@example.com","profiles":["second-usw2"]}`)
	req = httptest.NewRequest(http.MethodPost, "/api/member/profiles", body)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace member profiles status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/managed-profiles?include_yaml=1", nil)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "first-usw2") || !strings.Contains(rec.Body.String(), "second-usw2") {
		t.Fatalf("member should see only replacement profile body = %s status=%d", rec.Body.String(), rec.Code)
	}
}

func TestCleanupDefaultLocalConfigProfilesBacksUpAndClearsProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".connectmac")
	profilesDir := filepath.Join(configDir, "profiles")
	configPath := filepath.Join(configDir, "config.yaml")
	writeFile(t, configPath, `server:
  user_api: https://cm.example.com
defaults:
  user: ec2-user
  identity_file: ~/.ssh/member.pem
  aws:
    amis_by_region:
      us-west-2:
        mac_x86: ami-x86
        mac_arm: ami-arm
profiles:
  old-usw2:
    description: old local profile
    host: old.example.com
`)
	writeFile(t, filepath.Join(profilesDir, "extra.yaml"), `profiles:
  extra-usw2:
    description: extra local profile
    host: extra.example.com
`)

	backup, err := cleanupDefaultLocalConfigProfiles(DefaultConfigPath, time.Date(2026, 7, 7, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if backup == "" {
		t.Fatalf("expected backup path")
	}
	if _, err := os.Stat(filepath.Join(backup, "config.yaml")); err != nil {
		t.Fatalf("backup config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "profiles", "extra.yaml")); err != nil {
		t.Fatalf("backup profiles: %v", err)
	}
	if entries, err := os.ReadDir(profilesDir); err != nil {
		t.Fatalf("profiles dir after cleanup: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("profiles dir should be empty, got %d entries", len(entries))
	}
	cfg, err := LoadConfig(DefaultConfigPath)
	if err != nil {
		t.Fatalf("load cleaned config: %v", err)
	}
	if cfg.Server.UserAPI != "https://cm.example.com" {
		t.Fatalf("server user api = %q", cfg.Server.UserAPI)
	}
	if cfg.Defaults.IdentityFile != "~/.ssh/member.pem" {
		t.Fatalf("identity file = %q", cfg.Defaults.IdentityFile)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("profiles after cleanup = %+v", cfg.Profiles)
	}
}

func TestAppWebAWSStatusUsesManagedProfilesWhenLocalProfilesAreCleared(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := filepath.Join(dir, "config.yaml")
	writeFile(t, config, `defaults:
  user: ec2-user
  identity_file: `+key+`
profiles:
`)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{}}, nil
	}
	profile, err := ParseSingleProfileYAML(`profiles:
  managed-usw2:
    description: Apple account: managed@example.com
    host: ec2-1-2-3-4.us-west-2.compute.amazonaws.com
    aws:
      profile: cm-xcode
      region: us-west-2
      account_email: managed@example.com
      key_name: maiqi-xcode
      security_group_id: sg-example
      elastic_ip_allocation_id: eipalloc-example
      elastic_ip_owner_tag:
        key: Apple
        value: managed@example.com
      availability_zone_ids:
        - usw2-az1
      instance_type_priority:
        - mac2-m2.metal
`)
	if err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	if _, err := app.MemberStore.UpsertManagedProfile(profile); err != nil {
		t.Fatalf("upsert managed profile: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/aws/status?profile=managed-usw2", nil)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "no profiles are configured") || strings.Contains(rec.Body.String(), "unknown profile") {
		t.Fatalf("status used cleared local profiles: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "managed-usw2") {
		t.Fatalf("status body = %s", rec.Body.String())
	}
}

func TestAppWebAWSStatusFallsBackToManagedProfilesWithoutLocalAuth(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := filepath.Join(dir, "config.yaml")
	writeFile(t, config, `defaults:
  user: ec2-user
  identity_file: `+key+`
profiles:
`)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.RemoteUserAPI = true
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{}}, nil
	}
	profile, err := ParseSingleProfileYAML(`profiles:
  managed-no-auth-usw2:
    description: Apple account: managed-no-auth@example.com
    host: ec2-1-2-3-5.us-west-2.compute.amazonaws.com
    aws:
      profile: cm-xcode
      region: us-west-2
      account_email: managed-no-auth@example.com
      key_name: maiqi-xcode
      security_group_id: sg-example
      elastic_ip_allocation_id: eipalloc-example
      elastic_ip_owner_tag:
        key: Apple
        value: managed-no-auth@example.com
      availability_zone_ids:
        - usw2-az1
      instance_type_priority:
        - mac2-m2.metal
`)
	if err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	if _, err := app.MemberStore.UpsertManagedProfile(profile); err != nil {
		t.Fatalf("upsert managed profile: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/aws/status?profile=managed-no-auth-usw2", nil)
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "no profiles are configured") || strings.Contains(rec.Body.String(), "unknown profile") {
		t.Fatalf("status used cleared local profiles: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "managed-no-auth-usw2") {
		t.Fatalf("status body = %s", rec.Body.String())
	}
}

func TestAppWebProfilesUsesManagedProfilesWhenConfigMissing(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "missing", "config.yaml")
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	profile, err := ParseSingleProfileYAML(`profiles:
  managed-missing-config-usw2:
    description: Apple account: managed-missing-config@example.com
    host: ec2-1-2-3-6.us-west-2.compute.amazonaws.com
    aws:
      profile: cm-xcode
      region: us-west-2
      account_email: managed-missing-config@example.com
      key_name: maiqi-xcode
      security_group_id: sg-example
      elastic_ip_allocation_id: eipalloc-example
      elastic_ip_owner_tag:
        key: Apple
        value: managed-missing-config@example.com
      availability_zone_ids:
        - usw2-az1
      instance_type_priority:
        - mac2-m2.metal
`)
	if err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	if _, err := app.MemberStore.UpsertManagedProfile(profile); err != nil {
		t.Fatalf("upsert managed profile: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "managed-missing-config-usw2") {
		t.Fatalf("profiles body = %s", rec.Body.String())
	}
}

func TestAppWebProfilesAPISkipsLocalAuthWithRemoteUserAPI(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.RemoteUserAPI = true
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "xcode-vnc") {
		t.Fatalf("profiles body = %s", rec.Body.String())
	}
}

func TestAppWebConfigAPI(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	writeFile(t, config, `
server:
  user_api: https://cm.hsgitlab.xyz/
profiles:
  xcode-vnc:
    description: Example
`)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"user_api":"https://cm.hsgitlab.xyz"`) {
		t.Fatalf("config body = %s", rec.Body.String())
	}
}

func TestAppWebUserProxySessionCookieBecomesSameOrigin(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "cm_session=signed-session; Path=/; HttpOnly; Secure; SameSite=None")
	rec := httptest.NewRecorder()
	copyUserProxySessionCookies(rec, resp)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %+v, want one session cookie", cookies)
	}
	if cookies[0].Secure {
		t.Fatalf("proxied local cookie should not require Secure on local http")
	}
	if cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("proxied local cookie SameSite = %v, want Lax", cookies[0].SameSite)
	}
}

func TestAppWebMemberAPIs(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)

	body := strings.NewReader(`{"name":"王恒辉","email":"whh@example.com","role":"operator"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/member/add", body)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body = strings.NewReader(`{"email":"whh@example.com","password":"newpassword123"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/member/password", body)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set member password status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, ok, err := app.MemberStore.VerifyMemberPassword("whh@example.com", "newpassword123"); err != nil || !ok {
		t.Fatalf("member password not updated ok=%t err=%v", ok, err)
	}

	body = strings.NewReader(`{"apple_email":"user@example.com","member_email":"whh@example.com","relation":"owner"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/member/assign", body)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/members", nil)
	addWebAuth(t, &app, req, "admin")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"whh@example.com", "user@example.com", "owner"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("members body missing %q:\n%s", want, rec.Body.String())
		}
	}

	body = strings.NewReader(`{"name":"Nope","email":"nope@example.com","role":"operator"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/member/add", body)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("operator add member status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/members", nil)
	addWebAuth(t, &app, req, "operator")
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("operator list members status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAppWebAuthSetupLoginAndSettings(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)

	challenge := webChallengeForTest(t, app)
	body := strings.NewReader(`{"name":"管理员","email":"admin@example.com","password":"password123","challenge_token":"` + challenge["token"] + `","challenge_answer":"` + challenge["answer"] + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", body)
	rec := httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatalf("expected setup to set session cookie")
	}

	challenge = webChallengeForTest(t, app)
	body = strings.NewReader(`{"username":"admin@example.com","password":"password123","challenge_token":"` + challenge["token"] + `","challenge_answer":"` + challenge["answer"] + `"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	rec = httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected login to set session cookie")
	}
	if cookies[0].Secure {
		t.Fatalf("local login cookie should not be secure")
	}
	if cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("local login cookie SameSite = %v, want Lax", cookies[0].SameSite)
	}

	body = strings.NewReader(`{"default_owner_email":"admin@example.com","default_status_filter":"ready","background_confirm":true,"show_released":true}`)
	req = httptest.NewRequest(http.MethodPost, "/api/settings", body)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "auth_secret") {
		t.Fatalf("settings leaked auth secret: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin@example.com") || !strings.Contains(rec.Body.String(), "ready") {
		t.Fatalf("settings body = %s", rec.Body.String())
	}

	challenge = webChallengeForTest(t, app)
	body = strings.NewReader(`{"email":"new-admin@example.com","password":"password123","challenge_token":"` + challenge["token"] + `","challenge_answer":"` + challenge["answer"] + `"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/update-email", body)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update email status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "new-admin@example.com") || strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatalf("update email body = %s", rec.Body.String())
	}
	updatedCookies := rec.Result().Cookies()
	if len(updatedCookies) == 0 {
		t.Fatalf("expected update email to refresh session cookie")
	}
	_, ok, err := app.MemberStore.VerifyMemberPassword("new-admin@example.com", "password123")
	if err != nil || !ok {
		t.Fatalf("updated email login failed ok=%t err=%v", ok, err)
	}

	body = strings.NewReader(`{"current_password":"password123","new_password":"newpassword123","confirm_password":"newpassword123"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/change-password", body)
	req.AddCookie(updatedCookies[0])
	rec = httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body = %s", rec.Code, rec.Body.String())
	}
	_, ok, err = app.MemberStore.VerifyMemberPassword("new-admin@example.com", "newpassword123")
	if err != nil || !ok {
		t.Fatalf("changed password login failed ok=%t err=%v", ok, err)
	}
}

func TestAppWebAuthHTTPSProxyCookie(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if _, err := app.MemberStore.SetupAdmin("管理员", "admin@example.com", "password123"); err != nil {
		t.Fatalf("setup admin: %v", err)
	}
	challenge := webChallengeForTest(t, app)
	body := strings.NewReader(`{"username":"admin@example.com","password":"password123","challenge_token":"` + challenge["token"] + `","challenge_answer":"` + challenge["answer"] + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected login to set session cookie")
	}
	if !cookies[0].Secure {
		t.Fatalf("https proxy cookie should be secure")
	}
	if cookies[0].SameSite != http.SameSiteNoneMode {
		t.Fatalf("https proxy cookie SameSite = %v, want None", cookies[0].SameSite)
	}
}

func TestAppWebServesExternalIndex(t *testing.T) {
	dir := t.TempDir()
	webDir := filepath.Join(dir, "web")
	writeFile(t, filepath.Join(webDir, "index.html"), "<!doctype html><title>External ConnectMac</title>")
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.WebDir = webDir
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "External ConnectMac") {
		t.Fatalf("index body = %s", rec.Body.String())
	}
}

func TestAppWebAWSStatusAPI(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Hosts:     []DedicatedHostStatus{{HostID: "h-1", State: "available", InstanceType: "mac2.metal", ZoneID: "usw2-az1", Tags: managedTestTags()}},
			Instances: []InstanceStatus{{InstanceID: "i-1", State: "running", InstanceType: "mac2.metal", HostID: "h-1", PublicIP: "203.0.113.10", SystemStatus: "ok", InstanceStatusCheck: "ok", EBSStatus: "ok", Tags: managedTestTags()}},
			ElasticIP: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", AssociationID: "eipassoc-1", InstanceID: "i-1", PublicIP: "203.0.113.10"},
		}}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "/api/aws/status?profile=xcode-vnc", nil)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AWS Mac status for profile xcode-vnc") || !strings.Contains(rec.Body.String(), "203.0.113.10") {
		t.Fatalf("status body = %s", rec.Body.String())
	}
	var resp webAPIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("status data = %#v", resp.Data)
	}
	if data["decision"] != "ready" || data["ready"] != true || data["next"] != "cm start xcode-vnc" {
		t.Fatalf("structured status data = %#v", data)
	}
}

func TestAppWebAWSStatusWritesErrorLog(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return nil, errors.New("missing aws profile website")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/aws/status?profile=xcode-vnc", nil)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing aws profile website") {
		t.Fatalf("status body = %s", rec.Body.String())
	}
	files, err := app.LogManager.List()
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("log files = %+v", files)
	}
	data, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, want := range []string{"web.aws.status", "xcode-vnc", "missing aws profile website"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("log missing %q: %s", want, data)
		}
	}
}

func TestAppWebDestroyConfirmStartsBackgroundJob(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	body := strings.NewReader(`{"profile":"xcode-vnc","confirm":true,"notify":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/aws/destroy", body)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	text := rec.Body.String()
	for _, want := range []string{"Started background AWS destroy job", "aws-destroy-xcode-vnc-20260701123045", "Elastic IP allocation will be retained"} {
		if !strings.Contains(text, want) {
			t.Fatalf("destroy response missing %q:\n%s", want, text)
		}
	}
	events, err := app.MemberStore.RecentEvents("user@example.com", 10)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) != 1 || events[0].Action != "destroy" || !events[0].Confirmed || events[0].Profile != "xcode-vnc" {
		t.Fatalf("events = %+v", events)
	}
}

func TestAppWebOpenConfirmStartsBackgroundJobWhenRequested(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	body := strings.NewReader(`{"profile":"xcode-vnc","confirm":true,"background":true,"notify":true,"owner_email":"admin@example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/aws/open", body)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	text := rec.Body.String()
	for _, want := range []string{"Started background AWS open job", "aws-open-xcode-vnc-20260701123045", "user@example.com"} {
		if !strings.Contains(text, want) {
			t.Fatalf("open response missing %q:\n%s", want, text)
		}
	}
}

func TestAppWebOpenRequiresOwnerForAdminAndAutoAssignsOperator(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)

	body := strings.NewReader(`{"profile":"xcode-vnc","confirm":true,"background":true,"notify":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/aws/open", body)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("admin open without owner status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "owner_email is required") {
		t.Fatalf("admin open without owner body = %s", rec.Body.String())
	}

	operator, err := app.MemberStore.AddMemberWithPassword("Operator", "operator@example.com", "operator", "password123")
	if err != nil {
		t.Fatalf("add operator: %v", err)
	}
	body = strings.NewReader(`{"profile":"xcode-vnc","confirm":true,"background":true,"notify":true}`)
	req = httptest.NewRequest(http.MethodPost, "/api/aws/open", body)
	sessionRec := httptest.NewRecorder()
	if err := app.setWebSession(sessionRec, operator); err != nil {
		t.Fatalf("set operator session: %v", err)
	}
	cookies := sessionRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected operator auth cookie")
	}
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator open status = %d, body = %s", rec.Code, rec.Body.String())
	}
	owners, err := app.MemberStore.MembersForApple("user@example.com")
	if err != nil {
		t.Fatalf("members for apple: %v", err)
	}
	found := false
	for _, owner := range owners {
		if owner.Email == "operator@example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("operator was not assigned as owner: %+v", owners)
	}
}

func TestAppWebTerminalCheckRequiresReadyAWSMac(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{}}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/check?profile=xcode-vnc", nil)
	addWebAuth(t, &app, req, "operator")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "aws mac is not ready") || !strings.Contains(rec.Body.String(), "no managed instance found") {
		t.Fatalf("terminal check body = %s", rec.Body.String())
	}
}

func TestAppWebTerminalCheckReady(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Instances: []InstanceStatus{{
				InstanceID:          "i-ready",
				State:               "running",
				SystemStatus:        "ok",
				InstanceStatusCheck: "ok",
				EBSStatus:           "ok",
				Tags:                managedTestTags(),
			}},
			ElasticIP: ElasticIP{InstanceID: "i-ready", PublicIP: "54.1.2.3", AllocationID: "eipalloc-1"},
		}}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/check?profile=xcode-vnc", nil)
	addWebAuth(t, &app, req, "operator")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"ok":true`, `"profile":"xcode-vnc"`, `"target":"user@mac-host.example.com"`, `"ready":true`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("terminal check missing %q:\n%s", want, rec.Body.String())
		}
	}
}

func TestAppWebSyncPushRequiresReadyAndSavesHistory(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	localFile := filepath.Join(dir, "build.zip")
	writeFile(t, localFile, "zip")
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Instances: []InstanceStatus{{
				InstanceID:          "i-ready",
				State:               "running",
				SystemStatus:        "ok",
				InstanceStatusCheck: "ok",
				EBSStatus:           "ok",
				Tags:                managedTestTags(),
			}},
			ElasticIP: ElasticIP{InstanceID: "i-ready", PublicIP: "54.1.2.3", AllocationID: "eipalloc-1"},
		}}, nil
	}
	body := strings.NewReader(fmt.Sprintf(`{"profile":"xcode-vnc","local_path":%q,"remote_path":"~/Downloads/"}`, localFile))
	req := httptest.NewRequest(http.MethodPost, "/api/sync/push", body)
	addWebAuth(t, &app, req, "operator")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Push:") || !strings.Contains(rec.Body.String(), localFile) {
		t.Fatalf("push body = %s", rec.Body.String())
	}
	if !containsString(runner.rsync, localFile) || !containsString(runner.rsync, "user@mac-host.example.com:~/Downloads/") {
		t.Fatalf("rsync args = %#v", runner.rsync)
	}
	items, err := app.SyncHistory.List("xcode-vnc", 10)
	if err != nil {
		t.Fatalf("history list: %v", err)
	}
	if len(items) != 1 || items[0].Direction != "push" || items[0].LocalPath != localFile || items[0].RemotePath != "~/Downloads/" {
		t.Fatalf("history items = %+v", items)
	}
}

func TestAppWebSyncPullBlocksWhenNotReady(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{}}, nil
	}
	body := strings.NewReader(`{"profile":"xcode-vnc","remote_path":"~/Downloads/","local_path":"."}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sync/pull", body)
	addWebAuth(t, &app, req, "operator")
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "aws mac is not ready") {
		t.Fatalf("pull body = %s", rec.Body.String())
	}
	if len(runner.rsync) != 0 {
		t.Fatalf("rsync should not run: %#v", runner.rsync)
	}
}

func TestAppWebLocalListRoots(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	req := httptest.NewRequest(http.MethodGet, "/api/local/list", nil)
	addWebAuth(t, &app, req, "operator")
	rec := httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"entries"`) || !strings.Contains(rec.Body.String(), `"path":""`) {
		t.Fatalf("local roots body = %s", rec.Body.String())
	}
}

func TestAppWebLocalListRejectsHiddenPath(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	hiddenPath := filepath.Join(mustGetwd(t), ".git")
	req := httptest.NewRequest(http.MethodGet, "/api/local/list?path="+url.QueryEscape(hiddenPath), nil)
	addWebAuth(t, &app, req, "operator")
	rec := httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "outside allowed local directories") {
		t.Fatalf("hidden path body = %s", rec.Body.String())
	}
}

func TestAppWebJobsAPI(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if _, err := app.JobManager.Create(Job{Type: "aws-destroy", Profile: "xcode-vnc", Status: JobStatusSuccess}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "aws-destroy") || !strings.Contains(rec.Body.String(), "success") {
		t.Fatalf("jobs body = %s", rec.Body.String())
	}
}

func TestAppWebJobLogAPI(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	job, err := app.JobManager.Create(Job{Type: "aws-destroy", Profile: "xcode-vnc", Status: JobStatusSuccess})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := os.WriteFile(job.Log, []byte("job log line\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/job/log?id="+job.ID, nil)
	addWebAuth(t, &app, req, "admin")
	rec := httptest.NewRecorder()
	app.newWebHandler(DefaultConfigPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "job log line") || !strings.Contains(rec.Body.String(), job.ID) {
		t.Fatalf("job log body = %s", rec.Body.String())
	}
}

func TestAppProfileAddShowRenameRemove(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	code := app.Run(context.Background(), []string{
		"profile", "add",
		"--name", "new-user-usw2",
		"--apple-email", "new@example.com",
		"--aws-profile", "cm-xcode",
		"--region", "us-west-2",
		"--eip", "54.1.2.3",
		"--eip-allocation-id", "eipalloc-new",
		"--key-name", "new-key",
		"--security-group-id", "sg-new",
		"--az", "usw2-az1",
		"--subnet", "usw2-az1=subnet-new",
		"--config", config,
	})
	if code != 0 {
		t.Fatalf("profile add code = %d, err = %s", code, errOut.String())
	}
	profilePath := filepath.Join(dir, "profiles", "new-user-usw2.yaml")
	if _, err := os.Stat(profilePath); err != nil {
		t.Fatalf("expected profile file: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"profile", "show", "new@example.com", "--config", config}); code != 0 {
		t.Fatalf("profile show code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "new-user-usw2") || !strings.Contains(out.String(), "new@example.com") {
		t.Fatalf("profile show output = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"profile", "rename", "new-user-usw2", "renamed-usw2", "--config", config}); code != 0 {
		t.Fatalf("profile rename code = %d, err = %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "renamed-usw2.yaml")); err != nil {
		t.Fatalf("expected renamed profile file: %v", err)
	}
	out.Reset()
	errOut.Reset()
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{}}, nil
	}
	if code := app.Run(context.Background(), []string{"profile", "remove", "renamed-usw2", "--force-local", "--config", config}); code != 0 {
		t.Fatalf("profile remove code = %d, err = %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "renamed-usw2.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected removed profile file, err=%v", err)
	}
}

func TestAppProfileRemoveBlocksWhenAWSResourcesExist(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Hosts:     []DedicatedHostStatus{{HostID: "h-1", State: "available"}},
			Instances: []InstanceStatus{{InstanceID: "i-1", State: "running"}},
		}}, nil
	}
	if code := app.Run(context.Background(), []string{"profile", "remove", "xcode-vnc", "--config", config}); code != 1 {
		t.Fatalf("profile remove code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"still has AWS Mac resources", "never releases Elastic IP", "cm close xcode-vnc", "--force-local"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("err missing %q: %q", want, errOut.String())
		}
	}
}

func TestAppProfileAddWizardPreviewsAndWrites(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.In = strings.NewReader(strings.Join([]string{
		"wizard-usw2",
		"wizard@example.com",
		"",
		"",
		key,
		"cm-xcode",
		"us-west-2",
		"",
		"example-key",
		"sg-example",
		"eipalloc-example",
		"54.1.2.3",
		"usw2-az1",
		"subnet-example",
		"",
		"y",
	}, "\n") + "\n")
	if code := app.Run(context.Background(), []string{"profile", "add", "--wizard", "--config", config}); code != 0 {
		t.Fatalf("profile add wizard code = %d, err = %s, out = %s", code, errOut.String(), out.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles", "wizard-usw2.yaml"))
	if err != nil {
		t.Fatalf("read wizard profile: %v", err)
	}
	for _, want := range []string{"Profile preview", "Warnings", "ec2-54-1-2-3.us-west-2.compute.amazonaws.com"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("wizard output missing %q: %s", want, out.String())
		}
	}
	if !strings.Contains(string(data), "wizard@example.com") || !strings.Contains(string(data), "subnet-example") {
		t.Fatalf("wizard profile = %s", data)
	}
}

func TestAppProfileWizardAliasPreviewsAndWrites(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.In = strings.NewReader(strings.Join([]string{
		"alias-usw2",
		"alias@example.com",
		"",
		"",
		key,
		"cm-xcode",
		"us-west-2",
		"",
		"example-key",
		"sg-example",
		"eipalloc-example",
		"54.1.2.4",
		"",
		"y",
	}, "\n") + "\n")
	if code := app.Run(context.Background(), []string{"profile", "wizard", "--config", config}); code != 0 {
		t.Fatalf("profile wizard code = %d, err = %s, out = %s", code, errOut.String(), out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "alias-usw2.yaml")); err != nil {
		t.Fatalf("expected profile wizard alias file: %v", err)
	}
	if !strings.Contains(out.String(), "Profile preview") {
		t.Fatalf("wizard alias output = %s", out.String())
	}
}

func TestAppProfileAddWizardRejectsDuplicateAppleEmail(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.In = strings.NewReader("duplicate-usw2\nuser@example.com\n")
	if code := app.Run(context.Background(), []string{"profile", "add", "--wizard", "--config", config}); code != 1 {
		t.Fatalf("profile add wizard code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "already configured") {
		t.Fatalf("err = %q", errOut.String())
	}
}

func TestAppProfileExportImport(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"profile", "export", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("profile export code = %d, err = %s", code, errOut.String())
	}
	exported := filepath.Join(t.TempDir(), "export.yaml")
	writeFile(t, exported, strings.Replace(out.String(), "xcode-vnc:", "imported:", 1))
	otherDir := t.TempDir()
	otherConfig := filepath.Join(otherDir, "config.yaml")
	writeFile(t, otherConfig, "profiles:\n")
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"profile", "import", exported, "--config", otherConfig}); code != 0 {
		t.Fatalf("profile import code = %d, err = %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(otherDir, "profiles", "imported.yaml")); err != nil {
		t.Fatalf("expected imported profile: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"profile", "import", exported, "--config", otherConfig}); code != 1 {
		t.Fatalf("duplicate import code = %d, err = %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"profile", "import", exported, "--overwrite", "--config", otherConfig}); code != 0 {
		t.Fatalf("overwrite import code = %d, err = %s", code, errOut.String())
	}
}

func TestAppDoctorDashboardAndSetupVNC(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"doctor", "--fix", "--config", config}); code != 0 {
		t.Fatalf("doctor code = %d, err = %s\nout=%s", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "mcp tools") || !strings.Contains(out.String(), "config file") || !strings.Contains(out.String(), "NEXT") {
		t.Fatalf("doctor output = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"dashboard", "--config", config}); code != 0 {
		t.Fatalf("dashboard code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "PROFILE") || !strings.Contains(out.String(), "xcode-vnc") {
		t.Fatalf("dashboard output = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Instances: []InstanceStatus{{InstanceID: "i-1", State: "running", SystemStatus: "ok", InstanceStatusCheck: "ok"}},
			ElasticIP: ElasticIP{InstanceID: "i-1", PublicIP: "54.1.2.3"},
		}}, nil
	}
	if code := app.Run(context.Background(), []string{"dashboard", "--aws", "--config", config}); code != 0 {
		t.Fatalf("dashboard --aws code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "READY") || !strings.Contains(out.String(), "DECISION") || !strings.Contains(out.String(), "NEXT") || !strings.Contains(out.String(), "cm start xcode-vnc") || !strings.Contains(out.String(), "ready") || !strings.Contains(out.String(), "54.1.2.3") {
		t.Fatalf("dashboard --aws output = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"setup-vnc", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("setup-vnc code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "sudo passwd ec2-user") || !strings.Contains(out.String(), "cm open-vnc xcode-vnc") {
		t.Fatalf("setup-vnc output = %s", out.String())
	}
}

func TestAppDoctorSuggestsFixCommands(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "missing.yaml")
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"doctor", "--config", config}); code != 1 {
		t.Fatalf("doctor missing config code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"config file", "fail", "cm init", "profiles dir", "cm doctor --fix"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor missing config output missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppNextReportsReadyFlow(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Instances: []InstanceStatus{{InstanceID: "i-1", State: "running", SystemStatus: "ok", InstanceStatusCheck: "ok"}},
			ElasticIP: ElasticIP{InstanceID: "i-1", PublicIP: "54.1.2.3"},
		}}, nil
	}
	if code := app.Run(context.Background(), []string{"next", "user@example.com", "--config", config}); code != 0 {
		t.Fatalf("next code = %d, err = %s, out = %s", code, errOut.String(), out.String())
	}
	for _, want := range []string{"Next step for profile xcode-vnc", "Decision: ready", "Next: cm start xcode-vnc", "After tunnel: cm open-vnc xcode-vnc"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("next output missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppNextReportsConfigFixes(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	writeFile(t, config, "profiles:\n  broken:\n    description: broken\n")
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"next", "broken", "--config", config}); code != 0 {
		t.Fatalf("next broken code = %d, err = %s, out = %s", code, errOut.String(), out.String())
	}
	for _, want := range []string{"Decision: fix-config", "Local access issues", "AWS config issues", "Next: cm profile edit broken"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("next broken output missing %q:\n%s", want, out.String())
		}
	}
}

func TestAppCompletionZshScriptUsesDynamicProfiles(t *testing.T) {
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"completion", "zsh"}); code != 0 {
		t.Fatalf("completion zsh code = %d, err = %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"#compdef cm", "cm completion profiles", "cm completion apple-emails", "cm completion aws-commands", "open|close|next", "guide topic"} {
		if !strings.Contains(text, want) {
			t.Fatalf("zsh completion missing %q: %q", want, text)
		}
	}
}

func TestAppCheck(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"check", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("check code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "check passed") {
		t.Fatalf("check output = %q", out.String())
	}
}

func TestAppCheckPromptsForMissingIdentityFile(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(sshDir, "prompt-key.pem")
	if err := os.WriteFile(key, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	configData := strings.ReplaceAll(sampleConfig, "  identity_file: ~/.ssh/default.pem\n", "")
	configData = strings.ReplaceAll(configData, "    identity_file: ~/.ssh/example.pem\n", "")
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.In = strings.NewReader("prompt-key\n")
	if code := app.Run(context.Background(), []string{"check", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("check code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "identity_file for xcode-vnc") {
		t.Fatalf("expected prompt, got %q", errOut.String())
	}
	if !strings.Contains(out.String(), "Identity: ~/.ssh/prompt-key.pem") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppConnectUsesRunner(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"connect", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("connect code = %d, err = %s", code, errOut.String())
	}
	if len(runner.foreground) == 0 {
		t.Fatal("expected foreground runner to be called")
	}
}

func TestAppSSHUsesRunner(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"ssh", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("ssh code = %d, err = %s", code, errOut.String())
	}
	if len(runner.foreground) == 0 {
		t.Fatal("expected foreground runner to be called")
	}
	for _, arg := range runner.foreground {
		if arg == "-N" {
			t.Fatalf("interactive ssh args must not include -N: %#v", runner.foreground)
		}
	}
	if !strings.Contains(out.String(), "SSH: user@") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppStartSavesStateAfterHealthyTunnel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{knownHost: "mac-host.example.com ssh-ed25519 AAAACURRENT\n"}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"start", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("start code = %d, err = %s", code, errOut.String())
	}
	if len(runner.background) == 0 {
		t.Fatal("expected background runner to be called")
	}
	if !strings.Contains(out.String(), "started xcode-vnc with pid 55") {
		t.Fatalf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "Host key: current") {
		t.Fatalf("out missing host key status = %q", out.String())
	}
	state, ok, err := app.StateManager.Load("xcode-vnc")
	if err != nil || !ok || state.PID != 55 {
		t.Fatalf("state = %+v ok=%t err=%v", state, ok, err)
	}
}

func TestAppStartAddsMissingHostKeyBeforeTunnel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"start", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("start code = %d, err = %s", code, errOut.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, ".ssh", "known_hosts"))
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(data), "AAAACURRENT") || !strings.Contains(out.String(), "Host key: missing") {
		t.Fatalf("known_hosts=%q out=%q", data, out.String())
	}
	if len(runner.background) == 0 {
		t.Fatal("expected tunnel to start after adding host key")
	}
}

func TestAppStartReplacesStaleHostKeyBeforeTunnel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{knownHost: "mac-host.example.com ssh-ed25519 AAAAOLD\n"}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"start", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("start code = %d, err = %s", code, errOut.String())
	}
	if runner.forgotHost != "mac-host.example.com" {
		t.Fatalf("forgot host = %q", runner.forgotHost)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".ssh", "known_hosts"))
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(data), "AAAACURRENT") || !strings.Contains(out.String(), "Host key: stale") {
		t.Fatalf("known_hosts=%q out=%q", data, out.String())
	}
}

func TestAppStartStopsWhenHostKeyScanFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{scanErr: errors.New("scan failed")}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"start", "xcode-vnc", "--config", config}); code != 1 {
		t.Fatalf("start code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	if len(runner.background) != 0 {
		t.Fatalf("tunnel should not start after scan failure: %#v", runner.background)
	}
	if !strings.Contains(errOut.String(), "host key scan failed") {
		t.Fatalf("err = %q", errOut.String())
	}
}

func TestAppStartDoesNotSaveStateWhenTunnelFailsHealthCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{knownHost: "mac-host.example.com ssh-ed25519 AAAACURRENT\n", startErr: errors.New("Permission denied (publickey)")}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"start", "xcode-vnc", "--config", config}); code != 1 {
		t.Fatalf("start code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "started") {
		t.Fatalf("should not report started on failed health check: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "Permission denied") {
		t.Fatalf("err = %q", errOut.String())
	}
	if _, ok, err := app.StateManager.Load("xcode-vnc"); err != nil || ok {
		t.Fatalf("state should not be saved, ok=%t err=%v", ok, err)
	}
}

func TestAppPullUsesRsyncRunner(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"pull", "xcode-vnc", "~/Desktop/a.zip", "--config", config}); code != 0 {
		t.Fatalf("pull code = %d, err = %s", code, errOut.String())
	}
	if len(runner.rsync) == 0 {
		t.Fatal("expected rsync runner to be called")
	}
	if !strings.Contains(out.String(), "Pull:") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppPullAcceptsAppleEmail(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"pull", "user@example.com", "~/Desktop/a.zip", "--config", config}); code != 0 {
		t.Fatalf("pull code = %d, err = %s", code, errOut.String())
	}
	if !containsString(runner.rsync, "user@mac-host.example.com:~/Desktop/a.zip") {
		t.Fatalf("rsync args = %#v", runner.rsync)
	}
}

func TestAppPullAcceptsSyncFilters(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"pull", "xcode-vnc", "~/Desktop/a.zip", "--include", "*.zip", "--exclude", "*.tmp", "--config", config}); code != 0 {
		t.Fatalf("pull code = %d, err = %s", code, errOut.String())
	}
	for _, want := range []string{"--include", "*.zip", "--exclude", ".DS_Store", "--exclude", "*.tmp", "--exclude", "*"} {
		if !containsString(runner.rsync, want) {
			t.Fatalf("rsync args missing %q = %#v", want, runner.rsync)
		}
	}
}

func TestAppPushUsesRsyncRunner(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	localFile := filepath.Join(dir, "build.zip")
	writeFile(t, localFile, "zip")
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"push", "xcode-vnc", localFile, "~/Downloads/", "--config", config}); code != 0 {
		t.Fatalf("push code = %d, err = %s", code, errOut.String())
	}
	if len(runner.rsync) == 0 {
		t.Fatal("expected rsync runner to be called")
	}
	if !strings.Contains(out.String(), "Push:") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppPushAcceptsSyncFilters(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	localFile := filepath.Join(dir, "build.zip")
	writeFile(t, localFile, "zip")
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"push", "xcode-vnc", localFile, "~/Downloads/", "--include", "Sources/***", "--exclude", "DerivedData", "--config", config}); code != 0 {
		t.Fatalf("push code = %d, err = %s", code, errOut.String())
	}
	for _, want := range []string{"--include", "Sources/***", "--exclude", "xcuserdata", "--exclude", "DerivedData", "--exclude", "*"} {
		if !containsString(runner.rsync, want) {
			t.Fatalf("rsync args missing %q = %#v", want, runner.rsync)
		}
	}
}

func TestAppPushAcceptsAppleEmail(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	localFile := filepath.Join(dir, "build.zip")
	writeFile(t, localFile, "zip")
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"push", "user@example.com", localFile, "~/Downloads/", "--config", config}); code != 0 {
		t.Fatalf("push code = %d, err = %s", code, errOut.String())
	}
	if !containsString(runner.rsync, "user@mac-host.example.com:~/Downloads/") {
		t.Fatalf("rsync args = %#v", runner.rsync)
	}
}

func TestAppExecUsesForegroundRunner(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	command := "ls -ld ~/Downloads/Vitora && du -sh ~/Downloads/Vitora"
	if code := app.Run(context.Background(), []string{"exec", "xcode-vnc", "--config", config, "--", command, "--config", "remote.yaml"}); code != 0 {
		t.Fatalf("exec code = %d, err = %s", code, errOut.String())
	}
	if len(runner.foreground) == 0 {
		t.Fatal("expected foreground runner to be called")
	}
	if !containsString(runner.foreground, "IdentitiesOnly=yes") {
		t.Fatalf("foreground args missing IdentitiesOnly=yes: %#v", runner.foreground)
	}
	if got := runner.foreground[len(runner.foreground)-3:]; !reflect.DeepEqual(got, []string{command, "--config", "remote.yaml"}) {
		t.Fatalf("command args = %#v, want command and remote --config preserved", got)
	}
	if !strings.Contains(out.String(), "Exec:") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppForgetHostUsesRunner(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"forget-host", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("forget-host code = %d, err = %s", code, errOut.String())
	}
	want := "mac-host.example.com"
	if runner.forgotHost != want {
		t.Fatalf("forgot host = %q, want %q", runner.forgotHost, want)
	}
	if !strings.Contains(out.String(), "Removing known_hosts entries") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppHostKeyCommands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{knownHost: "mac-host.example.com ssh-ed25519 AAAACURRENT\n"}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"host-key", "check", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("host-key check code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Host key: current") {
		t.Fatalf("check out = %q", out.String())
	}
	out.Reset()
	errOut.Reset()
	runner.knownHost = ""
	if code := app.Run(context.Background(), []string{"host-key", "fix", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("host-key fix code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Host key: missing") {
		t.Fatalf("fix out = %q", out.String())
	}
}

func TestAppOpenVNCUsesRunner(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
	app := testApp(&out, &errOut, dir)
	app.Runner = runner
	if code := app.Run(context.Background(), []string{"open-vnc", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("open-vnc code = %d, err = %s", code, errOut.String())
	}
	if runner.openedURL != "vnc://mac-user@localhost:5900" {
		t.Fatalf("opened url = %q", runner.openedURL)
	}
	if !strings.Contains(out.String(), "Opening vnc://mac-user@localhost:5900") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppAWSPlan(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"aws", "plan", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("aws plan code = %d, err = %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"AWS Mac plan", "Selected instance type: mac2.metal", "Selected AMI: ami-063755aadeb97329a"} {
		if !strings.Contains(text, want) {
			t.Fatalf("aws plan output missing %q:\n%s", want, text)
		}
	}
}

func TestAppProfileFindByAppleEmail(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"profile", "find", "user@example.com", "--config", config}); code != 0 {
		t.Fatalf("profile find code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Profile: xcode-vnc") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppAWSWaitReady(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			CallerIdentity: CallerIdentity{Account: "123456789012", ARN: "arn:aws:iam::123456789012:user/cm"},
			Instances: []InstanceStatus{{
				InstanceID:          "i-1",
				State:               "running",
				SystemStatus:        "ok",
				InstanceStatusCheck: "ok",
				EBSStatus:           "ok",
			}},
			ElasticIP: ElasticIP{InstanceID: "i-1"},
		}}, nil
	}
	app.AWSService.ReadyPollInterval = time.Millisecond
	app.AWSService.ReadyTimeout = time.Second
	if code := app.Run(context.Background(), []string{"aws", "wait-ready", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("aws wait-ready code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "AWS Mac ready for profile xcode-vnc: true") {
		t.Fatalf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "Manual GUI setup:") || !strings.Contains(out.String(), "sudo passwd ec2-user") {
		t.Fatalf("ready output missing manual setup guide:\n%s", out.String())
	}
}

func TestAppAWSCreateFailureReportsReasonAndStops(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{
			eip:    ElasticIP{AllocationID: "<elastic-ip-allocation-id>", Tags: []AWSTagConfig{{Key: "Apple", Value: "user@example.com"}}},
			runErr: errString("RunInstances rejected selected host"),
		}, nil
	}
	if code := app.Run(context.Background(), []string{"aws", "create", "xcode-vnc", "--confirm", "--config", config}); code != 1 {
		t.Fatalf("aws create code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"aws create failed", "RunInstances rejected selected host", "Stopped", "wait for explicit instructions"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("err missing %q:\n%s", want, errOut.String())
		}
	}
}

func TestAppAWSDestroyResolvesAppleEmail(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Hosts:     []DedicatedHostStatus{{HostID: "h-1", State: "available", Tags: managedTestTags()}},
			Instances: []InstanceStatus{{InstanceID: "i-1", State: "running", Tags: managedTestTags()}},
			ElasticIP: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", AssociationID: "eipassoc-1", InstanceID: "i-1", PublicIP: "203.0.113.10"},
		}}, nil
	}
	if code := app.Run(context.Background(), []string{"aws", "destroy", "user@example.com", "--config", config}); code != 0 {
		t.Fatalf("aws destroy by email code = %d, err = %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"Resolved Apple account user@example.com -> profile xcode-vnc", "AWS Mac destroy preview", "Matched resources:", "retain the Elastic IP allocation"} {
		if !strings.Contains(text, want) {
			t.Fatalf("destroy by email output missing %q:\n%s", want, text)
		}
	}
}

func TestAppAWSDestroyConfirmPrintsFinalStatus(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	fake := &fakeAWSClient{status: AWSStatus{
		Hosts:     []DedicatedHostStatus{{HostID: "h-1", State: "available", InstanceType: "mac2.metal", ZoneID: "usw2-az1", Tags: managedTestTags()}},
		Instances: []InstanceStatus{{InstanceID: "i-1", State: "running", InstanceType: "mac2.metal", HostID: "h-1", Tags: managedTestTags()}},
		ElasticIP: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", AssociationID: "eipassoc-1", InstanceID: "i-1", PublicIP: "203.0.113.10"},
	}}
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return fake, nil
	}
	if code := app.Run(context.Background(), []string{"aws", "destroy", "user@example.com", "--confirm", "--config", config}); code != 0 {
		t.Fatalf("aws destroy confirm code = %d, err = %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"AWS Mac destroy executed", "Final status", "Dedicated hosts: 0", "Instances: 0", "Elastic IP retained: true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("destroy confirm output missing %q:\n%s", want, text)
		}
	}
}

func TestAppAWSDestroyBackgroundRequiresConfirm(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"aws", "destroy", "user@example.com", "--background", "--config", config}); code != 2 {
		t.Fatalf("aws destroy background code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "--background requires --confirm") {
		t.Fatalf("err missing background confirm guidance: %s", errOut.String())
	}
}

func TestAppAWSDestroyBackgroundCreatesJob(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"aws", "destroy", "user@example.com", "--confirm", "--background", "--notify", "--config", config}); code != 0 {
		t.Fatalf("aws destroy background code = %d, out = %s, err = %s", code, out.String(), errOut.String())
	}
	text := out.String()
	for _, want := range []string{"Started background AWS destroy job", "aws-destroy-xcode-vnc-20260701123045", "cm job status", "Elastic IP allocation will be retained"} {
		if !strings.Contains(text, want) {
			t.Fatalf("background output missing %q:\n%s", want, text)
		}
	}
	job, err := app.JobManager.Load("aws-destroy-xcode-vnc-20260701123045")
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.Profile != "xcode-vnc" || job.AppleEmail != "user@example.com" || !job.Notify {
		t.Fatalf("job = %#v", job)
	}
	joined := strings.Join(job.Command, " ")
	if !strings.Contains(joined, "aws destroy xcode-vnc --confirm --config "+config) {
		t.Fatalf("job command = %#v", job.Command)
	}
}

func TestAppJobListStatusAndLog(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	job, err := app.JobManager.Create(Job{
		Type:       "aws-destroy",
		Profile:    "xcode-vnc",
		AppleEmail: "user@example.com",
		Status:     JobStatusSuccess,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := os.WriteFile(job.Log, []byte("hello job\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if code := app.Run(context.Background(), []string{"job", "list"}); code != 0 {
		t.Fatalf("job list code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ID") || !strings.Contains(out.String(), "xcode-vnc") || !strings.Contains(out.String(), "success") {
		t.Fatalf("job list output = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"job", "status", job.ID}); code != 0 {
		t.Fatalf("job status code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Apple account: user@example.com") || !strings.Contains(out.String(), "Status: success") {
		t.Fatalf("job status output = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"job", "log", job.ID}); code != 0 {
		t.Fatalf("job log code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "hello job") {
		t.Fatalf("job log output = %s", out.String())
	}
}

func TestJobManagerRunJobMarksDeferred(t *testing.T) {
	dir := t.TempDir()
	manager := NewJobManager(filepath.Join(dir, "jobs"))
	manager.Now = func() time.Time {
		return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	}
	manager.Notify = func(title, message string) error { return nil }
	job, err := manager.Create(Job{
		Type:    "aws-destroy",
		Profile: "xcode-vnc",
		Command: []string{"/bin/echo", "Need rerun: true"},
		Notify:  true,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	job, err = manager.RunJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run job: %v", err)
	}
	if job.Status != JobStatusDeferred {
		t.Fatalf("status = %s", job.Status)
	}
	if job.ExitCode == nil || *job.ExitCode != 0 {
		t.Fatalf("exit code = %#v", job.ExitCode)
	}
}

func TestAppAWSRunningListsRunningInstances(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Hosts: []DedicatedHostStatus{{HostID: "h-1", State: "available", InstanceType: "mac2.metal", ZoneID: "usw2-az1", Tags: managedTestTags()}},
			Instances: []InstanceStatus{{
				InstanceID:          "i-1",
				State:               "running",
				InstanceType:        "mac2.metal",
				HostID:              "h-1",
				PublicIP:            "203.0.113.10",
				SystemStatus:        "ok",
				InstanceStatusCheck: "ok",
				EBSStatus:           "ok",
				Tags:                managedTestTags(),
			}},
			ElasticIP: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", InstanceID: "i-1", PublicIP: "203.0.113.10"},
		}}, nil
	}
	if code := app.Run(context.Background(), []string{"aws", "running", "--config", config}); code != 0 {
		t.Fatalf("aws running code = %d, err = %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"APPLE ACCOUNT", "user@example.com", "xcode-vnc", "usw2-az1", "mac2.metal", "i-1", "true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("aws running output missing %q:\n%s", want, text)
		}
	}
}

func TestAppAWSDestroyManyPreview(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Hosts:     []DedicatedHostStatus{{HostID: "h-1", State: "available", Tags: managedTestTags()}},
			Instances: []InstanceStatus{{InstanceID: "i-1", State: "running", Tags: managedTestTags()}},
			ElasticIP: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", AssociationID: "eipassoc-1", InstanceID: "i-1", PublicIP: "203.0.113.10"},
		}}, nil
	}
	if code := app.Run(context.Background(), []string{"aws", "destroy-many", "user@example.com", "--config", config}); code != 0 {
		t.Fatalf("aws destroy-many code = %d, err = %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"Resolved Apple account user@example.com -> profile xcode-vnc", "AWS Mac destroy preview", "Preview only. Include --confirm"} {
		if !strings.Contains(text, want) {
			t.Fatalf("destroy-many output missing %q:\n%s", want, text)
		}
	}
}

func TestAppAWSStatusAllIncludesTerminalResources(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			CallerIdentity: CallerIdentity{Account: "123456789012", ARN: "arn:aws:iam::123456789012:user/cm"},
			Hosts: []DedicatedHostStatus{
				{HostID: "h-active", State: "available", InstanceType: "mac2.metal", ZoneID: "usw2-az1", Tags: managedTestTags()},
				{HostID: "h-released", State: "released", InstanceType: "mac2.metal", ZoneID: "usw2-az1", Tags: managedTestTags()},
			},
			Instances: []InstanceStatus{
				{InstanceID: "i-active", State: "running", Tags: managedTestTags()},
				{InstanceID: "i-terminated", State: "terminated", Tags: managedTestTags()},
			},
		}}, nil
	}
	if code := app.Run(context.Background(), []string{"aws", "status", "xcode-vnc", "--config", config}); code != 0 {
		t.Fatalf("aws status code = %d, err = %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "h-released") || strings.Contains(out.String(), "i-terminated") {
		t.Fatalf("default status should hide terminal resources:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Use --all") {
		t.Fatalf("default status should mention --all:\n%s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := app.Run(context.Background(), []string{"aws", "status", "xcode-vnc", "--all", "--config", config}); code != 0 {
		t.Fatalf("aws status --all code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "h-released") || !strings.Contains(out.String(), "i-terminated") {
		t.Fatalf("status --all should include terminal resources:\n%s", out.String())
	}
}

func TestAppAWSAdoptHostPreview(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{host: DedicatedHostStatus{HostID: "h-empty", State: "available", InstanceType: "mac2.metal", ZoneID: "usw2-az1"}}, nil
	}
	if code := app.Run(context.Background(), []string{"aws", "adopt-host", "xcode-vnc", "--host-id", "h-empty", "--config", config}); code != 0 {
		t.Fatalf("aws adopt-host code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "AWS Mac adopt-host preview") || !strings.Contains(out.String(), "Preview only") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAppAWSLaunchOnHostRequiresHostID(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"aws", "launch-on-host", "xcode-vnc", "--config", config}); code != 2 {
		t.Fatalf("aws launch-on-host code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "--host-id is required") {
		t.Fatalf("err = %q", errOut.String())
	}
}

func TestAppUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"wat"}); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("err output = %q", errOut.String())
	}
}

func TestAppLogsCommands(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if err := app.LogManager.Write(LogEntry{Level: "error", Action: "test", Message: "hello"}); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if code := app.Run(context.Background(), []string{"logs", "list"}); code != 0 {
		t.Fatalf("logs list code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "cm-2026-07-01.log") {
		t.Fatalf("logs list output = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	exportPath := filepath.Join(dir, "logs.zip")
	if code := app.Run(context.Background(), []string{"logs", "export", "--output", exportPath}); code != 0 {
		t.Fatalf("logs export code = %d, err = %s", code, errOut.String())
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("expected export zip: %v", err)
	}
	if !strings.Contains(out.String(), "exported logs") {
		t.Fatalf("logs export output = %s", out.String())
	}
}

func testApp(out, errOut *bytes.Buffer, stateDir string) App {
	return App{
		Out:       out,
		Err:       errOut,
		Runner:    &fakeRunner{},
		Validator: NewValidatorForTest(nil),
		StateManager: StateManager{
			Dir:       filepath.Join(stateDir, "state"),
			IsRunning: func(pid int) bool { return pid == 55 },
		},
		JobManager: JobManager{
			Dir:        filepath.Join(stateDir, "jobs"),
			Executable: "/bin/echo",
			Now: func() time.Time {
				return time.Date(2026, 7, 1, 12, 30, 45, 0, time.UTC)
			},
			IsRunning: func(pid int) bool { return pid > 0 },
			Notify:    func(title, message string) error { return nil },
		},
		AWSService: AWSService{Now: func() time.Time {
			return time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
		}},
		WebDir:      filepath.Join("..", "..", "web"),
		MemberStore: NewMemberStore(filepath.Join(stateDir, "members.json")),
		LogManager: LogManager{
			Dir: filepath.Join(stateDir, "logs"),
			Now: func() time.Time {
				return time.Date(2026, 7, 1, 12, 30, 45, 0, time.UTC)
			},
		},
		SyncHistory: NewSyncHistoryStore(filepath.Join(stateDir, "sync-history.json")),
		KnownHosts:  filepath.Join(stateDir, ".ssh", "known_hosts"),
	}
}

func addWebAuth(t *testing.T, app *App, req *http.Request, role string) {
	t.Helper()
	member, err := app.MemberStore.SetupAdmin("Test Admin", "admin@example.com", "password123")
	if err != nil {
		t.Fatalf("setup admin: %v", err)
	}
	if role != "" && role != "admin" {
		member.Role = role
		db, err := app.MemberStore.Load()
		if err != nil {
			t.Fatalf("load members: %v", err)
		}
		for i := range db.Members {
			if db.Members[i].Email == member.Email {
				db.Members[i].Role = role
			}
		}
		if err := app.MemberStore.Save(db); err != nil {
			t.Fatalf("save members: %v", err)
		}
	}
	rec := httptest.NewRecorder()
	if err := app.setWebSession(rec, member); err != nil {
		t.Fatalf("set web session: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected auth cookie")
	}
	req.AddCookie(cookies[0])
}

func webChallengeForTest(t *testing.T, app App) map[string]string {
	t.Helper()
	challenge, err := app.newWebChallenge()
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}
	token := challenge["token"]
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		t.Fatalf("challenge parts = %#v", parts)
	}
	return map[string]string{"token": token, "answer": parts[1]}
}

func writeConfig(t *testing.T, dir, key string) string {
	t.Helper()
	config := strings.ReplaceAll(sampleConfig, "~/.ssh/example.pem", key)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
