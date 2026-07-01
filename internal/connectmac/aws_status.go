package connectmac

import (
	"context"
	"fmt"
	"strings"
	"time"
)

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
		s.progress("Waiting for AWS readiness: %s; retry in %s", AWSReadinessSummary(status), interval)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return MacPlan{}, last, ctx.Err()
		case <-timer.C:
		}
	}
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
		optionalEBSReady(instance.EBSStatus)
}
func optionalEBSReady(status string) bool {
	return status == "" || status == "ok"
}
func (s AWSService) waitInstanceRunning(ctx context.Context, client AWSClient, plan MacPlan, instanceID string) error {
	timeout := s.ReadyTimeout
	if timeout == 0 {
		timeout = 45 * time.Minute
	}
	interval := s.ReadyPollInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		status, err := client.DescribeStatus(ctx, plan)
		if err != nil {
			return err
		}
		for _, instance := range status.Instances {
			if instance.InstanceID != instanceID {
				continue
			}
			switch instance.State {
			case "running":
				return nil
			case "shutting-down", "terminated", "stopping", "stopped":
				return fmt.Errorf("instance %s entered %s before elastic ip association", instanceID, instance.State)
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for instance %s to become running", instanceID)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
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
func AWSOpenAction(status AWSStatus) AWSOpenDecision {
	if AWSStatusReady(status) {
		return AWSOpenDecision{Kind: "ready", Detail: "managed instance is already ready"}
	}
	for _, instance := range status.Instances {
		if !isTerminalInstanceState(instance.State) {
			return AWSOpenDecision{Kind: "wait-ready", Detail: fmt.Sprintf("managed instance %s is %s", instance.InstanceID, emptyStatus(instance.State))}
		}
	}
	for _, host := range status.Hosts {
		if host.State == "available" && len(host.InstanceIDs) == 0 {
			return AWSOpenDecision{Kind: "launch-on-host", HostID: host.HostID, Detail: fmt.Sprintf("launch on available host %s", host.HostID)}
		}
		if !isTerminalHostState(host.State) {
			return AWSOpenDecision{Kind: "blocked", Detail: fmt.Sprintf("host %s is %s", host.HostID, emptyStatus(host.State))}
		}
	}
	if status.ElasticIP.InstanceID != "" {
		return AWSOpenDecision{Kind: "blocked", Detail: fmt.Sprintf("elastic ip is associated with unmanaged instance %s", status.ElasticIP.InstanceID)}
	}
	return AWSOpenDecision{Kind: "create", Detail: "no active managed host or instance found"}
}
