# ConnectMac Beijing Time and Desktop Browser Compatibility Design

## Goal

All user-visible ConnectMac timestamps use Beijing time (`Asia/Shanghai`) while persisted values, API request values, and scheduling calculations remain RFC3339 UTC.

The standard display format is:

```text
2026-07-16 16:03:24（北京时间）
```

## Scope

- Web profile status, Host creation, release reminder, automatic release, events, jobs, and transfer history.
- Enterprise WeChat fields and message details, including the previous reminder time in extension notifications.
- Existing UTC values already stored in JSON or MySQL.
- Desktop management-page compatibility for the latest two major versions of Safari, Firefox, and Chrome.

CLI machine-readable output, database columns, API payloads, reminder calculations, and AWS timestamps remain unchanged.

## Design

### Web

`formatTime` parses RFC3339 values and formats them with `Intl.DateTimeFormat` using the explicit `Asia/Shanghai` time zone. It must not depend on the browser or operating-system time zone.

The reminder `datetime-local` input displays Beijing wall-clock time. On submit, the selected Beijing wall-clock value is converted to its corresponding UTC RFC3339 value before calling the API.

Invalid or empty timestamps continue to use the existing fallback behavior.

### Server Notifications

A small shared formatter parses RFC3339 timestamps, converts them to a fixed Beijing location, and emits the standard display format. Enterprise WeChat notification fields use this formatter for Host creation and release reminder times.

The release-extension detail formats the old reminder time before composing the message. The stored old and new reminder values remain UTC.

### Data Integrity

- Do not rewrite existing records.
- Do not change reminder comparison, grace-period, retry, or automatic-release calculations.
- Do not change API schemas.
- Do not use the server's local time zone as an implicit default.

### Desktop Browser Compatibility

Keep the existing Bootstrap 5.3 page and apply progressive compatibility fixes instead of introducing a separate frontend application or browser-specific stylesheet forks.

The desktop layout must remain stable at these viewport sizes:

- 1280 x 720
- 1440 x 900
- 1920 x 1080
- 2560 x 1440
- 3840 x 2160

The content area uses a readable maximum width on large monitors while tables retain enough width for status and action columns. Sidebar, toolbar, table, dialog, terminal, and transfer controls must have explicit shrink and overflow behavior so Safari and Firefox do not clip buttons or force incoherent horizontal scrolling.

Compatibility work covers:

- Home profile table and action buttons.
- Member management dialogs and tables.
- Profile add/edit dialogs.
- Profile operation and automatic-release controls.
- Transfer history, path fields, and progress bars.
- Web terminal sizing and scrolling.
- Beijing-time display and `datetime-local` editing.

Existing mobile behavior remains supported. Desktop fixes must not reveal local-only Connect, VNC, or Transfer actions on mobile.

## Error Handling

If a server notification timestamp cannot be parsed, preserve the original value rather than dropping it. The web formatter likewise returns the original value for invalid input.

## Testing

- Verify UTC-to-Beijing conversion, including crossing midnight.
- Verify Enterprise WeChat fields and extension details contain Beijing time and no raw `Z` timestamp.
- Verify the browser formatter explicitly uses `Asia/Shanghai`.
- Verify `datetime-local` round-trips between Beijing wall-clock time and UTC.
- Run Chromium, Firefox, and WebKit interaction and screenshot checks at the five desktop viewport sizes.
- Check that primary tables, dialogs, operation controls, transfer views, and the terminal have no overlap, clipped text, or unintended page-level horizontal overflow.
- Re-run representative mobile viewport checks to detect regressions.
- Run complete Go tests, race tests, vet, and JavaScript syntax validation.
