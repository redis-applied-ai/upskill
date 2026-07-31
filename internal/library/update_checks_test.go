package library

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"upskill/internal/config"
	"upskill/internal/updatecache"
)

type bareSkillRepo struct {
	remote string
	work   string
}

func newBareSkillRepo(t *testing.T, skills map[string]string) bareSkillRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := bareSkillRepo{
		remote: filepath.Join(root, "remote.git"),
		work:   filepath.Join(root, "work"),
	}
	runGit(t, "", "init", "--bare", repo.remote)
	if err := os.MkdirAll(repo.work, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo.work, "init")
	writeRepoSkills(t, repo, skills)
	runGit(t, repo.work, "add", ".")
	runGit(t, repo.work, "commit", "-m", "initial skills")
	runGit(t, repo.work, "branch", "-M", "main")
	runGit(t, repo.work, "remote", "add", "origin", repo.remote)
	runGit(t, repo.work, "push", "-u", "origin", "main")
	runGit(t, "", "--git-dir", repo.remote, "symbolic-ref", "HEAD", "refs/heads/main")
	return repo
}

func (repo bareSkillRepo) source() string {
	return "file://" + repo.remote
}

func (repo bareSkillRepo) commit(t *testing.T, skills map[string]string, message string) string {
	t.Helper()
	writeRepoSkills(t, repo, skills)
	runGit(t, repo.work, "add", ".")
	runGit(t, repo.work, "commit", "-m", message)
	revision := strings.TrimSpace(runGit(t, repo.work, "rev-parse", "HEAD"))
	runGit(t, repo.work, "push", "origin", "main")
	return revision
}

func writeRepoSkills(t *testing.T, repo bareSkillRepo, skills map[string]string) {
	t.Helper()
	for name, body := range skills {
		display := strings.ToUpper(name[:1]) + name[1:]
		writeFile(t, filepath.Join(repo.work, "skills", name, "SKILL.md"), fmt.Sprintf("---\nname: %s\n---\n\n%s\n", display, body))
	}
}

func installGitCounter(t *testing.T, lsRemoteAction string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	countPath := filepath.Join(root, "operations.log")
	if strings.TrimSpace(lsRemoteAction) == "" {
		lsRemoteAction = ":"
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$1" >> %q
if [ "$1" = "ls-remote" ]; then
  %s
fi
exec %q "$@"
`, countPath, lsRemoteAction, realGit)
	if err := os.WriteFile(filepath.Join(root, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return countPath
}

func gitOperationCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]int{}
	}
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, operation := range strings.Fields(string(data)) {
		counts[operation]++
	}
	return counts
}

func clearGitOperationCounts(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func readUpdateTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func withUpdateCheckTime(t *testing.T, now time.Time) *time.Time {
	t.Helper()
	current := now
	previous := updateCheckNow
	updateCheckNow = func() time.Time { return current }
	t.Cleanup(func() { updateCheckNow = previous })
	return &current
}

func TestUpdateCheckOptionsValidationAndOrdering(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "source")
	writeFile(t, filepath.Join(source, "zeta", "SKILL.md"), "---\nname: Zeta\n---\n\nZ.\n")
	writeFile(t, filepath.Join(source, "alpha", "SKILL.md"), "---\nname: Alpha\n---\n\nA.\n")
	if _, err := InstallSource(cfg, source, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSkillUpdatesWithOptions(cfg, UpdateCheckOptions{Mode: "unknown"}); err == nil {
		t.Fatal("unknown update check mode succeeded")
	}
	if _, err := CheckSkillUpdatesWithOptions(cfg, UpdateCheckOptions{Mode: UpdateCheckPreflight, IncludeDiff: true}); err == nil {
		t.Fatal("preflight diff succeeded")
	}
	live, err := CheckSkillUpdatesWithOptions(cfg, UpdateCheckOptions{Mode: UpdateCheckLive})
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := CheckSkillUpdatesWithOptions(cfg, UpdateCheckOptions{Mode: UpdateCheckPreflight})
	if err != nil {
		t.Fatal(err)
	}
	for _, checks := range [][]UpdateCheck{live.Checks, preflight.Checks} {
		if len(checks) != 2 || checks[0].StoreName != "alpha" || checks[1].StoreName != "zeta" {
			t.Fatalf("checks = %#v", checks)
		}
		if checks[0].CheckOrigin != UpdateCheckOriginLiveLocal || checks[0].CheckedAt == "" {
			t.Fatalf("metadata = %#v", checks[0])
		}
	}
}

func TestManualGitPreflightIsCacheOnlyAndCacheMissIsUnknown(t *testing.T) {
	now := withUpdateCheckTime(t, time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC))
	repo := newBareSkillRepo(t, map[string]string{"redis": "old"})
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = time.Hour
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyManual}); err != nil {
		t.Fatal(err)
	}
	repo.commit(t, map[string]string{"redis": "new"}, "advance manual source")
	*now = now.Add(24 * time.Hour)
	countPath := installGitCounter(t, "")

	result, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if counts := gitOperationCounts(t, countPath); len(counts) != 0 {
		t.Fatalf("manual preflight Git operations = %#v", counts)
	}
	if len(result.Checks) != 1 || result.Checks[0].CheckOrigin != UpdateCheckOriginCache || result.Checks[0].Changed {
		t.Fatalf("manual cached result = %#v", result)
	}

	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	group := groupedGitLockEntries(lock)[0]
	if err := os.Remove(updatecache.New(cfg.Home).Path(group.Identity)); err != nil {
		t.Fatal(err)
	}
	result, err = CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checks) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("manual cache miss = %#v", result)
	}
	if counts := gitOperationCounts(t, countPath); len(counts) != 0 {
		t.Fatalf("manual cache miss Git operations = %#v", counts)
	}
}

func TestAutomaticGitPreflightFreshUnchangedAndChangedPaths(t *testing.T) {
	now := withUpdateCheckTime(t, time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC))
	repo := newBareSkillRepo(t, map[string]string{"one": "old one", "two": "old two"})
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = time.Hour
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	countPath := installGitCounter(t, "")

	fresh, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if counts := gitOperationCounts(t, countPath); len(counts) != 0 {
		t.Fatalf("fresh cache Git operations = %#v", counts)
	}
	if len(fresh.Checks) != 2 || fresh.Checks[0].CheckOrigin != UpdateCheckOriginCache {
		t.Fatalf("fresh result = %#v", fresh)
	}

	*now = now.Add(2 * time.Hour)
	unchanged, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	counts := gitOperationCounts(t, countPath)
	if counts["ls-remote"] != 1 || counts["clone"] != 0 {
		t.Fatalf("unchanged operations = %#v", counts)
	}
	if len(unchanged.Checks) != 2 || unchanged.Checks[0].CheckOrigin != UpdateCheckOriginLockRevision || unchanged.Checks[0].Changed {
		t.Fatalf("unchanged result = %#v", unchanged)
	}

	clearGitOperationCounts(t, countPath)
	repo.commit(t, map[string]string{"one": "new one", "two": "new two"}, "advance auto source")
	clearGitOperationCounts(t, countPath)
	*now = now.Add(2 * time.Hour)
	changed, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	counts = gitOperationCounts(t, countPath)
	if counts["ls-remote"] != 1 || counts["clone"] != 1 {
		t.Fatalf("changed operations = %#v", counts)
	}
	if len(changed.Checks) != 2 || !changed.Checks[0].Changed || !changed.Checks[1].Changed {
		t.Fatalf("changed result = %#v", changed)
	}
}

func TestPinnedCommitPreflightRunsNoGitCommand(t *testing.T) {
	now := withUpdateCheckTime(t, time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC))
	repo := newBareSkillRepo(t, map[string]string{"redis": "pinned"})
	revision := strings.TrimSpace(runGit(t, repo.work, "rev-parse", "HEAD"))
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = 0
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: revision, Subpath: "skills", SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	countPath := installGitCounter(t, "")
	result, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if counts := gitOperationCounts(t, countPath); len(counts) != 0 {
		t.Fatalf("pinned preflight operations = %#v", counts)
	}
	if len(result.Checks) != 1 || result.Checks[0].CheckOrigin != UpdateCheckOriginLockRevision {
		t.Fatalf("pinned result = %#v", result)
	}
}

func TestRemoteRevisionProbeResolvesBranchesAndTags(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"redis": "one"})
	first := strings.TrimSpace(runGit(t, repo.work, "rev-parse", "HEAD"))
	runGit(t, repo.work, "tag", "light")
	runGit(t, repo.work, "tag", "-a", "annotated", "-m", "annotated tag")
	runGit(t, repo.work, "push", "origin", "light", "annotated")

	for name, ref := range map[string]string{"default": "", "branch": "main", "lightweight": "light", "annotated": "annotated"} {
		t.Run(name, func(t *testing.T) {
			got, err := probeRemoteRevision(repo.source(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if got != first {
				t.Fatalf("revision = %s, want %s", got, first)
			}
		})
	}
	if _, err := probeRemoteRevision(repo.source(), "missing"); err == nil {
		t.Fatal("missing ref resolved")
	}
}

func TestLiveCheckAndUpdateMaterializeSharedGitGroupOnce(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"one": "old one", "two": "old two"})
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = time.Hour
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	repo.commit(t, map[string]string{"one": "new one", "two": "new two"}, "advance shared source")
	countPath := installGitCounter(t, "")

	checks, err := CheckSkillUpdates(cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 || !checks[0].Changed || !checks[1].Changed {
		t.Fatalf("checks = %#v", checks)
	}
	if counts := gitOperationCounts(t, countPath); counts["clone"] != 1 {
		t.Fatalf("live check operations = %#v", counts)
	}

	clearGitOperationCounts(t, countPath)
	updated, err := UpdateSkills(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 2 {
		t.Fatalf("updated = %#v", updated)
	}
	if counts := gitOperationCounts(t, countPath); counts["clone"] != 1 {
		t.Fatalf("grouped update operations = %#v", counts)
	}

	clearGitOperationCounts(t, countPath)
	result, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if counts := gitOperationCounts(t, countPath); len(counts) != 0 {
		t.Fatalf("post-update preflight operations = %#v", counts)
	}
	if len(result.Checks) != 2 || result.Checks[0].Changed || result.Checks[1].Changed {
		t.Fatalf("post-update result = %#v", result)
	}
}

func TestMultipleRefsFromOneRepositoryUseSeparateGroups(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"one": "main"})
	runGit(t, repo.work, "checkout", "-b", "preview")
	writeRepoSkills(t, repo, map[string]string{"two": "preview"})
	runGit(t, repo.work, "add", ".")
	runGit(t, repo.work, "commit", "-m", "add preview skill")
	runGit(t, repo.work, "push", "-u", "origin", "preview")
	runGit(t, repo.work, "checkout", "main")

	cfg := testConfig(t)
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "preview", Subpath: "skills", SkillFilter: "two"}); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	groups := groupedGitLockEntries(lock)
	if len(groups) != 2 || groups[0].Identity == groups[1].Identity {
		t.Fatalf("groups = %#v", groups)
	}

	countPath := installGitCounter(t, "")
	checks, err := CheckSkillUpdates(cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks = %#v", checks)
	}
	if counts := gitOperationCounts(t, countPath); counts["clone"] != 2 {
		t.Fatalf("separate-ref operations = %#v", counts)
	}
}

func TestSharedGitCheckoutResolvesSourceAndSkillSubpaths(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"seed": "seed"})
	writeFile(t, filepath.Join(repo.work, "areas", "one", "catalog", "alpha", "SKILL.md"), "---\nname: Alpha\n---\n\nOld alpha.\n")
	writeFile(t, filepath.Join(repo.work, "areas", "two", "beta", "SKILL.md"), "---\nname: Beta\n---\n\nOld beta.\n")
	runGit(t, repo.work, "add", ".")
	runGit(t, repo.work, "commit", "-m", "add nested skills")
	runGit(t, repo.work, "push", "origin", "main")

	cfg := testConfig(t)
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "areas/one", SkillFilter: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "areas/two", SkillFilter: "beta"}); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Skills["alpha"]; got.SourceSubpath != "areas/one" || got.SkillSubpath != "catalog/alpha" {
		t.Fatalf("alpha lock entry = %#v", got)
	}
	if got := lock.Skills["beta"]; got.SourceSubpath != "areas/two" || got.SkillSubpath != "beta" {
		t.Fatalf("beta lock entry = %#v", got)
	}
	if groups := groupedGitLockEntries(lock); len(groups) != 1 || len(groups[0].Keys) != 2 {
		t.Fatalf("groups = %#v", groups)
	}

	writeFile(t, filepath.Join(repo.work, "areas", "one", "catalog", "alpha", "SKILL.md"), "---\nname: Alpha\n---\n\nNew alpha.\n")
	writeFile(t, filepath.Join(repo.work, "areas", "two", "beta", "SKILL.md"), "---\nname: Beta\n---\n\nNew beta.\n")
	runGit(t, repo.work, "add", ".")
	runGit(t, repo.work, "commit", "-m", "update nested skills")
	runGit(t, repo.work, "push", "origin", "main")
	countPath := installGitCounter(t, "")

	checks, err := CheckSkillUpdates(cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 || !checks[0].Changed || !checks[1].Changed {
		t.Fatalf("checks = %#v", checks)
	}
	if counts := gitOperationCounts(t, countPath); counts["clone"] != 1 {
		t.Fatalf("shared-subpath operations = %#v", counts)
	}
}

func TestAutomaticGitFailureUsesStaleCacheAndBackoff(t *testing.T) {
	now := withUpdateCheckTime(t, time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC))
	repo := newBareSkillRepo(t, map[string]string{"redis": "old"})
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = time.Hour
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Hour)
	countPath := installGitCounter(t, "exit 1")

	first, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Checks) != 1 || !first.Checks[0].Stale || len(first.Warnings) != 1 || first.Warnings[0].Type != UpdateCheckWarningRemoteDeferred {
		t.Fatalf("first failure result = %#v", first)
	}
	if counts := gitOperationCounts(t, countPath); counts["ls-remote"] != 1 {
		t.Fatalf("first failure operations = %#v", counts)
	}

	clearGitOperationCounts(t, countPath)
	second, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Checks) != 1 || !second.Checks[0].Stale || len(second.Warnings) != 1 {
		t.Fatalf("backoff result = %#v", second)
	}
	if counts := gitOperationCounts(t, countPath); len(counts) != 0 {
		t.Fatalf("backoff operations = %#v", counts)
	}
}

func TestLocalManualAndAutomaticPreflightRemainLive(t *testing.T) {
	cfg := testConfig(t)
	manualSource := filepath.Join(cfg.Home, "manual-source")
	autoSource := filepath.Join(cfg.Home, "auto-source")
	writeFile(t, filepath.Join(manualSource, "manual", "SKILL.md"), "---\nname: Manual\n---\n\nOld manual.\n")
	writeFile(t, filepath.Join(autoSource, "automatic", "SKILL.md"), "---\nname: Automatic\n---\n\nOld automatic.\n")
	if _, err := InstallSource(cfg, manualSource, InstallOptions{SkillFilter: "manual", UpdatePolicy: config.UpdatePolicyManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSource(cfg, autoSource, InstallOptions{SkillFilter: "automatic", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(manualSource, "manual", "SKILL.md"), "---\nname: Manual\n---\n\nNew manual.\n")
	writeFile(t, filepath.Join(autoSource, "automatic", "SKILL.md"), "---\nname: Automatic\n---\n\nNew automatic.\n")

	preflight, err := CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(preflight.Checks) != 2 {
		t.Fatalf("preflight = %#v", preflight)
	}
	for _, check := range preflight.Checks {
		if check.CheckOrigin != UpdateCheckOriginLiveLocal || !check.Changed {
			t.Fatalf("local check = %#v", check)
		}
	}

	result, err := AutoUpdateSkills(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Updated) != 1 || result.Updated[0].StoreName != "automatic" {
		t.Fatalf("automatic update result = %#v", result)
	}
	if got := readUpdateTestFile(t, filepath.Join(cfg.StoreDir, "automatic", "SKILL.md")); !strings.Contains(got, "New automatic.") {
		t.Fatalf("automatic store = %s", got)
	}
	if got := readUpdateTestFile(t, filepath.Join(cfg.StoreDir, "manual", "SKILL.md")); !strings.Contains(got, "Old manual.") {
		t.Fatalf("manual store = %s", got)
	}
}

func TestAutomaticUpdateApplicationFailureRemainsFatal(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "auto-source")
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	if _, err := InstallSource(cfg, source, InstallOptions{SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")
	blockedStore := filepath.Join(t.TempDir(), "store-file")
	if err := os.WriteFile(blockedStore, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.StoreDir = blockedStore

	result, err := AutoUpdateSkills(cfg)
	if err == nil {
		t.Fatalf("automatic update unexpectedly succeeded: %#v", result)
	}
	if len(result.Checks) != 1 || !result.Checks[0].Changed || len(result.Updated) != 0 {
		t.Fatalf("automatic failure result = %#v", result)
	}
}

func TestPreflightProbeTimeoutIsBounded(t *testing.T) {
	now := withUpdateCheckTime(t, time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC))
	repo := newBareSkillRepo(t, map[string]string{"redis": "old"})
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = time.Hour
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Hour)
	installGitCounter(t, "exec sleep 3")

	start := time.Now()
	result, err := CheckSkillUpdatesForPreflight(cfg)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 2800*time.Millisecond {
		t.Fatalf("preflight timeout took %s", elapsed)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Type != UpdateCheckWarningRemoteDeferred {
		t.Fatalf("timeout result = %#v", result)
	}
}

func TestCacheFingerprintMismatchAndCorruptionAreMisses(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"redis": "old"})
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = time.Hour
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyManual}); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	group := groupedGitLockEntries(lock)[0]
	lock.Skills["redis"] = func() LockEntry {
		entry := lock.Skills["redis"]
		entry.UpdatePolicy = config.UpdatePolicyAuto
		return entry
	}()
	if err := WriteLock(cfg, lock); err != nil {
		t.Fatal(err)
	}
	countPath := installGitCounter(t, "")
	result, err := CheckSkillUpdatesWithOptions(cfg, UpdateCheckOptions{Mode: UpdateCheckPreflight, Names: []string{"redis"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checks) != 1 || result.Checks[0].CheckOrigin != UpdateCheckOriginLockRevision {
		t.Fatalf("fingerprint mismatch result = %#v", result)
	}
	if counts := gitOperationCounts(t, countPath); counts["ls-remote"] != 1 {
		t.Fatalf("fingerprint mismatch operations = %#v", counts)
	}

	if err := os.WriteFile(updatecache.New(cfg.Home).Path(group.Identity), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock.Skills["redis"] = func() LockEntry {
		entry := lock.Skills["redis"]
		entry.UpdatePolicy = config.UpdatePolicyManual
		return entry
	}()
	if err := WriteLock(cfg, lock); err != nil {
		t.Fatal(err)
	}
	result, err = CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checks) != 0 || len(result.Warnings) != 1 || result.Warnings[0].Type != UpdateCheckWarningCacheIgnored {
		t.Fatalf("corrupt cache result = %#v", result)
	}
	if _, err := os.Stat(updatecache.New(cfg.Home).Path(group.Identity)); err != nil {
		t.Fatalf("corrupt cache was deleted: %v", err)
	}
}

func TestLockRelevantChangesInvalidateGitCache(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"redis": "old"})
	cfg := testConfig(t)
	cfg.PreflightCheckTTL = time.Hour
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyManual}); err != nil {
		t.Fatal(err)
	}
	original, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	group := groupedGitLockEntries(original)[0]
	cachePath := updatecache.New(cfg.Home).Path(group.Identity)
	cacheData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		mutate func(LockEntry) LockEntry
	}{
		{"hash", func(entry LockEntry) LockEntry { entry.Hash = "sha256:different"; return entry }},
		{"revision", func(entry LockEntry) LockEntry { entry.ResolvedRevision = strings.Repeat("a", 40); return entry }},
		{"source-subpath", func(entry LockEntry) LockEntry { entry.SourceSubpath = "other"; return entry }},
		{"skill-subpath", func(entry LockEntry) LockEntry { entry.SkillSubpath = "other"; return entry }},
		{"source-url", func(entry LockEntry) LockEntry { entry.SourceURL += "-other"; return entry }},
		{"ref", func(entry LockEntry) LockEntry { entry.Ref = "other"; return entry }},
		{"policy", func(entry LockEntry) LockEntry { entry.UpdatePolicy = config.UpdatePolicyAuto; return entry }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := original
			changed.Skills = map[string]LockEntry{}
			for key, entry := range original.Skills {
				changed.Skills[key] = entry
			}
			changed.Skills["redis"] = tc.mutate(changed.Skills["redis"])
			if err := WriteLock(cfg, changed); err != nil {
				t.Fatal(err)
			}
			result, err := CheckSkillUpdatesForPreflight(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range result.Checks {
				if check.CheckOrigin == UpdateCheckOriginCache {
					t.Fatalf("changed %s reused cache: %#v", tc.name, result)
				}
			}
			if err := WriteLock(cfg, original); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cachePath, cacheData, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPartialNamedLiveCheckNeverCreatesAnIncompleteRecord(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"one": "old one", "two": "old two"})
	cfg := testConfig(t)
	if _, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", UpdatePolicy: config.UpdatePolicyManual}); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	group := groupedGitLockEntries(lock)[0]
	repo.commit(t, map[string]string{"one": "new one", "two": "new two"}, "advance both skills")

	result, err := CheckSkillUpdatesWithOptions(cfg, UpdateCheckOptions{
		Names: []string{"one"},
		Mode:  UpdateCheckLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checks) != 1 || !result.Checks[0].Changed {
		t.Fatalf("named result = %#v", result)
	}
	record, found, err := updatecache.New(cfg.Home).Read(group.Identity)
	if err != nil || !found {
		t.Fatalf("complete record read: found=%t err=%v", found, err)
	}
	if len(record.Checks) != 2 {
		t.Fatalf("partial check replaced complete record: %#v", record)
	}

	if err := os.Remove(updatecache.New(cfg.Home).Path(group.Identity)); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSkillUpdatesWithOptions(cfg, UpdateCheckOptions{
		Names: []string{"one"},
		Mode:  UpdateCheckLive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := updatecache.New(cfg.Home).Read(group.Identity); err != nil || found {
		t.Fatalf("partial check created cache: found=%t err=%v", found, err)
	}
}

func TestCacheWriteFailureDoesNotChangeAuthoritativeResults(t *testing.T) {
	repo := newBareSkillRepo(t, map[string]string{"redis": "old"})
	cfg := testConfig(t)
	badHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badHome, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Home = badHome
	installed, err := InstallSource(cfg, repo.source(), InstallOptions{Ref: "main", Subpath: "skills", SkillFilter: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 {
		t.Fatalf("installed = %#v", installed)
	}
	checks, err := CheckSkillUpdates(cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Error != "" {
		t.Fatalf("checks = %#v", checks)
	}
	repo.commit(t, map[string]string{"redis": "new"}, "advance source")
	updated, err := UpdateSkills(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || updated[0].StoreName != "redis" {
		t.Fatalf("updated = %#v", updated)
	}
}
