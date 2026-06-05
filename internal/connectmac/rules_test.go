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
	if _, err := os.Stat(filepath.Join(skillsDir, "connectmac-aws", "SKILL.md")); err != nil {
		t.Fatalf("expected skill: %v", err)
	}
}
