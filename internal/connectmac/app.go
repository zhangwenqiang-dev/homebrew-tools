package connectmac

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type Runner interface {
	RunForeground(ctx context.Context, args []string) error
	StartBackground(ctx context.Context, args []string) (int, error)
	Stop(pid int) error
	RunRsync(ctx context.Context, args []string) error
	ForgetHost(ctx context.Context, host string) error
	OpenURL(ctx context.Context, target string) error
}

type ExecRunner struct{}

type App struct {
	In           io.Reader
	Out          io.Writer
	Err          io.Writer
	Runner       Runner
	Validator    Validator
	StateManager StateManager
	AWSService   AWSService
}

func NewApp(out, err io.Writer) App {
	return App{
		In:           os.Stdin,
		Out:          out,
		Err:          err,
		Runner:       ExecRunner{},
		Validator:    NewValidator(),
		StateManager: NewStateManager(DefaultStateDir),
		AWSService:   NewAWSService(),
	}
}

func (a App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		a.printUsage()
		return 0
	}
	configPath := DefaultConfigPath
	args = parseConfigFlag(args, &configPath)
	if len(args) == 0 {
		a.printUsage()
		return 0
	}
	command := args[0]
	switch command {
	case "init":
		return a.runInit(configPath, args[1:])
	case "init-rules":
		return a.runInitRules(args[1:])
	case "list":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runList(cfg)
	case "profile":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runProfile(cfg, args[1:])
	case "check":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runCheck(cfg, args[1:])
	case "connect":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runConnect(ctx, cfg, args[1:])
	case "ssh":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runSSH(ctx, cfg, args[1:])
	case "start":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runStart(ctx, cfg, args[1:])
	case "pull":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runPull(ctx, cfg, args[1:])
	case "push":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runPush(ctx, cfg, args[1:])
	case "forget-host":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runForgetHost(ctx, cfg, args[1:])
	case "open-vnc":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runOpenVNC(ctx, cfg, args[1:])
	case "stop":
		return a.runStop(args[1:])
	case "status":
		return a.runStatus()
	case "mcp":
		return a.runMCP(ctx, configPath)
	case "aws":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runAWS(ctx, cfg, args[1:])
	default:
		fmt.Fprintf(a.Err, "unknown command %q\n\n", command)
		a.printUsage()
		return 2
	}
}

func (a App) runMCP(ctx context.Context, configPath string) int {
	server := MCPServer{App: a, ConfigPath: configPath}
	if err := server.Serve(ctx, os.Stdin, a.Out); err != nil {
		fmt.Fprintf(a.Err, "mcp failed: %v\n", err)
		return 1
	}
	return 0
}

func (a App) runAWS(ctx context.Context, cfg Config, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(a.Err, "usage: cm aws <plan|open|create|status|wait-ready|adopt|adopt-host|launch-on-host|destroy> <profile-or-apple-email> [--confirm] [--all] [--host-id <id>]")
		return 2
	}
	command := args[0]
	profileRef := args[1]
	confirm := false
	hostID := ""
	includeTerminal := false
	for i := 2; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--confirm":
			confirm = true
		case "--all":
			includeTerminal = true
		case "--host-id":
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(a.Err, "--host-id requires a value")
				return 2
			}
			hostID = args[i]
		default:
			fmt.Fprintf(a.Err, "unknown aws option %q\n", arg)
			return 2
		}
	}
	profile, err := resolveProfileRef(cfg, profileRef)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	if profile.Name != profileRef {
		fmt.Fprintf(a.Out, "Resolved Apple account %s -> profile %s\n", profileRef, profile.Name)
	}
	if (command == "open" || command == "create" || command == "adopt" || command == "adopt-host" || command == "launch-on-host") && confirm {
		var creatorOK bool
		profile, creatorOK = a.promptMissingAWSCreator(profile)
		if !creatorOK {
			fmt.Fprintln(a.Err, "aws.creator is required for confirmed AWS mutations; set aws.creator in config or enter it when prompted")
			return 1
		}
	}
	errs := a.Validator.ValidateAWSProfile(profile)
	if len(errs) > 0 {
		printErrors(a.Err, errs)
		return 1
	}
	plan, err := a.AWSService.Plan(profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	switch command {
	case "plan":
		fmt.Fprint(a.Out, FormatMacPlan(plan))
		return 0
	case "open":
		return a.runAWSOpen(ctx, profile, plan, confirm)
	case "create":
		fmt.Fprint(a.Out, FormatMacPlan(plan))
		if !confirm {
			fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to execute AWS creation.")
			return 0
		}
		_, result, err := a.AWSService.Create(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws create failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCreateResult(plan, result))
		_, status, err := a.AWSService.WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, status))
		return 0
	case "status":
		_, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: includeTerminal})
		if err != nil {
			fmt.Fprintf(a.Err, "aws status failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSStatus(plan, status))
		if !includeTerminal {
			fmt.Fprintln(a.Out, "Terminal resources are hidden. Use --all to include terminated instances and released hosts.")
		}
		return 0
	case "wait-ready":
		_, status, err := a.AWSService.WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, status))
		return 0
	case "adopt":
		_, status, err := a.AWSService.AdoptionPreview(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws adopt failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSAdoptionPreview(plan, status))
		if !confirm {
			fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to tag these resources as cm-managed.")
			return 0
		}
		_, result, err := a.AWSService.Adopt(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws adopt failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSAdoptResult(plan, result))
		return 0
	case "adopt-host":
		if hostID == "" {
			fmt.Fprintln(a.Err, "--host-id is required for aws adopt-host")
			return 2
		}
		_, host, err := a.AWSService.AdoptHostPreview(ctx, profile, hostID)
		if err != nil {
			fmt.Fprintf(a.Err, "aws adopt-host failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSAdoptHostPreview(plan, host))
		if !confirm {
			fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to tag this host as cm-managed.")
			return 0
		}
		_, result, err := a.AWSService.AdoptHost(ctx, profile, hostID)
		if err != nil {
			fmt.Fprintf(a.Err, "aws adopt-host failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSAdoptResult(plan, result))
		return 0
	case "launch-on-host":
		if hostID == "" {
			fmt.Fprintln(a.Err, "--host-id is required for aws launch-on-host")
			return 2
		}
		_, preview, err := a.AWSService.LaunchOnHostPreview(ctx, profile, hostID)
		if err != nil {
			fmt.Fprintf(a.Err, "aws launch-on-host failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSLaunchOnHostPreview(plan, preview))
		if !confirm {
			fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to launch EC2 on this host.")
			return 0
		}
		_, result, err := a.AWSService.LaunchOnHost(ctx, profile, hostID)
		if err != nil {
			fmt.Fprintf(a.Err, "aws launch-on-host failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCreateResult(plan, result))
		_, status, err := a.AWSService.WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, status))
		return 0
	case "destroy":
		fmt.Fprint(a.Out, FormatMacDestroyPreview(plan))
		if !confirm {
			fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to execute AWS destruction.")
			return 0
		}
		_, result, err := a.AWSService.Destroy(ctx, profile)
		if err != nil {
			var partial AWSDestroyPartialError
			if errors.As(err, &partial) {
				fmt.Fprint(a.Out, FormatAWSDestroyResult(plan, partial.Result))
				fmt.Fprintf(a.Err, "aws destroy partially completed: %v\n", err)
				return 1
			}
			fmt.Fprintf(a.Err, "aws destroy failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSDestroyResult(plan, result))
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown aws command %q\n", command)
		return 2
	}
}

func (a App) runInit(configPath string, args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(a.Err, "unknown init option %q\n", args[0])
		return 2
	}
	path, err := ExpandPath(configPath)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(a.Err, "config already exists: %s\n", path)
		return 1
	}
	if err := os.MkdirAll(filepathDir(path), 0o700); err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if err := os.WriteFile(path, []byte(DefaultConfigTemplate()), 0o600); err != nil {
		fmt.Fprintf(a.Err, "write config: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "created config: %s\n", path)
	if strings.EqualFold(a.promptLine("Initialize AI rules now? [y/N]: "), "y") {
		return a.runInitRules(nil)
	}
	return 0
}

func (a App) runInitRules(args []string) int {
	options, err := parseInitRulesOptions(args)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	if options.PrintRules {
		fmt.Fprint(a.Out, DefaultRulesTemplate())
		return 0
	}
	agent := options.Agent
	if agent == "" {
		agent = strings.ToLower(a.promptLine("Agent for AI rules (Codex/Claude/Trae/Cursor): "))
	}
	options.Agent = agent
	install, err := BuildRulesInstallWithOptions(options)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if options.DryRun {
		fmt.Fprintln(a.Out, "AI rules install dry run")
		fmt.Fprintf(a.Out, "Rules source: %s\n", install.SourcePath)
		fmt.Fprintf(a.Out, "%s rules: %s\n", install.Agent, install.AgentPath)
		fmt.Fprintf(a.Out, "connectmac-aws skill: %s\n", install.SkillPath)
		fmt.Fprintln(a.Out, "No files were written.")
		return 0
	}
	result, err := InstallRules(install)
	if err != nil {
		fmt.Fprintf(a.Err, "init rules failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "created rules source: %s\n", result.SourcePath)
	fmt.Fprintf(a.Out, "installed %s rules: %s\n", result.Agent, result.AgentPath)
	fmt.Fprintf(a.Out, "installed connectmac-aws skill: %s\n", result.SkillPath)
	if result.Validated {
		fmt.Fprintln(a.Out, "validation passed")
	}
	fmt.Fprintln(a.Out, "Rule content source: ~/.connectmac/rules.md")
	fmt.Fprintln(a.Out, "Ask your AI agent to remember the rule content exactly and keep it as long-term memory.")
	return 0
}

func (a App) runList(cfg Config) int {
	fmt.Fprint(a.Out, listProfilesText(cfg))
	return 0
}

func (a App) runProfile(cfg Config, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm profile <find|accounts> [apple-email]")
		return 2
	}
	switch args[0] {
	case "accounts":
		fmt.Fprint(a.Out, FormatAppleAccountChoices(cfg))
		return 0
	case "find":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm profile find <apple-email>")
			return 2
		}
		profile, err := cfg.ProfileByAppleEmail(args[1])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		fmt.Fprintf(a.Out, "Apple account: %s\nProfile: %s\nDescription: %s\n", args[1], profile.Name, profile.Description)
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown profile command %q\n", args[0])
		return 2
	}
}

func (a App) runCheck(cfg Config, args []string) int {
	profile, ok := requireProfileArg(a.Err, cfg, args)
	if !ok {
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	errs := a.Validator.ValidateProfile(profile)
	if len(errs) > 0 {
		printErrors(a.Err, errs)
		return 1
	}
	printSummary(a.Out, profile)
	fmt.Fprintln(a.Out, "check passed")
	return 0
}

func (a App) runAWSOpen(ctx context.Context, profile Profile, plan MacPlan, confirm bool) int {
	_, status, err := a.AWSService.Status(ctx, profile)
	if err != nil {
		fmt.Fprintf(a.Err, "aws open failed: %v\n", err)
		return 1
	}
	fmt.Fprint(a.Out, FormatAWSOpenPreview(plan, status))
	action := AWSOpenAction(status)
	if !confirm {
		fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to open or wait for this Mac.")
		return 0
	}
	switch action.Kind {
	case "ready":
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, status))
		return 0
	case "wait-ready":
		_, readyStatus, err := a.AWSService.WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, readyStatus))
		return 0
	case "launch-on-host":
		_, result, err := a.AWSService.LaunchOnHost(ctx, profile, action.HostID)
		if err != nil {
			fmt.Fprintf(a.Err, "aws launch-on-host failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCreateResult(plan, result))
		_, readyStatus, err := a.AWSService.WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, readyStatus))
		return 0
	case "create":
		_, result, err := a.AWSService.Create(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws create failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCreateResult(plan, result))
		_, readyStatus, err := a.AWSService.WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, readyStatus))
		return 0
	default:
		fmt.Fprintf(a.Err, "aws open cannot continue automatically: %s\n", action.Detail)
		return 1
	}
}

func resolveProfileRef(cfg Config, ref string) (Profile, error) {
	if profile, ok := cfg.Profile(ref); ok {
		return profile, nil
	}
	if strings.Contains(ref, "@") {
		return cfg.ProfileByAppleEmail(ref)
	}
	return Profile{}, unknownProfileError(cfg, ref)
}

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
	pid, err := a.Runner.StartBackground(ctx, sshArgs)
	if err != nil {
		fmt.Fprintf(a.Err, "start ssh: %v\n", err)
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
	if err := a.Runner.OpenURL(ctx, target); err != nil {
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

func (a App) runPull(ctx context.Context, cfg Config, args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(a.Err, "usage: cm pull <profile> <remote-path>")
		return 2
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[0]))
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	if !a.validateRsyncAccess(profile) {
		return 1
	}
	rsyncArgs, err := RsyncPullArgs(profile, args[1], ".", profile.Sync.Pull.Excludes)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "Pull: %s -> .\n", RemoteTarget(profile, args[1]))
	if err := a.Runner.RunRsync(ctx, rsyncArgs); err != nil {
		fmt.Fprintf(a.Err, "rsync failed: %v\n", err)
		return 1
	}
	return 0
}

func (a App) runPush(ctx context.Context, cfg Config, args []string) int {
	if len(args) != 3 {
		fmt.Fprintln(a.Err, "usage: cm push <profile> <local-path> <remote-dir>")
		return 2
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[0]))
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	if !a.validateRsyncAccess(profile) {
		return 1
	}
	localPath := args[1]
	if _, err := os.Stat(localPath); err != nil {
		fmt.Fprintf(a.Err, "read local path %s: %v\n", localPath, err)
		return 1
	}
	remoteDir := NormalizeRemotePath(args[2])
	rsyncArgs, err := RsyncPushArgs(profile, localPath, remoteDir, profile.Sync.Push.Excludes)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "Push: %s -> %s\n", localPath, RemoteTarget(profile, remoteDir))
	if err := a.Runner.RunRsync(ctx, rsyncArgs); err != nil {
		fmt.Fprintf(a.Err, "rsync failed: %v\n", err)
		return 1
	}
	return 0
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

func (a App) validateAndSummarize(profile Profile) bool {
	errs := a.Validator.ValidateProfile(profile)
	if len(errs) > 0 {
		printErrors(a.Err, errs)
		return false
	}
	printSummary(a.Out, profile)
	return true
}

func (a App) validateRsyncAccess(profile Profile) bool {
	errs := a.Validator.ValidateAccess(profile)
	if a.Validator.CheckRsync != nil {
		if err := a.Validator.CheckRsync(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		printErrors(a.Err, errs)
		return false
	}
	return true
}

func (a App) promptMissingIdentityFile(profile Profile) Profile {
	if profile.IdentityFile != "" {
		return profile
	}
	value := a.promptLine(fmt.Sprintf("identity_file for %s (PEM name or path): ", profile.Name))
	if value == "" {
		return profile
	}
	profile.IdentityFile = NormalizeIdentityFileInput(value)
	return profile
}

func (a App) promptMissingAWSCreator(profile Profile) (Profile, bool) {
	if profile.AWS.Creator != "" {
		return profile, true
	}
	value := a.promptLine(fmt.Sprintf("aws.creator for %s: ", profile.Name))
	if value == "" {
		return profile, false
	}
	profile.AWS.Creator = value
	return profile, true
}

func (a App) promptLine(prompt string) string {
	if a.In == nil {
		return ""
	}
	fmt.Fprint(a.Err, prompt)
	reader := bufio.NewReader(a.In)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	return strings.TrimSpace(line)
}

func (a App) loadConfig(path string) (Config, int) {
	cfg, err := LoadConfig(path)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return Config{}, 1
	}
	return cfg, 0
}

func (a App) printUsage() {
	fmt.Fprint(a.Out, `Usage:
  cm init [--config <path>]
  cm init-rules [--agent <codex|claude|trae|cursor>] [--project <path>] [--skills-dir <path>] [--dry-run]
  cm init-rules --print-rules
  cm list [--config <path>]
  cm check <profile> [--config <path>]
  cm connect <profile> [--config <path>]
  cm ssh <profile> [--config <path>]
  cm start <profile> [--config <path>]
  cm pull <profile> <remote-path> [--config <path>]
  cm push <profile> <local-path> <remote-dir> [--config <path>]
  cm forget-host <profile> [--config <path>]
  cm open-vnc <profile> [--config <path>]
  cm profile accounts [--config <path>]
  cm profile find <apple-email> [--config <path>]
  cm aws plan <profile-or-apple-email> [--config <path>]
  cm aws open <profile-or-apple-email> [--confirm] [--config <path>]
  cm aws create <profile-or-apple-email> [--confirm] [--config <path>]
  cm aws status <profile-or-apple-email> [--config <path>]
  cm aws wait-ready <profile-or-apple-email> [--config <path>]
  cm aws adopt <profile-or-apple-email> [--confirm] [--config <path>]
  cm aws adopt-host <profile-or-apple-email> --host-id <id> [--confirm] [--config <path>]
  cm aws launch-on-host <profile-or-apple-email> --host-id <id> [--confirm] [--config <path>]
  cm aws destroy <profile-or-apple-email> [--confirm] [--config <path>]
  cm mcp [--config <path>]
  cm stop <profile>
  cm status
`)
}

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
	return pid, cmd.Process.Release()
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

func DefaultConfigTemplate() string {
	return `defaults:
  user: ec2-user
  identity_file: ~/.ssh/example.pem
  aws:
    creator: "Xiao Chen"
    amis_by_region:
      us-east-1:
        mac_x86: "<us-east-1-x86-mac-ami>"
        mac_arm: "<us-east-1-arm-mac-ami>"
      us-east-2:
        mac_x86: "<us-east-2-x86-mac-ami>"
        mac_arm: "<us-east-2-arm-mac-ami>"
      us-west-2:
        mac_x86: ami-0538568e5d3653bea
        mac_arm: ami-063755aadeb97329a

profiles:
  xcode-vnc:
    description: Apple account: user@example.com
    host: mac-host.example.com
    sync:
      push:
        excludes:
          - xcuserdata
          - .svn
          - .git
          - .DS_Store
      pull:
        excludes: []
    vnc:
      username: mac-user
    aws:
      profile: cm-xcode
      region: us-west-2
      resource_name: ""
      account_email: user@example.com
      key_name: example-key
      subnet_id: "<subnet-id>"
      subnets_by_az:
        usw2-az1: "<subnet-id-az1>"
        usw2-az2: "<subnet-id-az2>"
        usw2-az3: "<subnet-id-az3>"
        usw2-az4: "<subnet-id-az4>"
      security_group_id: "<security-group-id>"
      elastic_ip_allocation_id: "<elastic-ip-allocation-id>"
      elastic_ip_public_ip: "<elastic-ip-public-ip>"
      elastic_ip_owner_tag:
        key: Apple
        value: user@example.com
      availability_zone_ids:
        - usw2-az1
        - usw2-az2
        - usw2-az3
        - usw2-az4
      instance_type_priority:
        - mac2.metal
        - mac2-m2.metal
        - mac-m4.metal
        - mac2-m2pro.metal
        - mac-m4pro.metal
        - mac2-m1ultra.metal
        - mac-m4max.metal
        - mac-m3ultra.metal
      allow_intel_fallback: false
    tunnels:
      - local_port: 5900
        remote_host: localhost
        remote_port: 5900
`
}

func parseConfigFlag(args []string, configPath *string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			*configPath = args[i+1]
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func requireProfileArg(errOut io.Writer, cfg Config, args []string) (Profile, bool) {
	if len(args) != 1 {
		fmt.Fprintln(errOut, "profile name is required")
		return Profile{}, false
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(errOut, unknownProfileError(cfg, args[0]))
		return Profile{}, false
	}
	return profile, true
}

func printSummary(out io.Writer, profile Profile) {
	fmt.Fprintf(out, "Profile: %s\n", profile.Name)
	if profile.Description != "" {
		fmt.Fprintf(out, "Description: %s\n", profile.Description)
	}
	fmt.Fprintf(out, "SSH Target: %s@%s\n", profile.User, profile.Host)
	fmt.Fprintf(out, "Identity: %s\n", profile.IdentityFile)
	for _, tunnel := range profile.Tunnels {
		fmt.Fprintf(out, "Tunnel: %s\n", TunnelSummary(tunnel))
	}
}

func printErrors(out io.Writer, errs []error) {
	for _, err := range errs {
		fmt.Fprintf(out, "error: %v\n", err)
	}
}

func sortedProfileNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func filepathDir(path string) string {
	if idx := strings.LastIndex(path, string(os.PathSeparator)); idx >= 0 {
		return path[:idx]
	}
	return "."
}
