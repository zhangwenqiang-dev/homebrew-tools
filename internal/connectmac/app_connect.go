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
	code := 0
	err := a.StateManager.WithProfileLock(stateKey, func() error {
		code = a.runStartLocked(ctx, profile, stateKey)
		return nil
	})
	if err != nil {
		fmt.Fprintf(a.Err, "lock start lifecycle: %v\n", err)
		return 1
	}
	return code
}

func (a App) runStartLocked(ctx context.Context, profile Profile, stateKey string) int {
	if err := a.StateManager.PreflightTunnelLifecycle(); err != nil {
		fmt.Fprintf(a.Err, "cannot manage SSH tunnel on this platform: %v\n", err)
		return 1
	}
	sshArgs, err := SSHArgs(profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if state, ok, err := a.StateManager.Load(stateKey); err != nil {
		fmt.Fprintf(a.Err, "load state: %v\n", err)
		return 1
	} else if ok {
		if a.StateManager.IsRunning != nil && !a.StateManager.IsRunning(state.PID) {
			if err := a.StateManager.Remove(stateKey); err != nil {
				fmt.Fprintf(a.Err, "remove stale tunnel state: %v\n", err)
				return 1
			}
		} else if state.SSHCommandFingerprint == "" || state.ProcessStartMarker == "" || state.IdentityFile == "" {
			if !state.matchesLegacyProfile(profile) {
				fmt.Fprintf(a.Err, "refusing to replace legacy live tunnel pid %d: %v\n", state.PID, legacyStateError("complete process identity"))
				return 1
			}
			identity, err := a.StateManager.InspectExpectedProcess(state.PID, sshArgs)
			if err != nil {
				fmt.Fprintf(a.Err, "refusing to adopt legacy tunnel pid %d: %v; it cannot be safely killed\n", state.PID, err)
				return 1
			}
			adopted := NewState(profile, state.PID, identity)
			adopted.StartedAt = state.StartedAt
			if err := a.StateManager.Save(adopted); err != nil {
				fmt.Fprintf(a.Err, "save adopted legacy tunnel state: %v\n", err)
				return 1
			}
			fmt.Fprintf(a.Out, "already started %s with pid %d (adopted legacy state)\n", stateKey, state.PID)
			return 0
		} else if state.Matches(profile) {
			if err := a.StateManager.VerifyExpectedManagedProcess(state, sshArgs); err != nil {
				fmt.Fprintf(a.Err, "refusing to reuse tunnel pid %d: %v\n", state.PID, err)
				return 1
			}
			fmt.Fprintf(a.Out, "already started %s with pid %d\n", stateKey, state.PID)
			return 0
		} else if errs := a.Validator.ValidateProfileSyntax(profile); len(errs) > 0 {
			printErrors(a.Err, errs)
			return 1
		} else if errs := a.Validator.ValidateAccess(profile); len(errs) > 0 {
			printErrors(a.Err, errs)
			return 1
		} else if errs := a.Validator.ValidateNewLocalPorts(profile, state); len(errs) > 0 {
			printErrors(a.Err, errs)
			return 1
		} else if err := a.StateManager.TerminateManagedProcess(state, a.Runner.Stop); err != nil {
			fmt.Fprintf(a.Err, "stop mismatched managed tunnel pid %d: %v\n", state.PID, err)
			return 1
		} else if err := a.StateManager.Remove(stateKey); err != nil {
			fmt.Fprintf(a.Err, "remove mismatched managed tunnel state: %v\n", err)
			return 1
		}
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
	pid, err := a.Runner.StartBackground(ctx, sshArgs)
	if err != nil {
		fmt.Fprintf(a.Err, "start ssh tunnel failed: %v\n", err)
		return 1
	}
	identity, err := a.StateManager.InspectExpectedProcess(pid, sshArgs)
	if err != nil {
		fmt.Fprintf(a.Err, "inspect started tunnel pid %d: %v; cannot safely terminate the unverified process, manually verify and terminate it\n", pid, err)
		return 1
	}
	state := NewState(profile, pid, identity)
	if err := a.StateManager.Save(state); err != nil {
		cleanupErr := a.StateManager.TerminateManagedProcess(state, a.Runner.Stop)
		if cleanupErr != nil {
			fmt.Fprintf(a.Err, "save state for started tunnel pid %d: %v; cleanup failed: %v\n", pid, err, cleanupErr)
		} else {
			fmt.Fprintf(a.Err, "save state for started tunnel pid %d: %v; stopped unrecorded tunnel\n", pid, err)
		}
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
	code := 0
	err := a.StateManager.WithProfileLock(args[0], func() error {
		code = a.runStopLocked(args[0])
		return nil
	})
	if err != nil {
		fmt.Fprintf(a.Err, "lock stop lifecycle: %v\n", err)
		return 1
	}
	return code
}

func (a App) runStopLocked(profile string) int {
	state, ok, err := a.StateManager.Load(profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(a.Err, "no running managed tunnel for %s\n", profile)
		return 1
	}
	if a.StateManager.IsRunning != nil && !a.StateManager.IsRunning(state.PID) {
		if err := a.StateManager.Remove(profile); err != nil {
			fmt.Fprintf(a.Err, "remove stale tunnel state: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Err, "no running managed tunnel for %s (removed stale state)\n", profile)
		return 1
	}
	if err := a.StateManager.TerminateManagedProcess(state, a.Runner.Stop); err != nil {
		fmt.Fprintf(a.Err, "stop pid %d: %v\n", state.PID, err)
		return 1
	}
	if err := a.StateManager.Remove(profile); err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "stopped %s\n", profile)
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
