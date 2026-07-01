package connectmac

import (
	"context"
	"fmt"
	"sort"
)

func (s AWSService) Capacity(ctx context.Context, profile Profile) (MacPlan, AWSCapacity, error) {
	plan, err := s.Plan(profile)
	if err != nil {
		return MacPlan{}, AWSCapacity{}, err
	}
	client, err := s.client(ctx, plan)
	if err != nil {
		return MacPlan{}, AWSCapacity{}, err
	}
	identity, err := client.CallerIdentity(ctx)
	if err != nil {
		return MacPlan{}, AWSCapacity{}, err
	}
	quotas, err := client.DedicatedHostQuotas(ctx, plan.InstanceTypePriority)
	if err != nil {
		return MacPlan{}, AWSCapacity{}, err
	}
	hosts, err := client.DescribeAllHosts(ctx)
	if err != nil {
		return MacPlan{}, AWSCapacity{}, err
	}
	inUse := dedicatedHostUsageByInstanceType(hosts)
	items := make([]AWSCapacityItem, 0, len(plan.InstanceTypePriority))
	for _, instanceType := range plan.InstanceTypePriority {
		offerings, err := client.InstanceTypeOfferings(ctx, instanceType)
		if err != nil {
			offerings = []string{fmt.Sprintf("error: %v", err)}
		}
		sort.Strings(offerings)
		quota := quotas[instanceType]
		used := inUse[instanceType]
		available := quota - float64(used)
		if available < 0 {
			available = 0
		}
		items = append(items, AWSCapacityItem{
			InstanceType: instanceType,
			QuotaName:    dedicatedHostQuotaName(instanceType),
			Quota:        quota,
			InUse:        used,
			Available:    available,
			OfferingAZs:  offerings,
		})
	}
	return plan, AWSCapacity{CallerIdentity: identity, Items: items}, nil
}
func dedicatedHostUsageByInstanceType(hosts []DedicatedHostStatus) map[string]int {
	counts := map[string]int{}
	for _, host := range hosts {
		if host.InstanceType == "" || isTerminalHostState(host.State) {
			continue
		}
		counts[host.InstanceType]++
	}
	return counts
}
