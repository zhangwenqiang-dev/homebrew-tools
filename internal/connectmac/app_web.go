package connectmac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type webOptions struct {
	Host string
	Port int
	Open bool
	Dir  string
}

type webAPIResponse struct {
	OK     bool        `json:"ok"`
	Code   int         `json:"code,omitempty"`
	Output string      `json:"output,omitempty"`
	Error  string      `json:"error,omitempty"`
	Data   interface{} `json:"data,omitempty"`
}

type webProfile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	AppleEmail  string `json:"apple_email"`
	Region      string `json:"region"`
	AWSProfile  string `json:"aws_profile"`
	Host        string `json:"host"`
}

func (a App) runWeb(ctx context.Context, configPath string, args []string) int {
	opts, err := parseWebArgs(args)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	if opts.Dir != "" {
		a.WebDir = opts.Dir
	}
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	handler := a.newWebHandler(configPath)
	server := &http.Server{Addr: addr, Handler: handler}
	if opts.Open {
		url := "http://" + addr
		if err := a.Runner.OpenURL(ctx, url); err != nil {
			fmt.Fprintf(a.Err, "open browser failed: %v\n", err)
		}
	}
	fmt.Fprintf(a.Out, "ConnectMac web manager: http://%s\n", addr)
	fmt.Fprintln(a.Out, "Press Ctrl+C to stop.")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(a.Err, "web server failed: %v\n", err)
		return 1
	}
	return 0
}

func parseWebArgs(args []string) (webOptions, error) {
	opts := webOptions{Host: "127.0.0.1", Port: 8765}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			i++
			if i >= len(args) || args[i] == "" {
				return opts, fmt.Errorf("--host requires a value")
			}
			opts.Host = args[i]
		case "--port":
			i++
			if i >= len(args) || args[i] == "" {
				return opts, fmt.Errorf("--port requires a value")
			}
			port, err := strconv.Atoi(args[i])
			if err != nil || port < 1 || port > 65535 {
				return opts, fmt.Errorf("--port must be between 1 and 65535")
			}
			opts.Port = port
		case "--open":
			opts.Open = true
		case "--web-dir":
			i++
			if i >= len(args) || args[i] == "" {
				return opts, fmt.Errorf("--web-dir requires a value")
			}
			opts.Dir = args[i]
		case "--help", "-h":
			return opts, fmt.Errorf("usage: cm web [--host 127.0.0.1] [--port 8765] [--open] [--web-dir <path>]")
		default:
			return opts, fmt.Errorf("unknown web option %q", args[i])
		}
	}
	return opts, nil
}

func (a App) newWebHandler(configPath string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		dir, err := a.resolveWebDir()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
	mux.HandleFunc("/api/profiles", a.webProfilesHandler(configPath))
	mux.HandleFunc("/api/jobs", a.webJobsHandler())
	mux.HandleFunc("/api/aws/status", a.webAWSStatusHandler(configPath))
	mux.HandleFunc("/api/aws/open", a.webAWSActionHandler(configPath, "open"))
	mux.HandleFunc("/api/aws/destroy", a.webAWSActionHandler(configPath, "destroy"))
	return mux
}

func (a App) resolveWebDir() (string, error) {
	candidates := []string{}
	if env := os.Getenv("CM_WEB_DIR"); env != "" {
		candidates = append(candidates, env)
	}
	if a.WebDir != "" {
		candidates = append(candidates, a.WebDir)
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		binDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(binDir, "..", "share", "cm", "web"),
			filepath.Join(binDir, "..", "web"),
		)
	}
	candidates = append(candidates, "web")
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		expanded, err := ExpandPath(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(filepath.Join(expanded, "index.html"))
		if err == nil && !info.IsDir() {
			return expanded, nil
		}
	}
	return "", errors.New("web assets not found; set CM_WEB_DIR or install cm through Homebrew")
}

func (a App) webProfilesHandler(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		cfg, err := LoadConfig(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profiles := make([]webProfile, 0, len(cfg.Profiles))
		for _, name := range sortedProfileNames(cfg) {
			profile, _ := cfg.Profile(name)
			profiles = append(profiles, webProfile{
				Name:        profile.Name,
				Description: profile.Description,
				AppleEmail:  profile.AWS.AccountEmail,
				Region:      profile.AWS.Region,
				AWSProfile:  profile.AWS.Profile,
				Host:        profile.Host,
			})
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"profiles": profiles}})
	}
}

func (a App) webJobsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		jobs, err := a.JobManager.List()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if jobs == nil {
			jobs = []Job{}
		}
		sort.Slice(jobs, func(i, j int) bool {
			return jobs[i].StartedAt.After(jobs[j].StartedAt)
		})
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"jobs": jobs}})
	}
}

func (a App) webAWSStatusHandler(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		ref := strings.TrimSpace(r.URL.Query().Get("profile"))
		if ref == "" {
			writeWebError(w, http.StatusBadRequest, "profile is required")
			return
		}
		resp := a.webRunCommand(r.Context(), configPath, []string{"aws", "status", ref})
		writeWebJSON(w, resp)
	}
}

func (a App) webAWSActionHandler(configPath, command string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile    string `json:"profile"`
			Confirm    bool   `json:"confirm"`
			Background bool   `json:"background"`
			Notify     bool   `json:"notify"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		req.Profile = strings.TrimSpace(req.Profile)
		if req.Profile == "" {
			writeWebError(w, http.StatusBadRequest, "profile is required")
			return
		}
		args := []string{"aws", command, req.Profile}
		if req.Confirm {
			args = append(args, "--confirm")
		}
		if command == "destroy" && req.Confirm {
			args = append(args, "--background")
			if req.Notify {
				args = append(args, "--notify")
			}
		}
		resp := a.webRunCommand(r.Context(), configPath, args)
		writeWebJSON(w, resp)
	}
}

func (a App) webRunCommand(ctx context.Context, configPath string, args []string) webAPIResponse {
	var out, errOut bytes.Buffer
	sub := a
	sub.In = nil
	sub.Out = &out
	sub.Err = &errOut
	code := sub.Run(ctx, append(args, "--config", configPath))
	resp := webAPIResponse{OK: code == 0, Code: code, Output: out.String(), Error: errOut.String()}
	return resp
}

func writeWebJSON(w http.ResponseWriter, resp webAPIResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !resp.OK && resp.Code == 0 {
		resp.Code = 1
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeWebError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	writeWebJSON(w, webAPIResponse{OK: false, Code: status, Error: message})
}
