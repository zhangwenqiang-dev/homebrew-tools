package connectmac

import (
	"fmt"
	"sort"
	"strings"
)

func FormatMacPlan(plan MacPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac plan for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Resource name: %s\n", plan.ResourceName)
	fmt.Fprintf(&b, "AWS profile: %s\n", plan.AWSProfile)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Availability zones: %s\n", strings.Join(plan.AvailabilityZoneIDs, ", "))
	fmt.Fprintf(&b, "Selected instance type: %s\n", plan.SelectedInstanceType)
	fmt.Fprintf(&b, "Selected AMI: %s\n", plan.SelectedAMI)
	fmt.Fprintf(&b, "Key pair: %s\n", plan.KeyName)
	if len(plan.SubnetsByAZ) > 0 {
		fmt.Fprintf(&b, "Subnets by AZ: %s\n", formatStringMap(plan.SubnetsByAZ))
	} else {
		fmt.Fprintf(&b, "Subnet: %s\n", plan.SubnetID)
	}
	fmt.Fprintf(&b, "Security group: %s\n", plan.SecurityGroupID)
	fmt.Fprintf(&b, "Elastic IP allocation: %s\n", plan.ElasticIPAllocationID)
	fmt.Fprintf(&b, "Elastic IP owner tag: %s=%s\n", plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value)
	fmt.Fprintf(&b, "Required tags: %s\n", FormatAWSTags(plan.Tags))
	fmt.Fprintln(&b, "Operations:")
	fmt.Fprintln(&b, "- Allocate Dedicated Host with AutoPlacement=off and HostMaintenance=off")
	fmt.Fprintln(&b, "- Launch EC2 instance on the allocated Dedicated Host with Tenancy=host and Affinity=host")
	fmt.Fprintln(&b, "- Disable auto-assigned public IP")
	fmt.Fprintln(&b, "- Verify Elastic IP owner tag before association")
	fmt.Fprintln(&b, "- Associate Elastic IP to the new instance")
	return b.String()
}
func FormatAWSOpenPreview(plan MacPlan, status AWSStatus) string {
	action := AWSOpenAction(status)
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac open preview for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Apple account: %s\n", plan.AccountEmail)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Dedicated hosts: %d\n", len(status.Hosts))
	fmt.Fprintf(&b, "Instances: %d\n", len(status.Instances))
	fmt.Fprintf(&b, "Elastic IP: allocation=%s association=%s instance=%s public_ip=%s\n", status.ElasticIP.AllocationID, status.ElasticIP.AssociationID, status.ElasticIP.InstanceID, status.ElasticIP.PublicIP)
	fmt.Fprintf(&b, "Decision: %s", action.Kind)
	if action.HostID != "" {
		fmt.Fprintf(&b, " host=%s", action.HostID)
	}
	if action.Detail != "" {
		fmt.Fprintf(&b, " (%s)", action.Detail)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Next: %s\n", AWSOpenDecisionNextStep(plan.ProfileName, action))
	fmt.Fprintln(&b, "Guidance:")
	switch action.Kind {
	case "ready":
		fmt.Fprintln(&b, "- The managed Mac is already usable; no AWS resource creation is needed.")
		fmt.Fprintf(&b, "- Start the local tunnel with: cm start %s\n", plan.ProfileName)
	case "wait-ready":
		fmt.Fprintln(&b, "- A managed instance exists but AWS readiness checks are not complete.")
		fmt.Fprintf(&b, "- Wait with: cm aws wait-ready %s\n", plan.ProfileName)
	case "launch-on-host":
		fmt.Fprintln(&b, "- An available managed Dedicated Host exists; confirmation launches EC2 on that host.")
		fmt.Fprintln(&b, "- This can create billable EC2 usage on the existing Dedicated Host.")
	case "create":
		fmt.Fprintln(&b, "- Confirmation allocates a billable Mac Dedicated Host and launches EC2.")
		fmt.Fprintln(&b, "- If EC2 launch fails after host allocation, stop and fix that host; do not allocate another host.")
	case "blocked":
		fmt.Fprintln(&b, "- Stop here and fix the blocking reason before continuing.")
	default:
		fmt.Fprintln(&b, "- Inspect AWS status before continuing.")
	}
	return b.String()
}
func AWSOpenDecisionNextStep(profileName string, action AWSOpenDecision) string {
	switch action.Kind {
	case "ready":
		return "cm start " + profileName
	case "wait-ready":
		return "cm aws wait-ready " + profileName
	case "launch-on-host", "create":
		return "cm aws open " + profileName + " --confirm"
	case "blocked":
		if action.Detail != "" {
			return "stop: " + action.Detail
		}
		return "stop and inspect status"
	default:
		return "cm aws status " + profileName
	}
}
func FormatAWSOpenPreviewWithCandidates(plan MacPlan, status AWSStatus, candidates []AWSCreateAttempt) string {
	text := FormatAWSOpenPreview(plan, status)
	if AWSOpenAction(status).Kind != "create" || len(candidates) == 0 {
		return text
	}
	return text + FormatAWSCreateCandidates(candidates)
}
func FormatMacDestroyPreview(plan MacPlan) string {
	return FormatMacDestroyPreviewWithStatus(plan, AWSStatus{})
}
func FormatMacDestroyPreviewWithStatus(plan MacPlan, status AWSStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac destroy preview for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Managed resource name: %s\n", plan.ResourceName)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Safety tags required before any mutation: %s\n", FormatAWSTags(managedRequiredTags(plan)))
	if len(status.Hosts) > 0 || len(status.Instances) > 0 || status.ElasticIP.AllocationID != "" {
		fmt.Fprintln(&b, "Matched resources:")
		for _, host := range status.Hosts {
			fmt.Fprintf(&b, "- host=%s state=%s type=%s zone=%s\n", emptyTableValue(host.HostID), emptyTableValue(host.State), emptyTableValue(host.InstanceType), emptyTableValue(host.ZoneID))
		}
		for _, instance := range status.Instances {
			fmt.Fprintf(&b, "- instance=%s state=%s type=%s host=%s public_ip=%s ready=%t\n",
				emptyTableValue(instance.InstanceID),
				emptyTableValue(instance.State),
				emptyTableValue(instance.InstanceType),
				emptyTableValue(instance.HostID),
				emptyTableValue(instance.PublicIP),
				InstanceReady(instance, status.ElasticIP),
			)
		}
		if status.ElasticIP.AllocationID != "" {
			fmt.Fprintf(&b, "- elastic_ip=%s association=%s instance=%s public_ip=%s retained=true\n",
				emptyTableValue(status.ElasticIP.AllocationID),
				emptyTableValue(status.ElasticIP.AssociationID),
				emptyTableValue(status.ElasticIP.InstanceID),
				emptyTableValue(status.ElasticIP.PublicIP),
			)
		}
	}
	fmt.Fprintln(&b, "Operations:")
	fmt.Fprintln(&b, "- Disassociate Elastic IP only if attached to the managed instance; retain the Elastic IP allocation")
	fmt.Fprintln(&b, "- Terminate the managed EC2 instance")
	fmt.Fprintln(&b, "- Release the managed Dedicated Host when AWS allows release")
	fmt.Fprintln(&b, "Guidance:")
	fmt.Fprintln(&b, "- Preview only unless --confirm is provided.")
	fmt.Fprintln(&b, "- Elastic IP allocation is retained and must not be released by this workflow.")
	fmt.Fprintln(&b, "- If host release is deferred, run the same destroy command again later.")
	return b.String()
}
func FormatAWSAdoptionPreview(plan MacPlan, status AWSStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac adoption preview for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Resource name: %s\n", plan.ResourceName)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Hosts matched by Name: %d\n", len(status.Hosts))
	for _, host := range status.Hosts {
		fmt.Fprintf(&b, "- host=%s state=%s type=%s zone=%s\n", host.HostID, host.State, host.InstanceType, host.ZoneID)
	}
	fmt.Fprintf(&b, "Instance candidates: %d\n", len(status.Instances))
	for _, instance := range status.Instances {
		fmt.Fprintf(&b, "- instance=%s state=%s type=%s host=%s public_ip=%s\n", instance.InstanceID, instance.State, instance.InstanceType, instance.HostID, instance.PublicIP)
	}
	fmt.Fprintf(&b, "Elastic IP owner tag: %s=%s\n", plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value)
	fmt.Fprintf(&b, "Tags to add: %s\n", FormatAWSTags(adoptionTags(plan)))
	return b.String()
}
func FormatAWSAdoptResult(plan MacPlan, result AWSAdoptResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac resources adopted for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Tagged resources: %s\n", strings.Join(result.TaggedResources, ", "))
	fmt.Fprintf(&b, "Added tags: %s\n", FormatAWSTags(result.Tags))
	return b.String()
}
func FormatAWSAdoptHostPreview(plan MacPlan, host DedicatedHostStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac adopt-host preview for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Host: %s\n", host.HostID)
	fmt.Fprintf(&b, "State: %s\n", host.State)
	fmt.Fprintf(&b, "Instance type: %s\n", host.InstanceType)
	fmt.Fprintf(&b, "Availability zone: %s\n", host.ZoneID)
	fmt.Fprintf(&b, "Tags to add: %s\n", FormatAWSTags(adoptionTags(plan)))
	return b.String()
}
func FormatAWSLaunchOnHostPreview(plan MacPlan, preview AWSLaunchOnHostPreview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac launch-on-host preview for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Host: %s\n", preview.HostID)
	fmt.Fprintf(&b, "Selected: %s %s %s\n", preview.AvailabilityZoneID, preview.InstanceType, preview.AMI)
	fmt.Fprintf(&b, "Subnet: %s\n", preview.SubnetID)
	fmt.Fprintf(&b, "Elastic IP allocation: %s\n", plan.ElasticIPAllocationID)
	return b.String()
}
func FormatMacStatusPreview(plan MacPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac status target for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Resource name: %s\n", plan.ResourceName)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Lookup tags: %s\n", FormatAWSTags(plan.Tags))
	fmt.Fprintln(&b, "Status lookup will describe matching Dedicated Hosts, EC2 instances, and Elastic IP association.")
	return b.String()
}
func FormatAWSStatus(plan MacPlan, status AWSStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac status for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Caller: %s\n", status.CallerIdentity.ARN)
	fmt.Fprintf(&b, "Account: %s\n", MaskAWSAccount(status.CallerIdentity.Account))
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Dedicated hosts: %d\n", len(status.Hosts))
	for _, host := range status.Hosts {
		fmt.Fprintf(&b, "- host=%s state=%s type=%s zone=%s\n", host.HostID, host.State, host.InstanceType, host.ZoneID)
	}
	fmt.Fprintf(&b, "Instances: %d\n", len(status.Instances))
	for _, instance := range status.Instances {
		fmt.Fprintf(&b, "- instance=%s state=%s type=%s host=%s public_ip=%s system_status=%s instance_status=%s ebs_status=%s ready=%t\n",
			instance.InstanceID,
			instance.State,
			instance.InstanceType,
			instance.HostID,
			instance.PublicIP,
			emptyStatus(instance.SystemStatus),
			emptyStatus(instance.InstanceStatusCheck),
			emptyStatus(instance.EBSStatus),
			InstanceReady(instance, status.ElasticIP),
		)
	}
	fmt.Fprintf(&b, "Elastic IP: allocation=%s association=%s instance=%s public_ip=%s\n", status.ElasticIP.AllocationID, status.ElasticIP.AssociationID, status.ElasticIP.InstanceID, status.ElasticIP.PublicIP)
	fmt.Fprintf(&b, "Ready: %t\n", AWSStatusReady(status))
	return b.String()
}
func FormatAWSCapacity(plan MacPlan, capacity AWSCapacity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac capacity for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Caller: %s\n", capacity.CallerIdentity.ARN)
	fmt.Fprintf(&b, "Account: %s\n", MaskAWSAccount(capacity.CallerIdentity.Account))
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	rows := make([][]string, 0, len(capacity.Items)+1)
	rows = append(rows, []string{"INSTANCE TYPE", "QUOTA", "IN USE", "AVAILABLE", "OFFERING AZS", "SERVICE QUOTA"})
	for _, item := range capacity.Items {
		rows = append(rows, []string{
			item.InstanceType,
			formatQuotaValue(item.Quota),
			fmt.Sprintf("%d", item.InUse),
			formatQuotaValue(item.Available),
			emptyTableValue(strings.Join(item.OfferingAZs, ",")),
			item.QuotaName,
		})
	}
	fmt.Fprint(&b, formatRows(rows))
	return b.String()
}
func FormatAWSReadyStatus(plan MacPlan, status AWSStatus) string {
	return fmt.Sprintf("AWS Mac ready for profile %s: %t\n%s\n%s", plan.ProfileName, AWSStatusReady(status), AWSReadinessSummary(status), FormatAWSManualSetupGuide(plan))
}
func FormatAWSManualSetupGuide(plan MacPlan) string {
	return fmt.Sprintf("Manual GUI setup:\n  cm ssh %s\n  # Enter the remote Mac SSH shell for first-time GUI/VNC setup.\n  sudo passwd ec2-user\n  # 输入你要设置的密码，例如：12345678\n  # 再次输入你要设置的密码，例如：12345678\n  sudo launchctl enable system/com.apple.screensharing\n  # Enable the macOS Screen Sharing service.\n  sudo launchctl load -w /System/Library/LaunchDaemons/com.apple.screensharing.plist\n  # Start the Screen Sharing service now.\n  exit\n  # Exit SSH and return to the local terminal.\n  cm start %s\n  # Start the local SSH tunnel to the remote Mac VNC port.\n  cm open-vnc %s\n  # Open macOS Screen Sharing through the local tunnel.\n", plan.ProfileName, plan.ProfileName, plan.ProfileName)
}
func FormatAWSCreateResult(plan MacPlan, result AWSCreateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac created for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Host: %s\n", result.HostID)
	fmt.Fprintf(&b, "Instance: %s\n", result.InstanceID)
	fmt.Fprintf(&b, "EIP association: %s\n", result.AssociationID)
	fmt.Fprintf(&b, "Selected: %s %s %s\n", result.AvailabilityZoneID, result.InstanceType, result.AMI)
	fmt.Fprintf(&b, "Subnet: %s\n", result.SubnetID)
	fmt.Fprintf(&b, "Elastic IP allocation: %s\n", result.ElasticIPAllocation)
	if len(result.Attempts) > 0 {
		fmt.Fprint(&b, FormatAWSCreateAttempts(result.Attempts))
	}
	return b.String()
}
func FormatAWSCreateAttempts(attempts []AWSCreateAttempt) string {
	return formatAWSCreateAttemptTable("Create attempts:", attempts)
}
func FormatAWSCreateCandidates(candidates []AWSCreateAttempt) string {
	return formatAWSCreateAttemptTable("Create candidates:", candidates)
}
func formatAWSCreateAttemptTable(title string, attempts []AWSCreateAttempt) string {
	if len(attempts) == 0 {
		return ""
	}
	rows := make([][]string, 0, len(attempts)+1)
	rows = append(rows, []string{"AZ", "INSTANCE TYPE", "SUBNET", "RESULT", "DETAIL"})
	for _, attempt := range attempts {
		rows = append(rows, []string{
			emptyTableValue(attempt.AvailabilityZoneID),
			emptyTableValue(attempt.InstanceType),
			emptyTableValue(attempt.SubnetID),
			emptyTableValue(attempt.Status),
			emptyTableValue(attempt.Detail),
		})
	}
	var b strings.Builder
	fmt.Fprintln(&b, title)
	fmt.Fprint(&b, formatRows(rows))
	return b.String()
}
func formatRows(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, value := range row {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}
	var b strings.Builder
	for rowIndex, row := range rows {
		for i, value := range row {
			if i > 0 {
				fmt.Fprint(&b, "  ")
			}
			fmt.Fprintf(&b, "%-*s", widths[i], value)
		}
		fmt.Fprintln(&b)
		if rowIndex == 0 {
			for i, width := range widths {
				if i > 0 {
					fmt.Fprint(&b, "  ")
				}
				fmt.Fprint(&b, strings.Repeat("-", width))
			}
			fmt.Fprintln(&b)
		}
	}
	return b.String()
}
func formatQuotaValue(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%.0f", value)
	}
	return fmt.Sprintf("%.2f", value)
}
func dedicatedHostQuotaName(instanceType string) string {
	family := strings.TrimSuffix(instanceType, ".metal")
	return fmt.Sprintf("Running Dedicated %s Hosts", family)
}
func emptyTableValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
func FormatAWSDestroyResult(plan MacPlan, result AWSDestroyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac destroy executed for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Compute release: %s\n", awsDestroyReleaseStatus(result))
	fmt.Fprintf(&b, "Elastic IP retained: true\n")
	fmt.Fprintf(&b, "Need rerun: %t\n", len(result.DeferredHosts) > 0)
	if len(result.DeferredHosts) > 0 {
		fmt.Fprintf(&b, "Suggested wait: 60 minutes\n")
	}
	fmt.Fprintf(&b, "Disassociated Elastic IP: %t\n", result.DisassociatedElasticIP)
	fmt.Fprintf(&b, "Retained Elastic IP: %s public_ip=%s\n", emptyTableValue(result.RetainedElasticIP.AllocationID), emptyTableValue(result.RetainedElasticIP.PublicIP))
	fmt.Fprintf(&b, "Terminated instances: %s\n", formatStringList(result.TerminatedInstances))
	fmt.Fprintf(&b, "Skipped instances: %s\n", formatStringList(result.SkippedInstances))
	fmt.Fprintf(&b, "Released hosts: %s\n", formatStringList(result.ReleasedHosts))
	fmt.Fprintln(&b, "Deferred hosts:")
	if len(result.DeferredHosts) == 0 {
		fmt.Fprintln(&b, "-")
	} else {
		for _, host := range result.DeferredHosts {
			fmt.Fprintf(&b, "- %s state=%s reason=%s\n", host.HostID, emptyTableValue(host.State), host.Reason)
		}
	}
	fmt.Fprintf(&b, "Skipped hosts: %s\n", formatStringList(result.SkippedHosts))
	fmt.Fprintf(&b, "Next action: %s\n", AWSDestroyNextAction(plan, result))
	return b.String()
}
func FormatAWSDestroyFinalStatus(plan MacPlan, status AWSStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Final status for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Dedicated hosts: %d\n", len(status.Hosts))
	fmt.Fprintf(&b, "Instances: %d\n", len(status.Instances))
	fmt.Fprintf(&b, "Elastic IP retained: true allocation=%s association=%s instance=%s public_ip=%s\n",
		emptyTableValue(status.ElasticIP.AllocationID),
		emptyTableValue(status.ElasticIP.AssociationID),
		emptyTableValue(status.ElasticIP.InstanceID),
		emptyTableValue(status.ElasticIP.PublicIP),
	)
	fmt.Fprintf(&b, "Ready: %t\n", AWSStatusReady(status))
	return b.String()
}
func AWSDestroyNextAction(plan MacPlan, result AWSDestroyResult) string {
	if len(result.DeferredHosts) > 0 {
		return fmt.Sprintf("wait 60 minutes, then run: cm aws destroy %s --confirm; Elastic IP is retained", plan.AccountEmail)
	}
	if len(result.ReleasedHosts) > 0 || len(result.TerminatedInstances) > 0 || result.DisassociatedElasticIP {
		return "verify status with cm aws status --all; Elastic IP is retained"
	}
	return "nothing active was destroyed; Elastic IP is retained"
}
func awsDestroyReleaseStatus(result AWSDestroyResult) string {
	if len(result.DeferredHosts) > 0 {
		return "partial"
	}
	if len(result.ReleasedHosts) > 0 || len(result.TerminatedInstances) > 0 || result.DisassociatedElasticIP {
		return "complete"
	}
	return "none"
}
func formatStringList(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}
func FormatAWSTags(tags []AWSTagConfig) string {
	parts := make([]string, 0, len(tags))
	for _, tag := range tags {
		parts = append(parts, fmt.Sprintf("%s=%s", tag.Key, tag.Value))
	}
	return strings.Join(parts, ", ")
}
func formatStringMap(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return strings.Join(parts, ", ")
}
func managedTagsMatch(tags []AWSTagConfig, plan MacPlan) bool {
	return hasTag(tags, "cm-managed", "true") &&
		hasTag(tags, "cm-profile", plan.ProfileName) &&
		hasTag(tags, "cm-account-email", plan.AccountEmail)
}
func managedRequiredTags(plan MacPlan) []AWSTagConfig {
	return []AWSTagConfig{
		{Key: "cm-managed", Value: "true"},
		{Key: "cm-profile", Value: plan.ProfileName},
		{Key: "cm-account-email", Value: plan.AccountEmail},
	}
}
func managedTagsMismatch(tags []AWSTagConfig, plan MacPlan) string {
	required := managedRequiredTags(plan)
	missing := make([]string, 0, len(required))
	for _, tag := range required {
		if !hasTag(tags, tag.Key, tag.Value) {
			missing = append(missing, fmt.Sprintf("missing %s=%s", tag.Key, tag.Value))
		}
	}
	if len(missing) == 0 {
		return "required tags are present but did not match"
	}
	return strings.Join(missing, ", ")
}
func validateAdoptionCandidates(plan MacPlan, status AWSStatus) error {
	if len(status.Hosts) == 0 {
		return fmt.Errorf("no dedicated host found with Name=%s", plan.ResourceName)
	}
	if len(status.Instances) == 0 {
		return fmt.Errorf("no instance found with Name=%s", plan.ResourceName)
	}
	if !hasTag(status.ElasticIP.Tags, plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value) {
		return fmt.Errorf("elastic ip %s is missing required owner tag %s=%s", plan.ElasticIPAllocationID, plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value)
	}
	for _, instance := range status.Instances {
		matchedHost := false
		for _, host := range status.Hosts {
			if instance.HostID == host.HostID {
				matchedHost = true
				break
			}
		}
		if !matchedHost {
			return fmt.Errorf("instance %s is not running on a matched dedicated host", instance.InstanceID)
		}
		if status.ElasticIP.InstanceID != "" && status.ElasticIP.InstanceID != instance.InstanceID {
			return fmt.Errorf("elastic ip %s is associated with unexpected instance %s", plan.ElasticIPAllocationID, status.ElasticIP.InstanceID)
		}
	}
	return nil
}
func hasTag(tags []AWSTagConfig, key, value string) bool {
	for _, tag := range tags {
		if tag.Key == key && tag.Value == value {
			return true
		}
	}
	return false
}
func MaskAWSAccount(account string) string {
	if len(account) <= 8 {
		return account
	}
	return account[:4] + "****" + account[len(account)-4:]
}
