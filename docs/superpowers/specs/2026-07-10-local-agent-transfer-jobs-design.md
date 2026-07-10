# Local Agent Transfer Jobs Design

## Problem

Web uploads currently keep one HTTP request open until `cm push` exits. The page shows a simulated progress value that stops at 95%. If the local agent is restarted during the request, rsync is interrupted, the request fails, and the page reports only "上传失败". The visible percentage does not prove that the transfer completed.

The observed failure ended while rsync was transferring a file and was immediately followed by local-agent startup messages. There was no successful rsync exit or completion response.

## Approaches Considered

1. Keep the synchronous request and improve the error text. This is small, but page refreshes and agent restarts can still lose the operation state.
2. Run transfers as local-agent jobs and poll their status. This decouples browser requests from rsync, supports page refresh, and allows normal service restarts to be blocked while a transfer is active.
3. Detach rsync into an independent persistent worker. This could survive an agent restart, but adds orphan-process cleanup, durable process recovery, and more operational risk than the current requirement needs.

Approach 2 is selected.

## Behavior

- Starting an upload or download creates a local transfer job and returns immediately with a job ID.
- The local agent owns the rsync process and keeps job state in memory.
- The page polls the job until it reaches `succeeded`, `failed`, or `interrupted`.
- Progress is derived from rsync output. It may be approximate while a file is active, but it reaches 100% only after rsync exits with code 0.
- A non-zero rsync exit shows its stderr/output and exit status. It must never be labeled complete just because files appeared remotely.
- Reloading the page can rediscover and resume displaying the active job for the selected profile and direction.
- Only one active transfer is allowed for the same profile and direction. A duplicate request returns the existing job instead of starting a second rsync.
- Completed jobs remain queryable for the local-agent process lifetime and are pruned after 24 hours.

## Local Agent API

- `POST /sync/push` and `POST /sync/pull` create or reuse a transfer job and return its current state.
- `GET /sync/job?id=<job-id>` returns one job.
- `GET /sync/jobs?profile=<name>` returns recent jobs and the active job for that profile.
- `GET /activity` reports whether transfers are active.

The job response includes:

- `id`
- `profile`
- `direction`
- `status`: `queued`, `running`, `succeeded`, `failed`, or `interrupted`
- `percent`
- `output`
- `error`
- `created_at`, `started_at`, and `finished_at`

## Service Lifecycle Safety

Before `cm local-agent stop`, `restart`, or `uninstall` removes the launch service, it checks `/activity` on the configured local-agent address. If a transfer is active, the command exits without stopping the service and identifies the active profile and direction. When the agent is unavailable, normal lifecycle behavior is unchanged.

An unexpected process crash cannot preserve an in-memory job. On the next startup, the browser reports that the previous connection was interrupted instead of claiming success. Surviving deliberate or unexpected service termination is outside this change; the lifecycle guard handles the normal upgrade and restart path that caused the observed failure.

## Web UI

- Replace the simulated timer with polled job progress.
- Keep the progress bar visible after success or failure.
- Show `上传完成` only at 100% after a successful exit.
- Show a specific interruption or rsync error message on failure.
- Disable the matching transfer button while its job is active.
- Restore the selected profile's active/recent transfer state when the transfer page opens.

## Validation

- Unit-test job creation, duplicate suppression, progress parsing, successful completion, and failed completion.
- Unit-test lifecycle refusal when `/activity` reports an active transfer.
- Verify that success cannot be returned before rsync exits with code 0.
- Verify that page refresh restores an active job.
- Verify that a failed transfer retains its real progress and detailed error instead of stopping at the synthetic 95% value.
- Run `go test ./...` and browser-level checks for upload success, upload failure, and active-transfer restart refusal.

## Compatibility

CLI `cm push` and `cm pull` remain synchronous and unchanged. The new job behavior applies only to local-agent web transfer endpoints. Existing profile, PEM, include, and exclude configuration remains unchanged.
