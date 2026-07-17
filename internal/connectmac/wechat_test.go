package connectmac

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWechatNotifierSendsMarkdown(t *testing.T) {
	var got map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	notifier := WechatNotifier{WebhookURL: server.URL, WebBaseURL: "https://cm.example.com"}
	result, err := notifier.Send(WechatNotification{
		Event:         "open",
		Profile:       "apple-usw2",
		AppleEmail:    "apple@example.com",
		Owner:         "User",
		Operator:      "Admin",
		HostID:        "h-123",
		HostCreatedAt: "2026-07-16T08:03:24Z",
		DueAt:         "2026-07-17T16:00:00Z",
		Management:    true,
		Description:   "打开成功",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Skipped {
		t.Fatalf("result skipped: %+v", result)
	}
	if got["msgtype"] != "markdown" {
		t.Fatalf("msgtype = %#v", got["msgtype"])
	}
	markdown := got["markdown"].(map[string]interface{})
	content := markdown["content"].(string)
	for _, want := range []string{
		"ConnectMac",
		"apple-usw2",
		"apple@example.com",
		"https://cm.example.com",
		"Host 创建时间：2026-07-16 16:03:24（北京时间）",
		"释放提醒时间：2026-07-18 00:00:00（北京时间）",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
	for _, unexpected := range []string{"T08:03:24Z", "T16:00:00Z"} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("content leaked UTC timestamp %q:\n%s", unexpected, content)
		}
	}
}

func TestWechatNotifierMissingWebhookSkips(t *testing.T) {
	notifier := WechatNotifier{}
	result, err := notifier.Send(WechatNotification{Event: "due", Profile: "apple-usw2"})
	if err != nil {
		t.Fatalf("send missing webhook: %v", err)
	}
	if !result.Skipped {
		t.Fatalf("result should be skipped: %+v", result)
	}
}

func TestRedactWechatWebhookURL(t *testing.T) {
	raw := "post https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret failed"
	got := redactWechatWebhookURL(raw)
	if strings.Contains(got, "secret") || !strings.Contains(got, "key=[redacted]") {
		t.Fatalf("redacted = %s", got)
	}
}
