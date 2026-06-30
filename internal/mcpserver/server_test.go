package mcpserver

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/config"
	"upskill/internal/draft"
	"upskill/internal/library"
	"upskill/internal/render"
)

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func testConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		Home:         filepath.Join(root, "home"),
		StoreDir:     filepath.Join(root, "home", "store"),
		OverlaysDir:  filepath.Join(root, "home", "overlays"),
		RenderedDir:  filepath.Join(root, "home", "rendered"),
		DraftsDir:    filepath.Join(root, "home", "drafts"),
		ImportsDir:   filepath.Join(root, "home", "imports"),
		ImportDirs:   []string{filepath.Join(root, "home", "imports")},
		ConfigPath:   filepath.Join(root, "home", "config.json"),
		LockPath:     filepath.Join(root, "home", "upskill-lock.json"),
		AgentOutputs: []config.AgentOutput{{Name: "codex", Path: filepath.Join(root, "codex-skills")}},
		Sources:      map[string]config.Source{},
	}
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nBase.\n")
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "references", "guide.md"), "Guide.\n")
	return cfg
}

func writeFile(t *testing.T, path string, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeMCPZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	writer := zip.NewWriter(out)
	defer writer.Close()
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandleProtocolMethods(t *testing.T) {
	cfg := testConfig(t)
	id := json.RawMessage(`1`)
	initResp := handle(cfg, request{ID: id, Method: "initialize"})
	if initResp.Error != nil || initResp.Result == nil {
		t.Fatalf("initialize response = %#v", initResp)
	}
	toolsResp := handle(cfg, request{ID: id, Method: "tools/list"})
	data, _ := json.Marshal(toolsResp.Result)
	if !strings.Contains(string(data), "upsert_skill_overlay_file") {
		t.Fatalf("tools response = %s", data)
	}
	if !strings.Contains(string(data), "create_skill_draft") || !strings.Contains(string(data), "render_skill_draft") {
		t.Fatalf("tools response missing draft tools: %s", data)
	}
	if !strings.Contains(string(data), `"skill_id"`) || !strings.Contains(string(data), `"properties"`) {
		t.Fatalf("tools response missing input schema properties: %s", data)
	}
	missing := handle(cfg, request{ID: id, Method: "missing"})
	if missing.Error == nil || missing.Error.Code != -32601 {
		t.Fatalf("missing response = %#v", missing)
	}
	bad := handle(cfg, request{ID: id, Method: "tools/call", Params: json.RawMessage(`{bad`)})
	if bad.Error == nil || bad.Error.Code != -32602 {
		t.Fatalf("bad call response = %#v", bad)
	}
	unknownParams, _ := json.Marshal(callParams{Name: "missing", Arguments: map[string]any{}})
	unknown := handle(cfg, request{ID: id, Method: "tools/call", Params: unknownParams})
	if result, ok := unknown.Result.(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("unknown tool response = %#v", unknown)
	}
	successParams, _ := json.Marshal(callParams{Name: "find_skills", Arguments: map[string]any{"query": "cache"}})
	success := handle(cfg, request{ID: id, Method: "tools/call", Params: successParams})
	if success.Error != nil {
		t.Fatalf("successful tool response = %#v", success)
	}
	result, ok := success.Result.(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("successful tool result = %#v", success.Result)
	}
}

func TestToolCallsReadAndOverlayOnlyWrites(t *testing.T) {
	cfg := testConfig(t)
	found, err := callTool(cfg, "find_skills", map[string]any{"query": "cache", "limit": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	foundEntries := found.([]map[string]any)
	if len(foundEntries) != 1 || foundEntries[0]["kind"] != "base" {
		t.Fatalf("found = %#v", found)
	}
	base, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "rendered": false, "include_files": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(base), "Base.") {
		t.Fatalf("base = %#v", base)
	}
	files, err := callTool(cfg, "list_skill_files", map[string]any{"skill_id": "redis", "rendered": false, "include_hashes": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(files), "references/guide.md") {
		t.Fatalf("files = %#v", files)
	}
	file, err := callTool(cfg, "read_skill_file", map[string]any{"skill_id": "redis", "path": "references/guide.md", "rendered": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(file), "Guide.") {
		t.Fatalf("file = %#v", file)
	}

	upsert, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id":  "redis",
		"path":      "SKILL.md",
		"content":   "Overlay.\n",
		"agent":     "codex",
		"operation": "append",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(upsert), "base_mutated") {
		t.Fatalf("upsert = %#v", upsert)
	}
	baseBytes, _ := os.ReadFile(filepath.Join(cfg.StoreDir, "redis", "SKILL.md"))
	if strings.Contains(string(baseBytes), "Overlay.") {
		t.Fatalf("base was mutated: %s", baseBytes)
	}
	rendered, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rendered), "Overlay.") {
		t.Fatalf("rendered = %#v", rendered)
	}
	if _, err := callTool(cfg, "render_skill", map[string]any{"skill_id": "redis", "agent": "codex", "copy": false}); err != nil {
		t.Fatal(err)
	}
	synced, err := callTool(cfg, "sync_skills", map[string]any{"agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(synced), `"updated":[]`) || !strings.Contains(mustJSON(synced), `"rendered_count":1`) {
		t.Fatalf("synced = %#v", synced)
	}
	overlays, err := callTool(cfg, "list_skill_overlays", map[string]any{"skill_id": "redis", "agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(overlays), "codex") {
		t.Fatalf("overlays = %#v", overlays)
	}
	deleted, err := callTool(cfg, "delete_skill_overlay_file", map[string]any{"skill_id": "redis", "path": "SKILL.md", "agent": "codex", "operation": "append"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(deleted), "deleted") {
		t.Fatalf("deleted = %#v", deleted)
	}
}

func TestMCPDraftWorkflow(t *testing.T) {
	cfg := testConfig(t)
	created, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(created), `"type":"version"`) {
		t.Fatalf("created = %#v", created)
	}

	found, err := callTool(cfg, "find_skills", map[string]any{"query": "draft:redis", "limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(found), `"kind":"draft"`) {
		t.Fatalf("found = %#v", found)
	}
	listed, err := callTool(cfg, "list_skill_drafts", map[string]any{"query": "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(listed), "draft:redis") {
		t.Fatalf("listed = %#v", listed)
	}
	read, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "draft:redis", "include_files": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(read), "Base.") || !strings.Contains(mustJSON(read), "references/guide.md") {
		t.Fatalf("read = %#v", read)
	}
	draftRead, err := callTool(cfg, "read_skill_draft", map[string]any{"draft_id": "draft:redis", "include_files": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(draftRead), `"metadata"`) {
		t.Fatalf("draft read = %#v", draftRead)
	}
	files, err := callTool(cfg, "list_skill_draft_files", map[string]any{"draft_id": "draft:redis", "include_hashes": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(files), "references/guide.md") {
		t.Fatalf("files = %#v", files)
	}
	file, err := callTool(cfg, "read_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "references/guide.md", "max_bytes": 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(file), `"content":"Gu"`) || !strings.Contains(mustJSON(file), `"size":7`) {
		t.Fatalf("file = %#v", file)
	}

	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{
		"draft_id":       "draft:redis",
		"path":           "references/new.md",
		"base64_content": base64.StdEncoding.EncodeToString([]byte("New resource.")),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{
		"draft_id": "draft:redis",
		"path":     "references/plain.md",
		"content":  "Plain resource.",
	}); err != nil {
		t.Fatal(err)
	}
	baseBytes, _ := os.ReadFile(filepath.Join(cfg.StoreDir, "redis", "references", "new.md"))
	if strings.Contains(string(baseBytes), "New resource.") {
		t.Fatalf("base was mutated: %s", baseBytes)
	}
	plain, err := callTool(cfg, "read_skill_file", map[string]any{"skill_id": "draft:redis", "path": "references/plain.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(plain), "Plain resource.") {
		t.Fatalf("plain = %#v", plain)
	}
	if _, err := callTool(cfg, "delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "references/plain.md"}); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(cfg.ImportsDir, "extra.txt"), "imported")
	importedFile, err := callTool(cfg, "import_skill_draft_file", map[string]any{
		"draft_id":    "draft:redis",
		"source_path": filepath.Join(cfg.ImportsDir, "extra.txt"),
		"path":        "references/imported.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(importedFile), `"imported":true`) {
		t.Fatalf("importedFile = %#v", importedFile)
	}
	archive := filepath.Join(cfg.ImportsDir, "bundle.zip")
	writeMCPZip(t, archive, map[string]string{
		"bundle/SKILL.md":             "---\nname: Redis\ndescription: imported\n---\n\nImported.\n",
		"bundle/references/zip.md":    "Zip resource.",
		"bundle/references/plain.txt": "Plain import.",
	})
	inspected, err := callTool(cfg, "import_skill_draft_archive", map[string]any{
		"draft_id":     "draft:redis",
		"archive_path": archive,
		"inspect":      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(inspected), `"imported":false`) {
		t.Fatalf("inspected = %#v", inspected)
	}
	if _, err := callTool(cfg, "import_skill_draft_archive", map[string]any{"draft_id": "draft:redis", "archive_path": archive}); err != nil {
		t.Fatal(err)
	}
	rendered, err := callTool(cfg, "render_skill_draft", map[string]any{"draft_id": "draft:redis", "agent": "codex", "copy": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rendered), `"mode":"draft"`) {
		t.Fatalf("rendered = %#v", rendered)
	}
	renderedAlias, err := callTool(cfg, "render_skill", map[string]any{"skill_id": "draft:redis", "apply_overlays": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(renderedAlias), `"skill_id":"draft:redis"`) {
		t.Fatalf("renderedAlias = %#v", renderedAlias)
	}
	diff, err := callTool(cfg, "diff_skill_draft", map[string]any{"draft_id": "draft:redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(diff), "references/imported.txt") {
		t.Fatalf("diff = %#v", diff)
	}
	rebase, err := callTool(cfg, "rebase_skill_draft", map[string]any{"draft_id": "draft:redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rebase), `"rebased":false`) {
		t.Fatalf("rebase = %#v", rebase)
	}
	promote, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": filepath.Join(t.TempDir(), "repo")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(promote), `"dry_run":true`) {
		t.Fatalf("promote = %#v", promote)
	}
}

func TestMCPNewDraftWorkflow(t *testing.T) {
	cfg := testConfig(t)
	created, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "brand-new", "template": "qa"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(created), `"type":"new"`) {
		t.Fatalf("created = %#v", created)
	}
	existing, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "brand-new", "allow_existing": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(existing), "draft:brand-new") {
		t.Fatalf("existing = %#v", existing)
	}
	rendered, err := callTool(cfg, "render_skill_draft", map[string]any{"skill_id": "draft:brand-new"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rendered), "draft:brand-new") {
		t.Fatalf("rendered = %#v", rendered)
	}
	if _, err := callTool(cfg, "rebase_skill_draft", map[string]any{"draft_id": "draft:brand-new"}); err == nil {
		t.Fatal("new draft rebase unexpectedly succeeded")
	}
	renderedDraft, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis-rendered", "from_skill": "redis", "from_rendered": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(renderedDraft), `"created_from":"rendered"`) {
		t.Fatalf("renderedDraft = %#v", renderedDraft)
	}
}

func TestMCPTranscriptRegressionWorkflow(t *testing.T) {
	cfg := testConfig(t)
	createdNew, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "team-greeting-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(createdNew), `"type":"new"`) {
		t.Fatalf("createdNew = %#v", createdNew)
	}
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "team-greeting-demo", "from_skill": "redis", "allow_existing": true}); err == nil {
		t.Fatal("conflicting allow_existing draft unexpectedly succeeded")
	}
	replaced, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "team-greeting-demo", "from_skill": "redis", "replace_existing": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(replaced), `"type":"version"`) {
		t.Fatalf("replaced = %#v", replaced)
	}
	deleted, err := callTool(cfg, "delete_skill_draft", map[string]any{"skill_name": "team-greeting-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(deleted), `"deleted":true`) {
		t.Fatalf("deleted = %#v", deleted)
	}
	if draft.Exists(cfg, "team-greeting-demo") {
		t.Fatal("draft still exists after delete")
	}

	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
		sourceRoot := filepath.Join(t.TempDir(), "local", "source")
		cfg.Sources["local"] = config.Source{Name: "local", Source: sourceRoot}
		promoted, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "local", "dry_run": false})
	if err != nil {
		t.Fatal(err)
	}
		if !strings.Contains(mustJSON(promoted), `"source":"local"`) || !strings.Contains(readFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md")), "Base.") {
		t.Fatalf("promoted = %#v", promoted)
	}
	if _, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": cfg.StoreDir}); err == nil {
		t.Fatal("managed store promotion unexpectedly succeeded")
	}
		if _, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": sourceRoot, "to_source": "local"}); err == nil {
		t.Fatal("to_path plus to_source unexpectedly succeeded")
	}
	cfg.Sources["remote"] = config.Source{Name: "remote", Source: "owner/repo"}
	if _, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "remote"}); err == nil {
		t.Fatal("remote source promotion unexpectedly succeeded")
	}

	if _, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id":  "redis",
		"operation": "template",
		"path":      "SKILL.md.j2",
		"content":   `{"tone":"casual"}`,
	}); err == nil {
		t.Fatal("JSON template overlay unexpectedly succeeded")
	}
	template, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id":  "redis",
		"operation": "template",
		"path":      "SKILL.md.j2",
		"content":   `{% extends "base/SKILL.md.j2" %}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(template), `"updated":true`) {
		t.Fatalf("template = %#v", template)
	}
}

func TestMCPDraftSafetyErrors(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "outside")
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"create_skill_draft", map[string]any{}},
		{"create_skill_draft", map[string]any{"skill_name": "redis"}},
		{"create_skill_draft", map[string]any{"skill_name": "missing-rendered", "from_rendered": true}},
		{"read_skill_draft", map[string]any{}},
		{"read_skill_draft", map[string]any{"draft_id": "redis"}},
		{"read_skill_draft", map[string]any{"draft_id": "draft:missing"}},
		{"list_skill_draft_files", map[string]any{"draft_id": "redis"}},
		{"read_skill_draft_file", map[string]any{"draft_id": "draft:redis"}},
		{"read_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "../bad"}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "x.md"}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "x.md", "content": "", "base64_content": ""}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "x.md", "base64_content": "bad"}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": ".upskill-draft.json", "content": "bad"}},
		{"delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": ".upskill-draft.json"}},
		{"delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "missing.md"}},
		{"delete_skill_draft", map[string]any{"draft_id": "draft:missing"}},
		{"import_skill_draft_archive", map[string]any{"draft_id": "draft:redis"}},
		{"import_skill_draft_file", map[string]any{"draft_id": "draft:redis", "source_path": outside, "path": "outside.txt"}},
		{"render_skill_draft", map[string]any{}},
		{"diff_skill_draft", map[string]any{}},
		{"rebase_skill_draft", map[string]any{"draft_id": "draft:missing"}},
		{"promote_skill_draft", map[string]any{"draft_id": "draft:redis"}},
		{"upsert_skill_overlay_file", map[string]any{"skill_id": "draft:redis", "path": "SKILL.md", "content": "bad"}},
		{"delete_skill_overlay_file", map[string]any{"skill_id": "draft:redis", "path": "SKILL.md"}},
	} {
		if _, err := callTool(cfg, tc.name, tc.args); err == nil {
			t.Fatalf("%s unexpectedly succeeded with %#v", tc.name, tc.args)
		}
	}
}

func TestMCPDraftAdditionalBranches(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}

	foundDraftOnly, err := callTool(cfg, "find_skills", map[string]any{"query": "draft:redis", "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(foundDraftOnly), `"kind":"draft"`) {
		t.Fatalf("foundDraftOnly = %#v", foundDraftOnly)
	}
	foundLimited, err := callTool(cfg, "find_skills", map[string]any{"query": "Redis", "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(foundLimited.([]map[string]any)) != 1 {
		t.Fatalf("foundLimited = %#v", foundLimited)
	}
	draftOnlyCfg := cfg
	draftOnlyCfg.StoreDir = filepath.Join(t.TempDir(), "empty-store")
	if _, err := draft.Save(draftOnlyCfg, draft.Metadata{Name: "alpha", Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Save(draftOnlyCfg, draft.Metadata{Name: "beta", Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	noMatches, err := findSkills(draftOnlyCfg, "nomatch", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(noMatches) != 0 {
		t.Fatalf("noMatches = %#v", noMatches)
	}
	draftLimited, err := findSkills(draftOnlyCfg, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(draftLimited) != 1 || draftLimited[0]["kind"] != "draft" {
		t.Fatalf("draftLimited = %#v", draftLimited)
	}
	if _, err := findSkills(config.Config{StoreDir: filepath.Join(cfg.StoreDir, "redis", "SKILL.md")}, "", 10); err == nil {
		t.Fatal("findSkills bad store unexpectedly succeeded")
	}
	if _, err := findSkills(config.Config{StoreDir: cfg.StoreDir, DraftsDir: filepath.Join(cfg.StoreDir, "redis", "SKILL.md")}, "", 10); err == nil {
		t.Fatal("findSkills bad drafts unexpectedly succeeded")
	}

	noSkillCfg := cfg
	if _, err := draft.Save(noSkillCfg, draft.Metadata{Name: "source-only", Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(noSkillCfg, "read_skill_draft", map[string]any{"draft_id": "draft:source-only"}); err == nil {
		t.Fatal("read draft without SKILL.md unexpectedly succeeded")
	}
	badExistingCfg := cfg
	badExisting := filepath.Join(badExistingCfg.DraftsDir, "bad-existing")
	if err := os.MkdirAll(badExisting, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badExisting, draft.MetadataFile), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := createSkillDraft(badExistingCfg, map[string]any{"skill_name": "bad-existing", "allow_existing": true}); err == nil {
		t.Fatal("allow_existing read error unexpectedly succeeded")
	}
	if _, err := listSkillDrafts(config.Config{DraftsDir: filepath.Join(cfg.StoreDir, "redis", "SKILL.md")}, map[string]any{}); err == nil {
		t.Fatal("listSkillDrafts bad drafts dir unexpectedly succeeded")
	}
	limitedDrafts, err := listSkillDrafts(draftOnlyCfg, map[string]any{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limitedDrafts.([]map[string]any)) != 1 {
		t.Fatalf("limitedDrafts = %#v", limitedDrafts)
	}
	oldManifest := fileManifest
	fileManifest = func(string, string, bool, bool, int) ([]map[string]any, error) {
		return nil, errors.New("manifest failed")
	}
	t.Cleanup(func() { fileManifest = oldManifest })
	if _, err := callTool(cfg, "read_skill_draft", map[string]any{"draft_id": "draft:redis"}); err == nil {
		t.Fatal("read draft manifest failure unexpectedly succeeded")
	}
	fileManifest = oldManifest
	if _, err := readSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("readSkillDraftFile without draft unexpectedly succeeded")
	}
	if _, err := upsertSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("upsertSkillDraftFile without draft unexpectedly succeeded")
	}
	if _, err := deleteSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("deleteSkillDraftFile without draft unexpectedly succeeded")
	}
	if _, err := promoteSkillDraft(cfg, map[string]any{}); err == nil {
		t.Fatal("promoteSkillDraft without draft unexpectedly succeeded")
	}
	if _, err := deleteSkillDraft(cfg, map[string]any{}); err == nil {
		t.Fatal("deleteSkillDraft without draft unexpectedly succeeded")
	}
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "draft:missing"}); err == nil {
		t.Fatal("read_skill missing draft unexpectedly succeeded")
	}

	for _, tc := range []map[string]any{
		{"draft_id": "draft:redis", "path": "bad.md", "content": 1},
		{"draft_id": "draft:redis", "path": "bad.md", "base64_content": 1},
		{"draft_id": "draft:redis", "path": "../bad.md", "content": "bad"},
	} {
		if _, err := callTool(cfg, "upsert_skill_draft_file", tc); err == nil {
			t.Fatalf("upsert unexpectedly succeeded with %#v", tc)
		}
	}
	writeFile(t, filepath.Join(cfg.DraftsDir, "redis", "blocked"), "file")
	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "blocked/file.md", "content": "bad"}); err == nil {
		t.Fatal("upsert mkdir failure unexpectedly succeeded")
	}
	if err := os.MkdirAll(filepath.Join(cfg.DraftsDir, "redis", "as-dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "as-dir.md", "content": "bad"}); err == nil {
		t.Fatal("upsert write failure unexpectedly succeeded")
	}
	if _, err := callTool(cfg, "delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "../bad"}); err == nil {
		t.Fatal("delete traversal unexpectedly succeeded")
	}

	if _, err := importSkillDraftArchive(cfg, map[string]any{}); err == nil {
		t.Fatal("import archive without draft unexpectedly succeeded")
	}
	if _, err := importSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("import file without draft unexpectedly succeeded")
	}
	dest := filepath.Join(t.TempDir(), "repo")
	promoted, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": dest, "dry_run": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(promoted), `"dry_run":false`) || !strings.Contains(readFile(t, filepath.Join(dest, "redis", "SKILL.md")), "Base.") {
		t.Fatalf("promoted = %#v", promoted)
	}

	if _, err := createDraftForMCP(cfg, "", "", false, ""); err == nil {
		t.Fatal("empty createDraftForMCP unexpectedly succeeded")
	}
	if _, err := createNewDraftForMCP(cfg, "redis", "again"); err != nil {
		t.Fatalf("createNewDraftForMCP existing failed: %v", err)
	}
	oldMCPStat := mcpOsStat
	oldMCPWriteFile := mcpOsWriteFile
	oldMCPRemoveAll := mcpOsRemoveAll
	oldMCPCopyDir := mcpCopyDir
	oldMCPDraftRemove := mcpDraftRemove
	t.Cleanup(func() {
		mcpOsStat = oldMCPStat
		mcpOsWriteFile = oldMCPWriteFile
		mcpOsRemoveAll = oldMCPRemoveAll
		mcpCopyDir = oldMCPCopyDir
		mcpDraftRemove = oldMCPDraftRemove
	})
	blockedParent := filepath.Join(t.TempDir(), "blocked-parent")
	writeFile(t, blockedParent, "file")
	if _, err := createNewDraftForMCP(config.Config{DraftsDir: filepath.Join(blockedParent, "drafts")}, "blocked", ""); err == nil {
		t.Fatal("createNewDraftForMCP save error unexpectedly succeeded")
	}
	mcpOsStat = func(path string) (os.FileInfo, error) {
		if strings.HasSuffix(path, "SKILL.md") {
			return nil, errors.New("stat failed")
		}
		return oldMCPStat(path)
	}
	if _, err := createNewDraftForMCP(cfg, "stat-error", ""); err == nil {
		t.Fatal("createNewDraftForMCP stat error unexpectedly succeeded")
	}
	mcpOsStat = oldMCPStat
	mcpOsWriteFile = func(string, []byte, os.FileMode) error { return errors.New("write failed") }
	if _, err := createNewDraftForMCP(cfg, "write-error", ""); err == nil {
		t.Fatal("createNewDraftForMCP write error unexpectedly succeeded")
	}
	mcpOsWriteFile = oldMCPWriteFile
	writeFile(t, filepath.Join(cfg.StoreDir, "not-dir"), "file")
	if _, err := createVersionDraftForMCP(cfg, "from-file", "not-dir", false); err == nil {
		t.Fatal("createVersionDraftForMCP from file unexpectedly succeeded")
	}
	writeFile(t, filepath.Join(cfg.StoreDir, "bad-render", "SKILL.md.j2"), "{% bad")
	if _, err := createVersionDraftForMCP(cfg, "bad-render-draft", "bad-render", true); err == nil {
		t.Fatal("createVersionDraftForMCP render error unexpectedly succeeded")
	}
	mcpOsRemoveAll = func(string) error { return errors.New("remove failed") }
	if _, err := createVersionDraftForMCP(cfg, "remove-error", "redis", false); err == nil {
		t.Fatal("createVersionDraftForMCP remove error unexpectedly succeeded")
	}
	mcpOsRemoveAll = oldMCPRemoveAll
	mcpCopyDir = func(string, string, func(string, os.DirEntry) bool) error { return errors.New("copy failed") }
	if _, err := createVersionDraftForMCP(cfg, "copy-error", "redis", false); err == nil {
		t.Fatal("createVersionDraftForMCP copy error unexpectedly succeeded")
	}
	mcpCopyDir = oldMCPCopyDir
	mcpDraftRemove = func(config.Config, string) error { return errors.New("remove draft failed") }
	if _, err := createSkillDraft(cfg, map[string]any{"skill_name": "redis", "replace_existing": true}); err == nil {
		t.Fatal("replace_existing remove error unexpectedly succeeded")
	}
	if _, err := deleteSkillDraft(cfg, map[string]any{"draft_id": "draft:redis"}); err == nil {
		t.Fatal("delete draft remove error unexpectedly succeeded")
	}
	mcpDraftRemove = oldMCPDraftRemove
	badLockCfg := cfg
	badLockCfg.LockPath = filepath.Join(t.TempDir(), "lock.json")
	writeFile(t, badLockCfg.LockPath, "{bad")
	if _, err := createVersionDraftForMCP(badLockCfg, "parent-error", "redis", false); err == nil {
		t.Fatal("createVersionDraftForMCP parent metadata error unexpectedly succeeded")
	}
	if _, _, _, err := parentMetadataForMCP(badLockCfg, "redis", filepath.Join(cfg.StoreDir, "redis")); err == nil {
		t.Fatal("parent metadata bad lock unexpectedly succeeded")
	}
	lockCfg := cfg
	if err := library.WriteLock(lockCfg, library.Lock{Skills: map[string]library.LockEntry{"redis": {Source: "repo", Ref: "main", Hash: "sha256:lock"}}}); err != nil {
		t.Fatal(err)
	}
	source, ref, hash, err := parentMetadataForMCP(lockCfg, "redis", filepath.Join(cfg.StoreDir, "redis"))
	if err != nil || source != "repo" || ref != "main" || hash != "sha256:lock" {
		t.Fatalf("parent metadata lock = %q %q %q %v", source, ref, hash, err)
	}
	if _, _, _, err := parentMetadataForMCP(cfg, "missing", filepath.Join(cfg.StoreDir, "missing")); err == nil {
		t.Fatal("parent metadata hash failure unexpectedly succeeded")
	}
	if !matchesSearch("", "anything") || matchesSearch("needle", "hay") {
		t.Fatal("matchesSearch branches failed")
	}
	if got := readOptional(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("readOptional missing = %q", got)
	}
}

func TestServeAndFraming(t *testing.T) {
	cfg := testConfig(t)
	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	input := bytes.NewReader(EncodeForTest(msg))
	var output bytes.Buffer
	if err := Serve(cfg, input, &output); err != nil {
		t.Fatal(err)
	}
	frame, err := readMessage(bufio.NewReader(&output))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), "tools") {
		t.Fatalf("frame = %s", frame)
	}

	lineInput := bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"))
	line, err := readMessage(lineInput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), "initialize") {
		t.Fatalf("line = %s", line)
	}
	var lineOutput bytes.Buffer
	if err := Serve(cfg, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n"), &lineOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lineOutput.String(), "{") || !strings.HasSuffix(lineOutput.String(), "\n") {
		t.Fatalf("line-framed response = %q", lineOutput.String())
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader("Bad: header\r\n\r\n{}"))); err == nil {
		t.Fatal("missing content length unexpectedly succeeded")
	}
	if err := writeMessage(errWriter{}, response{}); err == nil {
		t.Fatal("writeMessage unexpectedly succeeded")
	}
	if _, err := DecodeBase64ForTest("bad"); err == nil {
		t.Fatal("bad base64 unexpectedly decoded")
	}
	if err := writeMessage(io.Discard, response{Result: math.Inf(1)}); err == nil {
		t.Fatal("writeMessage marshal error unexpectedly succeeded")
	}

	var notificationOut bytes.Buffer
	notification := map[string]any{"jsonrpc": "2.0", "method": "initialized"}
	if err := Serve(cfg, bytes.NewReader(EncodeForTest(notification)), &notificationOut); err != nil {
		t.Fatal(err)
	}
	if notificationOut.Len() != 0 {
		t.Fatalf("notification output = %q", notificationOut.String())
	}
	if err := Serve(cfg, strings.NewReader("Bad: header\r\n\r\n"), io.Discard); err == nil {
		t.Fatal("bad header unexpectedly served")
	}
	if err := Serve(cfg, bytes.NewReader([]byte("Content-Length: 4\r\n\r\n{bad")), errWriter{}); err == nil {
		t.Fatal("parse error write failure unexpectedly succeeded")
	}
	if err := Serve(cfg, bytes.NewReader(EncodeForTest(msg)), errWriter{}); err == nil {
		t.Fatal("valid response write failure unexpectedly succeeded")
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader("Content-Length: 4\r\n"))); err == nil {
		t.Fatal("header EOF unexpectedly succeeded")
	}
}

func TestServeParseError(t *testing.T) {
	var output bytes.Buffer
	input := bytes.NewReader([]byte("Content-Length: 4\r\n\r\n{bad"))
	if err := Serve(testConfig(t), input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "-32700") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestToolArgumentErrors(t *testing.T) {
	cfg := testConfig(t)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"read_skill", map[string]any{}},
		{"read_skill", map[string]any{"skill_id": "missing", "rendered": false}},
		{"list_skill_files", map[string]any{}},
		{"list_skill_files", map[string]any{"skill_id": "missing"}},
		{"list_skill_files", map[string]any{"skill_id": "missing", "rendered": false}},
		{"read_skill_file", map[string]any{}},
		{"read_skill_file", map[string]any{"skill_id": "redis"}},
		{"read_skill_file", map[string]any{"skill_id": "redis", "path": "../bad", "rendered": false}},
		{"read_skill_file", map[string]any{"skill_id": "redis", "path": "missing.md", "rendered": false}},
		{"render_skill", map[string]any{}},
		{"upsert_skill_overlay_file", map[string]any{"skill_id": "redis", "path": "../bad"}},
		{"delete_skill_overlay_file", map[string]any{"skill_id": "redis", "path": "missing.md"}},
	} {
		if _, err := callTool(cfg, tc.name, tc.args); err == nil {
			t.Fatalf("%s unexpectedly succeeded", tc.name)
		}
	}
	if _, err := readMessage(bufio.NewReader(bytes.NewReader(nil))); !errors.Is(err, io.EOF) {
		t.Fatalf("empty read err = %v", err)
	}
	if _, err := callTool(config.Config{LockPath: filepath.Join(t.TempDir(), "missing", "lock.json")}, "sync_skills", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	emptyCfg := config.Config{StoreDir: filepath.Join(t.TempDir(), "empty"), RenderedDir: filepath.Join(t.TempDir(), "rendered"), LockPath: filepath.Join(t.TempDir(), "lock.json")}
	emptySync, err := callTool(emptyCfg, "sync_skills", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(emptySync), `"warnings"`) || !strings.Contains(mustJSON(emptySync), `"rendered":[]`) {
		t.Fatalf("emptySync = %#v", emptySync)
	}
	if len(nonNilInstalled([]library.InstalledSkill{{StoreName: "x"}})) != 1 || len(nonNilRendered([]render.Result{{Skill: "x"}})) != 1 {
		t.Fatal("nonNil helper passthrough failed")
	}
	badLock := filepath.Join(t.TempDir(), "lock.json")
	writeFile(t, badLock, "{bad")
	if _, err := callTool(config.Config{LockPath: badLock}, "sync_skills", map[string]any{}); err == nil {
		t.Fatal("bad lock sync unexpectedly succeeded")
	}
	truncated, err := callTool(cfg, "read_skill_file", map[string]any{"skill_id": "redis", "path": "references/guide.md", "rendered": false, "max_bytes": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(truncated), `"content":"Gu"`) {
		t.Fatalf("truncated = %#v", truncated)
	}
	if _, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{"skill_name": "Redis", "path": "SKILL.md", "content": "x"}); err != nil {
		t.Fatal(err)
	}
	badSkill := filepath.Join(cfg.StoreDir, "bad")
	writeFile(t, filepath.Join(badSkill, "README.md"), "no entrypoint")
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "bad", "rendered": false}); err == nil {
		t.Fatal("read skill without SKILL.md unexpectedly succeeded")
	}
	oldManifest := fileManifest
	fileManifest = func(string, string, bool, bool, int) ([]map[string]any, error) {
		return nil, errors.New("manifest failed")
	}
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "rendered": false, "include_files": true}); err == nil {
		t.Fatal("read skill manifest error unexpectedly succeeded")
	}
	fileManifest = oldManifest

	stringResult := toolResult("plain", false)
	if !strings.Contains(mustJSON(stringResult), "plain") {
		t.Fatalf("string result = %#v", stringResult)
	}
	jsonResult := toolResult(map[string]any{"answer": 1}, false)
	if !strings.Contains(mustJSON(jsonResult), "answer") {
		t.Fatalf("json result = %#v", jsonResult)
	}
	if intArg(map[string]any{"n": 3}, "n", 0) != 3 || intArg(map[string]any{"n": "4"}, "n", 0) != 4 || intArg(map[string]any{"n": "bad"}, "n", 5) != 5 {
		t.Fatal("intArg variants failed")
	}
	if boolArg(map[string]any{"b": "bad"}, "b", true) != true {
		t.Fatal("boolArg fallback failed")
	}
	if err := rejectManagedPromotionDestination(config.Config{}, filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatal(err)
	}
	if isInsidePath(filepath.Join(t.TempDir(), "child"), t.TempDir()) {
		t.Fatal("unrelated inside path reported true")
	}
	if typ, parent := requestedDraftShape(cfg, "redis", "", false); typ != draft.TypeVersion || parent != "redis" {
		t.Fatalf("implicit requested shape = %s %s", typ, parent)
	}
	if typ, parent := requestedDraftShape(config.Config{StoreDir: filepath.Join(t.TempDir(), "store")}, "fresh", "", true); typ != draft.TypeVersion || parent != "fresh" {
		t.Fatalf("rendered requested shape = %s %s", typ, parent)
	}
	if draftMatchesRequest(draft.Draft{Metadata: draft.Metadata{Type: draft.TypeVersion, ParentSkill: "redis"}}, draft.TypeVersion, "other") {
		t.Fatal("parent mismatch unexpectedly matched")
	}
	if requestedParentSuffix("") != "" {
		t.Fatal("empty parent suffix failed")
	}
	badStore := filepath.Join(t.TempDir(), "store-file")
	writeFile(t, badStore, "file")
	if _, err := syncSkillsTool(config.Config{StoreDir: badStore, RenderedDir: filepath.Join(t.TempDir(), "rendered"), LockPath: filepath.Join(t.TempDir(), "lock.json")}, map[string]any{}); err == nil {
		t.Fatal("sync render error unexpectedly succeeded")
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
