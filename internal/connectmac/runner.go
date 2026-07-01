package connectmac

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
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
	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
