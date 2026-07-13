# Auto Release After Reminder Design

## Goal

Allow an administrator to enable automatic Mac release independently for each managed Profile. When a due reminder is sent, ConnectMac waits ten minutes. If no member extends the release time to at least ten minutes after the extension request, ConnectMac automatically runs the existing safe destroy workflow.

Automatic release is opt-in and disabled for every existing and new Profile by default. The workflow never releases an Elastic IP allocation.

## User Experience

The Profile management UI adds an administrator-only `自动释放` switch. Enabling it is the persistent authorization for future scheduled releases of that Profile; the worker does not require an interactive confirmation at execution time.

The reminder display shows one of these states:

- `未开启自动释放`
- `等待提醒`
- `将在 <time> 自动释放`
- `正在自动释放`
- `释放重试中（第 N 次）`
- `自动释放失败`
- `已释放`

When the reminder becomes due, the Enterprise WeChat notification states that automatic release will begin in ten minutes and includes the Profile, Apple account, owner, and exact automatic-release time.

During the ten-minute grace period, any member with access to the Profile may extend it. The new `release_due_at` must be at least ten minutes later than the server's current time. A valid extension cancels the current automatic-release schedule, resets retry state, returns the reminder to `active`, and sends the existing extension notification. A shorter extension is rejected with a clear validation error.

## Persisted State

Extend `ReleaseReminder` and `cm_release_reminders` with:

- `auto_release_enabled`: administrator-controlled boolean, default false.
- `auto_release_at`: reminder notification time plus ten minutes.
- `auto_release_started_at`: first attempt time for the current cycle.
- `auto_release_last_attempt_at`: most recent attempt time.
- `auto_release_attempts`: number of attempts in the current cycle.
- `auto_release_last_error`: most recent failure reason.
- `auto_release_state`: empty, scheduled, running, retrying, failed, or released.

The schema migration must preserve all existing reminder rows and leave `auto_release_enabled` false. JSON-file storage remains backward compatible because absent fields decode to zero values.

## Worker Flow

The existing one-minute reminder worker remains the single scheduler.

1. For an `active` reminder whose `release_due_at` has arrived, send the due notification.
2. Mark it `due_notified`. If automatic release is enabled, atomically set `auto_release_at = notification_time + 10 minutes` and `auto_release_state = scheduled`.
3. On later scans, ignore scheduled reminders until `auto_release_at` arrives.
4. At execution time, atomically claim the same reminder cycle. Re-read the row and refuse the claim if it was extended, disabled, released, cleaned, already claimed, or its schedule changed.
5. Resolve the stored Profile and explicit Apple account, run a fresh AWS status/preview check, and only then invoke the existing confirmed background destroy path.
6. The destroy path may disassociate the EIP, terminate managed EC2, and release managed Dedicated Hosts. It must retain the EIP allocation exactly as the manual release workflow does.
7. After each attempt, re-read AWS status and persist the outcome. If no managed instance or host remains, mark released and clear owner/reminder records through the existing cleanup path.

The worker must not execute a command assembled from untrusted database text. It calls typed internal application/service methods using the persisted Profile and Apple email.

## Retry Policy

Retries occur every five minutes for at most one hour measured from `auto_release_started_at`.

Before every retry, ConnectMac rechecks:

- Automatic release is still enabled.
- The reminder cycle and `auto_release_at` are unchanged.
- No valid extension has occurred.
- No equivalent active destroy Job already exists.
- AWS resources still belong to the expected Profile and Apple account.

Recoverable states include AWS throttling, temporary API/network errors, EC2 `shutting-down`/`terminated` transitions, and Dedicated Host `pending`/temporarily-not-releasable transitions. These enter `retrying` and retain diagnostic details.

Configuration errors, missing AWS credentials/profile, authorization failures, ambiguous resource ownership, Apple-account mismatch, or a different managed resource replacing the original cycle are terminal. They enter `failed` immediately.

Unknown failures may retry within the one-hour window after the state checks above. At one hour, retries stop, state becomes `failed`, and an administrator notification includes the final error and manual follow-up guidance.

Only the first failed attempt and the final failure send failure notifications, avoiding five-minute notification spam. Successful release sends one completion notification.

## Concurrency And Restart Safety

All schedule, extension, claim, and attempt-result updates use an atomic store operation. A valid extension racing with a worker claim wins if persisted before the claim. Once a destroy attempt has begun, an extension cannot undo AWS mutations; the UI must report that release is already running instead of accepting the extension.

The persisted timestamps allow the worker to resume scheduled or retrying work after service restart. A stale `running` auto-release state with no active Job is reconciled to `retrying`, subject to the same one-hour deadline. Duplicate active destroy Jobs for one Profile are prohibited.

The staging deployment drain added in version 0.1.120 treats automatic-release Jobs like every other active background Job and waits for them before restart.

## API And Permissions

Add an administrator-only endpoint to update `auto_release_enabled` for one Profile. The reminder list response exposes the automatic-release fields needed by the UI. Operators and viewers may see state but cannot toggle the setting.

Disabling automatic release cancels a scheduled or retrying cycle when no destroy Job is running. It does not terminate or roll back a release already in progress.

## Logging And Notifications

Structured logs and operation events cover:

- automatic release enabled/disabled and the administrator identity;
- schedule creation/cancellation;
- valid extension cancellation;
- claim and each attempt number;
- retry reason and next retry time;
- terminal failure or one-hour timeout;
- successful release with `eip_retained=true`.

Enterprise WeChat notifications cover due/scheduled, extension, first retry failure, terminal failure, and success.

## Testing

- Existing reminders migrate with automatic release disabled.
- Due notification schedules exactly ten minutes from the successful notification timestamp.
- Extensions shorter than ten minutes are rejected; valid extensions cancel the cycle.
- Worker does nothing while disabled or before the grace period ends.
- Atomic claim prevents extension/release races and duplicate Jobs.
- Recoverable failures retry every five minutes and stop after one hour.
- Terminal failures stop immediately.
- Service restart resumes scheduled/retrying state without duplicate release.
- Successful and partial destroy paths retain the Elastic IP allocation.
- Only authorized administrators can toggle automatic release.
- UI renders states and disables invalid controls on desktop and mobile.
- Full Go, race, vet, JavaScript, and Web handler tests pass without real AWS mutations.

## Non-Goals

- No global default-on switch.
- No automatic creation or opening of Mac resources.
- No Elastic IP release.
- No rollback after AWS destruction begins.
- No retry beyond one hour without a new administrator action.
