package connectmac

import (
	"fmt"
	"os"
	"strings"
)

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
	config := strings.Replace(DefaultConfigTemplate(), "  user: ec2-user\n", "  user: "+quoteYAMLString(user)+"\n", 1)
	if identity != "" {
		config = strings.Replace(config, "  identity_file: ~/.ssh/example.pem\n", "  identity_file: "+quoteYAMLString(identity)+"\n", 1)
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
func DefaultConfigTemplate() string {
	return `defaults:
  user: ec2-user
  identity_file: ~/.ssh/example.pem
  aws:
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
