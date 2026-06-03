package connectmac

import (
	"fmt"
	"strings"
	"time"
)

type AWSService struct {
	Now func() time.Time
}

func NewAWSService() AWSService {
	return AWSService{Now: time.Now}
}

func (s AWSService) Plan(profile Profile) (MacPlan, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return BuildMacPlan(profile, now)
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

func FormatMacStatusPreview(plan MacPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac status target for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Resource name: %s\n", plan.ResourceName)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Lookup tags: %s\n", FormatAWSTags(plan.Tags))
	fmt.Fprintln(&b, "Status lookup will describe matching Dedicated Hosts, EC2 instances, and Elastic IP association.")
	return b.String()
}

func FormatAWSTags(tags []AWSTagConfig) string {
	parts := make([]string, 0, len(tags))
	for _, tag := range tags {
		parts = append(parts, fmt.Sprintf("%s=%s", tag.Key, tag.Value))
	}
	return strings.Join(parts, ", ")
}
