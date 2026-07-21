# Auto Release UI and Completion Notification Design

## Goal

Represent an active automatic Mac release accurately, prevent conflicting user actions, and deliver a WeChat notification after AWS confirms that the release is complete.

## State and Actions

- A profile is `releasing` when its reminder is `running` or `retrying`, or when an active `aws-destroy` job exists for that profile.
- `releasing` takes precedence over the underlying AWS status label, including `wait-ready`; the UI must never show `creating` while a destroy flow is active.
- While releasing, Open, Release, and Extend Reminder are disabled. The server must also reject a confirmed open while the same profile is releasing, so stale pages cannot bypass the lock.
- Status refresh and event viewing remain available.

## Completion Notification

- Automatic release completion is recognized only after status confirms no managed host, no managed EC2 instance, and no EIP association/instance attachment. The EIP allocation remains retained.
- Completion sends the existing WeChat webhook message: `Mac 自动释放成功，Elastic IP 分配已保留`.
- A notification failure must be logged and retried by later background scans. Persist a notification-pending release state so a process restart cannot silently lose the completion notification.
- After successful delivery, finalize the reminder as released and clear the owner/reminder lifecycle records through the existing atomic completion path.

## Compatibility and Safety

- Do not change AWS destroy commands, resource ownership validation, retry timing, or EIP retention behavior.
- Existing scheduled, running, retrying, failed, and released records remain readable.
- Tests cover label precedence, button locking, server-side open rejection, successful notification, and notification retry.
