package connectmac

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

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
			return opts, fmt.Errorf("usage: cm local-agent [--host 127.0.0.1] [--port 18765]")
		default:
			return opts, fmt.Errorf("unknown local-agent option %q", args[i])
		}
	}
	return opts, nil
}

func (a App) newLocalAgentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", localAgentCORS(a.localAgentHealthHandler()))
	mux.HandleFunc("/start", localAgentCORS(a.localAgentCommandHandler("start")))
	mux.HandleFunc("/open-vnc", localAgentCORS(a.localAgentCommandHandler("open-vnc")))
	mux.HandleFunc("/ssh", localAgentCORS(a.localAgentSSHHandler()))
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
