package connectmac

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type HostKeyStatus string

const (
	HostKeyMissing    HostKeyStatus = "missing"
	HostKeyCurrent    HostKeyStatus = "current"
	HostKeyStale      HostKeyStatus = "stale"
	HostKeyScanFailed HostKeyStatus = "scan-failed"
)

type HostKeyCheck struct {
	Host       string
	Status     HostKeyStatus
	Scanned    string
	Known      string
	Message    string
	Scanner    string `json:"scanner,omitempty"`
	ScanCause  string `json:"-"`
	ScanDetail string `json:"-"`
}

func (a App) checkHostKey(ctx context.Context, profile Profile) (HostKeyCheck, error) {
	result := HostKeyCheck{Host: profile.Host}
	if profile.Host == "" {
		return result, fmt.Errorf("host is required")
	}
	var scanned string
	var err error
	if scanner, ok := a.Runner.(HostKeyScanner); ok {
		metadata, scanErr := scanner.ScanHostKeyWithMetadata(ctx, profile.Host)
		scanned, err = metadata.Keys, scanErr
		result.Scanner, result.ScanCause, result.ScanDetail = metadata.Scanner, metadata.Cause, metadata.Detail
	} else {
		scanned, err = a.Runner.ScanHostKey(ctx, profile.Host)
		if errors.Is(err, context.Canceled) {
			result.ScanCause = "canceled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			result.ScanCause = "deadline_exceeded"
		} else if err != nil {
			result.ScanCause = classifyHostKeyScanCause([]error{err}, err.Error())
		}
	}
	if errors.Is(err, context.Canceled) {
		result.ScanCause = "canceled"
		result.Scanned = scanned
		result.Status = HostKeyScanFailed
		result.Message = "host key scan canceled"
		return result, context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.ScanCause = "deadline_exceeded"
		result.Scanned = scanned
		result.Status = HostKeyScanFailed
		result.Message = "host key scan timed out"
		return result, context.DeadlineExceeded
	}
	if err != nil && result.ScanCause == "" {
		result.ScanCause = classifyHostKeyScanCause([]error{err}, err.Error())
	}
	result.Scanned = scanned
	if err != nil || len(hostKeyPairs(scanned)) == 0 {
		result.Status = HostKeyScanFailed
		if err != nil {
			result.Message = hostKeyScanFailureMessage(result.ScanCause)
		} else {
			result.Message = hostKeyScanFailureMessage(result.ScanCause)
		}
		return result, nil
	}
	known, err := a.Runner.KnownHostKey(ctx, profile.Host)
	result.Known = known
	if err != nil {
		return result, err
	}
	knownPairs := hostKeyPairs(known)
	if len(knownPairs) == 0 {
		result.Status = HostKeyMissing
		result.Message = "host key missing"
		return result, nil
	}
	if hasSharedHostKeyPair(knownPairs, hostKeyPairs(scanned)) {
		result.Status = HostKeyCurrent
		result.Message = "host key current"
		return result, nil
	}
	result.Status = HostKeyStale
	result.Message = "host key stale"
	return result, nil
}

func hostKeyScanFailureMessage(cause string) string {
	if cause == "unreachable" {
		return "host key scan failed: SSH service is unreachable; verify the host, network, and port 22"
	}
	return "host key scan failed: SSH Host Key negotiation failed; update OpenSSH or check server Host Key compatibility"
}

func (a App) requireCurrentHostKey(ctx context.Context, profile Profile) (HostKeyCheck, error) {
	check, err := a.checkHostKey(ctx, profile)
	if errors.Is(err, context.Canceled) {
		return check, context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return check, context.DeadlineExceeded
	}
	if err != nil {
		check.Status = HostKeyScanFailed
		check.Message = "host key check failed"
		return check, a.hostKeyBlocked(ctx, profile, check, "host_key_scan_failed",
			"host key check failed; verify network access and try again")
	}
	if check.Status == HostKeyCurrent {
		return check, nil
	}

	code := "host_key_scan_failed"
	message := "host key scan failed; verify network access and try again"
	switch check.Status {
	case HostKeyStale:
		code = "host_key_changed"
		message = "host key changed; confirm the new fingerprint, then run cm host-key fix <profile>"
	case HostKeyMissing:
		code = "host_key_missing"
		message = "host key is missing; confirm the fingerprint, then run cm host-key fix <profile>"
	case HostKeyScanFailed:
		message = check.Message
	default:
		message = "host key status is unknown; verify network access and try again"
	}
	return check, a.hostKeyBlocked(ctx, profile, check, code, message)
}

func (a App) hostKeyBlocked(ctx context.Context, profile Profile, check HostKeyCheck, code, message string) error {
	op := operationContextFrom(ctx)
	if op.RequestID == "" {
		op.RequestID, _ = newRequestID(time.Now(), rand.Reader)
	}
	source := op.Source
	if source == "" {
		source = "cli"
	}
	cache := a.HostKeyBlockedEvents
	if cache == nil {
		cache = defaultHostKeyBlockedEvents
	}
	if cache.First(op.RequestID + "\x00" + profile.Name + "\x00" + code) {
		a.writeRuntimeLog(LogEntry{
			Level: "error", Action: "host-key.blocked", Profile: profile.Name,
			AppleEmail: profile.AWS.AccountEmail, RequestID: op.RequestID, Source: source,
			Status: string(check.Status), Outcome: "failure", ErrorCode: code,
			Scanner: check.Scanner, FailureStage: check.ScanCause, Message: boundedHostKeyDiagnostic(message, check.ScanDetail),
		})
	}
	return LocalCodedError{Code: code, Cause: errors.New(message)}
}

func boundedHostKeyDiagnostic(message, detail string) string {
	if detail == "" || !safeHostKeyDiagnosticPattern.MatchString(detail) {
		return message
	}
	combined := message + "; diagnostic: " + detail
	if len(combined) > 240 {
		return combined[:240]
	}
	return combined
}

var safeHostKeyDiagnosticPattern = regexp.MustCompile(`^host key scan failed \(primary: (?:no valid keys|canceled|timed out|execution failed|exit [0-9]+); fallback: (?:no valid keys|canceled|timed out|execution failed|exit [0-9]+)\)$`)

func (a App) applyCheckedHostKey(ctx context.Context, check HostKeyCheck) (HostKeyCheck, error) {
	if check.Host == "" {
		return check, fmt.Errorf("host is required")
	}
	if check.Status != HostKeyCurrent && len(hostKeyPairs(check.Scanned)) == 0 {
		return check, fmt.Errorf("validated scanned host key is required")
	}
	switch check.Status {
	case HostKeyCurrent:
		check.Message = "host key current, unchanged"
		return check, nil
	case HostKeyMissing:
		if err := a.replaceKnownHostKeyAtomic(check); err != nil {
			return check, err
		}
		check.Message = "host key missing, added current key"
		return check, nil
	case HostKeyStale:
		if err := a.replaceKnownHostKeyAtomic(check); err != nil {
			return check, err
		}
		check.Message = "host key stale, replaced"
		return check, nil
	case HostKeyScanFailed:
		return check, nil
	default:
		return check, fmt.Errorf("unknown host key status %q", check.Status)
	}
}

var defaultHostKeyBlockedEvents = newHostKeyBlockedEventCache(1024, time.Minute)

func (a App) replaceKnownHostKeyAtomic(check HostKeyCheck) error {
	knownHosts := a.KnownHosts
	if knownHosts == "" {
		knownHosts = "~/.ssh/known_hosts"
	}
	path, err := ExpandPath(knownHosts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create known_hosts directory: %w", err)
	}
	path, err = normalizedKnownHostsPath(path)
	if err != nil {
		return fmt.Errorf("normalize known_hosts path: %w", err)
	}
	lock := normalizedKnownHostsPathLock(path)
	lock.Lock()
	defer lock.Unlock()
	return withFileLock(path+".lock", func() error {
		current, err := readKnownHosts(path)
		if err != nil {
			return err
		}
		updated, err := replaceKnownHostKeys(current, check.Host, check.Scanned)
		if err != nil {
			return err
		}
		write := a.WriteKnownHostsAtomic
		if write == nil {
			write = writeFileAtomically
		}
		return write(path, updated, knownHostsMode(path))
	})
}

func hostKeyPairs(text string) map[string]bool {
	pairs := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		keyType := fields[1]
		keyValue := fields[2]
		if strings.HasPrefix(keyType, "ssh-") || strings.HasPrefix(keyType, "ecdsa-") {
			pairs[keyType+" "+keyValue] = true
		}
	}
	return pairs
}

func hasSharedHostKeyPair(a, b map[string]bool) bool {
	for key := range a {
		if b[key] {
			return true
		}
	}
	return false
}

func trustedHostKeyAlgorithms(check HostKeyCheck) ([]string, error) {
	known := hostKeyPairs(check.Known)
	scanned := hostKeyPairs(check.Scanned)
	trustedTypes := map[string]bool{}
	for pair := range known {
		if !scanned[pair] {
			continue
		}
		keyType, _, ok := strings.Cut(pair, " ")
		if ok {
			trustedTypes[keyType] = true
		}
	}

	preference := []string{
		ssh.KeyAlgoED25519,
		ssh.KeyAlgoECDSA521,
		ssh.KeyAlgoECDSA384,
		ssh.KeyAlgoECDSA256,
		ssh.KeyAlgoRSASHA512,
		ssh.KeyAlgoRSASHA256,
		ssh.KeyAlgoRSA,
	}
	algorithms := make([]string, 0, len(preference))
	for _, algorithm := range preference {
		keyType := algorithm
		if algorithm == ssh.KeyAlgoRSASHA512 || algorithm == ssh.KeyAlgoRSASHA256 {
			keyType = ssh.KeyAlgoRSA
		}
		if trustedTypes[keyType] {
			algorithms = append(algorithms, algorithm)
		}
	}
	if len(algorithms) == 0 {
		return nil, LocalCodedError{Code: "host_key_algorithm_mismatch", Cause: errors.New("no confirmed SSH Host Key algorithm matches the current host")}
	}
	return algorithms, nil
}

func mustTrustedHostKeyAlgorithms(check HostKeyCheck) []string {
	algorithms, _ := trustedHostKeyAlgorithms(check)
	return algorithms
}

func hostKeyFingerprints(text string) []string {
	seen := map[string]bool{}
	var fingerprints []string
	for pair := range hostKeyPairs(text) {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pair))
		if err != nil {
			continue
		}
		fingerprint := ssh.FingerprintSHA256(key)
		if !seen[fingerprint] {
			seen[fingerprint] = true
			fingerprints = append(fingerprints, fingerprint)
		}
	}
	sort.Strings(fingerprints)
	return fingerprints
}

func confirmedHostKeyMaterial(text string, confirmed []string) (string, error) {
	confirmed = normalizeFingerprintSet(confirmed)
	if len(confirmed) == 0 {
		return "", fmt.Errorf("confirmed host key fingerprints are required")
	}
	seen := map[string]bool{}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(fields[1] + " " + fields[2]))
		if err != nil {
			continue
		}
		fingerprint := ssh.FingerprintSHA256(key)
		if !seen[fingerprint] {
			seen[fingerprint] = true
			lines = append(lines, strings.Join(fields[:3], " "))
		}
	}
	actual := make([]string, 0, len(seen))
	for fingerprint := range seen {
		actual = append(actual, fingerprint)
	}
	if fingerprintSetDigest(actual) != fingerprintSetDigest(confirmed) {
		return "", fmt.Errorf("scanned host keys do not match the complete confirmed fingerprint set")
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func (a App) appendKnownHostKey(scanned string) error {
	knownHosts := a.KnownHosts
	if knownHosts == "" {
		knownHosts = "~/.ssh/known_hosts"
	}
	path, err := ExpandPath(knownHosts)
	if err != nil {
		return err
	}
	return appendFileLines(path, strings.TrimSpace(scanned)+"\n", 0o600)
}

func appendFileLines(path, data string, mode os.FileMode) error {
	if strings.TrimSpace(data) == "" {
		return fmt.Errorf("host key data is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, mode)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(data); err != nil {
		return err
	}
	return nil
}
