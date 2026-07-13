package connectmac

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutoReleaseDueFlowSchedulesOnlyWhenEnabled(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		enabled bool
		state   string
		autoAt  string
	}{
		{name: "disabled"},
		{name: "enabled", enabled: true, state: ReleaseReminderAutoReleaseStateScheduled, autoAt: now.Add(AutoReleaseGracePeriod).Format(time.RFC3339)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newAutoReleaseTestStore(ReleaseReminder{ProfileName: "mac", AppleEmail: "apple@example.com", ReleaseDueAt: now.Format(time.RFC3339), Status: ReleaseReminderStatusActive, AutoReleaseEnabled: test.enabled})
			coordinator, notifications, _ := newAutoReleaseTestCoordinator(now, store)

			if err := coordinator.Scan(context.Background()); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			got := store.get("mac")
			if got.Status != ReleaseReminderStatusDueNotified || got.LastNotifiedAt != now.Format(time.RFC3339) || got.AutoReleaseState != test.state || got.AutoReleaseAt != test.autoAt {
				t.Fatalf("due reminder = %+v", got)
			}
			if len(*notifications) != 1 || (*notifications)[0].Kind != AutoReleaseNotificationDue {
				t.Fatalf("notifications = %+v", *notifications)
			}
		})
	}
}

func TestAutoReleaseWaitsUntilExactGraceDeadline(t *testing.T) {
	deadline := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	store := newAutoReleaseTestStore(scheduledAutoRelease(deadline))
	coordinator, _, starts := newAutoReleaseTestCoordinator(deadline.Add(-time.Nanosecond), store)

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("Scan before deadline: %v", err)
	}
	if len(*starts) != 0 {
		t.Fatalf("destroy starts before deadline = %d", len(*starts))
	}
	coordinator.Now = func() time.Time { return deadline }
	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("Scan at deadline: %v", err)
	}
	if len(*starts) != 1 || store.get("mac").AutoReleaseState != ReleaseReminderAutoReleaseStateRunning {
		t.Fatalf("starts=%d reminder=%+v", len(*starts), store.get("mac"))
	}
}

func TestAutoReleaseAtomicClaimLosesToExtension(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	store := newAutoReleaseTestStore(scheduledAutoRelease(now))
	store.beforeUpdate = func(reminder *ReleaseReminder) {
		reminder.ReleaseDueAt = now.Add(time.Hour).Format(time.RFC3339)
		reminder.Status = ReleaseReminderStatusActive
		reminder.AutoReleaseAt = ""
		reminder.AutoReleaseState = ""
		store.beforeUpdate = nil
	}
	coordinator, _, starts := newAutoReleaseTestCoordinator(now, store)

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(*starts) != 0 || store.get("mac").Status != ReleaseReminderStatusActive {
		t.Fatalf("extension lost: starts=%d reminder=%+v", len(*starts), store.get("mac"))
	}
}

func TestAutoReleasePreventsDuplicateDestroyJobs(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	store := newAutoReleaseTestStore(scheduledAutoRelease(now))
	coordinator, _, starts := newAutoReleaseTestCoordinator(now, store)
	coordinator.Jobs = &autoReleaseTestJobs{jobs: []Job{{ID: "existing", Type: "aws-destroy", Profile: "mac", Status: JobStatusRunning}}}

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(*starts) != 0 || store.get("mac").AutoReleaseState != ReleaseReminderAutoReleaseStateScheduled {
		t.Fatalf("duplicate was started: starts=%d reminder=%+v", len(*starts), store.get("mac"))
	}
}

func TestAutoReleaseCancelsChangedOrDisabledCycle(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	for _, mutate := range []func(*ReleaseReminder){
		func(r *ReleaseReminder) { r.AutoReleaseEnabled = false },
		func(r *ReleaseReminder) {
			r.Status = ReleaseReminderStatusActive
			r.ReleaseDueAt = now.Add(time.Hour).Format(time.RFC3339)
		},
		func(r *ReleaseReminder) { r.Status = ReleaseReminderStatusReleased },
	} {
		reminder := scheduledAutoRelease(now)
		mutate(&reminder)
		store := newAutoReleaseTestStore(reminder)
		coordinator, _, starts := newAutoReleaseTestCoordinator(now, store)
		if err := coordinator.Scan(context.Background()); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(*starts) != 0 {
			t.Fatalf("destroy starts = %d for %+v", len(*starts), reminder)
		}
	}
}

func TestApplyReleaseReminderExtensionCancelsCycleAndRejectsRunningOrShort(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 5, 0, 0, time.UTC)
	reminder := scheduledAutoRelease(now.Add(5 * time.Minute))
	reminder.AutoReleaseStartedAt = now.Format(time.RFC3339)
	reminder.AutoReleaseLastAttemptAt = now.Format(time.RFC3339)
	reminder.AutoReleaseAttempts = 2
	reminder.AutoReleaseLastError = "temporary"

	updated, err := applyReleaseReminderExtension(reminder, now.Add(AutoReleaseGracePeriod), now, "member@example.com", "Member")
	if err != nil {
		t.Fatalf("apply extension: %v", err)
	}
	if updated.Status != ReleaseReminderStatusActive || updated.AutoReleaseState != "" || updated.AutoReleaseAt != "" || updated.AutoReleaseStartedAt != "" || updated.AutoReleaseLastAttemptAt != "" || updated.AutoReleaseAttempts != 0 || updated.AutoReleaseLastError != "" {
		t.Fatalf("extension did not cancel cycle: %+v", updated)
	}
	if _, err := applyReleaseReminderExtension(reminder, now.Add(AutoReleaseGracePeriod-time.Second), now, "member@example.com", "Member"); err == nil {
		t.Fatal("short extension error = nil")
	}
	reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateRunning
	if _, err := applyReleaseReminderExtension(reminder, now.Add(time.Hour), now, "member@example.com", "Member"); err == nil {
		t.Fatal("running extension error = nil")
	}
}

func TestAutoReleaseRestartReconcilesStaleRunningThenRetriesOnSpacing(t *testing.T) {
	started := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	reminder := scheduledAutoRelease(started)
	reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateRunning
	reminder.AutoReleaseStartedAt = started.Format(time.RFC3339)
	reminder.AutoReleaseLastAttemptAt = started.Format(time.RFC3339)
	reminder.AutoReleaseAttempts = 1
	store := newAutoReleaseTestStore(reminder)
	coordinator, _, starts := newAutoReleaseTestCoordinator(started.Add(4*time.Minute), store)

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("restart Scan: %v", err)
	}
	if got := store.get("mac"); got.AutoReleaseState != ReleaseReminderAutoReleaseStateRetrying || len(*starts) != 0 {
		t.Fatalf("restart reconcile starts=%d reminder=%+v", len(*starts), got)
	}
	coordinator.Now = func() time.Time { return started.Add(AutoReleaseRetryInterval) }
	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("retry Scan: %v", err)
	}
	if len(*starts) != 1 || store.get("mac").AutoReleaseAttempts != 2 {
		t.Fatalf("retry starts=%d reminder=%+v", len(*starts), store.get("mac"))
	}
}

func TestAutoReleaseStopsAfterOneHour(t *testing.T) {
	started := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	reminder := scheduledAutoRelease(started)
	reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateRetrying
	reminder.AutoReleaseStartedAt = started.Format(time.RFC3339)
	reminder.AutoReleaseLastAttemptAt = started.Add(55 * time.Minute).Format(time.RFC3339)
	reminder.AutoReleaseAttempts = 12
	store := newAutoReleaseTestStore(reminder)
	coordinator, notifications, starts := newAutoReleaseTestCoordinator(started.Add(AutoReleaseRetryWindow), store)

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := store.get("mac"); got.AutoReleaseState != ReleaseReminderAutoReleaseStateFailed || len(*starts) != 0 {
		t.Fatalf("timeout starts=%d reminder=%+v", len(*starts), got)
	}
	if len(*notifications) != 1 || (*notifications)[0].Kind != AutoReleaseNotificationFinalFailure {
		t.Fatalf("notifications = %+v", *notifications)
	}
}

func TestAutoReleaseTerminalErrorsAndAppleMismatchDoNotRetry(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		resolve func(context.Context, ReleaseReminder) (Profile, error)
	}{
		{name: "missing credentials", resolve: func(context.Context, ReleaseReminder) (Profile, error) {
			return Profile{}, TerminalAutoReleaseError(errors.New("AWS credentials missing"))
		}},
		{name: "apple mismatch", resolve: func(context.Context, ReleaseReminder) (Profile, error) {
			p := autoReleaseTestProfile()
			p.AWS.AccountEmail = "other@example.com"
			return p, nil
		}},
		{name: "apple case mismatch", resolve: func(context.Context, ReleaseReminder) (Profile, error) {
			p := autoReleaseTestProfile()
			p.AWS.AccountEmail = "Apple@example.com"
			return p, nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newAutoReleaseTestStore(scheduledAutoRelease(now))
			coordinator, notifications, starts := newAutoReleaseTestCoordinator(now, store)
			coordinator.ResolveProfile = test.resolve
			if err := coordinator.Scan(context.Background()); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if got := store.get("mac"); got.AutoReleaseState != ReleaseReminderAutoReleaseStateFailed || len(*starts) != 0 || !strings.Contains(strings.ToLower(got.AutoReleaseLastError), strings.Split(test.name, " ")[0]) {
				t.Fatalf("terminal starts=%d reminder=%+v", len(*starts), got)
			}
			if len(*notifications) != 1 || (*notifications)[0].Kind != AutoReleaseNotificationFinalFailure {
				t.Fatalf("notifications = %+v", *notifications)
			}
		})
	}
}

func TestAutoReleaseRecoverableFailureRetriesAndNotifiesOnlyFirstFailure(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	store := newAutoReleaseTestStore(scheduledAutoRelease(now))
	coordinator, notifications, starts := newAutoReleaseTestCoordinator(now, store)
	coordinator.Status = func(context.Context, Profile) (AWSStatus, error) {
		return AWSStatus{}, errors.New("throttling: try again")
	}

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if got := store.get("mac"); got.AutoReleaseState != ReleaseReminderAutoReleaseStateRetrying || got.AutoReleaseAttempts != 1 || len(*starts) != 0 {
		t.Fatalf("first failure starts=%d reminder=%+v", len(*starts), got)
	}
	if len(*notifications) != 1 || (*notifications)[0].Kind != AutoReleaseNotificationFirstFailure {
		t.Fatalf("first notifications = %+v", *notifications)
	}
	coordinator.Now = func() time.Time { return now.Add(AutoReleaseRetryInterval) }
	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if len(*notifications) != 1 || store.get("mac").AutoReleaseAttempts != 2 {
		t.Fatalf("second notifications=%+v reminder=%+v", *notifications, store.get("mac"))
	}
}

func TestAutoReleaseObservesSuccessfulJobAndRetainsEIPAllocation(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	store := newAutoReleaseTestStore(scheduledAutoRelease(now))
	jobs := &autoReleaseTestJobs{}
	coordinator, notifications, starts := newAutoReleaseTestCoordinator(now, store)
	coordinator.Jobs = jobs
	statuses := []AWSStatus{
		{Hosts: []DedicatedHostStatus{{HostID: "h-1", State: "available"}}, Instances: []InstanceStatus{{InstanceID: "i-1", State: "running"}}, ElasticIP: ElasticIP{AllocationID: "eipalloc-1", AssociationID: "eipassoc-1", InstanceID: "i-1"}},
		{ElasticIP: ElasticIP{AllocationID: "eipalloc-1"}},
	}
	coordinator.Status = func(context.Context, Profile) (AWSStatus, error) {
		status := statuses[0]
		statuses = statuses[1:]
		return status, nil
	}
	coordinator.StartDestroy = func(_ context.Context, profile Profile) (Job, error) {
		*starts = append(*starts, profile)
		job := Job{ID: "destroy-1", Type: "aws-destroy", Profile: profile.Name, AppleEmail: profile.AWS.AccountEmail, Status: JobStatusRunning, StartedAt: now}
		jobs.jobs = []Job{job}
		return job, nil
	}

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("start Scan: %v", err)
	}
	jobs.jobs[0].Status = JobStatusSuccess
	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("observe Scan: %v", err)
	}
	got := store.get("mac")
	if got.Status != ReleaseReminderStatusReleased || got.AutoReleaseState != ReleaseReminderAutoReleaseStateReleased || store.cleanupCalls != 1 {
		t.Fatalf("success reminder=%+v cleanup=%d", got, store.cleanupCalls)
	}
	if len(*notifications) != 1 || (*notifications)[0].Kind != AutoReleaseNotificationSuccess {
		t.Fatalf("notifications = %+v", *notifications)
	}
}

func TestAutoReleaseDeferredJobWithResourcesRetries(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	reminder := scheduledAutoRelease(now)
	reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateRunning
	reminder.AutoReleaseStartedAt = now.Format(time.RFC3339)
	reminder.AutoReleaseLastAttemptAt = now.Format(time.RFC3339)
	reminder.AutoReleaseAttempts = 1
	store := newAutoReleaseTestStore(reminder)
	coordinator, _, _ := newAutoReleaseTestCoordinator(now.Add(time.Minute), store)
	coordinator.Jobs = &autoReleaseTestJobs{jobs: []Job{{ID: "destroy-1", Type: "aws-destroy", Profile: "mac", AppleEmail: "apple@example.com", Status: JobStatusDeferred, StartedAt: now, LastError: "host pending"}}}
	coordinator.Status = func(context.Context, Profile) (AWSStatus, error) {
		return AWSStatus{Hosts: []DedicatedHostStatus{{HostID: "h-1", State: "pending"}}, ElasticIP: ElasticIP{AllocationID: "eipalloc-1"}}, nil
	}

	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := store.get("mac"); got.AutoReleaseState != ReleaseReminderAutoReleaseStateRetrying || !strings.Contains(got.AutoReleaseLastError, "pending") {
		t.Fatalf("partial reminder = %+v", got)
	}
}

func scheduledAutoRelease(deadline time.Time) ReleaseReminder {
	return ReleaseReminder{ProfileName: "mac", AppleEmail: "apple@example.com", ReleaseDueAt: deadline.Add(-AutoReleaseGracePeriod).Format(time.RFC3339), LastNotifiedAt: deadline.Add(-AutoReleaseGracePeriod).Format(time.RFC3339), Status: ReleaseReminderStatusDueNotified, AutoReleaseEnabled: true, AutoReleaseAt: deadline.Format(time.RFC3339), AutoReleaseState: ReleaseReminderAutoReleaseStateScheduled}
}

func autoReleaseTestProfile() Profile {
	return Profile{Name: "mac", AWS: AWSConfig{AccountEmail: "apple@example.com", Profile: "aws-test", Region: "us-west-2"}}
}

type autoReleaseTestStore struct {
	mu           sync.Mutex
	reminders    map[string]ReleaseReminder
	beforeUpdate func(*ReleaseReminder)
	cleanupCalls int
}

func newAutoReleaseTestStore(reminders ...ReleaseReminder) *autoReleaseTestStore {
	s := &autoReleaseTestStore{reminders: map[string]ReleaseReminder{}}
	for _, reminder := range reminders {
		s.reminders[reminder.ProfileName] = reminder
	}
	return s
}

func (s *autoReleaseTestStore) ListReleaseReminders(string) ([]ReleaseReminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ReleaseReminder, 0, len(s.reminders))
	for _, reminder := range s.reminders {
		out = append(out, reminder)
	}
	return out, nil
}

func (s *autoReleaseTestStore) ReleaseReminder(profile string) (ReleaseReminder, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reminder, ok := s.reminders[profile]
	return reminder, ok, nil
}

func (s *autoReleaseTestStore) UpdateReleaseReminder(profile string, update func(ReleaseReminder) (ReleaseReminder, error)) (ReleaseReminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reminder, ok := s.reminders[profile]
	if !ok {
		return ReleaseReminder{}, fmt.Errorf("missing reminder %s", profile)
	}
	if s.beforeUpdate != nil {
		s.beforeUpdate(&reminder)
		s.reminders[profile] = reminder
	}
	updated, err := update(reminder)
	if err != nil {
		return ReleaseReminder{}, err
	}
	s.reminders[profile] = updated
	return updated, nil
}

func (s *autoReleaseTestStore) CleanupReleased(profile, _ string, at time.Time) (ReleaseReminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupCalls++
	reminder := s.reminders[profile]
	reminder.Status = ReleaseReminderStatusReleased
	reminder.ReleasedAt = at.Format(time.RFC3339)
	reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateReleased
	s.reminders[profile] = reminder
	return reminder, nil
}

func (s *autoReleaseTestStore) get(profile string) ReleaseReminder {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reminders[profile]
}

type autoReleaseTestJobs struct{ jobs []Job }

func (j *autoReleaseTestJobs) Active() ([]Job, error) {
	active := make([]Job, 0)
	for _, job := range j.jobs {
		if job.Status == JobStatusStarting || job.Status == JobStatusRunning {
			active = append(active, job)
		}
	}
	return active, nil
}
func (j *autoReleaseTestJobs) List() ([]Job, error) { return append([]Job(nil), j.jobs...), nil }

func newAutoReleaseTestCoordinator(now time.Time, store *autoReleaseTestStore) (*AutoReleaseCoordinator, *[]AutoReleaseNotification, *[]Profile) {
	notifications := []AutoReleaseNotification{}
	starts := []Profile{}
	coordinator := &AutoReleaseCoordinator{
		Now:            func() time.Time { return now },
		Store:          store,
		Jobs:           &autoReleaseTestJobs{},
		ResolveProfile: func(context.Context, ReleaseReminder) (Profile, error) { return autoReleaseTestProfile(), nil },
		Status: func(context.Context, Profile) (AWSStatus, error) {
			return AWSStatus{Hosts: []DedicatedHostStatus{{HostID: "h-1", State: "available"}}}, nil
		},
		StartDestroy: func(_ context.Context, profile Profile) (Job, error) {
			starts = append(starts, profile)
			return Job{ID: "destroy", Type: "aws-destroy", Profile: profile.Name, Status: JobStatusRunning, StartedAt: now}, nil
		},
		Notify: func(notification AutoReleaseNotification) error {
			notifications = append(notifications, notification)
			return nil
		},
		CleanupReleased: store.CleanupReleased,
	}
	return coordinator, &notifications, &starts
}
