package library

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/config"
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
		LockPath:    filepath.Join(root, "home", "upskill-lock.json"),
		Sources:     map[string]config.Source{},
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

func TestParseSourceVariants(t *testing.T) {
	cases := []struct {
		input string
		typ   string
		url   string
		ref   string
		sub   string
		skill string
	}{
		{"owner/repo", "git", "https://github.com/owner/repo.git", "", "", ""},
		{"github:owner/repo/path/to/skills@main", "git", "https://github.com/owner/repo.git", "main", "path/to/skills", ""},
		{"owner/repo#dev@redis", "git", "https://github.com/owner/repo.git", "dev", "", "redis"},
		{"https://github.com/owner/repo/tree/main/skills/redis", "git", "https://github.com/owner/repo.git", "main", "skills/redis", ""},
		{"git@github.com:owner/repo.git", "git", "git@github.com:owner/repo.git", "", "", ""},
		{"ssh://git@example.com/owner/repo.git", "git", "ssh://git@example.com/owner/repo.git", "", "", ""},
	}
	for _, tc := range cases {
		parsed, err := ParseSource(tc.input)
		if err != nil {
			t.Fatalf("%s parse error: %v", tc.input, err)
		}
		if parsed.Type != tc.typ || parsed.URL != tc.url || parsed.Ref != tc.ref || parsed.Subpath != tc.sub || parsed.SkillFilter != tc.skill {
			t.Fatalf("%s parsed as %#v", tc.input, parsed)
		}
	}
	local, err := ParseSource(".")
	if err != nil {
		t.Fatal(err)
	}
	if local.Type != "local" || !filepath.IsAbs(local.LocalPath) {
		t.Fatalf("local parse = %#v", local)
	}
	if _, err := ParseSource(""); err == nil {
		t.Fatal("empty source unexpectedly parsed")
	}
	if _, err := ParseSource("not a source: value"); err == nil {
		t.Fatal("unsupported source unexpectedly parsed")
	}
	if _, err := ApplySourceOverrides(local, "main", "../bad", "redis"); err == nil {
		t.Fatal("unsafe subpath unexpectedly accepted")
	}
	overridden, err := ApplySourceOverrides(local, "main", "skills", "redis")
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Ref != "main" || overridden.Subpath != "skills" || overridden.SkillFilter != "redis" {
		t.Fatalf("overridden = %#v", overridden)
	}
}

func TestInstallUpdateFindReadRemoveAndManifest(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "source")
	writeFile(t, filepath.Join(source, "skills", "redis", "SKILL.md"), "---\nname: Redis Cache\ndescription: fast cache\n---\n\nUse Redis.\n")
	writeFile(t, filepath.Join(source, "skills", "redis", "references", "guide.md"), "Guide.\n")
	writeFile(t, filepath.Join(source, "node_modules", "ignored", "SKILL.md"), "---\nname: Ignored\n---\n\nNo.\n")

	installed, err := InstallSource(cfg, source, InstallOptions{SkillFilter: "redis-cache"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].StoreName != "redis-cache" || !strings.HasPrefix(installed[0].Hash, "sha256:") {
		t.Fatalf("installed = %#v", installed)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Skills["redis-cache"].Subpath != "skills/redis" {
		t.Fatalf("lock = %#v", lock.Skills["redis-cache"])
	}

	listed, err := ListInstalled(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Description != "fast cache" {
		t.Fatalf("listed = %#v", listed)
	}
	found, err := FindInstalled(cfg, "Redis", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found = %#v", found)
	}
	sourceSkill, err := ReadSourceSkill(cfg, source, InstallOptions{SkillFilter: "redis-cache"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sourceSkill.Body, "Use Redis.") {
		t.Fatalf("source skill = %#v", sourceSkill)
	}

	manifest, err := FileManifest(filepath.Join(cfg.StoreDir, "redis-cache"), "references", true, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(manifest), "references/guide.md") {
		t.Fatalf("manifest = %#v", manifest)
	}

	writeFile(t, filepath.Join(source, "skills", "redis", "SKILL.md"), "---\nname: Redis Cache\ndescription: fast cache\n---\n\nUse Redis 8.\n")
	updated, err := UpdateSkills(cfg, []string{"redis-cache"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 {
		t.Fatalf("updated = %#v", updated)
	}
	body, _ := os.ReadFile(filepath.Join(cfg.StoreDir, "redis-cache", "SKILL.md"))
	if !strings.Contains(string(body), "Redis 8") {
		t.Fatalf("store body = %s", body)
	}

	removed, err := RemoveSkills(cfg, []string{"redis-cache"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %#v", removed)
	}
	if _, err := os.Stat(filepath.Join(cfg.StoreDir, "redis-cache")); !os.IsNotExist(err) {
		t.Fatalf("store still exists: %v", err)
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func TestLibraryErrorPathsAndGitMaterialize(t *testing.T) {
	cfg := testConfig(t)
	noopMaterializeCleanup()
	if _, err := InstallSource(cfg, filepath.Join(cfg.Home, "missing"), InstallOptions{}); err == nil {
		t.Fatal("missing install unexpectedly succeeded")
	}
	initDir := filepath.Join(cfg.Home, "init-skill")
	if _, err := InitSkill(initDir, "Test Skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSkill(initDir, "Test Skill"); err == nil {
		t.Fatal("duplicate init unexpectedly succeeded")
	}

	writeFile(t, cfg.LockPath, "{bad")
	if _, err := ReadLock(cfg); err == nil {
		t.Fatal("bad lock unexpectedly loaded")
	}
	_ = os.Remove(cfg.LockPath)
	if err := WriteLock(cfg, Lock{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg.LockPath)
	var lock Lock
	if err := json.Unmarshal(raw, &lock); err != nil || lock.SchemaVersion != LockSchemaVersion {
		t.Fatalf("written lock = %s err=%v", raw, err)
	}

	fakeBin := filepath.Join(cfg.Home, "bin")
	writeFile(t, filepath.Join(fakeBin, "git"), "#!/bin/sh\nlast=\"\"\nfor arg in \"$@\"; do last=\"$arg\"; done\nmkdir -p \"$last/skill\"\ncat > \"$last/skill/SKILL.md\" <<'EOF'\n---\nname: Git Skill\ndescription: cloned\n---\n\nFrom git.\nEOF\n")
	if err := os.Chmod(filepath.Join(fakeBin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	installed, err := InstallSource(cfg, "git@example.com:owner/repo.git", InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].StoreName != "git-skill" {
		t.Fatalf("git installed = %#v", installed)
	}
	if _, err := InstallSource(cfg, "git@example.com:owner/ref.git", InstallOptions{Ref: "main"}); err != nil {
		t.Fatalf("git ref install failed: %v", err)
	}

	writeFile(t, filepath.Join(fakeBin, "git"), "#!/bin/sh\necho nope\nexit 2\n")
	if _, err := InstallSource(cfg, "git@example.com:owner/fail.git", InstallOptions{}); err == nil {
		t.Fatal("failing git unexpectedly installed")
	}
}

func TestLibraryAdditionalBranches(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "source")
	writeFile(t, filepath.Join(source, "one", "SKILL.md"), "No frontmatter body.\n")
	writeFile(t, filepath.Join(source, "two", "SKILL.md"), "---\nname: Two\ndescription: \"Second\"\n---\n\nTwo body.\n")
	cfg.Sources["named"] = config.Source{Name: "named", Source: source, Subpath: "two"}

	installed, err := InstallSource(cfg, "named", InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].StoreName != "two" {
		t.Fatalf("named install = %#v", installed)
	}
	if _, err := InstallSource(cfg, source, InstallOptions{SkillFilter: "missing"}); err == nil {
		t.Fatal("missing filtered install unexpectedly succeeded")
	}
	if _, err := InstallSource(cfg, "bad source: value", InstallOptions{}); err == nil {
		t.Fatal("bad source unexpectedly installed")
	}

	all, err := FindInstalled(cfg, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("find all = %#v", all)
	}
	none, err := FindInstalled(cfg, "absent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("find none = %#v", none)
	}
	if _, err := ReadSourceSkill(cfg, "named", InstallOptions{SkillFilter: "missing"}); err == nil {
		t.Fatal("missing source skill unexpectedly read")
	}

	emptyCfg := testConfig(t)
	updated, err := UpdateSkills(emptyCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated != nil {
		t.Fatalf("empty update = %#v", updated)
	}
	if _, err := UpdateSkills(cfg, []string{"absent"}); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["two"]
	entry.Subpath = "missing"
	lock.Skills["two"] = entry
	if err := WriteLock(cfg, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSkills(cfg, []string{"two"}); err == nil {
		t.Fatal("bad update unexpectedly succeeded")
	}

	if _, err := ListInstalled(config.Config{StoreDir: "\x00"}); err == nil {
		t.Fatal("bad list unexpectedly succeeded")
	}
	if _, err := HashDir(filepath.Join(cfg.Home, "missing")); err == nil {
		t.Fatal("missing hash unexpectedly succeeded")
	}
	if _, err := DiscoverSkills(source, "../bad"); err == nil {
		t.Fatal("unsafe discover unexpectedly succeeded")
	}
	if _, err := DiscoverSkills(filepath.Join(source, "one", "SKILL.md"), ""); err == nil {
		t.Fatal("file discover unexpectedly succeeded")
	}
	if _, err := FileManifest(filepath.Join(cfg.Home, "missing"), "", true, false, 1); err == nil {
		t.Fatal("missing manifest unexpectedly succeeded")
	}
	if manifest, err := FileManifest(source, "", false, false, 1); err != nil || len(manifest) != 1 {
		t.Fatalf("limited manifest = %#v %v", manifest, err)
	}
	if _, err := RemoveSkills(cfg, []string{"two"}, true); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryEdgeBranches(t *testing.T) {
	cfg := testConfig(t)
	if _, err := ParseSource("http://%zz"); err == nil {
		t.Fatal("bad URL unexpectedly parsed")
	}
	if parsed, err := ParseSource("https://example.com/owner/repo.git"); err != nil || parsed.URL != "https://example.com/owner/repo.git" {
		t.Fatalf("generic http = %#v %v", parsed, err)
	}
	if parsed, err := ParseSource("https://example.com/owner/repo"); err != nil || parsed.URL != "https://example.com/owner/repo" {
		t.Fatalf("generic http fallback = %#v %v", parsed, err)
	}
	if _, err := ParseSource("https://github.com/o/r/tree/main/../bad"); err == nil {
		t.Fatal("bad github subpath unexpectedly parsed")
	}
	head, ref, skill := splitFragment("owner/repo#")
	if head != "owner/repo#" || ref != "" || skill != "" {
		t.Fatalf("split empty fragment = %s %s %s", head, ref, skill)
	}
	head, ref, skill = splitFragment("owner/repo#main@redis")
	if head != "owner/repo" || ref != "main" || skill != "redis" {
		t.Fatalf("split = %s %s %s", head, ref, skill)
	}

	source := filepath.Join(cfg.Home, "source")
	writeFile(t, filepath.Join(source, "a", "SKILL.md"), "---\nname: Same\n---\n\nA\n")
	writeFile(t, filepath.Join(source, "b", "SKILL.md"), "---\nname: Same\n---\n\nB\n")
	skills, err := DiscoverSkills(source, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("dedup skills = %#v", skills)
	}

	writeFile(t, cfg.LockPath, "{bad")
	if _, err := InstallSource(cfg, source, InstallOptions{}); err == nil {
		t.Fatal("install with bad lock unexpectedly succeeded")
	}
	if _, err := UpdateSkills(cfg, nil); err == nil {
		t.Fatal("update with bad lock unexpectedly succeeded")
	}
	if _, err := RemoveSkills(cfg, []string{"same"}, false); err == nil {
		t.Fatal("remove with bad lock unexpectedly succeeded")
	}
	_ = os.Remove(cfg.LockPath)

	if _, _, err := installSkill(cfg, source, ParsedSource{Type: "local", URL: source}, Skill{Name: "!!!", Path: source}); err == nil {
		t.Fatal("empty normalized skill unexpectedly installed")
	}
	if _, _, err := installSkill(cfg, source, ParsedSource{Type: "local", URL: source}, Skill{Name: "Missing", Path: filepath.Join(source, "missing")}); err == nil {
		t.Fatal("missing skill path unexpectedly installed")
	}

	lock := Lock{Skills: map[string]LockEntry{
		"git-fail": {Name: "Git Fail", Source: "git@example.com:o/r.git", SourceType: "git", SourceURL: "git@example.com:o/r.git"},
	}}
	if err := WriteLock(cfg, lock); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(cfg.Home, "bin")
	writeFile(t, filepath.Join(fakeBin, "git"), "#!/bin/sh\necho fail\nexit 2\n")
	if err := os.Chmod(filepath.Join(fakeBin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := UpdateSkills(cfg, nil); err == nil {
		t.Fatal("git update unexpectedly succeeded")
	}

	if _, err := ListInstalled(config.Config{StoreDir: cfg.LockPath}); err == nil {
		t.Fatal("list with file store unexpectedly succeeded")
	}
	writeFile(t, filepath.Join(cfg.StoreDir, "bad", "README.md"), "x")
	if listed, err := ListInstalled(cfg); err != nil || len(listed) != 0 {
		t.Fatalf("list skips bad = %#v %v", listed, err)
	}
	if _, err := FindInstalled(config.Config{StoreDir: cfg.LockPath}, "x", 1); err == nil {
		t.Fatal("find with bad store unexpectedly succeeded")
	}

	if _, err := ReadSourceSkill(cfg, "bad source: value", InstallOptions{}); err == nil {
		t.Fatal("bad read source unexpectedly succeeded")
	}
	if _, err := ReadSourceSkill(cfg, source, InstallOptions{Subpath: "../bad"}); err == nil {
		t.Fatal("bad read subpath unexpectedly succeeded")
	}
	if _, err := ReadSourceSkill(cfg, filepath.Join(cfg.Home, "missing"), InstallOptions{}); err == nil {
		t.Fatal("missing read source unexpectedly succeeded")
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSkill("", "Relative Skill"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSkill("\x00", "Bad"); err == nil {
		t.Fatal("bad init dir unexpectedly succeeded")
	}
	if _, err := ReadLock(config.Config{LockPath: cfg.StoreDir}); err == nil {
		t.Fatal("directory lock unexpectedly read")
	}
	if err := WriteLock(config.Config{LockPath: "\x00"}, Lock{}); err == nil {
		t.Fatal("bad lock path unexpectedly written")
	}

	if rootFromEntry(ParsedSource{Type: "git"}, "/tmp/root") != "/tmp/root" {
		t.Fatal("git rootFromEntry failed")
	}
	if err := walkSkillDirs(source, 7, 6, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, body := parseFrontmatter("---\nname: Open\nBody"); !strings.Contains(body, "Body") {
		t.Fatalf("open frontmatter body = %q", body)
	}
	if _, err := fileHash(filepath.Join(cfg.Home, "missing")); err == nil {
		t.Fatal("missing file hash unexpectedly succeeded")
	}
}

func TestLibraryFinalBranchCoverage(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "root-source")
	writeFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Root Skill\n---\n\nRoot.\n")
	writeFile(t, filepath.Join(source, "nested", "SKILL.md"), "---\nname: Nested Skill\n---\n\nNested.\n")
	if _, err := InstallSource(cfg, source, InstallOptions{Subpath: "../bad"}); err == nil {
		t.Fatal("unsafe install subpath unexpectedly succeeded")
	}
	installed, err := InstallSource(cfg, source, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 2 {
		t.Fatalf("installed = %#v", installed)
	}
	all, err := FindInstalled(cfg, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].StoreName != "nested-skill" {
		t.Fatalf("limited sorted find = %#v", all)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Skills["root-skill"].Subpath != "" || lock.Skills["root-skill"].SkillPath != "SKILL.md" {
		t.Fatalf("root lock = %#v", lock.Skills["root-skill"])
	}

	writeFile(t, cfg.LockPath, `{}`)
	defaulted, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if defaulted.SchemaVersion != LockSchemaVersion || defaulted.Skills == nil {
		t.Fatalf("defaulted lock = %#v", defaulted)
	}

	badStore := filepath.Join(cfg.Home, "store-file")
	writeFile(t, badStore, "file")
	if _, err := InstallSource(config.Config{StoreDir: badStore, LockPath: filepath.Join(cfg.Home, "bad-lock.json")}, source, InstallOptions{}); err == nil {
		t.Fatal("install to file store unexpectedly succeeded")
	}

	lock = Lock{Skills: map[string]LockEntry{
		"root-skill": {Name: "Root Skill", Source: source, SourceType: "local", SourceURL: source},
	}}
	if err := WriteLock(cfg, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSkills(config.Config{StoreDir: badStore, LockPath: cfg.LockPath}, nil); err == nil {
		t.Fatal("update to file store unexpectedly succeeded")
	}

	if err := walkSkillDirs(source, 0, 6, func(string) error { return os.ErrInvalid }); err == nil {
		t.Fatal("walk callback error not returned")
	}
	if _, err := parseHTTPSource(ParsedSource{}, "https://github.com/o/r/tree/main/../bad"); err == nil {
		t.Fatal("parseHTTPSource unsafe path unexpectedly succeeded")
	}
}

func TestLibraryCoverageCompletionBranches(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "source")
	writeFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Root\n---\n\nRoot.\n")
	writeFile(t, filepath.Join(source, "nested", "file.txt"), "nested")

	if _, err := ParseSource("owner/repo/../bad"); err == nil {
		t.Fatal("unsafe shorthand subpath unexpectedly parsed")
	}
	head, ref, skill := splitFragment("owner/repo#main")
	if head != "owner/repo" || ref != "main" || skill != "" {
		t.Fatalf("split without skill = %s %s %s", head, ref, skill)
	}

	oldAbs := absPath
	absPath = func(string) (string, error) { return "", errors.New("abs failed") }
	if _, err := ParseSource("."); err == nil {
		t.Fatal("local source parse error not returned")
	}
	absPath = oldAbs

	sourceFile := filepath.Join(cfg.Home, "source-file")
	writeFile(t, sourceFile, "not a directory")
	if _, err := InstallSource(cfg, sourceFile, InstallOptions{}); err == nil {
		t.Fatal("file source install unexpectedly succeeded")
	}
	if _, err := ReadSourceSkill(cfg, sourceFile, InstallOptions{}); err == nil {
		t.Fatal("file source read unexpectedly succeeded")
	}

	parentFile := filepath.Join(cfg.Home, "lock-parent")
	writeFile(t, parentFile, "file")
	if _, err := InstallSource(config.Config{
		StoreDir:    filepath.Join(cfg.Home, "store-write-lock"),
		LockPath:    filepath.Join(parentFile, "lock.json"),
		OverlaysDir: cfg.OverlaysDir,
		RenderedDir: cfg.RenderedDir,
	}, source, InstallOptions{}); err == nil {
		t.Fatal("install with unwritable lock path unexpectedly succeeded")
	}
	if err := WriteLock(config.Config{LockPath: filepath.Join(parentFile, "lock.json")}, Lock{}); err == nil {
		t.Fatal("WriteLock with file parent unexpectedly succeeded")
	}
	oldMakeAllForInstall := makeAll
	makeAll = func(path string, perm os.FileMode) error {
		if path == filepath.Join(cfg.Home, "lock-write-error") {
			return errors.New("lock mkdir failed")
		}
		return oldMakeAllForInstall(path, perm)
	}
	if _, err := InstallSource(config.Config{
		StoreDir:    filepath.Join(cfg.Home, "store-install-write-lock"),
		LockPath:    filepath.Join(cfg.Home, "lock-write-error", "lock.json"),
		OverlaysDir: cfg.OverlaysDir,
		RenderedDir: cfg.RenderedDir,
	}, source, InstallOptions{}); err == nil {
		t.Fatal("install write-lock failure not returned")
	}
	makeAll = oldMakeAllForInstall

	lock := Lock{Skills: map[string]LockEntry{
		"root": {Name: "Root", Source: sourceFile, SourceType: "local", SourceURL: sourceFile},
	}}
	if err := WriteLock(cfg, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSkills(cfg, nil); err == nil {
		t.Fatal("update with file source unexpectedly succeeded")
	}

	lock = Lock{Skills: map[string]LockEntry{
		"root": {Name: "Root", Source: source, SourceType: "local", SourceURL: source},
	}}
	if err := WriteLock(cfg, lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg.LockPath, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSkills(cfg, nil); err == nil {
		t.Fatal("update with read-only lock unexpectedly succeeded")
	}
	if err := os.Chmod(cfg.LockPath, 0o644); err != nil {
		t.Fatal(err)
	}

	oldTemp := makeTempDir
	makeTempDir = func(string, string) (string, error) { return "", errors.New("temp failed") }
	if _, _, err := materialize(ParsedSource{Type: "git", URL: "git@example.com:o/r.git"}); err == nil {
		t.Fatal("materialize temp error not returned")
	}
	makeTempDir = oldTemp

	fakeBin := filepath.Join(cfg.Home, "bin")
	writeFile(t, filepath.Join(fakeBin, "git"), "#!/bin/sh\necho fail\nexit 2\n")
	if err := os.Chmod(filepath.Join(fakeBin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := ReadSourceSkill(cfg, "git@example.com:o/fail.git", InstallOptions{}); err == nil {
		t.Fatal("failing git source read unexpectedly succeeded")
	}

	oldReadSkill := readSkillFromDir
	readSkillFromDir = func(string) (Skill, error) { return Skill{}, errors.New("read skill failed") }
	if _, err := DiscoverSkills(source, ""); err == nil {
		t.Fatal("discover read-skill failure not returned")
	}
	readSkillFromDir = oldReadSkill

	t.Run("remove skill errors", func(t *testing.T) {
		baseRemove := removeTree
		t.Cleanup(func() { removeTree = baseRemove })
		for _, failAt := range []int{1, 2, 3} {
			calls := 0
			removeTree = func(path string) error {
				calls++
				if calls == failAt {
					return errors.New("remove failed")
				}
				return nil
			}
			if _, err := RemoveSkills(cfg, []string{"root"}, true); err == nil {
				t.Fatalf("remove failure %d not returned", failAt)
			}
		}
		removeTree = func(path string) error { return nil }
		if err := os.Chmod(cfg.LockPath, 0o444); err != nil {
			t.Fatal(err)
		}
		if _, err := RemoveSkills(cfg, []string{"root"}, false); err == nil {
			t.Fatal("remove write-lock failure not returned")
		}
		if err := os.Chmod(cfg.LockPath, 0o644); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("install skill errors", func(t *testing.T) {
		baseRemove, baseMake, baseRename, baseHash := removeTree, makeAll, renameFS, hashTree
		t.Cleanup(func() {
			removeTree, makeAll, renameFS, hashTree = baseRemove, baseMake, baseRename, baseHash
		})
		skill := Skill{Name: "Root", Path: source}
		parsed := ParsedSource{Type: "local", URL: source}

		removeTree = func(path string) error { return errors.New("tmp remove failed") }
		if _, _, err := installSkill(cfg, source, parsed, skill); err == nil {
			t.Fatal("tmp remove failure not returned")
		}
		removeTree = baseRemove

		hashTree = func(string) (string, error) { return "", errors.New("hash failed") }
		if _, _, err := installSkill(cfg, source, parsed, skill); err == nil {
			t.Fatal("hash failure not returned")
		}
		hashTree = baseHash

		calls := 0
		removeTree = func(path string) error {
			calls++
			if calls == 2 {
				return errors.New("dst remove failed")
			}
			return baseRemove(path)
		}
		if _, _, err := installSkill(cfg, source, parsed, skill); err == nil {
			t.Fatal("dst remove failure not returned")
		}
		removeTree = baseRemove

		makeAll = func(string, os.FileMode) error { return errors.New("mkdir failed") }
		if _, _, err := installSkill(cfg, source, parsed, skill); err == nil {
			t.Fatal("mkdir failure not returned")
		}
		makeAll = baseMake

		renameFS = func(string, string) error { return errors.New("rename failed") }
		if _, _, err := installSkill(cfg, source, parsed, skill); err == nil {
			t.Fatal("rename failure not returned")
		}
		renameFS = baseRename
	})

	emptyNamePath, err := InitSkill(filepath.Join(cfg.Home, "empty-name"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, emptyNamePath)), "empty-name") {
		t.Fatalf("empty-name init = %s", mustRead(t, emptyNamePath))
	}
	noAccess := filepath.Join(cfg.Home, "no-access")
	if err := os.MkdirAll(noAccess, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(noAccess, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSkill(noAccess, "Blocked"); err == nil {
		t.Fatal("init stat permission error not returned")
	}
	_ = os.Chmod(noAccess, 0o755)

	if err := os.Symlink(filepath.Join(cfg.Home, "missing-target"), filepath.Join(source, "broken-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := HashDir(source); err == nil {
		t.Fatal("hash of broken symlink unexpectedly succeeded")
	}
	_ = os.Remove(filepath.Join(source, "broken-link"))

	oldInfo := infoForEntry
	infoForEntry = func(entry os.DirEntry) (os.FileInfo, error) {
		return nil, errors.New("info failed")
	}
	if _, err := FileManifest(source, "", true, false, 0); err == nil {
		t.Fatal("manifest info error not returned")
	}
	infoForEntry = oldInfo

	if err := os.MkdirAll(filepath.Join(source, "nested", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	prefixManifest, err := FileManifest(source, "absent", true, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixManifest) != 0 {
		t.Fatalf("prefix manifest = %#v", prefixManifest)
	}
	flatManifest, err := FileManifest(source, "", false, false, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range flatManifest {
		if item["path"] == "nested/file.txt" {
			t.Fatalf("nonrecursive manifest included nested file: %#v", flatManifest)
		}
	}

	walkRoot := filepath.Join(cfg.Home, "walk-root")
	if err := os.MkdirAll(walkRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(walkRoot, 0); err != nil {
		t.Fatal(err)
	}
	if err := walkSkillDirs(walkRoot, 0, 6, func(string) error { return nil }); err == nil {
		t.Fatal("walk read-dir permission error not returned")
	}
	_ = os.Chmod(walkRoot, 0o755)

	walkChild := filepath.Join(cfg.Home, "walk-child", "child")
	if err := os.MkdirAll(walkChild, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(walkChild, 0); err != nil {
		t.Fatal(err)
	}
	if err := walkSkillDirs(filepath.Dir(walkChild), 0, 6, func(string) error { return nil }); err == nil {
		t.Fatal("walk child permission error not returned")
	}
	_ = os.Chmod(walkChild, 0o755)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
