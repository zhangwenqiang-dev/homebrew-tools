# Background Job Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist dead background AWS jobs as interrupted and prevent staging deployments from restarting ConnectMac while jobs are active.

**Architecture:** `JobManager` becomes the single source of truth for reconciliation, active-job queries, and bounded waiting. CLI and Web startup call those APIs; the staging deployment script runs `job wait-all` before APT installation and systemd restart.

**Tech Stack:** Go filesystem/process APIs, Go CLI, vanilla JavaScript, POSIX shell, Go tests.

---

### Task 1: Persist interrupted background jobs

**Files:**
- Modify: `internal/connectmac/job.go`
- Test: `internal/connectmac/app_test.go`

- [x] **Step 1: Add failing reconciliation tests**

Create a running job with PID 42 and an injected `IsRunning` returning false. Call `Reconcile` and assert the saved JSON has `status=interrupted`, a non-zero `finished_at`, and `last_error=background process exited before recording completion`. Add a live-PID case that remains running and a terminal-job case that remains unchanged.

- [x] **Step 2: Run the focused tests**

Run: `go test ./internal/connectmac -run 'TestJobManagerReconcile' -count=1`

Expected: FAIL because `JobStatusInterrupted` and `Reconcile` do not exist.

- [x] **Step 3: Implement reconciliation**

Add:

```go
const JobStatusInterrupted JobStatus = "interrupted"

func (m JobManager) Reconcile() ([]Job, error)
func (m JobManager) Active() ([]Job, error)
```

`Reconcile` scans persisted jobs, changes only dead `running` jobs with positive PIDs, saves each change atomically, and returns changed jobs. `Active` reconciles first and returns only still-running jobs sorted newest first. Replace the old in-memory `unknown` refresh behavior so reads return the persisted terminal state.

- [x] **Step 4: Run focused tests and race checks**

Run: `go test -race ./internal/connectmac -run 'TestJobManager(Reconcile|Active)' -count=10`

Expected: PASS.

### Task 2: Add active and bounded wait CLI commands

**Files:**
- Modify: `internal/connectmac/job.go`
- Modify: `internal/connectmac/app_job.go`
- Modify: `internal/connectmac/app_usage.go`
- Modify: `internal/connectmac/app_completion.go`
- Test: `internal/connectmac/app_test.go`

- [x] **Step 1: Add failing CLI tests**

Cover `cm job active`, `cm job active --json`, `cm job wait-all` immediate success, eventual success, timeout, invalid timeout/interval, and canceled context. Assert timeout output names every remaining job and exits non-zero.

- [x] **Step 2: Add a testable wait primitive**

Extend `JobManager` with an injected context-aware sleeper and implement:

```go
func (m JobManager) WaitAll(
    ctx context.Context,
    timeout time.Duration,
    interval time.Duration,
    progress func([]Job, time.Duration),
) ([]Job, error)
```

It reconciles on every poll, exits only when no jobs remain, and returns a typed timeout error containing the active jobs.

- [x] **Step 3: Implement CLI parsing and output**

Add `active` and `wait-all` to `runJob`. Defaults are `--timeout 2h` and `--interval 10s`. `active --json` uses `json.Encoder`; text output reuses the jobs table. Progress lines include elapsed time and comma-separated job IDs.

- [x] **Step 4: Update usage and completions**

Expose `active`, `wait-all`, `--json`, `--timeout`, and `--interval` in zsh, bash, and fish completion templates without changing other commands.

- [x] **Step 5: Run CLI tests**

Run: `go test ./internal/connectmac -run 'TestAppJob(Active|WaitAll)|TestJobManagerWaitAll|TestAppCompletion' -count=1`

Expected: PASS.

### Task 3: Reconcile before Web startup and display interrupted jobs

**Files:**
- Modify: `internal/connectmac/app_web.go`
- Modify: `web/index.html`
- Test: `internal/connectmac/app_test.go`

- [x] **Step 1: Add failing Web startup tests**

Create a stale running job, invoke the startup reconciliation helper, and assert it becomes interrupted and the output names the job. Point the job directory at an unreadable/invalid path and assert startup returns non-zero before listening.

- [x] **Step 2: Add Web startup reconciliation**

Before database setup and listener creation, call `JobManager.Reconcile`. Print one line per changed job. Return an error when scanning or saving fails. Do not execute any job command.

- [x] **Step 3: Render interrupted jobs**

Update `jobBadge` so persisted `interrupted` jobs have a visible Chinese label and a non-success style. Keep job log access unchanged.

- [x] **Step 4: Run Web and JavaScript checks**

Run: `go test ./internal/connectmac -run 'TestWebJobReconciliation|TestAppWebJobs' -count=1`

Run: `node -e 'const fs=require("fs");const s=fs.readFileSync("web/index.html","utf8");new Function(s.match(/<script>([\\s\\S]*)<\\/script>/)[1])'`

Expected: both commands PASS.

### Task 4: Add a guarded staging deployment script

**Files:**
- Create: `scripts/deploy-staging.sh`
- Modify: `README.md`

- [x] **Step 1: Implement argument validation and dry-run-safe help**

The script accepts `--version <version>`, optional `--host <ssh-alias>` (default `staging2`), and optional `--timeout <duration>` (default `2h`). It validates all arguments, requires the local `dist/cm_<version>_arm64.deb`, computes SHA-256, uploads to `/tmp`, and exits on every error.

- [x] **Step 2: Guard the remote mutation sequence**

Run these remote operations in order. Extract the incoming package and use its binary for the wait command so the first release is protected even when the installed version does not yet support `wait-all`:

```sh
sha256sum -c -
dpkg-deb -x "$remote_package" "$preflight_dir"
env HOME=/var/lib/connectmac "$preflight_dir/usr/sbin/cm" job wait-all --timeout "$timeout" --drain
apt install -y "$remote_package"
systemctl restart connectmac
/usr/sbin/cm version
systemctl is-active connectmac
```

The script must not run APT or systemctl when `wait-all` fails. A failure after drain succeeds must run the hidden `job end-drain` recovery command before exiting.

- [x] **Step 3: Document the guarded release workflow**

Add the build and deploy commands to `README.md`, explain that active AWS jobs are awaited for up to two hours, and document that timeout aborts before service restart.

- [x] **Step 4: Validate shell syntax and help**

Run: `bash -n scripts/deploy-staging.sh`

Run: `scripts/deploy-staging.sh --help`

Expected: syntax succeeds and help exits 0 without network or file mutations.

### Task 5: Full verification

**Files:**
- Modify: tests only if verification finds a regression.

- [x] **Step 1: Format Go files**

Run: `gofmt -w internal/connectmac/job.go internal/connectmac/app_job.go internal/connectmac/app_web.go internal/connectmac/app_usage.go internal/connectmac/app_completion.go internal/connectmac/app_test.go`

- [x] **Step 2: Run all checks**

Run: `go test -count=1 ./...`

Run: `go test -race -count=1 ./...`

Run: `go vet ./...`

Run: `bash -n scripts/deploy-staging.sh`

Expected: all PASS.

- [x] **Step 3: Test reconciliation against an isolated job directory**

Use a temporary HOME/job directory containing one dead running PID and one terminal job. Run `cm job active --json` and verify the dead job becomes interrupted without executing its stored command.

- [x] **Step 4: Review the worktree**

Run: `git diff --check` and `git status --short`.

Expected: no whitespace errors; `.mcp.json` and `CLAUDE.md` remain untouched.
