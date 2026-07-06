package config

import "testing"

func TestWellKnownAgentOutputsExcludeClaudeDesktop(t *testing.T) {
	if _, ok := WellKnownAgentOutputs["claude"]; ok {
		t.Fatal("claude desktop should not have a built-in skills output directory")
	}
	if WellKnownAgentOutputs["claude-code"] != "~/.claude/skills" {
		t.Fatalf("claude-code output = %q", WellKnownAgentOutputs["claude-code"])
	}
	if WellKnownAgentOutputs["codex"] != "~/.codex/skills" {
		t.Fatalf("codex output = %q", WellKnownAgentOutputs["codex"])
	}
}
