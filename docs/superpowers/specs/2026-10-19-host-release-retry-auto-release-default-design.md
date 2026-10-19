# Host Release Retry And Auto-Release Default Design

## Goal

Fix manual and automatic AWS Mac release workflows so an AWS
`Client.InvalidHost.Occupied` result from `ReleaseHosts` remains retryable, and
enable automatic release by default for newly created release reminders.

## Scope

- Preserve the structured AWS error code returned through
  `ReleaseHostsOutput.Unsuccessful`.
- Reuse the existing bounded Dedicated Host release retry loop.
- Default newly created reminders to automatic release enabled.
- Preserve every existing reminder's current automatic-release setting.
- Keep Elastic IP allocations retained.

No migration, bulk update, UI redesign, or change to the one-hour Host release
retry limit is included.

## Host Release Error Contract

Introduce a small typed error for an unsuccessful Dedicated Host release. It
must implement `error` and `ErrorCode() string`, and retain the AWS code,
message, and Host ID without depending on message parsing.

`validateReleaseHostOutput` returns this typed error when the requested Host is
present in `ReleaseHostsOutput.Unsuccessful`. Existing malformed-response
checks remain ordinary terminal errors.

`releaseHostWithRetry` continues to classify only
`Client.InvalidHost.Occupied` as an in-progress Mac Host transition. It retries
with the existing backoff until success, cancellation, or the existing one-hour
deadline. Other AWS errors fail immediately. A repeated destroy after EC2 has
already terminated continues to target only the remaining managed Host and
retains the Elastic IP allocation.

## Automatic Release Default

When a successful open lifecycle creates a new active release reminder, set:

- `AutoReleaseEnabled = true`
- `AutoReleaseAt = ReleaseDueAt + AutoReleaseGracePeriod`
- `AutoReleaseState = scheduled`

The current grace period remains ten minutes. If a reminder already exists,
its explicit enabled/disabled choice is preserved. Extending a reminder keeps
the existing switch state and adjusts the schedule through the current logic.
No database migration changes old rows.

## Observability And Failure Handling

The existing job log must show each Host retry and the eventual release,
timeout, or cancellation. Structured job outcomes remain recoverable for an
occupied Host transition. No secrets or AWS credentials are added to logs.

## Tests

- Validate that a real SDK `UnsuccessfulItem` produces an error with
  `ErrorCode() == "Client.InvalidHost.Occupied"`.
- Validate that the real-output error enters `releaseHostWithRetry` and succeeds
  on a later attempt.
- Preserve tests proving non-occupied errors fail without retry.
- Validate that a newly created lifecycle reminder enables and schedules
  automatic release by default.
- Validate that updating an existing disabled reminder does not enable it.
- Run `go test ./... -count=1`, `go vet ./...`, and `git diff --check`.

## Rollback

The change is code-only. Reverting the implementation restores the old default
and error behavior; no schema or historical-data rollback is required.
