# AWS Job Completion Refresh and Notification Design

## Goal

Make confirmed Web open and release actions reflect the actual AWS Mac lifecycle:

- Starting a background job is not an open or release success.
- Open succeeds only after the managed Mac reports `ready=true`.
- Release succeeds only after the managed Mac reports `stopped`.
- Enterprise WeChat success notifications are sent only after those final states.
- The Web page refreshes affected profile state while the background operation is running.

The implementation must preserve the existing Elastic IP allocation during release.

## Current Problem

The confirmed Web action handler currently calls the lifecycle update immediately after a background job is created. As a result:

- the profile owner and release reminder can be updated before the Mac is usable;
- an open-success notification can be sent while AWS is still starting the Mac;
- a release-success notification can be sent before EC2 and the Dedicated Host are fully stopped;
- the Web page polls the job list but does not refresh the affected AWS profile status, so a Mac that becomes ready after several minutes remains stale on screen.

## Selected Approach

Use a server-side completion coordinator plus targeted Web polling.

Alternatives considered:

1. Refresh only in the browser. This fixes the visible stale state but loses lifecycle finalization when the page is closed.
2. Finalize immediately when the child process exits successfully. This is better than finalizing on enqueue, but a successful process exit still requires an AWS status check before declaring `ready` or `stopped`.
3. Persist the intended action with the background job, reconcile terminal jobs against AWS, and let the browser poll only for display. This is selected because it survives page closure and service restart.

## Persisted Job Intent

Add optional lifecycle metadata to background AWS jobs:

- requested owner email for an open action;
- lifecycle finalization state;
- finalization timestamp;
- notification timestamp;
- last finalization error.

The metadata is additive so existing job files remain readable.

Only Web-confirmed open jobs need a requested owner. For non-admin members, the server records the authenticated member email. For admins, the server records the explicitly selected owner. The browser-provided owner must never override the authenticated non-admin member.

## Completion Coordinator

Run a lightweight coordinator inside `cm web`. It executes at startup and periodically while unfinished AWS lifecycle jobs exist.

For each unfinalized AWS job:

### Open

1. A `starting` or `running` job remains pending.
2. A `failed` or `interrupted` job is recorded as failed and does not change ownership, reminders, or send a success notification.
3. A `success` job triggers a read-only AWS status check.
4. If `ready=false`, finalization remains pending and the coordinator checks again later.
5. If `ready=true`, the coordinator:
   - assigns the requested member to the profile;
   - stores the profile owner;
   - creates or refreshes the release reminder using the active host creation time;
   - records the lifecycle completion;
   - sends one Enterprise WeChat open-success notification.

The current profiles `ltx19850810-usw2` and `iossupport-usw2` keep their independent owners, 王恒辉 and 张会林 respectively.

### Release

1. A `starting` or `running` job remains pending.
2. A `failed` or `interrupted` job does not clear ownership, mark the reminder released, or send a success notification.
3. A `success` or `deferred` job triggers a read-only AWS status check.
4. Release is complete only when the profile is `stopped`: no managed active EC2 instance, no managed active Dedicated Host, and no Elastic IP association to the managed instance.
5. The Elastic IP allocation remains retained.
6. When stopped, the coordinator:
   - clears the profile owner;
   - marks the release reminder released;
   - records lifecycle completion;
   - sends one Enterprise WeChat release-success notification.

A deferred release remains pending until later reconciliation confirms `stopped`. The coordinator does not perform an unconfirmed AWS mutation.

## Idempotency and Recovery

Lifecycle finalization is keyed by the persisted job ID and state:

- finalized jobs are not finalized again;
- completed notifications are not sent again during ordinary polling or service restart;
- a temporary AWS status error records the error and retries later;
- a temporary webhook error is logged and may be retried without repeating ownership or reminder mutations.

The coordinator performs only read-only AWS status checks. It never creates a Dedicated Host, launches or terminates EC2, releases a host, changes a security group, creates a key pair, or releases an Elastic IP.

## Web Refresh

The existing 10-second job polling remains the trigger for UI updates.

While an AWS lifecycle job is active or waiting for final-state reconciliation, each poll refreshes:

- the job list;
- AWS status for the affected profile;
- visible profile ownership;
- release reminder state;
- action button availability.

The refresh is local to the current page. It does not reload the whole document or discard the user's login and current selection.

When a profile transitions to `ready` or `stopped`, the page updates on the next poll and stops polling that completed lifecycle when no other active work remains.

## User Feedback

After confirmation, the UI says that the background task has started. It must not say that the Mac opened or released successfully.

During reconciliation, the UI can distinguish:

- background task running;
- waiting for Mac readiness;
- waiting for release completion;
- completed;
- failed.

The final success notification text uses:

- `Mac 打开成功` only after `ready=true`;
- `Mac 释放完成` only after `stopped`.

## Error Handling

- Background job creation failure is returned immediately.
- Background job failure remains visible in the job list and log.
- AWS status errors keep finalization pending and are logged with profile and job ID.
- Missing owner metadata blocks open finalization and reports a clear error; it does not infer an owner from conversation or unrelated history.
- Notification failure does not roll back a completed AWS lifecycle or ownership update.
- Release finalization never deletes the Elastic IP allocation.

## Testing

Add tests for:

- starting a confirmed background open does not assign ownership, create an open reminder, or send an open-success notification;
- an open job that succeeds while `ready=false` remains pending;
- an open job plus `ready=true` assigns the correct owner and finalizes once;
- 王恒辉 and 张会林 remain associated with their respective profiles;
- a failed open job does not finalize;
- starting a confirmed release does not clear ownership or send a release-success notification;
- a release job with remaining EC2 or host resources remains pending;
- a release job with `stopped` status clears ownership, marks the reminder released, and finalizes once;
- release completion retains the Elastic IP allocation;
- repeated coordinator runs do not duplicate lifecycle mutations or success notifications;
- Web polling refreshes affected statuses, owners, reminders, and buttons without a full page reload;
- service restart resumes reconciliation of unfinished lifecycle jobs.

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Also run the existing JavaScript syntax and Web contract checks.

## Rollout

1. Add persisted job lifecycle metadata and backward-compatible loading.
2. Move open/release lifecycle updates out of the enqueue path.
3. Add the completion coordinator and tests.
4. Extend targeted Web polling and status labels.
5. Verify locally with simulated job and AWS states.
6. Publish and deploy only after the full test suite passes.
