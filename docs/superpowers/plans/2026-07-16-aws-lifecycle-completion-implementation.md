# AWS Lifecycle Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finalize Web open/release ownership, reminders, UI state, and Enterprise WeChat notifications only after AWS reports `ready=true` or `stopped`.

**Architecture:** Persist the Web action intent on each background AWS job, then run a read-only lifecycle coordinator inside the existing Web worker. The coordinator resolves the managed profile, checks AWS state, applies idempotent owner/reminder changes, and marks the job finalized. The browser polls affected profiles every ten seconds while a lifecycle is active or pending finalization.

**Tech Stack:** Go, persisted JSON jobs, AWS SDK-backed status service, member repository/MySQL, embedded HTML/JavaScript, Enterprise WeChat webhook.

---

### Task 1: Persist Web Lifecycle Intent on Jobs

**Files:**
- Modify: `internal/connectmac/job.go`
- Modify: `internal/connectmac/app_web.go`
- Test: `internal/connectmac/job_quality_test.go`
- Test: `internal/connectmac/app_web_job_cleanup_test.go`

- [ ] **Step 1: Write failing backward-compatibility and update tests**

Add tests that load an old job JSON without lifecycle fields and update a new job atomically:

```go
func TestJobLifecycleMetadataIsBackwardCompatible(t *testing.T) {
	manager := NewJobManager(t.TempDir())
	job, err := manager.Create(Job{Type: "aws-open", Profile: "mac", Status: JobStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.Update(job.ID, func(current Job) (Job, error) {
		current.LifecycleOwnerEmail = "owner@example.com"
		current.LifecycleState = JobLifecyclePending
		return current, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.LifecycleOwnerEmail != "owner@example.com" || updated.LifecycleState != JobLifecyclePending {
		t.Fatalf("updated job = %#v", updated)
	}
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```bash
go test ./internal/connectmac -run 'TestJobLifecycleMetadata|TestAppWebBackground'
```

Expected: FAIL because lifecycle fields, constants, and `JobManager.Update` do not exist.

- [ ] **Step 3: Add lifecycle fields and atomic update**

Add:

```go
type JobLifecycleState string

const (
	JobLifecyclePending   JobLifecycleState = "pending"
	JobLifecycleWaiting   JobLifecycleState = "waiting"
	JobLifecycleFinalized JobLifecycleState = "finalized"
	JobLifecycleFailed    JobLifecycleState = "failed"
)

type Job struct {
	// Existing fields...
	LifecycleOwnerEmail string            `json:"lifecycle_owner_email,omitempty"`
	LifecycleState      JobLifecycleState `json:"lifecycle_state,omitempty"`
	LifecycleFinalizedAt time.Time         `json:"lifecycle_finalized_at,omitempty"`
	LifecycleNotifiedAt  time.Time         `json:"lifecycle_notified_at,omitempty"`
	LifecycleError       string            `json:"lifecycle_error,omitempty"`
}
```

Implement `JobManager.Update` through `withJobLock`, `loadRaw`, callback validation, and `Save`.

When `startWebAWSJob` creates a confirmed Web job, pass the authenticated owner email for `open` and set `LifecycleState: pending`. Do not add lifecycle metadata to CLI-only jobs.

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./internal/connectmac -run 'TestJobLifecycleMetadata|TestAppWebBackground'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectmac/job.go internal/connectmac/app_web.go internal/connectmac/job_quality_test.go internal/connectmac/app_web_job_cleanup_test.go
git commit -m "feat: persist web AWS lifecycle intent"
```

### Task 2: Stop Finalizing Lifecycle on Job Enqueue

**Files:**
- Modify: `internal/connectmac/app_web.go`
- Test: `internal/connectmac/app_test.go`

- [ ] **Step 1: Change existing Web tests to require no immediate mutation**

For confirmed background open, assert:

```go
if _, ok, err := app.MemberStore.ProfileOwner("xcode-vnc"); err != nil || ok {
	t.Fatalf("owner must remain unset until ready: ok=%t err=%v", ok, err)
}
if _, ok, err := app.MemberStore.ReleaseReminder("xcode-vnc"); err != nil || ok {
	t.Fatalf("reminder must remain unset until ready: ok=%t err=%v", ok, err)
}
```

For confirmed background destroy, seed an owner/reminder and assert both remain until stopped.

- [ ] **Step 2: Run tests and verify current behavior fails**

Run:

```bash
go test ./internal/connectmac -run 'TestAppWeb.*Background.*(Open|Destroy)'
```

Expected: FAIL because `afterConfirmedWebAWSAction` currently runs immediately.

- [ ] **Step 3: Remove enqueue-time lifecycle mutation**

Delete both immediate calls to:

```go
a.afterConfirmedWebAWSAction(...)
```

Keep preview/event logging unchanged. Change user-facing output to state only that the background job started.

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./internal/connectmac -run 'TestAppWeb.*Background.*(Open|Destroy)'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectmac/app_web.go internal/connectmac/app_test.go
git commit -m "fix: defer AWS lifecycle updates until completion"
```

### Task 3: Add the Server-Side Lifecycle Coordinator

**Files:**
- Create: `internal/connectmac/web_aws_lifecycle.go`
- Create: `internal/connectmac/web_aws_lifecycle_test.go`
- Modify: `internal/connectmac/app_web.go`

- [ ] **Step 1: Write coordinator state tests**

Create table-driven tests for:

```go
type webAWSLifecycleCase struct {
	command       string
	jobStatus     JobStatus
	awsStatus     AWSStatus
	wantState     JobLifecycleState
	wantOwner     string
	wantReleased  bool
	wantNotify    string
}
```

Cover:

- open running remains pending;
- open success plus not-ready becomes waiting;
- open success plus ready finalizes owner/reminder;
- failed open becomes lifecycle failed without success notification;
- destroy success with resources remains waiting;
- destroy deferred with resources remains waiting;
- destroy stopped clears owner and releases reminder;
- repeated scan does not finalize or notify twice.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/connectmac -run 'TestWebAWSLifecycle'
```

Expected: FAIL because the coordinator does not exist.

- [ ] **Step 3: Implement profile resolution and final-state predicates**

Add focused helpers:

```go
func awsLifecycleOpenReady(status AWSStatus) bool {
	return AWSStatusReady(status)
}

func awsLifecycleStopped(status AWSStatus) bool {
	return len(status.Hosts) == 0 &&
		len(status.Instances) == 0 &&
		strings.TrimSpace(status.ElasticIP.InstanceID) == ""
}
```

Resolve profiles from local config plus all managed profile records, matching the job profile and Apple email. Do not infer a different account.

- [ ] **Step 4: Implement idempotent scan and finalization**

Add:

```go
func (a App) reconcileWebAWSLifecycles(ctx context.Context, configPath string) error
func (a App) reconcileWebAWSLifecycleJob(ctx context.Context, configPath string, job Job) error
```

Rules:

- only `aws-open` and `aws-destroy` jobs with lifecycle metadata participate;
- failed/interrupted jobs become lifecycle failed;
- open finalizes only after `AWSStatusReady`;
- destroy finalizes only after `awsLifecycleStopped`;
- owner assignment/reminder mutation happens once;
- release clears owner only at stopped;
- EIP allocation is never released or mutated;
- status errors update `LifecycleError` and remain retryable.

Use a notifier callback field on `App` for tests, falling back to `notifyReleaseReminder` in production.

- [ ] **Step 5: Run coordinator tests**

Run:

```bash
go test ./internal/connectmac -run 'TestWebAWSLifecycle'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/web_aws_lifecycle.go internal/connectmac/web_aws_lifecycle_test.go internal/connectmac/app_web.go internal/connectmac/app.go
git commit -m "feat: reconcile web AWS lifecycle completion"
```

### Task 4: Run the Coordinator in the Web Worker

**Files:**
- Modify: `internal/connectmac/app_web.go`
- Test: `internal/connectmac/app_test.go`

- [ ] **Step 1: Write worker startup and shutdown tests**

Verify the worker:

- scans once immediately;
- scans every ten seconds through an injected ticker/scan callback;
- stops when Web context is cancelled;
- resumes unfinished lifecycle jobs after service restart.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/connectmac -run 'TestAppWeb.*LifecycleWorker'
```

Expected: FAIL before worker integration.

- [ ] **Step 3: Integrate lifecycle scan with the existing worker**

Change the Web background worker to use a ten-second lifecycle ticker and a one-minute reminder schedule:

```go
func (a App) runWebBackgroundWorker(ctx context.Context, configPath string) {
	lifecycleTicker := time.NewTicker(10 * time.Second)
	reminderTicker := time.NewTicker(time.Minute)
	defer lifecycleTicker.Stop()
	defer reminderTicker.Stop()

	_ = a.reconcileWebAWSLifecycles(ctx, configPath)
	a.advanceAutoReleaseReminders(ctx, configPath, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-lifecycleTicker.C:
			_ = a.reconcileWebAWSLifecycles(ctx, configPath)
		case now := <-reminderTicker.C:
			a.advanceAutoReleaseReminders(ctx, configPath, now)
		}
	}
}
```

Keep shutdown timeout behavior.

- [ ] **Step 4: Run worker tests**

Run:

```bash
go test ./internal/connectmac -run 'TestAppWeb.*LifecycleWorker'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectmac/app_web.go internal/connectmac/app_test.go
git commit -m "feat: run AWS lifecycle coordinator in web worker"
```

### Task 5: Refresh Affected Profiles in the Web UI

**Files:**
- Modify: `web/index.html`
- Modify: `internal/connectmac/app_web_auto_release_test.go`
- Test: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add Web contract assertions**

Assert that job polling:

- treats lifecycle `pending` and `waiting` as pollable;
- refreshes status for affected job profiles;
- reloads owners and reminders;
- does not call `window.location.reload`.

- [ ] **Step 2: Run Web tests and verify failure**

Run:

```bash
go test ./internal/connectmac -run 'TestAppWeb.*Polling'
```

Expected: FAIL because `loadJobs` currently refreshes only jobs/reminders.

- [ ] **Step 3: Add targeted refresh helpers**

Implement:

```javascript
function lifecycleJobPollingNeeded(job) {
  return ["starting", "running"].includes(job.status) ||
    ["pending", "waiting"].includes(job.lifecycle_state);
}

async function refreshLifecycleProfiles(jobs) {
  const names = [...new Set(jobs.filter(lifecycleJobPollingNeeded).map((job) => job.profile).filter(Boolean))];
  await Promise.all(names.map((name) => refreshStatus(name, false)));
  await Promise.all([loadProfiles(), loadReleaseReminders()]);
}
```

Call it from the ten-second job poll. Preserve current selection and login.

- [ ] **Step 4: Improve status labels**

Render:

- `后台任务运行中`;
- `等待 Mac ready`;
- `等待释放完成`;
- `已完成`;
- `失败`.

Do not show open/release success immediately after enqueue.

- [ ] **Step 5: Run Web tests and JavaScript syntax check**

Run:

```bash
go test ./internal/connectmac -run 'TestAppWeb.*Polling'
```

Expected: PASS, including the embedded Web JavaScript contract checks.

- [ ] **Step 6: Commit**

```bash
git add web/index.html internal/connectmac/app_web_auto_release_test.go internal/connectmac/app_test.go
git commit -m "fix: refresh web state during AWS lifecycle jobs"
```

### Task 6: Full Verification

**Files:**
- Verify: `internal/connectmac`
- Verify: `web/index.html`

- [ ] **Step 1: Run complete tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run race tests**

```bash
go test -race ./...
```

Expected: PASS.

- [ ] **Step 3: Run vet**

```bash
go vet ./...
```

Expected: PASS.

- [ ] **Step 4: Verify no secret or EIP regression**

Run focused searches and tests:

```bash
rg -n "ReleaseAddress|release.*elastic|lifecycle_owner_email|Mac 打开确认成功|Mac 释放成功" internal/connectmac
go test ./internal/connectmac -run 'TestWebAWSLifecycle|Test.*ElasticIP'
```

Expected: no lifecycle coordinator path releases an EIP; focused tests PASS.
