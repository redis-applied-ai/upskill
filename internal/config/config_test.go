package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigWithAgentOutputsAndEnvOverrides(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	data, err := json.Marshal(map[string]any{
		"agents": map[string]string{
			"codex": filepath.Join(root, "codex"),
		},
		"drafts_dir":  filepath.Join(root, "drafts"),
		"imports_dir": filepath.Join(root, "imports"),
		"import_dirs": []string{
			filepath.Join(root, "imports"),
			filepath.Join(root, "shared-imports"),
		},
		"lock_path":     filepath.Join(root, "custom-lock.json"),
		"skills_bin":    "skills-custom",
		"source_ignore": []string{"custom-ignore", "overlays", ""},
		"sources": map[string]any{
			"core": map[string]string{"source": "owner/repo", "ref": "main"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
	t.Setenv("UPSKILL_CONFIG", configPath)
	t.Setenv("UPSKILL_STORE_DIR", filepath.Join(root, "store-override"))
	t.Setenv("UPSKILL_SOURCE_IGNORE", "env-ignore,,drafts")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StoreDir != filepath.Join(root, "store-override") {
		t.Fatalf("store dir = %s", cfg.StoreDir)
	}
	if cfg.SkillsBin != "skills-custom" {
		t.Fatalf("skills bin = %s", cfg.SkillsBin)
	}
	if cfg.LockPath != filepath.Join(root, "custom-lock.json") {
		t.Fatalf("lock path = %s", cfg.LockPath)
	}
	if cfg.DraftsDir != filepath.Join(root, "drafts") {
		t.Fatalf("drafts dir = %s", cfg.DraftsDir)
	}
	if cfg.ImportsDir != filepath.Join(root, "imports") {
		t.Fatalf("imports dir = %s", cfg.ImportsDir)
	}
	if len(cfg.ImportDirs) != 2 || cfg.ImportDirs[1] != filepath.Join(root, "shared-imports") {
		t.Fatalf("import dirs = %#v", cfg.ImportDirs)
	}
	if len(cfg.AgentOutputs) != 1 || cfg.AgentOutputs[0].Name != "codex" {
		t.Fatalf("agent outputs = %#v", cfg.AgentOutputs)
	}
	if cfg.Sources["core"].Ref != "main" {
		t.Fatalf("sources = %#v", cfg.Sources)
	}
	if !hasString(cfg.SourceIgnore, ".obsidian") || !hasString(cfg.SourceIgnore, "custom-ignore") || !hasString(cfg.SourceIgnore, "env-ignore") {
		t.Fatalf("source ignore = %#v", cfg.SourceIgnore)
	}
	if countString(cfg.SourceIgnore, "drafts") != 1 {
		t.Fatalf("source ignore did not dedupe defaults/env: %#v", cfg.SourceIgnore)
	}
}

func TestSaveConfig(t *testing.T) {
	root := t.TempDir()
	cfg := Config{
		Home:         filepath.Join(root, "home"),
		StoreDir:     filepath.Join(root, "store"),
			OverlaysDir:  filepath.Join(root, "workspace", "overlays"),
			DraftsDir:    filepath.Join(root, "workspace", "drafts"),
			ImportsDir:   filepath.Join(root, "workspace", "imports"),
			ImportDirs:   []string{filepath.Join(root, "workspace", "imports")},
		RenderedDir:  filepath.Join(root, "rendered"),
		SourceIgnore: []string{".git", "overlays"},
		ConfigPath:   filepath.Join(root, "config.json"),
		LockPath:     filepath.Join(root, "lock.json"),
		SkillsBin:    "skills",
			Sources:      map[string]Source{"local": {Name: "local", Source: filepath.Join(root, "workspace", "source")}},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UPSKILL_HOME", cfg.Home)
	t.Setenv("UPSKILL_CONFIG", cfg.ConfigPath)
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
		if loaded.OverlaysDir != cfg.OverlaysDir || loaded.Sources["local"].Source != cfg.Sources["local"].Source {
		t.Fatalf("loaded = %#v", loaded)
	}
	if !hasString(loaded.SourceIgnore, "overlays") {
		t.Fatalf("source ignore = %#v", loaded.SourceIgnore)
	}

	if err := os.WriteFile(cfg.ConfigPath, []byte(`{"source_ignore": 42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("invalid source_ignore unexpectedly loaded")
	}
	if err := SaveConfig(Config{ConfigPath: root}); err == nil {
		t.Fatal("SaveConfig with directory path unexpectedly succeeded")
	}
}

func TestParseAgentOutputsListAndHelpers(t *testing.T) {
	raw := json.RawMessage(`[
	  {"agent":"claude","skills_dir":"~/claude-skills"},
	  {"name":"codex","path":"~/codex-skills"},
	  {"name":"","path":""}
	]`)
	agentOutputs, err := parseAgentOutputs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(agentOutputs) != 2 {
		t.Fatalf("agent outputs = %#v", agentOutputs)
	}
	if OverlayDir(Config{OverlaysDir: "/tmp/overlays"}, "redis", "codex") != "/tmp/overlays/redis/agents/codex" {
		t.Fatal("OverlayDir agent failed")
	}
	if OverlayDir(Config{OverlaysDir: "/tmp/overlays"}, "redis", "") != "/tmp/overlays/redis" {
		t.Fatal("OverlayDir global failed")
	}
	if DraftDir(Config{DraftsDir: "/tmp/drafts"}, "redis") != "/tmp/drafts/redis" {
		t.Fatal("DraftDir failed")
	}
}

func TestImportDirsAndAllowlist(t *testing.T) {
	root := t.TempDir()
	imports := filepath.Join(root, "imports")
	shared := filepath.Join(root, "shared")
	dirs, err := parseImportDirs(json.RawMessage(`[]`), imports)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != imports {
		t.Fatalf("empty import dirs = %#v", dirs)
	}
	dirs, err = parseImportDirs(json.RawMessage(`["`+shared+`", ""]`), imports)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != shared {
		t.Fatalf("configured import dirs = %#v", dirs)
	}
	if _, err := parseImportDirs(json.RawMessage(`42`), imports); err == nil {
		t.Fatal("expected invalid import dirs error")
	}
	dirs, err = parseImportDirs(nil, "")
	if err != nil || dirs != nil {
		t.Fatalf("nil import dirs = %#v %v", dirs, err)
	}

	cfg := Config{ImportsDir: imports, ImportDirs: []string{imports}}
	allowed, err := ImportPathAllowed(cfg, filepath.Join(imports, "bundle.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if allowed != filepath.Join(imports, "bundle.zip") {
		t.Fatalf("allowed = %s", allowed)
	}
	if _, err := ImportPathAllowed(cfg, filepath.Join(root, "outside.zip")); err == nil {
		t.Fatal("expected outside import dir rejection")
	}
	if _, err := ImportPathAllowed(Config{ImportsDir: imports}, filepath.Join(imports, "x.zip")); err != nil {
		t.Fatalf("fallback import dir rejected: %v", err)
	}
	t.Setenv("HOME", "")
	if _, err := ImportPathAllowed(cfg, "~"); err == nil {
		t.Fatal("expected import path expansion error")
	}
	oldRel := filepathRel
	filepathRel = func(string, string) (string, error) { return "", errors.New("rel failed") }
	t.Cleanup(func() { filepathRel = oldRel })
	if _, err := ImportPathAllowed(cfg, filepath.Join(imports, "bundle.zip")); err == nil {
		t.Fatal("expected rel failure to reject path")
	}
}

func TestDiscoverSkills(t *testing.T) {
	root := t.TempDir()
	if skills, err := DiscoverSkills(filepath.Join(root, "missing")); err != nil || skills != nil {
		t.Fatalf("missing skills = %#v %v", skills, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "store", "redis"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "store", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := DiscoverSkills(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0] != "redis" {
		t.Fatalf("skills = %#v", skills)
	}
}

func hasString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func countString(values []string, needle string) int {
	count := 0
	for _, value := range values {
		if value == needle {
			count++
		}
	}
	return count
}

func TestLoadDefaultsAndWellKnownDetection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("UPSKILL_HOME", filepath.Join(root, ".skills"))
	t.Setenv("UPSKILL_CONFIG", filepath.Join(root, ".skills", "missing.json"))
	if err := os.MkdirAll(filepath.Join(root, ".codex", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkillsBin != "skills" {
		t.Fatalf("skills bin = %s", cfg.SkillsBin)
	}
	if cfg.DraftsDir != filepath.Join(root, ".skills", "drafts") {
		t.Fatalf("drafts dir = %s", cfg.DraftsDir)
	}
	if cfg.ImportsDir != filepath.Join(root, ".skills", "imports") {
		t.Fatalf("imports dir = %s", cfg.ImportsDir)
	}
	if len(cfg.ImportDirs) != 1 || cfg.ImportDirs[0] != cfg.ImportsDir {
		t.Fatalf("import dirs = %#v", cfg.ImportDirs)
	}
	if len(cfg.AgentOutputs) != 1 || cfg.AgentOutputs[0].Name != "codex" {
		t.Fatalf("agent outputs = %#v", cfg.AgentOutputs)
	}
}

func TestParseAgentOutputsRejectsInvalidJSON(t *testing.T) {
	if _, err := parseAgentOutputs(json.RawMessage(`42`)); err == nil {
		t.Fatal("expected invalid agent outputs error")
	}
}

func TestSourcesParsingAndSaving(t *testing.T) {
	object, err := parseSources(json.RawMessage(`{"core":{"source":"owner/repo","ref":"main"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if object["core"].Name != "core" || object["core"].Ref != "main" {
		t.Fatalf("object = %#v", object)
	}
	stringsMap, err := parseSources(json.RawMessage(`{"local":"./skills"}`))
	if err != nil {
		t.Fatal(err)
	}
	if stringsMap["local"].Source != "./skills" {
		t.Fatalf("strings = %#v", stringsMap)
	}
	list, err := parseSources(json.RawMessage(`[{"name":"one","source":"owner/one"},{"name":"","source":""}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list["one"].Source != "owner/one" {
		t.Fatalf("list = %#v", list)
	}
	if _, err := parseSources(json.RawMessage(`42`)); err == nil {
		t.Fatal("expected invalid sources error")
	}

	root := t.TempDir()
	cfg := Config{ConfigPath: filepath.Join(root, "config.json")}
	if err := SaveSources(cfg, map[string]Source{"core": {Name: "core", Source: "owner/repo"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := readRawConfig(cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := parseSources(raw.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if saved["core"].Source != "owner/repo" {
		t.Fatalf("saved = %#v", saved)
	}
	if err := SaveSources(Config{ConfigPath: root}, map[string]Source{}); err == nil {
		t.Fatal("expected SaveSources read error")
	}
	if err := SaveSources(Config{ConfigPath: "\x00"}, map[string]Source{}); err == nil {
		t.Fatal("expected SaveSources path error")
	}
	oldMakeDirAll := makeDirAll
	makeDirAll = func(string, os.FileMode) error { return errors.New("mkdir failed") }
	if err := SaveSources(Config{ConfigPath: filepath.Join(root, "mkdir-error.json")}, map[string]Source{}); err == nil {
		t.Fatal("expected SaveSources mkdir error")
	}
	makeDirAll = oldMakeDirAll
}

func TestConfigErrorBranches(t *testing.T) {
	root := t.TempDir()
	dirConfig := filepath.Join(root, "dir-config")
	if err := os.MkdirAll(dirConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := readRawConfig(dirConfig); err == nil {
		t.Fatal("expected readRawConfig directory error")
	}
	badJSON := filepath.Join(root, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRawConfig(badJSON); err == nil {
		t.Fatal("expected invalid raw config error")
	}
	if _, err := DiscoverSkills(badJSON); err == nil {
		t.Fatal("expected DiscoverSkills file error")
	}
	if got, err := expandPath(""); err != nil || got != "" {
		t.Fatalf("expand empty = %q %v", got, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := expandPath("~"); err != nil || got != home {
		t.Fatalf("expand ~ = %q %v, want %q", got, err, home)
	}
}

func TestLoadErrorBranches(t *testing.T) {
	root := t.TempDir()

	t.Run("home expansion", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("UPSKILL_HOME", "~")
		if _, err := Load(); err == nil {
			t.Fatal("expected home expansion error")
		}
	})

	t.Run("config path expansion", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
		t.Setenv("UPSKILL_CONFIG", "~")
		if _, err := Load(); err == nil {
			t.Fatal("expected config path expansion error")
		}
	})

	t.Run("config read", func(t *testing.T) {
		configDir := filepath.Join(root, "config-dir")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
		t.Setenv("UPSKILL_CONFIG", configDir)
		if _, err := Load(); err == nil {
			t.Fatal("expected config read error")
		}
	})

	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "store path expansion", key: "UPSKILL_STORE_DIR"},
		{name: "overlays path expansion", key: "UPSKILL_OVERLAYS_DIR"},
		{name: "drafts path expansion", key: "UPSKILL_DRAFTS_DIR"},
		{name: "imports path expansion", key: "UPSKILL_IMPORTS_DIR"},
		{name: "rendered path expansion", key: "UPSKILL_RENDERED_DIR"},
		{name: "lock path expansion", key: "UPSKILL_LOCK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", "")
			t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
			t.Setenv("UPSKILL_CONFIG", filepath.Join(root, "missing.json"))
			t.Setenv(tc.key, "~")
			if _, err := Load(); err == nil {
				t.Fatalf("expected %s error", tc.key)
			}
		})
	}

	t.Run("agents parse", func(t *testing.T) {
		configPath := filepath.Join(root, "bad-agents.json")
		if err := os.WriteFile(configPath, []byte(`{"agents": 42}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
		t.Setenv("UPSKILL_CONFIG", configPath)
		if _, err := Load(); err == nil {
			t.Fatal("expected agents parse error")
		}
	})

	t.Run("sources parse", func(t *testing.T) {
		configPath := filepath.Join(root, "bad-sources.json")
		if err := os.WriteFile(configPath, []byte(`{"sources": 42}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
		t.Setenv("UPSKILL_CONFIG", configPath)
		if _, err := Load(); err == nil {
			t.Fatal("expected source parse error")
		}
	})

	t.Run("import dirs parse", func(t *testing.T) {
		configPath := filepath.Join(root, "bad-import-dirs.json")
		if err := os.WriteFile(configPath, []byte(`{"import_dirs": 42}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
		t.Setenv("UPSKILL_CONFIG", configPath)
		if _, err := Load(); err == nil {
			t.Fatal("expected import dirs parse error")
		}
	})

	t.Run("import dir expansion", func(t *testing.T) {
		configPath := filepath.Join(root, "bad-import-dir-path.json")
		if err := os.WriteFile(configPath, []byte(`{"import_dirs": ["~"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", "")
		t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
		t.Setenv("UPSKILL_CONFIG", configPath)
		if _, err := Load(); err == nil {
			t.Fatal("expected import dir expansion error")
		}
	})

	t.Run("agent output expansion", func(t *testing.T) {
		configPath := filepath.Join(root, "bad-agent-output-path.json")
		if err := os.WriteFile(configPath, []byte(`{"agents": {"codex": "~"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", "")
		t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
		t.Setenv("UPSKILL_CONFIG", configPath)
		if _, err := Load(); err == nil {
			t.Fatal("expected agent output expansion error")
		}
	})

	t.Run("list agent output expansion", func(t *testing.T) {
		t.Setenv("HOME", "")
		if _, err := parseAgentOutputs(json.RawMessage(`[{"name":"codex","path":"~"}]`)); err == nil {
			t.Fatal("expected list agent output expansion error")
		}
	})

	t.Run("detect agent output expansion", func(t *testing.T) {
		t.Setenv("HOME", "")
		agentOutputs := detectWellKnownAgentOutputs()
		if len(agentOutputs) != 0 {
			t.Fatalf("agent outputs = %#v", agentOutputs)
		}
	})
}
