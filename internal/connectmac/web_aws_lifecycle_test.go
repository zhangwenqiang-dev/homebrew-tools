package connectmac

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebAWSLifecycleStateTransitions(t *testing.T) {
	ready := AWSStatus{
		Hosts: []DedicatedHostStatus{{
			HostID:    "h-1",
			State:     "available",
			CreatedAt: "2026-07-16T01:00:00Z",
		}},
		Instances: []InstanceStatus{{
			InstanceID:          "i-1",
			State:               "running",
			SystemStatus:        "ok",
			InstanceStatusCheck: "ok",
			EBSStatus:           "ok",
		}},
		ElasticIP: ElasticIP{InstanceID: "i-1"},
	}
	resources := AWSStatus{
		Hosts:     []DedicatedHostStatus{{HostID: "h-1", State: "pending"}},
		Instances: []InstanceStatus{{InstanceID: "i-1", State: "stopping"}},
		ElasticIP: ElasticIP{AllocationID: "eipalloc-1", InstanceID: "i-1"},
	}
	stopped := AWSStatus{ElasticIP: ElasticIP{AllocationID: "eipalloc-1", PublicIP: "203.0.113.10"}}

	tests := []struct {
		name         string
		command      string
		jobStatus    JobStatus
		awsStatus    AWSStatus
		seedOwner    bool
		seedReminder bool
		wantState    JobLifecycleState
		wantOwner    string
		wantReleased bool
		wantNotify   string
	}{
		{name: "open running remains pending", command: "open", jobStatus: JobStatusRunning, wantState: JobLifecyclePending},
		{name: "open success waits for ready", command: "open", jobStatus: JobStatusSuccess, awsStatus: resources, wantState: JobLifecycleWaiting},
		{name: "open success ready finalizes", command: "open", jobStatus: JobStatusSuccess, awsStatus: ready, wantState: JobLifecycleFinalized, wantOwner: "owner@example.com", wantNotify: "open"},
		{name: "failed open fails without success notification", command: "open", jobStatus: JobStatusFailed, wantState: JobLifecycleFailed},
		{name: "interrupted open fails without success notification", command: "open", jobStatus: JobStatusInterrupted, wantState: JobLifecycleFailed},
		{name: "destroy success waits for resources", command: "destroy", jobStatus: JobStatusSuccess, awsStatus: resources, seedOwner: true, seedReminder: true, wantState: JobLifecycleWaiting, wantOwner: "owner@example.com"},
		{name: "destroy deferred waits for resources", command: "destroy", jobStatus: JobStatusDeferred, awsStatus: resources, seedOwner: true, seedReminder: true, wantState: JobLifecycleWaiting, wantOwner: "owner@example.com"},
		{name: "destroy stopped finalizes", command: "destroy", jobStatus: JobStatusSuccess, awsStatus: stopped, seedOwner: true, seedReminder: true, wantState: JobLifecycleFinalized, wantReleased: true, wantNotify: "release"},
		{name: "destroy deferred stopped finalizes", command: "destroy", jobStatus: JobStatusDeferred, awsStatus: stopped, seedOwner: true, seedReminder: true, wantState: JobLifecycleFinalized, wantReleased: true, wantNotify: "release"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, configPath := newWebAWSLifecycleTestApp(t)
			if tt.seedOwner {
				seedWebAWSLifecycleOwner(t, &app)
			}
			if tt.seedReminder {
				seedWebAWSLifecycleReminder(t, &app)
			}
			app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
				return &fakeAWSClient{status: tt.awsStatus}, nil
			}
			var notifications []string
			app.WebAWSLifecycleNotifier = func(event string, _ ReleaseReminder, _, _ string) error {
				notifications = append(notifications, event)
				return nil
			}
			job := createWebAWSLifecycleJob(t, &app, tt.command, tt.jobStatus)

			if err := app.reconcileWebAWSLifecycleJob(context.Background(), configPath, job); err != nil {
				t.Fatalf("reconcile lifecycle: %v", err)
			}
			got, err := app.JobManager.Load(job.ID)
			if err != nil {
				t.Fatalf("load job: %v", err)
			}
			if got.LifecycleState != tt.wantState {
				t.Fatalf("lifecycle state = %q, want %q; job=%+v", got.LifecycleState, tt.wantState, got)
			}
			owner, ownerOK, err := app.MemberStore.ProfileOwner("xcode-vnc")
			if err != nil {
				t.Fatalf("profile owner: %v", err)
			}
			if tt.wantOwner == "" {
				if ownerOK {
					t.Fatalf("unexpected owner: %+v", owner)
				}
			} else if !ownerOK || owner.Owner.Email != tt.wantOwner {
				t.Fatalf("owner = %+v ok=%t, want %q", owner, ownerOK, tt.wantOwner)
			}
			reminder, reminderOK, err := app.MemberStore.ReleaseReminder("xcode-vnc")
			if err != nil {
				t.Fatalf("release reminder: %v", err)
			}
			if tt.wantReleased && (!reminderOK || reminder.Status != ReleaseReminderStatusReleased) {
				t.Fatalf("reminder = %+v ok=%t, want released", reminder, reminderOK)
			}
			if tt.wantNotify == "" {
				if len(notifications) != 0 {
					t.Fatalf("notifications = %+v, want none", notifications)
				}
			} else if len(notifications) != 1 || notifications[0] != tt.wantNotify {
				t.Fatalf("notifications = %+v, want [%s]", notifications, tt.wantNotify)
			}
		})
	}
}

func TestWebAWSLifecycleRepeatedScanIsIdempotent(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: AWSStatus{
			Hosts: []DedicatedHostStatus{{HostID: "h-1", State: "available", CreatedAt: "2026-07-16T01:00:00Z"}},
			Instances: []InstanceStatus{{
				InstanceID: "i-1", State: "running", SystemStatus: "ok", InstanceStatusCheck: "ok",
			}},
			ElasticIP: ElasticIP{InstanceID: "i-1"},
		}}, nil
	}
	notifications := 0
	app.WebAWSLifecycleNotifier = func(string, ReleaseReminder, string, string) error {
		notifications++
		return nil
	}
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)

	if err := app.reconcileWebAWSLifecycles(context.Background(), configPath); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	firstReminder, ok, err := app.MemberStore.ReleaseReminder("xcode-vnc")
	if err != nil || !ok {
		t.Fatalf("first reminder: %+v ok=%t err=%v", firstReminder, ok, err)
	}
	if err := app.reconcileWebAWSLifecycles(context.Background(), configPath); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	secondReminder, ok, err := app.MemberStore.ReleaseReminder("xcode-vnc")
	if err != nil || !ok {
		t.Fatalf("second reminder: %+v ok=%t err=%v", secondReminder, ok, err)
	}
	got, err := app.JobManager.Load(job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if notifications != 1 {
		t.Fatalf("notifications = %d, want 1", notifications)
	}
	if got.LifecycleState != JobLifecycleFinalized || got.LifecycleNotifiedAt.IsZero() {
		t.Fatalf("job not finalized/notified: %+v", got)
	}
	if firstReminder.CreatedAt != secondReminder.CreatedAt || firstReminder.ReleaseDueAt != secondReminder.ReleaseDueAt {
		t.Fatalf("reminder changed across repeated scan: first=%+v second=%+v", firstReminder, secondReminder)
	}
}

func TestWebAWSLifecycleConcurrentScansClaimNotificationAtMostOnce(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: readyWebAWSLifecycleStatus()}, nil
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	notifications := 0
	var mu sync.Mutex
	app.WebAWSLifecycleNotifier = func(string, ReleaseReminder, string, string) error {
		mu.Lock()
		notifications++
		mu.Unlock()
		once.Do(func() { close(entered) })
		<-release
		return nil
	}
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)

	results := make(chan error, 2)
	go func() { results <- app.reconcileWebAWSLifecycles(context.Background(), configPath) }()
	<-entered
	go func() { results <- app.reconcileWebAWSLifecycles(context.Background(), configPath) }()
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent scan: %v", err)
		}
	}
	mu.Lock()
	gotNotifications := notifications
	mu.Unlock()
	if gotNotifications != 1 {
		t.Fatalf("notifications = %d, want exactly 1", gotNotifications)
	}
	got, err := app.JobManager.Load(job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if got.LifecycleNotifyClaimedAt.IsZero() || got.LifecycleNotifiedAt.IsZero() {
		t.Fatalf("notification claim/success not persisted: %+v", got)
	}
}

func TestWebAWSLifecycleNotificationFailureClearsClaimForRetry(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
		return &fakeAWSClient{status: readyWebAWSLifecycleStatus()}, nil
	}
	attempts := 0
	app.WebAWSLifecycleNotifier = func(string, ReleaseReminder, string, string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary webhook failure")
		}
		return nil
	}
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)

	if err := app.reconcileWebAWSLifecycles(context.Background(), configPath); err == nil {
		t.Fatal("first scan should return notification failure")
	}
	failed, err := app.JobManager.Load(job.ID)
	if err != nil {
		t.Fatalf("load failed job: %v", err)
	}
	if !failed.LifecycleNotifyClaimedAt.IsZero() || !failed.LifecycleNotifiedAt.IsZero() {
		t.Fatalf("returned failure must clear claim for retry: %+v", failed)
	}
	if err := app.reconcileWebAWSLifecycles(context.Background(), configPath); err != nil {
		t.Fatalf("retry scan: %v", err)
	}
	succeeded, err := app.JobManager.Load(job.ID)
	if err != nil {
		t.Fatalf("load succeeded job: %v", err)
	}
	if attempts != 2 || succeeded.LifecycleNotifiedAt.IsZero() {
		t.Fatalf("attempts=%d job=%+v, want successful retry", attempts, succeeded)
	}
}

func TestWebAWSLifecyclePersistedNotificationClaimPreventsRedelivery(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)
	seedWebAWSLifecycleOwner(t, &app)
	seedWebAWSLifecycleReminder(t, &app)
	claimTime := time.Date(2026, 7, 16, 2, 0, 0, 0, time.UTC)
	if _, err := app.JobManager.Update(job.ID, func(current Job) (Job, error) {
		current.LifecycleState = JobLifecycleFinalized
		current.LifecycleFinalizedAt = claimTime
		current.LifecycleNotifyClaimedAt = claimTime
		return current, nil
	}); err != nil {
		t.Fatalf("persist notification claim: %v", err)
	}
	notifications := 0
	app.WebAWSLifecycleNotifier = func(string, ReleaseReminder, string, string) error {
		notifications++
		return nil
	}

	if err := app.reconcileWebAWSLifecycles(context.Background(), configPath); err != nil {
		t.Fatalf("scan claimed notification: %v", err)
	}
	got, err := app.JobManager.Load(job.ID)
	if err != nil {
		t.Fatalf("load claimed job: %v", err)
	}
	if notifications != 0 || got.LifecycleNotifyClaimedAt != claimTime || !got.LifecycleNotifiedAt.IsZero() {
		t.Fatalf("persisted claim must suppress redelivery after uncertain delivery: notifications=%d job=%+v", notifications, got)
	}
}

func TestWebAWSLifecycleStaleJobCannotMutateAfterNewerLifecycle(t *testing.T) {
	tests := []struct {
		name          string
		olderCommand  string
		newerCommand  string
		seedLifecycle bool
	}{
		{name: "older open after newer destroy", olderCommand: "open", newerCommand: "destroy"},
		{name: "older destroy after newer open", olderCommand: "destroy", newerCommand: "open", seedLifecycle: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, configPath := newWebAWSLifecycleTestApp(t)
			if tt.seedLifecycle {
				seedWebAWSLifecycleOwner(t, &app)
				seedWebAWSLifecycleReminder(t, &app)
			}
			statusCalls := 0
			app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
				statusCalls++
				if tt.olderCommand == "open" {
					return &fakeAWSClient{status: readyWebAWSLifecycleStatus()}, nil
				}
				return &fakeAWSClient{status: AWSStatus{}}, nil
			}
			older := createWebAWSLifecycleJob(t, &app, tt.olderCommand, JobStatusSuccess)
			newer := createWebAWSLifecycleJob(t, &app, tt.newerCommand, JobStatusRunning)
			if _, err := app.JobManager.Update(newer.ID, func(current Job) (Job, error) {
				current.StartedAt = older.StartedAt.Add(time.Minute)
				return current, nil
			}); err != nil {
				t.Fatalf("make newer job: %v", err)
			}

			if err := app.reconcileWebAWSLifecycleJob(context.Background(), configPath, older); err != nil {
				t.Fatalf("reconcile stale job: %v", err)
			}
			if statusCalls != 0 {
				t.Fatalf("stale job made %d AWS status calls, want 0", statusCalls)
			}
			_, ownerOK, err := app.MemberStore.ProfileOwner("xcode-vnc")
			if err != nil {
				t.Fatalf("profile owner: %v", err)
			}
			if ownerOK != tt.seedLifecycle {
				t.Fatalf("owner presence=%t, want %t", ownerOK, tt.seedLifecycle)
			}
			reminder, reminderOK, err := app.MemberStore.ReleaseReminder("xcode-vnc")
			if err != nil {
				t.Fatalf("release reminder: %v", err)
			}
			if reminderOK != tt.seedLifecycle || (reminderOK && reminder.Status != ReleaseReminderStatusActive) {
				t.Fatalf("stale job mutated reminder: %+v ok=%t", reminder, reminderOK)
			}
		})
	}
}

func TestWebAWSLifecycleProfileResolutionKeepsLocalExactCandidateOnManagedCollision(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	managed := cfg.Profiles["xcode-vnc"]
	managed.AWS.AccountEmail = "other@example.com"
	if _, err := app.MemberStore.UpsertManagedProfile(managed); err != nil {
		t.Fatalf("seed colliding managed profile: %v", err)
	}
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)

	resolved, err := app.resolveWebAWSLifecycleProfile(configPath, job)
	if err != nil {
		t.Fatalf("resolve exact local candidate: %v", err)
	}
	if resolved.Name != job.Profile || normalizeEmail(resolved.AWS.AccountEmail) != normalizeEmail(job.AppleEmail) {
		t.Fatalf("resolved mismatched candidate: %+v", resolved)
	}
}

func TestWebAWSLifecycleProductionNotifierReturnsRedactedFailure(t *testing.T) {
	const secret = "super-secret-webhook-key"
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader(`{"errcode":1,"errmsg":"failed"}`)),
			Header:     make(http.Header),
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	t.Setenv(envWechatWebhookURL, "https://example.invalid/webhook?key="+secret)
	app, _ := newWebAWSLifecycleTestApp(t)

	err := app.notifyWebAWSLifecycle("open", ReleaseReminder{ProfileName: "xcode-vnc", AppleEmail: "user@example.com"}, "", "open")
	if err == nil {
		t.Fatal("production notifier should return send failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("notifier error was not consistently redacted: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestWebAWSLifecycleFinalizedNotificationRetryDoesNotReadAWSAgain(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)
	seedWebAWSLifecycleOwner(t, &app)
	seedWebAWSLifecycleReminder(t, &app)
	if _, err := app.JobManager.Update(job.ID, func(current Job) (Job, error) {
		current.LifecycleState = JobLifecycleFinalized
		current.LifecycleFinalizedAt = time.Now()
		return current, nil
	}); err != nil {
		t.Fatalf("finalize job: %v", err)
	}
	statusCalls := 0
	app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
		statusCalls++
		return nil, errors.New("status should not be called")
	}
	notifications := 0
	app.WebAWSLifecycleNotifier = func(string, ReleaseReminder, string, string) error {
		notifications++
		return nil
	}

	if err := app.reconcileWebAWSLifecycles(context.Background(), configPath); err != nil {
		t.Fatalf("retry notification: %v", err)
	}
	if statusCalls != 0 || notifications != 1 {
		t.Fatalf("status calls=%d notifications=%d, want 0 and 1", statusCalls, notifications)
	}
}

func TestWebAWSLifecycleStatusErrorRemainsRetryable(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
		return nil, errors.New("temporary status failure")
	}
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)

	if err := app.reconcileWebAWSLifecycleJob(context.Background(), configPath, job); err == nil {
		t.Fatal("expected status error")
	}
	got, err := app.JobManager.Load(job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if got.LifecycleState != JobLifecycleWaiting || !strings.Contains(got.LifecycleError, "temporary status failure") {
		t.Fatalf("retryable job = %+v", got)
	}
	if !got.LifecycleFinalizedAt.IsZero() || !got.LifecycleNotifiedAt.IsZero() {
		t.Fatalf("status error finalized lifecycle: %+v", got)
	}
}

func TestWebAWSLifecycleDoesNotInferAnotherAccount(t *testing.T) {
	app, configPath := newWebAWSLifecycleTestApp(t)
	job := createWebAWSLifecycleJob(t, &app, "open", JobStatusSuccess)
	job.AppleEmail = "other@example.com"
	if _, err := app.JobManager.Update(job.ID, func(current Job) (Job, error) {
		current.AppleEmail = job.AppleEmail
		return current, nil
	}); err != nil {
		t.Fatalf("update job account: %v", err)
	}
	statusCalls := 0
	app.AWSService.NewClient = func(context.Context, MacPlan) (AWSClient, error) {
		statusCalls++
		return &fakeAWSClient{}, nil
	}

	err := app.reconcileWebAWSLifecycleJob(context.Background(), configPath, job)
	if err == nil || !strings.Contains(err.Error(), "other@example.com") {
		t.Fatalf("account mismatch error = %v", err)
	}
	if statusCalls != 0 {
		t.Fatalf("AWS status calls = %d, want 0", statusCalls)
	}
	got, loadErr := app.JobManager.Load(job.ID)
	if loadErr != nil {
		t.Fatalf("load job: %v", loadErr)
	}
	if got.LifecycleState != JobLifecycleWaiting || got.LifecycleError == "" {
		t.Fatalf("mismatched account job = %+v", got)
	}
}

func newWebAWSLifecycleTestApp(t *testing.T) (App, string) {
	t.Helper()
	dir := t.TempDir()
	key := writeSSHKey(t, 0o600)
	configPath := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	if _, err := app.MemberStore.AddMember("Owner", "owner@example.com", "operator"); err != nil {
		t.Fatalf("add lifecycle owner: %v", err)
	}
	return app, configPath
}

func createWebAWSLifecycleJob(t *testing.T, app *App, command string, status JobStatus) Job {
	t.Helper()
	job, err := app.JobManager.Create(Job{
		Type:                "aws-" + command,
		Profile:             "xcode-vnc",
		AppleEmail:          "user@example.com",
		Status:              status,
		StartedAt:           time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC),
		LifecycleOwnerEmail: "owner@example.com",
		LifecycleState:      JobLifecyclePending,
	})
	if err != nil {
		t.Fatalf("create lifecycle job: %v", err)
	}
	return job
}

func seedWebAWSLifecycleOwner(t *testing.T, app *App) {
	t.Helper()
	if _, err := app.MemberStore.AssignMember("user@example.com", "owner@example.com", "owner"); err != nil {
		t.Fatalf("assign lifecycle owner: %v", err)
	}
	if _, err := app.MemberStore.SetProfileOwner("xcode-vnc", "owner@example.com"); err != nil {
		t.Fatalf("set lifecycle owner: %v", err)
	}
}

func seedWebAWSLifecycleReminder(t *testing.T, app *App) {
	t.Helper()
	if _, err := app.MemberStore.UpsertReleaseReminder(ReleaseReminder{
		ProfileName:   "xcode-vnc",
		AppleEmail:    "user@example.com",
		HostID:        "h-1",
		HostCreatedAt: "2026-07-16T01:00:00Z",
		ReleaseDueAt:  "2026-07-17T01:00:00Z",
		OwnerEmail:    "owner@example.com",
		OwnerName:     "Owner",
		Status:        ReleaseReminderStatusActive,
	}); err != nil {
		t.Fatalf("seed lifecycle reminder: %v", err)
	}
}

func readyWebAWSLifecycleStatus() AWSStatus {
	return AWSStatus{
		Hosts: []DedicatedHostStatus{{HostID: "h-1", State: "available", CreatedAt: "2026-07-16T01:00:00Z"}},
		Instances: []InstanceStatus{{
			InstanceID: "i-1", State: "running", SystemStatus: "ok", InstanceStatusCheck: "ok", EBSStatus: "ok",
		}},
		ElasticIP: ElasticIP{InstanceID: "i-1"},
	}
}
