# Member Transfer History and Logging Design

## Goal

Persist upload and download records for Web-initiated local-agent transfers while enforcing strict member privacy:

- every transfer record belongs to the authenticated member who started it;
- members can list, reuse, and delete only their own records;
- administrators have no exception and can also see only their own transfer records;
- structured server and local-agent logs make transfer failures and stalled progress diagnosable.

## Current Problem

The existing sync history is stored in one server-side JSON file and contains no member identity. Its APIs filter only by profile. This allows records from different members to be mixed together.

The current Web transfer path runs rsync through the local agent. It does not provide a complete authenticated server-side lifecycle record for the local transfer job.

## Selected Approach

Use the authenticated browser session to coordinate a local transfer job with a server-side database record.

Alternatives considered:

1. Store history only on the local computer. This is simple but loses history across computers and cannot reliably bind records to a Web member.
2. Let the authenticated Web page create and update a server record while the local agent performs rsync. This is selected because the server controls member identity without giving the local agent a member token.
3. Give the local agent a member token and let it report directly to the server. This survives page closure better, but increases token storage and authentication risk and is not required for the first implementation.

## Data Model

Add a server-side transfer record keyed by a generated transfer record ID:

- `id`
- `member_id`
- `member_email`
- `profile_name`
- `apple_email`
- `direction`: `push` or `pull`
- `local_path`
- `remote_path`
- `local_job_id`
- `status`: `created`, `queued`, `running`, `succeeded`, `failed`, `interrupted`, or `unconfirmed`
- `percent`
- `error_summary`
- `created_at`
- `started_at`
- `finished_at`
- `updated_at`

The MySQL deployment stores these records in `cm_transfer_records`. The local member-store implementation receives a compatible representation for tests and non-MySQL development.

Transfer history is not deduplicated by path. Each actual transfer attempt creates a separate record so retries and failures remain visible.

## Identity and Authorization

The server derives `member_id` and `member_email` exclusively from the authenticated session or API token. It never accepts member identity from the request body.

All transfer-record operations apply an ownership predicate on the server:

```text
record.member_id = current_member.id
```

This applies to:

- list;
- detail;
- progress update;
- completion update;
- delete;
- reuse in the Web UI.

Supplying another member's record ID returns not found. Administrator role does not bypass this rule.

Profile authorization remains separate: the current member must also have access to the requested profile before creating a transfer record.

## Transfer Lifecycle

### Start

1. The Web page validates that the profile is ready and the local agent is online.
2. The Web page creates a server transfer record using the current authenticated session.
3. The Web page passes the returned transfer record ID to the local agent as a correlation ID and asks it to create the transfer job.
4. After receiving the local job ID, the Web page binds it to the server record and changes the record to `queued` or `running`.

If the server record cannot be created, the transfer does not start. If local-agent startup fails after record creation, the Web page marks the record `failed` with the sanitized startup error.

### Progress

The existing local-agent polling returns job status and percent. The Web page updates the corresponding server record when:

- the status changes; or
- progress crosses a meaningful milestone.

Milestones are `0`, `10`, `25`, `50`, `75`, `90`, `99`, and `100`. This avoids a database write for every rsync output chunk.

The server validates:

- the record belongs to the current member;
- the local job ID matches;
- percent is between 0 and 100;
- terminal states cannot return to an active state;
- progress cannot decrease for the same attempt.

### Completion

When the local agent reports a terminal state, the Web page sends the final status, final percent, elapsed time, and sanitized error summary.

- `succeeded` requires local-agent success and uses `100%`.
- `failed` and `interrupted` retain the last real percent.
- a record that cannot be reconciled after the page or local agent disappears is shown as `unconfirmed`, not success.

When the transfer page is reopened on the same computer, the Web page correlates active server records with local-agent jobs by `local_job_id` and resumes updates when possible.

## API

Add authenticated endpoints:

- `GET /api/transfer-records?profile=<profile>`
  - Returns only the current member's records.
- `POST /api/transfer-record/start`
  - Creates a record after validating profile access.
- `POST /api/transfer-record/update`
  - Updates status or progress for the current member's matching record and local job ID.
- `POST /api/transfer-record/delete`
  - Deletes only the current member's record.

The existing sync-history API is migrated or retired after the Web UI switches to the member-scoped endpoints.

## Web UI

The transfer page shows only the current member's records for the selected profile.

Each record displays:

- upload or download;
- source and destination;
- status;
- last progress;
- start and finish time;
- elapsed time;
- concise error summary when failed.

The existing `使用` action fills the transfer form from the selected record. `删除` removes only that record after a successful server response.

Refreshing the page reloads the authenticated member's records from the server.

## Structured Logging

Extend transfer logging with optional structured fields:

- transfer record ID;
- local job ID;
- member email;
- profile;
- Apple email;
- direction;
- status;
- percent;
- elapsed milliseconds.

Log these lifecycle events:

- `transfer.record.created`
- `transfer.local.started`
- `transfer.progress`
- `transfer.local.succeeded`
- `transfer.local.failed`
- `transfer.local.interrupted`
- `transfer.record.updated`
- `transfer.record.reconcile_failed`

Progress logging uses the same milestones as database updates to avoid excessive log volume.

Errors include the sanitized rsync and SSH cause. Logs must never contain:

- PEM contents;
- passwords;
- session cookies;
- API tokens;
- Enterprise WeChat webhook keys;
- private-key material.

Paths are logged because they are required to diagnose quoting, spaces, and destination errors. Log files retain the existing 30-day retention and export behavior.

## Error Handling

- Missing authentication returns unauthorized.
- Missing profile access returns forbidden.
- Another member's record ID returns not found.
- Local-agent start failure creates no false successful record.
- Server update failure remains visible in the page and is retried on the next poll.
- A local transfer can finish even if history persistence temporarily fails; the UI reports transfer outcome and record-update failure separately.
- Database errors include transfer record ID and local job ID in sanitized logs.

## Migration

Existing unowned JSON sync-history entries cannot be attributed safely and must not be exposed to members.

The rollout keeps the old file as a local backup but the new member-scoped API ignores those records. No historical entry is assigned to a member by inference.

## Testing

Add tests for:

- a member creates and lists their own upload and download records;
- two members using the same profile cannot see each other's records;
- an administrator cannot list, update, or delete another member's records;
- request bodies cannot spoof member identity;
- profile access is required before record creation;
- progress is monotonic and terminal states are immutable;
- failure and interruption retain the last real progress;
- page reload restores member-scoped records;
- same-computer reconciliation matches `local_job_id`;
- legacy unowned JSON history is not exposed;
- structured logs contain correlation fields and sanitized errors;
- logs do not contain tokens, cookies, webhook keys, passwords, or PEM contents.

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Also run the JavaScript syntax and Web contract checks.

## Rollout

1. Add database schema and member-store methods.
2. Add member-scoped APIs and authorization tests.
3. Connect the Web local-agent lifecycle to transfer records.
4. Replace the old history UI data source.
5. Add structured server and local-agent logging.
6. Verify two-member isolation and failure diagnostics locally.
7. Publish and deploy only after the complete test suite passes.
