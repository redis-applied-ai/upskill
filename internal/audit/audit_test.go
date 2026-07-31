package audit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"upskill/internal/config"
	"upskill/internal/diagnostic"
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

func TestSummarizeAgentOutputWarningsEmpty(t *testing.T) {
	summary := SummarizeAgentOutputWarnings(nil)
	if summary.WarningCount != 0 || summary.UnmanagedSkillCount != 0 || summary.UniqueSkillCount != 0 ||
		summary.MissingMarkerCount != 0 || summary.InspectionErrorCount != 0 || summary.Migratable {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.Agents == nil || summary.Items == nil {
		t.Fatalf("empty collections must be non-nil: %#v", summary)
	}
	if diagnostics := summary.Diagnostics(""); diagnostics == nil || len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestSummarizeAgentOutputWarningsMixedFindings(t *testing.T) {
	items := []AgentOutputSkillWarning{
		{Agent: "codex", OutputPath: "/outputs/codex", Skill: "shared", SkillPath: "/outputs/codex/shared", Reason: ReasonNotInstalled},
		{Agent: "claude-code", OutputPath: "/outputs/claude", Skill: "shared", SkillPath: "/outputs/claude/shared", Reason: ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/outputs/codex", Skill: "marker", SkillPath: "/outputs/codex/marker", Reason: ReasonMissingRenderMarker},
		{Agent: "claude-code", OutputPath: "/outputs/claude", Reason: "could not inspect agent output directory: permission denied"},
	}
	before := append([]AgentOutputSkillWarning(nil), items...)

	summary := SummarizeAgentOutputWarnings(items)
	if !reflect.DeepEqual(items, before) {
		t.Fatalf("input mutated:\n got: %#v\nwant: %#v", items, before)
	}
	if summary.WarningCount != 4 || summary.UnmanagedSkillCount != 2 || summary.UniqueSkillCount != 2 ||
		summary.MissingMarkerCount != 1 || summary.InspectionErrorCount != 1 || !summary.Migratable {
		t.Fatalf("summary counts = %#v", summary)
	}
	if len(summary.Agents) != 2 {
		t.Fatalf("agents = %#v", summary.Agents)
	}
	if got := summary.Agents[0]; got.Agent != "claude-code" || got.WarningCount != 2 || got.UnmanagedSkillCount != 1 ||
		got.InspectionErrorCount != 1 || !reflect.DeepEqual(got.Skills, []string{"shared"}) {
		t.Fatalf("claude summary = %#v", got)
	}
	if got := summary.Agents[1]; got.Agent != "codex" || got.WarningCount != 2 || got.UnmanagedSkillCount != 1 ||
		got.MissingMarkerCount != 1 || !reflect.DeepEqual(got.Skills, []string{"marker", "shared"}) {
		t.Fatalf("codex summary = %#v", got)
	}
	wantItemOrder := []string{"", "shared", "marker", "shared"}
	for index, skill := range wantItemOrder {
		if summary.Items[index].Skill != skill {
			t.Fatalf("item order = %#v", summary.Items)
		}
	}

	diagnostics := summary.Diagnostics("codex")
	if len(diagnostics) != 3 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	unmanaged := findAuditDiagnostic(t, diagnostics, diagnostic.CodeUnmanagedAgentOutput)
	wantActions := []diagnostic.Action{
		{Label: "details", Command: []string{"upskill", "doctor"}},
		{Label: "preview migration", Command: []string{"upskill", "migrate", "--agent", "codex"}},
		{Label: "apply migration", Command: []string{"upskill", "migrate", "--agent", "codex", "--yes"}},
	}
	if !reflect.DeepEqual(unmanaged.Actions, wantActions) {
		t.Fatalf("unmanaged actions = %#v", unmanaged.Actions)
	}
	missing := findAuditDiagnostic(t, diagnostics, diagnostic.CodeMissingRenderMarker)
	if !reflect.DeepEqual(missing.Actions, []diagnostic.Action{{Label: "repair managed outputs", Command: []string{"upskill", "sync", "--agent", "codex"}}}) {
		t.Fatalf("missing marker actions = %#v", missing.Actions)
	}
	inspection := findAuditDiagnostic(t, diagnostics, diagnostic.CodeAgentOutputInspectionFailed)
	if len(inspection.Actions) != 1 || !reflect.DeepEqual(inspection.Actions[0].Command, []string{"upskill", "doctor"}) {
		t.Fatalf("inspection actions = %#v", inspection.Actions)
	}
}

func TestSummarizeAgentOutputWarningsMissingMarkerIsNotMigratable(t *testing.T) {
	summary := SummarizeAgentOutputWarnings([]AgentOutputSkillWarning{{
		Agent:      "codex",
		OutputPath: "/outputs/codex",
		Skill:      "managed",
		SkillPath:  "/outputs/codex/managed",
		Reason:     ReasonMissingRenderMarker,
	}})
	if summary.Migratable || summary.UnmanagedSkillCount != 0 || summary.MissingMarkerCount != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	diagnostics := summary.Diagnostics("")
	if len(diagnostics) != 1 || diagnostics[0].Code != diagnostic.CodeMissingRenderMarker {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, action := range diagnostics[0].Actions {
		if len(action.Command) > 1 && action.Command[1] == "migrate" {
			t.Fatalf("missing marker suggested migration: %#v", action)
		}
	}
}

func TestAgentOutputWarningMessagesFromDoesNotInspectPaths(t *testing.T) {
	items := []AgentOutputSkillWarning{
		{Agent: "codex", OutputPath: "/does/not/exist", Skill: "orphan", SkillPath: "/does/not/exist/orphan", Reason: ReasonNotInstalled},
		{Agent: "claude", OutputPath: "/also/missing", Reason: "could not inspect agent output directory: unavailable"},
	}
	messages := AgentOutputWarningMessagesFrom(items)
	if len(messages) != 2 || !strings.Contains(messages[0], "orphan") || !strings.Contains(messages[1], "unavailable") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestAgentOutputWarningsReportsMissingMarkerAndInspectionError(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	output := filepath.Join(root, "output")
	unreadable := filepath.Join(root, "not-a-directory")
	writeTestFile(t, filepath.Join(store, "managed", "SKILL.md"), "# Managed\n")
	writeTestFile(t, filepath.Join(output, "managed", "SKILL.md"), "# Managed\n")
	writeTestFile(t, unreadable, "regular file\n")

	warnings := AgentOutputWarnings(config.Config{
		StoreDir: store,
		LockPath: filepath.Join(root, "upskill-lock.json"),
		AgentOutputs: []config.AgentOutput{
			{Name: "codex", Path: output},
			{Name: "broken", Path: unreadable},
		},
	}, "")
	if len(warnings) != 2 {
		t.Fatalf("warnings = %#v", warnings)
	}
	summary := SummarizeAgentOutputWarnings(warnings)
	if summary.MissingMarkerCount != 1 || summary.InspectionErrorCount != 1 || summary.UnmanagedSkillCount != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}

func findAuditDiagnostic(t *testing.T, diagnostics []diagnostic.Diagnostic, code diagnostic.Code) diagnostic.Diagnostic {
	t.Helper()
	for _, item := range diagnostics {
		if item.Code == code {
			return item
		}
	}
	t.Fatalf("diagnostic %q not found in %#v", code, diagnostics)
	return diagnostic.Diagnostic{}
}
