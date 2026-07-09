# Mac Release Reminder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build reminder-only release tracking for opened AWS Macs, with manual extension and Enterprise WeChat notifications.

**Architecture:** Store reminder records in the existing member repository alongside profile owners and settings. Integrate confirmed web open/release flows with reminder upsert/release updates, run a lightweight due-reminder worker inside the web service, and expose a small authenticated API for the web UI. Notifications are sent server-side from environment configuration and never expose the webhook URL to clients.

**Tech Stack:** Go standard library HTTP/JSON/time, existing ConnectMac `MemberRepository`, MySQL member store, file-backed member store, `cm web`, vanilla HTML/CSS/JS frontend.

---

### Task 1: Reminder Storage

**Files:**
- Modify: `internal/connectmac/member_store.go`
- Modify: `internal/connectmac/member_store_mysql.go`
- Test: `internal/connectmac/member_store_test.go`

- [ ] Add `ReleaseReminder` and `PublicReleaseReminder` structs with profile, Apple email, host ID, host created time, due time, owner/operator fields, notification fields, and status.
- [ ] Add repository methods: `ListReleaseReminders(memberEmail string)`, `UpsertReleaseReminder(ReleaseReminder)`, `ReleaseReminder(profileName string)`, `MarkReleaseReminderDue(profileName, notifiedAt string)`, and `MarkReleaseReminderReleased(profileName, releasedAt string)`.
- [ ] Persist reminders in `members.json` via `MemberData.ReleaseReminders`.
- [ ] Add MySQL table `cm_release_reminders` and include it in `Load` and `Save`.
- [ ] Add tests for upsert, list filtering, due marking, and release marking.

### Task 2: Enterprise WeChat Notifier

**Files:**
- Create: `internal/connectmac/wechat.go`
- Test: `internal/connectmac/wechat_test.go`

- [ ] Add `CONNECTMAC_WECHAT_WEBHOOK_URL` and `CONNECTMAC_WEB_BASE_URL` environment readers.
- [ ] Implement markdown notification payloads for open, extend, due, and release events.
- [ ] Redact webhook URLs from errors/log output.
- [ ] Make missing webhook a no-op success with a clear skip result.
- [ ] Add tests using `httptest.Server`.

### Task 3: Web Open/Release Integration

**Files:**
- Modify: `internal/connectmac/app_web.go`
- Test: `internal/connectmac/app_test.go`

- [ ] After confirmed open success, inspect profile status and upsert an active reminder with `release_due_at = host_created_at + 24h` only when the active host has no existing reminder.
- [ ] Preserve an existing extended due time for the same active host.
- [ ] Send open-success notification after confirmed open success.
- [ ] After confirmed release success, mark the reminder `released` and send release-success notification.
- [ ] Do not mark released for preview, failed, deferred, or partial release.

### Task 4: Reminder APIs and Worker

**Files:**
- Modify: `internal/connectmac/app_web.go`
- Test: `internal/connectmac/app_test.go`

- [ ] Add `GET /api/release-reminders`.
- [ ] Add `POST /api/release-reminder/extend` with `profile` and RFC3339 `release_due_at`.
- [ ] Enforce profile access: admin sees all, members only see/extend assigned profiles.
- [ ] Add a worker function that finds active due reminders, sends one due notification, and marks `due_notified`.
- [ ] Start the worker from `cm web` / server startup.

### Task 5: Web UI

**Files:**
- Modify: `web/index.html`

- [ ] Add reminder fields to frontend state and load them with profile data refresh.
- [ ] Show host created time, release reminder time, reminder status, and last extension operator in the selected profile detail.
- [ ] Add an `延长` button for ready Macs.
- [ ] Add a date-time modal that saves via `/api/release-reminder/extend`.
- [ ] Make the UI copy explicit: this is a reminder time, not automatic release.

### Task 6: Verification and Release

**Files:**
- Modify: `Formula/cm.rb`

- [ ] Run focused tests for member store, webhook, and web APIs.
- [ ] Run `go test ./...`.
- [ ] Build `cm`.
- [ ] Package debs.
- [ ] Deploy to staging2 and set `/etc/connectmac/.env` webhook variables manually.
- [ ] Verify a test reminder notification.
- [ ] Commit, tag, push, publish Homebrew, and upgrade local cm.
