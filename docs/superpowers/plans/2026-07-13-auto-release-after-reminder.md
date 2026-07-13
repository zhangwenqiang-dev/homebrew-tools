# Auto Release After Reminder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-Profile opt-in automatic Mac release ten minutes after a due reminder, with valid-extension cancellation, safe five-minute retries for at most one hour, and permanent Elastic IP retention.

**Architecture:** Persist the automatic-release schedule and retry state in `ReleaseReminder`, then let the existing minute worker delegate to a focused coordinator. The coordinator atomically claims a reminder cycle, starts the existing confirmed background destroy workflow, observes its Job/AWS result, and retries only while the persisted cycle is unchanged. An administrator-only API and Profile UI toggle control the opt-in setting.

**Tech Stack:** Go, existing MemberStore/MySQL store, existing JobManager/AWSService, net/http handlers, vanilla JavaScript/CSS, Enterprise WeChat notifier.

---

### Task 1: Persist Automatic-Release State

**Files:**
- Modify: `internal/connectmac/member_store.go`
- Modify: `internal/connectmac/member_store_mysql.go`
- Modify: `internal/connectmac/member_store_test.go`

- [ ] **Step 1: Add failing storage and migration tests**

Add tests proving absent JSON fields decode with automatic release disabled, MySQL migration adds all fields without replacing existing rows, and update methods preserve unrelated reminder fields. Define expected fields as `AutoReleaseEnabled`, `AutoReleaseAt`, `AutoReleaseStartedAt`, `AutoReleaseLastAttemptAt`, `AutoReleaseAttempts`, `AutoReleaseLastError`, and `AutoReleaseState`.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/connectmac -run 'Test.*ReleaseReminder.*(Auto|Migration|Preserve)' -count=1`

Expected: FAIL because the fields and store operations do not exist.

- [ ] **Step 3: Extend models and schema**

Add zero-value-compatible JSON fields to `ReleaseReminder`, SQL columns with `NOT NULL DEFAULT` values, select/scan/insert support, and constants for `scheduled`, `running`, `retrying`, `failed`, and `released`.

- [ ] **Step 4: Add atomic reminder mutation**

Extend the store contract with a callback-style operation:

```go
UpdateReleaseReminder(profileName string, update func(ReleaseReminder) (ReleaseReminder, error)) (ReleaseReminder, error)
```

The JSON store executes it under its existing lock. The MySQL implementation uses a transaction and `SELECT ... FOR UPDATE`, then updates only the claimed row. This operation is the only path used for schedule, extension, claim, and attempt-result transitions.

- [ ] **Step 5: Run storage tests**

Run: `go test ./internal/connectmac -run 'Test.*ReleaseReminder' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Commit message: `feat: persist automatic release state`

### Task 2: Build The Automatic-Release Coordinator

**Files:**
- Create: `internal/connectmac/auto_release.go`
- Create: `internal/connectmac/auto_release_test.go`
- Modify: `internal/connectmac/app.go`
- Modify: `internal/connectmac/app_web.go`

- [ ] **Step 1: Add deterministic coordinator tests**

Use an injected clock and fake destroy starter/status reader to cover disabled reminders, exact ten-minute scheduling, pre-deadline no-op, atomic claim, duplicate active Job prevention, valid extension cancellation, restart recovery, five-minute retry spacing, and one-hour terminal timeout. Assert every destroy attempt receives the persisted Profile and Apple email and reports `eip_retained=true`.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/connectmac -run 'TestAutoRelease' -count=1`

Expected: FAIL because `AutoReleaseCoordinator` does not exist.

- [ ] **Step 3: Implement scheduling and claims**

Create a coordinator with explicit dependencies:

```go
type AutoReleaseCoordinator struct {
    Store MemberRepository
    Jobs JobManager
    Now func() time.Time
    StartDestroy func(context.Context, ReleaseReminder) (Job, error)
    Status func(context.Context, ReleaseReminder) (AWSStatus, error)
    Notify func(event string, reminder ReleaseReminder, description string)
}
```

Use constants of `10*time.Minute`, `5*time.Minute`, and `time.Hour`. Claim only unchanged `due_notified/scheduled/retrying` cycles. Treat an existing active `aws-destroy` Job for the Profile as already claimed.

- [ ] **Step 4: Reuse the existing destroy path**

Extract the typed portion of `startWebAWSJob` into an internal helper that accepts a resolved Profile, creates the existing `aws-destroy` Job with `--confirm`, and retains the EIP through the existing AWS service. The coordinator must not construct commands from arbitrary database values and must validate Profile/Apple-account equality before starting.

- [ ] **Step 5: Implement retry classification and reconciliation**

Retry throttling/network/transition errors and unknown errors within the one-hour window. Stop immediately on missing credentials/config, authorization, ambiguous ownership, or Profile/Apple mismatch. Reconcile stale `running` state with no active Job to `retrying`. Mark success only after fresh status shows no managed host/instance and no EIP association; never release the EIP allocation.

- [ ] **Step 6: Integrate with the reminder worker**

Replace the worker's direct due-only call with one scan that sends due notifications, persists `auto_release_at` only after notification handling, then advances scheduled/retrying releases. Keep one-minute polling and persisted restart recovery.

- [ ] **Step 7: Run coordinator and worker tests**

Run: `go test ./internal/connectmac -run 'Test(AutoRelease|ReleaseReminderWorker)' -count=1`

Expected: PASS with no real AWS calls.

- [ ] **Step 8: Commit**

Commit message: `feat: automate safe Mac release after reminders`

### Task 3: Enforce Extension Rules And Administrator Controls

**Files:**
- Modify: `internal/connectmac/app_web.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing handler tests**

Test that only admins can toggle automatic release, existing reminders default disabled, a due reminder can be enabled/disabled, non-admin requests return 403, and disabling cancels scheduled/retrying state but rejects a currently running release. Add extension tests where `release_due_at < now+10m` returns 400 and a valid extension atomically clears schedule/retry fields.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/connectmac -run 'TestWebReleaseReminder(Auto|Extend)' -count=1`

Expected: FAIL because the endpoint and validation are absent.

- [ ] **Step 3: Add the administrator endpoint**

Register `POST /api/release-reminder/auto-release` behind the existing admin role middleware. Accept `{profile, enabled}`, update through `UpdateReleaseReminder`, record the acting member in an operation event, and return the updated reminder.

- [ ] **Step 4: Harden extension semantics**

Validate against one captured server `now` and require `dueAt >= now.Add(10*time.Minute)`. Atomically reject `running`; otherwise set status active, clear notification/schedule/retry fields, and preserve `auto_release_enabled`.

- [ ] **Step 5: Run handler tests**

Run: `go test ./internal/connectmac -run 'TestWebReleaseReminder(Auto|Extend)' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Commit message: `feat: add automatic release controls`

### Task 4: Add Profile UI States And Controls

**Files:**
- Modify: `web/index.html`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing Web contract assertions**

Assert the page contains an administrator-only automatic-release toggle, localized state labels, exact schedule/retry/failure text, and calls `/api/release-reminder/auto-release`. Assert mobile rendering does not hide the reminder state or extension action.

- [ ] **Step 2: Implement UI rendering**

Place the toggle in the Profile reminder/details area, not the member list. Show state and timestamps without adding nested cards. Disable the toggle while a release is running, keep it read-only for non-admins, close success dialogs after save, and refresh only Profile/reminder data.

- [ ] **Step 3: Add confirmation and feedback**

Enabling requires a custom confirmation dialog stating: reminder, ten-minute grace period, one-hour retry limit, and EIP retention. Every save displays success/failure feedback and prevents double-click submission.

- [ ] **Step 4: Validate JavaScript and Web tests**

Run: `node -e 'const fs=require("fs");const s=fs.readFileSync("web/index.html","utf8");const a=s.indexOf("<script>")+8,b=s.lastIndexOf("</script>");new Function(s.slice(a,b))'`

Run: `go test ./internal/connectmac -run 'Test.*Web.*AutoRelease' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

Commit message: `feat: show automatic release controls in web`

### Task 5: Notifications, Logs, And Full Verification

**Files:**
- Modify: `internal/connectmac/app_web.go`
- Modify: `internal/connectmac/wechat.go`
- Modify: `internal/connectmac/app_test.go`
- Modify: `README.md`

- [ ] **Step 1: Test notification and logging policy**

Assert due notifications include the exact automatic-release time, only first retry and final failure notify, success notifies once, all attempts produce structured logs/events, and success text includes `eip_retained=true`.

- [ ] **Step 2: Implement notification descriptions**

Reuse `notifyReleaseReminder` with events `auto-release-scheduled`, `auto-release-retry`, `auto-release-failed`, and `auto-release-success`. Redact webhook URLs from every error path.

- [ ] **Step 3: Document administrator behavior**

Update README with default-off semantics, ten-minute minimum extension, five-minute retry interval, one-hour limit, and permanent EIP retention.

- [ ] **Step 4: Run all verification**

Run:

```bash
gofmt -w internal/connectmac/*.go
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

Run the JavaScript syntax command from Task 4 and `git diff --check`.

Expected: all commands PASS; tests use fakes and perform no real AWS mutation.

- [ ] **Step 5: Review safety invariants**

Verify by code inspection and tests that automatic release is default-off, claims use explicit stored Apple email, no EIP release API is reachable, duplicate destroy Jobs are blocked, extension cannot race past a claim, retries stop after one hour, and deployment drain sees automatic Jobs.

- [ ] **Step 6: Commit**

Commit message: `test: verify automatic Mac release safety`
