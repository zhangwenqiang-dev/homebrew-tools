package connectmac

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPListProfiles(t *testing.T) {
	app, config, runner := mcpTestApp(t)
	out := runMCPCall(t, app, config, "cm_list_profiles", map[string]interface{}{})
	if !strings.Contains(out, "xcode-vnc") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "PROFILE") || !strings.Contains(out, "DESCRIPTION") {
		t.Fatalf("output missing table header = %q", out)
	}
	if len(runner.rsync) != 0 {
		t.Fatal("list must not run rsync")
	}
}

func TestMCPPushRequiresConfirm(t *testing.T) {
	app, config, runner := mcpTestApp(t)
	localPath := filepath.Join(t.TempDir(), "file.txt")
	writeFile(t, localPath, "data")
	out := runMCPCall(t, app, config, "cm_push", map[string]interface{}{
		"profile":    "xcode-vnc",
		"local_path": localPath,
		"remote_dir": "~/Documents/",
	})
	if !strings.Contains(out, "Preview only") {
		t.Fatalf("output = %q", out)
	}
	if len(runner.rsync) != 0 {
		t.Fatal("preview must not run rsync")
	}
}

func TestMCPPushConfirmRunsRsync(t *testing.T) {
	app, config, runner := mcpTestApp(t)
	localPath := filepath.Join(t.TempDir(), "file.txt")
	writeFile(t, localPath, "data")
	out := runMCPCall(t, app, config, "cm_push", map[string]interface{}{
		"profile":    "xcode-vnc",
		"local_path": localPath,
		"remote_dir": "~/Documents/",
		"confirm":    true,
	})
	if !strings.Contains(out, "Executed") {
		t.Fatalf("output = %q", out)
	}
	if len(runner.rsync) == 0 {
		t.Fatal("confirm=true should run rsync")
	}
}

func TestMCPForgetHostRequiresConfirm(t *testing.T) {
	app, config, runner := mcpTestApp(t)
	out := runMCPCall(t, app, config, "cm_forget_host", map[string]interface{}{
		"profile": "xcode-vnc",
	})
	if !strings.Contains(out, "Preview only") {
		t.Fatalf("output = %q", out)
	}
	if runner.forgotHost != "" {
		t.Fatal("preview must not forget host")
	}
}

func TestMCPForgetHostConfirmRuns(t *testing.T) {
	app, config, runner := mcpTestApp(t)
	out := runMCPCall(t, app, config, "cm_forget_host", map[string]interface{}{
		"profile": "xcode-vnc",
		"confirm": true,
	})
	if !strings.Contains(out, "Executed") {
		t.Fatalf("output = %q", out)
	}
	if runner.forgotHost != "mac-host.example.com" {
		t.Fatalf("forgot host = %q", runner.forgotHost)
	}
}

func TestMCPAWSPlan(t *testing.T) {
	app, config, runner := mcpTestApp(t)
	out := runMCPCall(t, app, config, "cm_aws_plan", map[string]interface{}{
		"profile": "xcode-vnc",
	})
	if !strings.Contains(out, "AWS Mac plan") || !strings.Contains(out, "xcode-user@example.com") {
		t.Fatalf("output = %q", out)
	}
	if len(runner.rsync) != 0 || runner.forgotHost != "" {
		t.Fatal("aws plan must not run side-effect commands")
	}
}

func mcpTestApp(t *testing.T) (App, string, *fakeRunner) {
	t.Helper()
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	runner := &fakeRunner{}
	app := App{
		Out:       &bytes.Buffer{},
		Err:       &bytes.Buffer{},
		Runner:    runner,
		Validator: NewValidatorForTest(nil),
		StateManager: StateManager{
			Dir:       filepath.Join(dir, "state"),
			IsRunning: func(pid int) bool { return pid == 55 },
		},
		AWSService: AWSService{Now: func() time.Time {
			return time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
		}},
	}
	return app, config, runner
}

func runMCPCall(t *testing.T, app App, config, tool string, args map[string]interface{}) string {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      tool,
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var in bytes.Buffer
	if err := writeMCPMessage(&in, json.RawMessage(body)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	server := MCPServer{App: app, ConfigPath: config}
	if err := server.Serve(context.Background(), &in, &out); err != nil {
		t.Fatal(err)
	}
	return decodeMCPText(t, out.Bytes())
}

func decodeMCPText(t *testing.T, data []byte) string {
	t.Helper()
	reader := bytes.NewReader(data)
	br := make([]byte, len(data))
	n, err := reader.Read(br)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(br[:n])
	_, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatalf("invalid MCP response: %q", raw)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *mcpError `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("mcp error: %v", resp.Error.Message)
	}
	if len(resp.Result.Content) == 0 {
		return ""
	}
	return resp.Result.Content[0].Text
}

func TestMCPMessageRoundTrip(t *testing.T) {
	var out bytes.Buffer
	if err := writeMCPMessage(&out, map[string]string{"ok": "yes"}); err != nil {
		t.Fatal(err)
	}
	body, err := readMCPMessage(bufio.NewReader(bytes.NewBuffer(out.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "yes") {
		t.Fatalf("body = %s", body)
	}
}
