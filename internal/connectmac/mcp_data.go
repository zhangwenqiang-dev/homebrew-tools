package connectmac

import (
	"bytes"
	"fmt"
	"strings"
)

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
func mcpMissingInputData(profile Profile, missing []string) map[string]interface{} {
	return map[string]interface{}{
		"ok":                  false,
		"profile":             profile.Name,
		"apple_email":         profile.AWS.AccountEmail,
		"missing":             append([]string(nil), missing...),
		"requires_user_input": true,
		"next":                "ask the user to provide the missing values explicitly; do not infer them from context",
	}
}
func applyMCPExplicitCreator(profile Profile, args map[string]interface{}) Profile {
	if creator := strings.TrimSpace(stringArg(args, "creator", "")); creator != "" {
		profile.AWS.Creator = creator
	}
	return profile
}
func errorStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}
func missingMCPInputText(profile Profile, requireCreator bool) string {
	missing := missingMCPInputFields(profile, requireCreator)
	if len(missing) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Profile %s is missing required input: %s\n", profile.Name, strings.Join(missing, ", "))
	fmt.Fprintln(&b, "Ask the user to provide these values explicitly. Do not infer them from old context.")
	if profile.IdentityFile == "" {
		fmt.Fprintln(&b, "- identity_file: PEM name or path, for example example-key or ~/.ssh/example-key.pem")
	}
	if requireCreator && profile.AWS.Creator == "" {
		fmt.Fprintln(&b, "- aws.creator: creator display name for AWS cm-creator tag; use the creator parameter or write it to the profile after the user provides it")
	}
	return b.String()
}
func missingMCPInputFields(profile Profile, requireCreator bool) []string {
	var missing []string
	if profile.IdentityFile == "" {
		missing = append(missing, "identity_file")
	}
	if requireCreator && profile.AWS.Creator == "" {
		missing = append(missing, "aws.creator")
	}
	return missing
}
func formatErrors(errs []error) string {
	var b bytes.Buffer
	printErrors(&b, errs)
	return b.String()
}
