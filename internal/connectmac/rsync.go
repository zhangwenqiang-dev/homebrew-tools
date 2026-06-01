package connectmac

import "fmt"

func RemoteTarget(profile Profile, path string) string {
	return fmt.Sprintf("%s@%s:%s", profile.User, profile.Host, path)
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

func RsyncPushArgs(profile Profile, localPath, remoteDir string) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	return []string{
		"-avzP",
		"-e", "ssh -i " + keyPath,
		localPath,
		RemoteTarget(profile, remoteDir),
	}, nil
}
