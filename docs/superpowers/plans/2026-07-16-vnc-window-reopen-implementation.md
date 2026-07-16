# VNC Window Reopen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reuse a healthy profile tunnel while forcing every explicit VNC click to open a new visible macOS Screen Sharing window.

**Architecture:** Add a VNC-specific runner method instead of changing general URL opening. Strengthen `cm start` so saved managed state is reused only when target and tunnel mappings still match. Remove the browser's unsafe port-conflict fallback and add correlated local-agent logs.

**Tech Stack:** Go, macOS `open`, SSH local forwarding, persisted tunnel state, local-agent HTTP, embedded JavaScript.

---

### Task 1: Add a VNC-Specific Runner Operation

**Files:**
- Modify: `internal/connectmac/app.go`
- Modify: `internal/connectmac/runner.go`
- Modify: `internal/connectmac/app_connect.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Write a failing forced-window test**

Extend `fakeRunner` with a VNC target field and assert:

```go
if code := app.Run(context.Background(), []string{"open-vnc", "xcode-vnc", "--config", config}); code != 0 {
	t.Fatalf("open-vnc failed: %s", errOut.String())
}
if runner.vncTarget != "vnc://mac-user@localhost:5900" {
	t.Fatalf("vnc target = %q", runner.vncTarget)
}
```

Add an ExecRunner command-construction test expecting:

```text
open -n -a Screen Sharing vnc://mac-user@localhost:5900
```

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/connectmac -run 'Test.*OpenVNC'
```

Expected: FAIL because `Runner.OpenVNC` does not exist.

- [ ] **Step 3: Add `OpenVNC`**

Extend the interface:

```go
OpenVNC(ctx context.Context, target string) error
```

Implement on macOS:

```go
func (ExecRunner) OpenVNC(ctx context.Context, target string) error {
	cmd := exec.CommandContext(ctx, "open", "-n", "-a", "Screen Sharing", target)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
```

Keep `OpenURL` unchanged for browser opening. Change `runOpenVNC` to call `OpenVNC`.

- [ ] **Step 4: Run focused tests**

```bash
go test ./internal/connectmac -run 'Test.*OpenVNC'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectmac/app.go internal/connectmac/runner.go internal/connectmac/app_connect.go internal/connectmac/app_test.go
git commit -m "fix: force a new screen sharing window"
```

### Task 2: Match Managed Tunnel State Before Reuse

**Files:**
- Modify: `internal/connectmac/state.go`
- Modify: `internal/connectmac/app_connect.go`
- Modify: `internal/connectmac/state_test.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Write matching and restart tests**

Add tests for:

- same target and tunnels returns match;
- changed host returns mismatch;
- changed local/remote port returns mismatch;
- dead PID is removed and restarted;
- healthy mismatched state is stopped, removed, and restarted;
- healthy matching state is reused.

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/connectmac -run 'Test.*TunnelState|TestAppStart'
```

Expected: FAIL before matching logic.

- [ ] **Step 3: Add state matching**

Implement:

```go
func (s State) Matches(profile Profile) bool {
	if s.Profile != profile.Name {
		return false
	}
	if s.Target != fmt.Sprintf("%s@%s", profile.User, profile.Host) {
		return false
	}
	return reflect.DeepEqual(s.Tunnels, profile.Tunnels)
}
```

Compare tunnel slices explicitly by length and by `LocalPort`, `RemoteHost`, and `RemotePort` at each index.

- [ ] **Step 4: Reconcile state in `runStart`**

Behavior:

```go
if state, ok, err := a.StateManager.Load(stateKey); err != nil {
	// fail
} else if ok && state.Matches(profile) {
	// reuse
} else if ok {
	// stop only this managed PID, remove state, then validate/start current profile
}
```

If a port remains occupied after stale/mismatched managed state is handled, return the normal port-conflict error. Never discover and kill an arbitrary process.

- [ ] **Step 5: Run focused tests**

```bash
go test ./internal/connectmac -run 'Test.*TunnelState|TestAppStart'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/state.go internal/connectmac/app_connect.go internal/connectmac/state_test.go internal/connectmac/app_test.go
git commit -m "fix: validate managed tunnel before reuse"
```

### Task 3: Remove Unsafe Web Port Conflict Fallback

**Files:**
- Modify: `web/index.html`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add Web contract tests**

Assert the VNC flow:

- always calls `/start`;
- fails visibly when `/start` returns a port conflict;
- does not test AWS ready to suppress that error;
- calls `/open-vnc` after a successful start or reuse response;
- keeps the button disabled while busy.

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/connectmac -run 'TestAppWeb.*VNC'
```

Expected: FAIL because `isLocalPortInUseError` fallback remains.

- [ ] **Step 3: Simplify `startTunnel`**

Replace nested fallback logic with:

```javascript
const start = await localAgentAPI("/start", {
  method: "POST",
  body: JSON.stringify(localAgentPayload(profile))
});
const opened = await localAgentAPI("/open-vnc", {
  method: "POST",
  body: JSON.stringify(localAgentPayload(profile))
});
```

Remove `isLocalPortInUseError` if unused.

- [ ] **Step 4: Improve success messages**

Use `start.output` to distinguish tunnel reuse from a new tunnel and display:

```text
已复用 SSH 隧道并打开新的 VNC 窗口
```

or:

```text
已启动 SSH 隧道并打开 VNC
```

- [ ] **Step 5: Run Web tests**

```bash
go test ./internal/connectmac -run 'TestAppWeb.*VNC'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/index.html internal/connectmac/app_test.go
git commit -m "fix: report VNC port conflicts accurately"
```

### Task 4: Add Local-Agent VNC Logs

**Files:**
- Modify: `internal/connectmac/app_local_agent.go`
- Modify: `internal/connectmac/app_connect.go`
- Modify: `internal/connectmac/logs.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Write log tests**

Assert logs include:

- action `local-agent.vnc`;
- profile;
- local port;
- tunnel action `reused`, `started`, `restarted`, or `conflict`;
- PID when available;
- launch success/failure.

Assert no PEM contents, passwords, session tokens, or cookies.

- [ ] **Step 2: Run tests and verify failure**

```bash
go test ./internal/connectmac -run 'TestLocalAgentVNCLog'
```

Expected: FAIL before logging exists.

- [ ] **Step 3: Return structured tunnel action**

Refactor the internal start path to expose:

```go
type TunnelStartResult struct {
	Action string
	PID int
	Profile string
	LocalPorts []int
}
```

CLI output remains compatible. Local-agent handlers log the structured result.

- [ ] **Step 4: Add VNC launch logs**

Write one success or failure entry after `OpenVNC`. Sanitize command errors and log only the VNC local port, not credentials.

- [ ] **Step 5: Run focused tests**

```bash
go test ./internal/connectmac -run 'TestLocalAgentVNCLog|TestAppStart|Test.*OpenVNC'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectmac/app_local_agent.go internal/connectmac/app_connect.go internal/connectmac/logs.go internal/connectmac/app_test.go
git commit -m "feat: log local VNC tunnel lifecycle"
```

### Task 5: Full Verification and Manual macOS Check

**Files:**
- Modify only when verification reveals defects.

- [ ] **Step 1: Run complete tests**

```bash
go test ./...
go test -race ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 2: Run local-agent focused tests**

```bash
go test ./internal/connectmac -run 'TestAppStart|Test.*OpenVNC|TestLocalAgentVNC'
```

Expected: PASS.

- [ ] **Step 3: Perform manual non-AWS VNC lifecycle verification**

Using an already ready profile:

```bash
cm local-agent restart
cm start iossupport-usw2
cm open-vnc iossupport-usw2
```

Close only the Screen Sharing window, then run:

```bash
cm open-vnc iossupport-usw2
```

Expected: a new visible Screen Sharing window opens; the tunnel PID remains unchanged.

- [ ] **Step 4: Verify unmanaged conflict safety**

Occupy a test local port with a harmless listener and run `cm start` for a temporary profile using that port.

Expected: clear conflict error; the listener remains running.
