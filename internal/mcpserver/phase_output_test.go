package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/audit"
	"upskill/internal/config"
	"upskill/internal/library"
	"upskill/internal/render"
)

func TestSyncSkillsInputSchemaRemainsAgentAndDryRunOnly(t *testing.T) {
	var syncTool map[string]any
	for _, item := range toolList() {
		if item["name"] == "sync_skills" {
			syncTool = item
			break
		}
	}
	if syncTool == nil {
		t.Fatal("sync_skills tool is not registered")
	}
	schema := syncTool["inputSchema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if len(properties) != 2 {
		t.Fatalf("sync input properties changed: %#v", properties)
	}
	if properties["agent"].(map[string]any)["type"] != "string" {
		t.Fatalf("agent schema = %#v", properties["agent"])
	}
	dryRun := properties["dry_run"].(map[string]any)
	if dryRun["type"] != "boolean" || dryRun["default"] != false {
		t.Fatalf("dry_run schema = %#v", dryRun)
	}
	if required := schema["required"].([]string); len(required) != 0 {
		t.Fatalf("sync required fields changed: %#v", required)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("sync additionalProperties = %#v", schema["additionalProperties"])
	}
}

func TestMCPSyncPreviewAndApplyExposeStructuredAuditAndLegacyWarnings(t *testing.T) {
	cfg := mixedMCPAuditConfig(t)

	previewed, err := callTool(cfg, "sync_skills", map[string]any{"dry_run": true})
	if err != nil {
		t.Fatal(err)
	}
	preview := previewed.(syncSkillsResult)
	if preview.AgentOutputAudit.WarningCount != 3 ||
		preview.AgentOutputAudit.UnmanagedSkillCount != 1 ||
		preview.AgentOutputAudit.MissingMarkerCount != 1 ||
		preview.AgentOutputAudit.InspectionErrorCount != 1 ||
		preview.AgentOutputAudit.UniqueSkillCount != 2 ||
		!preview.AgentOutputAudit.Migratable {
		t.Fatalf("preview audit = %#v", preview.AgentOutputAudit)
	}
	if len(preview.AgentOutputAudit.Agents) != 3 || len(preview.AgentOutputAudit.Items) != 3 || len(preview.Warnings) != 3 {
		t.Fatalf("preview audit shape or warnings = audit:%#v warnings:%#v", preview.AgentOutputAudit, preview.Warnings)
	}

	applied, err := callTool(cfg, "sync_skills", map[string]any{"agent": "unmanaged", "dry_run": false})
	if err != nil {
		t.Fatal(err)
	}
	applyResult := applied.(syncSkillsResult)
	if applyResult.DryRun || !applyResult.Applied {
		t.Fatalf("sync apply state = %#v", applyResult)
	}
	if applyResult.AgentOutputAudit.WarningCount != 1 || applyResult.AgentOutputAudit.UnmanagedSkillCount != 1 || len(applyResult.Warnings) != 1 {
		t.Fatalf("apply audit or warnings = audit:%#v warnings:%#v", applyResult.AgentOutputAudit, applyResult.Warnings)
	}
	encoded, err := json.Marshal(applyResult)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"warnings"`, `"agent_output_audit"`, `"agents"`, `"items"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("apply JSON missing %s: %s", field, encoded)
		}
	}
}

func TestMCPToolResultSeparatesPreflightNoticesFromSyncAudit(t *testing.T) {
	cfg := mixedMCPAuditConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "mixed-source")
	writeFile(t, filepath.Join(sourceRoot, "managed", "SKILL.md"), "---\nname: Managed\n---\n\nChanged.\n")

	params, err := json.Marshal(callParams{Name: "sync_skills", Arguments: map[string]any{"dry_run": true}})
	if err != nil {
		t.Fatal(err)
	}
	resp := handle(cfg, request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("sync RPC error = %#v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] != false {
		t.Fatalf("sync tool error = %#v", result)
	}
	notices, ok := result["notices"].([]notice)
	if !ok || len(notices) != 1 {
		t.Fatalf("preflight notices = %#v", result["notices"])
	}
	if notices[0].Type != "update_available" || notices[0].Phase != "preflight" || notices[0].SkillID != "managed" {
		t.Fatalf("preflight notice = %#v", notices[0])
	}
	for _, item := range notices {
		if item.Type == string(audit.ReasonNotInstalled) || strings.Contains(item.Type, "agent_output") {
			t.Fatalf("audit finding leaked into preflight notices: %#v", notices)
		}
	}

	content := result["content"].([]map[string]string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(content[0]["text"]), &payload); err != nil {
		t.Fatalf("sync text content is not JSON: %v\n%s", err, content[0]["text"])
	}
	auditPayload, ok := payload["agent_output_audit"].(map[string]any)
	if !ok || auditPayload["warning_count"] != float64(3) {
		t.Fatalf("sync text audit = %#v", payload["agent_output_audit"])
	}
	if warnings, ok := payload["warnings"].([]any); !ok || len(warnings) != 3 {
		t.Fatalf("sync text legacy warnings = %#v", payload["warnings"])
	}
	if _, exists := payload["notices"]; exists {
		t.Fatalf("wrapper notices leaked into sync mutation payload: %#v", payload)
	}
}

func TestMCPStructuredToolErrorsRemainAvailable(t *testing.T) {
	params, err := json.Marshal(callParams{Name: "sync_skills", Arguments: map[string]any{"unexpected": true}})
	if err != nil {
		t.Fatal(err)
	}
	resp := handle(testConfig(t), request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	result := resp.Result.(map[string]any)
	if result["isError"] != true {
		t.Fatalf("invalid sync call did not return a tool error: %#v", result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["error"] != "invalid_tool_arguments" {
		t.Fatalf("structured tool error changed: %#v", result["structuredContent"])
	}
}

func TestLegacySyncClientIgnoresAdditiveAuditField(t *testing.T) {
	current := syncSkillsResult{
		DryRun:   false,
		Applied:  true,
		Updated:  []library.InstalledSkill{},
		Rendered: []render.Result{},
		Warnings: []string{"legacy warning"},
		AgentOutputAudit: audit.SummarizeAgentOutputWarnings([]audit.AgentOutputSkillWarning{{
			Agent: "codex", OutputPath: "/codex", Skill: "local",
			SkillPath: "/codex/local", Reason: audit.ReasonNotInstalled,
		}}),
	}
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	type legacySyncResult struct {
		DryRun   bool     `json:"dry_run"`
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings"`
		Updated  []any    `json:"updated"`
		Rendered []any    `json:"rendered"`
	}
	var legacy legacySyncResult
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatalf("legacy client rejected additive field: %v\n%s", err, data)
	}
	if legacy.DryRun || !legacy.Applied || len(legacy.Warnings) != 1 || legacy.Warnings[0] != "legacy warning" || legacy.Updated == nil || legacy.Rendered == nil {
		t.Fatalf("legacy fields changed: %#v", legacy)
	}
}

func mixedMCPAuditConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.AgentOutputs = []config.AgentOutput{
		{Name: "unmanaged", Path: filepath.Join(cfg.Home, "outputs", "unmanaged")},
		{Name: "missing", Path: filepath.Join(cfg.Home, "outputs", "missing")},
		{Name: "broken", Path: filepath.Join(cfg.Home, "outputs", "broken-file")},
	}
	sourceRoot := filepath.Join(cfg.Home, "mixed-source")
	writeFile(t, filepath.Join(sourceRoot, "managed", "SKILL.md"), "---\nname: Managed\n---\n\nOriginal.\n")
	if _, err := library.InstallSource(cfg, sourceRoot, library.InstallOptions{SkillFilter: "managed"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfg.AgentOutputs[0].Path, "local", "SKILL.md"), "# Local\n")
	writeFile(t, filepath.Join(cfg.AgentOutputs[1].Path, "managed", "SKILL.md"), "# Unmarked copy\n")
	if err := os.MkdirAll(filepath.Dir(cfg.AgentOutputs[2].Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.AgentOutputs[2].Path, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}
