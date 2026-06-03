package connectmac

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuildMacPlanSelectsARMDefault(t *testing.T) {
	profile := validAWSProfile()
	profile.AWS.InstanceTypePriority = nil
	profile.AWS.AllowIntelFallback = false
	plan, err := BuildMacPlan(profile, time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildMacPlan returned error: %v", err)
	}
	if plan.ResourceName != "xcode-20260603-user@example.com" {
		t.Fatalf("resource name = %q", plan.ResourceName)
	}
	if plan.SelectedInstanceType != "mac2.metal" {
		t.Fatalf("selected instance type = %q, want mac2.metal", plan.SelectedInstanceType)
	}
	if plan.SelectedAMI != "ami-063755aadeb97329a" {
		t.Fatalf("selected ami = %q, want arm ami", plan.SelectedAMI)
	}
	for _, instanceType := range plan.InstanceTypePriority {
		if instanceType == "mac1.metal" {
			t.Fatal("default plan should exclude mac1.metal when intel fallback is disabled")
		}
	}
}

func TestBuildMacPlanSelectsIntelAMIWhenAllowed(t *testing.T) {
	profile := validAWSProfile()
	profile.AWS.AllowIntelFallback = true
	profile.AWS.InstanceTypePriority = []string{"mac1.metal"}
	plan, err := BuildMacPlan(profile, time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildMacPlan returned error: %v", err)
	}
	if plan.SelectedInstanceType != "mac1.metal" {
		t.Fatalf("selected instance type = %q, want mac1.metal", plan.SelectedInstanceType)
	}
	if plan.SelectedAMI != "ami-0538568e5d3653bea" {
		t.Fatalf("selected ami = %q, want x86 ami", plan.SelectedAMI)
	}
}

func TestFormatMacPlanIncludesSafetyDetails(t *testing.T) {
	plan, err := BuildMacPlan(validAWSProfile(), time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	text := FormatMacPlan(plan)
	for _, want := range []string{
		"AutoPlacement=off",
		"HostMaintenance=off",
		"cm-managed=true",
		"Elastic IP owner tag: Apple=user@example.com",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("plan text missing %q:\n%s", want, text)
		}
	}
}

func TestAWSServiceStatusUsesClient(t *testing.T) {
	fake := &fakeAWSClient{
		status: AWSStatus{
			CallerIdentity: CallerIdentity{Account: "123456789012", ARN: "arn:aws:iam::123456789012:user/cm"},
			Hosts:          []DedicatedHostStatus{{HostID: "h-1", State: "available", InstanceType: "mac2.metal", ZoneID: "usw2-az1", Tags: managedTestTags()}},
			ElasticIP:      ElasticIP{AllocationID: "<elastic-ip-allocation-id>", PublicIP: "203.0.113.10", Tags: []AWSTagConfig{{Key: "Apple", Value: "user@example.com"}}},
		},
	}
	service := testAWSService(fake)
	plan, status, err := service.Status(context.Background(), validAWSProfile())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if plan.ResourceName != "xcode-20260603-user@example.com" {
		t.Fatalf("resource name = %q", plan.ResourceName)
	}
	if len(status.Hosts) != 1 || status.Hosts[0].HostID != "h-1" {
		t.Fatalf("unexpected hosts: %+v", status.Hosts)
	}
}

func TestAWSServiceCreateAllocatesRunsAndAssociates(t *testing.T) {
	fake := &fakeAWSClient{
		eip: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", Tags: []AWSTagConfig{{Key: "Apple", Value: "user@example.com"}}},
	}
	service := testAWSService(fake)
	_, result, err := service.Create(context.Background(), validAWSProfile())
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if result.HostID != "h-created" || result.InstanceID != "i-created" || result.AssociationID != "eipassoc-created" {
		t.Fatalf("unexpected result: %+v", result)
	}
	want := []string{"verify-eip", "allocate:usw2-az1:mac2.metal", "run:h-created:mac2.metal:ami-063755aadeb97329a", "associate:<elastic-ip-allocation-id>:i-created"}
	if strings.Join(fake.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", fake.calls, want)
	}
}

func TestAWSServiceCreateRejectsWrongEIPOwner(t *testing.T) {
	fake := &fakeAWSClient{
		eip: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", Tags: []AWSTagConfig{{Key: "Apple", Value: "other@example.com"}}},
	}
	service := testAWSService(fake)
	_, _, err := service.Create(context.Background(), validAWSProfile())
	if err == nil || !strings.Contains(err.Error(), "missing required owner tag") {
		t.Fatalf("expected owner tag error, got %v", err)
	}
}

func TestAWSServiceDestroyRequiresManagedTags(t *testing.T) {
	fake := &fakeAWSClient{
		status: AWSStatus{
			Hosts:     []DedicatedHostStatus{{HostID: "h-1", Tags: []AWSTagConfig{{Key: "Name", Value: "not-managed"}}}},
			Instances: []InstanceStatus{{InstanceID: "i-1", Tags: managedTestTags()}},
		},
	}
	service := testAWSService(fake)
	_, _, err := service.Destroy(context.Background(), validAWSProfile())
	if err == nil || !strings.Contains(err.Error(), "refuse to release host") {
		t.Fatalf("expected safety tag error, got %v", err)
	}
}

func TestAWSServiceDestroyRunsSafeOrder(t *testing.T) {
	fake := &fakeAWSClient{
		status: AWSStatus{
			Hosts:     []DedicatedHostStatus{{HostID: "h-1", Tags: managedTestTags()}},
			Instances: []InstanceStatus{{InstanceID: "i-1", Tags: managedTestTags()}},
			ElasticIP: ElasticIP{AssociationID: "eipassoc-1", InstanceID: "i-1"},
		},
	}
	service := testAWSService(fake)
	_, result, err := service.Destroy(context.Background(), validAWSProfile())
	if err != nil {
		t.Fatalf("Destroy returned error: %v", err)
	}
	want := []string{"status", "disassociate:eipassoc-1", "terminate:i-1", "release:h-1"}
	if strings.Join(fake.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", fake.calls, want)
	}
	if len(result.TerminatedInstances) != 1 || len(result.ReleasedHosts) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAWSServiceAdoptTagsMatchedResources(t *testing.T) {
	fake := &fakeAWSClient{
		status: AWSStatus{
			Hosts:     []DedicatedHostStatus{{HostID: "h-1", Tags: []AWSTagConfig{{Key: "Name", Value: "xcode-20260603-user@example.com"}}}},
			Instances: []InstanceStatus{{InstanceID: "i-1", HostID: "h-1", Tags: []AWSTagConfig{{Key: "Name", Value: "xcode-20260603-user@example.com"}}}},
			ElasticIP: ElasticIP{AllocationID: "<elastic-ip-allocation-id>", InstanceID: "i-1", Tags: []AWSTagConfig{{Key: "Apple", Value: "user@example.com"}}},
		},
	}
	service := testAWSService(fake)
	_, result, err := service.Adopt(context.Background(), validAWSProfile())
	if err != nil {
		t.Fatalf("Adopt returned error: %v", err)
	}
	want := []string{"adoption", "tag:h-1,i-1"}
	if strings.Join(fake.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", fake.calls, want)
	}
	if strings.Join(result.TaggedResources, ",") != "h-1,i-1" {
		t.Fatalf("tagged resources = %v", result.TaggedResources)
	}
}

func testAWSService(fake *fakeAWSClient) AWSService {
	return AWSService{
		Now: func() time.Time {
			return time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
		},
		NewClient: func(ctx context.Context, plan MacPlan) (AWSClient, error) {
			return fake, nil
		},
	}
}

func managedTestTags() []AWSTagConfig {
	return []AWSTagConfig{
		{Key: "cm-managed", Value: "true"},
		{Key: "cm-profile", Value: "xcode-vnc"},
		{Key: "cm-account-email", Value: "user@example.com"},
	}
}

type fakeAWSClient struct {
	calls  []string
	status AWSStatus
	eip    ElasticIP
}

func (c *fakeAWSClient) CallerIdentity(ctx context.Context) (CallerIdentity, error) {
	return CallerIdentity{Account: "123456789012", ARN: "arn:aws:iam::123456789012:user/cm"}, nil
}

func (c *fakeAWSClient) DescribeStatus(ctx context.Context, plan MacPlan) (AWSStatus, error) {
	c.calls = append(c.calls, "status")
	return c.status, nil
}

func (c *fakeAWSClient) DescribeAdoptionCandidates(ctx context.Context, plan MacPlan) (AWSStatus, error) {
	c.calls = append(c.calls, "adoption")
	return c.status, nil
}

func (c *fakeAWSClient) TagResources(ctx context.Context, resourceIDs []string, tags []AWSTagConfig) error {
	c.calls = append(c.calls, "tag:"+strings.Join(resourceIDs, ","))
	return nil
}

func (c *fakeAWSClient) AllocateHost(ctx context.Context, plan MacPlan, availabilityZoneID, instanceType string) (string, error) {
	c.calls = append(c.calls, fmt.Sprintf("allocate:%s:%s", availabilityZoneID, instanceType))
	return "h-created", nil
}

func (c *fakeAWSClient) RunInstance(ctx context.Context, plan MacPlan, hostID, instanceType, amiID string) (string, error) {
	c.calls = append(c.calls, fmt.Sprintf("run:%s:%s:%s", hostID, instanceType, amiID))
	return "i-created", nil
}

func (c *fakeAWSClient) VerifyElasticIPOwner(ctx context.Context, plan MacPlan) (ElasticIP, error) {
	c.calls = append(c.calls, "verify-eip")
	if !hasTag(c.eip.Tags, plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value) {
		return ElasticIP{}, fmt.Errorf("elastic ip %s is missing required owner tag %s=%s", plan.ElasticIPAllocationID, plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value)
	}
	return c.eip, nil
}

func (c *fakeAWSClient) AssociateElasticIP(ctx context.Context, allocationID, instanceID string) (string, error) {
	c.calls = append(c.calls, fmt.Sprintf("associate:%s:%s", allocationID, instanceID))
	return "eipassoc-created", nil
}

func (c *fakeAWSClient) DisassociateElasticIP(ctx context.Context, associationID string) error {
	c.calls = append(c.calls, "disassociate:"+associationID)
	return nil
}

func (c *fakeAWSClient) TerminateInstance(ctx context.Context, instanceID string) error {
	c.calls = append(c.calls, "terminate:"+instanceID)
	return nil
}

func (c *fakeAWSClient) ReleaseHost(ctx context.Context, hostID string) error {
	c.calls = append(c.calls, "release:"+hostID)
	return nil
}
