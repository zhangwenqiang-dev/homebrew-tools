package connectmac

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	foreground []string
	background []string
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
}

func TestAppAWSDestroyResolvesAppleEmail(t *testing.T) {
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if code := app.Run(context.Background(), []string{"aws", "destroy", "user@example.com", "--config", config}); code != 0 {
		t.Fatalf("aws destroy by email code = %d, err = %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"Resolved Apple account user@example.com -> profile xcode-vnc", "AWS Mac destroy preview", "retain the Elastic IP allocation"} {
		if !strings.Contains(text, want) {
			t.Fatalf("destroy by email output missing %q:\n%s", want, text)
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
