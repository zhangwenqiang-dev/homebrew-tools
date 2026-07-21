# Auto Release UI and Completion Notification Implementation Plan

## Task 1: Derive and enforce releasing state

- Add Web behavior tests for destroy-job/reminder state precedence.
- Add one shared browser helper that determines whether a profile is releasing.
- Render `releasing` before AWS `creating` and disable Open, Release, and Extend Reminder.
- Add a server-side conflict check before confirmed open.

## Task 2: Make completion notification durable

- Add a persisted notification-pending auto-release state without changing the database schema.
- Transition clean AWS resources into notification-pending.
- Send the existing WeChat completion webhook from notification-pending.
- On success call the existing atomic completion cleanup; on failure retain notification-pending and log the error for the next scan.
- Add coordinator and Web notifier integration tests.

## Task 3: Verify

- Run focused auto-release and Web tests.
- Run the full package test suite, race checks, vet, JavaScript syntax check, and `git diff --check`.
- Review that EIP allocations are never released and unrelated Safari HTTPS changes remain intact.
