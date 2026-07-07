package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
		{"file:///tmp/repo.git", "git", "file:///tmp/repo.git", "", "", ""},
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
	entry := lock.Skills["redis-cache"]
	if lock.SchemaVersion != LockSchemaVersion ||
		entry.Subpath != "skills/redis" ||
		entry.SourceSubpath != "" ||
		entry.SkillSubpath != "skills/redis" ||
		entry.UpdatePolicy != config.UpdatePolicyManual {
		t.Fatalf("lock = %#v", entry)
	}

	listed, err := ListInstalled(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Description != "fast cache" {
		t.Fatalf("listed = %#v", listed)
	}
	found, _, err := FindInstalled(cfg, "Redis", 1)
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
	preview, err := PreviewUpdates(cfg, []string{"redis-cache"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 1 || !preview[0].Changed || preview[0].Diff != nil {
		t.Fatalf("preview = %#v", preview)
	}
	preview, err = PreviewUpdates(cfg, []string{"redis-cache"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 1 || !strings.Contains(strings.Join(preview[0].Diff, "\n"), "Redis 8") {
		t.Fatalf("preview diff = %#v", preview)
	}
	oldBody, _ := os.ReadFile(filepath.Join(cfg.StoreDir, "redis-cache", "SKILL.md"))
	if strings.Contains(string(oldBody), "Redis 8") {
		t.Fatalf("preview mutated store body = %s", oldBody)
	}
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
	preview, err = PreviewUpdates(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 1 || preview[0].Changed || len(preview[0].Diff) != 0 {
		t.Fatalf("unchanged preview = %#v", preview)
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

func TestLocalSourceDiscoveryAndResolution(t *testing.T) {
	cfg := testConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "local-source-root")
	source := filepath.Join(sourceRoot, "source")
	writeFile(t, filepath.Join(source, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nBase.\n")
	writeFile(t, filepath.Join(source, "overlays", "shadow", "SKILL.md"), "---\nname: Shadow\n---\n\nIgnored.\n")
	writeFile(t, filepath.Join(source, ".obsidian", "plugin", "SKILL.md"), "---\nname: Note Plugin\n---\n\nIgnored.\n")
	writeFile(t, filepath.Join(source, "private", "SKILL.md"), "---\nname: Private\n---\n\nIgnored by custom rule.\n")

	ignore := append([]string{}, config.DefaultSourceIgnore...)
	ignore = append(ignore, "private")
	skills, err := DiscoverSkillsWithIgnore(source, "", ignore)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].StoreName != "redis" {
		t.Fatalf("skills = %#v", skills)
	}

	cfg.Sources["local"] = config.Source{Name: "local", Source: sourceRoot, Subpath: "source"}
	dest, err := ResolveLocalSourceDestination(cfg, "local")
	if err != nil {
		t.Fatal(err)
	}
	if dest != source {
		t.Fatalf("dest = %s", dest)
	}
	cfg.Sources["local-root"] = config.Source{Name: "local-root", Source: source}
	rootDest, err := ResolveLocalSourceDestination(cfg, "local-root")
	if err != nil {
		t.Fatal(err)
	}
	if rootDest != source {
		t.Fatalf("rootDest = %s", rootDest)
	}
	if _, err := ResolveLocalSourceDestination(cfg, "missing"); err == nil {
		t.Fatal("missing source unexpectedly resolved")
	}
	cfg.Sources["empty"] = config.Source{Name: "empty"}
	if _, err := ResolveLocalSourceDestination(cfg, "empty"); err == nil {
		t.Fatal("empty source unexpectedly resolved")
	}
	cfg.Sources["remote"] = config.Source{Name: "remote", Source: "owner/repo"}
	if _, err := ResolveLocalSourceDestination(cfg, "remote"); err == nil {
		t.Fatal("remote source unexpectedly resolved")
	}
	cfg.Sources["bad-subpath"] = config.Source{Name: "bad-subpath", Source: sourceRoot, Subpath: "../bad"}
	if _, err := ResolveLocalSourceDestination(cfg, "bad-subpath"); err == nil {
		t.Fatal("unsafe source subpath unexpectedly resolved")
	}
	t.Setenv("HOME", cfg.Home)
	tilde, err := ParseSource("~/local-source-root/source")
	if err != nil {
		t.Fatal(err)
	}
	if tilde.LocalPath != source {
		t.Fatalf("tilde local path = %#v", tilde)
	}
	home, err := ParseSource("~")
	if err != nil {
		t.Fatal(err)
	}
	if home.LocalPath != cfg.Home {
		t.Fatalf("home local path = %#v", home)
	}
	if _, ok := ignoreNameSet([]string{"", "custom"})["custom"]; !ok {
		t.Fatal("ignoreNameSet custom entry missing")
	}
	t.Setenv("HOME", "")
	if _, err := ParseSource("~"); err == nil {
		t.Fatal("empty HOME tilde source unexpectedly parsed")
	}
}

func TestInstallTemplateOnlySkill(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "template-source")
	writeFile(t, filepath.Join(source, "github-issue-triage", "SKILL.md.j2"), `---
name: GitHub Issue Triage
description: Triage GitHub issues and PRs
---

# GitHub Issue Triage

{% block guidance %}Base guidance.{% endblock %}
`)

	installed, err := InstallSource(cfg, source, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].StoreName != "github-issue-triage" {
		t.Fatalf("installed = %#v", installed)
	}
	if _, err := os.Stat(filepath.Join(cfg.StoreDir, "github-issue-triage", "SKILL.md.j2")); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Skills["github-issue-triage"].SkillPath != "github-issue-triage/SKILL.md.j2" {
		t.Fatalf("lock = %#v", lock.Skills["github-issue-triage"])
	}
	listed, err := ListInstalled(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Description != "Triage GitHub issues and PRs" || !strings.HasSuffix(listed[0].SkillPath, "SKILL.md.j2") {
		t.Fatalf("listed = %#v", listed)
	}
}

func TestSchemaOneLockCompatibility(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "legacy-source")
	writeFile(t, filepath.Join(source, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	oldHash, err := HashDir(filepath.Join(cfg.StoreDir, "redis"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "schema_version": 1,
  "skills": {
    "redis": {
      "name": "Redis",
      "source": "` + source + `",
      "source_type": "local",
      "source_url": "` + source + `",
      "subpath": "skills/redis",
      "skill_path": "skills/redis/SKILL.md",
      "hash": "` + oldHash + `",
      "updated_at": "2026-01-01T00:00:00Z"
    }
  }
}`
	writeFile(t, cfg.LockPath, legacy)

	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["redis"]
	if lock.SchemaVersion != 1 ||
		entry.UpdatePolicy != config.UpdatePolicyManual ||
		entry.SourceSubpath != "" ||
		entry.SkillSubpath != "skills/redis" {
		t.Fatalf("legacy lock read = %#v %#v", lock, entry)
	}

	preview, err := PreviewUpdates(cfg, []string{"redis"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 1 || !preview[0].Changed {
		t.Fatalf("legacy preview = %#v", preview)
	}

	updated, err := UpdateSkills(cfg, []string{"redis"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || updated[0].StoreName != "redis" {
		t.Fatalf("legacy update = %#v", updated)
	}
	upgraded, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	upgradedEntry := upgraded.Skills["redis"]
	if upgraded.SchemaVersion != LockSchemaVersion ||
		upgradedEntry.UpdatePolicy != config.UpdatePolicyManual ||
		upgradedEntry.SourceSubpath != "skills/redis" ||
		upgradedEntry.SkillSubpath != "" ||
		upgradedEntry.Hash == oldHash {
		t.Fatalf("upgraded lock = %#v %#v", upgraded, upgradedEntry)
	}
}

func TestCheckSkillUpdatesReadOnly(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "check-source")
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	installed, err := InstallSource(cfg, source, InstallOptions{SkillFilter: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 {
		t.Fatalf("installed = %#v", installed)
	}
	installedHash := installed[0].Hash
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")

	checks, err := CheckSkillUpdates(cfg, []string{"redis"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 ||
		checks[0].Error != "" ||
		!checks[0].Changed ||
		checks[0].CurrentHash != installedHash ||
		checks[0].NewHash == "" ||
		len(checks[0].Diff) == 0 {
		t.Fatalf("checks = %#v", checks)
	}
	storeHash, err := HashDir(filepath.Join(cfg.StoreDir, "redis"))
	if err != nil {
		t.Fatal(err)
	}
	if storeHash != installedHash {
		t.Fatalf("check mutated store: %s != %s", storeHash, installedHash)
	}
}

func TestCheckSkillUpdatesDetectsGitContentChange(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cfg := testConfig(t)
	remote := filepath.Join(cfg.Home, "updates.git")
	work := filepath.Join(cfg.Home, "updates-work")
	runGit(t, "", "init", "--bare", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "init")
	writeFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "old redis")
	firstCommit := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	runGit(t, work, "branch", "-M", "main")
	runGit(t, work, "remote", "add", "origin", remote)
	runGit(t, work, "push", "-u", "origin", "main")
	runGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	source := "file://" + remote
	installed, err := InstallSource(cfg, source, InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].StoreName != "redis" {
		t.Fatalf("installed = %#v", installed)
	}

	writeFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "new redis")
	secondCommit := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	runGit(t, work, "push", "origin", "main")

	checks, err := CheckSkillUpdates(cfg, []string{"redis"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 ||
		checks[0].Error != "" ||
		!checks[0].Changed ||
		checks[0].CurrentRevision != firstCommit ||
		checks[0].ResolvedRevision != secondCommit ||
		checks[0].CurrentHash == "" ||
		checks[0].NewHash == "" ||
		checks[0].CurrentHash == checks[0].NewHash {
		t.Fatalf("git checks = %#v", checks)
	}
}

func TestCheckSkillUpdatesReportsFailures(t *testing.T) {
	t.Run("local source read", func(t *testing.T) {
		cfg := testConfig(t)
		sourceFile := filepath.Join(cfg.Home, "source-file")
		writeFile(t, sourceFile, "not a directory")
		if err := WriteLock(cfg, Lock{Skills: map[string]LockEntry{
			"file-skill": {Name: "File Skill", Source: sourceFile, SourceType: "local", SourceURL: sourceFile, Hash: "sha256:old"},
		}}); err != nil {
			t.Fatal(err)
		}
		checks, err := CheckSkillUpdates(cfg, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(checks) != 1 || checks[0].Error == "" || checks[0].Changed || checks[0].NewHash != "" {
			t.Fatalf("local failure checks = %#v", checks)
		}
	})

	t.Run("hash", func(t *testing.T) {
		cfg := testConfig(t)
		source := filepath.Join(cfg.Home, "source")
		writeFile(t, filepath.Join(source, "skill", "SKILL.md"), "---\nname: Skill\n---\n\nSkill.\n")
		if err := WriteLock(cfg, Lock{Skills: map[string]LockEntry{
			"skill": {Name: "Skill", Source: source, SourceType: "local", SourceURL: source, Hash: "sha256:old"},
		}}); err != nil {
			t.Fatal(err)
		}
		oldHashTree := hashTree
		hashTree = func(string) (string, error) { return "", errors.New("hash failed") }
		checks, err := CheckSkillUpdates(cfg, nil, false)
		hashTree = oldHashTree
		if err != nil {
			t.Fatal(err)
		}
		if len(checks) != 1 || !strings.Contains(checks[0].Error, "hash failed") || checks[0].Changed {
			t.Fatalf("hash failure checks = %#v", checks)
		}
	})

	t.Run("git clone", func(t *testing.T) {
		cfg := testConfig(t)
		fakeBin := filepath.Join(cfg.Home, "bin")
		writeFile(t, filepath.Join(fakeBin, "git"), "#!/bin/sh\necho clone failed\nexit 2\n")
		if err := os.Chmod(filepath.Join(fakeBin, "git"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err := WriteLock(cfg, Lock{Skills: map[string]LockEntry{
			"git-skill": {Name: "Git Skill", Source: "git@example.com:owner/fail.git", SourceType: "git", SourceURL: "git@example.com:owner/fail.git", Hash: "sha256:old"},
		}}); err != nil {
			t.Fatal(err)
		}
		checks, err := CheckSkillUpdates(cfg, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(checks) != 1 || !strings.Contains(checks[0].Error, "git clone failed") || checks[0].Changed {
			t.Fatalf("git clone failure checks = %#v", checks)
		}
	})

	t.Run("git checkout", func(t *testing.T) {
		cfg := testConfig(t)
		fakeBin := filepath.Join(cfg.Home, "bin")
		writeFile(t, filepath.Join(fakeBin, "git"), `#!/bin/sh
if [ "$1" = "-C" ]; then
  if [ "$3" = "checkout" ]; then
    echo checkout failed
    exit 2
  fi
  echo abcdef0123456789abcdef0123456789abcdef01
  exit 0
fi
last=""
for arg in "$@"; do last="$arg"; done
mkdir -p "$last/skill"
cat > "$last/skill/SKILL.md" <<'EOF'
---
name: Git Skill
---

Git.
EOF
`)
		if err := os.Chmod(filepath.Join(fakeBin, "git"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err := WriteLock(cfg, Lock{Skills: map[string]LockEntry{
			"git-skill": {
				Name:       "Git Skill",
				Source:     "git@example.com:owner/repo.git",
				SourceType: "git",
				SourceURL:  "git@example.com:owner/repo.git",
				Ref:        "abcdef0123456789abcdef0123456789abcdef01",
				Hash:       "sha256:old",
			},
		}}); err != nil {
			t.Fatal(err)
		}
		checks, err := CheckSkillUpdates(cfg, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(checks) != 1 || !strings.Contains(checks[0].Error, "git checkout failed") || checks[0].Changed {
			t.Fatalf("git checkout failure checks = %#v", checks)
		}
	})
}

func TestResolveMaterializedSourceMetadata(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	parsed := ParsedSource{
		Type:             "local",
		URL:              source,
		LocalPath:        source,
		Ref:              "main",
		Subpath:          "skills",
		SourceID:         "src_test",
		ResolvedRevision: "rev-test",
	}
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Cleanup()
	if resolved.Root != source ||
		resolved.SourceType != "local" ||
		resolved.SourceID != "src_test" ||
		resolved.ResolvedRevision != "rev-test" ||
		resolved.SourceIdentity != config.SourceIdentity(config.Source{Source: source, Ref: "main", Subpath: "skills"}) {
		t.Fatalf("resolved source = %#v", resolved)
	}
}

func TestGitPinnedCommitUsesDetachedCheckout(t *testing.T) {
	cfg := testConfig(t)
	fakeBin := filepath.Join(cfg.Home, "bin")
	logPath := filepath.Join(cfg.Home, "git.log")
	ref := "abcdef0123456789abcdef0123456789abcdef01"
	writeFile(t, filepath.Join(fakeBin, "git"), fmt.Sprintf(`#!/bin/sh
log=%q
echo "$@" >> "$log"
if [ "$1" = "-C" ]; then
  if [ "$3" = "rev-parse" ]; then
    echo %s
  fi
  exit 0
fi
last=""
for arg in "$@"; do last="$arg"; done
mkdir -p "$last/skill"
cat > "$last/skill/SKILL.md" <<'EOF'
---
name: Pinned Git Skill
---

Pinned.
EOF
`, logPath, ref))
	if err := os.Chmod(filepath.Join(fakeBin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	installed, err := InstallSource(cfg, "git@example.com:owner/pinned.git", InstallOptions{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].StoreName != "pinned-git-skill" {
		t.Fatalf("installed = %#v", installed)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if strings.Contains(log, "--branch "+ref) || !strings.Contains(log, "checkout --detach "+ref) {
		t.Fatalf("git commands did not use detached checkout for pinned ref:\n%s", log)
	}
}

func TestInspectLocalSourceReadsCurrentDirectory(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "live-local-source")
	writeFile(t, filepath.Join(source, "one", "SKILL.md"), "---\nname: One\n---\n\nOne.\n")

	first, err := InspectSource(cfg, source, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].StoreName != "one" {
		t.Fatalf("first local inspect = %#v", first)
	}

	writeFile(t, filepath.Join(source, "two", "SKILL.md"), "---\nname: Two\n---\n\nTwo.\n")
	second, err := InspectSource(cfg, source, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || second[0].StoreName != "one" || second[1].StoreName != "two" {
		t.Fatalf("second local inspect = %#v", second)
	}
}

func TestWriteLockDeterministicMetadata(t *testing.T) {
	cfg := testConfig(t)
	first := Lock{Skills: map[string]LockEntry{
		"zeta": {
			Name:          "Zeta",
			Source:        "owner/zeta",
			SourceID:      "src_zeta",
			SourceType:    "git",
			SourceURL:     "https://github.com/owner/zeta.git",
			Ref:           "main",
			Subpath:       "skills/zeta",
			SourceSubpath: "skills",
			SkillSubpath:  "zeta",
			SkillPath:     "skills/zeta/SKILL.md",
			Hash:          "sha256:zeta",
			UpdatedAt:     "2026-01-02T00:00:00Z",
		},
		"alpha": {
			Name:       "Alpha",
			Source:     "owner/alpha",
			SourceType: "git",
			SourceURL:  "https://github.com/owner/alpha.git",
			Subpath:    "alpha",
			SkillPath:  "alpha/SKILL.md",
			Hash:       "sha256:alpha",
			UpdatedAt:  "2026-01-01T00:00:00Z",
		},
	}}
	if err := WriteLock(cfg, first); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}

	second := Lock{Skills: map[string]LockEntry{
		"alpha": first.Skills["alpha"],
		"zeta":  first.Skills["zeta"],
	}}
	if err := WriteLock(cfg, second); err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("lock writes differ:\nfirst=%s\nsecond=%s", firstBytes, secondBytes)
	}
	if !strings.Contains(string(firstBytes), `"schema_version": 2`) ||
		!strings.Contains(string(firstBytes), `"update_policy": "manual"`) ||
		!strings.Contains(string(firstBytes), `"source_subpath": "skills"`) {
		t.Fatalf("lock metadata missing from deterministic write: %s", firstBytes)
	}
}

func TestSearchSource(t *testing.T) {
	cfg := testConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "source-root")
	writeFile(t, filepath.Join(sourceRoot, "skills", "redis", "SKILL.md"), "---\nname: Redis Cache\ndescription: fast cache\n---\n\nUse Redis for cache storage.\n")
	writeFile(t, filepath.Join(sourceRoot, "skills", "writing", "SKILL.md"), "---\nname: Writing Guide\ndescription: drafting help\n---\n\nPlain English writing guidance.\n")
	writeFile(t, filepath.Join(sourceRoot, "skills", "private", "SKILL.md"), "---\nname: Private Skill\ndescription: secret\n---\n\nHidden.\n")
	cfg.SourceIgnore = append(config.DefaultSourceIgnore, "private")
	cfg.Sources["named"] = config.Source{Name: "named", Source: sourceRoot, Subpath: "skills"}

	found, total, err := SearchSource(cfg, "named", "English", InstallOptions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || total != 1 || found[0].StoreName != "writing-guide" || found[0].SkillPath != "skills/writing/SKILL.md" {
		t.Fatalf("body search = %#v", found)
	}

	pathMatch, total, err := SearchSource(cfg, "named", "skills/redis", InstallOptions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pathMatch) != 1 || total != 1 || pathMatch[0].StoreName != "redis-cache" {
		t.Fatalf("path search = %#v", pathMatch)
	}

	limited, total, err := SearchSource(cfg, "named", "", InstallOptions{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || total != 2 || limited[0].StoreName != "redis-cache" {
		t.Fatalf("limited search = %#v", limited)
	}

	none, total, err := SearchSource(cfg, "named", "absent", InstallOptions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 || total != 0 {
		t.Fatalf("no-match search = %#v", none)
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
	writeFile(t, filepath.Join(fakeBin, "git"), `#!/bin/sh
if [ "$1" = "-C" ]; then
  echo 0123456789abcdef0123456789abcdef01234567
  exit 0
fi
last=""
for arg in "$@"; do last="$arg"; done
mkdir -p "$last/skill"
cat > "$last/skill/SKILL.md" <<'EOF'
---
name: Git Skill
description: cloned
---

From git.
EOF
`)
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
	gitLock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if gitLock.Skills["git-skill"].ResolvedRevision != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("git lock revision = %#v", gitLock.Skills["git-skill"])
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

	discovered, err := DiscoverSkills(source, "")
	if err != nil {
		t.Fatal(err)
	}
	var bare Skill
	for _, skill := range discovered {
		if skill.StoreName == "one" {
			bare = skill
			break
		}
	}
	if bare.StoreName != "one" || bare.Description != "No frontmatter body." {
		t.Fatalf("bare skill defaults = %#v", bare)
	}
	if !strings.Contains(bare.EntrypointText(), "name: \"one\"") ||
		!strings.Contains(bare.EntrypointText(), "description: \"No frontmatter body.\"") ||
		!strings.Contains(bare.EntrypointText(), "No frontmatter body.") {
		t.Fatalf("bare entrypoint = %q", bare.EntrypointText())
	}

	installed, err := InstallSource(cfg, "named", InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].StoreName != "two" {
		t.Fatalf("named install = %#v", installed)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["two"]
	if entry.SourceID == "" ||
		entry.SourceSubpath != "two" ||
		entry.SkillSubpath != "" ||
		entry.Subpath != "two" ||
		entry.UpdatePolicy != config.UpdatePolicyManual {
		t.Fatalf("named lock metadata = %#v", entry)
	}
	if _, err := InstallSource(cfg, source, InstallOptions{SkillFilter: "missing"}); err == nil {
		t.Fatal("missing filtered install unexpectedly succeeded")
	}
	if _, err := InstallSource(cfg, "bad source: value", InstallOptions{}); err == nil {
		t.Fatal("bad source unexpectedly installed")
	}

	all, _, err := FindInstalled(cfg, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("find all = %#v", all)
	}
	none, _, err := FindInstalled(cfg, "absent", 10)
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
	preview, err := PreviewUpdates(emptyCfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview != nil {
		t.Fatalf("empty preview = %#v", preview)
	}
	if _, err := UpdateSkills(cfg, []string{"absent"}); err != nil {
		t.Fatal(err)
	}
	lock, err = ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	entry = lock.Skills["two"]
	entry.SourceSubpath = "missing"
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
	if _, err := PreviewUpdates(cfg, nil, false); err == nil {
		t.Fatal("preview with bad lock unexpectedly succeeded")
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
	if _, err := PreviewUpdates(cfg, nil, false); err == nil {
		t.Fatal("git preview unexpectedly succeeded")
	}
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
	if _, _, err := FindInstalled(config.Config{StoreDir: cfg.LockPath}, "x", 1); err == nil {
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
	if err := walkSkillDirs(source, 7, 6, nil, func(string) error { return nil }); err != nil {
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
	all, total, err := FindInstalled(cfg, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || total != 2 || all[0].StoreName != "nested-skill" {
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

	if err := walkSkillDirs(source, 0, 6, nil, func(string) error { return os.ErrInvalid }); err == nil {
		t.Fatal("walk callback error not returned")
	}
	if _, err := parseHTTPSource(ParsedSource{}, "https://github.com/o/r/tree/main/../bad"); err == nil {
		t.Fatal("parseHTTPSource unsafe path unexpectedly succeeded")
	}
}

func TestGitSourceInspectionSeesNewUpstreamSkill(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cfg := testConfig(t)
	remote := filepath.Join(cfg.Home, "remote.git")
	work := filepath.Join(cfg.Home, "work")
	runGit(t, "", "init", "--bare", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "init")
	writeFile(t, filepath.Join(work, "skills", "one", "SKILL.md"), "---\nname: One\n---\n\nOne.\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "add one")
	firstCommit := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	runGit(t, work, "tag", "v1")
	runGit(t, work, "branch", "-M", "main")
	runGit(t, work, "remote", "add", "origin", remote)
	runGit(t, work, "push", "-u", "origin", "main")
	runGit(t, work, "push", "origin", "v1")
	runGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	source := "file://" + remote
	first, err := InspectSource(cfg, source, InstallOptions{Ref: "main", Subpath: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].StoreName != "one" {
		t.Fatalf("first git inspect = %#v", first)
	}
	defaultBranch, err := InspectSource(cfg, source, InstallOptions{Subpath: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultBranch) != 1 || defaultBranch[0].StoreName != "one" {
		t.Fatalf("default branch inspect = %#v", defaultBranch)
	}
	tagged, err := InspectSource(cfg, source, InstallOptions{Ref: "v1", Subpath: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tagged) != 1 || tagged[0].StoreName != "one" {
		t.Fatalf("tagged inspect = %#v", tagged)
	}
	firstResolved := resolveGitRevisionForTest(t, source, "main")
	if firstResolved != firstCommit {
		t.Fatalf("first branch revision = %s, want %s", firstResolved, firstCommit)
	}

	writeFile(t, filepath.Join(work, "skills", "two", "SKILL.md"), "---\nname: Two\n---\n\nTwo.\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "add two")
	secondCommit := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	runGit(t, work, "tag", "-f", "v1")
	runGit(t, work, "push", "origin", "main")
	runGit(t, work, "push", "--force", "origin", "v1")

	second, err := InspectSource(cfg, source, InstallOptions{Ref: "main", Subpath: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || second[0].StoreName != "one" || second[1].StoreName != "two" {
		t.Fatalf("second git inspect = %#v", second)
	}
	secondResolved := resolveGitRevisionForTest(t, source, "main")
	if secondResolved != secondCommit || secondResolved == firstResolved {
		t.Fatalf("second branch revision = %s, first = %s, want %s", secondResolved, firstResolved, secondCommit)
	}
	pinned, err := InspectSource(cfg, source, InstallOptions{Ref: firstCommit, Subpath: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 1 || pinned[0].StoreName != "one" {
		t.Fatalf("pinned commit inspect = %#v", pinned)
	}
	movedTag, err := InspectSource(cfg, source, InstallOptions{Ref: "v1", Subpath: "skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(movedTag) != 2 || movedTag[0].StoreName != "one" || movedTag[1].StoreName != "two" {
		t.Fatalf("moved tag inspect = %#v", movedTag)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Upskill Test",
		"GIT_AUTHOR_EMAIL=upskill@example.invalid",
		"GIT_COMMITTER_NAME=Upskill Test",
		"GIT_COMMITTER_EMAIL=upskill@example.invalid",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func resolveGitRevisionForTest(t *testing.T, source string, ref string) string {
	t.Helper()
	parsed, err := ParseSource(source)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ApplySourceOverrides(parsed, ref, "", "")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Cleanup()
	return resolved.ResolvedRevision
}

func TestPreviewUpdateErrorsAndDiffHelpers(t *testing.T) {
	cfg := testConfig(t)
	sourceFile := filepath.Join(cfg.Home, "source-file")
	writeFile(t, sourceFile, "not a directory")
	if err := WriteLock(cfg, Lock{Skills: map[string]LockEntry{
		"file-skill": {Name: "File Skill", Source: sourceFile, SourceType: "local", SourceURL: sourceFile},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewUpdates(cfg, nil, false); err == nil {
		t.Fatal("preview with file source unexpectedly succeeded")
	}

	emptySource := filepath.Join(cfg.Home, "empty-source")
	if err := os.MkdirAll(emptySource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteLock(cfg, Lock{Skills: map[string]LockEntry{
		"empty": {Name: "Empty", Source: emptySource, SourceType: "local", SourceURL: emptySource},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewUpdates(cfg, nil, false); err == nil {
		t.Fatal("preview with empty source unexpectedly succeeded")
	}

	skillSource := filepath.Join(cfg.Home, "skill-source")
	writeFile(t, filepath.Join(skillSource, "SKILL.md"), "---\nname: Skill\n---\n\nNew.\n")
	writeFile(t, filepath.Join(cfg.StoreDir, "skill", "SKILL.md"), "---\nname: Skill\n---\n\nOld.\n")
	hash, err := HashDir(filepath.Join(cfg.StoreDir, "skill"))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteLock(cfg, Lock{Skills: map[string]LockEntry{
		"skill": {Name: "Skill", Source: skillSource, SourceType: "local", SourceURL: skillSource, Hash: hash},
	}}); err != nil {
		t.Fatal(err)
	}
	oldHashTree := hashTree
	hashTree = func(string) (string, error) { return "", errors.New("hash failed") }
	_, err = PreviewUpdates(cfg, nil, false)
	hashTree = oldHashTree
	if err == nil {
		t.Fatal("preview hash error unexpectedly succeeded")
	}

	if err := os.RemoveAll(filepath.Join(cfg.StoreDir, "skill")); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewUpdates(cfg, nil, true); err == nil {
		t.Fatal("preview diff error unexpectedly succeeded")
	}

	current := filepath.Join(cfg.Home, "current")
	next := filepath.Join(cfg.Home, "next")
	writeFile(t, filepath.Join(current, "same.txt"), "same")
	writeFile(t, filepath.Join(next, "same.txt"), "same")
	writeFile(t, filepath.Join(next, "added.txt"), "added")
	diff, err := diffUpdateDirs(current, next)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(diff, "\n"), "added.txt") {
		t.Fatalf("added diff = %#v", diff)
	}
	if _, err := diffUpdateDirs(filepath.Join(cfg.Home, "missing"), next); err == nil {
		t.Fatal("missing current diff unexpectedly succeeded")
	}
	if _, err := diffUpdateDirs(current, filepath.Join(cfg.Home, "missing")); err == nil {
		t.Fatal("missing next diff unexpectedly succeeded")
	}

	blockedRoot := filepath.Join(cfg.Home, "blocked-root")
	blockedFile := filepath.Join(blockedRoot, "blocked.txt")
	writeFile(t, blockedFile, "blocked")
	if err := os.Chmod(blockedFile, 0); err != nil {
		t.Fatal(err)
	}
	_, readErr := readUpdateFiles(blockedRoot)
	_ = os.Chmod(blockedFile, 0o644)
	if readErr == nil {
		t.Fatal("blocked read unexpectedly succeeded")
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
	if err := walkSkillDirs(walkRoot, 0, 6, nil, func(string) error { return nil }); err == nil {
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
	if err := walkSkillDirs(filepath.Dir(walkChild), 0, 6, nil, func(string) error { return nil }); err == nil {
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
