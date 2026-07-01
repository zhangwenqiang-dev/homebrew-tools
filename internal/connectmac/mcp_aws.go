package connectmac

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

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
	profile = applyMCPExplicitCreator(profile, args)
	plan, err = s.App.AWSService.Plan(profile)
	if err != nil {
		return mcpUserError("aws_plan", err), nil
	}
	if missing := missingMCPInputFields(profile, true); len(missing) > 0 {
		return mcpTextData(missingMCPInputText(profile, true), mcpMissingInputData(profile, missing)), nil
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
	profile = applyMCPExplicitCreator(profile, args)
	plan, err := s.App.AWSService.Plan(profile)
	if err != nil {
		return mcpUserError("aws_plan", err), nil
	}
	if missing := missingMCPInputFields(profile, true); len(missing) > 0 {
		return mcpTextData(missingMCPInputText(profile, true), mcpMissingInputData(profile, missing)), nil
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
	profile = applyMCPExplicitCreator(profile, args)
	plan, err = s.App.AWSService.Plan(profile)
	if err != nil {
		return mcpUserError("aws_plan", err), nil
	}
	if missing := missingMCPInputFields(profile, true); len(missing) > 0 {
		return mcpTextData(missingMCPInputText(profile, true), mcpMissingInputData(profile, missing)), nil
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
	profile = applyMCPExplicitCreator(profile, args)
	plan, err = s.App.AWSService.Plan(profile)
	if err != nil {
		return mcpUserError("aws_plan", err), nil
	}
	if missing := missingMCPInputFields(profile, true); len(missing) > 0 {
		return mcpTextData(missingMCPInputText(profile, true), mcpMissingInputData(profile, missing)), nil
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
	profile = applyMCPExplicitCreator(profile, args)
	plan, err = s.App.AWSService.Plan(profile)
	if err != nil {
		return mcpUserError("aws_plan", err), nil
	}
	if missing := missingMCPInputFields(profile, true); len(missing) > 0 {
		return mcpTextData(missingMCPInputText(profile, true), mcpMissingInputData(profile, missing)), nil
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
