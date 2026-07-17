# ConnectMac Beijing Time Display Design

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

## Error Handling

If a server notification timestamp cannot be parsed, preserve the original value rather than dropping it. The web formatter likewise returns the original value for invalid input.

## Testing

- Verify UTC-to-Beijing conversion, including crossing midnight.
- Verify Enterprise WeChat fields and extension details contain Beijing time and no raw `Z` timestamp.
- Verify the browser formatter explicitly uses `Asia/Shanghai`.
- Verify `datetime-local` round-trips between Beijing wall-clock time and UTC.
- Run complete Go tests, race tests, vet, and JavaScript syntax validation.
