package connectmac

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
				"version": "0.1.58",
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
	case "cm_guide":
		return s.mcpGuide(call.Arguments)
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
	case "cm_next":
		return s.mcpNext(ctx, cfg, call.Arguments)
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
