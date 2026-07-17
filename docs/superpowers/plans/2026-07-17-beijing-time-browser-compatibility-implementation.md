# Beijing Time and Desktop Browser Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Display every user-facing timestamp in Beijing time and make the existing management page stable in modern Safari, Firefox, and Chrome from 1280 x 720 through 4K without changing UTC persistence or mobile behavior.

**Architecture:** Keep RFC3339 UTC as the storage and API contract. Add one Go display formatter for Enterprise WeChat, explicit `Asia/Shanghai` formatting and Beijing wall-clock conversion in the Web client, then apply narrowly scoped CSS and table wrappers to the existing Bootstrap 5.3 page. Browser verification runs from a temporary Playwright workspace so no browser-test dependency enters the shipped binary or Homebrew formula.

**Tech Stack:** Go, standard library `time`, Bootstrap 5.3, vanilla JavaScript `Intl.DateTimeFormat`, xterm.js, Go tests, Node syntax tests, Playwright Chromium/Firefox/WebKit.

---

## File Map

- Create `internal/connectmac/display_time.go`: user-facing Beijing-time formatting only.
- Create `internal/connectmac/display_time_test.go`: conversion, invalid-input, and midnight-boundary tests.
- Modify `internal/connectmac/wechat.go`: format Host and reminder fields at notification render time.
- Modify `internal/connectmac/wechat_test.go`: assert Beijing notification output.
- Modify `internal/connectmac/app_web.go`: format the old reminder time in extension descriptions.
- Modify `internal/connectmac/app_web_auto_release_test.go`: verify extension notifications contain Beijing time while stored values remain UTC.
- Modify `web/index.html`: explicit Beijing Web formatting, datetime round-trip helpers, table wrappers, and desktop compatibility CSS.
- Modify `internal/connectmac/app_test.go`: Web source contracts and executable Node behavior checks.

### Task 1: Server Beijing Display Formatter

**Files:**
- Create: `internal/connectmac/display_time.go`
- Create: `internal/connectmac/display_time_test.go`

- [ ] **Step 1: Write failing formatter tests**

Add table-driven tests covering normal conversion, crossing midnight, an RFC3339 offset input, empty input, and invalid input:

```go
func TestFormatBeijingDisplayTime(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-07-16T08:03:24Z", "2026-07-16 16:03:24（北京时间）"},
		{"2026-07-16T18:30:00Z", "2026-07-17 02:30:00（北京时间）"},
		{"2026-07-16T16:00:00+08:00", "2026-07-16 16:00:00（北京时间）"},
		{"", ""},
		{"not-a-time", "not-a-time"},
	}
	for _, test := range tests {
		if got := formatBeijingDisplayTime(test.input); got != test.want {
			t.Fatalf("formatBeijingDisplayTime(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify failure**

Run:

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run TestFormatBeijingDisplayTime -count=1
```

Expected: FAIL because `formatBeijingDisplayTime` does not exist.

- [ ] **Step 3: Implement the minimal isolated formatter**

Use a named fixed Beijing zone so packaged binaries do not depend on external timezone data:

```go
package connectmac

import (
	"strings"
	"time"
)

var beijingDisplayLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func formatBeijingDisplayTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.In(beijingDisplayLocation).Format("2006-01-02 15:04:05") + "（北京时间）"
}
```

- [ ] **Step 4: Run formatter tests**

Run the command from Step 2.

Expected: PASS.

- [ ] **Step 5: Commit the formatter**

```bash
git add internal/connectmac/display_time.go internal/connectmac/display_time_test.go
git commit -m "feat: format user times in Beijing timezone"
```

### Task 2: Enterprise WeChat and Reminder Extension Messages

**Files:**
- Modify: `internal/connectmac/wechat.go`
- Modify: `internal/connectmac/wechat_test.go`
- Modify: `internal/connectmac/app_web.go`
- Modify: `internal/connectmac/app_web_auto_release_test.go`

- [ ] **Step 1: Add failing notification assertions**

Extend `TestWechatNotifierSendsMarkdown` with both raw timestamps:

```go
HostCreatedAt: "2026-07-16T08:03:24Z",
DueAt:         "2026-07-17T16:00:00Z",
```

Assert the Markdown contains:

```go
for _, want := range []string{
	"Host 创建时间：2026-07-16 16:03:24（北京时间）",
	"释放提醒时间：2026-07-18 00:00:00（北京时间）",
} {
	if !strings.Contains(content, want) {
		t.Fatalf("content missing %q:\n%s", want, content)
	}
}
if strings.Contains(content, "T08:03:24Z") || strings.Contains(content, "T16:00:00Z") {
	t.Fatalf("content leaked UTC display values:\n%s", content)
}
```

Add or extend the release-reminder HTTP test so the notifier receives this description:

```text
释放提醒已延长（原时间：2026-07-17 17:17:07（北京时间））
```

while the returned and persisted `release_due_at` remains RFC3339 UTC.

- [ ] **Step 2: Run focused tests and verify failure**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'TestWechatNotifierSendsMarkdown|TestAppWebReleaseReminder' -count=1
```

Expected: FAIL because raw UTC strings are still rendered.

- [ ] **Step 3: Format only at notification composition boundaries**

In `WechatNotifier.markdown`, change only the two time fields:

```go
writeWechatField(&b, "Host 创建时间", formatBeijingDisplayTime(notification.HostCreatedAt))
writeWechatField(&b, "释放提醒时间", formatBeijingDisplayTime(notification.DueAt))
```

In the release reminder extension handler, format the copied old value before composing the description:

```go
oldDueAtDisplay := formatBeijingDisplayTime(oldDueAt)
a.notifyReleaseReminder("extend", reminder, member.Name, "释放提醒已延长（原时间："+oldDueAtDisplay+"）")
```

Do not change `ReleaseDueAt`, `HostCreatedAt`, API JSON, or reminder calculations.

- [ ] **Step 4: Run notification and reminder tests**

Run the command from Step 2.

Expected: PASS.

- [ ] **Step 5: Commit notification formatting**

```bash
git add internal/connectmac/wechat.go internal/connectmac/wechat_test.go internal/connectmac/app_web.go internal/connectmac/app_web_auto_release_test.go
git commit -m "feat: show Beijing time in release notifications"
```

### Task 3: Web Beijing Formatting and Datetime Round Trip

**Files:**
- Modify: `web/index.html`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing Web contracts and Node behavior tests**

Add source-contract assertions for:

```js
timeZone: "Asia/Shanghai"
hourCycle: "h23"
function beijingDateTimeLocalToISOString(value)
```

Add a Node harness that extracts the time helpers from `web/index.html` and verifies:

```js
assert.equal(formatTime("2026-07-16T08:03:24Z"), "2026-07-16 16:03:24（北京时间）");
assert.equal(formatTime("2026-07-16T18:30:00Z"), "2026-07-17 02:30:00（北京时间）");
assert.equal(toDateTimeLocal("2026-07-17T08:00:00Z"), "2026-07-17T16:00");
assert.equal(beijingDateTimeLocalToISOString("2026-07-17T16:00"), "2026-07-17T08:00:00.000Z");
assert.equal(formatTime("invalid"), "invalid");
```

- [ ] **Step 2: Run the Web time tests and verify failure**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'TestAppWeb.*Beijing|TestAppWeb.*DateTime' -count=1
```

Expected: FAIL because the page currently follows the browser's local timezone.

- [ ] **Step 3: Implement explicit Web timezone helpers**

Create a reusable formatter and build the stable string from `formatToParts` so Safari and Firefox punctuation differences cannot change output:

```js
const beijingTimeFormatter = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hourCycle: "h23"
});

function beijingDateParts(date) {
  return Object.fromEntries(beijingTimeFormatter.formatToParts(date)
    .filter((part) => part.type !== "literal")
    .map((part) => [part.type, part.value]));
}

function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const part = beijingDateParts(date);
  return `${part.year}-${part.month}-${part.day} ${part.hour}:${part.minute}:${part.second}（北京时间）`;
}
```

Implement `toDateTimeLocal` from the same Beijing parts. Implement `beijingDateTimeLocalToISOString` with a strict `YYYY-MM-DDTHH:mm` regex, `Date.UTC(..., hour - 8, ...)`, and a round-trip check through `toDateTimeLocal` to reject invalid dates.

Change `saveReleaseReminder` to call the Beijing parser instead of `new Date(value)`:

```js
const dueAt = beijingDateTimeLocalToISOString(value);
if (!dueAt) {
  setStatus("提醒时间格式不正确。");
  return;
}
```

Send `release_due_at: dueAt` without changing the API contract.

- [ ] **Step 4: Run Web tests and JavaScript syntax validation**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'TestAppWeb.*Beijing|TestAppWeb.*DateTime' -count=1
sed -n '/^  <script>$/,/^  <\/script>$/p' web/index.html | sed '1d;$d' > /tmp/connectmac-index-inline.js
node --check /tmp/connectmac-index-inline.js
```

Expected: PASS with no Node output.

- [ ] **Step 5: Commit Web time conversion**

```bash
git add web/index.html internal/connectmac/app_test.go
git commit -m "feat: show Beijing time in web manager"
```

### Task 4: Desktop Safari, Firefox, and Chrome Layout Stability

**Files:**
- Modify: `web/index.html`
- Modify: `internal/connectmac/app_test.go`

- [ ] **Step 1: Add failing markup and CSS contracts**

Add tests requiring:

```html
<div class="table-scroll profile-table-scroll">
<div class="table-scroll member-table-scroll">
```

and these compatibility properties:

```css
.app-layout.container-fluid { width: 100%; max-width: 2400px; margin-inline: auto; }
.table-scroll { min-width: 0; overflow-x: auto; overscroll-behavior-inline: contain; }
.profile-table-scroll table { min-width: 960px; }
.member-table-scroll table { min-width: 900px; }
.picker-card { min-width: 0; }
.profile-form-grid { min-height: 0; }
input[type="datetime-local"] { min-width: 0; }
.terminal-surface { height: clamp(420px, 62vh, 720px); min-height: 0; max-height: none; }
```

The contract must also verify the existing mobile rule remains:

```css
@media (max-width: 720px)
.local-action { display: none !important; }
```

- [ ] **Step 2: Run the compatibility contract and verify failure**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run TestAppWebDesktopBrowserCompatibility -count=1
```

Expected: FAIL because wrappers and compatibility properties are absent.

- [ ] **Step 3: Apply minimal progressive layout fixes**

Wrap only the Home and member tables. Keep the existing `.jobs` scrolling container. Add the CSS above plus:

```css
main, .view, .section-head > *, .toolbar, .row-actions { min-width: 0; }
.picker-layer { overflow: auto; }
.picker-card { max-width: calc(100vw - 36px); }
.xterm, .xterm-screen, .xterm-viewport { max-width: 100%; }
```

At `max-width: 720px`, neutralize desktop table minimums so the existing card-table layout remains intact:

```css
.profile-table-scroll table, .member-table-scroll table { min-width: 0; }
.table-scroll { overflow-x: visible; }
```

Do not add browser user-agent detection or vendor-specific forks.

- [ ] **Step 4: Run focused and full static tests**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./internal/connectmac -run 'TestAppWebDesktopBrowserCompatibility|TestAppWeb.*Mobile' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 5: Commit compatibility changes**

```bash
git add web/index.html internal/connectmac/app_test.go
git commit -m "fix: stabilize desktop web layout"
```

### Task 5: Cross-Browser Visual Verification and Final Quality Gate

**Files:**
- Modify only if verification exposes a defect.

- [ ] **Step 1: Start an isolated local Web server**

Use a temporary config and port so the installed service is not disturbed:

```bash
mkdir -p /private/tmp/connectmac-browser-home/.connectmac
printf 'profiles: {}\n' > /private/tmp/connectmac-browser-home/.connectmac/config.yaml
HOME=/private/tmp/connectmac-browser-home go run ./cmd/cm web --host 127.0.0.1 --port 18766 --web-dir web
```

Expected: `ConnectMac web manager: http://127.0.0.1:18766`.

- [ ] **Step 2: Prepare temporary Playwright tooling**

Outside the repository:

```bash
mkdir -p /private/tmp/connectmac-playwright
cd /private/tmp/connectmac-playwright
npm init -y
npm install --save-dev @playwright/test
npx playwright install chromium firefox webkit
```

Expected: the three engines install successfully; no repository file changes.

- [ ] **Step 3: Run desktop screenshots and overflow assertions**

Create `/private/tmp/connectmac-playwright/browser-check.mjs` with the complete sequential browser harness below. It creates the temporary administrator on the first run, logs in on subsequent contexts, opens every required view and dialog, checks viewport containment, and writes screenshots outside the repository:

```js
import { chromium, firefox, webkit } from "playwright";
import fs from "node:fs/promises";

const baseURL = "http://127.0.0.1:18766";
const outputDir = "/private/tmp/connectmac-browser-results";
const engines = { chromium, firefox, webkit };
const viewports = [
  { width: 1280, height: 720 },
  { width: 1440, height: 900 },
  { width: 1920, height: 1080 },
  { width: 2560, height: 1440 },
  { width: 3840, height: 2160 }
];

await fs.mkdir(outputDir, { recursive: true });

function challengeAnswer(question) {
  const match = /(\d+)\s*\+\s*(\d+)/.exec(question);
  if (!match) throw new Error(`unexpected challenge: ${question}`);
  return String(Number(match[1]) + Number(match[2]));
}

async function authenticate(page) {
  await page.goto(baseURL);
  await page.locator("#challengeQuestion").filter({ hasText: /\d+\s*\+/ }).waitFor();
  const question = await page.locator("#challengeQuestion").textContent();
  if (await page.locator("#setupForm:not(.hidden)").count()) {
    await page.locator("#setupName").fill("Browser Admin");
    await page.locator("#setupEmail").fill("browser-admin@example.com");
    await page.locator("#setupPassword").fill("browser-test-password");
  } else {
    await page.locator("#loginUsername").fill("browser-admin@example.com");
    await page.locator("#loginPassword").fill("browser-test-password");
  }
  await page.locator("#challengeAnswer").fill(challengeAnswer(question));
  await page.locator("#authSubmitBtn").click();
  await page.locator("#appShell:not(.hidden)").waitFor();
}

async function assertContained(page, selector) {
  const box = await page.locator(selector).boundingBox();
  if (!box) throw new Error(`${selector} is not visible`);
  const viewport = page.viewportSize();
  if (box.x < 0 || box.y < 0 || box.x + box.width > viewport.width + 1 || box.y + box.height > viewport.height + 1) {
    throw new Error(`${selector} exceeds viewport: ${JSON.stringify({ box, viewport })}`);
  }
}

for (const [engineName, engine] of Object.entries(engines)) {
  const browser = await engine.launch();
  for (const viewport of viewports) {
    const context = await browser.newContext({ viewport });
    const page = await context.newPage();
    await authenticate(page);
    const suffix = `${engineName}-${viewport.width}x${viewport.height}`;
    for (const view of ["profilesView", "profilesAdminView", "userManagementView", "operationsView", "syncView", "terminalView"]) {
      await page.evaluate((id) => showView(id, { history: false }), view);
      await page.locator(`#${view}:not(.hidden)`).waitFor();
      const noPageOverflow = await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);
      if (!noPageOverflow) throw new Error(`${suffix} ${view} has page overflow`);
      await page.screenshot({ path: `${outputDir}/${suffix}-${view}.png`, fullPage: true });
    }
    await page.evaluate(() => showView("profilesAdminView", { history: false }));
    await page.locator("#openProfileFormBtn").click();
    await assertContained(page, "#profileFormLayer .picker-card");
    await page.locator("#profileFormCloseBtn").click();
    await page.evaluate(() => showView("userManagementView", { history: false }));
    await page.locator("#openMemberFormBtn").click();
    await assertContained(page, "#memberFormLayer .picker-card");
    await page.locator("#memberFormCloseBtn").click();
    await context.close();
  }
  await browser.close();
}

for (const [engineName, engine] of Object.entries({ chromium, webkit })) {
  const browser = await engine.launch();
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await authenticate(page);
  const visibleLocalActions = await page.locator(".local-action:visible").count();
  if (visibleLocalActions !== 0) throw new Error(`${engineName} mobile exposes ${visibleLocalActions} local actions`);
  const noPageOverflow = await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);
  if (!noPageOverflow) throw new Error(`${engineName} mobile has page overflow`);
  await page.screenshot({ path: `${outputDir}/${engineName}-390x844-mobile.png`, fullPage: true });
  await context.close();
  await browser.close();
}
```

Run it with:

```bash
cd /private/tmp/connectmac-playwright
node browser-check.mjs
```

Expected: screenshots for every engine, viewport, and required view, with no thrown containment or overflow error. Inspect the screenshots for clipped text, overlapping controls, blank terminal surfaces, and incoherent table action wrapping.

- [ ] **Step 4: Inspect representative mobile regressions**

Inspect `chromium-390x844-mobile.png` and `webkit-390x844-mobile.png` generated by the same harness. Confirm the card-table layout remains readable, local-only controls are absent, dialogs fit the viewport, and the harness reported no horizontal overflow.

Expected: both screenshots pass visual inspection and the harness exits with status 0.

- [ ] **Step 5: Run the complete quality gate**

```bash
GOCACHE=/tmp/connectmac-go-cache go test ./...
GOCACHE=/tmp/connectmac-go-cache go test -race ./...
GOCACHE=/tmp/connectmac-go-cache go vet ./...
sed -n '/^  <script>$/,/^  <\/script>$/p' web/index.html | sed '1d;$d' > /tmp/connectmac-index-inline.js
node --check /tmp/connectmac-index-inline.js
git diff --check
```

Expected: all commands PASS.

- [ ] **Step 6: Commit any verification fixes**

If Step 3 or Step 4 required a source correction, stage only the corrected tracked files and commit:

```bash
git add web/index.html internal/connectmac/app_test.go internal/connectmac/wechat.go internal/connectmac/wechat_test.go internal/connectmac/app_web.go internal/connectmac/app_web_auto_release_test.go internal/connectmac/display_time.go internal/connectmac/display_time_test.go
git commit -m "fix: complete Beijing time browser verification"
```

If no correction was required, do not create an empty commit.
