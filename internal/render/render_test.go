package render

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/config"
	"upskill/internal/overlay"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{
		Home:        filepath.Join(root, "home"),
		StoreDir:    filepath.Join(root, "home", "store"),
		OverlaysDir: filepath.Join(root, "home", "overlays"),
		RenderedDir: filepath.Join(root, "home", "rendered"),
		ConfigPath:  filepath.Join(root, "home", "config.json"),
		SkillsBin:   "skills",
		AgentOutputs: []config.AgentOutput{
			{Name: "claude", Path: filepath.Join(root, "claude-skills")},
			{Name: "codex", Path: filepath.Join(root, "codex-skills")},
		},
	}
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

func TestRenderAppliesGlobalAndAgentOverlaysAndCopies(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "# Redis\n\nBase.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "SKILL.append.md"), "Global.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "agents", "codex", "SKILL.append.md"), "Codex.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "agents", "claude", "SKILL.append.md"), "Claude.\n")

	results, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	codex := readFile(t, filepath.Join(cfg.AgentOutputs[1].Path, "redis", "SKILL.md"))
	if !strings.Contains(codex, "Base.") || !strings.Contains(codex, "Global.") || !strings.Contains(codex, "Codex.") {
		t.Fatalf("codex render missing expected content:\n%s", codex)
	}
	if strings.Contains(codex, "Claude.") {
		t.Fatalf("codex render included claude overlay:\n%s", codex)
	}

	markerData := readFile(t, filepath.Join(cfg.AgentOutputs[1].Path, "redis", MarkerFile))
	var marker map[string]any
	if err := json.Unmarshal([]byte(markerData), &marker); err != nil {
		t.Fatal(err)
	}
	if marker["mode"] != "with-overlays" || marker["agent"] != "codex" {
		t.Fatalf("unexpected marker: %#v", marker)
	}
	if marker["base_hash"] == "" || marker["overlay_hash"] == "" {
		t.Fatalf("marker missing hashes: %#v", marker)
	}
}

func TestRenderInfersFrontmatterForBareMarkdownSkill(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "new-sesons-groceries", "SKILL.md"), `# New Seasons Market

Store-specific notes for grocery runs at New Seasons Market on Instacart.

## Setup

- Store slug: `+"`new-seasons-market`"+`
`)

	results, err := (Renderer{Config: cfg}).Render(Options{Skill: "new-sesons-groceries", Agent: "codex", Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].CopiedTo) != 1 {
		t.Fatalf("results = %#v", results)
	}
	rendered := readFile(t, filepath.Join(cfg.AgentOutputs[1].Path, "new-sesons-groceries", "SKILL.md"))
	for _, want := range []string{
		"---\nname: \"new-sesons-groceries\"",
		"description: \"Store-specific notes for grocery runs at New Seasons Market on Instacart.\"",
		"# New Seasons Market",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderSkipsUnchangedInputs(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "SKILL.append.md"), "Overlay.\n")
	renderer := Renderer{Config: cfg}

	results, err := renderer.Render(Options{Skill: "redis", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("first render should not skip: %#v", results)
	}
	markerPath := filepath.Join(cfg.RenderedDir, "codex", "redis", MarkerFile)
	firstMarker := readFile(t, markerPath)

	results, err = renderer.Render(Options{Skill: "redis", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("second render should skip: %#v", results)
	}
	if secondMarker := readFile(t, markerPath); secondMarker != firstMarker {
		t.Fatalf("skipped render rewrote marker:\nfirst=%s\nsecond=%s", firstMarker, secondMarker)
	}
	var oldMarker map[string]any
	if err := json.Unmarshal([]byte(firstMarker), &oldMarker); err != nil {
		t.Fatal(err)
	}
	oldMarker["schema_version"] = float64(1)
	oldMarkerData, err := json.MarshalIndent(oldMarker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, markerPath, string(oldMarkerData)+"\n")
	results, err = renderer.Render(Options{Skill: "redis", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("old marker schema should render: %#v", results)
	}

	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "SKILL.append.md"), "Changed overlay.\n")
	results, err = renderer.Render(Options{Skill: "redis", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("overlay change should render: %#v", results)
	}
	if rendered := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "SKILL.md")); !strings.Contains(rendered, "Changed overlay.") {
		t.Fatalf("changed overlay not rendered:\n%s", rendered)
	}

	results, err = renderer.Render(Options{Skill: "redis", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("post-change render should skip: %#v", results)
	}
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Changed base.\n")
	results, err = renderer.Render(Options{Skill: "redis", Agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("base change should render: %#v", results)
	}
	if rendered := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "SKILL.md")); !strings.Contains(rendered, "Changed base.") {
		t.Fatalf("changed base not rendered:\n%s", rendered)
	}
}

func TestFilesRefuseOverwriteAndReplaceOverwrites(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "references", "base.md"), "base\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "files", "references", "new.md"), "new\n")

	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	newFile := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "references", "new.md"))
	if newFile != "new\n" {
		t.Fatalf("files/ copy = %q", newFile)
	}

	if err := os.RemoveAll(filepath.Join(cfg.OverlaysDir, "redis", "files")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "files", "references", "base.md"), "new\n")
	_, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"})
	if err == nil {
		t.Fatal("expected files/ overwrite error")
	}

	if err := os.RemoveAll(filepath.Join(cfg.OverlaysDir, "redis", "files")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "replace", "references", "base.md"), "replaced\n")

	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "references", "base.md"))
	if got != "replaced\n" {
		t.Fatalf("got %q, want replaced", got)
	}
}

func TestAppendDirectoryAndMissingTarget(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "append", "references", "extra.md"), "Created by append.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "append", "SKILL.md"), "Append dir.\n")

	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	skill := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "SKILL.md"))
	if !strings.Contains(skill, "Base.") || !strings.Contains(skill, "Append dir.") {
		t.Fatalf("append output = %q", skill)
	}
	extra := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "references", "extra.md"))
	if extra != "Created by append.\n" {
		t.Fatalf("append-created file = %q", extra)
	}
}

func TestJSONPatchOnlyAppliesToJSONFiles(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "metadata.json"), `{"tags":["redis"]}`+"\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "patches", "metadata.patch.json"), `{
  "target": "metadata.json",
  "operations": [{"op": "add", "path": "/tags/-", "value": "codex"}]
}`)

	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "metadata.json"))
	if !strings.Contains(got, `"codex"`) {
		t.Fatalf("patch did not apply:\n%s", got)
	}

	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "patches", "bad.patch.json"), `{
  "target": "SKILL.md",
  "operations": [{"op": "add", "path": "/x", "value": "bad"}]
}`)
	_, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"})
	if err == nil || !strings.Contains(err.Error(), ".json") {
		t.Fatalf("expected non-json target rejection, got %v", err)
	}
}

func TestJSONPatchCreatesNewTargetAndRejectsBadSpecs(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "patches", "new.patch.json"), `{
  "target": "metadata.json",
  "operations": [{"op": "add", "path": "/owner", "value": "local"}]
}`)

	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	metadata := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "metadata.json"))
	if !strings.Contains(metadata, `"owner": "local"`) {
		t.Fatalf("new patch target = %q", metadata)
	}

	cases := map[string]string{
		"bad-json.patch.json":        `{bad`,
		"bad-operations.patch.json":  `{"target":"metadata.json","operations": {}}`,
		"bad-apply.patch.json":       `{"target":"metadata.json","operations": [{"op":"replace","path":"/missing","value":true}]}`,
		"bad-traversal.patch.json":   `{"target":"../metadata.json","operations": [{"op":"add","path":"/x","value":true}]}`,
		"bad-read-target.patch.json": `{"target":"directory.json","operations": [{"op":"add","path":"/x","value":true}]}`,
	}
	for name, spec := range cases {
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		if name == "bad-read-target.patch.json" {
			if err := os.MkdirAll(filepath.Join(cfg.StoreDir, "redis", "directory.json"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "patches", name), spec)
		if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
			t.Fatalf("%s unexpectedly succeeded", name)
		}
	}
}

func TestJinjaParentLayeringWithSuper(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md.j2"), `# Redis

{% block guidance %}Base guidance.{% endblock %}
`)
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "templates", "SKILL.md.j2"), `{% extends "parent/SKILL.md.j2" %}
{% block guidance %}
{{ super() }}
Global guidance.
{% endblock %}
`)
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "agents", "codex", "templates", "SKILL.md.j2"), `{% extends "parent/SKILL.md.j2" %}
{% block guidance %}
{{ super() }}
Codex guidance.
{% endblock %}
`)

	text, err := (Renderer{Config: cfg}).Print("redis", "codex", "SKILL.md", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Base guidance.", "Global guidance.", "Codex guidance."} {
		if !strings.Contains(text, want) {
			t.Fatalf("render missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.RenderedDir, "codex", "redis", "SKILL.md.j2")); !os.IsNotExist(err) {
		t.Fatalf(".j2 source should not be copied to rendered output")
	}
}

func TestOverlayListAddAndDiff(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")

	if _, err := overlay.Add(cfg, "redis", "codex"); err != nil {
		t.Fatal(err)
	}
	rows, err := overlay.List(cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Skill != "redis" || rows[0].Agent != "codex" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "SKILL.append.md"), "Overlay.\n")
	diff, err := (Renderer{Config: cfg}).Diff("redis", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+Overlay.") {
		t.Fatalf("diff missing overlay:\n%s", diff)
	}
}

func TestRenderWithoutAgentOutputsUsesGenericOutputAndDiscoversSkills(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentOutputs = nil
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Redis.\n")
	writeFile(t, filepath.Join(cfg.StoreDir, "other", "SKILL.md"), "Other.\n")

	results, err := (Renderer{Config: cfg}).Render(Options{Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	if results[0].CopiedTo != nil {
		t.Fatalf("generic render should not copy: %#v", results[0].CopiedTo)
	}
	if _, err := os.Stat(filepath.Join(cfg.RenderedDir, "generic", "redis", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestBaseOnlyRenderSkipsOverlaysAndCopy(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "SKILL.append.md"), "Overlay.\n")

	results, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex", Copy: true, BaseOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Mode != "base-only" || len(results[0].CopiedTo) != 0 {
		t.Fatalf("result = %#v", results[0])
	}
	text := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "SKILL.md"))
	if strings.Contains(text, "Overlay.") {
		t.Fatalf("base-only included overlay: %s", text)
	}
}

func TestJinjaHelpers(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "references", "base.md"), "base helper")
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md.j2"), `{{ base_file("references/base.md") }}
{{ skill }} {{ agent }}
`)
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "templates", "EXTRA.md.j2"), `{{ current_file("SKILL.md") }}`)

	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "SKILL.md"))
	if !strings.Contains(text, "base helper") || !strings.Contains(text, "redis codex") {
		t.Fatalf("helper output = %q", text)
	}
	extra := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "EXTRA.md"))
	if !strings.Contains(extra, "base helper") {
		t.Fatalf("current_file helper output = %q", extra)
	}
}

func TestJinjaErrorBranchesAndIgnoredTemplateFiles(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "templates", "README.md"), "ignored")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "templates", "EXTRA.md.j2"), `{{ skill }} {{ agent }}`)

	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.RenderedDir, "codex", "redis", "README.md")); !os.IsNotExist(err) {
		t.Fatalf("non-j2 template helper should be ignored")
	}
	extra := readFile(t, filepath.Join(cfg.RenderedDir, "codex", "redis", "EXTRA.md"))
	if strings.TrimSpace(extra) != "redis codex" {
		t.Fatalf("new overlay template output = %q", extra)
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md.j2"), `{% if`)
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
		t.Fatal("expected bad base template error")
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md.j2"), `{{ base_file("../outside") }}`)
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
		t.Fatal("expected template execution error")
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "templates", "BROKEN.md.j2"), `{% if`)
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
		t.Fatal("expected bad overlay template error")
	}
}

func TestTemplateOverlayRejectsJSONVariableMap(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nBase.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "templates", "SKILL.md.j2"), `{
  "tone": "casual"
}`)

	_, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"})
	if err == nil {
		t.Fatal("JSON template overlay unexpectedly rendered")
	}
	if !strings.Contains(err.Error(), "JSON variable map") || !strings.Contains(err.Error(), "SKILL.md.j2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrintMissingFileAndMissingSkillError(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	renderer := Renderer{Config: cfg}
	if _, err := renderer.Print("missing", "", "SKILL.md", false); err == nil {
		t.Fatal("expected print missing skill error")
	}
	if _, err := renderer.Print("redis", "", "../outside.md", false); err == nil {
		t.Fatal("expected print traversal error")
	}
	if _, err := renderer.Print("redis", "", "missing.md", false); err == nil {
		t.Fatal("expected missing file error")
	}
	if _, err := renderer.Render(Options{Skill: "missing"}); err == nil {
		t.Fatal("expected missing skill error")
	}
	if _, err := renderer.Diff("missing", ""); err == nil {
		t.Fatal("expected diff missing skill error")
	}
}

func TestCopyAgentOutputExistingFileError(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.AgentOutputs[1].Path, "redis", "old.md"), "old")
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex", Copy: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.AgentOutputs[1].Path, "redis", "old.md")); !os.IsNotExist(err) {
		t.Fatal("existing destination directory should be replaced")
	}
	results, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex", Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Skipped || len(results[0].CopiedTo) != 0 {
		t.Fatalf("unchanged copied output should skip render and copy: %#v", results)
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.AgentOutputs[1].Path, "redis"), "file-not-dir")

	_, err = (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex", Copy: true})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected destination file error, got %v", err)
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, cfg.AgentOutputs[1].Path, "file-not-dir")
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex", Copy: true}); err == nil {
		t.Fatal("expected destination stat error")
	}

	if _, _, err := copyToAgentOutput("redis", filepath.Join(t.TempDir(), "missing-rendered"), config.AgentOutput{Path: t.TempDir()}); err == nil {
		t.Fatal("expected copyToAgentOutput missing rendered error")
	}
}

func TestRenderPackageErrorBranches(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, cfg.StoreDir, "file-not-dir")
	if _, err := (Renderer{Config: cfg}).Render(Options{}); err == nil {
		t.Fatal("expected discover skills error")
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis"), "file-not-dir")
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis"}); err == nil {
		t.Fatal("expected base not directory error")
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	cfg.AgentOutputs = nil
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "missing"}); err == nil {
		t.Fatal("expected generic render error")
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.RenderedDir, "codex"), "file-not-dir")
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
		t.Fatal("expected tmp copy error")
	}

	if symlinkErr := os.Symlink("missing.md", filepath.Join(cfg.StoreDir, "redis", "broken.md")); symlinkErr == nil {
		cfg.RenderedDir = filepath.Join(t.TempDir(), "rendered")
		if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
			t.Fatal("expected copy broken symlink error")
		}
	} else if !errors.Is(symlinkErr, os.ErrPermission) {
		t.Fatal(symlinkErr)
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	if symlinkErr := os.Symlink("missing.md.j2", filepath.Join(cfg.StoreDir, "redis", "broken.md.j2")); symlinkErr == nil {
		if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
			t.Fatal("expected renderOne template state error")
		}
	} else if !errors.Is(symlinkErr, os.ErrPermission) {
		t.Fatal(symlinkErr)
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(cfg.OverlaysDir, "redis", "files", MarkerFile, "child"), "marker dir")
	if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
		t.Fatal("expected marker write error")
	}

	cfg = testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	finalDir := filepath.Join(cfg.RenderedDir, "codex", "redis")
	lockedDir := filepath.Join(finalDir, "locked")
	writeFile(t, filepath.Join(lockedDir, "file.md"), "locked")
	if err := os.Chmod(lockedDir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(lockedDir, 0o755)
	_, _ = (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"})
}

func TestTemplateStateHelpersAndWalkErrors(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	workDir := filepath.Join(cfg.RenderedDir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state, err := newTemplateState(filepath.Join(cfg.StoreDir, "redis"), workDir, "redis", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.baseFile("../outside"); err == nil {
		t.Fatal("expected baseFile traversal error")
	}
	if _, err := state.baseFile("missing.md"); err == nil {
		t.Fatal("expected baseFile missing error")
	}
	if _, err := state.currentFile("../outside"); err == nil {
		t.Fatal("expected currentFile traversal error")
	}
	if _, err := state.currentFile("missing.md"); err == nil {
		t.Fatal("expected currentFile missing error")
	}
	if err := state.appendTo("../outside", filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); err == nil {
		t.Fatal("expected appendTo traversal error")
	}
	if err := state.appendTo("SKILL.md", filepath.Join(cfg.StoreDir, "redis", "missing.md")); err == nil {
		t.Fatal("expected appendTo missing source error")
	}
	if err := os.MkdirAll(filepath.Join(workDir, "directory.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.appendTo("directory.md", filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); err == nil {
		t.Fatal("expected appendTo read target error")
	}
	writeFile(t, filepath.Join(workDir, "blocker"), "block")
	if err := state.appendTo("blocker/child.md", filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); err == nil {
		t.Fatal("expected appendTo mkdir error")
	}
	if err := walkExistingFiles(filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), func(path, rel string) error {
		return nil
	}); err == nil {
		t.Fatal("expected walkExistingFiles file root error")
	}
	if _, err := newTemplateState(filepath.Join(cfg.StoreDir, "missing"), workDir, "redis", "codex"); err == nil {
		t.Fatal("expected newTemplateState missing base error")
	}
	if symlinkErr := os.Symlink("missing.md", filepath.Join(cfg.StoreDir, "redis", "broken.md.j2")); symlinkErr == nil {
		if _, err := newTemplateState(filepath.Join(cfg.StoreDir, "redis"), workDir, "redis", "codex"); err == nil {
			t.Fatal("expected newTemplateState broken symlink error")
		}
	} else if !errors.Is(symlinkErr, os.ErrPermission) {
		t.Fatal(symlinkErr)
	}
}

func TestPatchAndOverlayHelperErrors(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
	workDir := filepath.Join(cfg.RenderedDir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state, err := newTemplateState(filepath.Join(cfg.StoreDir, "redis"), workDir, "redis", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.applyOverlay(filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); err == nil {
		t.Fatal("expected applyOverlay file root error")
	}
	filesDir := filepath.Join(cfg.OverlaysDir, "stat-error-files")
	writeFile(t, filepath.Join(filesDir, "new.md"), "new")
	badWorkState := *state
	badWorkState.workDir = "bad\x00work"
	if err := badWorkState.copyNewFiles(filesDir); err == nil {
		t.Fatal("expected copyNewFiles stat error")
	}
	overlayDir := filepath.Join(cfg.OverlaysDir, "bad-replace")
	writeFile(t, filepath.Join(overlayDir, "replace"), "file-not-dir")
	if err := state.applyOverlay(overlayDir); err == nil {
		t.Fatal("expected applyOverlay replace error")
	}
	overlayDir = filepath.Join(cfg.OverlaysDir, "bad-append")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(overlayDir, "append"), "file-not-dir")
	if err := state.applyOverlay(overlayDir); err == nil {
		t.Fatal("expected applyOverlay append error")
	}
	if err := state.appendFiles(filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); err == nil {
		t.Fatal("expected appendFiles read dir error")
	}
	if err := state.appendFiles(filepath.Join(cfg.StoreDir, "missing")); err != nil {
		t.Fatalf("missing append dir should be ignored: %v", err)
	}

	patchDir := filepath.Join(cfg.OverlaysDir, "patches")
	writeFile(t, filepath.Join(patchDir, "blocked.patch.json"), `{
  "target": "blocker/metadata.json",
  "operations": [{"op":"add","path":"/x","value":true}]
}`)
	writeFile(t, filepath.Join(workDir, "blocker"), "block")
	if err := state.applyPatches(patchDir); err == nil {
		t.Fatal("expected patch mkdir error")
	}
	brokenPatchDir := filepath.Join(cfg.OverlaysDir, "broken-patch")
	if err := os.MkdirAll(brokenPatchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if symlinkErr := os.Symlink("missing.patch.json", filepath.Join(brokenPatchDir, "BROKEN.patch.json")); symlinkErr == nil {
		if err := state.applyPatches(brokenPatchDir); err == nil {
			t.Fatal("expected patch read error")
		}
	} else if !errors.Is(symlinkErr, os.ErrPermission) {
		t.Fatal(symlinkErr)
	}

	state.contents[templateKey("manual")] = "manual"
	if err := state.renderTemplate(templateKey("manual"), "../outside.md.j2"); err == nil {
		t.Fatal("expected renderTemplate traversal error")
	}
	writeFile(t, filepath.Join(workDir, "blocker-template"), "block")
	if err := state.renderTemplate(templateKey("manual"), "blocker-template/child.md.j2"); err == nil {
		t.Fatal("expected renderTemplate mkdir error")
	}
	if err := os.MkdirAll(filepath.Join(workDir, "directory-output.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.renderTemplate(templateKey("manual"), "directory-output.md.j2"); err == nil {
		t.Fatal("expected renderTemplate write error")
	}

	templatesDir := filepath.Join(cfg.OverlaysDir, "templates-broken-read")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if symlinkErr := os.Symlink("missing.md.j2", filepath.Join(templatesDir, "BROKEN.md.j2")); symlinkErr == nil {
		if err := state.applyTemplates(templatesDir); err == nil {
			t.Fatal("expected applyTemplates broken symlink error")
		}
	} else if !errors.Is(symlinkErr, os.ErrPermission) {
		t.Fatal(symlinkErr)
	}

	walkRoot := filepath.Join(cfg.OverlaysDir, "walk-error")
	if err := os.MkdirAll(filepath.Join(walkRoot, "closed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(walkRoot, "closed"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(walkRoot, "closed"), 0o755)
	_ = walkExistingFiles(walkRoot, func(path, rel string) error { return nil })
}

func TestDiffDirsNoChangesAndErrors(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	rendered := filepath.Join(root, "rendered")
	writeFile(t, filepath.Join(base, "SKILL.md"), "Same.\n")
	writeFile(t, filepath.Join(rendered, "SKILL.md"), "Same.\n")
	diff, err := diffDirs(base, rendered)
	if err != nil {
		t.Fatal(err)
	}
	if diff != "" {
		t.Fatalf("diff = %q", diff)
	}
	if _, err := diffDirs(base, filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected diff missing dir error")
	}
	writeFile(t, filepath.Join(rendered, "ONLY.md"), "Only rendered.\n")
	diff, err = diffDirs(base, rendered)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"diff --git a/ONLY.md b/ONLY.md",
		"--- /dev/null",
		"+++ b/ONLY.md",
		"@@ -0,0 +1 @@",
		"+Only rendered.",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("rendered-only diff missing %q: %q", want, diff)
		}
	}
	if got := overlayHash([]string{filepath.Join(root, "missing")}); got == "" {
		t.Fatal("overlayHash should still return a hash")
	}
}

func TestInjectedFilesystemErrors(t *testing.T) {
	reset := func() {
		mkdirAll = os.MkdirAll
		removeAll = os.RemoveAll
		readDir = os.ReadDir
		renamePath = os.Rename
	}
	defer reset()

	t.Run("render final remove", func(t *testing.T) {
		reset()
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		finalDir := filepath.Join(cfg.RenderedDir, "codex", "redis")
		removeAll = func(path string) error {
			if path == finalDir {
				return errors.New("remove failed")
			}
			return os.RemoveAll(path)
		}
		if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
			t.Fatal("expected final remove error")
		}
	})

	t.Run("render final mkdir", func(t *testing.T) {
		reset()
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		finalParent := filepath.Join(cfg.RenderedDir, "codex")
		mkdirAll = func(path string, perm os.FileMode) error {
			if path == finalParent {
				return errors.New("mkdir failed")
			}
			return os.MkdirAll(path, perm)
		}
		if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
			t.Fatal("expected final mkdir error")
		}
	})

	t.Run("render rename", func(t *testing.T) {
		reset()
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		renamePath = func(oldpath string, newpath string) error {
			return errors.New("rename failed")
		}
		if _, err := (Renderer{Config: cfg}).Render(Options{Skill: "redis", Agent: "codex"}); err == nil {
			t.Fatal("expected rename error")
		}
	})

	t.Run("append mkdir", func(t *testing.T) {
		reset()
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		workDir := filepath.Join(cfg.RenderedDir, "work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		state, err := newTemplateState(filepath.Join(cfg.StoreDir, "redis"), workDir, "redis", "codex")
		if err != nil {
			t.Fatal(err)
		}
		mkdirAll = func(path string, perm os.FileMode) error {
			if strings.Contains(path, "newdir") {
				return errors.New("append mkdir failed")
			}
			return os.MkdirAll(path, perm)
		}
		if err := state.appendTo("newdir/file.md", filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); err == nil {
			t.Fatal("expected append mkdir error")
		}
	})

	t.Run("append readdir", func(t *testing.T) {
		reset()
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		workDir := filepath.Join(cfg.RenderedDir, "work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		state, err := newTemplateState(filepath.Join(cfg.StoreDir, "redis"), workDir, "redis", "codex")
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(cfg.OverlaysDir, "overlay")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		readDir = func(path string) ([]os.DirEntry, error) {
			if path == dir {
				return nil, errors.New("readdir failed")
			}
			return os.ReadDir(path)
		}
		if err := state.appendFiles(dir); err == nil {
			t.Fatal("expected append readdir error")
		}
	})

	t.Run("root append error", func(t *testing.T) {
		reset()
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		workDir := filepath.Join(cfg.RenderedDir, "work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		state, err := newTemplateState(filepath.Join(cfg.StoreDir, "redis"), workDir, "redis", "codex")
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(cfg.OverlaysDir, "overlay")
		writeFile(t, filepath.Join(dir, ".append."), "bad target")
		if err := state.appendFiles(dir); err == nil {
			t.Fatal("expected root append target error")
		}
	})

	t.Run("patch mkdir", func(t *testing.T) {
		reset()
		cfg := testConfig(t)
		writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "Base.\n")
		workDir := filepath.Join(cfg.RenderedDir, "work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		state, err := newTemplateState(filepath.Join(cfg.StoreDir, "redis"), workDir, "redis", "codex")
		if err != nil {
			t.Fatal(err)
		}
		patchDir := filepath.Join(cfg.OverlaysDir, "patches")
		writeFile(t, filepath.Join(patchDir, "metadata.patch.json"), `{
  "target": "newdir/metadata.json",
  "operations": [{"op":"add","path":"/x","value":true}]
}`)
		mkdirAll = func(path string, perm os.FileMode) error {
			if strings.Contains(path, "newdir") {
				return errors.New("patch mkdir failed")
			}
			return os.MkdirAll(path, perm)
		}
		if err := state.applyPatches(patchDir); err == nil {
			t.Fatal("expected patch mkdir error")
		}
	})

	t.Run("copy destination remove", func(t *testing.T) {
		reset()
		root := t.TempDir()
		rendered := filepath.Join(root, "rendered")
		destPath := filepath.Join(root, "dest")
		writeFile(t, filepath.Join(rendered, "SKILL.md"), "Base.\n")
		if err := os.MkdirAll(filepath.Join(destPath, "redis"), 0o755); err != nil {
			t.Fatal(err)
		}
		removeAll = func(path string) error {
			if path == filepath.Join(destPath, "redis") {
				return errors.New("copy remove failed")
			}
			return os.RemoveAll(path)
		}
		if _, _, err := copyToAgentOutput("redis", rendered, config.AgentOutput{Path: destPath}); err == nil {
			t.Fatal("expected copy destination remove error")
		}
	})

	t.Run("copy destination mkdir", func(t *testing.T) {
		reset()
		root := t.TempDir()
		rendered := filepath.Join(root, "rendered")
		destPath := filepath.Join(root, "dest")
		writeFile(t, filepath.Join(rendered, "SKILL.md"), "Base.\n")
		mkdirAll = func(path string, perm os.FileMode) error {
			if path == destPath {
				return errors.New("copy mkdir failed")
			}
			return os.MkdirAll(path, perm)
		}
		if _, _, err := copyToAgentOutput("redis", rendered, config.AgentOutput{Path: destPath}); err == nil {
			t.Fatal("expected copy destination mkdir error")
		}
	})
}

func TestTemplateKeyExistingSlash(t *testing.T) {
	if templateKey("/x") != "/x" {
		t.Fatal("templateKey should keep absolute template key")
	}
}
