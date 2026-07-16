# VNC Window Reopen Design

## Goal

Make every VNC button click open a visible macOS Screen Sharing window, including when:

- the same profile's SSH tunnel is already running;
- the previous Screen Sharing window was manually closed;
- the Screen Sharing application process is still alive without a visible connection window.

The existing healthy SSH tunnel should be reused rather than stopped and recreated.

## Current Problem

The Web flow first runs `cm start` and then runs `cm open-vnc`.

When the tunnel is already running, `cm start` correctly returns `already started`. The current VNC opener then runs:

```text
open vnc://...
```

macOS LaunchServices can reuse the existing Screen Sharing application process without creating a new visible connection window. The command exits successfully, so the Web page reports `VNC 已打开` even though no window appears.

The Web page also contains a fallback that treats any local-port-in-use error as an existing valid connection when the AWS profile is ready. AWS readiness does not prove that the process occupying the local port is the desired managed tunnel.

## Selected Approach

Force a new Screen Sharing application instance for each explicit VNC open request while preserving tunnel reuse.

Alternatives considered:

1. Keep using plain `open`. This cannot guarantee a new window.
2. Use `open -n -a "Screen Sharing" <vnc-url>` for every VNC click. This is selected because it reliably creates a new application instance and visible window without Accessibility permission.
3. Inspect Screen Sharing windows through AppleScript and open only when no window exists. This is fragile across macOS versions and may require Automation or Accessibility permission.

## Tunnel Behavior

Before opening VNC:

1. Load the managed tunnel state for the requested profile.
2. If its PID is running and its target and tunnel definition still match the profile, reuse it.
3. If the saved PID is dead, remove stale state and start a new tunnel.
4. If the same profile's saved tunnel no longer matches the current host or port mapping, stop that managed tunnel, remove its state, and start the updated tunnel.
5. If the local port is occupied without matching managed state, return a clear conflict error. Do not kill an arbitrary process.

Closing a Screen Sharing window does not stop the SSH tunnel. A later VNC click reuses the healthy tunnel.

## Screen Sharing Launch

On macOS, the VNC opener uses:

```text
open -n -a "Screen Sharing" <vnc-url>
```

Each explicit click therefore requests a new visible Screen Sharing instance.

On non-macOS platforms, preserve the existing unsupported or platform-specific behavior.

The VNC URL continues to use the profile's configured local port and optional VNC username. Passwords are never placed in the URL or logs.

## Web Behavior

The VNC button remains disabled while its request is in progress to prevent accidental rapid double-clicks.

The Web flow:

1. asks the local agent to ensure the tunnel;
2. reuses or starts the tunnel;
3. asks the local agent to force-open a new Screen Sharing window;
4. reports success only when both steps succeed.

Remove the browser fallback that silently ignores a local-port conflict based only on AWS `ready=true`.

User-facing results distinguish:

- `已复用 SSH 隧道并打开新的 VNC 窗口`;
- `已启动 SSH 隧道并打开 VNC`;
- `本地端口被非当前托管隧道占用`.

## Logging

Add clear local-agent log entries for:

- requested profile;
- local VNC port;
- tunnel action: `reused`, `started`, `restarted`, or `conflict`;
- managed tunnel PID when available;
- Screen Sharing launch command result;
- sanitized error.

Logs must not include PEM contents, passwords, tokens, cookies, or other credential material.

## Error Handling

- A dead saved PID is treated as stale state and can be safely replaced.
- A healthy matching tunnel is never stopped merely because the user closed the Screen Sharing window.
- An unmanaged local-port conflict is reported and never force-killed.
- Failure to launch Screen Sharing does not stop a healthy tunnel.
- Failure to establish the tunnel prevents Screen Sharing from opening.

## Testing

Add tests for:

- repeated start reuses a healthy matching tunnel;
- dead tunnel state is removed and restarted;
- changed host or tunnel mapping restarts the profile's managed tunnel;
- unmanaged local-port conflict returns an error and does not call process stop;
- repeated `open-vnc` uses the forced-new-window macOS arguments;
- closing the VNC application window does not alter managed tunnel state;
- the Web flow no longer swallows arbitrary port conflicts;
- rapid repeated clicks cannot create concurrent requests;
- logs identify tunnel reuse and VNC launch without secrets.

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Also run the JavaScript syntax and Web contract checks.

## Rollout

1. Add forced Screen Sharing launch support to the runner.
2. Strengthen managed tunnel matching and stale-state handling.
3. Remove the unsafe Web port-conflict fallback.
4. Add local-agent lifecycle logs and tests.
5. Verify manually by opening VNC, closing the Screen Sharing window, and clicking VNC again.
6. Publish and deploy only after all tests pass.
