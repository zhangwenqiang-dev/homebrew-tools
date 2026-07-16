package connectmac

import (
	"context"
	"fmt"
	"strings"
)

func (a App) runConnect(ctx context.Context, cfg Config, args []string) int {
	profile, ok := requireProfileArg(a.Err, cfg, args)
	if !ok {
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	if !a.validateAndSummarize(profile) {
		return 1
	}
	sshArgs, err := SSHArgs(profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if err := a.Runner.RunForeground(ctx, sshArgs); err != nil {
		fmt.Fprintf(a.Err, "ssh failed: %v\n", err)
		return 1
	}
	return 0
}
func (a App) runStart(ctx context.Context, cfg Config, args []string) int {
	profileRef := ""
	if len(args) == 1 {
		profileRef = args[0]
	}
	profile, ok := requireProfileArg(a.Err, cfg, args)
	if !ok {
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	stateKey := profile.Name
	if stateKey == "" {
		stateKey = profileRef
	}
	if state, ok, err := a.StateManager.Load(stateKey); err != nil {
		fmt.Fprintf(a.Err, "load state: %v\n", err)
		return 1
	} else if ok {
		fmt.Fprintf(a.Out, "already started %s with pid %d\n", stateKey, state.PID)
		return 0
	}
	if !a.validateAndSummarize(profile) {
		return 1
	}
	check, err := a.fixHostKey(ctx, profile)
	if err != nil {
		fmt.Fprintf(a.Err, "host key fix failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "Host key: %s (%s)\n", check.Status, check.Message)
	if check.Status == HostKeyScanFailed {
		fmt.Fprintf(a.Err, "host key scan failed for %s: %s\n", profile.Host, check.Message)
		return 1
	}
	sshArgs, err := SSHArgs(profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	pid, err := a.Runner.StartBackground(ctx, sshArgs)
	if err != nil {
		fmt.Fprintf(a.Err, "start ssh tunnel failed: %v\n", err)
		return 1
	}
	if err := a.StateManager.Save(NewState(profile, pid)); err != nil {
		fmt.Fprintf(a.Err, "save state: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "started %s with pid %d\n", profile.Name, pid)
	return 0
}
func (a App) runSSH(ctx context.Context, cfg Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "usage: cm ssh <profile>")
		return 2
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[0]))
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	errs := a.Validator.ValidateAccess(profile)
	if len(errs) > 0 {
		printErrors(a.Err, errs)
		return 1
	}
	sshArgs, err := InteractiveSSHArgs(profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "SSH: %s@%s\n", profile.User, profile.Host)
	if err := a.Runner.RunForeground(ctx, sshArgs); err != nil {
		fmt.Fprintf(a.Err, "ssh failed: %v\n", err)
		return 1
	}
	return 0
}
func (a App) runExec(ctx context.Context, cfg Config, args []string) int {
	if len(args) >= 2 && args[1] == "--" {
		args = append(args[:1], args[2:]...)
	}
	if len(args) < 2 {
		fmt.Fprintln(a.Err, "usage: cm exec <profile> -- <command>")
		return 2
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[0]))
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	errs := a.Validator.ValidateAccess(profile)
	if len(errs) > 0 {
		printErrors(a.Err, errs)
		return 1
	}
	command := args[1:]
	sshArgs, err := ExecSSHArgs(profile, command)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "Exec: %s@%s %s\n", profile.User, profile.Host, strings.Join(command, " "))
	if err := a.Runner.RunForeground(ctx, sshArgs); err != nil {
		fmt.Fprintf(a.Err, "ssh exec failed: %v\n", err)
		return 1
	}
	return 0
}
func (a App) runOpenVNC(ctx context.Context, cfg Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "usage: cm open-vnc <profile>")
		return 2
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[0]))
		return 2
	}
	target, err := VNCURL(profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "Opening %s\n", target)
	if err := a.Runner.OpenVNC(ctx, target); err != nil {
		fmt.Fprintf(a.Err, "open failed: %v\n", err)
		return 1
	}
	return 0
}
func (a App) runForgetHost(ctx context.Context, cfg Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "usage: cm forget-host <profile>")
		return 2
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[0]))
		return 2
	}
	if profile.Host == "" {
		fmt.Fprintln(a.Err, "host is required")
		return 1
	}
	fmt.Fprintf(a.Out, "Removing known_hosts entries for %s\n", profile.Host)
	if err := a.Runner.ForgetHost(ctx, profile.Host); err != nil {
		fmt.Fprintf(a.Err, "ssh-keygen failed: %v\n", err)
		return 1
	}
	return 0
}
func (a App) runHostKey(ctx context.Context, cfg Config, args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(a.Err, "usage: cm host-key <check|fix> <profile>")
		return 2
	}
	action := args[0]
	profile, ok := cfg.Profile(args[1])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[1]))
		return 2
	}
	switch action {
	case "check":
		check, err := a.checkHostKey(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "host key check failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Out, "Host key: %s (%s)\n", check.Status, check.Message)
		if check.Status == HostKeyScanFailed {
			return 1
		}
		return 0
	case "fix":
		check, err := a.fixHostKey(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "host key fix failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Out, "Host key: %s (%s)\n", check.Status, check.Message)
		if check.Status == HostKeyScanFailed {
			return 1
		}
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown host-key command %q\n", action)
		return 2
	}
}
func (a App) runStop(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "usage: cm stop <profile>")
		return 2
	}
	state, ok, err := a.StateManager.Load(args[0])
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(a.Err, "no running managed tunnel for %s\n", args[0])
		return 1
	}
	if err := a.Runner.Stop(state.PID); err != nil {
		fmt.Fprintf(a.Err, "stop pid %d: %v\n", state.PID, err)
		return 1
	}
	if err := a.StateManager.Remove(args[0]); err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "stopped %s\n", args[0])
	return 0
}
func (a App) runStatus() int {
	states, err := a.StateManager.List()
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if len(states) == 0 {
		fmt.Fprintln(a.Out, "no managed tunnels running")
		return 0
	}
	for _, state := range states {
		fmt.Fprintf(a.Out, "%s\tpid=%d\ttarget=%s", state.Profile, state.PID, state.Target)
		for _, tunnel := range state.Tunnels {
			fmt.Fprintf(a.Out, "\t%s", TunnelSummary(tunnel))
		}
		fmt.Fprintln(a.Out)
	}
	return 0
}
