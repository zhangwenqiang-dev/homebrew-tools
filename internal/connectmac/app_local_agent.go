package connectmac

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const localAgentLaunchLabel = "com.connectmac.local-agent"
const localAgentPlistPath = "~/Library/LaunchAgents/com.connectmac.local-agent.plist"

type localAgentOptions struct {
	Host         string
	Port         int
	Force        bool
	HostExplicit bool
	PortExplicit bool
}

type localAgentRequest struct {
	TransferID  string `json:"transfer_id"`
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
	if opts.Force {
		fmt.Fprintln(a.Err, "--force is only supported for local-agent stop, restart, and uninstall")
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
			opts.HostExplicit = true
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
			opts.PortExplicit = true
		case "--force":
			opts.Force = true
		case "--help", "-h":
			return opts, fmt.Errorf("usage: cm local-agent [--host 127.0.0.1] [--port 18765]\n       cm local-agent <install|start|stop|restart|status|uninstall> [--host 127.0.0.1] [--port 18765] [--force]")
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
	if opts.Force && command != "stop" && command != "restart" && command != "uninstall" {
		fmt.Fprintln(a.Err, "--force is only supported for local-agent stop, restart, and uninstall")
		return 2
	}
	if !opts.Force && (command == "status" || command == "stop" || command == "restart" || command == "uninstall") {
		opts, err = resolveInstalledLocalAgentOptions(opts)
		if err != nil {
			fmt.Fprintf(a.Err, "read installed local-agent options: %v\n", err)
			return 1
		}
	}
	switch command {
	case "install":
		return a.installLocalAgentLaunchAgent(ctx, opts)
	case "start":
		return a.startLocalAgentLaunchAgent(ctx)
	case "stop":
		return a.stopLocalAgentLaunchAgent(ctx, opts, false)
	case "restart":
		if code := a.stopLocalAgentLaunchAgent(ctx, opts, true); code != 0 {
			return code
		}
		return a.startLocalAgentLaunchAgent(ctx)
	case "status":
		return a.statusLocalAgent(ctx, opts)
	case "uninstall":
		return a.uninstallLocalAgentLaunchAgent(ctx, opts)
	default:
		fmt.Fprintf(a.Err, "unknown local-agent command %q\n", command)
		return 2
	}
}

func resolveInstalledLocalAgentOptions(opts localAgentOptions) (localAgentOptions, error) {
	if opts.HostExplicit && opts.PortExplicit {
		return opts, nil
	}
	path, err := ExpandPath(localAgentPlistPath)
	if err != nil {
		return opts, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return opts, nil
		}
		return opts, err
	}
	defer file.Close()
	installed, err := localAgentOptionsFromPlist(file)
	if err != nil {
		return opts, err
	}
	if !opts.HostExplicit && installed.HostExplicit {
		opts.Host = installed.Host
	}
	if !opts.PortExplicit && installed.PortExplicit {
		opts.Port = installed.Port
	}
	return opts, nil
}

func localAgentOptionsFromPlist(reader io.Reader) (localAgentOptions, error) {
	decoder := xml.NewDecoder(reader)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return localAgentOptions{Host: "127.0.0.1", Port: 18765}, nil
		}
		if err != nil {
			return localAgentOptions{}, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "key" {
			continue
		}
		var key string
		if err := decoder.DecodeElement(&key, &start); err != nil {
			return localAgentOptions{}, err
		}
		if key != "ProgramArguments" {
			continue
		}
		args, err := decodePlistStringArray(decoder)
		if err != nil {
			return localAgentOptions{}, err
		}
		for i, arg := range args {
			if arg == "local-agent" {
				return parseLocalAgentArgs(args[i+1:])
			}
		}
		return localAgentOptions{Host: "127.0.0.1", Port: 18765}, nil
	}
}

func decodePlistStringArray(decoder *xml.Decoder) ([]string, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "array" {
			continue
		}
		var args []string
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			switch value := token.(type) {
			case xml.StartElement:
				if value.Name.Local == "string" {
					var arg string
					if err := decoder.DecodeElement(&arg, &value); err != nil {
						return nil, err
					}
					args = append(args, arg)
				}
			case xml.EndElement:
				if value.Name.Local == "array" {
					return args, nil
				}
			}
		}
	}
}

func (a App) installLocalAgentLaunchAgent(ctx context.Context, opts localAgentOptions) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(a.Err, "resolve home: %v\n", err)
		return 1
	}
	material, _, err := ensureLocalAgentTLS(home, time.Now())
	if err != nil {
		fmt.Fprintf(a.Err, "prepare local-agent TLS material: %v\n", err)
		return 1
	}
	if err := a.ensureLocalAgentCATrust(ctx, home, material); err != nil {
		fmt.Fprintf(a.Err, "trust local-agent CA: %v\n", err)
		return 1
	}
	executable, err := exec.LookPath("cm")
	if err != nil || executable == "" {
		executable, err = os.Executable()
		if err != nil {
			fmt.Fprintf(a.Err, "resolve cm executable: %v\n", err)
			return 1
		}
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

func (a App) uninstallLocalAgentLaunchAgent(ctx context.Context, opts localAgentOptions) int {
	if code := a.stopLocalAgentLaunchAgent(ctx, opts, true); code != 0 {
		return code
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(a.Err, "resolve home: %v\n", err)
		return 1
	}
	material := localAgentTLSPaths(home)
	if _, err := os.Lstat(material.Dir); err == nil {
		if err := a.removeLocalAgentCATrust(ctx, home, material); err != nil {
			fmt.Fprintf(a.Err, "remove local-agent CA trust: %v\n", err)
			return 1
		}
		if err := os.RemoveAll(material.Dir); err != nil {
			fmt.Fprintf(a.Err, "remove local-agent TLS directory %s: %v\n", material.Dir, err)
			return 1
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(a.Err, "inspect local-agent TLS directory %s: %v\n", material.Dir, err)
		return 1
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
}

func (a App) ensureLocalAgentCATrust(ctx context.Context, home string, material localAgentTLSMaterial) error {
	sha1Fingerprint, sha256Fingerprint, err := localAgentCAFingerprints(material.CACertPath)
	if err != nil {
		return fmt.Errorf("read local-agent CA fingerprint: %w", err)
	}
	keychain := localAgentLoginKeychainPath(home)
	output, err := a.runLocalAgentSecurityCommand(ctx, "find-certificate", "-a", "-Z", keychain)
	if err != nil {
		return localAgentSecurityCommandError("inspect current-user login keychain", err, output)
	}
	if localAgentKeychainContainsFingerprint(output, sha1Fingerprint, sha256Fingerprint) {
		return nil
	}
	output, err = a.runLocalAgentSecurityCommand(ctx, "add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-k", keychain, material.CACertPath)
	if err != nil {
		return localAgentSecurityCommandError("add local-agent CA trust", err, output)
	}
	return nil
}

func (a App) removeLocalAgentCATrust(ctx context.Context, home string, material localAgentTLSMaterial) error {
	if err := checkLocalAgentTLSNonSymlink(material.Dir, true); err != nil {
		return err
	}
	if err := checkLocalAgentTLSNonSymlink(material.CACertPath, false); err != nil {
		return err
	}
	fingerprint, err := localAgentCAFingerprint(material.CACertPath)
	if err != nil {
		return fmt.Errorf("read local-agent CA fingerprint: %w", err)
	}
	output, err := a.runLocalAgentSecurityCommand(ctx, "delete-certificate", "-Z", fingerprint, "-t", localAgentLoginKeychainPath(home))
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return localAgentSecurityCommandError("delete local-agent CA trust", err, output)
	}
	if localAgentSecurityOutputIndicatesNotFound(output) {
		return nil
	}
	return localAgentSecurityCommandError("delete local-agent CA trust", err, output)
}

func (a App) runLocalAgentSecurityCommand(ctx context.Context, args ...string) ([]byte, error) {
	if a.LocalAgentSecurityCommand != nil {
		return a.LocalAgentSecurityCommand(ctx, args...)
	}
	return exec.CommandContext(ctx, "security", args...).CombinedOutput()
}

func localAgentLoginKeychainPath(home string) string {
	return filepath.Join(home, "Library", "Keychains", "login.keychain-db")
}

func localAgentCAFingerprints(path string) (string, string, error) {
	sha1Fingerprint, err := localAgentCAFingerprint(path)
	if err != nil {
		return "", "", err
	}
	certificate, err := readLocalAgentTLSCertificate(path)
	if err != nil {
		return "", "", err
	}
	sha256Fingerprint := sha256.Sum256(certificate.Raw)
	return sha1Fingerprint, fmt.Sprintf("%x", sha256Fingerprint), nil
}

var localAgentKeychainFingerprintPattern = regexp.MustCompile(`(?im)^\s*SHA-(1|256)\s+hash:\s*([0-9a-f:]+)\s*$`)

func localAgentKeychainContainsFingerprint(output []byte, sha1Fingerprint, sha256Fingerprint string) bool {
	for _, match := range localAgentKeychainFingerprintPattern.FindAllStringSubmatch(string(output), -1) {
		fingerprint := normalizeLocalAgentFingerprint(match[2])
		switch match[1] {
		case "1":
			if fingerprint == normalizeLocalAgentFingerprint(sha1Fingerprint) {
				return true
			}
		case "256":
			if fingerprint == normalizeLocalAgentFingerprint(sha256Fingerprint) {
				return true
			}
		}
	}
	return false
}

func normalizeLocalAgentFingerprint(fingerprint string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fingerprint), ":", ""))
}

func localAgentSecurityOutputIndicatesNotFound(output []byte) bool {
	text := strings.TrimSpace(string(output))
	for _, prefix := range []string{"security:", "SecKeychainSearchCopyNext:"} {
		if len(text) >= len(prefix) && strings.EqualFold(text[:len(prefix)], prefix) {
			text = strings.TrimSpace(text[len(prefix):])
		}
	}
	text = strings.TrimSpace(strings.TrimSuffix(text, "."))
	return strings.EqualFold(text, "The specified item could not be found in the keychain")
}

func localAgentSecurityCommandError(action string, err error, output []byte) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s canceled", action)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out", action)
	}
	if detail := safeLocalAgentSecurityOutput(output); detail != "" {
		return fmt.Errorf("%s: %w: %s", action, err, detail)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func safeLocalAgentSecurityOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	var safe []string
	for _, line := range strings.Split(text, "\n") {
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "PRIVATE KEY") || strings.Contains(upper, "BEGIN") && strings.Contains(upper, "KEY") {
			safe = append(safe, "[redacted key material]")
			break
		}
		safe = append(safe, line)
	}
	text = strings.Join(safe, "\n")
	const maxOutput = 4096
	if len(text) > maxOutput {
		return text[:maxOutput] + "..."
	}
	return text
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

func (a App) stopLocalAgentLaunchAgent(ctx context.Context, opts localAgentOptions, ignoreMissing bool) int {
	drained, allowed := a.prepareLocalAgentShutdown(ctx, opts)
	if !allowed {
		return 1
	}
	path, err := ExpandPath(localAgentPlistPath)
	if err != nil {
		if drained {
			a.resumeLocalAgentActivity(opts)
		}
		fmt.Fprintln(a.Err, err)
		return 1
	}
	err = exec.CommandContext(ctx, "launchctl", "bootout", localAgentLaunchDomain(), path).Run()
	if err != nil {
		if drained {
			a.resumeLocalAgentActivity(opts)
		}
		if ignoreMissing {
			return 0
		}
		fmt.Fprintf(a.Err, "stop launch agent: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "stopped %s\n", localAgentLaunchLabel)
	return 0
}

func (a App) prepareLocalAgentShutdown(ctx context.Context, opts localAgentOptions) (bool, bool) {
	if opts.Force {
		return false, true
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	endpoint := fmt.Sprintf("http://%s/activity/drain", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)))
	req, err := http.NewRequestWithContext(checkCtx, http.MethodPost, endpoint, nil)
	if err != nil {
		fmt.Fprintf(a.Err, "unable to verify local-agent activity: create drain request: %v\n", err)
		return false, false
	}
	resp, err := localAgentHTTPClient().Do(req)
	if err != nil {
		if localAgentConnectionUnavailable(err) {
			return false, true
		}
		fmt.Fprintf(a.Err, "unable to verify local-agent activity: drain request failed: %v\n", err)
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, a.guardLocalAgentActivity(ctx, opts)
	}
	draining, active, err := decodeLocalAgentDrainResponse(resp)
	if err != nil {
		fmt.Fprintf(a.Err, "unable to verify local-agent activity: %v\n", err)
		return false, false
	}
	if len(active) > 0 {
		for _, job := range active {
			fmt.Fprintf(a.Err, "local-agent has an active transfer: profile=%s direction=%s\n", job.Profile, job.Direction)
		}
		return false, false
	}
	if !draining {
		fmt.Fprintln(a.Err, "unable to verify local-agent activity: drain was not activated")
		return false, false
	}
	return true, true
}

func (a App) resumeLocalAgentActivity(opts localAgentOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	endpoint := fmt.Sprintf("http://%s/activity/resume", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return
	}
	resp, err := localAgentHTTPClient().Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func (a App) guardLocalAgentActivity(ctx context.Context, opts localAgentOptions) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	endpoint := fmt.Sprintf("http://%s/activity", net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)))
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		fmt.Fprintf(a.Err, "unable to verify local-agent activity: create request: %v\n", err)
		return false
	}
	resp, err := localAgentHTTPClient().Do(req)
	if err != nil {
		if localAgentConnectionUnavailable(err) {
			return true
		}
		fmt.Fprintf(a.Err, "unable to verify local-agent activity: request failed: %v\n", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return true
	}
	active, err := decodeLocalAgentActivityResponse(resp)
	if err != nil {
		fmt.Fprintf(a.Err, "unable to verify local-agent activity: %v\n", err)
		return false
	}
	if len(active) == 0 {
		return true
	}
	for _, job := range active {
		fmt.Fprintf(a.Err, "local-agent has an active transfer: profile=%s direction=%s\n", job.Profile, job.Direction)
	}
	return false
}

func localAgentHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func localAgentConnectionUnavailable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return !opErr.Timeout()
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && !dnsErr.Timeout()
}

func decodeLocalAgentActivityResponse(resp *http.Response) ([]LocalTransferJob, error) {
	if resp == nil {
		return nil, fmt.Errorf("empty response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP status %s", resp.Status)
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			Active []LocalTransferJob `json:"active"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !envelope.OK {
		reason := strings.TrimSpace(envelope.Error)
		if reason == "" {
			reason = "no error detail"
		}
		return nil, fmt.Errorf("response ok=false: %s", reason)
	}
	return envelope.Data.Active, nil
}

func decodeLocalAgentDrainResponse(resp *http.Response) (bool, []LocalTransferJob, error) {
	if resp == nil {
		return false, nil, fmt.Errorf("empty drain response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, nil, fmt.Errorf("drain HTTP status %s", resp.Status)
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			Draining bool               `json:"draining"`
			Active   []LocalTransferJob `json:"active"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, nil, fmt.Errorf("decode drain response: %w", err)
	}
	if !envelope.OK {
		return false, nil, fmt.Errorf("drain response ok=false: %s", strings.TrimSpace(envelope.Error))
	}
	return envelope.Data.Draining, envelope.Data.Active, nil
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
	mux.HandleFunc("/icon.svg", localAgentCORS(localAgentIconHandler))
	mux.HandleFunc("/health", localAgentCORS(a.localAgentHealthHandler()))
	mux.HandleFunc("/start", localAgentCORS(a.localAgentCommandHandler("start")))
	mux.HandleFunc("/open-vnc", localAgentCORS(a.localAgentCommandHandler("open-vnc")))
	mux.HandleFunc("/ssh", localAgentCORS(a.localAgentSSHHandler()))
	mux.HandleFunc("/terminal/check", localAgentCORS(a.localAgentTerminalCheckHandler()))
	mux.HandleFunc("/terminal/ws", a.localAgentTerminalWSHandler())
	mux.HandleFunc("/sync/push", localAgentCORS(a.localAgentTransferHandler("push")))
	mux.HandleFunc("/sync/pull", localAgentCORS(a.localAgentTransferHandler("pull")))
	mux.HandleFunc("/sync/job", localAgentCORS(a.localAgentTransferJobHandler()))
	mux.HandleFunc("/sync/jobs", localAgentCORS(a.localAgentTransferJobsHandler()))
	mux.HandleFunc("/activity", localAgentCORS(a.localAgentActivityHandler()))
	mux.HandleFunc("/activity/drain", localAgentCORS(a.localAgentDrainHandler()))
	mux.HandleFunc("/activity/resume", localAgentCORS(a.localAgentResumeHandler()))
	mux.HandleFunc("/local/pick", localAgentCORS(a.localAgentPickHandler()))
	mux.HandleFunc("/local/list", localAgentCORS(a.webLocalListHandler()))
	return mux
}

const localAgentIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-labelledby="title desc">
  <title id="title">ConnectMac</title>
  <desc id="desc">Two linked blue chain loops</desc>
  <g fill="none" stroke="#2563eb" stroke-linecap="round" stroke-linejoin="round" stroke-width="7">
    <path d="M25 39 18 46a11 11 0 0 1-16-16l9-9a11 11 0 0 1 16 0"/>
    <path d="m39 25 7-7a11 11 0 0 1 16 16l-9 9a11 11 0 0 1-16 0"/>
    <path d="m21 43 22-22"/>
  </g>
</svg>`

func localAgentIconHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.WriteString(w, localAgentIconSVG)
}

func (a App) localAgentTransferHandler(direction string) http.HandlerFunc {
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
		job, err := a.startLocalAgentTransfer(req, direction)
		if err != nil {
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: err.Error()})
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"job": job}})
	}
}

func (a App) startLocalAgentTransfer(req localAgentRequest, direction string) (LocalTransferJob, error) {
	if a.LocalTransfers == nil {
		return LocalTransferJob{}, fmt.Errorf("local transfer manager is not configured")
	}
	profileName, configPath, err := writeLocalAgentProfileConfig(req)
	if err != nil {
		return LocalTransferJob{}, err
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return LocalTransferJob{}, err
	}
	profile, ok := cfg.Profile(profileName)
	if !ok {
		return LocalTransferJob{}, fmt.Errorf("profile %q not found", profileName)
	}
	if errs := a.Validator.ValidateAccess(profile); len(errs) > 0 {
		return LocalTransferJob{}, fmt.Errorf("%s", strings.Join(validationMessages(errs), "\n"))
	}
	if a.Validator.CheckRsync != nil {
		if err := a.Validator.CheckRsync(); err != nil {
			return LocalTransferJob{}, err
		}
	}

	var rsyncArgs []string
	switch direction {
	case "push":
		localPath := strings.TrimSpace(req.LocalPath)
		if localPath == "" {
			return LocalTransferJob{}, fmt.Errorf("local_path is required")
		}
		if _, err := os.Stat(localPath); err != nil {
			return LocalTransferJob{}, fmt.Errorf("read local path %s: %w", localPath, err)
		}
		remotePath := strings.TrimSpace(req.RemotePath)
		if remotePath == "" {
			remotePath = "~/Downloads/"
		}
		rsyncArgs, err = RsyncPushArgs(profile, localPath, remotePath, mergeSyncFilters(profile.Sync.Push, SyncFilters{}))
	case "pull":
		remotePath := strings.TrimSpace(req.RemotePath)
		if remotePath == "" {
			remotePath = "~/Downloads/"
		}
		localPath := strings.TrimSpace(req.LocalPath)
		if localPath == "" {
			localPath = "."
		} else {
			localPath, err = ExpandPath(localPath)
			if err == nil {
				err = os.MkdirAll(localPath, 0o755)
			}
		}
		if err == nil {
			rsyncArgs, err = RsyncPullArgs(profile, remotePath, localPath, mergeSyncFilters(profile.Sync.Pull, SyncFilters{}))
		}
	default:
		return LocalTransferJob{}, fmt.Errorf("unsupported transfer direction %q", direction)
	}
	if err != nil {
		return LocalTransferJob{}, err
	}

	job, err := a.LocalTransfers.StartWithEvents(strings.TrimSpace(req.TransferID), profileName, direction, a.writeLocalTransferEvent, func(onOutput func(string)) error {
		return a.Runner.RunRsyncProgress(context.Background(), rsyncArgs, onOutput)
	})
	return job, err
}

func (a App) writeLocalTransferEvent(event LocalTransferEvent) {
	action := "transfer.progress"
	level := "info"
	message := "local transfer progress"
	switch event.Status {
	case LocalTransferRunning:
		if event.Percent == 0 {
			action = "transfer.local.started"
			message = "local transfer started"
		}
	case LocalTransferSucceeded:
		action = "transfer.local.succeeded"
		message = "local transfer succeeded"
	case LocalTransferFailed:
		action = "transfer.local.failed"
		level = "error"
		message = sanitizedLocalTransferError(event.Error)
	case LocalTransferInterrupted:
		action = "transfer.local.interrupted"
		level = "warn"
		message = sanitizedLocalTransferError(event.Error)
	}
	message = fmt.Sprintf("%s; percent=%d elapsed=%s", message, event.Percent, event.Elapsed)
	_ = a.LogManager.Write(LogEntry{
		Level:      level,
		Action:     action,
		TransferID: event.TransferID,
		LocalJobID: event.LocalJobID,
		Profile:    event.Profile,
		Direction:  event.Direction,
		Status:     event.Status,
		Percent:    event.Percent,
		ElapsedMS:  event.Elapsed.Milliseconds(),
		Message:    message,
	})
}

var localTransferSecretPattern = regexp.MustCompile(`(?i)(password|token|cookie|session|secret)(?:\s*[:=]\s*|\s+)\S+`)
var localTransferPEMPattern = regexp.MustCompile(`(?s)-----BEGIN [^-]+-----.*?-----END [^-]+-----`)

func sanitizedLocalTransferError(message string) string {
	message = localTransferPEMPattern.ReplaceAllString(message, "[private key]")
	message = localTransferSecretPattern.ReplaceAllString(message, "$1=[redacted]")
	return sanitizeLogText(message)
}

func (a App) localAgentTransferJobHandler() http.HandlerFunc {
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
		job, ok := a.LocalTransfers.Get(id)
		if !ok {
			writeWebError(w, http.StatusNotFound, "transfer job not found")
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"job": job}})
	}
}

func (a App) localAgentTransferJobsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		jobs := a.LocalTransfers.List(strings.TrimSpace(r.URL.Query().Get("profile")))
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"jobs": jobs}})
	}
}

func (a App) localAgentActivityHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{"active": a.LocalTransfers.Active()}})
	}
}

func (a App) localAgentDrainHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		active, draining := a.LocalTransfers.TryDrain()
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{
			"active": active, "draining": draining,
		}})
	}
}

func (a App) localAgentResumeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.LocalTransfers.Resume()
		writeWebJSON(w, webAPIResponse{OK: true})
	}
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
			a.writeLocalAgentVNCEarlyFailure(command)
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		profileName, configPath, err := writeLocalAgentProfileConfig(req)
		if err != nil {
			a.writeLocalAgentVNCEarlyFailure(command)
			writeWebJSON(w, webAPIResponse{OK: false, Code: 1, Error: err.Error()})
			return
		}
		args := []string{command, profileName, "--config", configPath}
		switch command {
		case "start", "open-vnc":
			resp := a.localAgentRunVNC(r.Context(), command, profileName, configPath)
			writeWebJSON(w, resp)
			return
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
	app := a
	app.Out = &out
	app.Err = &errOut
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

func (a App) localAgentRunVNC(ctx context.Context, command, profileName, configPath string) webAPIResponse {
	return a.localAgentRunVNCWithBeforeLock(ctx, command, profileName, configPath, nil)
}

func (a App) localAgentRunVNCWithBeforeLock(ctx context.Context, command, profileName, configPath string, beforeLock func()) webAPIResponse {
	var out bytes.Buffer
	var errOut bytes.Buffer
	app := a
	app.Out = &out
	app.Err = &errOut

	cfg, code := app.loadCommandConfig(ctx, configPath)
	if code != 0 {
		resp := webAPIResponse{OK: false, Code: code, Output: out.String(), Error: errOut.String()}
		a.writeLocalAgentVNCLog(LogEntry{
			Level: "error", Action: "local-agent.vnc", Profile: profileName,
			Outcome: "failure", Message: "load local VNC profile failed",
		})
		return resp
	}
	profile, _ := cfg.Profile(profileName)
	entry := LogEntry{
		Action:     "local-agent.vnc",
		Profile:    profileName,
		LocalPorts: profileLocalPorts(profile),
	}

	switch command {
	case "start":
		code, result := app.runStartResult(ctx, cfg, []string{profileName})
		entry.Profile = result.Profile
		entry.LocalPorts = result.LocalPorts
		entry.TunnelAction = result.Action
		entry.PID = result.PID
		entry.Outcome = outcomeForCode(code)
		if code != 0 {
			entry.Level = "error"
			entry.Message = "local VNC tunnel start failed"
		} else {
			entry.Message = "local VNC tunnel ready"
		}
		a.writeLocalAgentVNCLog(entry)
		return webAPIResponse{OK: code == 0, Code: code, Output: out.String(), Error: errOut.String()}
	case "open-vnc":
		code := 0
		if beforeLock != nil {
			beforeLock()
		}
		err := app.StateManager.WithProfileLock(profile.Name, func() error {
			code = app.runOpenVNC(ctx, cfg, []string{profileName})
			entry.PID = app.verifiedTunnelPID(profile)
			return nil
		})
		if err != nil {
			fmt.Fprintf(app.Err, "lock open-vnc lifecycle: %v\n", err)
			code = 1
		}
		entry.Outcome = outcomeForCode(code)
		entry.LaunchResult = entry.Outcome
		if code != 0 {
			entry.Level = "error"
			entry.Message = "local VNC launch failed"
		} else {
			entry.Message = "local VNC launched"
		}
		a.writeLocalAgentVNCLog(entry)
		return webAPIResponse{OK: code == 0, Code: code, Output: out.String(), Error: errOut.String()}
	default:
		return webAPIResponse{OK: false, Code: 2, Error: "unsupported local VNC command"}
	}
}

func outcomeForCode(code int) string {
	if code == 0 {
		return "success"
	}
	return "failure"
}

func (a App) writeLocalAgentVNCLog(entry LogEntry) {
	_ = a.LogManager.Write(entry)
}

func (a App) writeLocalAgentVNCEarlyFailure(command string) {
	if command != "start" && command != "open-vnc" {
		return
	}
	entry := LogEntry{
		Level: "error", Action: "local-agent.vnc", Outcome: "failure",
		Message: "local VNC request failed",
	}
	a.writeLocalAgentVNCLog(entry)
}

func (a App) verifiedTunnelPID(profile Profile) int {
	stateKey := profile.Name
	state, ok, err := a.StateManager.Load(stateKey)
	if err != nil || !ok || !state.Matches(profile) {
		return 0
	}
	sshArgs, err := SSHArgs(profile)
	if err != nil || a.StateManager.VerifyExpectedManagedProcess(state, sshArgs) != nil {
		return 0
	}
	return state.PID
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
