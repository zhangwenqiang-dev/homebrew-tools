package connectmac

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type HostKeyStatus string

const (
	HostKeyMissing    HostKeyStatus = "missing"
	HostKeyCurrent    HostKeyStatus = "current"
	HostKeyStale      HostKeyStatus = "stale"
	HostKeyScanFailed HostKeyStatus = "scan-failed"
)

type HostKeyCheck struct {
	Host    string
	Status  HostKeyStatus
	Scanned string
	Known   string
	Message string
}

func (a App) checkHostKey(ctx context.Context, profile Profile) (HostKeyCheck, error) {
	result := HostKeyCheck{Host: profile.Host}
	if profile.Host == "" {
		return result, fmt.Errorf("host is required")
	}
	scanned, err := a.Runner.ScanHostKey(ctx, profile.Host)
	result.Scanned = scanned
	if err != nil || len(hostKeyPairs(scanned)) == 0 {
		result.Status = HostKeyScanFailed
		if err != nil {
			result.Message = err.Error()
		} else {
			result.Message = "no host key returned by ssh-keyscan"
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

func (a App) fixHostKey(ctx context.Context, profile Profile) (HostKeyCheck, error) {
	check, err := a.checkHostKey(ctx, profile)
	if err != nil {
		return check, err
	}
	switch check.Status {
	case HostKeyCurrent:
		check.Message = "host key current, unchanged"
		return check, nil
	case HostKeyMissing:
		if err := a.appendKnownHostKey(check.Scanned); err != nil {
			return check, err
		}
		check.Message = "host key missing, added current key"
		return check, nil
	case HostKeyStale:
		if err := a.Runner.ForgetHost(ctx, profile.Host); err != nil {
			return check, err
		}
		if err := a.appendKnownHostKey(check.Scanned); err != nil {
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
