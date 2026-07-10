package connectmac

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func RemoteTarget(profile Profile, path string) string {
	return fmt.Sprintf("%s@%s:%s", profile.User, profile.Host, path)
}

func NormalizeRemotePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	cleanHome := filepath.Clean(home)
	cleanPath := filepath.Clean(path)
	if cleanPath == cleanHome {
		return "~"
	}
	if strings.HasPrefix(cleanPath, cleanHome+string(os.PathSeparator)) {
		rel, err := filepath.Rel(cleanHome, cleanPath)
		if err != nil || rel == "." {
			return path
		}
		normalized := "~/" + filepath.ToSlash(rel)
		if strings.HasSuffix(path, string(os.PathSeparator)) || strings.HasSuffix(path, "/") {
			normalized += "/"
		}
		return normalized
	}
	return path
}

type SyncFilters struct {
	Includes []string
	Excludes []string
}

func RsyncPullArgs(profile Profile, remotePath, localDir string, filters SyncFilters) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-avzP",
		"-e", "ssh -i " + keyPath,
	}
	args = appendRsyncFilters(args, filters)
	args = append(args, RemoteTarget(profile, EscapeRemotePath(remotePath)), localDir)
	return args, nil
}

func RsyncPushArgs(profile Profile, localPath, remoteDir string, filters SyncFilters) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	remoteDir = NormalizeRemotePath(remoteDir)
	args := []string{
		"-avzP",
		"-e", "ssh -i " + keyPath,
	}
	args = appendRsyncFilters(args, filters)
	args = append(args, localPath, RemoteTarget(profile, EscapeRemotePath(remoteDir)))
	return args, nil
}

func EscapeRemotePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if shouldEscapeRemotePathRune(r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func shouldEscapeRemotePathRune(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	return strings.ContainsRune("\\'\"$`&;()|<>*?[]{}!", r)
}

func appendRsyncFilters(args []string, filters SyncFilters) []string {
	for _, include := range filters.Includes {
		args = append(args, "--include", include)
	}
	for _, exclude := range filters.Excludes {
		args = append(args, "--exclude", exclude)
	}
	if len(filters.Includes) > 0 {
		args = append(args, "--exclude", "*")
	}
	return args
}
