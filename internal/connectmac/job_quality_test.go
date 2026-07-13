package connectmac

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManualAndAutomaticDestroyPathsShareAtomicUniqueness(t *testing.T) {
	var out, errOut bytes.Buffer
	app := NewApp(&out, &errOut)
	manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
	manager.Executable = "/usr/bin/true"
	manager.IsRunning = func(int) bool { return true }
	app.JobManager = manager
	profile := validAWSProfile()
	plan, err := app.AWSService.Plan(profile)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if code := app.startAWSDestroyJob(context.Background(), profile, plan, filepath.Join(t.TempDir(), "config.yaml"), false); code != 0 {
		t.Fatalf("manual start code=%d err=%s", code, errOut.String())
	}
	autoConfig := filepath.Join(t.TempDir(), "auto-config.yaml")
	if err := os.WriteFile(autoConfig, []byte("profiles: {}\n"), 0o600); err != nil {
		t.Fatalf("write automatic config: %v", err)
	}
	if _, _, err := app.startAWSJobForResolvedProfile(context.Background(), autoConfig, "destroy", profile, true, autoConfig); !IsDuplicateActiveJob(err, "aws-destroy", profile.Name) {
		t.Fatalf("automatic start error = %v", err)
	}
	if _, err := os.Stat(autoConfig); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("duplicate automatic config was not cleaned: %v", err)
	}
}

func TestJobManagerCreatePreventsConcurrentAWSDestroyAcrossManagers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	managers := []JobManager{NewJobManager(dir), NewJobManager(dir)}
	managers[0].Now = func() time.Time { return time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC) }
	managers[1].Now = func() time.Time { return time.Date(2026, 7, 13, 8, 0, 1, 0, time.UTC) }
	start := make(chan struct{})
	results := make(chan error, len(managers))
	var wg sync.WaitGroup
	for i := range managers {
		wg.Add(1)
		go func(manager JobManager) {
			defer wg.Done()
			<-start
			_, err := manager.Create(Job{Type: "aws-destroy", Profile: "mac"})
			results <- err
		}(managers[i])
	}
	close(start)
	wg.Wait()
	close(results)

	successes, duplicates := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case IsDuplicateActiveJob(err, "aws-destroy", "mac"):
			duplicates++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("successes=%d duplicates=%d", successes, duplicates)
	}
	jobs, err := managers[0].listRaw()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Type != "aws-destroy" || jobs[0].Profile != "mac" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestJobManagerProfileOperationGuardSerializesAcrossManagers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	first := NewJobManager(dir)
	second := NewJobManager(dir)
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithProfileOperation("../mac/unsafe", func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.WithProfileOperation("../mac/unsafe", func() error { return nil })
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second profile operation bypassed guard: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first profile operation: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second profile operation: %v", err)
	}
	lockPath, err := first.profileOperationLockPath("../mac/unsafe")
	if err != nil {
		t.Fatalf("profile lock path: %v", err)
	}
	if filepath.Dir(lockPath) != filepath.Join(dir, ".profile-locks") || strings.Contains(filepath.Base(lockPath), "mac") || filepath.Ext(lockPath) != ".lock" {
		t.Fatalf("unsafe profile lock path = %q", lockPath)
	}
}

func TestJobManagerAWSDestroyCreateUsesProfileOperationGuard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	guard := NewJobManager(dir)
	creator := NewJobManager(dir)
	createDone := make(chan error, 1)
	err := guard.WithProfileOperation("mac", func() error {
		go func() {
			_, err := creator.Create(Job{Type: "aws-destroy", Profile: "mac"})
			createDone <- err
		}()
		select {
		case err := <-createDone:
			t.Fatalf("destroy create bypassed profile guard: %v", err)
		case <-time.After(100 * time.Millisecond):
			return nil
		}
		return nil
	})
	if err != nil {
		t.Fatalf("hold profile guard: %v", err)
	}
	if err := <-createDone; err != nil {
		t.Fatalf("create destroy after guard: %v", err)
	}
}

func TestJobManagerAWSDestroyUniquenessAllowsOtherProfilesAndTerminalReplacement(t *testing.T) {
	manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
	first, err := manager.Create(Job{ID: "destroy-mac-1", Type: "aws-destroy", Profile: "mac"})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, err := manager.Create(Job{ID: "destroy-other", Type: "aws-destroy", Profile: "other"}); err != nil {
		t.Fatalf("create other profile: %v", err)
	}
	first.Status = JobStatusFailed
	first.FinishedAt = time.Now()
	if err := manager.Save(first); err != nil {
		t.Fatalf("finish first: %v", err)
	}
	if _, err := manager.Create(Job{ID: "destroy-mac-2", Type: "aws-destroy", Profile: "mac"}); err != nil {
		t.Fatalf("create replacement: %v", err)
	}
}

func TestJobManagerArtifactLifecycle(t *testing.T) {
	newArtifact := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "auto-config.yaml")
		if err := os.WriteFile(path, []byte("secret config"), 0o600); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
		return path
	}
	assertRemoved := func(t *testing.T, path string) {
		t.Helper()
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("artifact %s still exists: %v", path, err)
		}
	}

	t.Run("create failure", func(t *testing.T) {
		manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
		if err := manager.BeginDrain(); err != nil {
			t.Fatalf("begin drain: %v", err)
		}
		artifact := newArtifact(t)
		if _, err := manager.Create(Job{ID: "create-failure", CleanupPaths: []string{artifact}}); !errors.Is(err, ErrJobsDraining) {
			t.Fatalf("create error = %v", err)
		}
		assertRemoved(t, artifact)
	})

	t.Run("runner startup failure", func(t *testing.T) {
		manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
		manager.Executable = filepath.Join(t.TempDir(), "missing-cm")
		artifact := newArtifact(t)
		job, err := manager.Create(Job{ID: "startup-failure-artifact", CleanupPaths: []string{artifact}})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := manager.StartRunner(context.Background(), job); err == nil {
			t.Fatal("StartRunner error = nil")
		}
		assertRemoved(t, artifact)
	})

	for _, test := range []struct {
		name    string
		command []string
	}{
		{name: "success", command: []string{"/bin/sh", "-c", `test -f "$1"`, "sh"}},
		{name: "failed job", command: []string{"/bin/false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
			artifact := newArtifact(t)
			command := append([]string(nil), test.command...)
			if test.name == "success" {
				command = append(command, artifact)
			}
			job, err := manager.Create(Job{ID: "run-artifact", Status: JobStatusRunning, RunnerToken: "artifact-token", Command: command, CleanupPaths: []string{artifact}})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			t.Setenv(jobRunnerTokenEnv, "artifact-token")
			_, _ = manager.RunJob(context.Background(), job.ID)
			assertRemoved(t, artifact)
		})
	}

	t.Run("stale reconciliation", func(t *testing.T) {
		manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
		manager.IsRunning = func(int) bool { return false }
		artifact := newArtifact(t)
		if _, err := manager.Create(Job{ID: "stale-artifact", Status: JobStatusRunning, PID: 123, CleanupPaths: []string{artifact}}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := manager.Reconcile(); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		assertRemoved(t, artifact)
	})
}

func TestJobManagerPersistsStructuredChildOutcome(t *testing.T) {
	manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
	job, err := manager.Create(Job{
		ID:          "structured-outcome",
		Type:        "aws-destroy",
		Profile:     "mac",
		Status:      JobStatusRunning,
		RunnerToken: "outcome-token",
		Command: []string{"/bin/sh", "-c",
			`printf '%s' '{"error_category":"terminal","error_code":"AccessDenied","reason":"exact redacted reason"}' > "$CM_JOB_OUTCOME_PATH"; exit 1`},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Setenv(jobRunnerTokenEnv, "outcome-token")
	completed, err := manager.RunJob(context.Background(), job.ID)
	if err == nil {
		t.Fatal("RunJob error = nil")
	}
	if completed.Status != JobStatusFailed || completed.ErrorCategory != JobErrorCategoryTerminal || completed.ErrorCode != "AccessDenied" || completed.LastError != "exact redacted reason" {
		t.Fatalf("completed job = %+v", completed)
	}
	if completed.OutcomePath != "" {
		t.Fatalf("outcome path was persisted after ingestion: %q", completed.OutcomePath)
	}
}

func TestTerminalDestroyChildOutcomeStopsAutoReleaseWithExactReason(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 10, 0, 0, time.UTC)
	manager := NewJobManager(filepath.Join(t.TempDir(), "jobs"))
	manager.Now = func() time.Time { return now }
	job, err := manager.Create(Job{
		ID:          "terminal-destroy",
		Type:        "aws-destroy",
		Profile:     "mac",
		AppleEmail:  "apple@example.com",
		Status:      JobStatusRunning,
		RunnerToken: "terminal-token",
		StartedAt:   now,
		Command: []string{"/bin/sh", "-c",
			`printf '%s' '{"error_category":"terminal","error_code":"AccessDenied","reason":"authorization denied for expected account"}' > "$CM_JOB_OUTCOME_PATH"; exit 1`},
	})
	if err != nil {
		t.Fatalf("create destroy job: %v", err)
	}
	t.Setenv(jobRunnerTokenEnv, "terminal-token")
	completed, err := manager.RunJob(context.Background(), job.ID)
	if err == nil || completed.ErrorCategory != JobErrorCategoryTerminal {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	reminder := scheduledAutoRelease(now)
	reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateRunning
	reminder.AutoReleaseStartedAt = now.Format(time.RFC3339)
	reminder.AutoReleaseLastAttemptAt = now.Format(time.RFC3339)
	reminder.AutoReleaseAttempts = 1
	store := newAutoReleaseTestStore(reminder)
	coordinator, notifications, starts := newAutoReleaseTestCoordinator(now.Add(time.Minute), store)
	coordinator.Jobs = manager
	coordinator.Status = func(context.Context, Profile) (AWSStatus, error) {
		return AWSStatus{Hosts: []DedicatedHostStatus{autoReleaseTestHost("available")}}, nil
	}
	if err := coordinator.Scan(context.Background()); err != nil {
		t.Fatalf("coordinator Scan: %v", err)
	}
	got := store.get("mac")
	if got.AutoReleaseState != ReleaseReminderAutoReleaseStateFailed || got.AutoReleaseLastError != "authorization denied for expected account" || len(*starts) != 0 {
		t.Fatalf("terminal outcome retried or lost reason: reminder=%+v starts=%d", got, len(*starts))
	}
	if len(*notifications) != 1 || (*notifications)[0].Kind != AutoReleaseNotificationFinalFailure {
		t.Fatalf("notifications = %+v", *notifications)
	}
}
