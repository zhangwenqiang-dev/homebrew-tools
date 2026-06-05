package connectmac

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRulesInstallAgentPaths(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	tests := map[string]string{
		"codex":  filepath.Join(project, "AGENTS.md"),
		"claude": filepath.Join(project, "CLAUDE.md"),
		"trae":   filepath.Join(project, "AGENTS.md"),
		"cursor": filepath.Join(project, ".cursor", "rules", "connectmac.mdc"),
	}
	for agent, wantPath := range tests {
		install, err := BuildRulesInstall(agent, project)
		if err != nil {
			t.Fatalf("BuildRulesInstall(%s) returned error: %v", agent, err)
		}
		if install.AgentPath != wantPath {
			t.Fatalf("%s agent path = %q, want %q", agent, install.AgentPath, wantPath)
		}
		if install.SourcePath != filepath.Join(home, ".connectmac", "rules.md") {
			t.Fatalf("%s source path = %q", agent, install.SourcePath)
		}
	}
}

func TestBuildRulesInstallUsesAgentsSkillsByDefault(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	install, err := BuildRulesInstall("codex", project)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".agents", "skills", "connectmac-aws")
	if install.SkillPath != want {
		t.Fatalf("skill path = %q, want %q", install.SkillPath, want)
	}
}

func TestInstallRulesWritesSourceAndUpsertsAgentBlock(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	agentFile := filepath.Join(project, "AGENTS.md")
	if err := os.WriteFile(agentFile, []byte("before\n\n"+rulesStart+"\nold\n"+rulesEnd+"\n\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	install, err := BuildRulesInstall("codex", project)
	if err != nil {
		t.Fatal(err)
	}
	result, err := InstallRules(install)
	if err != nil {
		t.Fatalf("InstallRules returned error: %v", err)
	}
	source, err := os.ReadFile(result.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "ConnectMac AWS AI Rules") {
		t.Fatalf("source rules = %q", string(source))
	}
	agent, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(agent)
	if !strings.Contains(text, "before") || !strings.Contains(text, "after") {
		t.Fatalf("agent file lost existing content:\n%s", text)
	}
	if strings.Contains(text, "\nold\n") {
		t.Fatalf("agent file did not replace old block:\n%s", text)
	}
	if strings.Count(text, rulesStart) != 1 || strings.Count(text, rulesEnd) != 1 {
		t.Fatalf("agent file marker count wrong:\n%s", text)
	}
	skill, err := os.ReadFile(filepath.Join(result.SkillPath, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(skill), "name: connectmac-aws") {
		t.Fatalf("skill = %q", string(skill))
	}
}

func TestAppInitRules(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	skillsDir := filepath.Join(t.TempDir(), "skills")
	if code := app.Run(context.Background(), []string{"init-rules", "--agent", "cursor", "--project", project, "--skills-dir", skillsDir}); code != 0 {
		t.Fatalf("init-rules code = %d, err = %s", code, errOut.String())
	}
	cursorRules := filepath.Join(project, ".cursor", "rules", "connectmac.mdc")
	if _, err := os.Stat(cursorRules); err != nil {
		t.Fatalf("expected cursor rules: %v", err)
	}
	if !strings.Contains(out.String(), "Rule content source: ~/.connectmac/rules.md") {
		t.Fatalf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "validation passed") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "connectmac-aws", "SKILL.md")); err != nil {
		t.Fatalf("expected skill: %v", err)
	}
}

func TestAppInitRulesDryRunDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	skillsDir := filepath.Join(t.TempDir(), "skills")
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"init-rules", "--agent", "codex", "--project", project, "--skills-dir", skillsDir, "--dry-run"}); code != 0 {
		t.Fatalf("init-rules dry-run code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "AI rules install dry run") || !strings.Contains(out.String(), "No files were written.") {
		t.Fatalf("out = %q", out.String())
	}
	for _, path := range []string{
		filepath.Join(home, ".connectmac", "rules.md"),
		filepath.Join(project, "AGENTS.md"),
		filepath.Join(skillsDir, "connectmac-aws", "SKILL.md"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run should not create %s, err=%v", path, err)
		}
	}
}

func TestAppInitRulesPrintRulesDoesNotRequireAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, t.TempDir())
	if code := app.Run(context.Background(), []string{"init-rules", "--print-rules"}); code != 0 {
		t.Fatalf("print-rules code = %d, err = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ConnectMac AWS AI Rules") {
		t.Fatalf("out = %q", out.String())
	}
	for _, want := range []string{
		"After a Dedicated Host is allocated, any EC2 launch/start failure must stop the create loop",
		"Never terminate EC2 during open/create/launch-on-host recovery",
		"When AWS create/open/launch-on-host fails, report the exact error reason",
		"Launch EC2 on a Dedicated Host only when the host state is available",
		"Destroy workflows that terminate EC2 must defer Dedicated Host release",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out missing %q = %q", want, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".connectmac", "rules.md")); !os.IsNotExist(err) {
		t.Fatalf("print-rules should not write source, err=%v", err)
	}
}

func TestDefaultSkillTemplateIncludesTerminateSafetyRule(t *testing.T) {
	skill := DefaultSkillTemplate()
	for _, want := range []string{
		"After a Dedicated Host is allocated, any EC2 launch/start failure must stop the create loop",
		"Never terminate EC2 during open/create/launch-on-host recovery",
		"If a Dedicated Host exists, reuse that host",
		"When AWS create/open/launch-on-host fails, report the exact error reason",
		"Launch EC2 on a Dedicated Host only when the host state is available",
		"Destroy workflows that terminate EC2 must defer Dedicated Host release",
	} {
		if !strings.Contains(skill, want) {
			t.Fatalf("skill template missing %q", want)
		}
	}
}
