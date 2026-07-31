package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhaseAwareOutputFullLifecycle(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "upskill")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	home := filepath.Join(root, "home")
	configPath := filepath.Join(root, "config.json")
	claudeOutput := filepath.Join(root, "agent-outputs", "claude")
	codexOutput := filepath.Join(root, "agent-outputs", "codex")
	writeE2EJSON(t, configPath, map[string]any{
		"agents": map[string]string{
			"claude-code": claudeOutput,
			"codex":       codexOutput,
		},
	})
	env := append(os.Environ(), "UPSKILL_HOME="+home, "UPSKILL_CONFIG="+configPath)

	badSource := filepath.Join(root, "sources", "bad")
	availableSource := filepath.Join(root, "sources", "available")
	requestedSource := filepath.Join(root, "sources", "agent-plan")
	badOld := "---\nname: Bad Check\n---\n\nInstalled before source loss.\n"
	availableOld := "---\nname: Available Update\n---\n\nOld.\n"
	availableNew := "---\nname: Available Update\n---\n\nNew.\n"
	requested := "---\nname: Agent Plan\n---\n\nRequested skill.\n"
	writeE2EFile(t, filepath.Join(badSource, "SKILL.md"), badOld)
	writeE2EFile(t, filepath.Join(availableSource, "SKILL.md"), availableOld)
	writeE2EFile(t, filepath.Join(requestedSource, "SKILL.md"), requested)
	if stdout, stderr, code := runE2ECLI(t, binary, env, "--no-check", "add", badSource, "--no-render"); code != 0 {
		t.Fatalf("seed bad skill code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout, stderr, code := runE2ECLI(t, binary, env, "--no-check", "add", availableSource, "--no-render"); code != 0 {
		t.Fatalf("seed available skill code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if err := os.RemoveAll(badSource); err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, filepath.Join(availableSource, "SKILL.md"), availableNew)

	unmanaged := map[string]string{
		filepath.Join(claudeOutput, "shared", "SKILL.md"):       "# Shared Claude\n",
		filepath.Join(codexOutput, "shared", "SKILL.md"):        "# Shared Codex\n",
		filepath.Join(claudeOutput, "claude-local", "SKILL.md"): "# Claude only\n",
		filepath.Join(codexOutput, "codex-local", "SKILL.md"):   "# Codex only\n",
	}
	for path, content := range unmanaged {
		writeE2EFile(t, path, content)
	}
	unmanagedBefore := readE2EBytes(t, unmanaged)

	combined, code := runE2ECLICombined(t, binary, env, "add", requestedSource)
	if code != 0 {
		t.Fatalf("add code = %d\n%s", code, combined)
	}
	resultIndex := strings.Index(combined, "Result\n")
	preflightIndex := strings.Index(combined, "\nPreflight\n")
	auditIndex := strings.Index(combined, "\nAgent output audit\n")
	if resultIndex < 0 || preflightIndex < 0 || auditIndex < 0 || !(resultIndex < preflightIndex && preflightIndex < auditIndex) {
		t.Fatalf("combined lifecycle order = result:%d preflight:%d audit:%d\n%s", resultIndex, preflightIndex, auditIndex, combined)
	}
	for _, want := range []string{
		"OK installed agent-plan",
		"OK rendered agent-plan for claude-code",
		"OK rendered agent-plan for codex",
		"WARNING bad-check: update check failed: locked skill not found in source",
		"NOTICE available-update: update available",
		"4 unmanaged skill occurrences across 2 agents (3 unique skills)",
		"claude-code: 2",
		"codex: 2",
		"unmanaged skills were left unchanged",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("combined add output missing %q:\n%s", want, combined)
		}
	}

	stdout, stderr, code := runE2ECLI(t, binary, env, "add", requestedSource)
	if code != 0 {
		t.Fatalf("separate add code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.HasPrefix(stdout, "Result\n") || strings.Contains(stdout, "Preflight") || strings.Contains(stdout, "Agent output audit") {
		t.Fatalf("primary stdout contract changed:\n%s", stdout)
	}
	if strings.Contains(stderr, "Result\n") || !strings.Contains(stderr, "Preflight\n") || !strings.Contains(stderr, "Agent output audit\n") {
		t.Fatalf("diagnostic stderr contract changed:\n%s", stderr)
	}

	for _, path := range []string{
		filepath.Join(home, "store", "agent-plan", "SKILL.md"),
		filepath.Join(home, "rendered", "claude-code", "agent-plan", "SKILL.md"),
		filepath.Join(home, "rendered", "codex", "agent-plan", "SKILL.md"),
		filepath.Join(claudeOutput, "agent-plan", "SKILL.md"),
		filepath.Join(codexOutput, "agent-plan", "SKILL.md"),
	} {
		if got := string(readE2EFile(t, path)); !strings.Contains(got, "Requested skill.") {
			t.Fatalf("managed lifecycle file %s = %q", path, got)
		}
	}
	assertE2EBytesUnchanged(t, unmanagedBefore)
	lock := string(readE2EFile(t, filepath.Join(home, "upskill-lock.json")))
	if !strings.Contains(lock, `"agent-plan"`) {
		t.Fatalf("lock missing requested skill: %s", lock)
	}

	writeE2EFile(t, filepath.Join(badSource, "SKILL.md"), badOld)
	cliJSON, cliErr, code := runE2ECLI(t, binary, env, "--no-check", "sync", "--dry-run", "--json")
	if code != 0 || cliErr != "" {
		t.Fatalf("CLI sync preview code=%d stderr=%s stdout=%s", code, cliErr, cliJSON)
	}
	cliAudit := decodeE2EAudit(t, []byte(cliJSON))
	assertE2EAuditCounts(t, cliAudit, 4, 4, 3)

	mcpPreview := runE2EMCP(t, binary, env, map[string]any{"dry_run": true})
	mcpPreviewAudit := mcpPreview["agent_output_audit"].(map[string]any)
	assertE2EAuditCounts(t, mcpPreviewAudit, 4, 4, 3)
	if !sameE2EAuditCounts(cliAudit, mcpPreviewAudit) {
		t.Fatalf("CLI/MCP preview audit mismatch: cli=%#v mcp=%#v", cliAudit, mcpPreviewAudit)
	}
	mcpApply := runE2EMCP(t, binary, env, map[string]any{"dry_run": false})
	mcpApplyAudit := mcpApply["agent_output_audit"].(map[string]any)
	assertE2EAuditCounts(t, mcpApplyAudit, 4, 4, 3)
	if mcpApply["applied"] != true || len(mcpApply["warnings"].([]any)) != 4 {
		t.Fatalf("MCP apply contract = %#v", mcpApply)
	}
	assertE2EBytesUnchanged(t, unmanagedBefore)
}

func runE2ECLI(t *testing.T, binary string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return "", "", -1
}

func runE2ECLICombined(t *testing.T, binary string, env []string, args ...string) (string, int) {
	t.Helper()
	outputPath := filepath.Join(t.TempDir(), "combined.txt")
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	cmd.Stdout = output
	cmd.Stderr = output
	runErr := cmd.Run()
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if runErr == nil {
		return string(data), 0
	}
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		return string(data), exitErr.ExitCode()
	}
	t.Fatalf("run combined %v: %v", args, runErr)
	return "", -1
}

func runE2EMCP(t *testing.T, binary string, env []string, arguments map[string]any) map[string]any {
	t.Helper()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "sync_skills",
			"arguments": arguments,
		},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "mcp")
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("MCP sync: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &response); err != nil {
		t.Fatalf("MCP response decode: %v\n%s", err, output)
	}
	result := response["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("MCP tool error: %#v", result)
	}
	content := result["content"].([]any)[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Fatalf("MCP content decode: %v\n%s", err, content)
	}
	return payload
}

func decodeE2EAudit(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("CLI sync JSON decode: %v\n%s", err, data)
	}
	auditPayload, ok := payload["agent_output_audit"].(map[string]any)
	if !ok {
		t.Fatalf("CLI audit payload = %#v", payload["agent_output_audit"])
	}
	return auditPayload
}

func assertE2EAuditCounts(t *testing.T, auditPayload map[string]any, warnings, unmanaged, unique int) {
	t.Helper()
	if auditPayload["warning_count"] != float64(warnings) ||
		auditPayload["unmanaged_skill_count"] != float64(unmanaged) ||
		auditPayload["unique_skill_count"] != float64(unique) ||
		auditPayload["migratable"] != true {
		t.Fatalf("audit counts = %#v", auditPayload)
	}
	if len(auditPayload["agents"].([]any)) != 2 || len(auditPayload["items"].([]any)) != warnings {
		t.Fatalf("audit collections = %#v", auditPayload)
	}
}

func sameE2EAuditCounts(left, right map[string]any) bool {
	for _, field := range []string{"warning_count", "unmanaged_skill_count", "unique_skill_count", "missing_marker_count", "inspection_error_count", "migratable"} {
		if left[field] != right[field] {
			return false
		}
	}
	return true
}

func writeE2EJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeE2EFile(t, path, string(data))
}

func writeE2EFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readE2EFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readE2EBytes(t *testing.T, files map[string]string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte, len(files))
	for path := range files {
		result[path] = readE2EFile(t, path)
	}
	return result
}

func assertE2EBytesUnchanged(t *testing.T, before map[string][]byte) {
	t.Helper()
	for path, want := range before {
		if got := readE2EFile(t, path); !bytes.Equal(got, want) {
			t.Fatalf("unmanaged file changed: %s\nwant:%q\ngot: %q", path, want, got)
		}
	}
}
