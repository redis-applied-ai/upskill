package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/config"
	"upskill/internal/render"
)

func writeTestFile(t *testing.T, path string, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentOutputWarningsDedupeSharedPathsAndSkipManagedSkills(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "skills")
	cfg := config.Config{
		StoreDir: filepath.Join(root, "store"),
		LockPath: filepath.Join(root, "upskill-lock.json"),
		AgentOutputs: []config.AgentOutput{
			{Name: "claude", Path: output},
			{Name: "claude-code", Path: output},
		},
	}
	writeTestFile(t, filepath.Join(cfg.StoreDir, "managed", "SKILL.md"), "# Managed\n")
	writeTestFile(t, filepath.Join(output, "managed", "SKILL.md"), "# Managed\n")
	writeTestFile(t, filepath.Join(output, "managed", render.MarkerFile), "{}\n")
	writeTestFile(t, filepath.Join(output, "orphan", "SKILL.md"), "# Orphan\n")

	warnings := AgentOutputWarnings(cfg, "")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if warnings[0].Skill != "orphan" || !strings.Contains(warnings[0].Reason, "not installed") {
		t.Fatalf("warning = %#v", warnings[0])
	}

	warnings = AgentOutputWarnings(cfg, "claude-code")
	if len(warnings) != 1 || warnings[0].Agent != "claude-code" {
		t.Fatalf("filtered warnings = %#v", warnings)
	}
}
