package connectmac

import (
	"context"
	"fmt"
	"strings"
)

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
	candidates := createCandidates(ctx, client, plan, profile.AWS)
	for _, candidate := range candidates {
		attempt := candidate
		if attempt.Status != "candidate" {
			attempts = append(attempts, attempt)
			lastErr = fmt.Errorf("%s", attempt.Detail)
			continue
		}
		attemptPlan := plan
		attemptPlan.SubnetID = attempt.SubnetID
		hostID, err := client.AllocateHost(ctx, attemptPlan, attempt.AvailabilityZoneID, attempt.InstanceType)
		if err != nil {
			attempt.Status = "failed"
			attempt.Detail = err.Error()
			attempts = append(attempts, attempt)
			lastErr = err
			continue
		}
		attempt.Detail = fmt.Sprintf("host=%s", hostID)
		instanceID, err := client.RunInstance(ctx, attemptPlan, hostID, attempt.InstanceType, attempt.AMI)
		if err != nil {
			attempt.Status = "failed"
			attempt.Detail = fmt.Sprintf("host=%s; %s", hostID, err.Error())
			attempts = append(attempts, attempt)
			return MacPlan{}, AWSCreateResult{}, AWSCreateAttemptsError{Attempts: attempts, Stage: "run-instance", HostID: hostID, Cause: fmt.Errorf("dedicated host %s was allocated; stop retrying and fix instance launch: %w", hostID, err)}
		}
		if err := s.waitInstanceRunning(ctx, client, plan, instanceID); err != nil {
			attempt.Status = "failed"
			attempt.Detail = fmt.Sprintf("host=%s instance=%s; %s", hostID, instanceID, err.Error())
			attempts = append(attempts, attempt)
			return MacPlan{}, AWSCreateResult{}, AWSCreateAttemptsError{Attempts: attempts, Stage: "wait-instance-running", HostID: hostID, InstanceID: instanceID, Cause: fmt.Errorf("dedicated host %s and instance %s were created; stop retrying and wait or clean up: %w", hostID, instanceID, err)}
		}
		associationID, err := client.AssociateElasticIP(ctx, plan.ElasticIPAllocationID, instanceID)
		if err != nil {
			attempt.Status = "failed"
			attempt.Detail = fmt.Sprintf("host=%s instance=%s; %s", hostID, instanceID, err.Error())
			attempts = append(attempts, attempt)
			return MacPlan{}, AWSCreateResult{}, AWSCreateAttemptsError{Attempts: attempts, Stage: "associate-elastic-ip", HostID: hostID, InstanceID: instanceID, Cause: fmt.Errorf("dedicated host %s and instance %s were created; stop retrying and fix elastic ip association: %w", hostID, instanceID, err)}
		}
		attempt.Status = "created"
		attempt.Detail = fmt.Sprintf("host=%s instance=%s", hostID, instanceID)
		attempts = append(attempts, attempt)
		return plan, AWSCreateResult{
			HostID:              hostID,
			InstanceID:          instanceID,
			AssociationID:       associationID,
			AvailabilityZoneID:  attempt.AvailabilityZoneID,
			InstanceType:        attempt.InstanceType,
			AMI:                 attempt.AMI,
			SubnetID:            attempt.SubnetID,
			ElasticIPAllocation: plan.ElasticIPAllocationID,
			Attempts:            attempts,
		}, nil
	}
	if lastErr != nil {
		return MacPlan{}, AWSCreateResult{}, AWSCreateAttemptsError{Attempts: attempts, Cause: lastErr}
	}
	return MacPlan{}, AWSCreateResult{}, AWSCreateAttemptsError{Attempts: attempts, Cause: fmt.Errorf("no aws create candidate was attempted")}
}
func (s AWSService) CreateCandidates(ctx context.Context, profile Profile) (MacPlan, []AWSCreateAttempt, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, nil, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, nil, err
	}
	return plan, createCandidates(ctx, client, plan, profile.AWS), nil
}
func createCandidates(ctx context.Context, client AWSClient, plan MacPlan, cfg AWSConfig) []AWSCreateAttempt {
	quotas, quotaErr := client.DedicatedHostQuotas(ctx, plan.InstanceTypePriority)
	var inUse map[string]int
	if quotaErr == nil {
		if hosts, err := client.DescribeAllHosts(ctx); err == nil {
			inUse = dedicatedHostUsageByInstanceType(hosts)
		}
	}
	candidates := make([]AWSCreateAttempt, 0, len(plan.InstanceTypePriority)*len(plan.AvailabilityZoneIDs))
	for _, instanceType := range plan.InstanceTypePriority {
		ami := amiForInstanceType(cfg, instanceType)
		if ami == "" {
			continue
		}
		if quota, ok := quotas[instanceType]; quotaErr == nil && inUse != nil && ok && float64(inUse[instanceType]) >= quota {
			for _, zoneID := range plan.AvailabilityZoneIDs {
				candidates = append(candidates, AWSCreateAttempt{
					AvailabilityZoneID: zoneID,
					InstanceType:       instanceType,
					AMI:                ami,
					Status:             "quota-exhausted",
					Detail:             fmt.Sprintf("%s quota %.0f is fully used", instanceType, quota),
				})
			}
			continue
		}
		offerings, err := client.InstanceTypeOfferings(ctx, instanceType)
		if err != nil {
			candidates = append(candidates, AWSCreateAttempt{
				InstanceType: instanceType,
				AMI:          ami,
				Status:       "failed",
				Detail:       err.Error(),
			})
			continue
		}
		supportedZones := stringSet(offerings)
		for _, zoneID := range plan.AvailabilityZoneIDs {
			candidate := AWSCreateAttempt{
				AvailabilityZoneID: zoneID,
				InstanceType:       instanceType,
				AMI:                ami,
			}
			if !supportedZones[zoneID] {
				candidate.Status = "unsupported"
				candidate.Detail = fmt.Sprintf("%s is not offered in %s", instanceType, zoneID)
				candidates = append(candidates, candidate)
				continue
			}
			subnetID := plan.SubnetForAZ(zoneID)
			if subnetID == "" {
				candidate.Status = "no-subnet"
				candidate.Detail = fmt.Sprintf("no subnet configured for availability zone %s", zoneID)
				candidates = append(candidates, candidate)
				continue
			}
			candidate.SubnetID = subnetID
			actualZoneID, err := client.SubnetAvailabilityZoneID(ctx, subnetID)
			if err != nil {
				candidate.Status = "failed"
				candidate.Detail = err.Error()
				candidates = append(candidates, candidate)
				continue
			}
			if actualZoneID != zoneID {
				candidate.Status = "subnet-mismatch"
				candidate.Detail = fmt.Sprintf("subnet %s is in %s, but candidate availability zone is %s", subnetID, actualZoneID, zoneID)
				candidates = append(candidates, candidate)
				continue
			}
			candidate.Status = "candidate"
			candidate.Detail = "ready for AllocateHost"
			candidates = append(candidates, candidate)
		}
	}
	return candidates
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
	if err := s.waitInstanceRunning(ctx, client, plan, instanceID); err != nil {
		return MacPlan{}, AWSCreateResult{}, err
	}
	associationID, err := client.AssociateElasticIP(ctx, plan.ElasticIPAllocationID, instanceID)
	if err != nil {
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
