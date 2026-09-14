# Host Key Scan Fallback Design

## Problem

`cm 0.1.149` uses `ssh-keyscan` to collect a remote SSH Host Key before the
user confirms its fingerprints. On the current macOS OpenSSH 10.2 client,
`ssh-keyscan` receives the remote OpenSSH 10.2 banner but exits with status 1
before returning a key. A normal `ssh` client completes the same key exchange.
The UI consequently reports a network failure even when port 22 is reachable.

## Design

Keep `ssh-keyscan` as the primary scanner. If it exits unsuccessfully or returns
no valid Host Key, run the system `ssh` client against the same host using a
new temporary `known_hosts` file. The fallback uses:

- batch mode and no interactive authentication;
- a bounded connection timeout;
- `StrictHostKeyChecking=accept-new` only for the temporary file;
- an empty global known-hosts source;
- no shell command construction;
- no access to the user's configured identity file.

The SSH process is expected to fail at authentication after writing the remote
Host Key. A valid key in the temporary file is therefore a successful scan even
when the SSH exit status is nonzero. If no valid key is written, return a scan
failure containing a bounded diagnostic from both scanning attempts.

The temporary file and directory use owner-only permissions and are removed on
every return path. The fallback never reads or writes the real
`~/.ssh/known_hosts` file.

## Trust Boundary

Collecting a key is not trusting it. Existing behavior remains unchanged:

1. The local Agent returns the complete normalized fingerprint set.
2. The page displays every fingerprint.
3. The user explicitly confirms the complete set.
4. The one-time challenge is consumed.
5. Only the confirmed key material is atomically installed in the real
   `known_hosts` file.

No automatic repair or silent trust is introduced.

## Observability

Host Key checks record the scanner method (`ssh-keyscan` or `ssh-fallback`) and
a stable error code. Diagnostics are bounded and sanitized. Public keys,
fingerprints, challenge tokens, temporary paths, and local usernames are not
written to logs.

The user-facing error distinguishes an unreachable SSH service from a scanner
compatibility failure when the available evidence permits it.

## Testing

Tests use temporary fake `ssh-keyscan` and `ssh` executables to verify:

- successful `ssh-keyscan` does not invoke the fallback;
- an empty or failed primary scan invokes the fallback;
- a fallback key is returned despite the expected authentication failure;
- no fallback key returns a bounded diagnostic;
- temporary files are removed;
- command arguments prevent writes to the real known-hosts file;
- context cancellation and timeout terminate the fallback;
- fingerprints still require the existing explicit confirmation flow;
- no sensitive key material appears in structured logs.

The implementation remains inside the existing runner and Host Key boundaries;
it adds no third-party dependency and does not change AWS behavior.
