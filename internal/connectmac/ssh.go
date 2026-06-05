package connectmac

import (
	"fmt"
	"strconv"
)

func SSHArgs(profile Profile) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	args := []string{"-N"}
	for _, tunnel := range profile.Tunnels {
		args = append(args, "-L", fmt.Sprintf("%d:%s:%d", tunnel.LocalPort, tunnel.RemoteHost, tunnel.RemotePort))
	}
	args = append(args,
		"-i", keyPath,
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		fmt.Sprintf("%s@%s", profile.User, profile.Host),
	)
	return args, nil
}

func InteractiveSSHArgs(profile Profile) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	return []string{
		"-i", keyPath,
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		fmt.Sprintf("%s@%s", profile.User, profile.Host),
	}, nil
}

func SSHScriptArgs(profile Profile) ([]string, error) {
	keyPath, err := ExpandPath(profile.IdentityFile)
	if err != nil {
		return nil, err
	}
	return []string{
		"-i", keyPath,
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		fmt.Sprintf("%s@%s", profile.User, profile.Host),
		"/bin/bash", "-s",
	}, nil
}

func TunnelSummary(t Tunnel) string {
	return "localhost:" + strconv.Itoa(t.LocalPort) + " -> " + t.RemoteHost + ":" + strconv.Itoa(t.RemotePort)
}
