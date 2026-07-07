package connectmac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type webClientConfig struct {
	UserAPI string `json:"user_api"`
}

type webProfile struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	AppleEmail  string         `json:"apple_email"`
	Region      string         `json:"region"`
	AWSProfile  string         `json:"aws_profile"`
	Host        string         `json:"host"`
	Owners      []PublicMember `json:"owners"`
}

type webAWSStatus struct {
	Profile    string             `json:"profile"`
	AppleEmail string             `json:"apple_email"`
	Region     string             `json:"region"`
	Decision   string             `json:"decision"`
	Detail     string             `json:"detail"`
	Next       string             `json:"next"`
	Ready      bool               `json:"ready"`
	Hosts      []webDedicatedHost `json:"hosts"`
	Instances  []webInstance      `json:"instances"`
	ElasticIP  webElasticIP       `json:"elastic_ip"`
}

type webDedicatedHost struct {
	HostID       string `json:"host_id"`
	State        string `json:"state"`
	InstanceType string `json:"instance_type"`
	ZoneID       string `json:"zone_id"`
}

type webInstance struct {
	InstanceID     string `json:"instance_id"`
	State          string `json:"state"`
	InstanceType   string `json:"instance_type"`
	HostID         string `json:"host_id"`
	PublicIP       string `json:"public_ip"`
	SystemStatus   string `json:"system_status"`
	InstanceStatus string `json:"instance_status"`
	EBSStatus      string `json:"ebs_status"`
	Ready          bool   `json:"ready"`
}

type webElasticIP struct {
	AllocationID  string `json:"allocation_id"`
	AssociationID string `json:"association_id"`
	InstanceID    string `json:"instance_id"`
	PublicIP      string `json:"public_ip"`
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
	if cfg, err := LoadConfig(configPath); err == nil && cfg.Server.UserAPI != "" {
		a.RemoteUserAPI = true
		fmt.Fprintf(a.Out, "ConnectMac user API: %s\n", cfg.Server.UserAPI)
	}
	if store, ok, err := NewMySQLMemberStoreFromEnv(); err != nil {
		fmt.Fprintf(a.Err, "mysql member store failed: %v\n", err)
		return 1
	} else if ok {
		if err := store.EnsureSchema(); err != nil {
			fmt.Fprintf(a.Err, "mysql member schema failed: %v\n", err)
			return 1
		}
		a.MemberStore = store
		fmt.Fprintln(a.Out, "ConnectMac member store: mysql")
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
	mux.HandleFunc("/api/auth/me", a.webAuthMeHandler())
	mux.HandleFunc("/api/auth/challenge", a.webAuthChallengeHandler())
	mux.HandleFunc("/api/auth/setup", a.webAuthSetupHandler())
	mux.HandleFunc("/api/auth/login", a.webAuthLoginHandler())
	mux.HandleFunc("/api/auth/logout", a.webAuthLogoutHandler())
	mux.HandleFunc("/api/config", a.webConfigHandler(configPath))
	mux.HandleFunc("/api/user-proxy/", a.webUserProxyHandler(configPath))
	mux.HandleFunc("/api/auth/update-email", a.requireWebRole(a.webAuthUpdateEmailHandler(), "admin"))
	mux.HandleFunc("/api/settings", a.requireWebRole(a.webSettingsHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/profiles", a.requireWebRole(a.webProfilesHandler(configPath), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/members", a.requireWebRole(a.webMembersHandler(), "admin"))
	mux.HandleFunc("/api/member/add", a.requireWebRole(a.webMemberAddHandler(), "admin"))
	mux.HandleFunc("/api/member/enable", a.requireWebRole(a.webMemberEnabledHandler(true), "admin"))
	mux.HandleFunc("/api/member/disable", a.requireWebRole(a.webMemberEnabledHandler(false), "admin"))
	mux.HandleFunc("/api/member/assign", a.requireWebRole(a.webMemberAssignHandler(false), "admin"))
	mux.HandleFunc("/api/member/unassign", a.requireWebRole(a.webMemberAssignHandler(true), "admin"))
	mux.HandleFunc("/api/events", a.requireWebRole(a.webEventsHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/jobs", a.requireWebRole(a.webJobsHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/job/log", a.requireWebRole(a.webJobLogHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/aws/status", a.requireWebRole(a.webAWSStatusHandler(configPath), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/aws/open", a.requireWebRole(a.webAWSActionHandler(configPath, "open"), "operator", "admin"))
	mux.HandleFunc("/api/aws/destroy", a.requireWebRole(a.webAWSActionHandler(configPath, "destroy"), "operator", "admin"))
	mux.HandleFunc("/api/tunnel/start", a.requireWebRole(a.webTunnelStartHandler(configPath), "operator", "admin"))
	mux.HandleFunc("/api/terminal/check", a.requireWebRole(a.webTerminalCheckHandler(configPath), "operator", "admin"))
	mux.HandleFunc("/api/terminal/ws", a.requireWebRole(a.webTerminalWSHandler(configPath), "operator", "admin"))
	mux.HandleFunc("/api/sync/history", a.requireWebRole(a.webSyncHistoryHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/sync/history/delete", a.requireWebRole(a.webSyncHistoryDeleteHandler(), "operator", "admin"))
	mux.HandleFunc("/api/sync/push", a.requireWebRole(a.webSyncPushHandler(configPath), "operator", "admin"))
	mux.HandleFunc("/api/sync/pull", a.requireWebRole(a.webSyncPullHandler(configPath), "operator", "admin"))
	mux.HandleFunc("/api/local/list", a.requireWebRole(a.webLocalListHandler(), "operator", "admin"))
	return mux
}

func (a App) webUserProxyHandler(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := LoadConfig(configPath)
		if err != nil || cfg.Server.UserAPI == "" {
			writeWebError(w, http.StatusBadGateway, "remote user api is not configured")
			return
		}
		remotePath := strings.TrimPrefix(r.URL.Path, "/api/user-proxy")
		if !isRemoteUserAPIPath(remotePath) {
			writeWebError(w, http.StatusForbidden, "path is not allowed")
			return
		}
		target := strings.TrimRight(cfg.Server.UserAPI, "/") + remotePath
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
		if err != nil {
			writeWebError(w, http.StatusBadGateway, err.Error())
			return
		}
		for _, name := range []string{"Accept", "Content-Type", "Cookie"} {
			if value := r.Header.Get(name); value != "" {
				req.Header.Set(name, value)
			}
		}
		req.Header.Set("X-Forwarded-Proto", "https")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			writeWebError(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			if strings.EqualFold(key, "Set-Cookie") {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		copyUserProxySessionCookies(w, resp)
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			a.logProfileError("user-proxy", Profile{}, err.Error())
		}
	}
}

func copyUserProxySessionCookies(w http.ResponseWriter, resp *http.Response) {
	for _, cookie := range resp.Cookies() {
		if cookie.Name != webSessionCookie {
			continue
		}
		http.SetCookie(w, &http.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Path:     "/",
			Expires:  cookie.Expires,
			MaxAge:   cookie.MaxAge,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func isRemoteUserAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/auth/") ||
		path == "/api/members" ||
		strings.HasPrefix(path, "/api/member/") ||
		path == "/api/settings" ||
		strings.HasPrefix(path, "/api/events")
}

func (a App) webConfigHandler(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		cfg, err := LoadConfig(configPath)
		if err != nil {
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || !os.IsNotExist(pathErr.Err) {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
			cfg = Config{}
		}
		writeWebJSON(w, webAPIResponse{
			OK: true,
			Data: map[string]interface{}{
				"config": webClientConfig{UserAPI: cfg.Server.UserAPI},
			},
		})
	}
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
			owners, _ := a.MemberStore.MembersForApple(profile.AWS.AccountEmail)
			profiles = append(profiles, webProfile{
				Name:        profile.Name,
				Description: profile.Description,
				AppleEmail:  profile.AWS.AccountEmail,
				Region:      profile.AWS.Region,
				AWSProfile:  profile.AWS.Profile,
				Host:        profile.Host,
				Owners:      owners,
			})
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"profiles": profiles}})
	}
}

func (a App) webMembersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		members, err := a.MemberStore.ListMembers()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"members": members}})
	}
}

func (a App) webMemberAddHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		var member Member
		var err error
		if strings.TrimSpace(req.Password) != "" {
			member, err = a.MemberStore.AddMemberWithPassword(req.Name, req.Email, req.Role, req.Password)
		} else {
			member, err = a.MemberStore.AddMember(req.Name, req.Email, req.Role)
		}
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"member": member}})
	}
}

func (a App) webMemberEnabledHandler(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		member, err := a.MemberStore.SetMemberEnabled(req.Email, enabled)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"member": member}})
	}
}

func (a App) webMemberAssignHandler(unassign bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			AppleEmail  string `json:"apple_email"`
			MemberEmail string `json:"member_email"`
			Relation    string `json:"relation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if unassign {
			if err := a.MemberStore.UnassignMember(req.AppleEmail, req.MemberEmail); err != nil {
				writeWebError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeWebJSON(w, webAPIResponse{OK: true})
			return
		}
		assignment, err := a.MemberStore.AssignMember(req.AppleEmail, req.MemberEmail, req.Relation)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"assignment": assignment}})
	}
}

func (a App) webEventsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		events, err := a.MemberStore.RecentEvents(r.URL.Query().Get("apple_email"), 50)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"events": events}})
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
		cfg, err := LoadConfig(configPath)
		if err != nil {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "web.aws.status", Profile: ref, Message: err.Error()})
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profile, err := resolveProfileRef(cfg, ref)
		if err != nil {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "web.aws.status", Profile: ref, Message: err.Error()})
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errs := a.Validator.ValidateAWSProfile(profile); len(errs) > 0 {
			message := fmt.Sprintf("profile %s config error:\n%s", profile.Name, strings.Join(validationMessages(errs), "\n"))
			a.logProfileError("web.aws.status", profile, message)
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: message})
			return
		}
		plan, status, err := a.AWSService.StatusWithOptions(r.Context(), profile, AWSStatusOptions{IncludeTerminal: false})
		if err != nil {
			a.logProfileError("web.aws.status", profile, fmt.Sprintf("aws status failed: %v", err))
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: fmt.Sprintf("aws status failed: %v", err)})
			return
		}
		writeWebJSON(w, webAPIResponse{
			OK:     true,
			Code:   0,
			Output: FormatAWSStatus(plan, status),
			Data:   webAWSStatusData(profile, plan, status),
		})
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
			OwnerEmail string `json:"owner_email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "web.aws." + command, Message: "invalid json body"})
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		req.Profile = strings.TrimSpace(req.Profile)
		if req.Profile == "" {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "web.aws." + command, Message: "profile is required"})
			writeWebError(w, http.StatusBadRequest, "profile is required")
			return
		}
		if command == "open" && req.Confirm {
			if err := a.assignWebOwnerForOpen(r, configPath, req.Profile, req.OwnerEmail); err != nil {
				_ = a.LogManager.Write(LogEntry{Level: "error", Action: "web.aws.open", Profile: req.Profile, Message: err.Error()})
				writeWebError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		args := []string{"aws", command, req.Profile}
		if req.Confirm {
			args = append(args, "--confirm")
		}
		if req.Confirm && req.Background {
			resp := a.startWebAWSJob(r.Context(), configPath, command, req.Profile, req.Notify)
			a.logWebResponse("web.aws."+command, req.Profile, resp)
			a.recordWebEvent(configPath, req.Profile, command, req.Confirm, resp)
			writeWebJSON(w, resp)
			return
		}
		if command == "destroy" && req.Confirm {
			args = append(args, "--background")
			if req.Notify {
				args = append(args, "--notify")
			}
		}
		resp := a.webRunCommand(r.Context(), configPath, args)
		a.logWebResponse("web.aws."+command, req.Profile, resp)
		a.recordWebEvent(configPath, req.Profile, command, req.Confirm, resp)
		writeWebJSON(w, resp)
	}
}

func (a App) webTunnelStartHandler(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile string `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "web.tunnel.start", Message: "invalid json body"})
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		req.Profile = strings.TrimSpace(req.Profile)
		if req.Profile == "" {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "web.tunnel.start", Message: "profile is required"})
			writeWebError(w, http.StatusBadRequest, "profile is required")
			return
		}
		resp := a.webRunCommand(r.Context(), configPath, []string{"start", req.Profile})
		a.logWebResponse("web.tunnel.start", req.Profile, resp)
		a.recordWebEvent(configPath, req.Profile, "start", true, resp)
		writeWebJSON(w, resp)
	}
}

func (a App) assignWebOwnerForOpen(r *http.Request, configPath, profileRef, ownerEmail string) error {
	member, ok := a.currentWebMember(r)
	if !ok {
		return errors.New("login required")
	}
	if member.Role == "admin" {
		ownerEmail = normalizeEmail(ownerEmail)
		if ownerEmail == "" {
			return errors.New("owner_email is required when admin confirms open")
		}
	} else {
		ownerEmail = member.Email
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	profile, err := resolveProfileRef(cfg, profileRef)
	if err != nil {
		return err
	}
	_, err = a.MemberStore.AssignMember(profile.AWS.AccountEmail, ownerEmail, "owner")
	return err
}

func (a App) webJobLogHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeWebError(w, http.StatusBadRequest, "id is required")
			return
		}
		job, err := a.JobManager.Load(id)
		if err != nil {
			writeWebError(w, http.StatusNotFound, err.Error())
			return
		}
		var out bytes.Buffer
		if err := copyTail(&out, job.Log, 128*1024); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"job": job}, Output: out.String()})
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

func (a App) startWebAWSJob(ctx context.Context, configPath, command, profileRef string, notify bool) webAPIResponse {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	profile, err := resolveProfileRef(cfg, profileRef)
	if err != nil {
		return webAPIResponse{OK: false, Code: 2, Error: err.Error()}
	}
	if errs := a.Validator.ValidateAWSProfile(profile); len(errs) > 0 {
		return webAPIResponse{OK: false, Code: 1, Error: strings.Join(validationMessages(errs), "\n")}
	}
	plan, err := a.AWSService.Plan(profile)
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	executable, err := os.Executable()
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	jobType := "aws-" + command
	job, err := a.JobManager.Create(Job{
		Type:       jobType,
		Profile:    profile.Name,
		AppleEmail: plan.AccountEmail,
		Command:    []string{executable, "aws", command, profile.Name, "--confirm", "--config", configPath},
		Notify:     notify,
	})
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	job, err = a.JobManager.StartRunner(ctx, job)
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Started background AWS %s job: %s\n", command, job.ID)
	fmt.Fprintf(&out, "Profile: %s\n", profile.Name)
	if plan.AccountEmail != "" {
		fmt.Fprintf(&out, "Apple account: %s\n", plan.AccountEmail)
	}
	fmt.Fprintf(&out, "PID: %d\n", job.PID)
	fmt.Fprintf(&out, "Log: %s\n", job.Log)
	if command == "destroy" {
		fmt.Fprintln(&out, "Elastic IP allocation will be retained.")
	}
	return webAPIResponse{OK: true, Code: 0, Output: out.String(), Data: map[string]interface{}{"job": job}}
}

func (a App) recordWebEvent(configPath, profileRef, action string, confirmed bool, resp webAPIResponse) {
	status := "success"
	message := strings.TrimSpace(resp.Output)
	if !resp.OK {
		status = "failed"
		message = strings.TrimSpace(resp.Error)
	}
	if len(message) > 400 {
		message = message[:400]
	}
	event := OperationEvent{
		Action:    action,
		Profile:   profileRef,
		Confirmed: confirmed,
		Status:    status,
		Message:   message,
	}
	if cfg, err := LoadConfig(configPath); err == nil {
		if profile, err := resolveProfileRef(cfg, profileRef); err == nil {
			event.Profile = profile.Name
			event.AppleEmail = profile.AWS.AccountEmail
		}
	}
	_ = a.MemberStore.RecordEvent(event)
}

func (a App) logProfileError(action string, profile Profile, message string) {
	_ = a.LogManager.Write(LogEntry{
		Level:      "error",
		Action:     action,
		Profile:    profile.Name,
		AppleEmail: profile.AWS.AccountEmail,
		Region:     profile.AWS.Region,
		AWSProfile: profile.AWS.Profile,
		Message:    message,
	})
}

func (a App) logWebResponse(action, profileRef string, resp webAPIResponse) {
	level := "info"
	message := strings.TrimSpace(resp.Output)
	if !resp.OK {
		level = "error"
		message = strings.TrimSpace(resp.Error)
	}
	if message == "" {
		message = fmt.Sprintf("code=%d ok=%t", resp.Code, resp.OK)
	}
	_ = a.LogManager.Write(LogEntry{Level: level, Action: action, Profile: profileRef, Message: message})
}

func webAWSStatusData(profile Profile, plan MacPlan, status AWSStatus) webAWSStatus {
	action := AWSOpenAction(status)
	data := webAWSStatus{
		Profile:    profile.Name,
		AppleEmail: profile.AWS.AccountEmail,
		Region:     plan.Region,
		Decision:   action.Kind,
		Detail:     action.Detail,
		Next:       AWSOpenDecisionNextStep(profile.Name, action),
		Ready:      AWSStatusReady(status),
		ElasticIP: webElasticIP{
			AllocationID:  status.ElasticIP.AllocationID,
			AssociationID: status.ElasticIP.AssociationID,
			InstanceID:    status.ElasticIP.InstanceID,
			PublicIP:      status.ElasticIP.PublicIP,
		},
	}
	for _, host := range status.Hosts {
		data.Hosts = append(data.Hosts, webDedicatedHost{
			HostID:       host.HostID,
			State:        host.State,
			InstanceType: host.InstanceType,
			ZoneID:       host.ZoneID,
		})
	}
	for _, instance := range status.Instances {
		data.Instances = append(data.Instances, webInstance{
			InstanceID:     instance.InstanceID,
			State:          instance.State,
			InstanceType:   instance.InstanceType,
			HostID:         instance.HostID,
			PublicIP:       instance.PublicIP,
			SystemStatus:   emptyStatus(instance.SystemStatus),
			InstanceStatus: emptyStatus(instance.InstanceStatusCheck),
			EBSStatus:      emptyStatus(instance.EBSStatus),
			Ready:          InstanceReady(instance, status.ElasticIP),
		})
	}
	if data.Hosts == nil {
		data.Hosts = []webDedicatedHost{}
	}
	if data.Instances == nil {
		data.Instances = []webInstance{}
	}
	return data
}

func validationMessages(errs []error) []string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return messages
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
