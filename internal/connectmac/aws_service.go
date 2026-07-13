package connectmac

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type AWSService struct {
	Now                 func() time.Time
	NewClient           func(ctx context.Context, plan MacPlan) (AWSClient, error)
	Progress            func(message string)
	ReadyPollInterval   time.Duration
	ReadyTimeout        time.Duration
	DestroyPollInterval time.Duration
	DestroyTimeout      time.Duration
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

func (s AWSService) progress(format string, args ...interface{}) {
	if s.Progress == nil {
		return
	}
	s.Progress(fmt.Sprintf(format, args...))
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
	AMI                string
	SubnetID           string
	Status             string
	Detail             string
}

type AWSCreateAttemptsError struct {
	Attempts   []AWSCreateAttempt
	Stage      string
	HostID     string
	InstanceID string
	Cause      error
}

func (e AWSCreateAttemptsError) Error() string {
	var b strings.Builder
	if e.Cause != nil {
		fmt.Fprintf(&b, "%v\n", e.Cause)
	}
	if e.Stage != "" {
		fmt.Fprintf(&b, "Failed stage: %s\n", e.Stage)
	}
	if e.HostID != "" || e.InstanceID != "" {
		fmt.Fprintf(&b, "Created resources: host=%s instance=%s\n", emptyTableValue(e.HostID), emptyTableValue(e.InstanceID))
	}
	if len(e.Attempts) > 0 {
		fmt.Fprint(&b, FormatAWSCreateAttempts(e.Attempts))
	}
	if e.Stage != "" {
		fmt.Fprintln(&b, "Next action: inspect the created resources with cm aws status --all; do not retry another instance type or terminate EC2 unless the user explicitly requests destroy.")
	}
	return strings.TrimSpace(b.String())
}

type AWSDestroyResult struct {
	DisassociatedElasticIP bool
	TerminatedInstances    []string
	ReleasedHosts          []string
	SkippedInstances       []string
	SkippedHosts           []string
	DeferredHosts          []AWSDeferredHost
	RetainedElasticIP      ElasticIP
}

type AWSDeferredHost struct {
	HostID string
	State  string
	Reason string
}

type AWSDestroyPartialError struct {
	Result AWSDestroyResult
	Cause  error
}

type AWSSafetyError struct{ Cause error }

func (e AWSSafetyError) Error() string {
	if e.Cause == nil {
		return "AWS resource safety validation failed"
	}
	return e.Cause.Error()
}

func (e AWSSafetyError) Unwrap() error { return e.Cause }

func (e AWSDestroyPartialError) Error() string {
	if e.Cause == nil {
		return "aws destroy partially completed"
	}
	return fmt.Sprintf("%v; partial destroy state recorded; do not release Elastic IP; run the same destroy command again after AWS finishes the pending transition", e.Cause)
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

type AWSCapacity struct {
	CallerIdentity CallerIdentity
	Items          []AWSCapacityItem
}

type AWSCapacityItem struct {
	InstanceType string
	QuotaName    string
	Quota        float64
	InUse        int
	Available    float64
	OfferingAZs  []string
}

type AWSOpenDecision struct {
	Kind   string
	HostID string
	Detail string
}
