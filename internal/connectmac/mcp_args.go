package connectmac

import (
	"fmt"
)

func requireMCPProfile(cfg Config, args map[string]interface{}) (Profile, error) {
	name, err := requiredString(args, "profile")
	if err != nil {
		return Profile{}, err
	}
	profile, ok := cfg.Profile(name)
	if !ok {
		return Profile{}, unknownProfileError(cfg, name)
	}
	return profile, nil
}
func requireMCPAppleProfile(cfg Config, args map[string]interface{}) (Profile, error) {
	email, err := requiredString(args, "apple_email")
	if err != nil {
		return Profile{}, fmt.Errorf("Apple account email is required. Ask the user to choose one.\n%s", FormatAppleAccountChoices(cfg))
	}
	return cfg.ProfileByAppleEmail(email)
}
func cloneArgs(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}
func requiredString(args map[string]interface{}, name string) (string, error) {
	value, ok := args[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return text, nil
}
func stringArg(args map[string]interface{}, name, fallback string) string {
	value, ok := args[name].(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}
func boolArg(args map[string]interface{}, name string) bool {
	value, ok := args[name].(bool)
	return ok && value
}
func mcpSyncFilters(args map[string]interface{}) (SyncFilters, error) {
	includes, err := optionalStringArrayArg(args, "includes")
	if err != nil {
		return SyncFilters{}, err
	}
	excludes, err := optionalStringArrayArg(args, "excludes")
	if err != nil {
		return SyncFilters{}, err
	}
	return SyncFilters{Includes: includes, Excludes: excludes}, nil
}
func optionalStringArrayArg(args map[string]interface{}, name string) ([]string, error) {
	value, ok := args[name]
	if !ok || value == nil {
		return nil, nil
	}
	raw, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", name)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("%s must be an array of non-empty strings", name)
		}
		out = append(out, text)
	}
	return out, nil
}
