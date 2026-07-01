package connectmac

import (
	"context"
	"fmt"
	"strings"
)

func (s MCPServer) mcpGuide(args map[string]interface{}) (interface{}, error) {
	topic := stringArg(args, "topic", "overview")
	text, ok := guideText(topic)
	if !ok {
		return mcpUserError("guide", fmt.Errorf("unknown guide topic %q", topic)), nil
	}
	return mcpTextData(text, map[string]interface{}{
		"ok":    true,
		"topic": topic,
	}), nil
}
func (s MCPServer) mcpNext(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	ref, err := requiredString(args, "profile")
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	profile, err := resolveProfileRef(cfg, ref)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Next step for profile %s\n", profile.Name)
	fmt.Fprintf(&b, "Apple account: %s\n", emptyTableValue(profile.AWS.AccountEmail))
	fmt.Fprintf(&b, "Region: %s\n", emptyTableValue(profile.AWS.Region))

	data := map[string]interface{}{
		"ok":          true,
		"profile":     profile.Name,
		"apple_email": profile.AWS.AccountEmail,
		"region":      profile.AWS.Region,
	}
	accessErrs := s.App.Validator.ValidateAccess(profile)
	awsErrs := s.App.Validator.ValidateAWSProfile(profile)
	if len(accessErrs) > 0 || len(awsErrs) > 0 {
		fmt.Fprintln(&b, "Decision: fix-config")
		if len(accessErrs) > 0 {
			fmt.Fprintln(&b, "Local access issues:")
			writeErrorBullets(&b, accessErrs)
		}
		if len(awsErrs) > 0 {
			fmt.Fprintln(&b, "AWS config issues:")
			writeErrorBullets(&b, awsErrs)
		}
		next := "cm profile edit " + profile.Name
		fmt.Fprintf(&b, "Next: %s\n", next)
		fmt.Fprintf(&b, "Then: cm check %s\n", profile.Name)
		data["decision"] = "fix-config"
		data["next"] = next
		data["local_errors"] = errorStrings(accessErrs)
		data["aws_errors"] = errorStrings(awsErrs)
		return mcpTextData(b.String(), data), nil
	}

	_, status, err := s.App.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
	if err != nil {
		next := "cm aws status " + profile.Name
		fmt.Fprintln(&b, "Decision: inspect-aws")
		fmt.Fprintf(&b, "AWS status error: %v\n", err)
		fmt.Fprintf(&b, "Next: %s\n", next)
		data["ok"] = false
		data["decision"] = "inspect-aws"
		data["next"] = next
		data["error"] = err.Error()
		return mcpTextData(b.String(), data), nil
	}
	action := AWSOpenAction(status)
	next := AWSOpenDecisionNextStep(profile.Name, action)
	fmt.Fprintf(&b, "Decision: %s\n", action.Kind)
	if action.Detail != "" {
		fmt.Fprintf(&b, "Detail: %s\n", action.Detail)
	}
	fmt.Fprintf(&b, "Ready: %t\n", AWSStatusReady(status))
	fmt.Fprintf(&b, "Next: %s\n", next)
	switch action.Kind {
	case "ready":
		fmt.Fprintf(&b, "After tunnel: cm open-vnc %s\n", profile.Name)
	case "wait-ready":
		fmt.Fprintf(&b, "After ready: cm start %s\n", profile.Name)
	case "launch-on-host", "create":
		fmt.Fprintln(&b, "Preview first. Add --confirm only after reviewing the AWS mutation.")
	case "blocked":
		fmt.Fprintln(&b, "Stop here and fix the blocking reason before continuing.")
	}
	data["decision"] = action.Kind
	data["detail"] = action.Detail
	data["ready"] = AWSStatusReady(status)
	data["next"] = next
	data["hosts"] = status.Hosts
	data["instances"] = status.Instances
	data["elastic_ip"] = status.ElasticIP
	return mcpTextData(b.String(), data), nil
}
func FormatMCPGuideText() string {
	return strings.Join([]string{
		"ConnectMac MCP guide",
		"",
		"Core parameters:",
		"- apple_email: Apple account email. Required for AI-driven open/create/destroy requests. Never infer it from old context.",
		"- profile: Local cm profile name. Use for check, status, dashboard, push, pull, and profile management.",
		"- confirm: Missing or false means preview only. Set confirm=true only after the user explicitly approves that exact operation.",
		"- creator: Creator display name for AWS create/open/adopt/launch. If missing, ask the user; never infer it from context or defaults.",
		"- force_local: Only for cm_profile_remove. It removes local profile config without AWS safety checks.",
		"- includes/excludes: Optional rsync filters for cm_push and cm_pull.",
		"",
		"Common flows:",
		"- Step-by-step help: cm_guide with optional topic, or cm_next to decide the next safe action.",
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
