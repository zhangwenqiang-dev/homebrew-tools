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
	"sort"
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
				"version": "0.1.42",
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
	case "cm_find_profile_by_apple":
		return s.mcpFindProfileByApple(cfg, call.Arguments)
	case "cm_check_profile":
		profile, err := requireMCPProfile(cfg, call.Arguments)
		if err != nil {
			return nil, err
		}
		if text := missingMCPInputText(profile, false); text != "" {
			return mcpText(text), nil
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
	case "cm_aws_capacity":
		return s.mcpAWSCapacity(ctx, cfg, call.Arguments)
	case "cm_aws_status":
		return s.mcpAWSStatus(ctx, cfg, call.Arguments)
	case "cm_aws_wait_ready":
		return s.mcpAWSWaitReady(ctx, cfg, call.Arguments)
	case "cm_aws_create_mac":
		return s.mcpAWSCreateMac(ctx, cfg, call.Arguments)
	case "cm_aws_open_mac_by_email":
		return s.mcpAWSOpenMacByEmail(ctx, cfg, call.Arguments)
	case "cm_aws_adopt_mac":
		return s.mcpAWSAdoptMac(ctx, cfg, call.Arguments)
	case "cm_aws_adopt_host":
		return s.mcpAWSAdoptHost(ctx, cfg, call.Arguments)
	case "cm_aws_launch_on_host":
		return s.mcpAWSLaunchOnHost(ctx, cfg, call.Arguments)
	case "cm_aws_destroy_mac":
		return s.mcpAWSDestroyMac(ctx, cfg, call.Arguments)
	case "cm_aws_destroy_mac_by_email":
		return s.mcpAWSDestroyMacByEmail(ctx, cfg, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (s MCPServer) mcpFindProfileByApple(cfg Config, args map[string]interface{}) (interface{}, error) {
	email, err := requiredString(args, "apple_email")
	if err != nil {
		return mcpText("Apple account email is required. Ask the user to choose one.\n" + FormatAppleAccountChoices(cfg)), nil
	}
	profile, err := cfg.ProfileByAppleEmail(email)
	if err != nil {
		return mcpText(err.Error()), nil
	}
	return mcpText(fmt.Sprintf("Apple account: %s\nProfile: %s\nDescription: %s\n", email, profile.Name, profile.Description)), nil
}

func (s MCPServer) mcpPush(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return nil, err
	}
	if text := missingMCPInputText(profile, false); text != "" {
		return mcpText(text), nil
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
	extraFilters, err := mcpSyncFilters(args)
	if err != nil {
		return nil, err
	}
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
	rsyncArgs, err := RsyncPushArgs(profile, localPath, remoteDir, mergeSyncFilters(profile.Sync.Push, extraFilters))
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
	if text := missingMCPInputText(profile, false); text != "" {
		return mcpText(text), nil
	}
	remotePath, err := requiredString(args, "remote_path")
	if err != nil {
		return nil, err
	}
	localDir := stringArg(args, "local_dir", ".")
	extraFilters, err := mcpSyncFilters(args)
	if err != nil {
		return nil, err
	}
	preview := fmt.Sprintf("Pull %s -> %s\n", RemoteTarget(profile, remotePath), localDir)
	if !boolArg(args, "confirm") {
		return mcpText(preview + "Preview only. Call again with confirm=true to execute."), nil
	}
	if !s.validateMCPRsync(profile) {
		return nil, fmt.Errorf("profile validation failed")
	}
	rsyncArgs, err := RsyncPullArgs(profile, remotePath, localDir, mergeSyncFilters(profile.Sync.Pull, extraFilters))
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

func (s MCPServer) mcpAWSCapacity(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	_, capacity, err := s.App.AWSService.Capacity(ctx, profile)
	if err != nil {
		return nil, err
	}
	return mcpText(FormatAWSCapacity(plan, capacity)), nil
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

func (s MCPServer) mcpAWSWaitReady(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	_, status, err := s.App.AWSService.WaitReady(ctx, profile)
	if err != nil {
		return nil, err
	}
	return mcpText(FormatAWSReadyStatus(plan, status)), nil
}

func (s MCPServer) mcpAWSCreateMac(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	if text := missingMCPInputText(profile, true); text != "" {
		return mcpText(text), nil
	}
	text := FormatMacPlan(plan)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to execute AWS creation."), nil
	}
	_, result, err := s.App.AWSService.Create(ctx, profile)
	if err != nil {
		return mcpText(text + awsStoppedMessage("AWS create", err)), nil
	}
	_, status, err := s.App.AWSService.WaitReady(ctx, profile)
	if err != nil {
		return mcpText(text + FormatAWSCreateResult(plan, result) + fmt.Sprintf("AWS wait-ready failed: %v\n", err)), nil
	}
	return mcpText(text + FormatAWSCreateResult(plan, result) + FormatAWSReadyStatus(plan, status)), nil
}

func (s MCPServer) mcpAWSOpenMacByEmail(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	email, _ := args["apple_email"].(string)
	profile, err := requireMCPAppleProfile(cfg, args)
	if err != nil {
		return mcpText(err.Error()), nil
	}
	plan, err := s.App.AWSService.Plan(profile)
	if err != nil {
		return nil, err
	}
	if text := missingMCPInputText(profile, true); text != "" {
		return mcpText(text), nil
	}
	_, status, err := s.App.AWSService.Status(ctx, profile)
	if err != nil {
		return nil, err
	}
	action := AWSOpenAction(status)
	var candidates []AWSCreateAttempt
	if action.Kind == "create" {
		_, values, err := s.App.AWSService.CreateCandidates(ctx, profile)
		if err != nil {
			return nil, err
		}
		candidates = values
	}
	text := fmt.Sprintf("Resolved Apple account %s -> profile %s\n", email, profile.Name) + FormatAWSOpenPreviewWithCandidates(plan, status, candidates)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to open or wait for this Mac."), nil
	}
	switch action.Kind {
	case "ready":
		return mcpText(text + FormatAWSReadyStatus(plan, status)), nil
	case "wait-ready":
		_, readyStatus, err := s.App.AWSService.WaitReady(ctx, profile)
		if err != nil {
			return mcpText(text + fmt.Sprintf("AWS wait-ready failed: %v\n", err)), nil
		}
		return mcpText(text + FormatAWSReadyStatus(plan, readyStatus)), nil
	case "launch-on-host":
		_, result, err := s.App.AWSService.LaunchOnHost(ctx, profile, action.HostID)
		if err != nil {
			return mcpText(text + awsStoppedMessage("AWS launch-on-host", err)), nil
		}
		_, readyStatus, err := s.App.AWSService.WaitReady(ctx, profile)
		if err != nil {
			return mcpText(text + FormatAWSCreateResult(plan, result) + fmt.Sprintf("AWS wait-ready failed: %v\n", err)), nil
		}
		return mcpText(text + FormatAWSCreateResult(plan, result) + FormatAWSReadyStatus(plan, readyStatus)), nil
	case "create":
		_, result, err := s.App.AWSService.Create(ctx, profile)
		if err != nil {
			return mcpText(text + awsStoppedMessage("AWS create", err)), nil
		}
		_, readyStatus, err := s.App.AWSService.WaitReady(ctx, profile)
		if err != nil {
			return mcpText(text + FormatAWSCreateResult(plan, result) + fmt.Sprintf("AWS wait-ready failed: %v\n", err)), nil
		}
		return mcpText(text + FormatAWSCreateResult(plan, result) + FormatAWSReadyStatus(plan, readyStatus)), nil
	default:
		return mcpText(text + fmt.Sprintf("Cannot continue automatically: %s\n", action.Detail)), nil
	}
}

func (s MCPServer) mcpAWSAdoptMac(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	if text := missingMCPInputText(profile, true); text != "" {
		return mcpText(text), nil
	}
	_, status, err := s.App.AWSService.AdoptionPreview(ctx, profile)
	if err != nil {
		return nil, err
	}
	text := FormatAWSAdoptionPreview(plan, status)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to tag these resources as cm-managed."), nil
	}
	_, result, err := s.App.AWSService.Adopt(ctx, profile)
	if err != nil {
		return nil, err
	}
	return mcpText(text + FormatAWSAdoptResult(plan, result)), nil
}

func (s MCPServer) mcpAWSAdoptHost(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	if text := missingMCPInputText(profile, true); text != "" {
		return mcpText(text), nil
	}
	hostID, err := requiredString(args, "host_id")
	if err != nil {
		return nil, err
	}
	_, host, err := s.App.AWSService.AdoptHostPreview(ctx, profile, hostID)
	if err != nil {
		return nil, err
	}
	text := FormatAWSAdoptHostPreview(plan, host)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to tag this host as cm-managed."), nil
	}
	_, result, err := s.App.AWSService.AdoptHost(ctx, profile, hostID)
	if err != nil {
		return nil, err
	}
	return mcpText(text + FormatAWSAdoptResult(plan, result)), nil
}

func (s MCPServer) mcpAWSLaunchOnHost(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return nil, err
	}
	if text := missingMCPInputText(profile, true); text != "" {
		return mcpText(text), nil
	}
	hostID, err := requiredString(args, "host_id")
	if err != nil {
		return nil, err
	}
	_, preview, err := s.App.AWSService.LaunchOnHostPreview(ctx, profile, hostID)
	if err != nil {
		return nil, err
	}
	text := FormatAWSLaunchOnHostPreview(plan, preview)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to launch EC2 on this host."), nil
	}
	_, result, err := s.App.AWSService.LaunchOnHost(ctx, profile, hostID)
	if err != nil {
		return mcpText(text + awsStoppedMessage("AWS launch-on-host", err)), nil
	}
	_, status, err := s.App.AWSService.WaitReady(ctx, profile)
	if err != nil {
		return mcpText(text + FormatAWSCreateResult(plan, result) + fmt.Sprintf("AWS wait-ready failed: %v\n", err)), nil
	}
	return mcpText(text + FormatAWSCreateResult(plan, result) + FormatAWSReadyStatus(plan, status)), nil
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
		var partial AWSDestroyPartialError
		if errors.As(err, &partial) {
			return mcpText(text + FormatAWSDestroyResult(plan, partial.Result) + fmt.Sprintf("AWS destroy partially completed: %v\n", err)), nil
		}
		return nil, err
	}
	return mcpText(text + FormatAWSDestroyResult(plan, result)), nil
}

func (s MCPServer) mcpAWSDestroyMacByEmail(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPAppleProfile(cfg, args)
	if err != nil {
		return mcpText(err.Error()), nil
	}
	args = cloneArgs(args)
	args["profile"] = profile.Name
	return s.mcpAWSDestroyMac(ctx, cfg, args)
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
	names := sortedProfileNames(cfg)
	if len(names) == 0 {
		return "no profiles configured"
	}
	nameWidth := len("PROFILE")
	for _, name := range names {
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
	}
	fmt.Fprintf(&b, "%-*s  %s\n", nameWidth, "PROFILE", "DESCRIPTION")
	fmt.Fprintf(&b, "%s  %s\n", strings.Repeat("-", nameWidth), strings.Repeat("-", len("DESCRIPTION")))
	for _, name := range names {
		description := cfg.Profiles[name].Description
		if description == "" {
			description = "-"
		}
		fmt.Fprintf(&b, "%-*s  %s\n", nameWidth, name, description)
	}
	return b.String()
}

func profileSummaryText(profile Profile) string {
	var b bytes.Buffer
	printSummary(&b, profile)
	return b.String()
}

func missingMCPInputText(profile Profile, requireCreator bool) string {
	var missing []string
	if profile.IdentityFile == "" {
		missing = append(missing, "identity_file")
	}
	if requireCreator && profile.AWS.Creator == "" {
		missing = append(missing, "aws.creator")
	}
	if len(missing) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Profile %s is missing required input: %s\n", profile.Name, strings.Join(missing, ", "))
	fmt.Fprintln(&b, "Ask the user to provide these values, then update the profile config or defaults.")
	if profile.IdentityFile == "" {
		fmt.Fprintln(&b, "- identity_file: PEM name or path, for example example-key or ~/.ssh/example-key.pem")
	}
	if requireCreator && profile.AWS.Creator == "" {
		fmt.Fprintln(&b, "- aws.creator: creator display name for AWS cm-creator tag")
	}
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

func requireMCPAppleProfile(cfg Config, args map[string]interface{}) (Profile, error) {
	email, err := requiredString(args, "apple_email")
	if err != nil {
		return Profile{}, fmt.Errorf("Apple account email is required. Ask the user to choose one.\n%s", FormatAppleAccountChoices(cfg))
	}
	return cfg.ProfileByAppleEmail(email)
}

func cloneArgs(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
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

func mcpSyncFilters(args map[string]interface{}) (SyncFilters, error) {
	includes, err := optionalStringArrayArg(args, "includes")
	if err != nil {
		return SyncFilters{}, err
	}
	excludes, err := optionalStringArrayArg(args, "excludes")
	if err != nil {
		return SyncFilters{}, err
	}
	return SyncFilters{Includes: includes, Excludes: excludes}, nil
}

func optionalStringArrayArg(args map[string]interface{}, name string) ([]string, error) {
	value, ok := args[name]
	if !ok || value == nil {
		return nil, nil
	}
	raw, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", name)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("%s must be an array of non-empty strings", name)
		}
		out = append(out, text)
	}
	return out, nil
}

func mcpTools() []map[string]interface{} {
	return []map[string]interface{}{
		mcpTool("cm_list_profiles", "List configured cm profiles.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}),
		mcpTool("cm_find_profile_by_apple", "Find the cm profile for an explicit Apple account email. If the user did not provide an email, ask them to choose from configured accounts.", appleEmailSchema()),
		mcpTool("cm_check_profile", "Validate a profile without connecting.", profileSchema()),
		mcpTool("cm_push", "Preview or execute rsync upload with optional includes/excludes filters. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":    stringSchema(),
				"local_path": stringSchema(),
				"remote_dir": stringSchema(),
				"includes":   stringArraySchema(),
				"excludes":   stringArraySchema(),
				"confirm":    map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "local_path", "remote_dir"},
		}),
		mcpTool("cm_pull", "Preview or execute rsync download with optional includes/excludes filters. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":     stringSchema(),
				"remote_path": stringSchema(),
				"local_dir":   stringSchema(),
				"includes":    stringArraySchema(),
				"excludes":    stringArraySchema(),
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
		mcpTool("cm_aws_capacity", "Read-only AWS Mac capacity report: Dedicated Host quotas, active host usage, remaining capacity, and offering AZs for a profile.", profileSchema()),
		mcpTool("cm_aws_status", "Describe managed AWS Mac Dedicated Hosts, EC2 instances, Elastic IP association, and EC2 status checks for a profile.", profileSchema()),
		mcpTool("cm_aws_wait_ready", "Wait until the managed AWS Mac EC2 instance is running, EIP-bound, and system, instance, and EBS status checks are ok.", profileSchema()),
		mcpTool("cm_aws_create_mac", "Preview or execute AWS Mac creation. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_open_mac_by_email", "Preview or open an AWS Mac for an explicit Apple account email. Never infer the email from context; ask the user if missing. Requires confirm=true to mutate AWS.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"apple_email": stringSchema(),
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"apple_email"},
		}),
		mcpTool("cm_aws_adopt_mac", "Preview or tag existing AWS Mac resources as cm-managed. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_adopt_host", "Preview or tag an existing empty AWS Mac Dedicated Host as cm-managed. Requires confirm=true to mutate tags.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"host_id": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "host_id"},
		}),
		mcpTool("cm_aws_launch_on_host", "Preview or launch AWS Mac EC2 on an existing Dedicated Host. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"host_id": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "host_id"},
		}),
		mcpTool("cm_aws_destroy_mac", "Preview or execute AWS Mac destruction. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_destroy_mac_by_email", "Preview or release AWS Mac compute resources for an explicit Apple account email. This never releases Elastic IP allocations. Requires confirm=true to execute.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"apple_email": stringSchema(),
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"apple_email"},
		}),
	}
}

func FormatMCPToolsText() string {
	tools := mcpTools()
	sort.Slice(tools, func(i, j int) bool {
		return fmt.Sprint(tools[i]["name"]) < fmt.Sprint(tools[j]["name"])
	})
	rows := make([][]string, 0, len(tools)+1)
	rows = append(rows, []string{"TOOL", "DESCRIPTION", "REQUIRED"})
	for _, tool := range tools {
		rows = append(rows, []string{
			fmt.Sprint(tool["name"]),
			fmt.Sprint(tool["description"]),
			strings.Join(mcpToolRequiredParams(tool), ", "),
		})
	}
	return formatRows(rows)
}

func WriteMCPToolsJSON(out io.Writer) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(map[string]interface{}{"tools": mcpTools()})
}

func mcpToolRequiredParams(tool map[string]interface{}) []string {
	schema, _ := tool["inputSchema"].(map[string]interface{})
	rawRequired, _ := schema["required"].([]string)
	if rawRequired != nil {
		return rawRequired
	}
	rawAny, _ := schema["required"].([]interface{})
	required := make([]string, 0, len(rawAny))
	for _, value := range rawAny {
		required = append(required, fmt.Sprint(value))
	}
	return required
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

func appleEmailSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"apple_email": stringSchema()},
		"required":   []string{"apple_email"},
	}
}

func stringSchema() map[string]string {
	return map[string]string{"type": "string"}
}

func stringArraySchema() map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": stringSchema()}
}
