package overlay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/config"
)

func overlayConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{
		OverlaysDir: filepath.Join(root, "overlays"),
	}
}

func TestAddListChainAndMarshal(t *testing.T) {
	cfg := overlayConfig(t)
	global, err := Add(cfg, "redis", "")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := Add(cfg, "redis", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if global != filepath.Join(cfg.OverlaysDir, "redis") {
		t.Fatalf("global = %s", global)
	}
	if agent != filepath.Join(cfg.OverlaysDir, "redis", "agents", "codex") {
		t.Fatalf("agent = %s", agent)
	}
	rows, err := List(cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if _, err := Add(cfg, "alpha", ""); err != nil {
		t.Fatal(err)
	}
	rows, err = List(cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Skill != "alpha" {
		t.Fatalf("sorted rows = %#v", rows)
	}
	filtered, err := List(cfg, "redis", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Agent != "codex" {
		t.Fatalf("filtered = %#v", filtered)
	}
	chain := Chain(cfg, "redis", "codex")
	if len(chain) != 2 {
		t.Fatalf("chain = %#v", chain)
	}
	data := MarshalSummaries(filtered)
	var decoded []Summary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestListMissingRoot(t *testing.T) {
	cfg := overlayConfig(t)
	rows, err := List(cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if rows != nil {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestListFiltersAndNonDirs(t *testing.T) {
	cfg := overlayConfig(t)
	if err := os.MkdirAll(cfg.OverlaysDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OverlaysDir, "not-a-dir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, "redis", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OverlaysDir, "redis", "agents", "not-a-dir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, "other", "claude"); err != nil {
		t.Fatal(err)
	}
	rows, err := List(cfg, "redis", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestListGlobalOnlyAndFileCountErrors(t *testing.T) {
	cfg := overlayConfig(t)
	if _, err := Add(cfg, "redis", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := List(cfg, "redis", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Agent != "" {
		t.Fatalf("rows = %#v", rows)
	}
	if _, err := fileCount("bad\x00path", false); err == nil {
		t.Fatal("expected fileCount path error")
	}

	cfg = overlayConfig(t)
	if err := os.MkdirAll(filepath.Join(cfg.OverlaysDir, "redis"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OverlaysDir, "redis", "agents"), []byte("file-not-dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(cfg, "redis", ""); err == nil {
		t.Fatal("expected agents read error")
	}

	cfg = overlayConfig(t)
	skillDir := filepath.Join(cfg.OverlaysDir, "redis")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skillDir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(skillDir, 0o755)
	if _, err := List(cfg, "redis", ""); err == nil {
		t.Fatal("expected skill fileCount error")
	}

	cfg = overlayConfig(t)
	agentDir := filepath.Join(cfg.OverlaysDir, "redis", "agents", "codex")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OverlaysDir, "redis", "SKILL.append.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agentDir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(agentDir, 0o755)
	if _, err := List(cfg, "redis", ""); err == nil {
		t.Fatal("expected agent fileCount error")
	}

	cfg = overlayConfig(t)
	if _, err := Add(cfg, "redis", "codex"); err != nil {
		t.Fatal(err)
	}
	oldCountFiles := countFiles
	countFiles = func(root string, skipAgents bool) (int, error) {
		if !skipAgents {
			return 0, errors.New("agent count failed")
		}
		return fileCount(root, skipAgents)
	}
	defer func() { countFiles = oldCountFiles }()
	if _, err := List(cfg, "redis", ""); err == nil {
		t.Fatal("expected injected agent count error")
	}
}

func TestOverlayErrorBranches(t *testing.T) {
	cfg := overlayConfig(t)
	if err := os.WriteFile(cfg.OverlaysDir, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, "redis", ""); err == nil {
		t.Fatal("expected Add mkdir error")
	}
	if _, err := List(cfg, "", ""); err == nil {
		t.Fatal("expected List file root error")
	}
	if count, err := fileCount(filepath.Join(filepath.Dir(cfg.OverlaysDir), "missing"), false); err != nil || count != 0 {
		t.Fatalf("missing fileCount = %d %v", count, err)
	}

	cfg = overlayConfig(t)
	dir := filepath.Join(cfg.OverlaysDir, "redis")
	for _, sub := range []string{"append", "files", "replace", "patches", "templates"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if _, err := Add(cfg, "redis", ""); err == nil {
		t.Fatal("expected append file write error")
	}
}

func TestAddDoesNotOverwriteExistingAppend(t *testing.T) {
	cfg := overlayConfig(t)
	path, err := Add(cfg, "redis", "")
	if err != nil {
		t.Fatal(err)
	}
	appendPath := filepath.Join(path, "SKILL.append.md")
	if err := os.WriteFile(appendPath, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, "redis", ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(appendPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom" {
		t.Fatalf("append overwritten: %q", data)
	}
}

func TestUpsertDeleteOverlayFiles(t *testing.T) {
	cfg := overlayConfig(t)
	cases := []struct {
		operation string
		path      string
		want      string
	}{
		{"append", "SKILL.md", filepath.Join("append", "SKILL.md")},
		{"root_append", "SKILL.md", "SKILL.append.md"},
		{"files", "references/local.md", filepath.Join("files", "references", "local.md")},
		{"replace", "metadata.json", filepath.Join("replace", "metadata.json")},
		{"template", "SKILL.md.j2", filepath.Join("templates", "SKILL.md.j2")},
		{"patch", "metadata.patch.json", filepath.Join("patches", "metadata.patch.json")},
	}
	for _, tc := range cases {
		path, err := UpsertFile(cfg, WriteOptions{Skill: "redis", Agent: "codex", Operation: tc.operation, Path: tc.path, Content: "x"})
		if err != nil {
			t.Fatalf("%s upsert error: %v", tc.operation, err)
		}
		if !filepath.IsAbs(path) || filepath.ToSlash(path) == "" || !strings.HasSuffix(path, filepath.ToSlash(tc.want)) {
			t.Fatalf("%s path = %s", tc.operation, path)
		}
		deleted, err := DeleteFile(cfg, WriteOptions{Skill: "redis", Agent: "codex", Operation: tc.operation, Path: tc.path})
		if err != nil {
			t.Fatalf("%s delete error: %v", tc.operation, err)
		}
		if deleted != path {
			t.Fatalf("deleted = %s want %s", deleted, path)
		}
	}
}

func TestUpsertOverlayValidation(t *testing.T) {
	cfg := overlayConfig(t)
	for _, opts := range []WriteOptions{
		{Path: "SKILL.md"},
		{Skill: "redis"},
		{Skill: "redis", Operation: "unknown", Path: "SKILL.md"},
		{Skill: "redis", Operation: "template", Path: "SKILL.md"},
		{Skill: "redis", Operation: "patch", Path: "metadata.txt"},
		{Skill: "redis", Operation: "append", Path: "../SKILL.md"},
		{Skill: "redis", Operation: "append", Path: "/tmp/SKILL.md"},
		{Skill: "redis", Operation: "append", Path: "SKILL.md", Base64Content: "bad"},
	} {
		if _, err := UpsertFile(cfg, opts); err == nil {
			t.Fatalf("%#v unexpectedly succeeded", opts)
		}
	}

	if path, err := UpsertFile(cfg, WriteOptions{Skill: "redis", Path: "SKILL.md", Content: "default"}); err != nil {
		t.Fatal(err)
	} else if !strings.HasSuffix(path, filepath.Join("append", "SKILL.md")) {
		t.Fatalf("default operation path = %s", path)
	}
	if _, err := DeleteFile(cfg, WriteOptions{Skill: "redis"}); err == nil {
		t.Fatal("delete with missing path unexpectedly succeeded")
	}
	if _, err := DeleteFile(cfg, WriteOptions{Skill: "redis", Path: "missing.md"}); err == nil {
		t.Fatal("delete missing file unexpectedly succeeded")
	}

	blockerCfg := overlayConfig(t)
	if err := os.WriteFile(blockerCfg.OverlaysDir, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertFile(blockerCfg, WriteOptions{Skill: "redis", Path: "SKILL.md", Content: "x"}); err == nil {
		t.Fatal("upsert with file overlay root unexpectedly succeeded")
	}
}
