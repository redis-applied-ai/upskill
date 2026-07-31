package activity

import (
	"os"
	"path/filepath"
	"testing"

	"upskill/internal/config"
	"upskill/internal/library"
)

func TestUpdateChangesSkillHashButPreservesSkillKey(t *testing.T) {
	cfg := identityTestConfig(t)
	writeInstalledSkill(t, cfg, "redis", "Redis")
	entry := library.LockEntry{
		Name:          "Redis",
		Source:        "owner/repo",
		SourceID:      "src_stable",
		SourceType:    "git",
		SourceURL:     "https://github.com/owner/repo.git",
		SourceSubpath: "skills",
		SkillSubpath:  "redis",
		Hash:          "sha256:old",
	}
	writeIdentityTestLock(t, cfg, "redis", entry)
	before, err := ResolveSkill(cfg, "Redis")
	if err != nil {
		t.Fatal(err)
	}

	entry.Hash = "sha256:new"
	writeIdentityTestLock(t, cfg, "redis", entry)
	after, err := ResolveSkill(cfg, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if before.SkillKey != after.SkillKey {
		t.Fatalf("skill key changed across update: %s != %s", before.SkillKey, after.SkillKey)
	}
	if before.SkillHash != "sha256:old" || after.SkillHash != "sha256:new" {
		t.Fatalf("skill hashes = %q, %q", before.SkillHash, after.SkillHash)
	}
}

func TestDifferentSourceUnderSameStoreNameChangesSkillKey(t *testing.T) {
	first := library.LockEntry{SourceID: "src_first", SourceSubpath: "skills", SkillSubpath: "redis"}
	second := first
	second.SourceID = "src_second"
	firstKey, err := skillKeyFromLockEntry(first, "redis")
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := skillKeyFromLockEntry(second, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if firstKey == secondKey {
		t.Fatalf("different source identities produced one key: %s", firstKey)
	}
}

func TestConfiguredSourceRenamePreservesStableSourceIdentity(t *testing.T) {
	before := library.LockEntry{
		Source:        "old-display-name",
		SourceID:      "src_stable",
		SourceType:    "local",
		SourceURL:     "/skills/team",
		SourceSubpath: "catalog",
		SkillSubpath:  "redis",
	}
	after := before
	after.Source = "new-display-name"
	beforeKey, err := skillKeyFromLockEntry(before, "redis")
	if err != nil {
		t.Fatal(err)
	}
	afterKey, err := skillKeyFromLockEntry(after, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if beforeKey != afterKey {
		t.Fatalf("configured source rename changed key: %s != %s", beforeKey, afterKey)
	}
}

func TestUnlockedFallbackIdentityUsesStorePathAndSeparatesLaterLock(t *testing.T) {
	cfg := identityTestConfig(t)
	writeInstalledSkill(t, cfg, "redis", "Redis")
	before, err := ResolveSkill(cfg, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if before.SkillKey == "" || before.SkillHash == "" || before.SourceID != "" {
		t.Fatalf("unlocked identity = %#v", before)
	}

	if err := os.WriteFile(filepath.Join(cfg.StoreDir, "redis", "notes.md"), []byte("new revision"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterContentChange, err := ResolveSkill(cfg, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if before.SkillKey != afterContentChange.SkillKey || before.SkillHash == afterContentChange.SkillHash {
		t.Fatalf("unlocked update identity before=%#v after=%#v", before, afterContentChange)
	}

	entry := library.LockEntry{SourceID: "src_later", Source: "owner/repo", SourceType: "git", SourceURL: "https://github.com/owner/repo.git", SkillSubpath: "redis", Hash: afterContentChange.SkillHash}
	writeIdentityTestLock(t, cfg, "redis", entry)
	locked, err := ResolveSkill(cfg, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if locked.SkillKey == before.SkillKey {
		t.Fatalf("later lock did not start a separate identity: %s", locked.SkillKey)
	}
}

func identityTestConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{
		Home:      root,
		StoreDir:  filepath.Join(root, "store"),
		LockPath:  filepath.Join(root, "upskill-lock.json"),
		StatsPath: filepath.Join(root, "upskill-stats.jsonl"),
	}
}

func writeInstalledSkill(t *testing.T, cfg config.Config, storeName, name string) {
	t.Helper()
	dir := filepath.Join(cfg.StoreDir, storeName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: Test skill\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeIdentityTestLock(t *testing.T, cfg config.Config, storeName string, entry library.LockEntry) {
	t.Helper()
	if err := library.WriteLock(cfg, library.Lock{Skills: map[string]library.LockEntry{storeName: entry}}); err != nil {
		t.Fatal(err)
	}
}
