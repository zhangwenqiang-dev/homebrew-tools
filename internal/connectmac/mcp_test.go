package connectmac

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
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

func TestMCPCheckAsksForMissingIdentityFile(t *testing.T) {
	app, config, _ := mcpTestAppWithConfig(t, strings.ReplaceAll(strings.ReplaceAll(sampleConfig,
		"  identity_file: ~/.ssh/default.pem\n", ""),
		"    identity_file: ~/.ssh/example.pem\n", ""))
	out := runMCPCall(t, app, config, "cm_check_profile", map[string]interface{}{
		"profile": "xcode-vnc",
	})
	if !strings.Contains(out, "missing required input: identity_file") {
		t.Fatalf("output = %q", out)
	}
}

func TestMCPAWSCreateAsksForMissingCreator(t *testing.T) {
	configData := strings.ReplaceAll(sampleConfig, "  aws:\n    creator: Default Creator\n", "")
	configData = strings.ReplaceAll(configData, "      creator: Xiao Chen\n", "")
	app, config, _ := mcpTestAppWithConfig(t, configData)
	out := runMCPCall(t, app, config, "cm_aws_create_mac", map[string]interface{}{
		"profile": "xcode-vnc",
	})
	if !strings.Contains(out, "missing required input: aws.creator") {
		t.Fatalf("output = %q", out)
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

func TestMCPAWSCapacity(t *testing.T) {
	app, config, _ := mcpTestApp(t)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{
			quotas: map[string]float64{"mac2.metal": 1},
			allHosts: []DedicatedHostStatus{
				{HostID: "h-1", State: "available", InstanceType: "mac2.metal", ZoneID: "usw2-az1"},
			},
			offerings: map[string][]string{"mac2.metal": {"usw2-az1"}},
		}, nil
	}
	out := runMCPCall(t, app, config, "cm_aws_capacity", map[string]interface{}{
		"profile": "xcode-vnc",
	})
	if !strings.Contains(out, "AWS Mac capacity") || !strings.Contains(out, "Running Dedicated mac2 Hosts") {
		t.Fatalf("output = %q", out)
	}
}

func TestMCPFindProfileByAppleEmail(t *testing.T) {
	app, config, _ := mcpTestApp(t)
	out := runMCPCall(t, app, config, "cm_find_profile_by_apple", map[string]interface{}{
		"apple_email": "user@example.com",
	})
	if !strings.Contains(out, "Profile: xcode-vnc") {
		t.Fatalf("output = %q", out)
	}
}

func TestMCPFindProfileByAppleEmailAsksWhenMissing(t *testing.T) {
	app, config, _ := mcpTestApp(t)
	out := runMCPCall(t, app, config, "cm_find_profile_by_apple", map[string]interface{}{})
	if !strings.Contains(out, "Apple account email is required") || !strings.Contains(out, "xcode-vnc: user@example.com") {
		t.Fatalf("output = %q", out)
	}
}

func TestMCPAWSOpenMacByEmailPreview(t *testing.T) {
	app, config, _ := mcpTestApp(t)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{}}, nil
	}
	out := runMCPCall(t, app, config, "cm_aws_open_mac_by_email", map[string]interface{}{
		"apple_email": "user@example.com",
	})
	if !strings.Contains(out, "Resolved Apple account user@example.com -> profile xcode-vnc") || !strings.Contains(out, "AWS Mac open preview") {
		t.Fatalf("output = %q", out)
	}
}

func TestMCPAWSCreateFailureReportsReasonAndStops(t *testing.T) {
	app, config, _ := mcpTestApp(t)
	app.AWSService.NewClient = func(ctx context.Context, plan MacPlan) (AWSClient, error) {
		return &fakeAWSClient{
			eip:    ElasticIP{AllocationID: "<elastic-ip-allocation-id>", Tags: []AWSTagConfig{{Key: "Apple", Value: "user@example.com"}}},
			runErr: errString("RunInstances rejected selected host"),
		}, nil
	}
	out := runMCPCall(t, app, config, "cm_aws_create_mac", map[string]interface{}{
		"profile": "xcode-vnc",
		"confirm": true,
	})
	for _, want := range []string{"AWS create failed", "RunInstances rejected selected host", "Stopped", "wait for explicit instructions"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestMCPAWSWaitReady(t *testing.T) {
	app, config, _ := mcpTestApp(t)
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
	out := runMCPCall(t, app, config, "cm_aws_wait_ready", map[string]interface{}{
		"profile": "xcode-vnc",
	})
	if !strings.Contains(out, "AWS Mac ready for profile xcode-vnc: true") {
		t.Fatalf("output = %q", out)
	}
}

func TestMCPToolsIncludesAWSHostWorkflow(t *testing.T) {
	app, config, _ := mcpTestApp(t)
	out := runMCPToolsList(t, app, config)
	for _, want := range []string{"cm_find_profile_by_apple", "cm_aws_capacity", "cm_aws_wait_ready", "cm_aws_adopt_host", "cm_aws_launch_on_host", "cm_aws_open_mac_by_email", "cm_aws_destroy_mac_by_email"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tools output missing %q: %q", want, out)
		}
	}
}

func mcpTestApp(t *testing.T) (App, string, *fakeRunner) {
	t.Helper()
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	return mcpTestAppWithPath(t, dir, config)
}

func runMCPToolsList(t *testing.T, app App, config string) string {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
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
	return string(out.Bytes())
}

func mcpTestAppWithConfig(t *testing.T, configData string) (App, string, *fakeRunner) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	return mcpTestAppWithPath(t, dir, config)
}

func mcpTestAppWithPath(t *testing.T, dir, config string) (App, string, *fakeRunner) {
	t.Helper()
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
