package connectmac

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type AWSService struct {
	Now               func() time.Time
	NewClient         func(ctx context.Context, plan MacPlan) (AWSClient, error)
	ReadyPollInterval time.Duration
	ReadyTimeout      time.Duration
}

func NewAWSService() AWSService {
	return AWSService{
		Now: time.Now,
		NewClient: func(ctx context.Context, plan MacPlan) (AWSClient, error) {
			client, err := NewRealAWSClient(ctx, plan)
			return client, err
		},
	}
}

func (s AWSService) Plan(profile Profile) (MacPlan, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return BuildMacPlan(profile, now)
}

func (s AWSService) Status(ctx context.Context, profile Profile) (MacPlan, AWSStatus, error) {
	return s.StatusWithOptions(ctx, profile, AWSStatusOptions{})
}

func (s AWSService) StatusWithOptions(ctx context.Context, profile Profile, options AWSStatusOptions) (MacPlan, AWSStatus, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	status, err := client.DescribeStatus(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	if !options.IncludeTerminal {
		status = filterTerminalAWSStatus(status)
	}
	return plan, status, nil
}

func (s AWSService) WaitReady(ctx context.Context, profile Profile) (MacPlan, AWSStatus, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	timeout := s.ReadyTimeout
	if timeout == 0 {
		timeout = 45 * time.Minute
	}
	interval := s.ReadyPollInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var last AWSStatus
	for {
		status, err := client.DescribeStatus(ctx, plan)
		if err != nil {
			return MacPlan{}, AWSStatus{}, err
		}
		last = status
		if AWSStatusReady(status) {
			return plan, status, nil
		}
		if !time.Now().Before(deadline) {
			return MacPlan{}, last, fmt.Errorf("timed out waiting for AWS Mac readiness: %s", AWSReadinessSummary(status))
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return MacPlan{}, last, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s AWSService) AdoptionPreview(ctx context.Context, profile Profile) (MacPlan, AWSStatus, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	status, err := client.DescribeAdoptionCandidates(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	if err := validateAdoptionCandidates(plan, status); err != nil {
		return MacPlan{}, AWSStatus{}, err
	}
	return plan, status, nil
}

func (s AWSService) Adopt(ctx context.Context, profile Profile) (MacPlan, AWSAdoptResult, error) {
	plan, status, err := s.AdoptionPreview(ctx, profile)
	if err != nil {
		return MacPlan{}, AWSAdoptResult{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSAdoptResult{}, err
	}
	resourceIDs := make([]string, 0, len(status.Hosts)+len(status.Instances))
	for _, host := range status.Hosts {
		resourceIDs = append(resourceIDs, host.HostID)
	}
	for _, instance := range status.Instances {
		resourceIDs = append(resourceIDs, instance.InstanceID)
	}
	tags := adoptionTags(plan)
	if err := client.TagResources(ctx, resourceIDs, tags); err != nil {
		return MacPlan{}, AWSAdoptResult{}, err
	}
	return plan, AWSAdoptResult{
		TaggedResources: resourceIDs,
		Tags:            tags,
	}, nil
}

func (s AWSService) AdoptHostPreview(ctx context.Context, profile Profile, hostID string) (MacPlan, DedicatedHostStatus, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, DedicatedHostStatus{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, DedicatedHostStatus{}, err
	}
	host, err := client.DescribeHostByID(ctx, hostID)
	if err != nil {
		return MacPlan{}, DedicatedHostStatus{}, err
	}
	if err := validateHostForProfile(plan, host); err != nil {
		return MacPlan{}, DedicatedHostStatus{}, err
	}
	return plan, host, nil
}

func (s AWSService) AdoptHost(ctx context.Context, profile Profile, hostID string) (MacPlan, AWSAdoptResult, error) {
	plan, host, err := s.AdoptHostPreview(ctx, profile, hostID)
	if err != nil {
		return MacPlan{}, AWSAdoptResult{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSAdoptResult{}, err
	}
	tags := adoptionTags(plan)
	if err := client.TagResources(ctx, []string{host.HostID}, tags); err != nil {
		return MacPlan{}, AWSAdoptResult{}, err
	}
	return plan, AWSAdoptResult{TaggedResources: []string{host.HostID}, Tags: tags}, nil
}

func (s AWSService) Create(ctx context.Context, profile Profile) (MacPlan, AWSCreateResult, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	if _, err := client.VerifyElasticIPOwner(ctx, plan); err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	var lastErr error
	var attempts []AWSCreateAttempt
	for _, instanceType := range plan.InstanceTypePriority {
		ami := amiForInstanceType(AWSConfig{AMI: AWSAMIConfig{MacX86: profile.AWS.AMI.MacX86, MacARM: profile.AWS.AMI.MacARM}}, instanceType)
		if ami == "" {
			continue
		}
		offerings, err := client.InstanceTypeOfferings(ctx, instanceType)
		if err != nil {
			attempts = append(attempts, AWSCreateAttempt{
				InstanceType: instanceType,
				Status:       "failed",
				Detail:       err.Error(),
			})
			lastErr = err
			continue
		}
		supportedZones := stringSet(offerings)
		for _, zoneID := range plan.AvailabilityZoneIDs {
			attempt := AWSCreateAttempt{
				AvailabilityZoneID: zoneID,
				InstanceType:       instanceType,
			}
			if !supportedZones[zoneID] {
				attempt.Status = "unsupported"
				attempt.Detail = fmt.Sprintf("%s is not offered in %s", instanceType, zoneID)
				attempts = append(attempts, attempt)
				lastErr = fmt.Errorf("%s", attempt.Detail)
				continue
			}
			subnetID, err := subnetForLaunch(ctx, client, plan, zoneID)
			if err != nil {
				attempt.Status = "failed"
				attempt.Detail = err.Error()
				attempts = append(attempts, attempt)
				lastErr = err
				continue
			}
			attempt.SubnetID = subnetID
			attemptPlan := plan
			attemptPlan.SubnetID = subnetID
			hostID, err := client.AllocateHost(ctx, attemptPlan, zoneID, instanceType)
			if err != nil {
				attempt.Status = "failed"
				attempt.Detail = err.Error()
				attempts = append(attempts, attempt)
				lastErr = err
				continue
			}
			instanceID, err := client.RunInstance(ctx, attemptPlan, hostID, instanceType, ami)
			if err != nil {
				_ = client.ReleaseHost(ctx, hostID)
				attempt.Status = "failed"
				attempt.Detail = err.Error()
				attempts = append(attempts, attempt)
				lastErr = err
				continue
			}
			associationID, err := client.AssociateElasticIP(ctx, plan.ElasticIPAllocationID, instanceID)
			if err != nil {
				_ = client.TerminateInstance(ctx, instanceID)
				_ = client.ReleaseHost(ctx, hostID)
				attempt.Status = "failed"
				attempt.Detail = err.Error()
				attempts = append(attempts, attempt)
				lastErr = err
				continue
			}
			attempt.Status = "created"
			attempt.Detail = fmt.Sprintf("host=%s instance=%s", hostID, instanceID)
			attempts = append(attempts, attempt)
			return plan, AWSCreateResult{
				HostID:              hostID,
				InstanceID:          instanceID,
				AssociationID:       associationID,
				AvailabilityZoneID:  zoneID,
				InstanceType:        instanceType,
				AMI:                 ami,
				SubnetID:            subnetID,
				ElasticIPAllocation: plan.ElasticIPAllocationID,
				Attempts:            attempts,
			}, nil
		}
	}
	if lastErr != nil {
		return MacPlan{}, AWSCreateResult{}, AWSCreateAttemptsError{Attempts: attempts, Cause: lastErr}
	}
	return MacPlan{}, AWSCreateResult{}, AWSCreateAttemptsError{Attempts: attempts, Cause: fmt.Errorf("no aws create candidate was attempted")}
}

func (s AWSService) LaunchOnHostPreview(ctx context.Context, profile Profile, hostID string) (MacPlan, AWSLaunchOnHostPreview, error) {
	plan, host, err := s.AdoptHostPreview(ctx, profile, hostID)
	if err != nil {
		return MacPlan{}, AWSLaunchOnHostPreview{}, err
	}
	instanceType := host.InstanceType
	ami := amiForInstanceType(profile.AWS, instanceType)
	if ami == "" {
		return MacPlan{}, AWSLaunchOnHostPreview{}, fmt.Errorf("no architecture-compatible AMI configured for host instance type %s", instanceType)
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSLaunchOnHostPreview{}, err
	}
	subnetID, err := subnetForLaunch(ctx, client, plan, host.ZoneID)
	if err != nil {
		return MacPlan{}, AWSLaunchOnHostPreview{}, err
	}
	return plan, AWSLaunchOnHostPreview{
		HostID:             host.HostID,
		AvailabilityZoneID: host.ZoneID,
		InstanceType:       instanceType,
		AMI:                ami,
		SubnetID:           subnetID,
	}, nil
}

func (s AWSService) LaunchOnHost(ctx context.Context, profile Profile, hostID string) (MacPlan, AWSCreateResult, error) {
	plan, preview, err := s.LaunchOnHostPreview(ctx, profile, hostID)
	if err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	if _, err := client.VerifyElasticIPOwner(ctx, plan); err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	attemptPlan := plan
	attemptPlan.SubnetID = preview.SubnetID
	instanceID, err := client.RunInstance(ctx, attemptPlan, preview.HostID, preview.InstanceType, preview.AMI)
	if err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	associationID, err := client.AssociateElasticIP(ctx, plan.ElasticIPAllocationID, instanceID)
	if err != nil {
		_ = client.TerminateInstance(ctx, instanceID)
		return MacPlan{}, AWSCreateResult{}, err
	}
	return plan, AWSCreateResult{
		HostID:              preview.HostID,
		InstanceID:          instanceID,
		AssociationID:       associationID,
		AvailabilityZoneID:  preview.AvailabilityZoneID,
		InstanceType:        preview.InstanceType,
		AMI:                 preview.AMI,
		SubnetID:            preview.SubnetID,
		ElasticIPAllocation: plan.ElasticIPAllocationID,
	}, nil
}

func subnetForLaunch(ctx context.Context, client AWSClient, plan MacPlan, availabilityZoneID string) (string, error) {
	subnetID := plan.SubnetForAZ(availabilityZoneID)
	if subnetID == "" {
		return "", fmt.Errorf("no subnet configured for availability zone %s; set aws.subnets_by_az.%s or aws.subnet_id", availabilityZoneID, availabilityZoneID)
	}
	actualZoneID, err := client.SubnetAvailabilityZoneID(ctx, subnetID)
	if err != nil {
		return "", err
	}
	if actualZoneID != availabilityZoneID {
		return "", fmt.Errorf("subnet %s is in %s, but selected host availability zone is %s", subnetID, actualZoneID, availabilityZoneID)
	}
	return subnetID, nil
}

func adoptionTags(plan MacPlan) []AWSTagConfig {
	tags := []AWSTagConfig{
		{Key: "cm-managed", Value: "true"},
		{Key: "cm-profile", Value: plan.ProfileName},
		{Key: "cm-account-email", Value: plan.AccountEmail},
	}
	for _, tag := range plan.Tags {
		if tag.Key == "cm-creator" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func validateHostForProfile(plan MacPlan, host DedicatedHostStatus) error {
	if host.HostID == "" {
		return fmt.Errorf("dedicated host id is empty")
	}
	if host.State != "available" {
		return fmt.Errorf("dedicated host %s state is %s, want available", host.HostID, emptyStatus(host.State))
	}
	if len(host.InstanceIDs) > 0 {
		return fmt.Errorf("dedicated host %s is not empty; running instances: %s", host.HostID, strings.Join(host.InstanceIDs, ", "))
	}
	if host.InstanceType == "" {
		return fmt.Errorf("dedicated host %s instance type is empty", host.HostID)
	}
	if !containsString(plan.InstanceTypePriority, host.InstanceType) {
		return fmt.Errorf("dedicated host %s type %s is not allowed by instance_type_priority", host.HostID, host.InstanceType)
	}
	if host.ZoneID == "" {
		return fmt.Errorf("dedicated host %s availability zone id is empty", host.HostID)
	}
	if !containsString(plan.AvailabilityZoneIDs, host.ZoneID) {
		return fmt.Errorf("dedicated host %s zone %s is not allowed by availability_zone_ids", host.HostID, host.ZoneID)
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func AWSStatusReady(status AWSStatus) bool {
	for _, instance := range status.Instances {
		if InstanceReady(instance, status.ElasticIP) {
			return true
		}
	}
	return false
}

func InstanceReady(instance InstanceStatus, eip ElasticIP) bool {
	return instance.State == "running" &&
		instance.InstanceID != "" &&
		eip.InstanceID == instance.InstanceID &&
		instance.SystemStatus == "ok" &&
		instance.InstanceStatusCheck == "ok" &&
		instance.EBSStatus == "ok"
}

func AWSReadinessSummary(status AWSStatus) string {
	if len(status.Instances) == 0 {
		return "no managed instance found"
	}
	parts := make([]string, 0, len(status.Instances))
	for _, instance := range status.Instances {
		eipBound := status.ElasticIP.InstanceID == instance.InstanceID && instance.InstanceID != ""
		parts = append(parts, fmt.Sprintf("instance=%s state=%s eip_bound=%t system_status=%s instance_status=%s ebs_status=%s",
			instance.InstanceID,
			emptyStatus(instance.State),
			eipBound,
			emptyStatus(instance.SystemStatus),
			emptyStatus(instance.InstanceStatusCheck),
			emptyStatus(instance.EBSStatus),
		))
	}
	return strings.Join(parts, "; ")
}

func emptyStatus(value string) string {
	if value == "" {
		return "pending"
	}
	return value
}

func (s AWSService) Destroy(ctx context.Context, profile Profile) (MacPlan, AWSDestroyResult, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, AWSDestroyResult{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSDestroyResult{}, err
	}
	status, err := client.DescribeStatus(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSDestroyResult{}, err
	}
	result := AWSDestroyResult{RetainedElasticIP: status.ElasticIP}
	for _, instance := range status.Instances {
		if isTerminalInstanceState(instance.State) {
			result.SkippedInstances = append(result.SkippedInstances, fmt.Sprintf("%s:%s", instance.InstanceID, instance.State))
			continue
		}
		if !managedTagsMatch(instance.Tags, plan) {
			return MacPlan{}, AWSDestroyResult{}, fmt.Errorf("refuse to terminate instance %s because required safety tags do not match", instance.InstanceID)
		}
		if status.ElasticIP.AssociationID != "" && status.ElasticIP.InstanceID == instance.InstanceID {
			if err := client.DisassociateElasticIP(ctx, status.ElasticIP.AssociationID); err != nil {
				return MacPlan{}, result, err
			}
			result.DisassociatedElasticIP = true
		}
		if err := client.TerminateInstance(ctx, instance.InstanceID); err != nil {
			return MacPlan{}, result, AWSDestroyPartialError{Result: result, Cause: err}
		}
		result.TerminatedInstances = append(result.TerminatedInstances, instance.InstanceID)
	}
	for _, host := range status.Hosts {
		if isTerminalHostState(host.State) {
			result.SkippedHosts = append(result.SkippedHosts, fmt.Sprintf("%s:%s", host.HostID, host.State))
			continue
		}
		if !managedTagsMatch(host.Tags, plan) {
			return MacPlan{}, AWSDestroyResult{}, fmt.Errorf("refuse to release host %s because required safety tags do not match", host.HostID)
		}
		if err := client.ReleaseHost(ctx, host.HostID); err != nil {
			return MacPlan{}, result, AWSDestroyPartialError{Result: result, Cause: err}
		}
		result.ReleasedHosts = append(result.ReleasedHosts, host.HostID)
	}
	return plan, result, nil
}

func filterTerminalAWSStatus(status AWSStatus) AWSStatus {
	filtered := status
	filtered.Hosts = nil
	for _, host := range status.Hosts {
		if !isTerminalHostState(host.State) {
			filtered.Hosts = append(filtered.Hosts, host)
		}
	}
	filtered.Instances = nil
	for _, instance := range status.Instances {
		if !isTerminalInstanceState(instance.State) {
			filtered.Instances = append(filtered.Instances, instance)
		}
	}
	return filtered
}

func isTerminalInstanceState(state string) bool {
	return state == "terminated"
}

func isTerminalHostState(state string) bool {
	return state == "released" || state == "released-permanent-failure"
}

func (s AWSService) client(ctx context.Context, plan MacPlan) (AWSClient, error) {
	if s.NewClient == nil {
		return NewRealAWSClient(ctx, plan)
	}
	return s.NewClient(ctx, plan)
}

type AWSCreateResult struct {
	HostID              string
	InstanceID          string
	AssociationID       string
	AvailabilityZoneID  string
	InstanceType        string
	AMI                 string
	SubnetID            string
	ElasticIPAllocation string
	Attempts            []AWSCreateAttempt
}

type AWSCreateAttempt struct {
	AvailabilityZoneID string
	InstanceType       string
	SubnetID           string
	Status             string
	Detail             string
}

type AWSCreateAttemptsError struct {
	Attempts []AWSCreateAttempt
	Cause    error
}

func (e AWSCreateAttemptsError) Error() string {
	var b strings.Builder
	if e.Cause != nil {
		fmt.Fprintf(&b, "%v\n", e.Cause)
	}
	if len(e.Attempts) > 0 {
		fmt.Fprint(&b, FormatAWSCreateAttempts(e.Attempts))
	}
	return strings.TrimSpace(b.String())
}

type AWSDestroyResult struct {
	DisassociatedElasticIP bool
	TerminatedInstances    []string
	ReleasedHosts          []string
	SkippedInstances       []string
	SkippedHosts           []string
	RetainedElasticIP      ElasticIP
}

type AWSDestroyPartialError struct {
	Result AWSDestroyResult
	Cause  error
}

func (e AWSDestroyPartialError) Error() string {
	if e.Cause == nil {
		return "aws destroy partially completed"
	}
	return fmt.Sprintf("%v; partial destroy state recorded; run the same destroy command again after AWS finishes the pending transition", e.Cause)
}

type AWSAdoptResult struct {
	TaggedResources []string
	Tags            []AWSTagConfig
}

type AWSLaunchOnHostPreview struct {
	HostID             string
	AvailabilityZoneID string
	InstanceType       string
	AMI                string
	SubnetID           string
}

type AWSStatusOptions struct {
	IncludeTerminal bool
}

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

func FormatMacDestroyPreview(plan MacPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac destroy preview for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Managed resource name: %s\n", plan.ResourceName)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Safety tags required before any mutation: %s\n", FormatAWSTags(plan.Tags[1:]))
	fmt.Fprintln(&b, "Operations:")
	fmt.Fprintln(&b, "- Disassociate Elastic IP only if attached to the managed instance; retain the Elastic IP allocation")
	fmt.Fprintln(&b, "- Terminate the managed EC2 instance")
	fmt.Fprintln(&b, "- Release the managed Dedicated Host when AWS allows release")
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
	fmt.Fprintf(&b, "Instances matched by Name: %d\n", len(status.Instances))
	for _, instance := range status.Instances {
		fmt.Fprintf(&b, "- instance=%s state=%s type=%s host=%s public_ip=%s\n", instance.InstanceID, instance.State, instance.InstanceType, instance.HostID, instance.PublicIP)
	}
	fmt.Fprintf(&b, "Elastic IP owner tag: %s=%s\n", plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value)
	fmt.Fprintf(&b, "Tags to add: cm-managed=true, cm-profile=%s, cm-account-email=%s\n", plan.ProfileName, plan.AccountEmail)
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

func FormatAWSReadyStatus(plan MacPlan, status AWSStatus) string {
	return fmt.Sprintf("AWS Mac ready for profile %s: %t\n%s\n", plan.ProfileName, AWSStatusReady(status), AWSReadinessSummary(status))
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
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, value := range row {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}
	var b strings.Builder
	fmt.Fprintln(&b, "Create attempts:")
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

func emptyTableValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func FormatAWSDestroyResult(plan MacPlan, result AWSDestroyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac destroy executed for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Disassociated Elastic IP: %t\n", result.DisassociatedElasticIP)
	fmt.Fprintf(&b, "Retained Elastic IP: %s public_ip=%s\n", emptyTableValue(result.RetainedElasticIP.AllocationID), emptyTableValue(result.RetainedElasticIP.PublicIP))
	fmt.Fprintf(&b, "Terminated instances: %s\n", strings.Join(result.TerminatedInstances, ", "))
	fmt.Fprintf(&b, "Skipped instances: %s\n", strings.Join(result.SkippedInstances, ", "))
	fmt.Fprintf(&b, "Released hosts: %s\n", strings.Join(result.ReleasedHosts, ", "))
	fmt.Fprintf(&b, "Skipped hosts: %s\n", strings.Join(result.SkippedHosts, ", "))
	return b.String()
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
