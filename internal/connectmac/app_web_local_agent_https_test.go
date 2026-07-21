package connectmac

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppWebLocalAgentSecureFallbackContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "web", "index.html"))
	if err != nil {
		t.Fatalf("read web index: %v", err)
	}
	html := string(data)
	for _, want := range []string{
		`secureURL: "https://127.0.0.1:18765"`,
		`legacyURL: "http://127.0.0.1:18765"`,
		`async function probeLocalAgent(url, timeoutMs = 1200)`,
		`if (!res.ok || !body?.ok)`,
		`[state.localAgent.secureURL, state.localAgent.legacyURL]`,
		`state.localAgent.errorReason = connectedURL ? "" : "本机代理未连接，请运行 cm local-agent install";`,
		`state.localAgent.url.replace(/^http:/, "ws:").replace(/^https:/, "wss:")`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("secure local-agent contract missing %q", want)
		}
	}
	if strings.Contains(html, `body.local-agent-off .local-action { display: none !important; }`) {
		t.Fatal("desktop local actions must remain visible while the agent is offline")
	}
	if !strings.Contains(html, `.local-action { display: none !important; }`) {
		t.Fatal("mobile local actions must remain hidden")
	}
}

func TestAppWebLocalAgentDoesNotSwitchEndpointDuringActiveWork(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "web", "index.html"))
	if err != nil {
		t.Fatalf("read web index: %v", err)
	}
	html := string(data)
	for _, want := range []string{
		`function localAgentEndpointLocked()`,
		`state.terminal.socket && state.terminal.socket.readyState !== WebSocket.CLOSED`,
		`Object.values(state.syncJobs).some((job) => syncJobActive(job))`,
		`...(locked ? [] : [state.localAgent.secureURL, state.localAgent.legacyURL]`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("active endpoint lock missing %q", want)
		}
	}
}
