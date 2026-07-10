package connectmac

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

const localAgentLaunchLabel = "com.connectmac.local-agent"
const localAgentPlistPath = "~/Library/LaunchAgents/com.connectmac.local-agent.plist"

type localAgentOptions struct {
	Host string
	Port int
}

type localAgentRequest struct {
	Profile     string `json:"profile"`
	ProfileYAML string `json:"profile_yaml"`
	LocalPath   string `json:"local_path"`
	RemotePath  string `json:"remote_path"`
}

func (a App) runLocalAgent(ctx context.Context, args []string) int {
	if len(args) > 0 && isLocalAgentServiceCommand(args[0]) {
		return a.runLocalAgentService(ctx, args)
	}
	opts, err := parseLocalAgentArgs(args)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	server := &http.Server{Addr: addr, Handler: a.newLocalAgentHandler()}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	fmt.Fprintf(a.Out, "ConnectMac local agent: http://%s\n", addr)
	fmt.Fprintln(a.Out, "Use this on your own computer for web Connect/VNC/Transfer actions.")
	fmt.Fprintln(a.Out, "Press Ctrl+C to stop.")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(a.Err, "local agent failed: %v\n", err)
		return 1
	}
	return 0
}

func isLocalAgentServiceCommand(command string) bool {
	switch command {
	case "install", "start", "stop", "restart", "status", "uninstall":
		return true
	default:
		return false
	}
}

func parseLocalAgentArgs(args []string) (localAgentOptions, error) {
	opts := localAgentOptions{Host: "127.0.0.1", Port: 18765}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, fmt.Errorf("--host requires a value")
			}
			opts.Host = args[i]
		case "--port":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--port requires a value")
			}
			port, err := strconv.Atoi(args[i])
			if err != nil || port < 1 || port > 65535 {
				return opts, fmt.Errorf("--port must be between 1 and 65535")
			}
			opts.Port = port
		case "--help", "-h":
			return opts, fmt.Errorf("usage: cm local-agent [--host 127.0.0.1] [--port 18765]\n       cm local-agent <install|start|stop|restart|status|uninstall> [--host 127.0.0.1] [--port 18765]")
		default:
			return opts, fmt.Errorf("unknown local-agent option %q", args[i])
		}
	}
	return opts, nil
}

func (a App) runLocalAgentService(ctx context.Context, args []string) int {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(a.Err, "local-agent service management is only supported on macOS")
		return 1
	}
	command := args[0]
	opts, err := parseLocalAgentArgs(args[1:])
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	switch command {
	case "install":
		return a.installLocalAgentLaunchAgent(opts)
	case "start":
		return a.startLocalAgentLaunchAgent(ctx)
	case "stop":
		return a.stopLocalAgentLaunchAgent(ctx, false)
	case "restart":
		_ = a.stopLocalAgentLaunchAgent(ctx, true)
		return a.startLocalAgentLaunchAgent(ctx)
	case "status":
		return a.statusLocalAgent(ctx, opts)
	case "uninstall":
		if code := a.stopLocalAgentLaunchAgent(ctx, true); code != 0 {
			return code
		}
		path, err := ExpandPath(localAgentPlistPath)
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(a.Err, "remove %s: %v\n", path, err)
			return 1
		}
		fmt.Fprintf(a.Out, "uninstalled %s\n", path)
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown local-agent command %q\n", command)
		return 2
	}
}

func (a App) installLocalAgentLaunchAgent(opts localAgentOptions) int {
	executable, err := exec.LookPath("cm")
	if err != nil || executable == "" {
		executable, err = os.Executable()
		if err != nil {
			fmt.Fprintf(a.Err, "resolve cm executable: %v\n", err)
			return 1
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(a.Err, "resolve home: %v\n", err)
		return 1
	}
	logDir := filepath.Join(home, ".connectmac", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		fmt.Fprintf(a.Err, "create log dir: %v\n", err)
		return 1
	}
	path, err := ExpandPath(localAgentPlistPath)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(a.Err, "create launch agents dir: %v\n", err)
		return 1
	}
	plist := localAgentLaunchAgentPlist(localAgentLaunchLabel, executable, opts, filepath.Join(logDir, "local-agent.out.log"), filepath.Join(logDir, "local-agent.err.log"))
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		fmt.Fprintf(a.Err, "write %s: %v\n", path, err)
		return 1
	}
	fmt.Fprintf(a.Out, "installed %s\n", path)
	fmt.Fprintln(a.Out, "start with: cm local-agent start")
	return 0
}

func (a App) startLocalAgentLaunchAgent(ctx context.Context) int {
	path, err := ExpandPath(localAgentPlistPath)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(a.Err, "launch agent is not installed: %s\nrun: cm local-agent install\n", path)
			return 1
		}
		fmt.Fprintf(a.Err, "stat %s: %v\n", path, err)
		return 1
	}
	domain := localAgentLaunchDomain()
	if err := exec.CommandContext(ctx, "launchctl", "bootstrap", domain, path).Run(); err != nil {
		fmt.Fprintf(a.Out, "launch agent may already be loaded: %v\n", err)
	}
	if err := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", domain+"/"+localAgentLaunchLabel).Run(); err != nil {
		fmt.Fprintf(a.Err, "start launch agent: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "started %s\n", localAgentLaunchLabel)
	return 0
}

func (a App) stopLocalAgentLaunchAgent(ctx context.Context, ignoreMissing bool) int {
	path, err := ExpandPath(localAgentPlistPath)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	err = exec.CommandContext(ctx, "launchctl", "bootout", localAgentLaunchDomain(), path).Run()
	if err != nil {
		if ignoreMissing {
			return 0
		}
		fmt.Fprintf(a.Err, "stop launch agent: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "stopped %s\n", localAgentLaunchLabel)
	return 0
}

func (a App) statusLocalAgent(ctx context.Context, opts localAgentOptions) int {
	url := fmt.Sprintf("http://%s/health", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(a.Out, "local-agent is not responding at %s\n", url)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(a.Out, "local-agent status %s: %s\n", resp.Status, strings.TrimSpace(string(body)))
		return 1
	}
	fmt.Fprintf(a.Out, "local-agent is running at %s\n%s\n", url, strings.TrimSpace(string(body)))
	return 0
}

func localAgentLaunchDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func localAgentLaunchAgentPlist(label, executable string, opts localAgentOptions, stdoutPath, stderrPath string) string {
	args := []string{executable, "local-agent", "--host", opts.Host, "--port", strconv.Itoa(opts.Port)}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	writePlistKeyString(&b, "Label", label)
	b.WriteString("  <key>ProgramArguments</key>\n")
	b.WriteString("  <array>\n")
	for _, arg := range args {
		b.WriteString("    <string>")
		_ = xml.EscapeText(&b, []byte(arg))
		b.WriteString("</string>\n")
	}
	b.WriteString("  </array>\n")
	writePlistKeyBool(&b, "RunAtLoad", true)
	writePlistKeyBool(&b, "KeepAlive", true)
	writePlistKeyString(&b, "StandardOutPath", stdoutPath)
	writePlistKeyString(&b, "StandardErrorPath", stderrPath)
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

func writePlistKeyString(b *strings.Builder, key, value string) {
	b.WriteString("  <key>")
	_ = xml.EscapeText(b, []byte(key))
	b.WriteString("</key>\n")
	b.WriteString("  <string>")
	_ = xml.EscapeText(b, []byte(value))
	b.WriteString("</string>\n")
}

func writePlistKeyBool(b *strings.Builder, key string, value bool) {
	b.WriteString("  <key>")
	_ = xml.EscapeText(b, []byte(key))
	b.WriteString("</key>\n")
	if value {
		b.WriteString("  <true/>\n")
		return
	}
	b.WriteString("  <false/>\n")
}

func (a App) newLocalAgentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", localAgentCORS(a.localAgentHealthHandler()))
	mux.HandleFunc("/start", localAgentCORS(a.localAgentCommandHandler("start")))
	mux.HandleFunc("/open-vnc", localAgentCORS(a.localAgentCommandHandler("open-vnc")))
	mux.HandleFunc("/ssh", localAgentCORS(a.localAgentSSHHandler()))
	mux.HandleFunc("/terminal/check", localAgentCORS(a.localAgentTerminalCheckHandler()))
	mux.HandleFunc("/terminal/ws", a.localAgentTerminalWSHandler())
	mux.HandleFunc("/sync/push", localAgentCORS(a.localAgentCommandHandler("push")))
	mux.HandleFunc("/sync/pull", localAgentCORS(a.localAgentCommandHandler("pull")))
	mux.HandleFunc("/local/pick", localAgentCORS(a.localAgentPickHandler()))
	mux.HandleFunc("/local/list", localAgentCORS(a.webLocalListHandler()))
	return mux
}

func localAgentCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if localAgentAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
		} else if origin != "" {
			writeWebError(w, http.StatusForbidden, "origin is not allowed")
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func localAgentAllowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	if origin == "https://cm.hsgitlab.xyz" {
		return true
	}
	if strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:") {
		return true
	}
	return false
}

func (a App) localAgentHealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{
			"name":    "connectmac-local-agent",
			"version": a.Version,
			"os":      runtime.GOOS,
		}})
	}
}

func (a App) localAgentCommandHandler(command string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req localAgentRequest
		if err := decodeWebJSON(r, &req); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		profileName, configPath, err := writeLocalAgentProfileConfig(req)
		if err != nil {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: err.Error()})
			return
		}
		args := []string{command, profileName, "--config", configPath}
		switch command {
		case "push":
			localPath := strings.TrimSpace(req.LocalPath)
			remotePath := strings.TrimSpace(req.RemotePath)
			if localPath == "" {
				writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: "local_path is required"})
				return
			}
			if remotePath == "" {
				remotePath = "~/Downloads/"
			}
			args = []string{"push", profileName, localPath, remotePath, "--config", configPath}
		case "pull":
			remotePath := strings.TrimSpace(req.RemotePath)
			localPath := strings.TrimSpace(req.LocalPath)
			if remotePath == "" {
				remotePath = "~/Downloads/"
			}
			if localPath == "" {
				localPath = "."
			}
			args = []string{"pull", profileName, remotePath, "--config", configPath}
			// Existing cm pull writes to the current directory. Run it from localPath.
			resp := a.localAgentRunInDir(r.Context(), localPath, args)
			writeWebJSON(w, resp)
			return
		}
		writeWebJSON(w, a.localAgentRun(r.Context(), args))
	}
}

func (a App) localAgentSSHHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req localAgentRequest
		if err := decodeWebJSON(r, &req); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		profileName, configPath, err := writeLocalAgentProfileConfig(req)
		if err != nil {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: err.Error()})
			return
		}
		if runtime.GOOS != "darwin" {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: "opening a local terminal is only supported on macOS"})
			return
		}
		command := fmt.Sprintf("cm ssh %s --config %s", shellQuote(profileName), shellQuote(configPath))
		script := fmt.Sprintf(`tell application "Terminal"
	activate
	do script %q
end tell`, command)
		cmd := exec.CommandContext(r.Context(), "osascript", "-e", script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Output: string(out), Error: err.Error()})
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Output: "Opened Terminal: " + command + "\n"})
	}
}

func (a App) localAgentTerminalCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		profile, _, err := a.prepareLocalAgentTerminal(r)
		if err != nil {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: err.Error()})
			return
		}
		check, err := a.fixHostKey(r.Context(), profile)
		if err != nil {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: err.Error()})
			return
		}
		if check.Status == HostKeyScanFailed {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: fmt.Sprintf("ssh host key scan failed for %s: %s", profile.Host, check.Message)})
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{
			"profile":          profile.Name,
			"target":           fmt.Sprintf("%s@%s", profile.User, profile.Host),
			"host_key_status":  string(check.Status),
			"host_key_message": check.Message,
		}})
	}
}

func (a App) localAgentTerminalWSHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !localAgentAllowedOrigin(origin) {
			writeWebError(w, http.StatusForbidden, "origin is not allowed")
			return
		}
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		profileName := strings.TrimSpace(r.URL.Query().Get("profile"))
		if profileName == "" {
			writeWebError(w, http.StatusBadRequest, "profile is required")
			return
		}
		configPath, err := localAgentProfileConfigPath(profileName)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		cfg, err := LoadConfig(configPath)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		profile, err := resolveProfileRef(cfg, profileName)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errs := a.Validator.ValidateAccess(profile); len(errs) > 0 {
			writeWebError(w, http.StatusBadRequest, strings.Join(validationMessages(errs), "\n"))
			return
		}
		check, err := a.fixHostKey(r.Context(), profile)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if check.Status == HostKeyScanFailed {
			writeWebError(w, http.StatusBadRequest, fmt.Sprintf("ssh host key scan failed for %s: %s", profile.Host, check.Message))
			return
		}
		upgrader := websocket.Upgrader{
			CheckOrigin: func(req *http.Request) bool {
				return localAgentAllowedOrigin(req.Header.Get("Origin"))
			},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if err := a.proxyWebTerminal(r.Context(), conn, profile); err != nil && !errors.Is(err, context.Canceled) {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "local-agent.terminal", Profile: profile.Name, Message: err.Error()})
		}
	}
}

func (a App) prepareLocalAgentTerminal(r *http.Request) (Profile, string, error) {
	var req localAgentRequest
	if err := decodeWebJSON(r, &req); err != nil {
		return Profile{}, "", err
	}
	profileName, configPath, err := writeLocalAgentProfileConfig(req)
	if err != nil {
		return Profile{}, "", err
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return Profile{}, "", err
	}
	profile, err := resolveProfileRef(cfg, profileName)
	if err != nil {
		return Profile{}, "", err
	}
	if errs := a.Validator.ValidateAccess(profile); len(errs) > 0 {
		return Profile{}, "", fmt.Errorf("profile %s config error:\n%s", profile.Name, strings.Join(validationMessages(errs), "\n"))
	}
	return profile, configPath, nil
}

func (a App) localAgentPickHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if runtime.GOOS != "darwin" {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: "native folder picker is only supported on macOS"})
			return
		}
		script := `POSIX path of (choose folder with prompt "选择 ConnectMac 本机目录")`
		cmd := exec.CommandContext(r.Context(), "osascript", "-e", script)
		out, err := cmd.Output()
		if err != nil {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: err.Error()})
			return
		}
		path := strings.TrimSpace(string(out))
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"path": path}})
	}
}

func (a App) localAgentRun(ctx context.Context, args []string) webAPIResponse {
	return a.localAgentRunInDir(ctx, "", args)
}

func (a App) localAgentRunInDir(ctx context.Context, dir string, args []string) webAPIResponse {
	var out bytes.Buffer
	var errOut bytes.Buffer
	app := NewApp(&out, &errOut)
	app.Version = a.Version
	if dir != "" {
		expanded, err := ExpandPath(dir)
		if err != nil {
			return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
		}
		if err := os.MkdirAll(expanded, 0o755); err != nil {
			return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
		}
		current, err := os.Getwd()
		if err != nil {
			return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
		}
		if err := os.Chdir(expanded); err != nil {
			return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
		}
		defer func() { _ = os.Chdir(current) }()
	}
	code := app.Run(ctx, args)
	return webAPIResponse{OK: code == 0, Code: code, Output: out.String(), Error: errOut.String()}
}

func writeLocalAgentProfileConfig(req localAgentRequest) (string, string, error) {
	profileYAML := strings.TrimSpace(req.ProfileYAML)
	if profileYAML == "" {
		return "", "", fmt.Errorf("profile_yaml is required")
	}
	profile, err := ParseSingleProfileYAML(profileYAML)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(req.Profile) != "" && req.Profile != profile.Name {
		return "", "", fmt.Errorf("profile mismatch: request=%s yaml=%s", req.Profile, profile.Name)
	}
	profile = applyLocalAgentPrivateProfileFields(profile)
	dir, err := localAgentProfileDir()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, safeLocalAgentFileName(profile.Name)+".yaml")
	if err := os.WriteFile(path, []byte(FormatProfileFile(profile)), 0o600); err != nil {
		return "", "", err
	}
	return profile.Name, path, nil
}

func applyLocalAgentPrivateProfileFields(profile Profile) Profile {
	cfg, err := LoadConfig(DefaultConfigPath)
	if err != nil {
		return profile
	}
	if local, ok := cfg.Profile(profile.Name); ok {
		applyLocalPrivateProfileFields(&profile, local)
	}
	if profile.IdentityFile == "" {
		profile.IdentityFile = cfg.Defaults.IdentityFile
	}
	return profile
}

func localAgentProfileDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".connectmac", "local-agent", "profiles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func localAgentProfileConfigPath(name string) (string, error) {
	dir, err := localAgentProfileDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, safeLocalAgentFileName(name)+".yaml"), nil
}

func safeLocalAgentFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "profile"
	}
	return b.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
