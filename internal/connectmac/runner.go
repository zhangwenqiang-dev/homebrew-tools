package connectmac

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

func (ExecRunner) RunForeground(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func (ExecRunner) StartBackground(ctx context.Context, args []string) (int, error) {
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-waitDone:
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("ssh tunnel exited before it became healthy")
	case <-timer.C:
		return pid, cmd.Process.Release()
	}
}
func (ExecRunner) Stop(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
func (ExecRunner) RunRsync(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, defaultRsyncCommandPath(), args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (ExecRunner) RunRsyncProgress(ctx context.Context, args []string, onOutput func(string)) error {
	return ExecRunner{}.RunRsyncCommandProgress(ctx, defaultRsyncCommandPath(), args, onOutput)
}

func (ExecRunner) RunRsyncCommandProgress(ctx context.Context, path string, args []string, onOutput func(string)) error {
	if path == "" {
		path = "rsync"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = outputCallbackWriter{onOutput: onOutput}
	cmd.Stderr = outputCallbackWriter{onOutput: onOutput}
	return cmd.Run()
}

func (ExecRunner) RsyncCommandOutput(ctx context.Context, path string, args []string) ([]byte, error) {
	if path == "" {
		path = "rsync"
	}
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

type outputCallbackWriter struct {
	onOutput func(string)
}

func (w outputCallbackWriter) Write(data []byte) (int, error) {
	if w.onOutput != nil && len(data) > 0 {
		w.onOutput(string(data))
	}
	return len(data), nil
}
func (ExecRunner) KnownHostKey(ctx context.Context, host string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-F", host)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return string(out), nil
		}
		return string(out), err
	}
	return string(out), nil
}
func (ExecRunner) ScanHostKey(ctx context.Context, host string) (string, error) {
	result, err := (ExecRunner{}).ScanHostKeyWithMetadata(ctx, host)
	return result.Keys, err
}

func (ExecRunner) ScanHostKeyWithMetadata(ctx context.Context, host string) (HostKeyScanResult, error) {
	result := HostKeyScanResult{Scanner: "ssh-keyscan"}
	normalizedHost, err := normalizeHostKeyScanHost(host)
	if err != nil {
		return result, err
	}
	cmd := exec.CommandContext(ctx, "ssh-keyscan", "-T", "5", "--", normalizedHost)
	var primaryOutput, primaryDiagnostic boundedBuffer
	cmd.Stdout = &primaryOutput
	cmd.Stderr = &primaryDiagnostic
	primaryErr := cmd.Run()
	if keys := validHostKeyRecords(primaryOutput.data, normalizedHost); keys != "" {
		result.Keys = keys
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return canceledHostKeyScan(result, err)
	}
	result.Scanner = "ssh-fallback"

	tempDir, err := os.MkdirTemp("", "connectmac-host-key-")
	if err != nil {
		return failedHostKeyScan(result, primaryErr, primaryDiagnostic.String(), fmt.Errorf("create isolated storage: %w", err), "")
	}
	defer os.RemoveAll(tempDir)
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return failedHostKeyScan(result, primaryErr, primaryDiagnostic.String(), fmt.Errorf("secure isolated storage: %w", err), "")
	}
	knownHosts := filepath.Join(tempDir, "known_hosts")
	file, err := os.OpenFile(knownHosts, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return failedHostKeyScan(result, primaryErr, primaryDiagnostic.String(), fmt.Errorf("create isolated host key file: %w", err), "")
	}
	if err := file.Close(); err != nil {
		return failedHostKeyScan(result, primaryErr, primaryDiagnostic.String(), fmt.Errorf("close isolated host key file: %w", err), "")
	}

	args := []string{
		"-F", "/dev/null",
		"-n", "-T",
		"-o", "BatchMode=yes",
		"-o", "PreferredAuthentications=none",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "IdentityFile=none",
		"-o", "CertificateFile=none",
		"-o", "IdentityAgent=none",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ClearAllForwardings=yes",
		"-o", "ProxyCommand=none",
		"--", normalizedHost, "true",
	}
	fallbackCmd := exec.CommandContext(ctx, "ssh", args...)
	var fallbackDiagnostic boundedBuffer
	fallbackCmd.Stderr = &fallbackDiagnostic
	fallbackErr := fallbackCmd.Run()
	data, readErr := os.ReadFile(knownHosts)
	if readErr == nil {
		if keys := validHostKeyRecords(data, normalizedHost); keys != "" {
			result.Keys = keys
			return result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return canceledHostKeyScan(result, err)
	}
	if readErr != nil {
		fallbackErr = fmt.Errorf("read isolated host key file: %w", readErr)
	}
	return failedHostKeyScan(result, primaryErr, primaryDiagnostic.String(), fallbackErr, fallbackDiagnostic.String())
}

func canceledHostKeyScan(result HostKeyScanResult, err error) (HostKeyScanResult, error) {
	if errors.Is(err, context.Canceled) {
		result.Cause = "canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		result.Cause = "deadline_exceeded"
	}
	return result, err
}

const maxHostKeyDiagnosticBytes = 4096

type boundedBuffer struct{ data []byte }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxHostKeyDiagnosticBytes - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b boundedBuffer) String() string { return string(b.data) }

func failedHostKeyScan(result HostKeyScanResult, primaryErr error, primaryText string, fallbackErr error, fallbackText string) (HostKeyScanResult, error) {
	result.Cause = classifyHostKeyScanCause([]error{fallbackErr}, fallbackText)
	result.Detail = hostKeyScanError(primaryErr, fallbackErr).Error()
	return result, errors.New(result.Detail)
}

func classifyHostKeyScanCause(errs []error, text string) string {
	for _, err := range errs {
		if hostKeyScanUnreachableError(err) {
			return "unreachable"
		}
	}
	if len(text) > maxHostKeyDiagnosticBytes {
		text = text[:maxHostKeyDiagnosticBytes]
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"connection refused", "connection reset", "connection timed out", "operation timed out",
		"host is down", "no route to host", "network is unreachable", "network unreachable",
		"could not resolve hostname", "name or service not known", "temporary failure in name resolution",
		"nodename nor servname provided, or not known",
	} {
		if strings.Contains(lower, marker) {
			return "unreachable"
		}
	}
	return "scanner_compatibility"
}

func hostKeyScanUnreachableError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.EHOSTDOWN) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func normalizeHostKeyScanHost(host string) (string, error) {
	if host == "" || strings.TrimSpace(host) != host {
		return "", errors.New("invalid host for host key scan")
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("invalid host for host key scan")
		}
	}
	if strings.HasPrefix(host, "[") {
		if !strings.HasSuffix(host, "]") || strings.Count(host, "[") != 1 || strings.Count(host, "]") != 1 {
			return "", errors.New("invalid host for host key scan")
		}
		addr, err := netip.ParseAddr(host[1 : len(host)-1])
		if err != nil || !addr.Is6() {
			return "", errors.New("invalid host for host key scan")
		}
		return addr.String(), nil
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String(), nil
	}
	if len(host) > 253 || strings.ContainsAny(host, ":@/,\\") || strings.HasPrefix(host, "-") {
		return "", errors.New("invalid host for host key scan")
	}
	if strings.HasSuffix(host, "..") {
		return "", errors.New("invalid host for host key scan")
	}
	trimmed := strings.TrimSuffix(host, ".")
	if trimmed == "" {
		return "", errors.New("invalid host for host key scan")
	}
	allNumeric := true
	for _, label := range strings.Split(trimmed, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid host for host key scan")
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return "", errors.New("invalid host for host key scan")
			}
			if r < '0' || r > '9' {
				allNumeric = false
			}
		}
	}
	if allNumeric && strings.Contains(trimmed, ".") {
		return "", errors.New("invalid host for host key scan")
	}
	return strings.ToLower(trimmed), nil
}

func validHostKeyRecords(data []byte, host string) string {
	var records []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || !hostKeyRecordHostMatches(fields[0], host) || !supportedHostKeyType(fields[1]) {
			continue
		}
		key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(fields[1] + " " + fields[2]))
		if err != nil || len(rest) != 0 || key.Type() != fields[1] {
			continue
		}
		records = append(records, strings.Join(fields[:3], " "))
	}
	if len(records) == 0 {
		return ""
	}
	return strings.Join(records, "\n") + "\n"
}

func hostKeyRecordHostMatches(recordHost, host string) bool {
	if strings.HasPrefix(recordHost, "@") || strings.Contains(recordHost, ",") {
		return false
	}
	return strings.EqualFold(recordHost, host) || strings.EqualFold(recordHost, "["+host+"]:22")
}

func supportedHostKeyType(keyType string) bool {
	switch keyType {
	case "ssh-ed25519", "ssh-rsa",
		"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
		return true
	default:
		return false
	}
}

func hostKeyScanError(primaryErr, fallbackErr error) error {
	return fmt.Errorf("host key scan failed (primary: %s; fallback: %s)",
		hostKeyScanStage(primaryErr), hostKeyScanStage(fallbackErr))
}

func hostKeyScanStage(err error) string {
	if err == nil {
		return "no valid keys"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit %d", exitErr.ExitCode())
	}
	return "execution failed"
}
func (ExecRunner) ForgetHost(ctx context.Context, host string) error {
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-R", host)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func (ExecRunner) OpenURL(ctx context.Context, target string) error {
	cmd := exec.CommandContext(ctx, "open", target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r ExecRunner) OpenVNC(ctx context.Context, target string) error {
	cmd := r.openVNCCommand(ctx, target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (ExecRunner) openVNCCommand(ctx context.Context, target string) *exec.Cmd {
	return exec.CommandContext(ctx, "open", "-n", "-a", "Screen Sharing", target)
}
