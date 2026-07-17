# WeChat Operator-Only Notification Design

## Goal

Enterprise WeChat webhook messages show the operation actor without exposing the profile owner.

## Scope

- Remove the `负责人` field from every Enterprise WeChat Markdown notification.
- Keep the `操作人` field and its current value unchanged.
- Keep owner data in notification objects, persistence, the management page, reminders, and automatic-release workflows.
- Do not change webhook configuration, delivery, event timing, or message types.

## Implementation

Change only the shared Markdown renderer in `internal/connectmac/wechat.go`. Because every webhook message passes through this boundary, removing the owner field there applies the rule consistently without changing business data.

Update `internal/connectmac/wechat_test.go` to verify that rendered Markdown:

- contains the operator label and value;
- does not contain the owner label;
- does not contain the configured owner value.

## Compatibility

This is a display-only change. Existing API contracts, database records, profile-owner assignment, reminder ownership, and automatic-release behavior remain unchanged.
