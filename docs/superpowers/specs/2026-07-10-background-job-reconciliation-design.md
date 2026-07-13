# Background Job Reconciliation and Upgrade Safety Design

## Problem

ConnectMac starts AWS open and destroy commands as background `cm job run` processes. On Linux these processes remain in the `connectmac.service` cgroup. Restarting the Web service terminates them. If the child dies before `RunJob` saves its terminal state, `job.json` remains `running` even though its PID no longer exists.

This happened to `aws-destroy-iossupport-usw2-20260710085332`: the job disassociated the retained Elastic IP and requested EC2 termination, then the staging service restart killed the waiting process before it could release the Dedicated Host or save a final status.

## Selected Approach

Use persistent job reconciliation plus an explicit pre-restart wait command.

Alternatives considered:

1. Reject every deployment while a job is active. This is safe but requires a person or agent to retry later.
2. Wait for active jobs with a bounded timeout, then continue only when none remain. This provides safe automatic progress without hiding a permanently stuck job.
3. Move each AWS job into an independent transient systemd unit so it survives Web restarts. This is more complex, Linux-specific, and changes process ownership; it is deferred until the simpler guard proves insufficient.

Approach 2 is selected.

## Job Status Model

Add the persisted status `interrupted`.

A running job is stale when:

- its status is `running`;
- it has a positive PID; and
- that PID is no longer running.

Reconciliation persists a stale job as:

- `status: interrupted`;
- `finished_at`: reconciliation time;
- `last_error`: `background process exited before recording completion`;
- no synthetic success or deferred result.

Reconciliation does not run the stored command again, call AWS, alter an Elastic IP, terminate EC2, or release a Dedicated Host.

## Commands

### `cm job active`

Lists only jobs whose refreshed status is `running`. The table includes job ID, type, profile, PID, and start time. It exits 0 even when the list is empty.

`cm job active --json` returns a JSON array for scripts and AI callers.

Before returning results, the command reconciles stale running records so dead processes are not reported as active.

### `cm job wait-all`

Usage:

```text
cm job wait-all [--timeout 2h] [--interval 10s]
```

Behavior:

- Reconcile first.
- If no active jobs remain, exit 0 immediately.
- While jobs are active, print one progress line per interval with job IDs and elapsed time.
- Reconcile on every poll.
- Exit 0 only when no active jobs remain.
- On timeout, print the still-active jobs and exit non-zero.
- Invalid or non-positive durations return usage error without waiting.
- Context cancellation returns non-zero and does not modify active jobs.

The default timeout is two hours and the default interval is ten seconds.

## Web Startup Reconciliation

Before `cm web` starts listening, call job reconciliation once. Log each changed job ID and its new `interrupted` status. A reconciliation read/write error prevents Web startup because silently ignoring stale state would make deployment safety unreliable.

The startup scan only changes stale local metadata. It never resumes a command.

## Deployment Workflow

The supported staging upgrade sequence first verifies and extracts the incoming package, then uses its new binary for the preflight. This also protects the first deployment when the installed `cm` does not yet provide `job wait-all`:

```bash
sha256sum -c <package-checksum>
dpkg-deb -x /tmp/cm_<version>_arm64.deb /tmp/cm-<version>-preflight
sudo -u root HOME=/var/lib/connectmac /tmp/cm-<version>-preflight/usr/sbin/cm job wait-all --timeout 2h --drain
sudo apt install -y /tmp/cm_<version>_arm64.deb
sudo systemctl restart connectmac
```

The wait command must use the same `HOME` and job directory as the service. The drain marker makes checking active jobs and rejecting new jobs atomic for drain-aware binaries. Package checksum failure, extraction failure, or wait timeout stops the deployment before APT installation or service restart. If installation or restart fails after draining, the script uses the incoming binary's hidden `job end-drain` recovery command so a failed deployment cannot permanently block future jobs. A successful Web startup removes the marker only after configuration, database setup, and listener binding succeed.

This repository will document or script that preflight, but it cannot prevent an administrator from bypassing it with a direct `systemctl restart`.

## Web Job Display

Existing Web job lists consume the reconciled persisted status. Add a visible `interrupted` label so a dead process is not shown as running or success. The job log remains available for diagnosis.

## Testing

- Reconcile a running job with a dead PID and verify the persisted `interrupted` fields.
- Keep a running job unchanged while its PID is alive.
- Verify reconciliation performs no command execution.
- Test `job active` text and JSON output.
- Test `job wait-all` immediate success, eventual success, timeout, invalid duration, and context cancellation with a controllable clock/poller.
- Test Web startup success after reconciliation and startup failure on reconciliation errors.
- Test Web rendering for `interrupted` jobs.
- Run `go test ./...`, race tests, vet, JavaScript syntax checks, and a staging dry run using a temporary job directory.

## Compatibility

Existing terminal statuses `success`, `failed`, and `deferred` remain unchanged. Existing job JSON remains readable because the new status is additive. AWS open/destroy safety behavior and Elastic IP retention are unchanged.
