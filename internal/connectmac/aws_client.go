package connectmac

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type AWSClient interface {
	CallerIdentity(ctx context.Context) (CallerIdentity, error)
	DescribeStatus(ctx context.Context, plan MacPlan) (AWSStatus, error)
	DescribeAdoptionCandidates(ctx context.Context, plan MacPlan) (AWSStatus, error)
	TagResources(ctx context.Context, resourceIDs []string, tags []AWSTagConfig) error
	AllocateHost(ctx context.Context, plan MacPlan, availabilityZoneID, instanceType string) (string, error)
	RunInstance(ctx context.Context, plan MacPlan, hostID, instanceType, amiID string) (string, error)
	VerifyElasticIPOwner(ctx context.Context, plan MacPlan) (ElasticIP, error)
	AssociateElasticIP(ctx context.Context, allocationID, instanceID string) (string, error)
	DisassociateElasticIP(ctx context.Context, associationID string) error
	TerminateInstance(ctx context.Context, instanceID string) error
	ReleaseHost(ctx context.Context, hostID string) error
}

type CallerIdentity struct {
	Account string
	ARN     string
	UserID  string
}

type AWSStatus struct {
	CallerIdentity CallerIdentity
	Hosts          []DedicatedHostStatus
	Instances      []InstanceStatus
	ElasticIP      ElasticIP
}

type DedicatedHostStatus struct {
	HostID       string
	State        string
	InstanceType string
	ZoneID       string
	Tags         []AWSTagConfig
}

type InstanceStatus struct {
	InstanceID   string
	State        string
	InstanceType string
	HostID       string
	PublicIP     string
	Tags         []AWSTagConfig
}

type ElasticIP struct {
	AllocationID  string
	AssociationID string
	InstanceID    string
	PublicIP      string
	Tags          []AWSTagConfig
}

type RealAWSClient struct {
	ec2 *ec2.Client
	sts *sts.Client
}

func NewRealAWSClient(ctx context.Context, plan MacPlan) (RealAWSClient, error) {
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(plan.Region),
	}
	if plan.AWSProfile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(plan.AWSProfile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return RealAWSClient{}, fmt.Errorf("load aws config: %w", err)
	}
	return RealAWSClient{
		ec2: ec2.NewFromConfig(cfg),
		sts: sts.NewFromConfig(cfg),
	}, nil
}

func (c RealAWSClient) CallerIdentity(ctx context.Context) (CallerIdentity, error) {
	out, err := c.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return CallerIdentity{}, fmt.Errorf("sts get caller identity: %w", err)
	}
	return CallerIdentity{
		Account: aws.ToString(out.Account),
		ARN:     aws.ToString(out.Arn),
		UserID:  aws.ToString(out.UserId),
	}, nil
}

func (c RealAWSClient) DescribeStatus(ctx context.Context, plan MacPlan) (AWSStatus, error) {
	identity, err := c.CallerIdentity(ctx)
	if err != nil {
		return AWSStatus{}, err
	}
	hosts, err := c.describeManagedHosts(ctx, plan)
	if err != nil {
		return AWSStatus{}, err
	}
	instances, err := c.describeManagedInstances(ctx, plan)
	if err != nil {
		return AWSStatus{}, err
	}
	eip, err := c.describeElasticIP(ctx, plan.ElasticIPAllocationID)
	if err != nil {
		return AWSStatus{}, err
	}
	return AWSStatus{
		CallerIdentity: identity,
		Hosts:          hosts,
		Instances:      instances,
		ElasticIP:      eip,
	}, nil
}

func (c RealAWSClient) DescribeAdoptionCandidates(ctx context.Context, plan MacPlan) (AWSStatus, error) {
	identity, err := c.CallerIdentity(ctx)
	if err != nil {
		return AWSStatus{}, err
	}
	hosts, err := c.describeHostsByName(ctx, plan.ResourceName)
	if err != nil {
		return AWSStatus{}, err
	}
	instances, err := c.describeInstancesByName(ctx, plan.ResourceName)
	if err != nil {
		return AWSStatus{}, err
	}
	eip, err := c.describeElasticIP(ctx, plan.ElasticIPAllocationID)
	if err != nil {
		return AWSStatus{}, err
	}
	return AWSStatus{
		CallerIdentity: identity,
		Hosts:          hosts,
		Instances:      instances,
		ElasticIP:      eip,
	}, nil
}

func (c RealAWSClient) TagResources(ctx context.Context, resourceIDs []string, tags []AWSTagConfig) error {
	if len(resourceIDs) == 0 {
		return nil
	}
	_, err := c.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: resourceIDs,
		Tags:      toEC2Tags(tags),
	})
	if err != nil {
		return fmt.Errorf("tag resources %v: %w", resourceIDs, err)
	}
	return nil
}

func (c RealAWSClient) AllocateHost(ctx context.Context, plan MacPlan, availabilityZoneID, instanceType string) (string, error) {
	out, err := c.ec2.AllocateHosts(ctx, &ec2.AllocateHostsInput{
		AutoPlacement:      ec2types.AutoPlacementOff,
		AvailabilityZoneId: aws.String(availabilityZoneID),
		HostMaintenance:    ec2types.HostMaintenanceOff,
		InstanceType:       aws.String(instanceType),
		Quantity:           aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeDedicatedHost,
			Tags:         toEC2Tags(plan.Tags),
		}},
	})
	if err != nil {
		return "", fmt.Errorf("allocate host %s in %s: %w", instanceType, availabilityZoneID, err)
	}
	if len(out.HostIds) == 0 {
		return "", fmt.Errorf("allocate host returned no host id")
	}
	return out.HostIds[0], nil
}

func (c RealAWSClient) RunInstance(ctx context.Context, plan MacPlan, hostID, instanceType, amiID string) (string, error) {
	out, err := c.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String(amiID),
		InstanceType: ec2types.InstanceType(instanceType),
		KeyName:      aws.String(plan.KeyName),
		MaxCount:     aws.Int32(1),
		MinCount:     aws.Int32(1),
		NetworkInterfaces: []ec2types.InstanceNetworkInterfaceSpecification{{
			AssociatePublicIpAddress: aws.Bool(false),
			DeviceIndex:              aws.Int32(0),
			Groups:                   []string{plan.SecurityGroupID},
			SubnetId:                 aws.String(plan.SubnetID),
		}},
		Placement: &ec2types.Placement{
			Affinity: aws.String("host"),
			HostId:   aws.String(hostID),
			Tenancy:  ec2types.TenancyHost,
		},
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         toEC2Tags(plan.Tags),
		}},
	})
	if err != nil {
		return "", fmt.Errorf("run instance on host %s: %w", hostID, err)
	}
	if len(out.Instances) == 0 {
		return "", fmt.Errorf("run instances returned no instance")
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

func (c RealAWSClient) VerifyElasticIPOwner(ctx context.Context, plan MacPlan) (ElasticIP, error) {
	eip, err := c.describeElasticIP(ctx, plan.ElasticIPAllocationID)
	if err != nil {
		return ElasticIP{}, err
	}
	if !hasTag(eip.Tags, plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value) {
		return ElasticIP{}, fmt.Errorf("elastic ip %s is missing required owner tag %s=%s", plan.ElasticIPAllocationID, plan.ElasticIPOwnerTag.Key, plan.ElasticIPOwnerTag.Value)
	}
	return eip, nil
}

func (c RealAWSClient) AssociateElasticIP(ctx context.Context, allocationID, instanceID string) (string, error) {
	out, err := c.ec2.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: aws.String(allocationID),
		InstanceId:   aws.String(instanceID),
	})
	if err != nil {
		return "", fmt.Errorf("associate elastic ip %s to %s: %w", allocationID, instanceID, err)
	}
	return aws.ToString(out.AssociationId), nil
}

func (c RealAWSClient) DisassociateElasticIP(ctx context.Context, associationID string) error {
	_, err := c.ec2.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{
		AssociationId: aws.String(associationID),
	})
	if err != nil {
		return fmt.Errorf("disassociate elastic ip association %s: %w", associationID, err)
	}
	return nil
}

func (c RealAWSClient) TerminateInstance(ctx context.Context, instanceID string) error {
	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}
	_, err := c.ec2.TerminateInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("terminate instance %s: %w", instanceID, err)
	}
	waiter := ec2.NewInstanceTerminatedWaiter(c.ec2)
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}}, 15*time.Minute); err != nil {
		return fmt.Errorf("wait for instance %s termination: %w", instanceID, err)
	}
	return nil
}

func (c RealAWSClient) ReleaseHost(ctx context.Context, hostID string) error {
	_, err := c.ec2.ReleaseHosts(ctx, &ec2.ReleaseHostsInput{
		HostIds: []string{hostID},
	})
	if err != nil {
		return fmt.Errorf("release host %s: %w", hostID, err)
	}
	return nil
}

func (c RealAWSClient) describeManagedHosts(ctx context.Context, plan MacPlan) ([]DedicatedHostStatus, error) {
	out, err := c.ec2.DescribeHosts(ctx, &ec2.DescribeHostsInput{
		Filter: managedFilters(plan),
	})
	if err != nil {
		return nil, fmt.Errorf("describe hosts: %w", err)
	}
	hosts := make([]DedicatedHostStatus, 0, len(out.Hosts))
	for _, host := range out.Hosts {
		hosts = append(hosts, DedicatedHostStatus{
			HostID:       aws.ToString(host.HostId),
			State:        string(host.State),
			InstanceType: aws.ToString(host.HostProperties.InstanceType),
			ZoneID:       aws.ToString(host.AvailabilityZoneId),
			Tags:         fromEC2Tags(host.Tags),
		})
	}
	return hosts, nil
}

func (c RealAWSClient) describeHostsByName(ctx context.Context, name string) ([]DedicatedHostStatus, error) {
	out, err := c.ec2.DescribeHosts(ctx, &ec2.DescribeHostsInput{
		Filter: []ec2types.Filter{{Name: aws.String("tag:Name"), Values: []string{name}}},
	})
	if err != nil {
		return nil, fmt.Errorf("describe hosts by name: %w", err)
	}
	hosts := make([]DedicatedHostStatus, 0, len(out.Hosts))
	for _, host := range out.Hosts {
		hosts = append(hosts, DedicatedHostStatus{
			HostID:       aws.ToString(host.HostId),
			State:        string(host.State),
			InstanceType: aws.ToString(host.HostProperties.InstanceType),
			ZoneID:       aws.ToString(host.AvailabilityZoneId),
			Tags:         fromEC2Tags(host.Tags),
		})
	}
	return hosts, nil
}

func (c RealAWSClient) describeManagedInstances(ctx context.Context, plan MacPlan) ([]InstanceStatus, error) {
	out, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: managedFilters(plan),
	})
	if err != nil {
		return nil, fmt.Errorf("describe instances: %w", err)
	}
	var instances []InstanceStatus
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			instances = append(instances, InstanceStatus{
				InstanceID:   aws.ToString(instance.InstanceId),
				State:        string(instance.State.Name),
				InstanceType: string(instance.InstanceType),
				HostID:       aws.ToString(instance.Placement.HostId),
				PublicIP:     aws.ToString(instance.PublicIpAddress),
				Tags:         fromEC2Tags(instance.Tags),
			})
		}
	}
	return instances, nil
}

func (c RealAWSClient) describeInstancesByName(ctx context.Context, name string) ([]InstanceStatus, error) {
	out, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:Name"), Values: []string{name}}},
	})
	if err != nil {
		return nil, fmt.Errorf("describe instances by name: %w", err)
	}
	var instances []InstanceStatus
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			instances = append(instances, InstanceStatus{
				InstanceID:   aws.ToString(instance.InstanceId),
				State:        string(instance.State.Name),
				InstanceType: string(instance.InstanceType),
				HostID:       aws.ToString(instance.Placement.HostId),
				PublicIP:     aws.ToString(instance.PublicIpAddress),
				Tags:         fromEC2Tags(instance.Tags),
			})
		}
	}
	return instances, nil
}

func (c RealAWSClient) describeElasticIP(ctx context.Context, allocationID string) (ElasticIP, error) {
	out, err := c.ec2.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		AllocationIds: []string{allocationID},
	})
	if err != nil {
		return ElasticIP{}, fmt.Errorf("describe elastic ip %s: %w", allocationID, err)
	}
	if len(out.Addresses) == 0 {
		return ElasticIP{}, fmt.Errorf("elastic ip %s not found", allocationID)
	}
	address := out.Addresses[0]
	return ElasticIP{
		AllocationID:  aws.ToString(address.AllocationId),
		AssociationID: aws.ToString(address.AssociationId),
		InstanceID:    aws.ToString(address.InstanceId),
		PublicIP:      aws.ToString(address.PublicIp),
		Tags:          fromEC2Tags(address.Tags),
	}, nil
}

func managedFilters(plan MacPlan) []ec2types.Filter {
	return []ec2types.Filter{
		{Name: aws.String("tag:cm-managed"), Values: []string{"true"}},
		{Name: aws.String("tag:cm-profile"), Values: []string{plan.ProfileName}},
		{Name: aws.String("tag:cm-account-email"), Values: []string{plan.AccountEmail}},
	}
}

func toEC2Tags(tags []AWSTagConfig) []ec2types.Tag {
	out := make([]ec2types.Tag, 0, len(tags))
	for _, tag := range tags {
		out = append(out, ec2types.Tag{Key: aws.String(tag.Key), Value: aws.String(tag.Value)})
	}
	return out
}

func fromEC2Tags(tags []ec2types.Tag) []AWSTagConfig {
	out := make([]AWSTagConfig, 0, len(tags))
	for _, tag := range tags {
		out = append(out, AWSTagConfig{Key: aws.ToString(tag.Key), Value: aws.ToString(tag.Value)})
	}
	return out
}
