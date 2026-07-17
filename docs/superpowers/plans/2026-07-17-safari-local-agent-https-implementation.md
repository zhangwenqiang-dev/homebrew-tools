# Safari Local Agent HTTPS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make hosted ConnectMac local actions work in Safari by automatically installing a trusted per-computer certificate and serving the local agent over HTTPS/WSS.

**Architecture:** Add an isolated TLS-material module, inject macOS keychain operations at the App boundary, and make service-management clients resolve HTTP or HTTPS from installed material. The Web client probes HTTPS first, falls back to legacy HTTP for existing Chrome/Firefox installations, and shows explicit migration guidance when neither endpoint is reachable.

**Tech Stack:** Go standard library `crypto/ecdsa`, `crypto/x509`, `encoding/pem`, macOS `security`, LaunchAgent, vanilla JavaScript Fetch/WebSocket, Node behavior tests, Chromium/Firefox/WebKit browser checks.

---

## File Map

- Create `internal/connectmac/local_agent_tls.go`: TLS paths, certificate generation, validation, renewal, fingerprinting, and atomic persistence.
- Create `internal/connectmac/local_agent_tls_test.go`: certificate SAN, validity, permissions, reuse, renewal, and corruption tests.
- Modify `internal/connectmac/app.go`: injectable keychain trust operations for deterministic tests.
- Modify `internal/connectmac/app_local_agent.go`: install/uninstall lifecycle, TLS server, HTTPS-aware service management, and health verification.
- Modify `internal/connectmac/app_test.go`: LaunchAgent, install, keychain, HTTPS management, and Web source/Node behavior tests.
- Modify `web/index.html`: secure-first local-agent detection, legacy fallback, HTTPS/WSS selection, and visible migration guidance.

### Task 1: Generate and Maintain Local TLS Material

**Files:**
- Create: `internal/connectmac/local_agent_tls.go`
- Create: `internal/connectmac/local_agent_tls_test.go`

- [ ] **Step 1: Write failing TLS lifecycle tests**

Add table and lifecycle tests that use a temporary home directory and fixed clock. They must assert:

```go
material, changed, err := ensureLocalAgentTLS(home, time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC))
if err != nil { t.Fatal(err) }
if !changed { t.Fatal("first generation must report changed") }

leaf := parseCertificateFile(t, material.ServerCertPath)
if leaf.DNSNames[0] != "localhost" { t.Fatalf("DNSNames = %#v", leaf.DNSNames) }
for _, want := range []string{"127.0.0.1", "::1"} {
	if !certificateHasIP(leaf, want) { t.Fatalf("missing IP SAN %s", want) }
}
```

Also assert directory mode `0700`, private-key mode `0600`, certificate mode `0644`, a second call reuses byte-identical files with `changed=false`, a server certificate within 30 days of expiry renews without replacing the CA, and corrupt or partial material is repaired without preserving invalid files.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestLocalAgentTLS' -count=1
```

Expected: FAIL because the TLS lifecycle functions do not exist.

- [ ] **Step 3: Implement TLS material types and paths**

Create:

```go
type localAgentTLSMaterial struct {
	Dir            string
	CACertPath     string
	CAKeyPath      string
	ServerCertPath string
	ServerKeyPath  string
}

func localAgentTLSPaths(home string) localAgentTLSMaterial {
	dir := filepath.Join(home, ".connectmac", "local-agent", "tls")
	return localAgentTLSMaterial{
		Dir: dir,
		CACertPath: filepath.Join(dir, "ca.pem"),
		CAKeyPath: filepath.Join(dir, "ca-key.pem"),
		ServerCertPath: filepath.Join(dir, "server.pem"),
		ServerKeyPath: filepath.Join(dir, "server-key.pem"),
	}
}
```

Use ECDSA P-256 keys, random positive serial numbers, a ten-year CA, a one-year server certificate, `x509.ExtKeyUsageServerAuth`, DNS SAN `localhost`, and IP SANs `127.0.0.1` and `::1`.

- [ ] **Step 4: Implement validation, renewal, and atomic writes**

Implement:

```go
func ensureLocalAgentTLS(home string, now time.Time) (localAgentTLSMaterial, bool, error)
func loadLocalAgentTLS(home string, now time.Time) (localAgentTLSMaterial, error)
func localAgentCAFingerprint(path string) (string, error)
```

Validation must check certificate/key pairing, CA signature, SANs, server-auth usage, and remaining validity. Reuse a valid CA and renew only the server certificate when possible. Write each replacement through a same-directory temporary file, `Chmod`, `Sync`, `Rename`, and directory sync; never truncate a valid file in place.

- [ ] **Step 5: Run focused tests and commit**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestLocalAgentTLS' -count=1
git add internal/connectmac/local_agent_tls.go internal/connectmac/local_agent_tls_test.go
git commit -m "feat: generate local agent TLS certificates"
```

### Task 2: Manage macOS Keychain Trust Safely

**Files:**
- Modify: `internal/connectmac/app.go`
- Modify: `internal/connectmac/app_local_agent.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing keychain command tests**

Add an injectable App field:

```go
LocalAgentSecurityCommand func(context.Context, ...string) ([]byte, error)
```

Tests inject a recorder and assert install uses:

```text
security add-trusted-cert -r trustRoot -p ssl -k <home>/Library/Keychains/login.keychain-db <ca.pem>
```

and uninstall uses the exact CA SHA-1 fingerprint:

```text
security delete-certificate -Z <fingerprint> <home>/Library/Keychains/login.keychain-db
```

Tests must prove cancellation/failure returns a precise error and never reports successful trust or cleanup.

- [ ] **Step 2: Run keychain tests and verify failure**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestLocalAgent(Keychain|Trust|UninstallTLS)' -count=1
```

Expected: FAIL because trust lifecycle functions are absent.

- [ ] **Step 3: Implement keychain boundary**

Add default execution in `NewApp`:

```go
LocalAgentSecurityCommand: func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "security", args...).CombinedOutput()
},
```

Implement helpers that use the current user's login keychain, compare fingerprints before adding trust, and include command output in errors. Never use `-d`, the system keychain, `sudo`, or disabled TLS verification.

- [ ] **Step 4: Implement exact-certificate uninstall behavior**

Uninstall must stop/drain the agent first, delete the fingerprint-matched trust entry, and only then remove the TLS directory. If keychain deletion fails, keep TLS files and return a retryable error.

- [ ] **Step 5: Run tests and commit**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestLocalAgent(Keychain|Trust|UninstallTLS)' -count=1
git add internal/connectmac/app.go internal/connectmac/app_local_agent.go internal/connectmac/app_test.go
git commit -m "feat: manage local agent certificate trust"
```

### Task 3: Serve HTTPS and Use It in Service Management

**Files:**
- Modify: `internal/connectmac/app_local_agent.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing transport-selection tests**

Test a pure endpoint resolver:

```go
got := localAgentEndpoint(opts, true, "/health")
if got != "https://127.0.0.1:18765/health" { t.Fatalf("endpoint = %q", got) }
```

Add HTTPS server tests using generated material and an HTTP client rooted in `ca.pem`. Verify `/health`, `/activity`, `/activity/drain`, and `/activity/resume` work without `InsecureSkipVerify`.

- [ ] **Step 2: Run transport tests and verify failure**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestLocalAgent(HTTPS|Endpoint|StatusTLS|DrainTLS)' -count=1
```

Expected: FAIL because the local agent only serves and manages HTTP.

- [ ] **Step 3: Add TLS-aware server startup**

When complete installed material exists, call:

```go
server.ListenAndServeTLS(material.ServerCertPath, material.ServerKeyPath)
```

When no TLS directory exists during direct manual execution, retain `ListenAndServe` for backward compatibility. If the TLS directory exists but validation fails, refuse startup and print `run: cm local-agent install`.

- [ ] **Step 4: Centralize service-management endpoint and client selection**

Replace hard-coded `http://` construction in status, drain, resume, and activity checks with shared helpers. The HTTPS client must append `ca.pem` to a normal certificate pool and validate hostnames. Keep redirect refusal and existing active-transfer safeguards.

- [ ] **Step 5: Make install an end-to-end idempotent command**

Change install sequencing to:

```text
drain/stop existing service when present
ensure TLS material
ensure keychain trust
write LaunchAgent
bootstrap/kickstart LaunchAgent
poll HTTPS /health with a bounded timeout
report installed and ready
```

Before this sequence, recover the installed host and port from the existing plist whenever the user did not pass explicit overrides. Repeated install must reuse valid certificates, avoid duplicate trust entries, preserve custom host/port options, and restart safely. Health failure must return nonzero and print the HTTPS endpoint and log files.

- [ ] **Step 6: Run tests and commit**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestLocalAgent(HTTPS|Endpoint|StatusTLS|DrainTLS|InstallTLS)' -count=1
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -count=1
git add internal/connectmac/app_local_agent.go internal/connectmac/app_test.go
git commit -m "feat: serve local agent over HTTPS"
```

### Task 4: Probe HTTPS First and Preserve Legacy Browser Support

**Files:**
- Modify: `web/index.html`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing Web source and Node behavior tests**

Tests must assert the source contains both endpoints:

```js
secureURL: "https://127.0.0.1:18765"
legacyURL: "http://127.0.0.1:18765"
```

Execute extracted production detection helpers in Node and verify:

```text
secure succeeds -> selected URL is HTTPS
secure fails and legacy succeeds -> selected URL is HTTP
both fail -> offline reason contains cm local-agent install
```

Also verify WebSocket construction maps the selected HTTPS URL to `wss:`.

- [ ] **Step 2: Run Web tests and verify failure**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestAppWebLocalAgent(Secure|Fallback|Migration|WebSocket)' -count=1
```

Expected: FAIL because the page has one hard-coded HTTP endpoint.

- [ ] **Step 3: Implement secure-first detection**

Change local-agent state to track secure URL, legacy URL, selected URL, online status, and error reason. Probe each candidate with an abort timeout; commit the selected URL only after a valid `{ok:true}` health response.

All REST calls and terminal WebSockets must use the verified selected URL. Never silently switch endpoints in the middle of an active terminal or transfer.

- [ ] **Step 4: Show actionable offline state on desktop**

Remove the desktop rule that hides every `.local-action` while offline. Render Connect, VNC, and Transfer as disabled with title and visible status text:

```text
本机代理未连接，请运行 cm local-agent install
```

Keep `.local-action { display: none !important; }` inside the existing mobile media query because mobile devices cannot execute local Mac actions.

- [ ] **Step 5: Run Web tests and commit**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestAppWebLocalAgent(Secure|Fallback|Migration|WebSocket)' -count=1
sed -n '/^  <script>$/,/^  <\/script>$/p' web/index.html | sed '1d;$d' > /tmp/connectmac-index-inline.js
node --check /tmp/connectmac-index-inline.js
git add web/index.html internal/connectmac/app_test.go
git commit -m "fix: connect Safari to the secure local agent"
```

### Task 5: Full Regression and macOS Migration Verification

**Files:**
- Modify only if a verified defect requires a narrowly scoped fix.

- [ ] **Step 1: Run static and backend quality gates**

```bash
go test ./...
go test -race ./...
go vet ./...
git diff --check
```

Extract the inline script from `web/index.html` and run `node --check` against it. Expected: all pass.

- [ ] **Step 2: Run browser behavior checks**

Use a temporary Playwright workspace outside the repository. In Chromium, Firefox, and WebKit verify secure success, legacy fallback, offline migration guidance, desktop action-button visibility, mobile action hiding, and WSS terminal URL construction.

- [ ] **Step 3: Build and install the local candidate on macOS**

```bash
make build VERSION=dev-safari-tls
bin/cm local-agent install
bin/cm local-agent status
```

Expected install output identifies the one-time keychain trust step and finishes with a successful HTTPS health check. Expected status reports `https://127.0.0.1:18765/health`.

- [ ] **Step 4: Verify the hosted Safari workflow**

Open `https://cm.hsgitlab.xyz` in Safari and verify the page reports `本机代理已连接`; Connect, VNC, and Transfer are visible for a ready profile; terminal connects using WSS; and Chrome continues to work.

- [ ] **Step 5: Verify idempotency and cleanup in a controlled cycle**

Run install a second time and verify the CA fingerprint is unchanged and no second trust prompt appears. Do not run uninstall on the user's working agent during release verification; exercise exact trust removal through automated temporary-home tests.

- [ ] **Step 6: Final review and release commit**

Review the complete diff for certificate leakage, disabled verification, broad CORS, or private-key staging. Confirm no TLS files exist in `git status`. Commit any verification-only fix separately, then prepare the next patch release through the existing Homebrew/Apt pipeline.
