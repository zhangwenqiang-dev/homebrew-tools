package connectmac

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type MCPServer struct {
	App        App
	ConfigPath string
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func (s MCPServer) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	for {
		body, err := readMCPMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req mcpRequest
		if err := json.Unmarshal(body, &req); err != nil {
			if err := writeMCPMessage(out, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32700, Message: err.Error()}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		resp := s.handle(ctx, req)
		if err := writeMCPMessage(out, resp); err != nil {
			return err
		}
	}
}

func (s MCPServer) handle(ctx context.Context, req mcpRequest) mcpResponse {
	resp := mcpResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]string{
				"name":    "cm",
				"version": "0.1.6",
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
		}
	case "tools/list":
		resp.Result = map[string]interface{}{"tools": mcpTools()}
	case "tools/call":
		result, err := s.handleTool(ctx, req.Params)
		if err != nil {
			resp.Error = &mcpError{Code: -32000, Message: err.Error()}
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &mcpError{Code: -32601, Message: "method not found"}
	}
	return resp
}

func (s MCPServer) handleTool(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var call mcpCallParams
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(s.ConfigPath)
	if err != nil {
		return nil, err
	}
	switch call.Name {
	case "cm_list_profiles":
		return mcpText(listProfilesText(cfg)), nil
	case "cm_check_profile":
		profile, err := requireMCPProfile(cfg, call.Arguments)
		if err != nil {
			return nil, err
		}
		errs := s.App.Validator.ValidateProfile(profile)
		if len(errs) > 0 {
			return mcpText(formatErrors(errs)), nil
		}
		return mcpText("check passed\n" + profileSummaryText(profile)), nil
	case "cm_push":
		return s.mcpPush(ctx, cfg, call.Arguments)
	case "cm_pull":
		return s.mcpPull(ctx, cfg, call.Arguments)
	case "cm_forget_host":
		return s.mcpForgetHost(ctx, cfg, call.Arguments)
	case "cm_aws_plan":
		return s.mcpAWSPlan(cfg, call.Arguments)
	case "cm_aws_status":
		return s.mcpAWSStatus(ctx, cfg, call.Arguments)
	case "cm_aws_create_mac":
		return s.mcpAWSCreateMac(ctx, cfg, call.Arguments)
	case "cm_aws_destroy_mac":
		return s.mcpAWSDestroyMac(ctx, cfg, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (s MCPServer) mcpPush(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return nil, err
	}
	localPath, err := requiredString(args, "local_path")
	if err != nil {
		return nil, err
	}
	remoteDir, err := requiredString(args, "remote_dir")
	if err != nil {
		return nil, err
	}
	remoteDir = NormalizeRemotePath(remoteDir)
	preview := fmt.Sprintf("Push %s -> %s\n", localPath, RemoteTarget(profile, remoteDir))
	if !boolArg(args, "confirm") {
		return mcpText(preview + "Preview only. Call again with confirm=true to execute."), nil
	}
	if _, err := os.Stat(localPath); err != nil {
		return nil, fmt.Errorf("read local path %s: %w", localPath, err)
	}
	if !s.validateMCPRsync(profile) {
		return nil, fmt.Errorf("profile validation failed")
	}
	rsyncArgs, err := RsyncPushArgs(profile, localPath, remoteDir, profile.Sync.Push.Excludes)
	if err != nil {
		return nil, err
	}
	if err := s.App.Runner.RunRsync(ctx, rsyncArgs); err != nil {
		return nil, err
	}
	return mcpText(preview + "Executed."), nil
}

func (s MCPServer) mcpPull(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return nil, err
	}
	remotePath, err := requiredString(args, "remote_path")
	if err != nil {
		return nil, err
	}
	localDir := stringArg(args, "local_dir", ".")
	preview := fmt.Sprintf("Pull %s -> %s\n", RemoteTarget(profile, remotePath), localDir)
	if !boolArg(args, "confirm") {
		return mcpText(preview + "Preview only. Call again with confirm=true to execute."), nil
	}
	if !s.validateMCPRsync(profile) {
		return nil, fmt.Errorf("profile validation failed")
	}
	rsyncArgs, err := RsyncPullArgs(profile, remotePath, localDir, profile.Sync.Pull.Excludes)
	if err != nil {
		return nil, err
	}
	if err := s.App.Runner.RunRsync(ctx, rsyncArgs); err != nil {
		return nil, err
	}
	return mcpText(preview + "Executed."), nil
}

func (s MCPServer) mcpForgetHost(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return nil, err
	}
	preview := fmt.Sprintf("Remove known_hosts entries for %s\n", profile.Host)
	if !boolArg(args, "confirm") {
		return mcpText(preview + "Preview only. Call again with confirm=true to execute."), nil
	}
	if profile.Host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if err := s.App.Runner.ForgetHost(ctx, profile.Host); err != nil {
		return nil, err
	}
	return mcpText(preview + "Executed."), nil
}

func (s MCPServer) validateMCPRsync(profile Profile) bool {
	errs := s.App.Validator.ValidateAccess(profile)
	if s.App.Validator.CheckRsync != nil {
		if err := s.App.Validator.CheckRsync(); err != nil {
			errs = append(errs, err)
		}
	}
	return len(errs) == 0
}

func (s MCPServer) mcpAWSPlan(cfg Config, args map[string]interface{}) (interface{}, error) {
	plan, err := s.mcpMacPlan(cfg, args)
	if err != nil {
		return nil, err
	}
	return mcpText(FormatMacPlan(plan)), nil
}

func (s MCPServer) mcpAWSStatus(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	_, status, err := s.App.AWSService.Status(ctx, profile)
	if err != nil {
		return nil, err
	}
	return mcpText(FormatAWSStatus(plan, status)), nil
}

func (s MCPServer) mcpAWSCreateMac(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	text := FormatMacPlan(plan)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to execute AWS creation."), nil
	}
	_, result, err := s.App.AWSService.Create(ctx, profile)
	if err != nil {
		return nil, err
	}
	return mcpText(text + FormatAWSCreateResult(plan, result)), nil
}

func (s MCPServer) mcpAWSDestroyMac(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	text := FormatMacDestroyPreview(plan)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to execute AWS destruction."), nil
	}
	_, result, err := s.App.AWSService.Destroy(ctx, profile)
	if err != nil {
		return nil, err
	}
	return mcpText(text + FormatAWSDestroyResult(plan, result)), nil
}

func (s MCPServer) mcpMacPlan(cfg Config, args map[string]interface{}) (MacPlan, error) {
	_, plan, err := s.mcpMacProfilePlan(cfg, args)
	return plan, err
}

func (s MCPServer) mcpMacProfilePlan(cfg Config, args map[string]interface{}) (Profile, MacPlan, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return Profile{}, MacPlan{}, err
	}
	errs := s.App.Validator.ValidateAWSProfile(profile)
	if len(errs) > 0 {
		return Profile{}, MacPlan{}, errors.New(strings.TrimSpace(formatErrors(errs)))
	}
	plan, err := s.App.AWSService.Plan(profile)
	return profile, plan, err
}

func readMCPMessage(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			length = parsed
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(reader, body)
	return body, err
}

func writeMCPMessage(out io.Writer, msg interface{}) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func mcpText(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": text}},
	}
}

func listProfilesText(cfg Config) string {
	var b bytes.Buffer
	for _, name := range sortedProfileNames(cfg) {
		p := cfg.Profiles[name]
		if p.Description != "" {
			fmt.Fprintf(&b, "%s\t%s\n", name, p.Description)
		} else {
			fmt.Fprintln(&b, name)
		}
	}
	if b.Len() == 0 {
		return "no profiles configured"
	}
	return b.String()
}

func profileSummaryText(profile Profile) string {
	var b bytes.Buffer
	printSummary(&b, profile)
	return b.String()
}

func formatErrors(errs []error) string {
	var b bytes.Buffer
	printErrors(&b, errs)
	return b.String()
}

func requireMCPProfile(cfg Config, args map[string]interface{}) (Profile, error) {
	name, err := requiredString(args, "profile")
	if err != nil {
		return Profile{}, err
	}
	profile, ok := cfg.Profile(name)
	if !ok {
		return Profile{}, unknownProfileError(cfg, name)
	}
	return profile, nil
}

func requiredString(args map[string]interface{}, name string) (string, error) {
	value, ok := args[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return text, nil
}

func stringArg(args map[string]interface{}, name, fallback string) string {
	value, ok := args[name].(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func boolArg(args map[string]interface{}, name string) bool {
	value, ok := args[name].(bool)
	return ok && value
}

func mcpTools() []map[string]interface{} {
	return []map[string]interface{}{
		mcpTool("cm_list_profiles", "List configured cm profiles.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}),
		mcpTool("cm_check_profile", "Validate a profile without connecting.", profileSchema()),
		mcpTool("cm_push", "Preview or execute rsync upload. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":    stringSchema(),
				"local_path": stringSchema(),
				"remote_dir": stringSchema(),
				"confirm":    map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "local_path", "remote_dir"},
		}),
		mcpTool("cm_pull", "Preview or execute rsync download. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":     stringSchema(),
				"remote_path": stringSchema(),
				"local_dir":   stringSchema(),
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "remote_path"},
		}),
		mcpTool("cm_forget_host", "Preview or remove known_hosts entries for a profile host. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_plan", "Preview AWS Mac Dedicated Host, EC2, and EIP operations for a profile.", profileSchema()),
		mcpTool("cm_aws_status", "Describe managed AWS Mac Dedicated Hosts, EC2 instances, and Elastic IP association for a profile.", profileSchema()),
		mcpTool("cm_aws_create_mac", "Preview or execute AWS Mac creation. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_destroy_mac", "Preview or execute AWS Mac destruction. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
	}
}

func mcpTool(name, description string, schema map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"name": name, "description": description, "inputSchema": schema}
}

func profileSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"profile": stringSchema()},
		"required":   []string{"profile"},
	}
}

func stringSchema() map[string]string {
	return map[string]string{"type": "string"}
}
