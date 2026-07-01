package connectmac

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	var checks []doctorCheck
	if _, err := os.Stat(configFile); err == nil {
		checks = append(checks, doctorCheck{"config file", true, configFile})
	} else {
		checks = append(checks, doctorCheck{"config file", false, configFile})
	}
	if info, err := os.Stat(profilesDir); err == nil && info.IsDir() {
		checks = append(checks, doctorCheck{"profiles dir", true, profilesDir})
	} else {
		if fix {
			_ = os.MkdirAll(profilesDir, 0o700)
		}
		_, err := os.Stat(profilesDir)
		checks = append(checks, doctorCheck{"profiles dir", err == nil, profilesDir})
	}
	if _, err := exec.LookPath("ssh"); err == nil {
		checks = append(checks, doctorCheck{"ssh executable", true, "found"})
	} else {
		checks = append(checks, doctorCheck{"ssh executable", false, err.Error()})
	}
	if _, err := exec.LookPath("rsync"); err == nil {
		checks = append(checks, doctorCheck{"rsync executable", true, "found"})
	} else {
		checks = append(checks, doctorCheck{"rsync executable", false, err.Error()})
	}
	if path := detectedCompletionScript(); path != "" {
		checks = append(checks, doctorCheck{"zsh completion", true, path})
	} else {
		checks = append(checks, doctorCheck{"zsh completion", true, "not detected; run cm completion zsh or enable Homebrew completions"})
	}
	cfg, err := LoadConfig(configPath)
	if err == nil {
		checks = append(checks, doctorCheck{"config parse", true, fmt.Sprintf("%d profiles", len(cfg.Profiles))})
		seenEmails := map[string]string{}
		for _, name := range sortedProfileNames(cfg) {
			profile, _ := cfg.Profile(name)
			if profile.AWS.AccountEmail != "" {
				if previous := seenEmails[strings.ToLower(profile.AWS.AccountEmail)]; previous != "" {
					checks = append(checks, doctorCheck{"duplicate Apple email", false, previous + " and " + profile.Name})
				}
				seenEmails[strings.ToLower(profile.AWS.AccountEmail)] = profile.Name
			}
			for _, validationErr := range a.Validator.ValidateAccess(profile) {
				checks = append(checks, doctorCheck{"profile " + profile.Name, false, validationErr.Error()})
			}
		}
	} else {
		checks = append(checks, doctorCheck{"config parse", false, err.Error()})
	}
	checks = append(checks, doctorCheck{"mcp tools", len(mcpTools()) > 0, fmt.Sprintf("%d tools", len(mcpTools()))})
	rows := [][]string{{"CHECK", "STATUS", "DETAIL", "NEXT"}}
	ok := true
	for _, item := range checks {
		status := "ok"
		if !item.OK {
			status = "fail"
			ok = false
		}
		rows = append(rows, []string{item.Name, status, item.Detail, doctorAction(item)})
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
