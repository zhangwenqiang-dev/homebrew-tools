package connectmac

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
func (a App) runProfile(ctx context.Context, configPath string, cfg Config, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm profile <accounts|find|show|add|wizard|remove|rename|edit|export|import|import-dir> ...")
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
	case "wizard":
		if len(args) != 1 {
			fmt.Fprintln(a.Err, "usage: cm profile wizard")
			return 2
		}
		return a.runProfileAddWizard(configPath, cfg)
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
	profile.AWS.Creator = a.promptLine("AWS creator (required before confirmed AWS create/open): ")
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
	if profile.AWS.Creator == "" {
		warnings = append(warnings, "aws.creator is empty; confirmed AWS create/open/adopt commands will ask who is creating the Mac")
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
