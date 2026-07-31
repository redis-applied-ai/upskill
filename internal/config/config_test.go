package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPreflightCheckTTLConfiguration(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
	t.Setenv("UPSKILL_CONFIG", configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreflightCheckTTL != time.Hour {
		t.Fatalf("default TTL = %s", cfg.PreflightCheckTTL)
	}

	if err := os.WriteFile(configPath, []byte(`{"preflight_check_ttl":"45m"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreflightCheckTTL != 45*time.Minute {
		t.Fatalf("configured TTL = %s", cfg.PreflightCheckTTL)
	}

	t.Setenv("UPSKILL_PREFLIGHT_CHECK_TTL", "0")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreflightCheckTTL != 0 {
		t.Fatalf("zero TTL = %s", cfg.PreflightCheckTTL)
	}

	t.Setenv("UPSKILL_PREFLIGHT_CHECK_TTL", "30s")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreflightCheckTTL != 30*time.Second {
		t.Fatalf("environment TTL = %s", cfg.PreflightCheckTTL)
	}
}

func TestPreflightCheckTTLRejectsInvalidValues(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
	t.Setenv("UPSKILL_CONFIG", configPath)

	for _, value := range []string{"nope", "-1s"} {
		if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{"preflight_check_ttl":%q}`, value)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "preflight check TTL") {
			t.Fatalf("Load(%q) error = %v", value, err)
		}
	}

	if err := os.WriteFile(configPath, []byte(`{"preflight_check_ttl":"1h"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UPSKILL_PREFLIGHT_CHECK_TTL", "bad-env")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "bad-env") {
		t.Fatalf("invalid environment error = %v", err)
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
	if object["core"].ID == "" || object["core"].DefaultUpdatePolicy != UpdatePolicyManual {
		t.Fatalf("object metadata = %#v", object["core"])
	}
	stringsMap, err := parseSources(json.RawMessage(`{"local":"./skills"}`))
	if err != nil {
		t.Fatal(err)
	}
	if stringsMap["local"].Source != "./skills" {
		t.Fatalf("strings = %#v", stringsMap)
	}
	if stringsMap["local"].ID != SourceID(Source{Name: "local", Source: "./skills"}) {
		t.Fatalf("string source id = %s", stringsMap["local"].ID)
	}
	list, err := parseSources(json.RawMessage(`[{"name":"one","source":"owner/one"},{"name":"","source":""}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list["one"].Source != "owner/one" {
		t.Fatalf("list = %#v", list)
	}
	withMetadata, err := parseSources(json.RawMessage(`[{"id":"src_existing","name":"team","source":"owner/repo","default_update_policy":"auto"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if withMetadata["team"].ID != "src_existing" || withMetadata["team"].DefaultUpdatePolicy != UpdatePolicyAuto {
		t.Fatalf("metadata source = %#v", withMetadata["team"])
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
	if saved["core"].ID == "" || saved["core"].DefaultUpdatePolicy != UpdatePolicyManual {
		t.Fatalf("saved metadata = %#v", saved["core"])
	}
	renamed := Source{Name: "renamed", Source: "owner/repo"}
	if SourceID(saved["core"]) != SourceID(renamed) {
		t.Fatalf("source id should ignore display name: %s vs %s", SourceID(saved["core"]), SourceID(renamed))
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

func TestSourcesParsingPolicyFields(t *testing.T) {
	object, err := parseSources(json.RawMessage(`{
		"team": {
			"source": "owner/repo",
			"scope": "team",
			"write_policy": "overlay_preferred",
			"authors": ["Redis Applied AI"],
			"owners": ["redis"],
			"llm_guidance": "Ask before changing source files."
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	source := object["team"]
	if source.Scope != SourceScopeTeam ||
		source.WritePolicy != WritePolicyOverlayPreferred ||
		len(source.Authors) != 1 ||
		source.Authors[0] != "Redis Applied AI" ||
		len(source.Owners) != 1 ||
		source.Owners[0] != "redis" ||
		source.LLMGuidance == "" {
		t.Fatalf("policy source = %#v", source)
	}

	list, err := parseSources(json.RawMessage(`[{
		"name": "personal",
		"source": "./skills",
		"scope": "personal",
		"write_policy": "direct_allowed"
	}]`))
	if err != nil {
		t.Fatal(err)
	}
	if list["personal"].Scope != SourceScopePersonal || list["personal"].WritePolicy != WritePolicyDirectAllowed {
		t.Fatalf("list policy source = %#v", list["personal"])
	}
}

func TestSourcePolicyValidation(t *testing.T) {
	if _, err := parseSources(json.RawMessage(`[{"name":"bad","source":"owner/repo","scope":"global"}]`)); err == nil {
		t.Fatal("invalid scope unexpectedly parsed")
	}
	if _, err := parseSources(json.RawMessage(`[{"name":"bad","source":"owner/repo","write_policy":"merge_to_main"}]`)); err == nil {
		t.Fatal("invalid write policy unexpectedly parsed")
	}
	if !ValidSourceScope(SourceScopeTeam) || !ValidWritePolicy(WritePolicyDraftPromote) {
		t.Fatal("valid source policy values rejected")
	}
}

func TestSourcePolicyDefaultsAndMerge(t *testing.T) {
	localDefault := ResolveSourcePolicy(Source{}, "local", ManifestResolution{})
	if localDefault.Scope != SourceScopePersonal ||
		localDefault.WritePolicy != WritePolicyDirectAllowed ||
		localDefault.PolicySource != PolicySourceDefault {
		t.Fatalf("local default = %#v", localDefault)
	}
	remoteDefault := ResolveSourcePolicy(Source{}, "git", ManifestResolution{})
	if remoteDefault.Scope != SourceScopeExternal ||
		remoteDefault.WritePolicy != WritePolicyReadOnly {
		t.Fatalf("remote default = %#v", remoteDefault)
	}

	manifest := SourceManifest{
		Scope:       SourceScopeTeam,
		Authors:     []string{"Team"},
		Owners:      []string{"owner"},
		LLMGuidance: "Use drafts.",
	}
	fromManifest := ResolveSourcePolicy(Source{}, "local", ManifestResolution{Manifest: &manifest, Path: "/repo/upskill.toml"})
	if fromManifest.Scope != SourceScopeTeam ||
		fromManifest.WritePolicy != WritePolicyDraftPromote ||
		fromManifest.PolicySource != PolicySourceManifest ||
		fromManifest.ManifestPath != "/repo/upskill.toml" ||
		fromManifest.LLMGuidance != "Use drafts." {
		t.Fatalf("manifest policy = %#v", fromManifest)
	}

	configured := ResolveSourcePolicy(Source{
		Scope:       SourceScopePersonal,
		WritePolicy: WritePolicyOverlayPreferred,
		Authors:     []string{"Local"},
	}, "local", ManifestResolution{Manifest: &manifest})
	if configured.Scope != SourceScopePersonal ||
		configured.WritePolicy != WritePolicyOverlayPreferred ||
		configured.PolicySource != PolicySourceConfig ||
		configured.Authors[0] != "Local" {
		t.Fatalf("configured policy = %#v", configured)
	}
}

func TestSourceManifestDiscoveryAndWarnings(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, SourceManifestName), []byte(`scope = "team"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", SourceManifestName), []byte(`scope = "personal"
write_policy = "overlay_preferred"
authors = ["Subpath"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved := DiscoverSourceManifest(root, "skills")
	if resolved.Manifest == nil ||
		resolved.Manifest.Scope != SourceScopePersonal ||
		resolved.Manifest.WritePolicy != WritePolicyOverlayPreferred ||
		resolved.Manifest.Authors[0] != "Subpath" ||
		resolved.Path != filepath.Join(root, "skills", SourceManifestName) {
		t.Fatalf("resolved manifest = %#v", resolved)
	}

	badRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(badRoot, SourceManifestName), []byte(`scope = "team"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(badRoot, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badRoot, "skills", SourceManifestName), []byte(`scope = "global"`), 0o644); err != nil {
		t.Fatal(err)
	}
	warned := DiscoverSourceManifest(badRoot, "skills")
	if warned.Manifest == nil || warned.Manifest.Scope != SourceScopeTeam || !strings.Contains(warned.Warning, "invalid source scope") {
		t.Fatalf("warning manifest = %#v", warned)
	}
}

func TestCheckedInExampleManifestParses(t *testing.T) {
	manifest, err := ParseSourceManifest(filepath.Join("..", "..", "docs", "example-upskill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Scope != SourceScopeTeam ||
		manifest.WritePolicy != WritePolicyDraftPromote ||
		len(manifest.Authors) != 1 ||
		manifest.LLMGuidance == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestSaveSourcesDeterministicMetadata(t *testing.T) {
	root := t.TempDir()
	cfg := Config{ConfigPath: filepath.Join(root, "config.json")}
	first := map[string]Source{
		"zeta":  {Name: "zeta", Source: "owner/zeta", DefaultUpdatePolicy: UpdatePolicyAuto},
		"alpha": {Name: "alpha", Source: "owner/alpha"},
	}
	if err := SaveSources(cfg, first); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}

	second := map[string]Source{
		"alpha": {Name: "alpha", Source: "owner/alpha"},
		"zeta":  {Name: "zeta", Source: "owner/zeta", DefaultUpdatePolicy: UpdatePolicyAuto},
	}
	if err := SaveSources(cfg, second); err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("source writes differ:\nfirst=%s\nsecond=%s", firstBytes, secondBytes)
	}
	if !strings.Contains(string(firstBytes), `"id": "src_`) ||
		!strings.Contains(string(firstBytes), `"default_update_policy": "manual"`) ||
		!strings.Contains(string(firstBytes), `"default_update_policy": "auto"`) {
		t.Fatalf("source metadata missing from deterministic write: %s", firstBytes)
	}
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
