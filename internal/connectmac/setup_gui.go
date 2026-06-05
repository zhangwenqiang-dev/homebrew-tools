package connectmac

import (
	"context"
	"fmt"
	"strings"
)

func (a App) runAWSSetupGUI(ctx context.Context, profile Profile, plan MacPlan, confirm bool) int {
	_, status, err := a.AWSService.Status(ctx, profile)
	if err != nil {
		fmt.Fprintf(a.Err, "aws setup-gui failed: %v\n", err)
		return 1
	}
	fmt.Fprint(a.Out, FormatAWSSetupGUIPreview(plan, profile, status))
	if !confirm {
		fmt.Fprintln(a.Out, "Preview only. Run again with --confirm to set the ec2-user password and enable macOS Screen Sharing.")
		return 0
	}
	if !AWSStatusReady(status) {
		fmt.Fprintf(a.Err, "aws setup-gui cannot continue: AWS Mac is not ready: %s\n", AWSReadinessSummary(status))
		return 1
	}
	if !strings.EqualFold(a.promptLine("Continue? [y/N]: "), "y") {
		fmt.Fprintln(a.Err, "aws setup-gui cancelled")
		return 1
	}
	password, ok := a.promptSetupGUIPassword()
	if !ok {
		return 1
	}
	if err := a.executeSetupGUI(ctx, profile, password); err != nil {
		fmt.Fprint(a.Err, awsStoppedMessage("aws setup-gui", err))
		return 1
	}
	fmt.Fprintln(a.Out, "AWS Mac GUI setup completed.")
	return 0
}

func (a App) promptSetupGUIPassword() (string, bool) {
	password, err := a.promptSecret("New ec2-user password: ")
	if err != nil {
		fmt.Fprintf(a.Err, "read password failed: %v\n", err)
		return "", false
	}
	confirm, err := a.promptSecret("Confirm password: ")
	if err != nil {
		fmt.Fprintf(a.Err, "read password confirmation failed: %v\n", err)
		return "", false
	}
	if password == "" {
		fmt.Fprintln(a.Err, "password cannot be empty")
		return "", false
	}
	if err := validateSetupGUIPassword(password); err != nil {
		fmt.Fprintln(a.Err, err)
		return "", false
	}
	if password != confirm {
		fmt.Fprintln(a.Err, "passwords do not match")
		return "", false
	}
	return password, true
}

func (a App) executeSetupGUI(ctx context.Context, profile Profile, password string) error {
	args, err := SSHScriptArgs(profile)
	if err != nil {
		return err
	}
	return a.Runner.RunSSHScript(ctx, args, setupGUIScriptInput(password))
}

func FormatAWSSetupGUIPreview(plan MacPlan, profile Profile, status AWSStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AWS Mac GUI setup for profile %s\n", plan.ProfileName)
	fmt.Fprintf(&b, "Region: %s\n", plan.Region)
	fmt.Fprintf(&b, "Host: %s\n", profile.Host)
	fmt.Fprintf(&b, "User: %s\n", profile.User)
	fmt.Fprintf(&b, "Ready: %t\n", AWSStatusReady(status))
	fmt.Fprintf(&b, "Readiness: %s\n", AWSReadinessSummary(status))
	fmt.Fprintln(&b, "This will set the ec2-user password and enable macOS Screen Sharing.")
	return b.String()
}

func setupGUIScriptInput(password string) string {
	return setupGUIRemoteScriptPrefix() + password + "\n" + setupGUIRemoteScriptSuffix()
}

func validateSetupGUIPassword(password string) error {
	if password == "" {
		return fmt.Errorf("password cannot be empty")
	}
	if strings.ContainsAny(password, "\r\n") {
		return fmt.Errorf("password cannot contain newline characters")
	}
	return nil
}

func setupGUIRemoteScriptPrefix() string {
	return `set -euo pipefail
IFS= read -r CM_GUI_PASSWORD
`
}

func setupGUIRemoteScriptSuffix() string {
	return `if [ -z "$CM_GUI_PASSWORD" ]; then
  echo "password is empty" >&2
  exit 1
fi
sudo /usr/bin/dscl . -passwd /Users/ec2-user "$CM_GUI_PASSWORD"
unset CM_GUI_PASSWORD
sudo launchctl enable system/com.apple.screensharing
sudo launchctl load -w /System/Library/LaunchDaemons/com.apple.screensharing.plist
`
}
