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
				"version": "0.1.48",
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
	switch call.Name {
	case "cm_mcp_guide":
		return mcpTextData(FormatMCPGuideText(), map[string]interface{}{
			"preview_rule":     "confirm missing or false means preview only; confirm=true executes mutations after explicit user approval",
			"primary_identity": "apple_email for open/create/destroy; profile for local profile, status, sync, and check tools",
			"safe_destroy":     "destroy tools never release Elastic IP allocations",
		}), nil
	}
	cfg, err := LoadConfig(s.ConfigPath)
	if err != nil {
		return mcpUserError("config", err), nil
	}
	switch call.Name {
	case "cm_list_profiles":
		return mcpText(listProfilesText(cfg)), nil
	case "cm_find_profile_by_apple":
		return s.mcpFindProfileByApple(cfg, call.Arguments)
	case "cm_check_profile":
		profile, err := requireMCPProfile(cfg, call.Arguments)
		if err != nil {
			return mcpUserError("profile", err), nil
		}
		if text := missingMCPInputText(profile, false); text != "" {
			return mcpTextData(text, mcpCheckData(profile, false, []string{text})), nil
		}
		errs := s.App.Validator.ValidateProfile(profile)
		if len(errs) > 0 {
			return mcpTextData(formatErrors(errs), mcpCheckData(profile, false, errorStrings(errs))), nil
		}
		return mcpTextData("check passed\n"+profileSummaryText(profile), mcpCheckData(profile, true, nil)), nil
	case "cm_profile_show":
		return s.mcpProfileShow(cfg, call.Arguments)
	case "cm_profile_add":
		return s.mcpProfileAdd(cfg, call.Arguments)
	case "cm_profile_remove":
		return s.mcpProfileRemove(ctx, cfg, call.Arguments)
	case "cm_profile_rename":
		return s.mcpProfileRename(cfg, call.Arguments)
	case "cm_profile_export":
		return s.mcpProfileShow(cfg, call.Arguments)
	case "cm_doctor":
		return s.mcpDoctor(call.Arguments)
	case "cm_dashboard":
		return s.mcpDashboard(ctx, call.Arguments)
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

func (s MCPServer) mcpProfileShow(cfg Config, args map[string]interface{}) (interface{}, error) {
	ref, err := requiredString(args, "profile")
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	profile, err := resolveProfileRef(cfg, ref)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	return mcpText(FormatProfileFile(profile)), nil
}

func (s MCPServer) mcpProfileAdd(cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := mcpProfileFromArgs(args)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	if _, ok := cfg.Profile(profile.Name); ok {
		return mcpUserError("profile", fmt.Errorf("profile %q already exists", profile.Name)), nil
	}
	if profile.Description == "" && profile.AWS.AccountEmail != "" {
		profile.Description = "Apple account: " + profile.AWS.AccountEmail
	}
	if profile.AWS.ElasticIPOwnerTag.Key == "" && profile.AWS.AccountEmail != "" {
		profile.AWS.ElasticIPOwnerTag = AWSTagConfig{Key: "Apple", Value: profile.AWS.AccountEmail}
	}
	text := fmt.Sprintf("Create local profile %s\n", profile.Name)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to write the profile file."), nil
	}
	path, err := WriteProfileFile(s.ConfigPath, profile)
	if err != nil {
		return mcpUserError("profile_file", err), nil
	}
	return mcpText(text + "Created profile file: " + path), nil
}

func (s MCPServer) mcpProfileRemove(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	name, err := requiredString(args, "profile")
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	profile, ok := cfg.Profile(name)
	if !ok {
		return mcpUserError("profile", unknownProfileError(cfg, name)), nil
	}
	text := fmt.Sprintf("Remove local profile %s\n", profile.Name)
	if !boolArg(args, "force_local") {
		if blocked, detail := s.App.profileRemoveBlockedByAWS(ctx, profile); blocked {
			return mcpText(text + detail), nil
		}
	}
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to remove the local profile file."), nil
	}
	path, err := RemoveProfileFile(s.ConfigPath, profile.Name)
	if err != nil {
		return mcpUserError("profile_file", err), nil
	}
	_ = s.App.StateManager.Remove(profile.Name)
	return mcpText(text + "Removed profile file: " + path), nil
}

func (s MCPServer) mcpProfileRename(cfg Config, args map[string]interface{}) (interface{}, error) {
	oldName, err := requiredString(args, "profile")
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	newName, err := requiredString(args, "new_name")
	if err != nil {
		return mcpUserError("new_name", err), nil
	}
	if _, ok := cfg.Profile(oldName); !ok {
		return mcpUserError("profile", unknownProfileError(cfg, oldName)), nil
	}
	if _, ok := cfg.Profile(newName); ok {
		return mcpUserError("profile", fmt.Errorf("profile %q already exists", newName)), nil
	}
	text := fmt.Sprintf("Rename local profile %s -> %s\n", oldName, newName)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to rename the profile file."), nil
	}
	oldPath, newPath, err := RenameProfileFile(s.ConfigPath, oldName, newName)
	if err != nil {
		return mcpUserError("profile_file", err), nil
	}
	_ = s.App.StateManager.Remove(oldName)
	return mcpText(fmt.Sprintf("%sRenamed profile file: %s -> %s", text, oldPath, newPath)), nil
}

func (s MCPServer) mcpDoctor(args map[string]interface{}) (interface{}, error) {
	var out, errOut bytes.Buffer
	app := s.App
	app.Out = &out
	app.Err = &errOut
	var cliArgs []string
	if boolArg(args, "fix") {
		cliArgs = append(cliArgs, "--fix")
	}
	code := app.runDoctor(s.ConfigPath, cliArgs)
	text := out.String()
	if errOut.Len() > 0 {
		text += errOut.String()
	}
	text += fmt.Sprintf("exit_code=%d\n", code)
	return mcpText(text), nil
}

func (s MCPServer) mcpDashboard(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	cfg, err := LoadConfig(s.ConfigPath)
	if err != nil {
		return mcpUserError("config", err), nil
	}
	var out, errOut bytes.Buffer
	app := s.App
	app.Out = &out
	app.Err = &errOut
	var cliArgs []string
	if boolArg(args, "aws") {
		cliArgs = append(cliArgs, "--aws")
	}
	code := app.runDashboard(ctx, cfg, cliArgs)
	text := out.String()
	if errOut.Len() > 0 {
		text += errOut.String()
	}
	text += fmt.Sprintf("exit_code=%d\n", code)
	data := map[string]interface{}{
		"ok":        code == 0,
		"exit_code": code,
		"aws":       boolArg(args, "aws"),
		"profiles":  s.mcpDashboardProfiles(ctx, cfg, boolArg(args, "aws")),
	}
	return mcpTextData(text, data), nil
}

func (s MCPServer) mcpDashboardProfiles(ctx context.Context, cfg Config, includeAWS bool) []map[string]interface{} {
	states, _ := s.App.StateManager.List()
	running := map[string]string{}
	for _, state := range states {
		running[state.Profile] = fmt.Sprintf("pid=%d", state.PID)
	}
	items := make([]map[string]interface{}, 0, len(cfg.Profiles))
	for _, name := range sortedProfileNames(cfg) {
		profile, _ := cfg.Profile(name)
		item := map[string]interface{}{
			"profile":     profile.Name,
			"apple_email": profile.AWS.AccountEmail,
			"region":      profile.AWS.Region,
			"host":        profile.Host,
			"tunnel":      running[profile.Name],
			"aws":         dashboardAWSConfigStatus(s.App.Validator.ValidateAWSProfile(profile)),
		}
		if includeAWS {
			item["ready"] = false
			item["decision"] = "config"
			item["next"] = "fix config"
			if len(s.App.Validator.ValidateAWSProfile(profile)) == 0 {
				_, status, err := s.App.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
				if err != nil {
					item["decision"] = "error"
					item["next"] = "cm aws status " + profile.Name
					item["error"] = err.Error()
				} else {
					action := AWSOpenAction(status)
					item["ready"] = AWSStatusReady(status)
					item["decision"] = action.Kind
					item["next"] = AWSOpenDecisionNextStep(profile.Name, action)
					item["eip_public_ip"] = status.ElasticIP.PublicIP
					item["eip_allocation"] = status.ElasticIP.AllocationID
					if len(status.Instances) > 0 {
						item["instance_id"] = status.Instances[0].InstanceID
						item["instance_state"] = status.Instances[0].State
					}
				}
			}
		}
		items = append(items, item)
	}
	return items
}

func mcpProfileFromArgs(args map[string]interface{}) (Profile, error) {
	name, err := requiredString(args, "name")
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{
		Name:         name,
		Description:  stringArg(args, "description", ""),
		User:         stringArg(args, "user", ""),
		Host:         stringArg(args, "host", ""),
		IdentityFile: NormalizeIdentityFileInput(stringArg(args, "identity_file", "")),
	}
	profile.AWS.Profile = stringArg(args, "aws_profile", "")
	profile.AWS.Region = stringArg(args, "region", "")
	profile.AWS.AccountEmail = stringArg(args, "apple_email", "")
	profile.AWS.Creator = stringArg(args, "creator", "")
	profile.AWS.KeyName = stringArg(args, "key_name", "")
	profile.AWS.SecurityGroupID = stringArg(args, "security_group_id", "")
	profile.AWS.ElasticIPAllocationID = stringArg(args, "elastic_ip_allocation_id", "")
	profile.AWS.ElasticIPPublicIP = stringArg(args, "elastic_ip_public_ip", "")
	profile.AWS.AvailabilityZoneIDs, err = optionalStringArrayArg(args, "availability_zone_ids")
	if err != nil {
		return Profile{}, err
	}
	profile.AWS.InstanceTypePriority, err = optionalStringArrayArg(args, "instance_type_priority")
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s MCPServer) mcpFindProfileByApple(cfg Config, args map[string]interface{}) (interface{}, error) {
	email, err := requiredString(args, "apple_email")
	if err != nil {
		return mcpTextData("Apple account email is required. Ask the user to choose one.\n"+FormatAppleAccountChoices(cfg), map[string]interface{}{
			"ok":          false,
			"found":       false,
			"error":       err.Error(),
			"apple_email": "",
		}), nil
	}
	profile, err := cfg.ProfileByAppleEmail(email)
	if err != nil {
		return mcpTextData(err.Error(), map[string]interface{}{
			"ok":          false,
			"found":       false,
			"error":       err.Error(),
			"apple_email": email,
		}), nil
	}
	return mcpTextData(fmt.Sprintf("Apple account: %s\nProfile: %s\nDescription: %s\n", email, profile.Name, profile.Description), map[string]interface{}{
		"ok":          true,
		"found":       true,
		"profile":     profile.Name,
		"apple_email": email,
		"description": profile.Description,
	}), nil
}

func (s MCPServer) mcpPush(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	if text := missingMCPInputText(profile, false); text != "" {
		return mcpTextData(text, mcpSyncData(profile, "push", false, "", "", SyncFilters{}, text)), nil
	}
	localPath, err := requiredString(args, "local_path")
	if err != nil {
		return mcpUserError("local_path", err), nil
	}
	remoteDir, err := requiredString(args, "remote_dir")
	if err != nil {
		return mcpUserError("remote_dir", err), nil
	}
	remoteDir = NormalizeRemotePath(remoteDir)
	extraFilters, err := mcpSyncFilters(args)
	if err != nil {
		return mcpUserError("sync_filters", err), nil
	}
	filters := mergeSyncFilters(profile.Sync.Push, extraFilters)
	preview := fmt.Sprintf("Push %s -> %s\n", localPath, RemoteTarget(profile, remoteDir))
	if !boolArg(args, "confirm") {
		return mcpTextData(preview+"Preview only. Call again with confirm=true to execute.", mcpSyncData(profile, "push", false, localPath, remoteDir, filters, "")), nil
	}
	if _, err := os.Stat(localPath); err != nil {
		return mcpUserError("local_path", fmt.Errorf("read local path %s: %w", localPath, err)), nil
	}
	if !s.validateMCPRsync(profile) {
		return mcpTextData("profile validation failed", mcpSyncData(profile, "push", true, localPath, remoteDir, filters, "profile validation failed")), nil
	}
	rsyncArgs, err := RsyncPushArgs(profile, localPath, remoteDir, filters)
	if err != nil {
		return mcpUserError("rsync_args", err), nil
	}
	if err := s.App.Runner.RunRsync(ctx, rsyncArgs); err != nil {
		return nil, err
	}
	return mcpTextData(preview+"Executed.", mcpSyncData(profile, "push", true, localPath, remoteDir, filters, "")), nil
}

func (s MCPServer) mcpPull(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	if text := missingMCPInputText(profile, false); text != "" {
		return mcpTextData(text, mcpSyncData(profile, "pull", false, "", "", SyncFilters{}, text)), nil
	}
	remotePath, err := requiredString(args, "remote_path")
	if err != nil {
		return mcpUserError("remote_path", err), nil
	}
	localDir := stringArg(args, "local_dir", ".")
	extraFilters, err := mcpSyncFilters(args)
	if err != nil {
		return mcpUserError("sync_filters", err), nil
	}
	filters := mergeSyncFilters(profile.Sync.Pull, extraFilters)
	preview := fmt.Sprintf("Pull %s -> %s\n", RemoteTarget(profile, remotePath), localDir)
	if !boolArg(args, "confirm") {
		return mcpTextData(preview+"Preview only. Call again with confirm=true to execute.", mcpSyncData(profile, "pull", false, remotePath, localDir, filters, "")), nil
	}
	if !s.validateMCPRsync(profile) {
		return mcpTextData("profile validation failed", mcpSyncData(profile, "pull", true, remotePath, localDir, filters, "profile validation failed")), nil
	}
	rsyncArgs, err := RsyncPullArgs(profile, remotePath, localDir, filters)
	if err != nil {
		return mcpUserError("rsync_args", err), nil
	}
	if err := s.App.Runner.RunRsync(ctx, rsyncArgs); err != nil {
		return nil, err
	}
	return mcpTextData(preview+"Executed.", mcpSyncData(profile, "pull", true, remotePath, localDir, filters, "")), nil
}

func (s MCPServer) mcpForgetHost(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPProfile(cfg, args)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	preview := fmt.Sprintf("Remove known_hosts entries for %s\n", profile.Host)
	if !boolArg(args, "confirm") {
		return mcpText(preview + "Preview only. Call again with confirm=true to execute."), nil
	}
	if profile.Host == "" {
		return mcpUserError("host", fmt.Errorf("host is required")), nil
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
		return mcpUserError("aws_plan", err), nil
	}
	return mcpText(FormatMacPlan(plan)), nil
}

func (s MCPServer) mcpAWSCapacity(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return mcpUserError("aws_profile", err), nil
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
		return mcpUserError("aws_profile", err), nil
	}
	_, status, err := s.App.AWSService.Status(ctx, profile)
	if err != nil {
		return nil, err
	}
	return mcpTextData(FormatAWSStatus(plan, status), mcpAWSStatusData(profile, plan, status, false)), nil
}

func (s MCPServer) mcpAWSWaitReady(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return mcpUserError("aws_profile", err), nil
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
		return mcpUserError("aws_profile", err), nil
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
		return mcpUserError("apple_email", err), nil
	}
	plan, err := s.App.AWSService.Plan(profile)
	if err != nil {
		return mcpUserError("aws_plan", err), nil
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
		return mcpTextData(text+"Preview only. Call again with confirm=true to open or wait for this Mac.", mcpAWSStatusData(profile, plan, status, false)), nil
	}
	switch action.Kind {
	case "ready":
		return mcpTextData(text+FormatAWSReadyStatus(plan, status), mcpAWSStatusData(profile, plan, status, true)), nil
	case "wait-ready":
		_, readyStatus, err := s.App.AWSService.WaitReady(ctx, profile)
		if err != nil {
			data := mcpAWSStatusData(profile, plan, status, true)
			data["error"] = err.Error()
			return mcpTextData(text+fmt.Sprintf("AWS wait-ready failed: %v\n", err), data), nil
		}
		return mcpTextData(text+FormatAWSReadyStatus(plan, readyStatus), mcpAWSStatusData(profile, plan, readyStatus, true)), nil
	case "launch-on-host":
		_, result, err := s.App.AWSService.LaunchOnHost(ctx, profile, action.HostID)
		if err != nil {
			data := mcpAWSStatusData(profile, plan, status, true)
			data["error"] = err.Error()
			return mcpTextData(text+awsStoppedMessage("AWS launch-on-host", err), data), nil
		}
		_, readyStatus, err := s.App.AWSService.WaitReady(ctx, profile)
		if err != nil {
			data := mcpAWSStatusData(profile, plan, status, true)
			data["error"] = err.Error()
			return mcpTextData(text+FormatAWSCreateResult(plan, result)+fmt.Sprintf("AWS wait-ready failed: %v\n", err), data), nil
		}
		return mcpTextData(text+FormatAWSCreateResult(plan, result)+FormatAWSReadyStatus(plan, readyStatus), mcpAWSStatusData(profile, plan, readyStatus, true)), nil
	case "create":
		_, result, err := s.App.AWSService.Create(ctx, profile)
		if err != nil {
			data := mcpAWSStatusData(profile, plan, status, true)
			data["error"] = err.Error()
			return mcpTextData(text+awsStoppedMessage("AWS create", err), data), nil
		}
		_, readyStatus, err := s.App.AWSService.WaitReady(ctx, profile)
		if err != nil {
			data := mcpAWSStatusData(profile, plan, status, true)
			data["error"] = err.Error()
			return mcpTextData(text+FormatAWSCreateResult(plan, result)+fmt.Sprintf("AWS wait-ready failed: %v\n", err), data), nil
		}
		return mcpTextData(text+FormatAWSCreateResult(plan, result)+FormatAWSReadyStatus(plan, readyStatus), mcpAWSStatusData(profile, plan, readyStatus, true)), nil
	default:
		return mcpTextData(text+fmt.Sprintf("Cannot continue automatically: %s\n", action.Detail), mcpAWSStatusData(profile, plan, status, true)), nil
	}
}

func (s MCPServer) mcpAWSAdoptMac(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, plan, err := s.mcpMacProfilePlan(cfg, args)
	if err != nil {
		return mcpUserError("aws_profile", err), nil
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
		return mcpUserError("aws_profile", err), nil
	}
	if text := missingMCPInputText(profile, true); text != "" {
		return mcpText(text), nil
	}
	hostID, err := requiredString(args, "host_id")
	if err != nil {
		return mcpUserError("host_id", err), nil
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
		return mcpUserError("aws_profile", err), nil
	}
	if text := missingMCPInputText(profile, true); text != "" {
		return mcpText(text), nil
	}
	hostID, err := requiredString(args, "host_id")
	if err != nil {
		return mcpUserError("host_id", err), nil
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
		return mcpUserError("aws_profile", err), nil
	}
	text := FormatMacDestroyPreview(plan)
	if !boolArg(args, "confirm") {
		return mcpTextData(text+"Preview only. Call again with confirm=true to execute AWS destruction.", mcpAWSDestroyData(profile, plan, false, nil)), nil
	}
	_, result, err := s.App.AWSService.Destroy(ctx, profile)
	if err != nil {
		var partial AWSDestroyPartialError
		if errors.As(err, &partial) {
			data := mcpAWSDestroyData(profile, plan, true, &partial.Result)
			data["error"] = err.Error()
			return mcpTextData(text+FormatAWSDestroyResult(plan, partial.Result)+fmt.Sprintf("AWS destroy partially completed: %v\n", err), data), nil
		}
		return nil, err
	}
	return mcpTextData(text+FormatAWSDestroyResult(plan, result), mcpAWSDestroyData(profile, plan, true, &result)), nil
}

func (s MCPServer) mcpAWSDestroyMacByEmail(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := requireMCPAppleProfile(cfg, args)
	if err != nil {
		return mcpUserError("apple_email", err), nil
	}
	args = cloneArgs(args)
	args["profile"] = profile.Name
	return s.mcpAWSDestroyMac(ctx, cfg, args)
}

func mcpAWSStatusData(profile Profile, plan MacPlan, status AWSStatus, confirmed bool) map[string]interface{} {
	action := AWSOpenAction(status)
	data := map[string]interface{}{
		"profile":         profile.Name,
		"apple_email":     profile.AWS.AccountEmail,
		"region":          plan.Region,
		"decision":        action.Kind,
		"decision_detail": action.Detail,
		"next":            AWSOpenDecisionNextStep(profile.Name, action),
		"confirmed":       confirmed,
		"ready":           AWSStatusReady(status),
		"host_count":      len(status.Hosts),
		"instance_count":  len(status.Instances),
		"eip_allocation":  status.ElasticIP.AllocationID,
		"eip_public_ip":   status.ElasticIP.PublicIP,
	}
	if action.HostID != "" {
		data["host_id"] = action.HostID
	}
	if len(status.Instances) > 0 {
		instance := status.Instances[0]
		data["instance_id"] = instance.InstanceID
		data["instance_state"] = instance.State
		data["instance_ready"] = InstanceReady(instance, status.ElasticIP)
	}
	return data
}

func mcpAWSDestroyData(profile Profile, plan MacPlan, confirmed bool, result *AWSDestroyResult) map[string]interface{} {
	data := map[string]interface{}{
		"profile":        profile.Name,
		"apple_email":    profile.AWS.AccountEmail,
		"region":         plan.Region,
		"confirmed":      confirmed,
		"eip_retained":   true,
		"eip_allocation": plan.ElasticIPAllocationID,
		"next":           "run cm aws destroy " + profile.Name + " again later if the Dedicated Host is still pending release",
	}
	if !confirmed {
		data["next"] = "call again with confirm=true after explicit user approval"
	}
	if result != nil {
		data["terminated_instances"] = append([]string(nil), result.TerminatedInstances...)
		data["released_hosts"] = append([]string(nil), result.ReleasedHosts...)
		data["disassociated_elastic_ip"] = result.DisassociatedElasticIP
		data["retained_elastic_ip"] = result.RetainedElasticIP.AllocationID
		data["deferred_hosts"] = append([]AWSDeferredHost(nil), result.DeferredHosts...)
	}
	return data
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

func mcpTextData(text string, data map[string]interface{}) map[string]interface{} {
	result := mcpText(text)
	if data != nil {
		result["structuredContent"] = data
	}
	return result
}

func mcpUserError(kind string, err error) map[string]interface{} {
	return mcpTextData(err.Error(), map[string]interface{}{
		"ok":    false,
		"kind":  kind,
		"error": err.Error(),
	})
}

func mcpCheckData(profile Profile, ok bool, errs []string) map[string]interface{} {
	return map[string]interface{}{
		"ok":          ok,
		"profile":     profile.Name,
		"apple_email": profile.AWS.AccountEmail,
		"errors":      append([]string(nil), errs...),
	}
}

func mcpSyncData(profile Profile, direction string, confirmed bool, source, target string, filters SyncFilters, errorText string) map[string]interface{} {
	data := map[string]interface{}{
		"ok":          errorText == "",
		"profile":     profile.Name,
		"apple_email": profile.AWS.AccountEmail,
		"direction":   direction,
		"confirmed":   confirmed,
		"includes":    append([]string(nil), filters.Includes...),
		"excludes":    append([]string(nil), filters.Excludes...),
	}
	if direction == "push" {
		data["local_path"] = source
		data["remote_dir"] = target
	} else {
		data["remote_path"] = source
		data["local_dir"] = target
	}
	if errorText != "" {
		data["error"] = errorText
	}
	return data
}

func errorStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}

func FormatMCPGuideText() string {
	return strings.Join([]string{
		"ConnectMac MCP guide",
		"",
		"Core parameters:",
		"- apple_email: Apple account email. Required for AI-driven open/create/destroy requests. Never infer it from old context.",
		"- profile: Local cm profile name. Use for check, status, dashboard, push, pull, and profile management.",
		"- confirm: Missing or false means preview only. Set confirm=true only after the user explicitly approves that exact operation.",
		"- force_local: Only for cm_profile_remove. It removes local profile config without AWS safety checks.",
		"- includes/excludes: Optional rsync filters for cm_push and cm_pull.",
		"",
		"Common flows:",
		"- List accounts: cm_list_profiles or cm_find_profile_by_apple without an email to ask the user to choose.",
		"- Open/create/start Mac: cm_aws_open_mac_by_email with apple_email, preview first, then confirm=true after approval.",
		"- Release/close Mac: cm_aws_destroy_mac_by_email with apple_email, preview first, then confirm=true after approval. Elastic IP allocations are retained.",
		"- Check current state: cm_dashboard with aws=true for a table, or cm_aws_status for one profile.",
		"- Upload: cm_push with profile, local_path, remote_dir, optional includes/excludes, confirm=true only after preview.",
		"- Download: cm_pull with profile, remote_path, optional local_dir/includes/excludes, confirm=true only after preview.",
		"- Remove stale SSH fingerprint: cm_forget_host with profile, preview first, then confirm=true.",
		"",
		"Decision handling:",
		"- ready: Mac is already usable; next usually cm start <profile>.",
		"- wait-ready: use cm_aws_wait_ready or wait before SSH/VNC.",
		"- launch-on-host or create: only execute with confirm=true after user approval.",
		"- blocked/error/config: stop, report the reason, and ask for user instruction.",
		"",
	}, "\n")
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
		mcpTool("cm_mcp_guide", "Read this first when using cm MCP. Explains stable tool flows, main parameters, preview/confirm rules, and safe AWS Mac handling.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}),
		mcpTool("cm_list_profiles", "List configured cm profiles. Use when the user has not provided an Apple account email or profile and must choose.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}),
		mcpTool("cm_find_profile_by_apple", "Resolve apple_email to the local profile. apple_email is required for AI-driven open/create/destroy requests; never infer it from old context. Returns structuredContent.", appleEmailSchema()),
		mcpTool("cm_check_profile", "Validate profile without connecting. Main parameter: profile. Returns structuredContent with ok and errors.", profileSchema()),
		mcpTool("cm_profile_show", "Show a profile file by profile name or Apple account email. Main parameter: profile.", profileSchema()),
		mcpTool("cm_profile_add", "Preview or create a local profile file. Main parameter: name. confirm=false previews; confirm=true writes after user approval.", profileAddSchema()),
		mcpTool("cm_profile_remove", "Preview or remove a local profile file only; this does not close AWS Mac resources. Blocks when AWS resources are active unless force_local=true. confirm=true removes after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":     stringSchema(),
				"force_local": map[string]string{"type": "boolean"},
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_profile_rename", "Preview or rename a local profile file. Main parameters: profile, new_name. confirm=true writes after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":  stringSchema(),
				"new_name": stringSchema(),
				"confirm":  map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "new_name"},
		}),
		mcpTool("cm_profile_export", "Export a profile file by profile name or Apple account email. Main parameter: profile.", profileSchema()),
		mcpTool("cm_doctor", "Run local ConnectMac diagnostics. Optional fix=true creates missing local support dirs.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"fix": map[string]string{"type": "boolean"},
			},
			"additionalProperties": false,
		}),
		mcpTool("cm_dashboard", "Show local profile/tunnel dashboard. Set aws=true for read-only AWS status, decision, and next-step columns. Returns structuredContent profiles array.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"aws": map[string]string{"type": "boolean"},
			},
			"additionalProperties": false,
		}),
		mcpTool("cm_push", "Preview or execute rsync upload. Main parameters: profile, local_path, remote_dir. Optional includes/excludes. confirm=true executes after preview approval. Returns structuredContent.", map[string]interface{}{
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
		mcpTool("cm_pull", "Preview or execute rsync download. Main parameters: profile, remote_path, optional local_dir/includes/excludes. confirm=true executes after preview approval. Returns structuredContent.", map[string]interface{}{
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
		mcpTool("cm_forget_host", "Preview or remove known_hosts entries for a profile host after rebuild/IP reuse. Main parameter: profile. confirm=true executes after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_plan", "Read-only local plan for AWS Mac Dedicated Host, EC2, and EIP operations. Main parameter: profile.", profileSchema()),
		mcpTool("cm_aws_capacity", "Read-only AWS Mac capacity report: Dedicated Host quotas, active host usage, remaining capacity, and offering AZs. Main parameter: profile.", profileSchema()),
		mcpTool("cm_aws_status", "Read-only status for one profile. Returns text plus structuredContent with profile, apple_email, decision, next, ready, EIP, hosts, and instances.", profileSchema()),
		mcpTool("cm_aws_wait_ready", "Wait until the managed AWS Mac EC2 instance is running, EIP-bound, and AWS status checks are ok. Use only after a confirmed create/open/launch.", profileSchema()),
		mcpTool("cm_aws_create_mac", "Preview or execute AWS Mac creation by profile. Prefer cm_aws_open_mac_by_email for user requests. confirm=true mutates AWS after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_open_mac_by_email", "Use for 'open/create/start Mac' requests. Requires explicit apple_email. confirm=false previews decision; confirm=true may create/launch/wait after user approval. Returns structuredContent.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"apple_email": stringSchema(),
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"apple_email"},
		}),
		mcpTool("cm_aws_adopt_mac", "Preview or tag existing AWS Mac resources as cm-managed. Main parameter: profile. confirm=true mutates tags after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_adopt_host", "Preview or tag an existing empty AWS Mac Dedicated Host as cm-managed. Main parameters: profile, host_id. confirm=true mutates tags after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"host_id": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "host_id"},
		}),
		mcpTool("cm_aws_launch_on_host", "Preview or launch AWS Mac EC2 on an explicit existing Dedicated Host. Main parameters: profile, host_id. confirm=true mutates AWS after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"host_id": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "host_id"},
		}),
		mcpTool("cm_aws_destroy_mac", "Preview or execute AWS Mac destruction by profile. Never releases Elastic IP allocations. confirm=true mutates AWS after approval. Returns structuredContent.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_destroy_mac_by_email", "Use for 'release/close Mac' requests. Requires explicit apple_email. Never releases Elastic IP allocations. confirm=false previews; confirm=true mutates AWS after approval. Returns structuredContent.", map[string]interface{}{
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
	rows = append(rows, []string{"TOOL", "DESCRIPTION", "REQUIRED", "KEY PARAMS"})
	for _, tool := range tools {
		rows = append(rows, []string{
			fmt.Sprint(tool["name"]),
			fmt.Sprint(tool["description"]),
			strings.Join(mcpToolRequiredParams(tool), ", "),
			strings.Join(mcpToolParamNames(tool), ", "),
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

func mcpToolParamNames(tool map[string]interface{}) []string {
	schema, _ := tool["inputSchema"].(map[string]interface{})
	properties, _ := schema["properties"].(map[string]interface{})
	params := make([]string, 0, len(properties))
	for key := range properties {
		params = append(params, key)
	}
	sort.Strings(params)
	return params
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

func profileAddSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":                     stringSchema(),
			"description":              stringSchema(),
			"user":                     stringSchema(),
			"host":                     stringSchema(),
			"identity_file":            stringSchema(),
			"apple_email":              stringSchema(),
			"aws_profile":              stringSchema(),
			"region":                   stringSchema(),
			"creator":                  stringSchema(),
			"key_name":                 stringSchema(),
			"security_group_id":        stringSchema(),
			"elastic_ip_allocation_id": stringSchema(),
			"elastic_ip_public_ip":     stringSchema(),
			"availability_zone_ids":    stringArraySchema(),
			"instance_type_priority":   stringArraySchema(),
			"confirm":                  map[string]string{"type": "boolean"},
		},
		"required": []string{"name"},
	}
}

func stringSchema() map[string]string {
	return map[string]string{"type": "string"}
}

func stringArraySchema() map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": stringSchema()}
}
