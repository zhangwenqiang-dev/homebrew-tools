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
	"time"
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
	ProfileYAML string         `json:"profile_yaml,omitempty"`
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
	CreatedAt    string `json:"created_at,omitempty"`
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
	go a.runReleaseReminderWorker(ctx)
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

func (a App) runReleaseReminderWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	a.sendDueReleaseReminders(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.sendDueReleaseReminders(now)
		}
	}
}

func (a App) sendDueReleaseReminders(now time.Time) {
	reminders, err := a.MemberStore.ListReleaseReminders("")
	if err != nil {
		_ = a.LogManager.Write(LogEntry{Level: "error", Action: "release-reminder.worker", Message: err.Error()})
		return
	}
	for _, reminder := range reminders {
		if reminder.Status != ReleaseReminderStatusActive || strings.TrimSpace(reminder.ReleaseDueAt) == "" {
			continue
		}
		dueAt, err := time.Parse(time.RFC3339, reminder.ReleaseDueAt)
		if err != nil || dueAt.After(now) {
			continue
		}
		a.notifyReleaseReminder("due", reminder, "", "Mac 释放提醒已到期")
		if _, err := a.MemberStore.MarkReleaseReminderDue(reminder.ProfileName, now.Format(time.RFC3339)); err != nil {
			_ = a.LogManager.Write(LogEntry{Level: "error", Action: "release-reminder.worker", Profile: reminder.ProfileName, AppleEmail: reminder.AppleEmail, Message: err.Error()})
		}
	}
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
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
	mux.HandleFunc("/vendor/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		dir, err := a.resolveWebDir()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.StripPrefix("/vendor/", http.FileServer(http.Dir(filepath.Join(dir, "vendor")))).ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/auth/me", a.webAuthMeHandler())
	mux.HandleFunc("/api/auth/challenge", a.webAuthChallengeHandler())
	mux.HandleFunc("/api/auth/setup", a.webAuthSetupHandler(configPath))
	mux.HandleFunc("/api/auth/login", a.webAuthLoginHandler(configPath))
	mux.HandleFunc("/api/auth/logout", a.webAuthLogoutHandler())
	mux.HandleFunc("/api/auth/change-password", a.requireWebRole(a.webAuthChangePasswordHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/auth/token", a.requireWebRole(a.webAuthTokenHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/config", a.webConfigHandler(configPath))
	mux.HandleFunc("/api/user-proxy/", a.webUserProxyHandler(configPath))
	mux.HandleFunc("/api/auth/update-email", a.requireWebRole(a.webAuthUpdateEmailHandler(), "admin"))
	mux.HandleFunc("/api/settings", a.requireWebRole(a.webSettingsHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/profiles", a.requireWebRole(a.webProfilesHandler(configPath), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/members", a.requireWebRole(a.webMembersHandler(), "admin"))
	mux.HandleFunc("/api/member/add", a.requireWebRole(a.webMemberAddHandler(), "admin"))
	mux.HandleFunc("/api/member/update", a.requireWebRole(a.webMemberUpdateHandler(), "admin"))
	mux.HandleFunc("/api/member/password", a.requireWebRole(a.webMemberPasswordHandler(), "admin"))
	mux.HandleFunc("/api/member/token", a.requireWebRole(a.webMemberTokenHandler(), "admin"))
	mux.HandleFunc("/api/member/enable", a.requireWebRole(a.webMemberEnabledHandler(true), "admin"))
	mux.HandleFunc("/api/member/disable", a.requireWebRole(a.webMemberEnabledHandler(false), "admin"))
	mux.HandleFunc("/api/member/assign", a.requireWebRole(a.webMemberAssignHandler(false), "admin"))
	mux.HandleFunc("/api/member/unassign", a.requireWebRole(a.webMemberAssignHandler(true), "admin"))
	mux.HandleFunc("/api/member/profiles", a.requireWebRole(a.webMemberProfilesHandler(), "admin"))
	mux.HandleFunc("/api/profile-owners", a.requireWebRole(a.webProfileOwnersHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/profile-owner/set", a.requireWebRole(a.webProfileOwnerSetHandler(), "operator", "admin"))
	mux.HandleFunc("/api/release-reminders", a.requireWebRole(a.webReleaseRemindersHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/release-reminder/extend", a.requireWebRole(a.webReleaseReminderExtendHandler(), "operator", "admin"))
	mux.HandleFunc("/api/release-reminder/cleanup", a.requireWebRole(a.webReleaseReminderCleanupHandler(), "admin"))
	mux.HandleFunc("/api/managed-profiles", a.requireWebRole(a.webManagedProfilesHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/managed-profile/save", a.requireWebRole(a.webManagedProfileSaveHandler(), "admin"))
	mux.HandleFunc("/api/managed-profile/status", a.requireWebRole(a.webManagedProfileStatusHandler(), "admin"))
	mux.HandleFunc("/api/managed-profile/delete", a.requireWebRole(a.webManagedProfileDeleteHandler(), "admin"))
	mux.HandleFunc("/api/managed-profile/access", a.requireWebRole(a.webManagedProfileAccessHandler(), "admin"))
	mux.HandleFunc("/api/events", a.requireWebRole(a.webEventsHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/jobs", a.requireWebRole(a.webJobsHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/job/log", a.requireWebRole(a.webJobLogHandler(), "viewer", "operator", "admin"))
	mux.HandleFunc("/api/debug/status-config", a.requireWebRole(a.webDebugStatusConfigHandler(configPath), "admin"))
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
		if isUserProxyLoginSuccess(remotePath, resp.StatusCode) {
			a.cleanupLocalConfigAfterLogin(configPath)
		}
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			a.logProfileError("user-proxy", Profile{}, err.Error())
		}
	}
}

func isUserProxyLoginSuccess(path string, status int) bool {
	return status >= 200 && status < 300 && (path == "/api/auth/login" || path == "/api/auth/setup")
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
		path == "/api/profile-owners" ||
		strings.HasPrefix(path, "/api/profile-owner/") ||
		path == "/api/managed-profiles" ||
		strings.HasPrefix(path, "/api/managed-profile/") ||
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
		cfg, err := a.loadWebConfig(r, configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profiles := make([]webProfile, 0, len(cfg.Profiles))
		for _, name := range sortedProfileNames(cfg) {
			profile, _ := cfg.Profile(name)
			owners, _ := a.MemberStore.MembersForApple(profile.AWS.AccountEmail)
			if owner, ok, _ := a.MemberStore.ProfileOwner(profile.Name); ok {
				owners = []PublicMember{owner.Owner}
			}
			profiles = append(profiles, webProfile{
				Name:        profile.Name,
				Description: profile.Description,
				AppleEmail:  profile.AWS.AccountEmail,
				Region:      profile.AWS.Region,
				AWSProfile:  profile.AWS.Profile,
				Host:        profile.Host,
				Owners:      owners,
				ProfileYAML: FormatProfileFile(profile),
			})
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"profiles": profiles}})
	}
}

func (a App) loadWebConfig(r *http.Request, configPath string) (Config, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) || !os.IsNotExist(pathErr.Err) {
			return Config{}, err
		}
		cfg = Config{Profiles: map[string]Profile{}}
	}
	if strings.TrimSpace(cfg.Server.UserAPI) == "" {
		member, ok := a.currentWebMember(r)
		memberEmail := ""
		if ok {
			memberEmail = member.Email
		}
		records, err := a.MemberStore.ListManagedProfiles(memberEmail)
		if err != nil {
			return cfg, nil
		}
		if len(records) == 0 {
			return cfg, nil
		}
		return a.mergeManagedProfileRecords(cfg, records)
	}
	remoteProfiles, err := a.fetchRemoteManagedProfiles(r, cfg.Server.UserAPI)
	if err != nil {
		return cfg, nil
	}
	if len(remoteProfiles) == 0 {
		return Config{Profiles: map[string]Profile{}}, nil
	}
	records := make([]ManagedProfile, 0, len(remoteProfiles))
	for _, remote := range remoteProfiles {
		records = append(records, ManagedProfile{Name: remote.Name, ProfileYAML: remote.ProfileYAML})
	}
	return a.mergeManagedProfileRecords(cfg, records)
}

func (a App) mergeManagedProfileRecords(cfg Config, records []ManagedProfile) (Config, error) {
	merged := Config{Profiles: map[string]Profile{}}
	merged.Defaults = cfg.Defaults
	for _, record := range records {
		profile, err := ParseSingleProfileYAML(record.ProfileYAML)
		if err != nil {
			return Config{}, fmt.Errorf("parse managed profile %s: %w", record.Name, err)
		}
		if local, ok := cfg.Profile(profile.Name); ok {
			applyLocalPrivateProfileFields(&profile, local)
		}
		if profile.IdentityFile == "" {
			profile.IdentityFile = cfg.Defaults.IdentityFile
		}
		merged.Profiles[profile.Name] = profile
	}
	merged.ApplyDefaults()
	return merged, nil
}

func (a App) fetchRemoteManagedProfiles(r *http.Request, userAPI string) ([]webManagedProfile, error) {
	target := strings.TrimRight(userAPI, "/") + "/api/managed-profiles?include_yaml=1"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if cookie := r.Header.Get("Cookie"); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			Profiles []webManagedProfile `json:"profiles"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if !body.OK {
		if body.Error == "" {
			body.Error = resp.Status
		}
		return nil, errors.New(body.Error)
	}
	return body.Data.Profiles, nil
}

func applyLocalPrivateProfileFields(remote *Profile, local Profile) {
	if local.IdentityFile != "" {
		remote.IdentityFile = local.IdentityFile
	}
	if !syncConfigEmpty(local.Sync) {
		remote.Sync = local.Sync
	}
	if local.VNC.Username != "" {
		remote.VNC = local.VNC
	}
}

func syncConfigEmpty(sync SyncConfig) bool {
	return len(sync.Push.Includes) == 0 &&
		len(sync.Push.Excludes) == 0 &&
		len(sync.Pull.Includes) == 0 &&
		len(sync.Pull.Excludes) == 0
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

func (a App) webMemberUpdateHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			OriginalEmail string `json:"original_email"`
			Name          string `json:"name"`
			Email         string `json:"email"`
			Role          string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		member, err := a.MemberStore.UpdateMember(req.OriginalEmail, req.Name, req.Email, req.Role)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"member": member}})
	}
}

func (a App) webMemberPasswordHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if strings.TrimSpace(req.Email) == "" {
			writeWebError(w, http.StatusBadRequest, "member email is required")
			return
		}
		if err := a.MemberStore.SetMemberPassword(req.Email, req.Password); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true})
	}
}

func (a App) webMemberTokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Email  string `json:"email"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		data, err := a.applyWebAPITokenAction(req.Email, req.Action)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: data})
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
			Profile     string `json:"profile"`
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
		data := map[string]interface{}{"assignment": assignment}
		if strings.TrimSpace(req.Profile) != "" {
			owner, err := a.MemberStore.SetProfileOwner(req.Profile, req.MemberEmail)
			if err != nil {
				writeWebError(w, http.StatusBadRequest, err.Error())
				return
			}
			data["profile_owner"] = owner
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: data})
	}
}

func (a App) webMemberProfilesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			MemberEmail string   `json:"member_email"`
			Profiles    []string `json:"profiles"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		access, err := a.MemberStore.SetMemberProfileAccess(req.MemberEmail, req.Profiles)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"profile_access": access}})
	}
}

func (a App) webProfileOwnersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		owners, err := a.MemberStore.ProfileOwners()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"owners": owners}})
	}
}

func (a App) webProfileOwnerSetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile     string `json:"profile"`
			MemberEmail string `json:"member_email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		owner, err := a.MemberStore.SetProfileOwner(req.Profile, req.MemberEmail)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"owner": owner}})
	}
}

func (a App) webReleaseRemindersHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		member, ok := a.currentWebMember(r)
		if !ok {
			writeWebError(w, http.StatusUnauthorized, "login required")
			return
		}
		reminders, err := a.MemberStore.ListReleaseReminders(member.Email)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"reminders": reminders}})
	}
}

func (a App) webReleaseReminderExtendHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		member, ok := a.currentWebMember(r)
		if !ok {
			writeWebError(w, http.StatusUnauthorized, "login required")
			return
		}
		var req struct {
			Profile      string `json:"profile"`
			ReleaseDueAt string `json:"release_due_at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		profileName := strings.TrimSpace(req.Profile)
		if profileName == "" {
			writeWebError(w, http.StatusBadRequest, "profile is required")
			return
		}
		dueAt, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ReleaseDueAt))
		if err != nil {
			writeWebError(w, http.StatusBadRequest, "release_due_at must be RFC3339")
			return
		}
		if dueAt.Before(time.Now().Add(-1 * time.Minute)) {
			writeWebError(w, http.StatusBadRequest, "release_due_at must be in the future")
			return
		}
		if err := a.ensureWebMemberProfileAccess(member, profileName); err != nil {
			writeWebError(w, http.StatusForbidden, err.Error())
			return
		}
		reminder, ok, err := a.MemberStore.ReleaseReminder(profileName)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeWebError(w, http.StatusNotFound, "release reminder not found")
			return
		}
		oldDueAt := reminder.ReleaseDueAt
		now := time.Now().Format(time.RFC3339)
		reminder.ReleaseDueAt = dueAt.Format(time.RFC3339)
		reminder.LastExtendedByEmail = member.Email
		reminder.LastExtendedByName = member.Name
		reminder.LastExtendedAt = now
		reminder.Status = ReleaseReminderStatusActive
		reminder.LastNotifiedAt = ""
		reminder, err = a.MemberStore.UpsertReleaseReminder(reminder)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.notifyReleaseReminder("extend", reminder, member.Name, "释放提醒已延长（原时间："+oldDueAt+"）")
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"reminder": reminder}})
	}
}

func (a App) webReleaseReminderCleanupHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile string `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		profileName := strings.TrimSpace(req.Profile)
		if profileName == "" {
			writeWebError(w, http.StatusBadRequest, "profile is required")
			return
		}
		reminder, err := a.cleanupProfileLocalRecords(profileName, "manual")
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"reminder": reminder}})
	}
}

func (a App) ensureWebMemberProfileAccess(member Member, profileName string) error {
	if member.Role == "admin" {
		return nil
	}
	profiles, err := a.MemberStore.ListManagedProfiles(member.Email)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.Name == profileName {
			return nil
		}
	}
	return fmt.Errorf("profile %s is not assigned to %s", profileName, member.Email)
}

type webManagedProfile struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	AppleEmail  string         `json:"apple_email"`
	Region      string         `json:"region"`
	AWSProfile  string         `json:"aws_profile"`
	Host        string         `json:"host"`
	Enabled     bool           `json:"enabled"`
	ProfileYAML string         `json:"profile_yaml,omitempty"`
	Members     []PublicMember `json:"members,omitempty"`
	UpdatedAt   string         `json:"updated_at"`
}

func (a App) webManagedProfilesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		member, ok := a.currentWebMember(r)
		if !ok {
			writeWebError(w, http.StatusUnauthorized, "login required")
			return
		}
		records, err := a.MemberStore.ListManagedProfiles(member.Email)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		includeYAML := r.URL.Query().Get("include_yaml") == "1"
		items := make([]webManagedProfile, 0, len(records))
		for _, record := range records {
			profile, err := ParseSingleProfileYAML(record.ProfileYAML)
			if err != nil {
				continue
			}
			item := webManagedProfile{
				Name:        profile.Name,
				Description: profile.Description,
				AppleEmail:  profile.AWS.AccountEmail,
				Region:      profile.AWS.Region,
				AWSProfile:  profile.AWS.Profile,
				Host:        profile.Host,
				Enabled:     record.Enabled,
				UpdatedAt:   record.UpdatedAt,
			}
			if includeYAML || member.Role == "admin" {
				item.ProfileYAML = record.ProfileYAML
			}
			if member.Role == "admin" {
				item.Members, _ = a.MemberStore.MembersForProfile(profile.Name)
			}
			items = append(items, item)
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"profiles": items}})
	}
}

func (a App) webManagedProfileSaveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ProfileYAML string `json:"profile_yaml"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		profile, err := ParseSingleProfileYAML(req.ProfileYAML)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		record, err := a.MemberStore.UpsertManagedProfile(profile)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"profile": record}})
	}
}

func (a App) webManagedProfileStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile string `json:"profile"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		record, err := a.MemberStore.SetManagedProfileEnabled(req.Profile, req.Enabled)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"profile": record}})
	}
}

func (a App) webManagedProfileDeleteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile string `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := a.MemberStore.DeleteManagedProfile(req.Profile); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true})
	}
}

func (a App) webManagedProfileAccessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile     string `json:"profile"`
			MemberEmail string `json:"member_email"`
			Grant       bool   `json:"grant"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		var err error
		if req.Grant {
			_, err = a.MemberStore.AssignProfileAccess(req.Profile, req.MemberEmail)
		} else {
			err = a.MemberStore.UnassignProfileAccess(req.Profile, req.MemberEmail)
		}
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true})
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

func (a App) webDebugStatusConfigHandler(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		ref := strings.TrimSpace(r.URL.Query().Get("profile"))
		rawCfg, rawErr := LoadConfig(configPath)
		member, memberOK := a.currentWebMember(r)
		memberEmail := ""
		memberRole := ""
		if memberOK {
			memberEmail = member.Email
			memberRole = member.Role
		}
		managedRecords, managedErr := a.MemberStore.ListManagedProfiles(memberEmail)
		if managedErr != nil && memberEmail != "" {
			managedRecords, managedErr = a.MemberStore.ListManagedProfiles("")
		}
		effectiveCfg, effectiveErr := a.loadWebConfig(r, configPath)
		resolveOK := false
		resolveErr := ""
		if effectiveErr == nil && ref != "" {
			if _, err := resolveProfileRef(effectiveCfg, ref); err != nil {
				resolveErr = err.Error()
			} else {
				resolveOK = true
			}
		}
		data := map[string]interface{}{
			"profile":                     ref,
			"config_path":                 configPath,
			"remote_user_api_mode":        a.RemoteUserAPI,
			"authenticated_member":        memberOK,
			"member_email":                memberEmail,
			"member_role":                 memberRole,
			"managed_profiles_count":      len(managedRecords),
			"managed_profiles":            managedProfileNames(managedRecords),
			"effective_profiles_count":    len(effectiveCfg.Profiles),
			"effective_profiles":          sortedProfileNames(effectiveCfg),
			"resolve_ok":                  resolveOK,
			"resolve_error":               resolveErr,
			"load_config_error":           errorString(rawErr),
			"load_effective_config_error": errorString(effectiveErr),
			"managed_profiles_error":      errorString(managedErr),
		}
		if rawErr == nil {
			data["server_user_api_configured"] = strings.TrimSpace(rawCfg.Server.UserAPI) != ""
			data["local_profiles_count"] = len(rawCfg.Profiles)
			data["local_profiles"] = sortedProfileNames(rawCfg)
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: data})
	}
}

func managedProfileNames(records []ManagedProfile) []string {
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.Name)
	}
	sort.Strings(names)
	return names
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
		cfg, err := a.loadWebConfig(r, configPath)
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
		if shouldAutoCleanupProfileRecords(status) {
			if _, err := a.cleanupProfileLocalRecords(profile.Name, "auto-status"); err != nil {
				_ = a.LogManager.Write(LogEntry{Level: "error", Action: "release-reminder.cleanup.auto", Profile: profile.Name, AppleEmail: profile.AWS.AccountEmail, Message: err.Error()})
			}
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
		if req.Confirm {
			if err := a.validateWebAWSOwner(r, command, req.OwnerEmail); err != nil {
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
			resp := a.startWebAWSJob(r, configPath, command, req.Profile, req.Notify)
			if resp.OK {
				if err := a.afterConfirmedWebAWSAction(r, configPath, command, req.Profile, req.OwnerEmail); err != nil {
					resp.Output = strings.TrimSpace(resp.Output+"\n"+resp.Error) + "\n负责人记录更新失败：" + err.Error()
					resp.Error = ""
				}
			}
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
		resp := a.webRunCommand(r, configPath, args)
		if resp.OK && req.Confirm {
			if err := a.afterConfirmedWebAWSAction(r, configPath, command, req.Profile, req.OwnerEmail); err != nil {
				resp.Output = strings.TrimSpace(resp.Output+"\n"+resp.Error) + "\n负责人记录更新失败：" + err.Error()
				resp.Error = ""
			}
		}
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
		resp := a.webRunCommand(r, configPath, []string{"start", req.Profile})
		a.logWebResponse("web.tunnel.start", req.Profile, resp)
		a.recordWebEvent(configPath, req.Profile, "start", true, resp)
		writeWebJSON(w, resp)
	}
}

func (a App) validateWebAWSOwner(r *http.Request, command, ownerEmail string) error {
	member, ok := a.currentWebMember(r)
	if !ok {
		return errors.New("login required")
	}
	if command == "open" && member.Role == "admin" {
		ownerEmail = normalizeEmail(ownerEmail)
		if ownerEmail == "" {
			return errors.New("owner_email is required when admin confirms open")
		}
	}
	return nil
}

func (a App) afterConfirmedWebAWSAction(r *http.Request, configPath, command, profileRef, ownerEmail string) error {
	cfg, err := a.loadWebConfig(r, configPath)
	if err != nil {
		return err
	}
	profile, err := resolveProfileRef(cfg, profileRef)
	if err != nil {
		return err
	}
	switch command {
	case "open":
		member, ok := a.currentWebMember(r)
		if !ok {
			return errors.New("login required")
		}
		if member.Role != "admin" {
			ownerEmail = member.Email
		}
		ownerEmail = normalizeEmail(ownerEmail)
		if _, err = a.MemberStore.AssignMember(profile.AWS.AccountEmail, ownerEmail, "owner"); err != nil {
			return err
		}
		owner, err := a.MemberStore.SetProfileOwner(profile.Name, ownerEmail)
		if err != nil {
			return err
		}
		return a.upsertReleaseReminderAfterOpen(r.Context(), profile, owner.Owner)
	case "destroy":
		if err := a.MemberStore.ClearProfileOwner(profile.Name); err != nil {
			return err
		}
		return a.markReleaseReminderAfterDestroy(profile)
	default:
		return nil
	}
}

func (a App) upsertReleaseReminderAfterOpen(ctx context.Context, profile Profile, owner PublicMember) error {
	now := time.Now()
	hostID := ""
	hostCreatedAt := now.Format(time.RFC3339)
	if _, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false}); err == nil {
		for _, host := range status.Hosts {
			if host.HostID == "" || strings.EqualFold(host.State, "released") {
				continue
			}
			hostID = host.HostID
			if strings.TrimSpace(host.CreatedAt) != "" {
				hostCreatedAt = host.CreatedAt
			}
			break
		}
	}
	existing, ok, err := a.MemberStore.ReleaseReminder(profile.Name)
	if err != nil {
		return err
	}
	if ok && existing.HostID == hostID && existing.Status != ReleaseReminderStatusReleased {
		a.notifyReleaseReminder("open", existing, owner.Name, "Mac 打开确认成功")
		return nil
	}
	createdAt, err := time.Parse(time.RFC3339, hostCreatedAt)
	if err != nil {
		createdAt = now
		hostCreatedAt = now.Format(time.RFC3339)
	}
	reminder := ReleaseReminder{
		ProfileName:   profile.Name,
		AppleEmail:    profile.AWS.AccountEmail,
		HostID:        hostID,
		HostCreatedAt: hostCreatedAt,
		ReleaseDueAt:  createdAt.Add(24 * time.Hour).Format(time.RFC3339),
		OwnerEmail:    owner.Email,
		OwnerName:     owner.Name,
		Status:        ReleaseReminderStatusActive,
	}
	reminder, err = a.MemberStore.UpsertReleaseReminder(reminder)
	if err != nil {
		return err
	}
	a.notifyReleaseReminder("open", reminder, owner.Name, "Mac 打开确认成功")
	return nil
}

func (a App) markReleaseReminderAfterDestroy(profile Profile) error {
	reminder, ok, err := a.MemberStore.ReleaseReminder(profile.Name)
	if err != nil || !ok {
		return err
	}
	reminder, err = a.MemberStore.MarkReleaseReminderReleased(profile.Name, time.Now().Format(time.RFC3339))
	if err != nil {
		return err
	}
	a.notifyReleaseReminder("release", reminder, "", "Mac 释放成功")
	return nil
}

func shouldAutoCleanupProfileRecords(status AWSStatus) bool {
	return len(status.Hosts) == 0 && len(status.Instances) == 0 && strings.TrimSpace(status.ElasticIP.InstanceID) == ""
}

func (a App) cleanupProfileLocalRecords(profileName, reason string) (ReleaseReminder, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return ReleaseReminder{}, errors.New("profile is required")
	}
	if err := a.MemberStore.ClearProfileOwner(profileName); err != nil {
		return ReleaseReminder{}, err
	}
	reminder, ok, err := a.MemberStore.ReleaseReminder(profileName)
	if err != nil {
		return ReleaseReminder{}, err
	}
	if !ok {
		_ = a.MemberStore.RecordEvent(OperationEvent{
			Action:    "cleanup-records",
			Profile:   profileName,
			Confirmed: true,
			Status:    "success",
			Message:   "cleared profile owner; no release reminder found (" + reason + ")",
		})
		return ReleaseReminder{ProfileName: profileName, Status: ReleaseReminderStatusReleased}, nil
	}
	if reminder.Status != ReleaseReminderStatusReleased {
		reminder, err = a.MemberStore.MarkReleaseReminderReleased(profileName, time.Now().Format(time.RFC3339))
		if err != nil {
			return ReleaseReminder{}, err
		}
	}
	_ = a.MemberStore.RecordEvent(OperationEvent{
		Action:     "cleanup-records",
		Profile:    profileName,
		AppleEmail: reminder.AppleEmail,
		Confirmed:  true,
		Status:     "success",
		Message:    "cleared profile owner and marked release reminder released (" + reason + ")",
	})
	return reminder, nil
}

func (a App) notifyReleaseReminder(event string, reminder ReleaseReminder, operator, description string) {
	result, err := NewWechatNotifierFromEnv().Send(WechatNotification{
		Event:         event,
		Profile:       reminder.ProfileName,
		AppleEmail:    reminder.AppleEmail,
		Owner:         displayNameEmail(reminder.OwnerName, reminder.OwnerEmail),
		Operator:      operator,
		HostID:        reminder.HostID,
		HostCreatedAt: reminder.HostCreatedAt,
		DueAt:         reminder.ReleaseDueAt,
		Management:    true,
		Description:   description,
	})
	if err != nil {
		_ = a.LogManager.Write(LogEntry{Level: "error", Action: "wechat." + event, Profile: reminder.ProfileName, AppleEmail: reminder.AppleEmail, Message: redactWechatWebhookURL(err.Error())})
		return
	}
	if result.Skipped {
		_ = a.LogManager.Write(LogEntry{Level: "info", Action: "wechat." + event, Profile: reminder.ProfileName, AppleEmail: reminder.AppleEmail, Message: result.Message})
	}
}

func displayNameEmail(name, email string) string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" {
		return email
	}
	if email == "" || strings.EqualFold(name, email) {
		return name
	}
	return name + " <" + email + ">"
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

func (a App) webRunCommand(r *http.Request, configPath string, args []string) webAPIResponse {
	runConfigPath, cleanup, err := a.webCommandConfigPath(r, configPath)
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	defer cleanup()
	var out, errOut bytes.Buffer
	sub := a
	sub.In = nil
	sub.Out = &out
	sub.Err = &errOut
	code := sub.Run(r.Context(), append(args, "--config", runConfigPath))
	resp := webAPIResponse{OK: code == 0, Code: code, Output: out.String(), Error: errOut.String()}
	return resp
}

func (a App) startWebAWSJob(r *http.Request, configPath, command, profileRef string, notify bool) webAPIResponse {
	cfg, err := a.loadWebConfig(r, configPath)
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
	runConfigPath, _, err := a.writeWebTempConfig(r, configPath)
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	jobType := "aws-" + command
	job, err := a.JobManager.Create(Job{
		Type:       jobType,
		Profile:    profile.Name,
		AppleEmail: plan.AccountEmail,
		Command:    []string{executable, "aws", command, profile.Name, "--confirm", "--config", runConfigPath},
		Notify:     notify,
	})
	if err != nil {
		return webAPIResponse{OK: false, Code: 1, Error: err.Error()}
	}
	job, err = a.JobManager.StartRunner(r.Context(), job)
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

func (a App) webCommandConfigPath(r *http.Request, configPath string) (string, func(), error) {
	cfg, err := LoadConfig(configPath)
	if err == nil && strings.TrimSpace(cfg.Server.UserAPI) == "" {
		return configPath, func() {}, nil
	}
	path, cleanup, err := a.writeWebTempConfig(r, configPath)
	return path, cleanup, err
}

func (a App) writeWebTempConfig(r *http.Request, configPath string) (string, func(), error) {
	cfg, err := a.loadWebConfig(r, configPath)
	if err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp("", "cm-web-config-*.yaml")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	if _, err := file.WriteString(FormatConfigProfiles(cfg)); err != nil {
		file.Close()
		os.Remove(path)
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
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
			CreatedAt:    host.CreatedAt,
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
