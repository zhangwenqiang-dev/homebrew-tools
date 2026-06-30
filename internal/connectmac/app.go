package connectmac

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	Version      string
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
		Version:      "dev",
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
	if args[0] == "version" || args[0] == "--version" || args[0] == "-v" {
		a.printVersion()
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
	case "version":
		a.printVersion()
		return 0
	case "completion":
		return a.runCompletion(configPath, args[1:])
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
		return a.runProfile(ctx, configPath, cfg, args[1:])
	case "doctor":
		return a.runDoctor(configPath, args[1:])
	case "dashboard":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runDashboard(ctx, cfg, args[1:])
	case "open":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runAWS(ctx, cfg, append([]string{"open"}, args[1:]...))
	case "close":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runAWS(ctx, cfg, append([]string{"destroy"}, args[1:]...))
	case "setup-vnc":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runSetupVNC(cfg, args[1:])
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
	case "exec":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		return a.runExec(ctx, cfg, args[1:])
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
		return a.runMCP(ctx, configPath, args[1:])
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

func (a App) runMCP(ctx context.Context, configPath string, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			a.printMCPUsage()
			return 0
		case "tools":
			return a.runMCPTools(args[1:])
		default:
			fmt.Fprintf(a.Err, "unknown mcp command %q\n\n", args[0])
			a.printMCPUsage()
			return 2
		}
	}
	server := MCPServer{App: a, ConfigPath: configPath}
	if err := server.Serve(ctx, os.Stdin, a.Out); err != nil {
		fmt.Fprintf(a.Err, "mcp failed: %v\n", err)
		return 1
	}
	return 0
}

func (a App) runMCPTools(args []string) int {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--help", "-h":
			a.printMCPUsage()
			return 0
		default:
			fmt.Fprintf(a.Err, "unknown mcp tools option %q\n", arg)
			return 2
		}
	}
	if jsonOutput {
		if err := WriteMCPToolsJSON(a.Out); err != nil {
			fmt.Fprintf(a.Err, "mcp tools failed: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprint(a.Out, FormatMCPToolsText())
	return 0
}

func (a App) runAWS(ctx context.Context, cfg Config, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm aws <plan|capacity|open|create|status|wait-ready|adopt|adopt-host|launch-on-host|destroy|destroy-many|destroy-all|running> [profile-or-apple-email] [--confirm] [--all] [--host-id <id>] [--except <profile-or-apple-email>]")
		return 2
	}
	command := args[0]
	if command == "running" {
		if len(args) != 1 {
			fmt.Fprintln(a.Err, "usage: cm aws running")
			return 2
		}
		return a.runAWSRunning(ctx, cfg)
	}
	if command == "destroy-many" {
		return a.runAWSDestroyMany(ctx, cfg, args[1:])
	}
	if command == "destroy-all" {
		return a.runAWSDestroyAll(ctx, cfg, args[1:])
	}
	if len(args) < 2 {
		fmt.Fprintln(a.Err, "usage: cm aws <plan|capacity|open|create|status|wait-ready|adopt|adopt-host|launch-on-host|destroy> <profile-or-apple-email> [--confirm] [--all] [--host-id <id>]")
		return 2
	}
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
	case "capacity":
		_, capacity, err := a.AWSService.Capacity(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws capacity failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCapacity(plan, capacity))
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
			fmt.Fprint(a.Err, awsStoppedMessage("aws create", err))
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCreateResult(plan, result))
		_, status, err := a.awsServiceWithProgress().WaitReady(ctx, profile)
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
		_, status, err := a.awsServiceWithProgress().WaitReady(ctx, profile)
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
			fmt.Fprint(a.Err, awsStoppedMessage("aws launch-on-host", err))
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
		return a.runAWSDestroy(ctx, profile, plan, confirm)
	default:
		fmt.Fprintf(a.Err, "unknown aws command %q\n", command)
		return 2
	}
}

func (a App) runAWSDestroy(ctx context.Context, profile Profile, plan MacPlan, confirm bool) int {
	_, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
	if err != nil {
		fmt.Fprintf(a.Err, "aws destroy preview failed: %v\n", err)
		return 1
	}
	fmt.Fprint(a.Out, FormatMacDestroyPreviewWithStatus(plan, status))
	if !confirm {
		fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to execute AWS destruction.")
		return 0
	}
	service := a.awsServiceWithProgress()
	_, result, err := service.Destroy(ctx, profile)
	if err != nil {
		var partial AWSDestroyPartialError
		if errors.As(err, &partial) {
			fmt.Fprint(a.Out, FormatAWSDestroyResult(plan, partial.Result))
			a.printAWSDestroyFinalStatus(ctx, profile)
			fmt.Fprintf(a.Err, "aws destroy partially completed: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Err, "aws destroy failed: %v\n", err)
		return 1
	}
	fmt.Fprint(a.Out, FormatAWSDestroyResult(plan, result))
	a.printAWSDestroyFinalStatus(ctx, profile)
	return 0
}

func (a App) printAWSDestroyFinalStatus(ctx context.Context, profile Profile) {
	plan, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
	if err != nil {
		fmt.Fprintf(a.Err, "aws destroy final status failed: %v\n", err)
		return
	}
	fmt.Fprint(a.Out, FormatAWSDestroyFinalStatus(plan, status))
}

func (a App) runAWSRunning(ctx context.Context, cfg Config) int {
	rows := [][]string{{"APPLE ACCOUNT", "PROFILE", "REGION", "AZ", "TYPE", "INSTANCE", "PUBLIC IP", "READY"}}
	var errs []string
	for _, name := range sortedProfileNames(cfg) {
		profile, _ := cfg.Profile(name)
		if profile.AWS.Profile == "" {
			continue
		}
		validationErrs := a.Validator.ValidateAWSProfile(profile)
		if len(validationErrs) > 0 {
			continue
		}
		plan, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", profile.Name, err))
			continue
		}
		for _, instance := range status.Instances {
			if instance.State != "running" {
				continue
			}
			rows = append(rows, []string{
				emptyTableValue(plan.AccountEmail),
				plan.ProfileName,
				plan.Region,
				emptyTableValue(zoneForInstance(status.Hosts, instance.HostID)),
				emptyTableValue(instance.InstanceType),
				emptyTableValue(instance.InstanceID),
				emptyTableValue(instance.PublicIP),
				fmt.Sprintf("%t", InstanceReady(instance, status.ElasticIP)),
			})
		}
	}
	if len(rows) == 1 {
		fmt.Fprintln(a.Out, "No running AWS Mac instances found.")
	} else {
		fmt.Fprint(a.Out, formatRows(rows))
	}
	if len(errs) > 0 {
		fmt.Fprintln(a.Err, "Errors:")
		for _, err := range errs {
			fmt.Fprintf(a.Err, "- %s\n", err)
		}
		return 1
	}
	return 0
}

func zoneForInstance(hosts []DedicatedHostStatus, hostID string) string {
	for _, host := range hosts {
		if host.HostID == hostID {
			return host.ZoneID
		}
	}
	return ""
}

func (a App) runAWSDestroyMany(ctx context.Context, cfg Config, args []string) int {
	refs, confirm, err := parseDestroyManyArgs(args)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	return a.runAWSDestroyProfiles(ctx, cfg, refs, confirm, "destroy-many")
}

func parseDestroyManyArgs(args []string) ([]string, bool, error) {
	confirm := false
	var refs []string
	for _, arg := range args {
		switch arg {
		case "--confirm":
			confirm = true
		default:
			if strings.HasPrefix(arg, "--") {
				return nil, false, fmt.Errorf("unknown destroy-many option %q", arg)
			}
			refs = append(refs, arg)
		}
	}
	if len(refs) == 0 {
		return nil, false, fmt.Errorf("usage: cm aws destroy-many <profile-or-apple-email>... [--confirm]")
	}
	return refs, confirm, nil
}

func (a App) runAWSDestroyAll(ctx context.Context, cfg Config, args []string) int {
	exceptRefs, confirm, err := parseDestroyAllArgs(args)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	except := map[string]bool{}
	for _, ref := range exceptRefs {
		profile, err := resolveProfileRef(cfg, ref)
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		except[profile.Name] = true
	}
	var refs []string
	for _, name := range sortedProfileNames(cfg) {
		if !except[name] {
			refs = append(refs, name)
		}
	}
	return a.runAWSDestroyProfiles(ctx, cfg, refs, confirm, "destroy-all")
}

func parseDestroyAllArgs(args []string) ([]string, bool, error) {
	confirm := false
	var exceptRefs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--confirm":
			confirm = true
		case "--except":
			i++
			if i >= len(args) || args[i] == "" {
				return nil, false, fmt.Errorf("--except requires a profile or Apple email")
			}
			exceptRefs = append(exceptRefs, args[i])
		default:
			return nil, false, fmt.Errorf("unknown destroy-all option %q", args[i])
		}
	}
	return exceptRefs, confirm, nil
}

func parseProfileAddArgs(args []string) (Profile, error) {
	var profile Profile
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--name":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--name requires a value")
			}
			profile.Name = args[i]
		case "--description":
			i++
			if i >= len(args) {
				return profile, fmt.Errorf("--description requires a value")
			}
			profile.Description = args[i]
		case "--user":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--user requires a value")
			}
			profile.User = args[i]
		case "--host":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--host requires a value")
			}
			profile.Host = args[i]
		case "--identity-file":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--identity-file requires a value")
			}
			profile.IdentityFile = NormalizeIdentityFileInput(args[i])
		case "--apple-email":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--apple-email requires a value")
			}
			profile.AWS.AccountEmail = args[i]
		case "--aws-profile":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--aws-profile requires a value")
			}
			profile.AWS.Profile = args[i]
		case "--region":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--region requires a value")
			}
			profile.AWS.Region = args[i]
		case "--creator":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--creator requires a value")
			}
			profile.AWS.Creator = args[i]
		case "--key-name":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--key-name requires a value")
			}
			profile.AWS.KeyName = args[i]
		case "--security-group-id":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--security-group-id requires a value")
			}
			profile.AWS.SecurityGroupID = args[i]
		case "--eip-allocation-id":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--eip-allocation-id requires a value")
			}
			profile.AWS.ElasticIPAllocationID = args[i]
		case "--eip":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--eip requires a value")
			}
			profile.AWS.ElasticIPPublicIP = args[i]
		case "--az":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--az requires a value")
			}
			profile.AWS.AvailabilityZoneIDs = append(profile.AWS.AvailabilityZoneIDs, args[i])
		case "--instance-type":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--instance-type requires a value")
			}
			profile.AWS.InstanceTypePriority = append(profile.AWS.InstanceTypePriority, args[i])
		case "--subnet-id":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--subnet-id requires a value")
			}
			profile.AWS.SubnetID = args[i]
		case "--subnet":
			i++
			if i >= len(args) || args[i] == "" {
				return profile, fmt.Errorf("--subnet requires <az-id>=<subnet-id>")
			}
			az, subnet, ok := strings.Cut(args[i], "=")
			if !ok || az == "" || subnet == "" {
				return profile, fmt.Errorf("--subnet requires <az-id>=<subnet-id>")
			}
			if profile.AWS.SubnetsByAZ == nil {
				profile.AWS.SubnetsByAZ = map[string]string{}
			}
			profile.AWS.SubnetsByAZ[az] = subnet
		default:
			return profile, fmt.Errorf("unknown profile add option %q", arg)
		}
	}
	if profile.Name == "" {
		return profile, fmt.Errorf("usage: cm profile add --name <profile> [--apple-email <email>] [--region <region>] ...")
	}
	return profile, nil
}

func (a App) runAWSDestroyProfiles(ctx context.Context, cfg Config, refs []string, confirm bool, command string) int {
	matched := 0
	for _, ref := range refs {
		profile, err := resolveProfileRef(cfg, ref)
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		if profile.Name != ref && strings.Contains(ref, "@") {
			fmt.Fprintf(a.Out, "Resolved Apple account %s -> profile %s\n", ref, profile.Name)
		}
		errs := a.Validator.ValidateAWSProfile(profile)
		if len(errs) > 0 {
			printErrors(a.Err, errs)
			return 1
		}
		plan, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
		if err != nil {
			fmt.Fprintf(a.Err, "aws %s preview failed for %s: %v\n", command, profile.Name, err)
			return 1
		}
		if len(status.Hosts) == 0 && len(status.Instances) == 0 {
			continue
		}
		matched++
		if !confirm {
			fmt.Fprint(a.Out, FormatMacDestroyPreviewWithStatus(plan, status))
			fmt.Fprintln(a.Out, "Preview only. Include --confirm to execute AWS destruction.")
			continue
		}
		code := a.runAWSDestroy(ctx, profile, plan, true)
		if code != 0 {
			return code
		}
	}
	if matched == 0 {
		fmt.Fprintf(a.Out, "No active AWS Mac resources matched %s.\n", command)
	}
	return 0
}

func (a App) runInit(configPath string, args []string) int {
	if len(args) == 1 && args[0] == "wizard" {
		return a.runInitWizard(configPath)
	}
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

func (a App) runInitWizard(configPath string) int {
	path, err := ExpandPath(configPath)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(a.Err, "config already exists: %s\n", path)
		return 1
	}
	user := a.promptDefault("Default SSH user", DefaultAWSUser)
	identity := NormalizeIdentityFileInput(a.promptLine("Default PEM path or name (for example example.pem): "))
	creator := a.promptLine("Default AWS creator name: ")
	config := strings.Replace(DefaultConfigTemplate(), "  user: ec2-user\n", "  user: "+quoteYAMLString(user)+"\n", 1)
	if identity != "" {
		config = strings.Replace(config, "  identity_file: ~/.ssh/example.pem\n", "  identity_file: "+quoteYAMLString(identity)+"\n", 1)
	}
	if creator != "" {
		config = strings.Replace(config, "    creator: \"Xiao Chen\"\n", "    creator: "+quoteYAMLString(creator)+"\n", 1)
	}
	if err := os.MkdirAll(filepathDir(path), 0o700); err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		fmt.Fprintf(a.Err, "write config: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "created config: %s\n", path)
	if strings.EqualFold(a.promptLine("Initialize AI rules now? [y/N]: "), "y") {
		if code := a.runInitRules(nil); code != 0 {
			return code
		}
	}
	fmt.Fprintln(a.Out, "Next: run cm profile add or edit ~/.connectmac/profiles/<name>.yaml")
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
		fmt.Fprintf(a.Out, "connectmac skill: %s\n", install.SkillPath)
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
	fmt.Fprintf(a.Out, "installed connectmac skill: %s\n", result.SkillPath)
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

func (a App) runDoctor(configPath string, args []string) int {
	fix := false
	for _, arg := range args {
		switch arg {
		case "--fix":
			fix = true
		default:
			fmt.Fprintf(a.Err, "unknown doctor option %q\n", arg)
			return 2
		}
	}
	configFile, err := ExpandPath(configPath)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	configDir := filepathDir(configFile)
	profilesDir := filepath.Join(configDir, "profiles")
	type check struct {
		Name   string
		OK     bool
		Detail string
	}
	var checks []check
	if _, err := os.Stat(configFile); err == nil {
		checks = append(checks, check{"config file", true, configFile})
	} else {
		checks = append(checks, check{"config file", false, configFile})
	}
	if info, err := os.Stat(profilesDir); err == nil && info.IsDir() {
		checks = append(checks, check{"profiles dir", true, profilesDir})
	} else {
		if fix {
			_ = os.MkdirAll(profilesDir, 0o700)
		}
		_, err := os.Stat(profilesDir)
		checks = append(checks, check{"profiles dir", err == nil, profilesDir})
	}
	if _, err := exec.LookPath("ssh"); err == nil {
		checks = append(checks, check{"ssh executable", true, "found"})
	} else {
		checks = append(checks, check{"ssh executable", false, err.Error()})
	}
	if _, err := exec.LookPath("rsync"); err == nil {
		checks = append(checks, check{"rsync executable", true, "found"})
	} else {
		checks = append(checks, check{"rsync executable", false, err.Error()})
	}
	if path := detectedCompletionScript(); path != "" {
		checks = append(checks, check{"zsh completion", true, path})
	} else {
		checks = append(checks, check{"zsh completion", true, "not detected; run cm completion zsh or enable Homebrew completions"})
	}
	cfg, err := LoadConfig(configPath)
	if err == nil {
		checks = append(checks, check{"config parse", true, fmt.Sprintf("%d profiles", len(cfg.Profiles))})
		seenEmails := map[string]string{}
		for _, name := range sortedProfileNames(cfg) {
			profile, _ := cfg.Profile(name)
			if profile.AWS.AccountEmail != "" {
				if previous := seenEmails[strings.ToLower(profile.AWS.AccountEmail)]; previous != "" {
					checks = append(checks, check{"duplicate Apple email", false, previous + " and " + profile.Name})
				}
				seenEmails[strings.ToLower(profile.AWS.AccountEmail)] = profile.Name
			}
			for _, validationErr := range a.Validator.ValidateAccess(profile) {
				checks = append(checks, check{"profile " + profile.Name, false, validationErr.Error()})
			}
		}
	} else {
		checks = append(checks, check{"config parse", false, err.Error()})
	}
	checks = append(checks, check{"mcp tools", len(mcpTools()) > 0, fmt.Sprintf("%d tools", len(mcpTools()))})
	rows := [][]string{{"CHECK", "STATUS", "DETAIL"}}
	ok := true
	for _, item := range checks {
		status := "ok"
		if !item.OK {
			status = "fail"
			ok = false
		}
		rows = append(rows, []string{item.Name, status, item.Detail})
	}
	fmt.Fprint(a.Out, formatRows(rows))
	if !ok {
		return 1
	}
	return 0
}

func detectedCompletionScript() string {
	for _, path := range []string{
		"/usr/local/share/zsh/site-functions/_cm",
		"/opt/homebrew/share/zsh/site-functions/_cm",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (a App) runDashboard(ctx context.Context, cfg Config, args []string) int {
	includeAWS := false
	for _, arg := range args {
		switch arg {
		case "--aws":
			includeAWS = true
		default:
			fmt.Fprintf(a.Err, "unknown dashboard option %q\n", arg)
			return 2
		}
	}
	states, _ := a.StateManager.List()
	running := map[string]string{}
	for _, state := range states {
		running[state.Profile] = fmt.Sprintf("pid=%d", state.PID)
	}
	rows := [][]string{{"PROFILE", "APPLE ACCOUNT", "REGION", "HOST", "TUNNEL", "AWS"}}
	if includeAWS {
		rows[0] = append(rows[0], "INSTANCE", "READY", "DECISION", "NEXT", "EIP")
	}
	for _, name := range sortedProfileNames(cfg) {
		profile, _ := cfg.Profile(name)
		row := []string{
			profile.Name,
			emptyTableValue(profile.AWS.AccountEmail),
			emptyTableValue(profile.AWS.Region),
			emptyTableValue(profile.Host),
			emptyTableValue(running[profile.Name]),
			dashboardAWSConfigStatus(a.Validator.ValidateAWSProfile(profile)),
		}
		if includeAWS {
			instance, ready, decision, next, eip := "-", "-", "-", "-", "-"
			if len(a.Validator.ValidateAWSProfile(profile)) > 0 {
				ready = "config"
				decision = "config"
				next = "fix config"
			} else {
				_, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
				if err != nil {
					ready = "error"
					decision = "error"
					next = "cm aws status " + profile.Name
				} else {
					instance = dashboardInstanceSummary(status)
					ready = fmt.Sprintf("%t", AWSStatusReady(status))
					action := AWSOpenAction(status)
					decision = action.Kind
					next = AWSOpenDecisionNextStep(profile.Name, action)
					eip = emptyTableValue(status.ElasticIP.PublicIP)
				}
			}
			row = append(row, instance, ready, decision, next, eip)
		}
		rows = append(rows, row)
	}
	fmt.Fprint(a.Out, formatRows(rows))
	return 0
}

func dashboardAWSConfigStatus(errs []error) string {
	if len(errs) > 0 {
		return "config"
	}
	return "ok"
}

func dashboardInstanceSummary(status AWSStatus) string {
	if len(status.Instances) == 0 {
		return "-"
	}
	instance := status.Instances[0]
	return fmt.Sprintf("%s/%s", instance.InstanceID, emptyStatus(instance.State))
}

func (a App) runSetupVNC(cfg Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "usage: cm setup-vnc <profile>")
		return 2
	}
	profile, ok := cfg.Profile(args[0])
	if !ok {
		fmt.Fprintln(a.Err, unknownProfileError(cfg, args[0]))
		return 2
	}
	fmt.Fprintf(a.Out, "Manual GUI setup:\n")
	fmt.Fprintf(a.Out, "  cm ssh %s\n", profile.Name)
	fmt.Fprintf(a.Out, "  sudo passwd ec2-user\n")
	fmt.Fprintf(a.Out, "  # 输入你要设置的密码，例如：12345678\n")
	fmt.Fprintf(a.Out, "  # 再次输入你要设置的密码，例如：12345678\n")
	fmt.Fprintf(a.Out, "  sudo launchctl enable system/com.apple.screensharing\n")
	fmt.Fprintf(a.Out, "  sudo launchctl load -w /System/Library/LaunchDaemons/com.apple.screensharing.plist\n")
	fmt.Fprintf(a.Out, "  exit\n")
	fmt.Fprintf(a.Out, "  cm start %s\n", profile.Name)
	fmt.Fprintf(a.Out, "  cm open-vnc %s\n", profile.Name)
	return 0
}

func (a App) runCompletion(configPath string, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "usage: cm completion <zsh|bash|fish|commands|profiles|apple-emails|aws-commands|mcp-commands|profile-commands>")
		return 2
	}
	switch args[0] {
	case "zsh":
		fmt.Fprint(a.Out, zshCompletionScript())
		return 0
	case "bash":
		fmt.Fprint(a.Out, bashCompletionScript())
		return 0
	case "fish":
		fmt.Fprint(a.Out, fishCompletionScript())
		return 0
	case "commands":
		printLines(a.Out, completionCommands())
		return 0
	case "aws-commands":
		printLines(a.Out, completionAWSCommands())
		return 0
	case "mcp-commands":
		printLines(a.Out, completionMCPCommands())
		return 0
	case "profile-commands":
		printLines(a.Out, completionProfileCommands())
		return 0
	case "profiles":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		printLines(a.Out, sortedProfileNames(cfg))
		return 0
	case "apple-emails":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		printLines(a.Out, sortedAppleEmails(cfg))
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown completion target %q\n", args[0])
		return 2
	}
}

func (a App) runProfile(ctx context.Context, configPath string, cfg Config, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm profile <accounts|find|show|add|remove|rename|edit|export|import|import-dir> ...")
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
	case "show":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm profile show <profile-or-apple-email>")
			return 2
		}
		profile, err := resolveProfileRef(cfg, args[1])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		fmt.Fprint(a.Out, FormatProfileFile(profile))
		return 0
	case "add":
		return a.runProfileAdd(configPath, cfg, args[1:])
	case "remove":
		forceLocal := false
		values := make([]string, 0, len(args)-1)
		for _, arg := range args[1:] {
			switch arg {
			case "--force-local":
				forceLocal = true
			default:
				if strings.HasPrefix(arg, "--") {
					fmt.Fprintf(a.Err, "unknown profile remove option %q\n", arg)
					return 2
				}
				values = append(values, arg)
			}
		}
		if len(values) != 1 {
			fmt.Fprintln(a.Err, "usage: cm profile remove <profile> [--force-local]")
			return 2
		}
		profile, ok := cfg.Profile(values[0])
		if !ok {
			fmt.Fprintln(a.Err, unknownProfileError(cfg, values[0]))
			return 2
		}
		if !forceLocal {
			if blocked, detail := a.profileRemoveBlockedByAWS(ctx, profile); blocked {
				fmt.Fprint(a.Err, detail)
				return 1
			}
		}
		path, err := RemoveProfileFile(configPath, values[0])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		_ = a.StateManager.Remove(values[0])
		fmt.Fprintf(a.Out, "removed profile file: %s\n", path)
		return 0
	case "rename":
		if len(args) != 3 {
			fmt.Fprintln(a.Err, "usage: cm profile rename <old> <new>")
			return 2
		}
		if _, ok := cfg.Profile(args[1]); !ok {
			fmt.Fprintln(a.Err, unknownProfileError(cfg, args[1]))
			return 2
		}
		if _, ok := cfg.Profile(args[2]); ok {
			fmt.Fprintf(a.Err, "profile %q already exists\n", args[2])
			return 1
		}
		oldPath, newPath, err := RenameProfileFile(configPath, args[1], args[2])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		_ = a.StateManager.Remove(args[1])
		fmt.Fprintf(a.Out, "renamed profile file: %s -> %s\n", oldPath, newPath)
		return 0
	case "edit":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm profile edit <profile>")
			return 2
		}
		path, err := ProfileFilePath(configPath, args[1])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		if _, err := os.Stat(path); err != nil {
			fmt.Fprintf(a.Err, "profile %q is not managed as %s\n", args[1], path)
			return 1
		}
		if err := OpenProfileInEditor(path); err != nil {
			fmt.Fprintf(a.Out, "Profile file: %s\n", path)
			fmt.Fprintf(a.Err, "open editor failed: %v\n", err)
			return 1
		}
		return 0
	case "export":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm profile export <profile-or-apple-email>")
			return 2
		}
		profile, err := resolveProfileRef(cfg, args[1])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		fmt.Fprint(a.Out, FormatProfileFile(profile))
		return 0
	case "import":
		overwrite, values, err := parseOverwriteArgs(args[1:])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		if len(values) != 1 {
			fmt.Fprintln(a.Err, "usage: cm profile import <profile-file.yaml> [--overwrite]")
			return 2
		}
		paths, err := ImportProfileFileWithOptions(configPath, values[0], ProfileImportOptions{Overwrite: overwrite})
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		for _, path := range paths {
			fmt.Fprintf(a.Out, "imported profile: %s\n", path)
		}
		return 0
	case "import-dir":
		overwrite, values, err := parseOverwriteArgs(args[1:])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		if len(values) != 1 {
			fmt.Fprintln(a.Err, "usage: cm profile import-dir <profiles-dir> [--overwrite]")
			return 2
		}
		paths, err := ImportProfileDirWithOptions(configPath, values[0], ProfileImportOptions{Overwrite: overwrite})
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 1
		}
		for _, path := range paths {
			fmt.Fprintf(a.Out, "imported profile: %s\n", path)
		}
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown profile command %q\n", args[0])
		return 2
	}
}

func parseOverwriteArgs(args []string) (bool, []string, error) {
	overwrite := false
	values := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--overwrite":
			overwrite = true
		default:
			if strings.HasPrefix(arg, "--") {
				return false, nil, fmt.Errorf("unknown import option %q", arg)
			}
			values = append(values, arg)
		}
	}
	return overwrite, values, nil
}

func (a App) profileRemoveBlockedByAWS(ctx context.Context, profile Profile) (bool, string) {
	if emptyAWSConfig(profile.AWS) {
		return false, ""
	}
	if len(a.Validator.ValidateAWSProfile(profile)) > 0 {
		return true, fmt.Sprintf("profile %s has AWS settings but cannot be checked safely. Fix AWS config or use --force-local to remove only the local profile file.\n", profile.Name)
	}
	_, status, err := a.AWSService.StatusWithOptions(ctx, profile, AWSStatusOptions{IncludeTerminal: false})
	if err != nil {
		return true, fmt.Sprintf("profile %s AWS resources could not be checked: %v\nUse --force-local only if you are sure no AWS Mac resources need this profile.\n", profile.Name, err)
	}
	if len(status.Hosts) > 0 || len(status.Instances) > 0 {
		return true, fmt.Sprintf("profile %s still has AWS Mac resources: hosts=%d instances=%d.\nThis command only removes local config; it never releases Elastic IP allocations.\nRun cm close %s first to release managed EC2/Dedicated Host resources, then remove the profile.\nUse --force-local only when you intentionally want to remove the local profile without closing AWS resources.\n", profile.Name, len(status.Hosts), len(status.Instances), profile.Name)
	}
	return false, ""
}

func (a App) runProfileAdd(configPath string, cfg Config, args []string) int {
	if len(args) == 1 && args[0] == "--wizard" {
		return a.runProfileAddWizard(configPath, cfg)
	}
	profile, err := parseProfileAddArgs(args)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	if _, ok := cfg.Profile(profile.Name); ok {
		fmt.Fprintf(a.Err, "profile %q already exists\n", profile.Name)
		return 1
	}
	if profile.Description == "" && profile.AWS.AccountEmail != "" {
		profile.Description = "Apple account: " + profile.AWS.AccountEmail
	}
	if profile.AWS.ElasticIPOwnerTag.Key == "" && profile.AWS.AccountEmail != "" {
		profile.AWS.ElasticIPOwnerTag = AWSTagConfig{Key: "Apple", Value: profile.AWS.AccountEmail}
	}
	path, err := WriteProfileFile(configPath, profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "created profile: %s\n", path)
	return 0
}

func (a App) runProfileAddWizard(configPath string, cfg Config) int {
	var profile Profile
	profile.Name = a.promptLine("Profile name: ")
	if profile.Name == "" {
		fmt.Fprintln(a.Err, "profile name is required")
		return 2
	}
	if _, ok := cfg.Profile(profile.Name); ok {
		fmt.Fprintf(a.Err, "profile %q already exists\n", profile.Name)
		return 1
	}
	profile.AWS.AccountEmail = a.promptLine("Apple account email: ")
	if profile.AWS.AccountEmail == "" {
		fmt.Fprintln(a.Err, "apple account email is required")
		return 2
	}
	if _, err := cfg.ProfileByAppleEmail(profile.AWS.AccountEmail); err == nil {
		fmt.Fprintf(a.Err, "Apple account %s is already configured\n", profile.AWS.AccountEmail)
		return 1
	}
	profile.Description = a.promptDefault("Description", "Apple account: "+profile.AWS.AccountEmail)
	profile.User = a.promptDefault("SSH user", cfg.Defaults.User)
	profile.IdentityFile = NormalizeIdentityFileInput(a.promptDefault("PEM path or name", cfg.Defaults.IdentityFile))
	profile.AWS.Profile = a.promptLine("AWS profile: ")
	profile.AWS.Region = a.promptLine("AWS region: ")
	profile.AWS.Creator = a.promptDefault("AWS creator", cfg.Defaults.AWS.Creator)
	profile.AWS.KeyName = a.promptLine("AWS key pair name: ")
	profile.AWS.SecurityGroupID = a.promptLine("Security group ID: ")
	profile.AWS.ElasticIPAllocationID = a.promptLine("Elastic IP allocation ID: ")
	profile.AWS.ElasticIPPublicIP = a.promptLine("Elastic IP public IP: ")
	if profile.AWS.ElasticIPPublicIP != "" && profile.AWS.Region != "" {
		profile.Host = fmt.Sprintf("ec2-%s.%s.compute.amazonaws.com", strings.ReplaceAll(profile.AWS.ElasticIPPublicIP, ".", "-"), profile.AWS.Region)
	}
	for {
		az := a.promptLine("Availability zone ID (empty to finish): ")
		if az == "" {
			break
		}
		profile.AWS.AvailabilityZoneIDs = append(profile.AWS.AvailabilityZoneIDs, az)
		subnet := a.promptLine("Subnet ID for " + az + " (optional): ")
		if subnet != "" {
			if profile.AWS.SubnetsByAZ == nil {
				profile.AWS.SubnetsByAZ = map[string]string{}
			}
			profile.AWS.SubnetsByAZ[az] = subnet
		}
	}
	if profile.AWS.ElasticIPOwnerTag.Key == "" && profile.AWS.AccountEmail != "" {
		profile.AWS.ElasticIPOwnerTag = AWSTagConfig{Key: "Apple", Value: profile.AWS.AccountEmail}
	}
	if warnings := profileWizardWarnings(profile); len(warnings) > 0 {
		fmt.Fprintln(a.Out, "Warnings:")
		for _, warning := range warnings {
			fmt.Fprintf(a.Out, "- %s\n", warning)
		}
	}
	fmt.Fprintln(a.Out, "Profile preview:")
	fmt.Fprint(a.Out, FormatProfileFile(profile))
	if !strings.EqualFold(a.promptLine("Write this profile? [y/N]: "), "y") {
		fmt.Fprintln(a.Out, "cancelled")
		return 0
	}
	path, err := WriteProfileFile(configPath, profile)
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	fmt.Fprintf(a.Out, "created profile: %s\n", path)
	return 0
}

func profileWizardWarnings(profile Profile) []string {
	var warnings []string
	if profile.Host == "" && profile.AWS.ElasticIPPublicIP == "" {
		warnings = append(warnings, "host could not be derived because Elastic IP public IP is empty")
	}
	if profile.IdentityFile == "" {
		warnings = append(warnings, "identity_file is empty; SSH commands will ask for a PEM later")
	}
	identityKey := identityFileKeyName(profile.IdentityFile)
	if profile.AWS.KeyName != "" && identityKey != "" && profile.AWS.KeyName != identityKey {
		warnings = append(warnings, fmt.Sprintf("identity_file basename %q differs from AWS key_name %q", identityKey, profile.AWS.KeyName))
	}
	return warnings
}

func identityFileKeyName(identityFile string) string {
	if identityFile == "" {
		return ""
	}
	base := filepath.Base(identityFile)
	return strings.TrimSuffix(base, ".pem")
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
	action := AWSOpenAction(status)
	var candidates []AWSCreateAttempt
	if action.Kind == "create" {
		_, values, err := a.AWSService.CreateCandidates(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws open failed: %v\n", err)
			return 1
		}
		candidates = values
	}
	fmt.Fprint(a.Out, FormatAWSOpenPreviewWithCandidates(plan, status, candidates))
	if !confirm {
		fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to open or wait for this Mac.")
		return 0
	}
	switch action.Kind {
	case "ready":
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, status))
		return 0
	case "wait-ready":
		_, readyStatus, err := a.awsServiceWithProgress().WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, readyStatus))
		return 0
	case "launch-on-host":
		_, result, err := a.AWSService.LaunchOnHost(ctx, profile, action.HostID)
		if err != nil {
			fmt.Fprint(a.Err, awsStoppedMessage("aws launch-on-host", err))
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCreateResult(plan, result))
		_, readyStatus, err := a.awsServiceWithProgress().WaitReady(ctx, profile)
		if err != nil {
			fmt.Fprintf(a.Err, "aws wait-ready failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSReadyStatus(plan, readyStatus))
		return 0
	case "create":
		_, result, err := a.AWSService.Create(ctx, profile)
		if err != nil {
			fmt.Fprint(a.Err, awsStoppedMessage("aws create", err))
			return 1
		}
		fmt.Fprint(a.Out, FormatAWSCreateResult(plan, result))
		_, readyStatus, err := a.awsServiceWithProgress().WaitReady(ctx, profile)
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

func (a App) awsServiceWithProgress() AWSService {
	service := a.AWSService
	service.Progress = func(message string) {
		fmt.Fprintln(a.Out, message)
	}
	return service
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

func awsStoppedMessage(operation string, err error) string {
	return fmt.Sprintf("%s failed: %v\nStopped. Report this reason to the user and wait for explicit instructions before continuing.\n", operation, err)
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
	if len(args) < 2 {
		fmt.Fprintln(a.Err, "usage: cm pull <profile-or-apple-email> <remote-path> [--include <pattern>] [--exclude <pattern>]")
		return 2
	}
	extraFilters, err := parseSyncFilterFlags(args[2:])
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	profile, err := resolveProfileRef(cfg, args[0])
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	profile = a.promptMissingIdentityFile(profile)
	if !a.validateRsyncAccess(profile) {
		return 1
	}
	rsyncArgs, err := RsyncPullArgs(profile, args[1], ".", mergeSyncFilters(profile.Sync.Pull, extraFilters))
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
	if len(args) < 3 {
		fmt.Fprintln(a.Err, "usage: cm push <profile-or-apple-email> <local-path> <remote-dir> [--include <pattern>] [--exclude <pattern>]")
		return 2
	}
	extraFilters, err := parseSyncFilterFlags(args[3:])
	if err != nil {
		fmt.Fprintln(a.Err, err)
		return 2
	}
	profile, err := resolveProfileRef(cfg, args[0])
	if err != nil {
		fmt.Fprintln(a.Err, err)
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
	rsyncArgs, err := RsyncPushArgs(profile, localPath, remoteDir, mergeSyncFilters(profile.Sync.Push, extraFilters))
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
	line, err := readInputLine(a.In)
	if err != nil && len(line) == 0 {
		return ""
	}
	return strings.TrimSpace(line)
}

func (a App) promptDefault(label, fallback string) string {
	value := a.promptLine(fmt.Sprintf("%s [%s]: ", label, fallback))
	if value == "" {
		return fallback
	}
	return value
}

func readInputLine(r io.Reader) (string, error) {
	if byteReader, ok := r.(interface {
		ReadByte() (byte, error)
	}); ok {
		var b strings.Builder
		for {
			ch, err := byteReader.ReadByte()
			if err != nil {
				return b.String(), err
			}
			b.WriteByte(ch)
			if ch == '\n' {
				return b.String(), nil
			}
		}
	}
	reader := bufio.NewReader(r)
	return reader.ReadString('\n')
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
  cm version
  cm init [--config <path>]
  cm init wizard [--config <path>]
  cm init-rules [--agent <codex|claude|trae|cursor>] [--project <path>] [--skills-dir <path>] [--dry-run]
  cm init-rules --print-rules
  cm completion <zsh|bash|fish>
  cm list [--config <path>]
  cm check <profile> [--config <path>]
  cm connect <profile> [--config <path>]
  cm open <profile-or-apple-email> [--confirm] [--config <path>]
  cm close <profile-or-apple-email> [--confirm] [--config <path>]
  cm ssh <profile> [--config <path>]
  cm exec <profile> [--config <path>] -- <command>
  cm start <profile> [--config <path>]
  cm pull <profile-or-apple-email> <remote-path> [--include <pattern>] [--exclude <pattern>] [--config <path>]
  cm push <profile-or-apple-email> <local-path> <remote-dir> [--include <pattern>] [--exclude <pattern>] [--config <path>]
  cm forget-host <profile> [--config <path>]
  cm open-vnc <profile> [--config <path>]
  cm setup-vnc <profile> [--config <path>]
  cm profile accounts [--config <path>]
  cm profile find <apple-email> [--config <path>]
  cm profile show <profile-or-apple-email> [--config <path>]
  cm profile add --wizard [--config <path>]
  cm profile add --name <profile> [options] [--config <path>]
  cm profile remove <profile> [--force-local] [--config <path>]
  cm profile rename <old> <new> [--config <path>]
  cm profile edit <profile> [--config <path>]
  cm profile export <profile-or-apple-email> [--config <path>]
  cm profile import <profile-file.yaml> [--overwrite] [--config <path>]
  cm profile import-dir <profiles-dir> [--overwrite] [--config <path>]
  cm aws plan <profile-or-apple-email> [--config <path>]
  cm aws capacity <profile-or-apple-email> [--config <path>]
  cm aws open <profile-or-apple-email> [--confirm] [--config <path>]
  cm aws create <profile-or-apple-email> [--confirm] [--config <path>]
  cm aws status <profile-or-apple-email> [--config <path>]
  cm aws wait-ready <profile-or-apple-email> [--config <path>]
  cm aws adopt <profile-or-apple-email> [--confirm] [--config <path>]
  cm aws adopt-host <profile-or-apple-email> --host-id <id> [--confirm] [--config <path>]
  cm aws launch-on-host <profile-or-apple-email> --host-id <id> [--confirm] [--config <path>]
  cm aws destroy <profile-or-apple-email> [--confirm] [--config <path>]
  cm aws destroy-many <profile-or-apple-email>... [--confirm] [--config <path>]
  cm aws destroy-all [--except <profile-or-apple-email>] [--confirm] [--config <path>]
  cm aws running [--config <path>]
  cm mcp [--config <path>]
  cm mcp tools [--json]
  cm doctor [--fix] [--config <path>]
  cm dashboard [--aws] [--config <path>]
  cm stop <profile>
  cm status
`)
}

func (a App) printMCPUsage() {
	fmt.Fprint(a.Out, `Usage:
  cm mcp [--config <path>]
  cm mcp tools
  cm mcp tools --json

cm mcp starts the stdio MCP server. It waits for JSON-RPC messages on stdin
and does not print a tool list when run directly.

Use cm mcp tools for a human-readable tool list, or cm mcp tools --json for
the MCP tools/list result JSON.
`)
}

func (a App) printVersion() {
	version := a.Version
	if version == "" {
		version = "dev"
	}
	fmt.Fprintf(a.Out, "cm %s\n", version)
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
        includes: []
        excludes:
          - xcuserdata
          - .svn
          - .git
          - .DS_Store
      pull:
        includes: []
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
		if args[i] == "--" {
			out = append(out, args[i:]...)
			break
		}
		if args[i] == "--config" && i+1 < len(args) {
			*configPath = args[i+1]
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func parseSyncFilterFlags(args []string) (SyncFilters, error) {
	var filters SyncFilters
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--include":
			i++
			if i >= len(args) || args[i] == "" {
				return filters, fmt.Errorf("--include requires a value")
			}
			filters.Includes = append(filters.Includes, args[i])
		case "--exclude":
			i++
			if i >= len(args) || args[i] == "" {
				return filters, fmt.Errorf("--exclude requires a value")
			}
			filters.Excludes = append(filters.Excludes, args[i])
		default:
			return filters, fmt.Errorf("unknown sync option %q", args[i])
		}
	}
	return filters, nil
}

func mergeSyncFilters(direction SyncDirection, extra SyncFilters) SyncFilters {
	return SyncFilters{
		Includes: append(append([]string{}, direction.Includes...), extra.Includes...),
		Excludes: append(append([]string{}, direction.Excludes...), extra.Excludes...),
	}
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

func sortedAppleEmails(cfg Config) []string {
	seen := map[string]bool{}
	var emails []string
	for _, profile := range cfg.Profiles {
		for _, email := range profileAppleEmails(profile) {
			if !seen[email] {
				seen[email] = true
				emails = append(emails, email)
			}
		}
	}
	sort.Strings(emails)
	return emails
}

func printLines(out io.Writer, values []string) {
	for _, value := range values {
		fmt.Fprintln(out, value)
	}
}

func completionCommands() []string {
	return []string{
		"init",
		"init-rules",
		"version",
		"completion",
		"list",
		"profile",
		"check",
		"connect",
		"open",
		"close",
		"ssh",
		"exec",
		"start",
		"pull",
		"push",
		"forget-host",
		"open-vnc",
		"setup-vnc",
		"stop",
		"status",
		"doctor",
		"dashboard",
		"mcp",
		"aws",
	}
}

func completionProfileCommands() []string {
	return []string{"accounts", "find", "show", "add", "remove", "rename", "edit", "export", "import", "import-dir"}
}

func completionAWSCommands() []string {
	return []string{
		"plan",
		"capacity",
		"open",
		"create",
		"status",
		"wait-ready",
		"adopt",
		"adopt-host",
		"launch-on-host",
		"destroy",
		"destroy-many",
		"destroy-all",
		"running",
	}
}

func completionMCPCommands() []string {
	return []string{"tools"}
}

func zshCompletionScript() string {
	return `#compdef cm

_cm_config_args() {
  local -a args
  args=()
  local i
  for (( i = 1; i < ${#words}; i++ )); do
    if [[ "${words[$i]}" == "--config" && -n "${words[$((i + 1))]}" ]]; then
      args=(--config "${words[$((i + 1))]}")
      break
    fi
  done
  echo "${args[@]}"
}

_cm_profiles() {
  local -a config_args values
  config_args=(${(z)$(_cm_config_args)})
  values=("${(@f)$(command cm completion profiles "${config_args[@]}" 2>/dev/null)}")
  compadd -- "${values[@]}"
}

_cm_profile_or_apple() {
  local -a config_args values
  config_args=(${(z)$(_cm_config_args)})
  values=("${(@f)$(command cm completion profiles "${config_args[@]}" 2>/dev/null)}")
  values+=("${(@f)$(command cm completion apple-emails "${config_args[@]}" 2>/dev/null)}")
  compadd -- "${values[@]}"
}

_cm() {
  local -a commands aws_commands mcp_commands profile_commands
  commands=("${(@f)$(command cm completion commands 2>/dev/null)}")
  aws_commands=("${(@f)$(command cm completion aws-commands 2>/dev/null)}")
  mcp_commands=("${(@f)$(command cm completion mcp-commands 2>/dev/null)}")
  profile_commands=("${(@f)$(command cm completion profile-commands 2>/dev/null)}")

  if [[ "${words[$((CURRENT - 1))]}" == "--config" ]]; then
    _files
    return
  fi

  if (( CURRENT == 2 )); then
    compadd -- "${commands[@]}"
    return
  fi

  case "${words[2]}" in
    check|connect|ssh|start|forget-host|open-vnc|setup-vnc|stop)
      (( CURRENT == 3 )) && _cm_profiles
      ;;
    open|close)
      (( CURRENT == 3 )) && _cm_profile_or_apple
      ;;
    pull)
      if (( CURRENT == 3 )); then
        _cm_profile_or_apple
      elif [[ "${words[$((CURRENT - 1))]}" == "--include" || "${words[$((CURRENT - 1))]}" == "--exclude" ]]; then
        _files
      else
        _values 'pull option' --include --exclude --config
      fi
      ;;
    push)
      if (( CURRENT == 3 )); then
        _cm_profile_or_apple
      elif (( CURRENT == 4 )); then
        _files
      elif [[ "${words[$((CURRENT - 1))]}" == "--include" || "${words[$((CURRENT - 1))]}" == "--exclude" ]]; then
        _files
      else
        _values 'push option' --include --exclude --config
      fi
      ;;
    exec)
      (( CURRENT == 3 )) && _cm_profiles
      ;;
    profile)
      if (( CURRENT == 3 )); then
        compadd -- "${profile_commands[@]}"
      elif [[ "${words[3]}" == "find" || "${words[3]}" == "show" || "${words[3]}" == "export" ]]; then
        local -a config_args apple_emails
        config_args=(${(z)$(_cm_config_args)})
        apple_emails=("${(@f)$(command cm completion apple-emails "${config_args[@]}" 2>/dev/null)}")
        values=("${(@f)$(command cm completion profiles "${config_args[@]}" 2>/dev/null)}")
        values+=("${apple_emails[@]}")
        compadd -- "${values[@]}"
      elif [[ "${words[3]}" == "remove" || "${words[3]}" == "rename" || "${words[3]}" == "edit" ]]; then
        _cm_profiles
      elif [[ "${words[3]}" == "import" || "${words[3]}" == "import-dir" ]]; then
        _files
      fi
      ;;
    aws)
      if (( CURRENT == 3 )); then
        compadd -- "${aws_commands[@]}"
      elif (( CURRENT == 4 )); then
        _cm_profile_or_apple
      else
        case "${words[$((CURRENT - 1))]}" in
          --host-id) ;;
          --except) _cm_profile_or_apple ;;
          *) _values 'aws option' --confirm --all --host-id --except --config ;;
        esac
      fi
      ;;
    mcp)
      if (( CURRENT == 3 )); then
        compadd -- "${mcp_commands[@]}"
      elif [[ "${words[3]}" == "tools" ]]; then
        _values 'mcp tools option' --json --config
      fi
      ;;
    completion)
      _values 'shell' zsh bash fish
      ;;
  esac
}

_cm "$@"
`
}

func bashCompletionScript() string {
	return `_cm_completion()
{
  local cur prev cmd sub config_args
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  cmd="${COMP_WORDS[1]}"
  config_args=()
  local i
  for (( i=1; i<COMP_CWORD; i++ )); do
    if [[ "${COMP_WORDS[i]}" == "--config" && -n "${COMP_WORDS[i+1]}" ]]; then
      config_args=(--config "${COMP_WORDS[i+1]}")
      break
    fi
  done

  if [[ "$prev" == "--config" ]]; then
    COMPREPLY=( $(compgen -f -- "$cur") )
    return 0
  fi

  if [[ $COMP_CWORD -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "$(cm completion commands 2>/dev/null)" -- "$cur") )
    return 0
  fi

  case "$cmd" in
    check|connect|ssh|start|forget-host|open-vnc|setup-vnc|stop|exec)
      [[ $COMP_CWORD -eq 2 ]] && COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      ;;
    open|close)
      [[ $COMP_CWORD -eq 2 ]] && COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      ;;
    pull)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "$prev" == "--include" || "$prev" == "--exclude" ]]; then
        COMPREPLY=( $(compgen -f -- "$cur") )
      else
        COMPREPLY=( $(compgen -W "--include --exclude --config" -- "$cur") )
      fi
      ;;
    push)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "$prev" == "--include" || "$prev" == "--exclude" ]]; then
        COMPREPLY=( $(compgen -f -- "$cur") )
      else
        COMPREPLY=( $(compgen -W "--include --exclude --config" -- "$cur") )
      fi
      ;;
    profile)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profile-commands 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "find" || "${COMP_WORDS[2]}" == "show" || "${COMP_WORDS[2]}" == "export" ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "remove" || "${COMP_WORDS[2]}" == "rename" || "${COMP_WORDS[2]}" == "edit" ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "import" || "${COMP_WORDS[2]}" == "import-dir" ]]; then
        COMPREPLY=( $(compgen -f -- "$cur") )
      fi
      ;;
    aws)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion aws-commands 2>/dev/null)" -- "$cur") )
      elif [[ $COMP_CWORD -eq 3 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      else
        if [[ "$prev" == "--except" ]]; then
          COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
        else
          COMPREPLY=( $(compgen -W "--confirm --all --host-id --except --config" -- "$cur") )
        fi
      fi
      ;;
    mcp)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion mcp-commands 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "tools" ]]; then
        COMPREPLY=( $(compgen -W "--json --config" -- "$cur") )
      fi
      ;;
    completion)
      COMPREPLY=( $(compgen -W "zsh bash fish" -- "$cur") )
      ;;
  esac
  return 0
}
complete -F _cm_completion cm
`
}

func fishCompletionScript() string {
	return `complete -c cm -f
complete -c cm -n "not __fish_seen_subcommand_from (cm completion commands)" -a "(cm completion commands)"
complete -c cm -n "__fish_seen_subcommand_from check connect ssh start forget-host open-vnc setup-vnc stop exec" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from open close" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from open close" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from pull push" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from pull push" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from pull push" -a "--include --exclude"
complete -c cm -n "__fish_seen_subcommand_from profile; and not __fish_seen_subcommand_from (cm completion profile-commands)" -a "(cm completion profile-commands)"
complete -c cm -n "__fish_seen_subcommand_from profile; and __fish_seen_subcommand_from find" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from aws; and not __fish_seen_subcommand_from (cm completion aws-commands)" -a "(cm completion aws-commands)"
complete -c cm -n "__fish_seen_subcommand_from aws; and __fish_seen_subcommand_from (cm completion aws-commands)" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from aws; and __fish_seen_subcommand_from (cm completion aws-commands)" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from mcp; and not __fish_seen_subcommand_from (cm completion mcp-commands)" -a "(cm completion mcp-commands)"
complete -c cm -n "__fish_seen_subcommand_from mcp; and __fish_seen_subcommand_from tools" -a "--json"
complete -c cm -n "__fish_seen_subcommand_from completion" -a "zsh bash fish"
`
}

func filepathDir(path string) string {
	if idx := strings.LastIndex(path, string(os.PathSeparator)); idx >= 0 {
		return path[:idx]
	}
	return "."
}
