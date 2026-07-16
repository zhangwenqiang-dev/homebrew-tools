# Member Transfer History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store Web transfer attempts in the server database with strict per-member visibility and correlated diagnostic logs.

**Architecture:** Extend the member repository with transfer-record methods implemented by JSON and MySQL stores. The authenticated server creates and owns each record; the browser coordinates the local-agent rsync job and sends milestone updates. All record APIs enforce current-member ownership, including for administrators.

**Tech Stack:** Go, MySQL, JSON fallback store, authenticated HTTP APIs, local-agent rsync jobs, embedded JavaScript, structured JSON logs.

---

### Task 1: Add Transfer Record Domain Types and JSON Repository

**Files:**
- Modify: `internal/connectmac/member_store.go`
- Create: `internal/connectmac/member_store_transfer_test.go`

- [ ] **Step 1: Write member-isolation repository tests**

Test creation, listing, update, deletion, monotonic percent, terminal immutability, and two-member isolation using:

```go
record := TransferRecord{
	MemberID: "member-a",
	MemberEmail: "a@example.com",
	ProfileName: "iossupport-usw2",
	Direction: TransferDirectionPush,
	LocalPath: "/tmp/App",
	RemotePath: "~/Documents/",
	Status: TransferStatusCreated,
}
```

- [ ] **Step 2: Run focused tests and verify failure**

```bash
go test ./internal/connectmac -run 'TestMemberStoreTransfer'
```

Expected: FAIL because transfer types and repository methods do not exist.

- [ ] **Step 3: Add types and repository contract**

Add constants and fields:

```go
const (
	TransferDirectionPush = "push"
	TransferDirectionPull = "pull"
	TransferStatusCreated = "created"
	TransferStatusQueued = "queued"
	TransferStatusRunning = "running"
	TransferStatusSucceeded = "succeeded"
	TransferStatusFailed = "failed"
	TransferStatusInterrupted = "interrupted"
	TransferStatusUnconfirmed = "unconfirmed"
)

type TransferRecord struct {
	ID string `json:"id"`
	MemberID string `json:"member_id"`
	MemberEmail string `json:"member_email"`
	ProfileName string `json:"profile_name"`
	AppleEmail string `json:"apple_email,omitempty"`
	Direction string `json:"direction"`
	LocalPath string `json:"local_path"`
	RemotePath string `json:"remote_path"`
	LocalJobID string `json:"local_job_id,omitempty"`
	Status string `json:"status"`
	Percent int `json:"percent"`
	ErrorSummary string `json:"error_summary,omitempty"`
	CreatedAt string `json:"created_at"`
	StartedAt string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	UpdatedAt string `json:"updated_at"`
}
```

Extend `MemberData` with `TransferRecords []TransferRecord` and `MemberRepository` with create/list/update/delete methods that always take `memberID`.

- [ ] **Step 4: Implement JSON mutations**

Use the existing mutation lock. Generate IDs with a cryptographically random suffix, validate direction/status, enforce matching member ID and local job ID, prevent percent regression, and prevent terminal-to-active transitions.

- [ ] **Step 5: Run focused tests**

```bash
go test ./internal/connectmac -run 'TestMemberStoreTransfer'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/member_store.go internal/connectmac/member_store_transfer_test.go
git commit -m "feat: add member-scoped transfer records"
```

### Task 2: Add MySQL Transfer Storage

**Files:**
- Modify: `internal/connectmac/member_store_mysql.go`
- Modify: `internal/connectmac/member_store_mysql_test.go`

- [ ] **Step 1: Add schema and query expectation tests**

Require `cm_transfer_records` with indexes on:

```text
(member_id, profile_name, updated_at)
(member_id, local_job_id)
```

Test every select/update/delete query includes `member_id = ?`.

- [ ] **Step 2: Run MySQL tests and verify failure**

```bash
go test ./internal/connectmac -run 'TestMySQL.*Transfer'
```

Expected: FAIL before schema and methods exist.

- [ ] **Step 3: Add schema**

Add:

```sql
CREATE TABLE IF NOT EXISTS cm_transfer_records (
  id VARCHAR(160) PRIMARY KEY,
  member_id VARCHAR(128) NOT NULL,
  member_email VARCHAR(255) NOT NULL,
  profile_name VARCHAR(255) NOT NULL,
  apple_email VARCHAR(255) NULL,
  direction VARCHAR(16) NOT NULL,
  local_path TEXT NOT NULL,
  remote_path TEXT NOT NULL,
  local_job_id VARCHAR(255) NULL,
  status VARCHAR(32) NOT NULL,
  percent INT NOT NULL DEFAULT 0,
  error_summary TEXT NULL,
  created_at VARCHAR(64) NOT NULL,
  started_at VARCHAR(64) NULL,
  finished_at VARCHAR(64) NULL,
  updated_at VARCHAR(64) NOT NULL,
  INDEX idx_cm_transfer_member_profile (member_id, profile_name, updated_at),
  INDEX idx_cm_transfer_member_job (member_id, local_job_id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci
```

- [ ] **Step 4: Implement MySQL methods**

Use transactions and `SELECT ... FOR UPDATE` for updates. Return not found when member ID does not match, including for admins.

- [ ] **Step 5: Run MySQL tests**

```bash
go test ./internal/connectmac -run 'TestMySQL.*Transfer'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/member_store_mysql.go internal/connectmac/member_store_mysql_test.go
git commit -m "feat: persist transfer records in mysql"
```

### Task 3: Add Authenticated Transfer Record APIs

**Files:**
- Create: `internal/connectmac/app_web_transfer_records.go`
- Modify: `internal/connectmac/app_web.go`
- Create: `internal/connectmac/app_web_transfer_records_test.go`

- [ ] **Step 1: Write API authorization tests**

Create two members with access to the same profile. Verify:

- each lists only their own records;
- admin cannot read/update/delete the other member's record;
- request JSON cannot provide `member_id`;
- profile access is required for start;
- another member's record ID returns 404.

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/connectmac -run 'TestWebTransferRecord'
```

Expected: FAIL because endpoints do not exist.

- [ ] **Step 3: Implement handlers**

Register:

```go
mux.HandleFunc("/api/transfer-records", a.requireWebRole(a.webTransferRecordsHandler(), "viewer", "operator", "admin"))
mux.HandleFunc("/api/transfer-record/start", a.requireWebRole(a.webTransferRecordStartHandler(configPath), "operator", "admin"))
mux.HandleFunc("/api/transfer-record/update", a.requireWebRole(a.webTransferRecordUpdateHandler(), "operator", "admin"))
mux.HandleFunc("/api/transfer-record/delete", a.requireWebRole(a.webTransferRecordDeleteHandler(), "operator", "admin"))
```

Derive member identity only through `currentWebMember`. Validate profile visibility with the existing managed-profile access path.

- [ ] **Step 4: Record structured server logs**

Extend `LogEntry` with optional:

```go
MemberEmail string `json:"member_email,omitempty"`
TransferID string `json:"transfer_id,omitempty"`
LocalJobID string `json:"local_job_id,omitempty"`
Direction string `json:"direction,omitempty"`
Status string `json:"status,omitempty"`
Percent int `json:"percent,omitempty"`
ElapsedMS int64 `json:"elapsed_ms,omitempty"`
```

Log creation, milestone update, terminal result, authorization rejection, and persistence error.

- [ ] **Step 5: Run API and log tests**

```bash
go test ./internal/connectmac -run 'TestWebTransferRecord|TestLogManager'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/app_web_transfer_records.go internal/connectmac/app_web.go internal/connectmac/app_web_transfer_records_test.go internal/connectmac/logs.go internal/connectmac/logs_test.go
git commit -m "feat: add private transfer record APIs"
```

### Task 4: Correlate Local-Agent Jobs and Add Local Logs

**Files:**
- Modify: `internal/connectmac/app_local_agent.go`
- Modify: `internal/connectmac/local_transfer_jobs.go`
- Modify: `internal/connectmac/local_transfer_jobs_test.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Write correlation and lifecycle log tests**

Extend local transfer requests with `transfer_id`. Assert the resulting local job contains it and logs:

```text
transfer.local.started
transfer.progress
transfer.local.succeeded
```

or the matching failed/interrupted event.

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/connectmac -run 'TestLocalTransfer.*(Correlation|Log)'
```

Expected: FAIL before correlation support.

- [ ] **Step 3: Add correlation ID and milestone callbacks**

Add `TransferID` to `LocalTransferJob` and local-agent request. Add an optional job event callback:

```go
type LocalTransferEvent struct {
	TransferID string
	LocalJobID string
	Profile string
	Direction string
	Status string
	Percent int
	Elapsed time.Duration
	Error string
}
```

Emit only status changes and milestones `0, 10, 25, 50, 75, 90, 99, 100`.

- [ ] **Step 4: Write sanitized local logs**

Use `LogManager.Write` with transfer/local job IDs, profile, direction, percent, elapsed time, and sanitized rsync error. Never log profile YAML, identity file content, cookies, or tokens.

- [ ] **Step 5: Run focused tests**

```bash
go test ./internal/connectmac -run 'TestLocalTransfer'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/app_local_agent.go internal/connectmac/local_transfer_jobs.go internal/connectmac/local_transfer_jobs_test.go internal/connectmac/app_test.go
git commit -m "feat: correlate and log local transfers"
```

### Task 5: Replace the Web Sync History Flow

**Files:**
- Modify: `web/index.html`
- Modify: `internal/connectmac/app_test.go`
- Modify: `internal/connectmac/app_web_sync.go`

- [ ] **Step 1: Add Web contract tests**

Assert that Web JavaScript:

- creates a server record before calling `/sync/push` or `/sync/pull`;
- passes `transfer_id` to the local agent;
- binds `local_job_id`;
- sends milestone and terminal updates;
- loads `/api/transfer-records`;
- never sends member ID/email in request bodies.

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/connectmac -run 'TestAppWeb.*Transfer'
```

Expected: FAIL before the Web flow changes.

- [ ] **Step 3: Implement start ordering**

In `runSync`:

```javascript
const record = await api("/api/transfer-record/start", {
  method: "POST",
  body: JSON.stringify({ profile: p.name, direction, local_path: payload.local_path, remote_path: payload.remote_path })
});
payload.transfer_id = record.data.record.id;
const local = await localAgentAPI("/sync/" + direction, {
  method: "POST",
  body: JSON.stringify(localAgentPayload(p.name, payload))
});
await updateTransferRecord(record.data.record.id, local.data.job, true);
```

If local start fails, mark the record failed.

- [ ] **Step 4: Implement milestone/terminal updates and reconciliation**

Track the last reported milestone per transfer ID. On page reopen, load member records and local jobs, correlate by `local_job_id`, and resume polling. Mark unrecoverable active records `unconfirmed`.

- [ ] **Step 5: Replace history rendering**

Render member records with direction, paths, status, percent, start/finish, duration, error summary, `使用`, and `删除`. Remove use of unowned legacy history APIs from the active Web flow.

- [ ] **Step 6: Run Web tests**

```bash
go test ./internal/connectmac -run 'TestAppWeb.*Transfer'
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/index.html internal/connectmac/app_test.go internal/connectmac/app_web_sync.go
git commit -m "feat: show private member transfer history"
```

### Task 6: Full Verification

**Files:**
- Modify only when tests expose defects.

- [ ] **Step 1: Run complete tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run race tests and vet**

```bash
go test -race ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 3: Run privacy and secret checks**

```bash
go test ./internal/connectmac -run 'Test.*Transfer.*(Isolation|Secret|Admin)'
rg -n "member_id.*json|api_token|password|BEGIN .*PRIVATE KEY" web/index.html internal/connectmac/app_web_transfer_records.go internal/connectmac/logs.go
```

Expected: request bodies do not accept member identity; no secret values are logged.
