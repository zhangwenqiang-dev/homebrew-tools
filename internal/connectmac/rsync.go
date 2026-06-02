package connectmac

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func RsyncPullArgs(profile Profile, remotePath, localDir string, excludes []string) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-avzP",
		"-e", "ssh -i " + keyPath,
	}
	for _, exclude := range excludes {
		args = append(args, "--exclude", exclude)
	}
	args = append(args, RemoteTarget(profile, remotePath), localDir)
	return args, nil
}

func RsyncPushArgs(profile Profile, localPath, remoteDir string, excludes []string) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	remoteDir = NormalizeRemotePath(remoteDir)
	args := []string{
		"-avzP",
		"-e", "ssh -i " + keyPath,
	}
	for _, exclude := range excludes {
		args = append(args, "--exclude", exclude)
	}
	args = append(args, localPath, RemoteTarget(profile, remoteDir))
	return args, nil
}
