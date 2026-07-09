# Mac Release Reminder Design

## Goal

Add a reminder-only lifecycle for opened AWS Mac machines. When a Mac is opened, ConnectMac records when the managed Dedicated Host was created and sets a default release reminder time 24 hours later. Members can extend that reminder from the web UI. At reminder time, ConnectMac sends an Enterprise WeChat notification, but it never releases AWS resources automatically.

## Non-Goals

- Do not automatically release Dedicated Hosts or terminate EC2 instances when the reminder expires.
- Do not expose the Enterprise WeChat webhook URL through the web API or frontend.
- Do not store the webhook URL in the database.
- Do not change existing open/destroy safety rules: release still requires an explicit user confirmation.

## Configuration

The Enterprise WeChat webhook is configured on the server, not in code:

```bash
CONNECTMAC_WECHAT_WEBHOOK_URL="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
CONNECTMAC_WEB_BASE_URL="https://cm.hsgitlab.xyz"
```

The preferred location is:

```bash
/etc/connectmac/.env
```

If `CONNECTMAC_WECHAT_WEBHOOK_URL` is empty, reminder and lifecycle actions still succeed. The server logs that notification is skipped.

`CONNECTMAC_WEB_BASE_URL` is used only to include a management-page link in notification text. If it is empty, notifications omit the link.

## Data Model

Add a server-side Mac reminder record keyed by profile name:

- `profile_name`
- `apple_email`
- `host_id`
- `host_created_at`
- `release_due_at`
- `owner_email`
- `owner_name`
- `last_extended_by_email`
- `last_extended_by_name`
- `last_extended_at`
- `last_notified_at`
- `status`

`status` values:

- `active`: reminder is active.
- `due_notified`: reminder became due and notification was sent.
- `released`: Mac was released by a confirmed release workflow.

The record is updated after successful confirmed open, extension, reminder notification, and confirmed release.

## Host Created Time

After a confirmed open succeeds, ConnectMac should read the current AWS status for the profile and pick the managed Dedicated Host creation time when AWS exposes it. If the host creation time is missing, use the successful open time as a fallback. The default reminder is:

```text
release_due_at = host_created_at + 24 hours
```

If a Mac is already ready and a confirmed open only re-checks readiness, the reminder record should be created or refreshed only when it does not already exist for the active host. Existing manual extensions must not be overwritten by a repeated ready check.

## Web UI

The profile detail area should show:

- Host created time
- Release reminder time
- Reminder status: active, due, or released
- Last extension operator and time when present

Add an `延长` action for ready Macs. Clicking it opens a date-time picker modal:

- The minimum selectable time is now.
- The default value is the current `release_due_at` when present, otherwise now plus 24 hours.
- Saving updates `release_due_at`, records the operator, closes the modal, refreshes the profile status area, and sends an Enterprise WeChat notification.

The UI must make clear that this is only a reminder time, not an automatic release time.

## Notifications

Use Enterprise WeChat robot webhook message type `markdown` when possible. Send notifications for:

1. Confirmed open success.
2. Reminder extension success.
3. Reminder due.
4. Confirmed release success.

Each notification should include:

- Event type
- Profile
- Apple account
- Owner
- Operator when relevant
- Host ID when available
- Host created time
- Release reminder time
- Management page link when configured

Notification failures must not fail the user action. They are logged for troubleshooting.

## Reminder Worker

Add a lightweight server-side worker to `cm web` / `connectmac.service`.

Worker behavior:

- Runs periodically, for example every minute.
- Finds active reminder records with `release_due_at <= now`.
- Sends the due notification once.
- Sets status to `due_notified` and `last_notified_at`.
- Does not call destroy, terminate, release-host, or any AWS mutation.

The worker should be idempotent so service restarts do not resend the same due notification repeatedly.

## Release Flow Integration

After a confirmed release succeeds, mark the reminder record as `released` and send a release-success notification. If the release is deferred or partially complete, do not mark the reminder as released until the release workflow reports success. Continue to preserve the existing Elastic IP retention guarantee.

## API

Add authenticated web APIs:

- `GET /api/release-reminders`
  - Returns reminders visible to the current user.
- `POST /api/release-reminder/extend`
  - Body: `profile`, `release_due_at`
  - Requires the user to have access to the profile.
  - Records the current logged-in member as the extension operator.

Admin users can see all reminders. Non-admin users only see reminders for profiles assigned to them.

## Error Handling

- Invalid date-time input returns a clear validation error.
- Extending an unknown profile returns a clear profile error.
- Extending a profile the user cannot access returns forbidden.
- Missing webhook configuration logs a warning and continues.
- Webhook HTTP failure logs status and response body with sensitive URL redacted.

## Testing

Add tests for:

- Creating or refreshing a reminder after confirmed open.
- Repeated ready/open checks do not overwrite an existing extended reminder.
- Extending a reminder updates due time and operator.
- Due worker sends one notification and marks the record `due_notified`.
- Confirmed release marks the reminder `released`.
- Missing webhook does not fail open, extend, due notification, or release.
- Non-admin users cannot see or extend profiles not assigned to them.

## Rollout

1. Add storage and API with tests.
2. Add notification sender with webhook URL loaded from server environment.
3. Integrate open and release events.
4. Add worker.
5. Add web UI display and extension modal.
6. Deploy to staging2 with webhook configured in `/etc/connectmac/.env`.
7. Verify notifications with a test profile before publishing Homebrew.
