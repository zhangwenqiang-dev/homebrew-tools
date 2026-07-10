# Local Agent Transfer Jobs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make web transfers report real rsync completion, survive browser refreshes, and prevent normal local-agent lifecycle commands from interrupting active transfers.

**Architecture:** Add an in-memory transfer job manager owned by `App`, execute rsync asynchronously with streamed output callbacks, and expose job/activity endpoints from the local agent. The web page starts a job and polls it; lifecycle commands query `/activity` before stopping launchd.

**Tech Stack:** Go HTTP server, `os/exec` rsync process, mutex-protected in-memory state, vanilla JavaScript polling, Go tests.

---

### Task 1: Stream rsync output through the runner

**Files:**
- Modify: `internal/connectmac/app.go`
- Modify: `internal/connectmac/runner.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add a failing runner contract test**

Add a fake runner implementation of `RunRsyncProgress(ctx, args, onOutput)` that records arguments and sends representative rsync output to the callback. Assert the callback receives `to-check=10/20` text.

- [ ] **Step 2: Run the focused test and confirm the interface method is missing**

Run: `go test ./internal/connectmac -run TestRsyncProgressRunner -count=1`

Expected: compile failure until `Runner.RunRsyncProgress` exists.

- [ ] **Step 3: Implement streamed execution**

Add this method to `Runner`:

```go
RunRsyncProgress(ctx context.Context, args []string, onOutput func(string)) error
```

Implement it in `ExecRunner` with `exec.CommandContext`. Attach a synchronized writer to stdout and stderr that forwards each non-empty chunk to `onOutput` while retaining the existing local-agent log output.

- [ ] **Step 4: Run the focused tests**

Run: `go test ./internal/connectmac -run 'TestRsyncProgressRunner|TestRsyncPushArgs' -count=1`

Expected: PASS.

### Task 2: Add the transfer job manager

**Files:**
- Create: `internal/connectmac/local_transfer_jobs.go`
- Create: `internal/connectmac/local_transfer_jobs_test.go`
- Modify: `internal/connectmac/app.go`

- [ ] **Step 1: Write failing job-state tests**

Cover these exact transitions:

```go
queued -> running -> succeeded (percent 100)
queued -> running -> failed (percent remains below 100, error retained)
```

Also assert a duplicate active `(profile, direction)` start returns the existing job ID.

- [ ] **Step 2: Write failing progress parser tests**

Use output containing `62%` and `to-check=6147/9749`; assert the result is between 1 and 99. Assert the manager sets 100 only after a nil rsync error.

- [ ] **Step 3: Implement focused job types and manager**

Define `LocalTransferJob`, `LocalTransferJobManager`, and statuses `queued`, `running`, `succeeded`, `failed`, and `interrupted`. Use a mutex, cloned response values, a 64 KiB output tail, duplicate suppression, and 24-hour pruning.

- [ ] **Step 4: Initialize shared state**

Add `LocalTransfers *LocalTransferJobManager` to `App` and initialize it in `NewApp`. Because `App` uses value receivers, keep the manager as a pointer so all handler copies share one job registry.

- [ ] **Step 5: Run job tests**

Run: `go test ./internal/connectmac -run 'TestLocalTransfer' -count=1`

Expected: PASS.

### Task 3: Expose asynchronous local-agent transfer APIs

**Files:**
- Modify: `internal/connectmac/app_local_agent.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing endpoint tests**

Test `POST /sync/push` returns a job without waiting for completion, `GET /sync/job?id=...` returns terminal state, `GET /sync/jobs?profile=...` lists it, and `GET /activity` reports active profile/direction.

- [ ] **Step 2: Replace synchronous push/pull handlers**

Keep `writeLocalAgentProfileConfig` and existing path defaults. Build the same CLI arguments, then call `LocalTransfers.Start(...)` with `Runner.RunRsyncProgress(context.Background(), args, callback)`.

- [ ] **Step 3: Add query handlers**

Register CORS-enabled handlers for `/sync/job`, `/sync/jobs`, and `/activity`. Return the standard `webAPIResponse` envelope with `data.job`, `data.items`, or `data.active`.

- [ ] **Step 4: Run endpoint tests**

Run: `go test ./internal/connectmac -run 'TestLocalAgent.*(Sync|Activity)' -count=1`

Expected: PASS.

### Task 4: Protect local-agent lifecycle commands

**Files:**
- Modify: `internal/connectmac/app_local_agent.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing activity-check tests**

Start an `httptest.Server` that returns `data.active` with one running job. Assert the activity decoder identifies the profile and direction. Also test unavailable and empty responses.

- [ ] **Step 2: Add lifecycle guard**

Before `stop`, `restart`, or `uninstall`, query `/activity` using the parsed host/port with a short timeout. If active jobs exist, print `local-agent has an active upload for <profile>; wait for it to finish` and return non-zero before calling launchctl.

- [ ] **Step 3: Run lifecycle tests**

Run: `go test ./internal/connectmac -run 'TestLocalAgent.*Activity' -count=1`

Expected: PASS.

### Task 5: Replace simulated web progress with job polling

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Add transfer job state to the page**

Replace `syncProgressTimer` with `syncJobs` and `syncJobPollTimer`. Add helpers that render `queued/running/succeeded/failed/interrupted` consistently.

- [ ] **Step 2: Start and poll jobs**

Make `runSync` consume `data.job`, poll `/sync/job?id=...` every second, and stop only on a terminal state. A successful job sets 100 and `上传完成`; failed/interrupted jobs retain their reported percentage and detailed error.

- [ ] **Step 3: Restore jobs after navigation or refresh**

When a profile's transfer view renders, call `/sync/jobs?profile=...`, restore the latest upload state, and resume polling if it is active.

- [ ] **Step 4: Prevent duplicate clicks**

Disable only the matching direction's button while its job is queued/running. Keep other existing readiness and local-agent checks.

### Task 6: Full verification

**Files:**
- Modify: tests only if verification exposes a regression.

- [ ] **Step 1: Format Go files**

Run: `gofmt -w internal/connectmac/app.go internal/connectmac/runner.go internal/connectmac/app_local_agent.go internal/connectmac/local_transfer_jobs.go internal/connectmac/local_transfer_jobs_test.go internal/connectmac/app_test.go`

- [ ] **Step 2: Run the full suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 3: Run browser-level checks**

Verify the page starts a job, displays polled progress, reaches 100 only after success, preserves a real error below 100, restores a running job after refresh, and disables restart while active.

- [ ] **Step 4: Review scope and worktree**

Run: `git diff --check` and `git status --short`.

Expected: no whitespace errors; `.mcp.json` and `CLAUDE.md` remain untouched.
