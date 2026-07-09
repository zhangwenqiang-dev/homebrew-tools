package connectmac

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

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
func resolveProfileRef(cfg Config, ref string) (Profile, error) {
	if profile, ok := cfg.Profile(ref); ok {
		return profile, nil
	}
	if strings.Contains(ref, "@") {
		return cfg.ProfileByAppleEmail(ref)
	}
	return Profile{}, unknownProfileError(cfg, ref)
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
func (a App) promptMissingIdentityFile(profile Profile) Profile {
	if profile.IdentityFile != "" {
		return profile
	}
	profile.IdentityFile = DefaultIdentityFile
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
func filepathDir(path string) string {
	if idx := strings.LastIndex(path, string(os.PathSeparator)); idx >= 0 {
		return path[:idx]
	}
	return "."
}
