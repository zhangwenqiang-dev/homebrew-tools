package connectmac

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type PortChecker func(port int) error
type SSHChecker func() error

type Validator struct {
	CheckPort  PortChecker
	CheckSSH   SSHChecker
	CheckRsync SSHChecker
}

func NewValidator() Validator {
	return Validator{
		CheckPort:  CheckLocalPortAvailable,
		CheckSSH:   CheckSSHAvailable,
		CheckRsync: CheckRsyncAvailable,
	}
}

func (v Validator) ValidateProfile(profile Profile) []error {
	errs := v.ValidateAccess(profile)
	if len(profile.Tunnels) == 0 {
		errs = append(errs, errors.New("at least one tunnel is required"))
	}
	for i, tunnel := range profile.Tunnels {
		if tunnel.LocalPort < 1 || tunnel.LocalPort > 65535 {
			errs = append(errs, fmt.Errorf("tunnel %d local_port must be between 1 and 65535", i+1))
		}
		if tunnel.RemoteHost == "" {
			errs = append(errs, fmt.Errorf("tunnel %d remote_host is required", i+1))
		}
		if tunnel.RemotePort < 1 || tunnel.RemotePort > 65535 {
			errs = append(errs, fmt.Errorf("tunnel %d remote_port must be between 1 and 65535", i+1))
		}
		if v.CheckPort != nil && tunnel.LocalPort > 0 {
			if err := v.CheckPort(tunnel.LocalPort); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func (v Validator) ValidateAccess(profile Profile) []error {
	var errs []error
	if profile.Name == "" {
		errs = append(errs, errors.New("profile name is empty"))
	}
	if profile.User == "" {
		errs = append(errs, errors.New("user is required"))
	}
	if profile.Host == "" {
		errs = append(errs, errors.New("host is required"))
	}
	keyPath, err := ExpandPath(profile.IdentityFile)
	if profile.IdentityFile == "" {
		errs = append(errs, errors.New("identity_file is required"))
	} else if err != nil {
		errs = append(errs, err)
	} else if pathErr := validateIdentityFileLocation(keyPath); pathErr != nil {
		errs = append(errs, pathErr)
	} else if statErr := validateIdentityFile(keyPath); statErr != nil {
		errs = append(errs, statErr)
	}
	if v.CheckSSH != nil {
		if err := v.CheckSSH(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func ValidateNamedProfile(cfg Config, name string, validator Validator) (Profile, []error) {
	profile, ok := cfg.Profile(name)
	if !ok {
		return Profile{}, []error{unknownProfileError(cfg, name)}
	}
	return profile, validator.ValidateProfile(profile)
}

func CheckLocalPortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("local port %d is already in use", port)
	}
	return listener.Close()
}

func CheckSSHAvailable() error {
	if _, err := exec.LookPath("ssh"); err != nil {
		return errors.New("ssh executable not found on PATH")
	}
	return nil
}

func CheckRsyncAvailable() error {
	if _, err := exec.LookPath("rsync"); err != nil {
		return errors.New("rsync executable not found on PATH")
	}
	return nil
}

func validateIdentityFileLocation(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find home directory: %w", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve identity file path %s: %w", path, err)
	}
	cleanSSHDir, err := filepath.Abs(sshDir)
	if err != nil {
		return fmt.Errorf("resolve ssh directory %s: %w", sshDir, err)
	}
	if cleanPath == cleanSSHDir || !strings.HasPrefix(cleanPath, cleanSSHDir+string(os.PathSeparator)) {
		return fmt.Errorf("identity file must be stored under %s; move the PEM into ~/.ssh and update identity_file", sshDir)
	}
	return nil
}

func validateIdentityFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("identity file does not exist: %s", path)
		}
		return fmt.Errorf("read identity file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("identity file is a directory: %s", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("identity file permissions are too open: %s has mode %o; run chmod 600 %s", path, info.Mode().Perm(), path)
	}
	return nil
}

func unknownProfileError(cfg Config, name string) error {
	if len(cfg.Profiles) == 0 {
		return fmt.Errorf("unknown profile %q; no profiles are configured", name)
	}
	names := make([]string, 0, len(cfg.Profiles))
	for profileName := range cfg.Profiles {
		names = append(names, profileName)
	}
	return fmt.Errorf("unknown profile %q; available profiles: %v", name, names)
}
