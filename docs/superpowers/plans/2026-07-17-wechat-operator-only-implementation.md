# WeChat Operator-Only Notification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the profile owner from every Enterprise WeChat webhook message while preserving the operation actor and all owner business data.

**Architecture:** Change only the shared WeChat Markdown rendering boundary. Keep the notification model and all callers unchanged so owner persistence, profile assignment, reminders, and automatic release continue to work exactly as before.

**Tech Stack:** Go, `net/http/httptest`, standard library tests.

---

## File Map

- Modify `internal/connectmac/wechat.go`: stop rendering the owner field.
- Modify `internal/connectmac/wechat_test.go`: verify the operator remains and owner data is absent from the rendered message.

### Task 1: Render Operator Without Owner

**Files:**
- Modify: `internal/connectmac/wechat.go`
- Modify: `internal/connectmac/wechat_test.go`

- [ ] **Step 1: Write the failing notification assertions**

In `TestWechatNotifierSendsMarkdown`, retain distinct values for the two fields:

```go
Owner:    "Profile Owner",
Operator: "Operation Actor",
```

Assert the operator is rendered and neither the owner label nor value is present:

```go
if !strings.Contains(content, "操作人：Operation Actor") {
	t.Fatalf("content missing operator: %s", content)
}
for _, forbidden := range []string{"负责人", "Profile Owner"} {
	if strings.Contains(content, forbidden) {
		t.Fatalf("content contains forbidden owner value %q: %s", forbidden, content)
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestWechatNotifierSendsMarkdown$' -count=1
```

Expected: FAIL because the shared Markdown renderer still emits `负责人：Profile Owner`.

- [ ] **Step 3: Remove owner rendering at the shared boundary**

In `WechatNotifier.markdown`, remove only this call:

```go
writeWechatField(&b, "负责人", notification.Owner)
```

Keep the operator call unchanged:

```go
writeWechatField(&b, "操作人", notification.Operator)
```

Do not remove `WechatNotification.Owner` or change any caller.

- [ ] **Step 4: Run notification and package tests**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run '^TestWechatNotifierSendsMarkdown$' -count=1
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -count=1
go vet ./internal/connectmac
git diff --check
```

Expected: all commands pass.

- [ ] **Step 5: Commit the implementation**

```bash
git add internal/connectmac/wechat.go internal/connectmac/wechat_test.go
git commit -m "fix: hide owner from webhook messages"
```
