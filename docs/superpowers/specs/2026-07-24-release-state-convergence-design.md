# Release State Convergence Design

## Problem

An already released Mac can still appear as `releasing` when the release reminder
contains contradictory fields:

- `status: released`
- `auto_release_state: running`

The web UI currently treats `running`, `retrying`, or `notifying` as active before
considering the terminal reminder status. Status polling also calls cleanup for an
already-clean profile and records a new `cleanup-records` event on every poll.

## Design

Use three layers of protection:

1. `MarkReleaseReminderReleased` must converge every reminder to the terminal
   release state by setting `status`, `released_at`, `auto_release_state`, and
   clearing any stale automatic-release error.
2. The web UI must treat `status: released` or
   `auto_release_state: released` as terminal and never label it `releasing`, even
   when older inconsistent database rows are encountered.
3. Automatic status cleanup must be idempotent. If the reminder is already
   released and no profile owner exists, it must return without writing another
   `cleanup-records` event.

Existing automatic-release completion, persistent webhook retry, AWS resource
checks, and EIP preservation behavior remain unchanged.

## Data Flow

When AWS status confirms that no managed host, instance, or EIP association
remains, cleanup clears an existing owner and converges an existing reminder to
the terminal release state. It writes one cleanup event only when it changed the
owner or reminder. The next UI refresh receives terminal fields and renders
`stopped`/`released`, not `releasing`.

## Error Handling

Database and owner lookup errors continue to fail cleanup and are written to the
existing application log. No AWS mutation is introduced by this change.

## Tests

- File and MySQL member stores converge stale `running` reminders when marked
  released.
- Web UI ignores stale active auto-release fields on a released reminder.
- Repeated automatic cleanup is a no-op after the first successful cleanup and
  does not append duplicate operation events.
- Existing automatic-release and web test suites continue to pass.
