# Host Key Scan Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Host Key scanning work with OpenSSH 10.2 servers when `ssh-keyscan` fails, without modifying the user's real trust store before explicit fingerprint confirmation.

**Architecture:** Keep `ssh-keyscan` as the primary path in `ExecRunner.ScanHostKey`. Move the system-command details into focused helpers, then fall back to a normal `ssh` handshake that writes only to an owner-only temporary known-hosts file. Return valid captured keys even when authentication predictably fails, while preserving the existing challenge and confirmation boundary.

**Tech Stack:** Go, `os/exec`, system OpenSSH, existing ConnectMac runner and structured logging.

---

### Task 1: Add a safe SSH Host Key fallback scanner

**Files:**
- Modify: `internal/connectmac/runner.go`
- Create: `internal/connectmac/runner_host_key_test.go`

- [ ] **Step 1: Add failing primary/fallback command tests**

Create temporary fake `ssh-keyscan` and `ssh` executables and prepend their directory to `PATH`. Cover these cases:

```go
func TestExecRunnerScanHostKeyUsesPrimaryResult(t *testing.T)
func TestExecRunnerScanHostKeyFallsBackToSSH(t *testing.T)
func TestExecRunnerScanHostKeyRejectsEmptyFallback(t *testing.T)
func TestExecRunnerScanHostKeyCleansTemporaryFiles(t *testing.T)
func TestExecRunnerScanHostKeyHonorsCancellation(t *testing.T)
```

The fallback fixture must write a valid key to the path supplied by
`UserKnownHostsFile`, exit with status 255, and prove that `ScanHostKey` still
returns the key. It must also record argv so the test can assert that no
identity file or real known-hosts path was supplied.

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'TestExecRunnerScanHostKey' -count=1
```

Expected: FAIL because `ScanHostKey` currently returns the primary scanner's
exit error without a fallback.

- [ ] **Step 3: Implement the primary and fallback helpers**

Keep the public interface unchanged:

```go
func (ExecRunner) ScanHostKey(ctx context.Context, host string) (string, error)
```

Implement private helpers with these responsibilities:

```go
func scanHostKeyWithKeyscan(ctx context.Context, host string) (string, string, error)
func scanHostKeyWithSSH(ctx context.Context, host string) (string, string, error)
func validScannedHostKeys(text string) bool
```

The fallback creates a `0700` temporary directory and an empty `0600`
known-hosts file, then executes `ssh` without a shell using arguments equivalent
to:

```text
-o BatchMode=yes
-o PreferredAuthentications=none
-o PasswordAuthentication=no
-o KbdInteractiveAuthentication=no
-o ConnectTimeout=5
-o StrictHostKeyChecking=accept-new
-o UserKnownHostsFile=<temporary file>
-o GlobalKnownHostsFile=/dev/null
-T <host> true
```

Do not pass `IdentityFile`, `IdentitiesOnly`, agent forwarding, port forwarding,
or the user's SSH configuration as a trust source. Read the temporary file after
the process exits. If it contains valid Host Key records, return them regardless
of the expected authentication error. Always remove the temporary directory.

If both scanners fail, return one sanitized, bounded error containing the stage
names and exit classifications, not raw key material or temporary paths.

- [ ] **Step 4: Run focused and package tests**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'TestExecRunnerScanHostKey' -count=1
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectmac/runner.go internal/connectmac/runner_host_key_test.go
git commit -m "fix: fall back when ssh-keyscan is incompatible"
```

### Task 2: Preserve confirmation and improve diagnostics

**Files:**
- Modify: `internal/connectmac/host_key.go`
- Modify: `internal/connectmac/logs.go`
- Modify: `internal/connectmac/app_local_agent.go`
- Test: `internal/connectmac/app_test.go`
- Test: `internal/connectmac/app_local_observability_test.go`

- [ ] **Step 1: Add failing integration and logging tests**

Add tests proving:

```go
func TestHostKeyFallbackStillRequiresExplicitConfirmation(t *testing.T)
func TestHostKeyScanFailureLogsMethodWithoutSensitiveMaterial(t *testing.T)
func TestHostKeyScanFailureReturnsActionableMessage(t *testing.T)
```

The first test must show that a fallback result remains `missing` or `stale`,
does not alter the real `known_hosts`, and only changes it after the existing
complete fingerprint confirmation and one-time challenge are accepted.

The logging test must reject public-key bodies, fingerprints, challenge values,
temporary paths, and usernames in the serialized event.

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'TestHostKey(Fallback|ScanFailure)' -count=1
```

Expected: FAIL because scanner method and actionable failure classification are
not yet represented.

- [ ] **Step 3: Add scanner metadata without changing trust semantics**

Extend `HostKeyCheck` and `LogEntry` additively:

```go
Scanner string `json:"scanner,omitempty"`
```

Set it to `ssh-keyscan` or `ssh-fallback`. Keep scanned key material only in the
in-memory check object and existing challenge flow. `host-key.blocked` records
the scanner and a stable `host_key_scan_failed` code but never records the key,
fingerprint, challenge, or temporary path.

Map diagnostics to user-facing messages:

```text
SSH service unreachable; verify that the Mac is ready and port 22 is reachable.
SSH Host Key negotiation failed; retry or inspect local Agent diagnostics.
```

The detailed sanitized reason remains in structured local diagnostics.

- [ ] **Step 4: Run all Host Key and Agent tests**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'Test.*HostKey|Test.*LocalAgent' -count=1
GOCACHE=/tmp/connectmac-go-cache go test ./... -count=1
git diff --check
```

Expected: PASS. Tests that require local listeners may need the repository's
approved non-sandbox test environment.

- [ ] **Step 5: Perform a real read-only compatibility check**

Run:

```bash
cm host-key check guo1010hui-usw2
```

Expected: the reachable OpenSSH 10.2 host reports `current`, `missing`, or
`stale`, not `scan-failed`. Do not run `cm host-key fix` during this check.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/host_key.go internal/connectmac/logs.go internal/connectmac/app_local_agent.go internal/connectmac/app_test.go internal/connectmac/app_local_observability_test.go
git commit -m "fix: report host key scan fallback diagnostics"
```
