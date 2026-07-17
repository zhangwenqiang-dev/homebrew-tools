# Safari Local Agent HTTPS Design

## Goal

Make ConnectMac local actions work from the hosted HTTPS management page in Safari while preserving Chrome and Firefox compatibility and keeping terminal, VNC, and transfer traffic on the user's computer.

## Problem

The hosted management page runs at `https://cm.hsgitlab.xyz`, but the local agent currently listens at `http://127.0.0.1:18765`. Safari/WebKit blocks this HTTPS-to-HTTP loopback access. The health probe therefore reports the local agent offline, and the page hides Connect, VNC, and Transfer actions even though the agent is running.

CORS cannot fix a request that the browser blocks as mixed content before it reaches the local agent.

## Architecture

The local agent will serve HTTPS and secure WebSocket traffic using a certificate authority generated specifically for the current computer. `cm local-agent install` owns the full lifecycle: create or reuse certificates, add the CA to the current user's macOS login keychain, install the LaunchAgent, start it, and verify its HTTPS health endpoint.

The hosted Web client probes the secure local endpoint first. It temporarily falls back to the existing HTTP endpoint for previously installed Chrome and Firefox agents, allowing a gradual migration. Safari users with an old agent receive an explicit installation instruction instead of silently losing local action buttons.

## Certificate Lifecycle

TLS material lives under:

```text
~/.connectmac/local-agent/tls/
  ca.pem
  ca-key.pem
  server.pem
  server-key.pem
```

Rules:

- The directory uses mode `0700`.
- CA and server private keys use mode `0600`.
- Public certificate files use mode `0644`.
- The CA is unique to the current computer and has a long validity period.
- The server certificate is signed by that CA, has a shorter validity period, and contains SAN entries for `localhost`, `127.0.0.1`, and `::1`.
- Re-running `cm local-agent install` reuses valid TLS material.
- An expiring server certificate is renewed using the existing CA, avoiding another trust prompt.
- Missing, invalid, mismatched, or partially written material is repaired by the install flow.
- TLS files are written atomically so interruption cannot leave a valid-looking partial configuration.

## Keychain Trust

On macOS, installation uses the system `security` command to add the generated CA as an SSL trust root in the current user's login keychain. macOS may request the user's password or Touch ID; this confirmation cannot and must not be bypassed.

The certificate is identified by its stored fingerprint rather than only by a common name. `cm local-agent uninstall` stops the LaunchAgent, removes that exact trust entry, deletes the generated TLS directory, and leaves unrelated certificates untouched.

If keychain trust installation fails or is cancelled, installation fails with a precise recovery message and does not report the agent as Safari-ready.

## Local Agent Transport

When complete TLS material exists, the local agent:

- serves `https://127.0.0.1:18765`;
- serves terminal WebSockets over `wss://127.0.0.1:18765`;
- prints the HTTPS URL at startup;
- rejects startup when TLS material is incomplete or invalid, with instructions to rerun `cm local-agent install`.

Direct manual `cm local-agent` execution without installed TLS material may continue serving HTTP for backward compatibility and local diagnostics. An installed LaunchAgent always uses the generated TLS material.

Service-management commands use a shared endpoint resolver so `status`, activity checks, drain, resume, stop, restart, and install verification select HTTPS when TLS is installed. The Go HTTP client continues normal certificate validation through the macOS trust store; it never disables TLS verification.

## Web Client Migration

The Web client stores both endpoints:

```text
secure: https://127.0.0.1:18765
legacy: http://127.0.0.1:18765
```

Detection behavior:

1. Probe the secure health endpoint.
2. If it succeeds, use HTTPS for REST calls and WSS for terminal connections.
3. If it fails, probe the legacy endpoint for Chrome and Firefox compatibility.
4. If both fail, mark the local agent unavailable and show `请运行 cm local-agent install`.

The selected base URL is reused by VNC, terminal, transfer, directory selection, activity monitoring, and WebSocket construction. Local action controls remain unavailable until one endpoint is verified; the page must explain why rather than presenting Safari as if the agent were not installed.

## Origin Security

The existing local-agent CORS allowlist remains restricted to:

- `https://cm.hsgitlab.xyz`;
- local ConnectMac pages on `127.0.0.1` or `localhost`.

HTTPS does not broaden the allowed origins. Private keys never leave the local computer, and no terminal or transfer payload is proxied through staging2.

## Compatibility

- Existing Chrome and Firefox users continue working through the temporary HTTP fallback until they reinstall the local agent.
- Newly installed agents use HTTPS in Safari, Chrome, and Firefox.
- Existing profile, identity-file, terminal, VNC, transfer, and AWS behavior is unchanged.
- Existing LaunchAgent host and port overrides remain supported.

## Testing

Automated coverage will verify:

- CA and server certificate generation, SANs, validity, file permissions, and reuse;
- server-certificate renewal without replacing a valid CA;
- keychain command construction and failure handling without modifying the developer's real keychain;
- idempotent install and exact-certificate uninstall cleanup;
- LaunchAgent installation and HTTPS health verification;
- HTTPS endpoint selection for status, drain, resume, stop, and restart;
- HTTPS REST and WSS URL selection in the Web client;
- secure-first and legacy fallback behavior;
- explicit migration guidance when neither endpoint works;
- CORS behavior for the hosted origin;
- full Go tests, race tests, vet, JavaScript syntax checks, and browser checks in Chromium, Firefox, and WebKit.

Manual release verification will run `cm local-agent install` on macOS, approve the one-time keychain prompt, open `https://cm.hsgitlab.xyz` in Safari, and confirm Connect, VNC, Transfer, and terminal actions are restored.

## Failure Handling

- Certificate generation failure: leave the previous valid material intact and report the exact error.
- Keychain confirmation cancelled: stop installation and print the retry command.
- HTTPS health verification failure: report the endpoint and log location; do not claim installation success.
- Active transfer during reinstall or uninstall: preserve the existing drain/force behavior.
- Certificate removal failure during uninstall: report it and preserve TLS files so the user can retry cleanup safely.
