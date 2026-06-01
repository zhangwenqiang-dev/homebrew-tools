package connectmac

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
