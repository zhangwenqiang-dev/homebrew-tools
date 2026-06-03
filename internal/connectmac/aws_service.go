package connectmac

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type AWSService struct {
	Now       func() time.Time
	NewClient func(ctx context.Context, plan MacPlan) (AWSClient, error)
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
	return plan, status, nil
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
	tags := []AWSTagConfig{
		{Key: "cm-managed", Value: "true"},
		{Key: "cm-profile", Value: plan.ProfileName},
		{Key: "cm-account-email", Value: plan.AccountEmail},
	}
	if err := client.TagResources(ctx, resourceIDs, tags); err != nil {
		return MacPlan{}, AWSAdoptResult{}, err
	}
	return plan, AWSAdoptResult{
		TaggedResources: resourceIDs,
		Tags:            tags,
	}, nil
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
	for _, instanceType := range plan.InstanceTypePriority {
		ami := amiForInstanceType(AWSConfig{AMI: AWSAMIConfig{MacX86: profile.AWS.AMI.MacX86, MacARM: profile.AWS.AMI.MacARM}}, instanceType)
		if ami == "" {
			continue
		}
		for _, zoneID := range plan.AvailabilityZoneIDs {
			hostID, err := client.AllocateHost(ctx, plan, zoneID, instanceType)
			if err != nil {
				lastErr = err
				continue
			}
			instanceID, err := client.RunInstance(ctx, plan, hostID, instanceType, ami)
			if err != nil {
				_ = client.ReleaseHost(ctx, hostID)
				lastErr = err
				continue
			}
			associationID, err := client.AssociateElasticIP(ctx, plan.ElasticIPAllocationID, instanceID)
			if err != nil {
				_ = client.TerminateInstance(ctx, instanceID)
				_ = client.ReleaseHost(ctx, hostID)
				lastErr = err
				continue
			}
			return plan, AWSCreateResult{
				HostID:              hostID,
				InstanceID:          instanceID,
				AssociationID:       associationID,
				AvailabilityZoneID:  zoneID,
				InstanceType:        instanceType,
				AMI:                 ami,
				ElasticIPAllocation: plan.ElasticIPAllocationID,
			}, nil
		}
	}
	if lastErr != nil {
		return MacPlan{}, AWSCreateResult{}, lastErr
	}
	return MacPlan{}, AWSCreateResult{}, fmt.Errorf("no aws create candidate was attempted")
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
	result := AWSDestroyResult{}
	for _, instance := range status.Instances {
		if !managedTagsMatch(instance.Tags, plan) {
			return MacPlan{}, AWSDestroyResult{}, fmt.Errorf("refuse to terminate instance %s because required safety tags do not match", instance.InstanceID)
		}
		if status.ElasticIP.AssociationID != "" && status.ElasticIP.InstanceID == instance.InstanceID {
			if err := client.DisassociateElasticIP(ctx, status.ElasticIP.AssociationID); err != nil {
				return MacPlan{}, AWSDestroyResult{}, err
			}
			result.DisassociatedElasticIP = true
		}
		if err := client.TerminateInstance(ctx, instance.InstanceID); err != nil {
			return MacPlan{}, AWSDestroyResult{}, err
		}
		result.TerminatedInstances = append(result.TerminatedInstances, instance.InstanceID)
	}
	for _, host := range status.Hosts {
		if !managedTagsMatch(host.Tags, plan) {
			return MacPlan{}, AWSDestroyResult{}, fmt.Errorf("refuse to release host %s because required safety tags do not match", host.HostID)
		}
		if err := client.ReleaseHost(ctx, host.HostID); err != nil {
			return MacPlan{}, AWSDestroyResult{}, err
		}
		result.ReleasedHosts = append(result.ReleasedHosts, host.HostID)
	}
	return plan, result, nil
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
	ElasticIPAllocation string
}

type AWSDestroyResult struct {
	DisassociatedElasticIP bool
	TerminatedInstances    []string
	ReleasedHosts          []string
}

type AWSAdoptResult struct {
	TaggedResources []string
	Tags            []AWSTagConfig
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
	fmt.Fprintf(&b, "Subnet: %s\n", plan.SubnetID)
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
	fmt.Fprintln(&b, "- Disassociate Elastic IP only if attached to the managed instance")
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
		fmt.Fprintf(&b, "- instance=%s state=%s type=%s host=%s public_ip=%s\n", instance.InstanceID, instance.State, instance.InstanceType, instance.HostID, instance.PublicIP)
	}
	fmt.Fprintf(&b, "Elastic IP: allocation=%s association=%s instance=%s public_ip=%s\n", status.ElasticIP.AllocationID, status.ElasticIP.AssociationID, status.ElasticIP.InstanceID, status.ElasticIP.PublicIP)
	return b.String()
}

func FormatAWSCreateResult(plan MacPlan, result AWSCreateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac created for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Host: %s\n", result.HostID)
	fmt.Fprintf(&b, "Instance: %s\n", result.InstanceID)
	fmt.Fprintf(&b, "EIP association: %s\n", result.AssociationID)
	fmt.Fprintf(&b, "Selected: %s %s %s\n", result.AvailabilityZoneID, result.InstanceType, result.AMI)
	return b.String()
}

func FormatAWSDestroyResult(plan MacPlan, result AWSDestroyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac destroy executed for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Disassociated Elastic IP: %t\n", result.DisassociatedElasticIP)
	fmt.Fprintf(&b, "Terminated instances: %s\n", strings.Join(result.TerminatedInstances, ", "))
	fmt.Fprintf(&b, "Released hosts: %s\n", strings.Join(result.ReleasedHosts, ", "))
	return b.String()
}

func FormatAWSTags(tags []AWSTagConfig) string {
	parts := make([]string, 0, len(tags))
	for _, tag := range tags {
		parts = append(parts, fmt.Sprintf("%s=%s", tag.Key, tag.Value))
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
