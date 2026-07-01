package connectmac

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

func (s MCPServer) mcpProfileShow(cfg Config, args map[string]interface{}) (interface{}, error) {
	ref, err := requiredString(args, "profile")
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	profile, err := resolveProfileRef(cfg, ref)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	return mcpText(FormatProfileFile(profile)), nil
}
func (s MCPServer) mcpProfileAdd(cfg Config, args map[string]interface{}) (interface{}, error) {
	profile, err := mcpProfileFromArgs(args)
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	if _, ok := cfg.Profile(profile.Name); ok {
		return mcpUserError("profile", fmt.Errorf("profile %q already exists", profile.Name)), nil
	}
	if profile.Description == "" && profile.AWS.AccountEmail != "" {
		profile.Description = "Apple account: " + profile.AWS.AccountEmail
	}
	if profile.AWS.ElasticIPOwnerTag.Key == "" && profile.AWS.AccountEmail != "" {
		profile.AWS.ElasticIPOwnerTag = AWSTagConfig{Key: "Apple", Value: profile.AWS.AccountEmail}
	}
	text := fmt.Sprintf("Create local profile %s\n", profile.Name)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to write the profile file."), nil
	}
	path, err := WriteProfileFile(s.ConfigPath, profile)
	if err != nil {
		return mcpUserError("profile_file", err), nil
	}
	return mcpText(text + "Created profile file: " + path), nil
}
func (s MCPServer) mcpProfileRemove(ctx context.Context, cfg Config, args map[string]interface{}) (interface{}, error) {
	name, err := requiredString(args, "profile")
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	profile, ok := cfg.Profile(name)
	if !ok {
		return mcpUserError("profile", unknownProfileError(cfg, name)), nil
	}
	text := fmt.Sprintf("Remove local profile %s\n", profile.Name)
	if !boolArg(args, "force_local") {
		if blocked, detail := s.App.profileRemoveBlockedByAWS(ctx, profile); blocked {
			return mcpText(text + detail), nil
		}
	}
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to remove the local profile file."), nil
	}
	path, err := RemoveProfileFile(s.ConfigPath, profile.Name)
	if err != nil {
		return mcpUserError("profile_file", err), nil
	}
	_ = s.App.StateManager.Remove(profile.Name)
	return mcpText(text + "Removed profile file: " + path), nil
}
func (s MCPServer) mcpProfileRename(cfg Config, args map[string]interface{}) (interface{}, error) {
	oldName, err := requiredString(args, "profile")
	if err != nil {
		return mcpUserError("profile", err), nil
	}
	newName, err := requiredString(args, "new_name")
	if err != nil {
		return mcpUserError("new_name", err), nil
	}
	if _, ok := cfg.Profile(oldName); !ok {
		return mcpUserError("profile", unknownProfileError(cfg, oldName)), nil
	}
	if _, ok := cfg.Profile(newName); ok {
		return mcpUserError("profile", fmt.Errorf("profile %q already exists", newName)), nil
	}
	text := fmt.Sprintf("Rename local profile %s -> %s\n", oldName, newName)
	if !boolArg(args, "confirm") {
		return mcpText(text + "Preview only. Call again with confirm=true to rename the profile file."), nil
	}
	oldPath, newPath, err := RenameProfileFile(s.ConfigPath, oldName, newName)
	if err != nil {
		return mcpUserError("profile_file", err), nil
	}
	_ = s.App.StateManager.Remove(oldName)
	return mcpText(fmt.Sprintf("%sRenamed profile file: %s -> %s", text, oldPath, newPath)), nil
}
func mcpProfileFromArgs(args map[string]interface{}) (Profile, error) {
	name, err := requiredString(args, "name")
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{
		Name:         name,
		Description:  stringArg(args, "description", ""),
		User:         stringArg(args, "user", ""),
		Host:         stringArg(args, "host", ""),
		IdentityFile: NormalizeIdentityFileInput(stringArg(args, "identity_file", "")),
	}
	profile.AWS.Profile = stringArg(args, "aws_profile", "")
	profile.AWS.Region = stringArg(args, "region", "")
	profile.AWS.AccountEmail = stringArg(args, "apple_email", "")
	profile.AWS.Creator = stringArg(args, "creator", "")
	profile.AWS.KeyName = stringArg(args, "key_name", "")
	profile.AWS.SecurityGroupID = stringArg(args, "security_group_id", "")
	profile.AWS.ElasticIPAllocationID = stringArg(args, "elastic_ip_allocation_id", "")
	profile.AWS.ElasticIPPublicIP = stringArg(args, "elastic_ip_public_ip", "")
	profile.AWS.AvailabilityZoneIDs, err = optionalStringArrayArg(args, "availability_zone_ids")
	if err != nil {
		return Profile{}, err
	}
	profile.AWS.InstanceTypePriority, err = optionalStringArrayArg(args, "instance_type_priority")
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}
func (s MCPServer) mcpFindProfileByApple(cfg Config, args map[string]interface{}) (interface{}, error) {
	email, err := requiredString(args, "apple_email")
	if err != nil {
		return mcpTextData("Apple account email is required. Ask the user to choose one.\n"+FormatAppleAccountChoices(cfg), map[string]interface{}{
			"ok":          false,
			"found":       false,
			"error":       err.Error(),
			"apple_email": "",
		}), nil
	}
	profile, err := cfg.ProfileByAppleEmail(email)
	if err != nil {
		return mcpTextData(err.Error(), map[string]interface{}{
			"ok":          false,
			"found":       false,
			"error":       err.Error(),
			"apple_email": email,
		}), nil
	}
	return mcpTextData(fmt.Sprintf("Apple account: %s\nProfile: %s\nDescription: %s\n", email, profile.Name, profile.Description), map[string]interface{}{
		"ok":          true,
		"found":       true,
		"profile":     profile.Name,
		"apple_email": email,
		"description": profile.Description,
	}), nil
}
func listProfilesText(cfg Config) string {
	var b bytes.Buffer
	names := sortedProfileNames(cfg)
	if len(names) == 0 {
		return "no profiles configured"
	}
	nameWidth := len("PROFILE")
	for _, name := range names {
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
	}
	fmt.Fprintf(&b, "%-*s  %s\n", nameWidth, "PROFILE", "DESCRIPTION")
	fmt.Fprintf(&b, "%s  %s\n", strings.Repeat("-", nameWidth), strings.Repeat("-", len("DESCRIPTION")))
	for _, name := range names {
		description := cfg.Profiles[name].Description
		if description == "" {
			description = "-"
		}
		fmt.Fprintf(&b, "%-*s  %s\n", nameWidth, name, description)
	}
	return b.String()
}
func profileSummaryText(profile Profile) string {
	var b bytes.Buffer
	printSummary(&b, profile)
	return b.String()
}
