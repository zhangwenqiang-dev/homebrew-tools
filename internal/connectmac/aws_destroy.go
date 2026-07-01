package connectmac

import (
	"context"
	"fmt"
	"time"
)

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
			return MacPlan{}, AWSDestroyResult{}, fmt.Errorf("refuse to terminate instance %s because required safety tags do not match: %s", instance.InstanceID, managedTagsMismatch(instance.Tags, plan))
		}
		if status.ElasticIP.AssociationID != "" && status.ElasticIP.InstanceID == instance.InstanceID {
			s.progress("Disassociating Elastic IP %s from instance %s", status.ElasticIP.AssociationID, instance.InstanceID)
			if err := client.DisassociateElasticIP(ctx, status.ElasticIP.AssociationID); err != nil {
				return MacPlan{}, result, err
			}
			result.DisassociatedElasticIP = true
		}
		s.progress("Terminating EC2 instance %s and waiting for AWS termination", instance.InstanceID)
		if err := client.TerminateInstance(ctx, instance.InstanceID); err != nil {
			return MacPlan{}, result, AWSDestroyPartialError{Result: result, Cause: err}
		}
		if err := s.waitInstanceTerminated(ctx, client, plan, instance.InstanceID); err != nil {
			return MacPlan{}, result, AWSDestroyPartialError{Result: result, Cause: err}
		}
		s.progress("EC2 instance %s is terminated", instance.InstanceID)
		result.TerminatedInstances = append(result.TerminatedInstances, instance.InstanceID)
	}
	for _, host := range status.Hosts {
		if isTerminalHostState(host.State) {
			result.SkippedHosts = append(result.SkippedHosts, fmt.Sprintf("%s:%s", host.HostID, host.State))
			continue
		}
		if !managedTagsMatch(host.Tags, plan) {
			return MacPlan{}, AWSDestroyResult{}, fmt.Errorf("refuse to release host %s because required safety tags do not match: %s", host.HostID, managedTagsMismatch(host.Tags, plan))
		}
		released, reason, err := s.releaseHostWithRetry(ctx, client, host)
		if err != nil {
			return MacPlan{}, result, AWSDestroyPartialError{Result: result, Cause: err}
		}
		if !released {
			result.DeferredHosts = append(result.DeferredHosts, AWSDeferredHost{
				HostID: host.HostID,
				State:  emptyStatus(host.State),
				Reason: reason,
			})
			continue
		}
		result.ReleasedHosts = append(result.ReleasedHosts, host.HostID)
	}
	return plan, result, nil
}
func (s AWSService) waitInstanceTerminated(ctx context.Context, client AWSClient, plan MacPlan, instanceID string) error {
	timeout := s.DestroyTimeout
	if timeout == 0 {
		timeout = 45 * time.Minute
	}
	interval := s.DestroyPollInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	start := time.Now()
	deadline := start.Add(timeout)
	for {
		status, err := client.DescribeStatus(ctx, plan)
		if err != nil {
			return err
		}
		instance, ok := findInstanceStatus(status, instanceID)
		if !ok || isTerminalInstanceState(instance.State) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for EC2 instance %s termination; last state=%s", instanceID, emptyStatus(instance.State))
		}
		s.progress("Waiting for EC2 termination: instance=%s state=%s elapsed=%s; retry in %s", instanceID, emptyStatus(instance.State), roundDuration(time.Since(start)), interval)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func findInstanceStatus(status AWSStatus, instanceID string) (InstanceStatus, bool) {
	for _, instance := range status.Instances {
		if instance.InstanceID == instanceID {
			return instance, true
		}
	}
	return InstanceStatus{}, false
}
func roundDuration(value time.Duration) time.Duration {
	if value < time.Second {
		return 0
	}
	return value.Round(time.Second)
}
func (s AWSService) releaseHostWithRetry(ctx context.Context, client AWSClient, host DedicatedHostStatus) (bool, string, error) {
	s.progress("Attempting to release Dedicated Host %s", host.HostID)
	err := client.ReleaseHost(ctx, host.HostID)
	if err == nil {
		s.progress("Dedicated Host %s is released", host.HostID)
		return true, "", nil
	}
	if emptyStatus(host.State) != "pending" {
		return false, "", err
	}
	timeout := s.DestroyTimeout
	if timeout == 0 {
		timeout = time.Hour
	}
	interval := s.DestroyPollInterval
	if interval == 0 {
		interval = time.Minute
	}
	deadline := time.Now().Add(timeout)
	lastErr := err
	for time.Now().Before(deadline) {
		s.progress("Dedicated Host %s is pending; retry release in %s", host.HostID, interval)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, fmt.Sprintf("release was attempted after EC2 termination but context ended while AWS Mac host transition was still in progress: %v", ctx.Err()), nil
		case <-timer.C:
		}
		err = client.ReleaseHost(ctx, host.HostID)
		if err == nil {
			s.progress("Dedicated Host %s is released", host.HostID)
			return true, "", nil
		}
		lastErr = err
	}
	return false, fmt.Sprintf("release was attempted after EC2 termination but AWS Mac host transition was still in progress after %s: %v", timeout, lastErr), nil
}
