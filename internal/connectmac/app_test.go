package connectmac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{}
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
	state, ok, err := app.StateManager.Load("xcode-vnc")
	if err != nil || !ok || state.PID != 55 {
		t.Fatalf("state = %+v ok=%t err=%v", state, ok, err)
	}
}

func TestAppStartDoesNotSaveStateWhenTunnelFailsHealthCheck(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	runner := &fakeRunner{startErr: errors.New("Permission denied (publickey)")}
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
		AWSService: AWSService{Now: func() time.Time {
			return time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
		}},
	}
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
