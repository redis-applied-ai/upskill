package library

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"upskill/internal/config"
	"upskill/internal/updatecache"
)

const (
	preflightGitTimeout = 2 * time.Second
	preflightRetryDelay = 5 * time.Minute
)

var updateCheckNow = time.Now

type updateCheckGroup struct {
	Identity    string
	SourceKey   string
	Fingerprint string
	Keys        []string
	Entries     map[string]LockEntry
}

type materializedUpdateGroup struct {
	Identity string
	Root     string
	Revision string
}

func updateSkillsGrouped(cfg config.Config, names []string) ([]InstalledSkill, error) {
	lock, err := ReadLock(cfg)
	if err != nil {
		return nil, err
	}
	selected := selectedLockKeys(lock, names)
	if len(selected) == 0 {
		return nil, nil
	}
	selectedSet := stringSet(selected)
	var updated []InstalledSkill
	var materialized []materializedUpdateGroup
	var cleanups []func()
	defer func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	for _, key := range selected {
		entry := lock.Skills[key]
		if entry.SourceType != "local" {
			continue
		}
		result, newEntry, err := updateOneLockEntry(cfg, key, entry, nil)
		if err != nil {
			return nil, err
		}
		lock.Skills[key] = newEntry
		if newEntry.Hash != entry.Hash {
			updated = append(updated, result)
		}
	}
	for _, group := range groupedGitLockEntries(lock) {
		groupSelected := intersectSorted(group.Keys, selectedSet)
		if len(groupSelected) == 0 {
			continue
		}
		entry := group.Entries[group.Keys[0]]
		parsed := parsedSourceForLockEntry(group.Keys[0], entry)
		parsed.Subpath = ""
		resolved, err := ResolveMaterializedSource(parsed)
		if err != nil {
			return nil, err
		}
		cleanups = append(cleanups, resolved.Cleanup)
		for _, key := range groupSelected {
			entry := lock.Skills[key]
			result, newEntry, err := updateOneLockEntry(cfg, key, entry, &resolved)
			if err != nil {
				return nil, err
			}
			lock.Skills[key] = newEntry
			if newEntry.Hash != entry.Hash {
				updated = append(updated, result)
			}
		}
		materialized = append(materialized, materializedUpdateGroup{
			Identity: group.Identity,
			Root:     resolved.Root,
			Revision: resolved.ResolvedRevision,
		})
	}
	if err := WriteLock(cfg, lock); err != nil {
		return nil, err
	}
	checkedAt := updateCheckNow().UTC().Format(time.RFC3339)
	for _, item := range materialized {
		group, ok := gitGroupByIdentity(lock, item.Identity)
		if !ok {
			continue
		}
		checks := checkEntriesFromRoot(cfg, group, group.Keys, item.Root, item.Revision, false, UpdateCheckOriginLiveGit, checkedAt)
		if !hasCheckErrors(checks) {
			_ = writeCompletePreflightCache(cfg, group, checks, item.Revision, checkedAt)
		}
	}
	sort.Slice(updated, func(i, j int) bool {
		return updated[i].StoreName < updated[j].StoreName
	})
	return updated, nil
}

func updateOneLockEntry(
	cfg config.Config,
	key string,
	entry LockEntry,
	shared *MaterializedSource,
) (InstalledSkill, LockEntry, error) {
	parsed := parsedSourceForLockEntry(key, entry)
	var resolved MaterializedSource
	if shared == nil {
		var err error
		resolved, err = ResolveMaterializedSource(parsed)
		if err != nil {
			return InstalledSkill{}, LockEntry{}, err
		}
		defer resolved.Cleanup()
	} else {
		resolved = *shared
	}
	parsed.ResolvedRevision = resolved.ResolvedRevision
	skills, err := DiscoverSkillsWithIgnore(resolved.Root, parsed.Subpath, cfg.SourceIgnore)
	if err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	skills = filterSkills(skills, key)
	if len(skills) == 0 {
		return InstalledSkill{}, LockEntry{}, fmt.Errorf("locked skill not found in source: %s", key)
	}
	result, newEntry, err := installSkill(cfg, rootFromEntry(parsed, resolved.Root), parsed, skills[0])
	if err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	newEntry.Source = entry.Source
	if newEntry.SourceID == "" {
		newEntry.SourceID = entry.SourceID
	}
	newEntry.UpdatePolicy = entry.EffectiveUpdatePolicy()
	return result, newEntry, nil
}

func seedGitCacheFromRoot(cfg config.Config, lock Lock, entry LockEntry, root string, revision string) {
	identity := updatecache.Identity(entry.SourceType, entry.SourceURL, entry.Ref)
	group, ok := gitGroupByIdentity(lock, identity)
	if !ok {
		return
	}
	checkedAt := updateCheckNow().UTC().Format(time.RFC3339)
	checks := checkEntriesFromRoot(cfg, group, group.Keys, root, revision, false, UpdateCheckOriginLiveGit, checkedAt)
	if hasCheckErrors(checks) {
		return
	}
	_ = writeCompletePreflightCache(cfg, group, checks, revision, checkedAt)
}

func gitGroupByIdentity(lock Lock, identity string) (updateCheckGroup, bool) {
	for _, group := range groupedGitLockEntries(lock) {
		if group.Identity == identity {
			return group, true
		}
	}
	return updateCheckGroup{}, false
}

func checkSkillUpdatesLiveGrouped(cfg config.Config, names []string, includeDiff bool) ([]UpdateCheck, []UpdateCheckWarning, error) {
	lock, err := ReadLock(cfg)
	if err != nil {
		return nil, nil, err
	}
	selected := selectedLockKeys(lock, names)
	if len(selected) == 0 {
		return nil, nil, nil
	}
	selectedSet := stringSet(selected)
	checkedAt := updateCheckNow().UTC().Format(time.RFC3339)
	checks := make([]UpdateCheck, 0, len(selected))
	var warnings []UpdateCheckWarning

	for _, key := range selected {
		entry := lock.Skills[key]
		if entry.SourceType != "local" {
			continue
		}
		checks = append(checks, checkLocalEntry(cfg, key, entry, includeDiff, checkedAt))
	}
	for _, group := range groupedGitLockEntries(lock) {
		groupSelected := intersectSorted(group.Keys, selectedSet)
		if len(groupSelected) == 0 {
			continue
		}
		groupChecks, revision, err := checkLiveGitGroup(cfg, group, groupSelected, includeDiff, checkedAt)
		if err != nil {
			for _, key := range groupSelected {
				check := baseUpdateCheck(key, group.Entries[key], UpdateCheckOriginLiveGit, checkedAt)
				check.Error = err.Error()
				checks = append(checks, check)
			}
			continue
		}
		checks = append(checks, groupChecks...)
		if warning := writeGroupCache(cfg, group, groupSelected, groupChecks, revision, checkedAt); warning != nil {
			warnings = append(warnings, *warning)
		}
	}
	sortUpdateChecks(checks)
	sortUpdateWarnings(warnings)
	return checks, warnings, nil
}

func checkSkillUpdatesPreflight(cfg config.Config, names []string) (UpdateCheckResult, error) {
	lock, err := ReadLock(cfg)
	if err != nil {
		return UpdateCheckResult{}, err
	}
	selected := selectedLockKeys(lock, names)
	if len(selected) == 0 {
		return UpdateCheckResult{}, nil
	}
	selectedSet := stringSet(selected)
	now := updateCheckNow().UTC()
	checkedAt := now.Format(time.RFC3339)
	result := UpdateCheckResult{Checks: make([]UpdateCheck, 0, len(selected))}

	for _, key := range selected {
		entry := lock.Skills[key]
		if entry.SourceType != "local" {
			continue
		}
		result.Checks = append(result.Checks, checkLocalEntry(cfg, key, entry, false, checkedAt))
	}
	for _, group := range groupedGitLockEntries(lock) {
		groupSelected := intersectSorted(group.Keys, selectedSet)
		if len(groupSelected) == 0 {
			continue
		}
		groupResult := checkGitGroupPreflight(cfg, group, groupSelected, now)
		result.Checks = append(result.Checks, groupResult.Checks...)
		result.Warnings = append(result.Warnings, groupResult.Warnings...)
	}
	sortUpdateChecks(result.Checks)
	sortUpdateWarnings(result.Warnings)
	return result, nil
}

func selectedLockKeys(lock Lock, names []string) []string {
	requested := map[string]struct{}{}
	for _, name := range names {
		requested[NormalizeName(name)] = struct{}{}
	}
	keys := make([]string, 0, len(lock.Skills))
	for key := range lock.Skills {
		if len(requested) == 0 {
			keys = append(keys, key)
			continue
		}
		if _, ok := requested[key]; ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func groupedGitLockEntries(lock Lock) []updateCheckGroup {
	byIdentity := map[string]*updateCheckGroup{}
	for key, entry := range lock.Skills {
		if entry.SourceType == "local" {
			continue
		}
		identity := updatecache.Identity(entry.SourceType, entry.SourceURL, entry.Ref)
		group := byIdentity[identity]
		if group == nil {
			group = &updateCheckGroup{
				Identity:  identity,
				SourceKey: updatecache.SourceKey(identity),
				Entries:   map[string]LockEntry{},
			}
			byIdentity[identity] = group
		}
		group.Keys = append(group.Keys, key)
		group.Entries[key] = entry
	}
	identities := make([]string, 0, len(byIdentity))
	for identity := range byIdentity {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	groups := make([]updateCheckGroup, 0, len(identities))
	for _, identity := range identities {
		group := byIdentity[identity]
		sort.Strings(group.Keys)
		group.Fingerprint = lockGroupFingerprint(*group)
		groups = append(groups, *group)
	}
	return groups
}

func lockGroupFingerprint(group updateCheckGroup) string {
	entries := make([]updatecache.FingerprintEntry, 0, len(group.Keys))
	for _, key := range group.Keys {
		entry := group.Entries[key]
		entries = append(entries, updatecache.FingerprintEntry{
			StoreName:        key,
			SourceType:       entry.SourceType,
			SourceURL:        entry.SourceURL,
			Ref:              entry.Ref,
			SourceSubpath:    entry.EffectiveSourceSubpath(),
			SkillSubpath:     entry.EffectiveSkillSubpath(),
			ResolvedRevision: entry.ResolvedRevision,
			Hash:             entry.Hash,
			UpdatePolicy:     entry.EffectiveUpdatePolicy(),
		})
	}
	return updatecache.Fingerprint(entries)
}

func checkLocalEntry(cfg config.Config, key string, entry LockEntry, includeDiff bool, checkedAt string) UpdateCheck {
	check := baseUpdateCheck(key, entry, UpdateCheckOriginLiveLocal, checkedAt)
	parsed := parsedSourceForLockEntry(key, entry)
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	defer resolved.Cleanup()
	check.ResolvedRevision = resolved.ResolvedRevision
	return checkEntryFromRoot(cfg, key, entry, resolved.Root, resolved.ResolvedRevision, includeDiff, check)
}

func checkLiveGitGroup(cfg config.Config, group updateCheckGroup, keys []string, includeDiff bool, checkedAt string) ([]UpdateCheck, string, error) {
	entry := group.Entries[group.Keys[0]]
	parsed := parsedSourceForLockEntry(group.Keys[0], entry)
	parsed.Subpath = ""
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		return nil, "", err
	}
	defer resolved.Cleanup()
	checks := checkEntriesFromRoot(cfg, group, keys, resolved.Root, resolved.ResolvedRevision, includeDiff, UpdateCheckOriginLiveGit, checkedAt)
	return checks, resolved.ResolvedRevision, nil
}

func checkEntriesFromRoot(
	cfg config.Config,
	group updateCheckGroup,
	keys []string,
	root string,
	resolvedRevision string,
	includeDiff bool,
	origin string,
	checkedAt string,
) []UpdateCheck {
	checks := make([]UpdateCheck, 0, len(keys))
	for _, key := range keys {
		entry := group.Entries[key]
		check := baseUpdateCheck(key, entry, origin, checkedAt)
		check.ResolvedRevision = resolvedRevision
		checks = append(checks, checkEntryFromRoot(cfg, key, entry, root, resolvedRevision, includeDiff, check))
	}
	return checks
}

func checkEntryFromRoot(
	cfg config.Config,
	key string,
	entry LockEntry,
	root string,
	resolvedRevision string,
	includeDiff bool,
	check UpdateCheck,
) UpdateCheck {
	skills, err := DiscoverSkillsWithIgnore(root, entry.EffectiveSourceSubpath(), cfg.SourceIgnore)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	skills = filterSkills(skills, key)
	if len(skills) == 0 {
		check.Error = "locked skill not found in source"
		return check
	}
	check.Name = skills[0].Name
	check.ResolvedRevision = resolvedRevision
	newHash, err := hashTree(skills[0].Path)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	check.NewHash = newHash
	check.Changed = entry.Hash != newHash
	if includeDiff && check.Changed {
		diff, err := diffUpdateDirs(filepath.Join(cfg.StoreDir, key), skills[0].Path)
		if err != nil {
			check.Error = err.Error()
			return check
		}
		check.Diff = diff
	}
	return check
}

func baseUpdateCheck(key string, entry LockEntry, origin string, checkedAt string) UpdateCheck {
	return UpdateCheck{
		StoreName:       key,
		Name:            entry.Name,
		Source:          entry.Source,
		SourceID:        entry.SourceID,
		SourceType:      entry.SourceType,
		SourceURL:       entry.SourceURL,
		Ref:             entry.Ref,
		UpdatePolicy:    entry.EffectiveUpdatePolicy(),
		CurrentRevision: entry.ResolvedRevision,
		CurrentHash:     entry.Hash,
		CheckOrigin:     origin,
		CheckedAt:       checkedAt,
	}
}

func parsedSourceForLockEntry(key string, entry LockEntry) ParsedSource {
	return ParsedSource{
		Input:            entry.Source,
		Type:             entry.SourceType,
		URL:              entry.SourceURL,
		LocalPath:        entry.SourceURL,
		Ref:              entry.Ref,
		Subpath:          entry.EffectiveSourceSubpath(),
		SkillFilter:      key,
		SourceID:         entry.SourceID,
		UpdatePolicy:     entry.EffectiveUpdatePolicy(),
		ResolvedRevision: entry.ResolvedRevision,
	}
}

func checkGitGroupPreflight(cfg config.Config, group updateCheckGroup, selected []string, now time.Time) UpdateCheckResult {
	store := updatecache.New(cfg.Home)
	record, found, readErr := store.Read(group.Identity)
	valid := found && readErr == nil && record.LockFingerprint == group.Fingerprint
	result := UpdateCheckResult{}
	if readErr != nil {
		result.Warnings = append(result.Warnings, cacheIgnoredWarning(group.SourceKey))
	}
	hasAuto := false
	for _, key := range selected {
		if group.Entries[key].EffectiveUpdatePolicy() == config.UpdatePolicyAuto {
			hasAuto = true
			break
		}
	}
	if !hasAuto {
		if valid {
			result.Checks = cachedChecks(group, selected, record, false)
		}
		return result
	}
	if valid && cacheRecordFresh(record, cfg.PreflightCheckTTL, now) && cacheHasChecks(record, selected) {
		result.Checks = cachedChecks(group, selected, record, false)
		return result
	}
	if valid && retryActive(record, now) {
		if cacheHasChecks(record, selected) {
			result.Checks = cachedChecks(group, selected, record, true)
		}
		result.Warnings = append(result.Warnings, remoteDeferredWarning(group.SourceKey))
		return result
	}

	revision, err := probeRemoteRevision(group.Entries[group.Keys[0]].SourceURL, group.Entries[group.Keys[0]].Ref)
	if err != nil {
		return deferGitGroup(cfg, group, selected, record, valid, now, result)
	}
	checkedAt := now.Format(time.RFC3339)
	if revisionMatchesLock(group, selected, revision) {
		checks := checksFromLockRevision(group, selected, revision, checkedAt)
		result.Checks = checks
		if warning := writeCompletePreflightCache(cfg, group, checksFromLockRevision(group, group.Keys, revision, checkedAt), revision, checkedAt); warning != nil {
			result.Warnings = append(result.Warnings, *warning)
		}
		return result
	}
	if valid && record.ResolvedRevision == revision && cacheHasChecks(record, selected) {
		record.CheckedAt = checkedAt
		record.LastAttemptAt = checkedAt
		record.RetryAfter = ""
		record.LastFailure = ""
		if err := store.Write(group.Identity, record); err != nil {
			result.Warnings = append(result.Warnings, cacheIgnoredWarning(group.SourceKey))
		}
		result.Checks = cachedChecks(group, selected, record, false)
		return result
	}

	root, cleanup, materializedRevision, err := materializeGitForPreflight(group.Entries[group.Keys[0]])
	if err != nil {
		return deferGitGroup(cfg, group, selected, record, valid, now, result)
	}
	defer cleanup()
	allChecks := checkEntriesFromRoot(cfg, group, group.Keys, root, materializedRevision, false, UpdateCheckOriginLiveGit, checkedAt)
	if hasCheckErrors(allChecks) {
		result.Checks = selectChecks(allChecks, selected)
		return result
	}
	if warning := writeCompletePreflightCache(cfg, group, allChecks, materializedRevision, checkedAt); warning != nil {
		result.Warnings = append(result.Warnings, *warning)
	}
	result.Checks = selectChecks(allChecks, selected)
	return result
}

func cacheRecordFresh(record updatecache.Record, ttl time.Duration, now time.Time) bool {
	if ttl <= 0 || record.CheckedAt == "" {
		return false
	}
	checkedAt, err := time.Parse(time.RFC3339, record.CheckedAt)
	return err == nil && now.Sub(checkedAt) <= ttl
}

func retryActive(record updatecache.Record, now time.Time) bool {
	if record.RetryAfter == "" {
		return false
	}
	retryAfter, err := time.Parse(time.RFC3339, record.RetryAfter)
	return err == nil && now.Before(retryAfter)
}

func cacheHasChecks(record updatecache.Record, keys []string) bool {
	for _, key := range keys {
		if _, ok := record.Checks[key]; !ok {
			return false
		}
	}
	return true
}

func cachedChecks(group updateCheckGroup, keys []string, record updatecache.Record, stale bool) []UpdateCheck {
	if !cacheHasChecks(record, keys) {
		return nil
	}
	checks := make([]UpdateCheck, 0, len(keys))
	for _, key := range keys {
		entry := group.Entries[key]
		cached := record.Checks[key]
		check := baseUpdateCheck(key, entry, UpdateCheckOriginCache, record.CheckedAt)
		check.Name = cached.Name
		check.CurrentRevision = cached.CurrentRevision
		check.ResolvedRevision = cached.ResolvedRevision
		check.CurrentHash = cached.CurrentHash
		check.NewHash = cached.NewHash
		check.Changed = cached.Changed
		check.Stale = stale
		checks = append(checks, check)
	}
	return checks
}

func checksFromLockRevision(group updateCheckGroup, keys []string, revision string, checkedAt string) []UpdateCheck {
	checks := make([]UpdateCheck, 0, len(keys))
	for _, key := range keys {
		entry := group.Entries[key]
		check := baseUpdateCheck(key, entry, UpdateCheckOriginLockRevision, checkedAt)
		check.ResolvedRevision = revision
		check.NewHash = entry.Hash
		check.Changed = false
		checks = append(checks, check)
	}
	return checks
}

func revisionMatchesLock(group updateCheckGroup, keys []string, revision string) bool {
	if revision == "" {
		return false
	}
	for _, key := range keys {
		if group.Entries[key].ResolvedRevision != revision {
			return false
		}
	}
	return true
}

func deferGitGroup(
	cfg config.Config,
	group updateCheckGroup,
	selected []string,
	record updatecache.Record,
	valid bool,
	now time.Time,
	result UpdateCheckResult,
) UpdateCheckResult {
	if !valid {
		record = updatecache.Record{LockFingerprint: group.Fingerprint}
	}
	record.LastAttemptAt = now.Format(time.RFC3339)
	record.RetryAfter = now.Add(preflightRetryDelay).Format(time.RFC3339)
	record.LastFailure = "remote_probe_failed"
	if err := updatecache.New(cfg.Home).Write(group.Identity, record); err != nil {
		result.Warnings = append(result.Warnings, cacheIgnoredWarning(group.SourceKey))
	}
	if valid && cacheHasChecks(record, selected) {
		result.Checks = cachedChecks(group, selected, record, true)
	}
	result.Warnings = append(result.Warnings, remoteDeferredWarning(group.SourceKey))
	return result
}

func writeGroupCache(
	cfg config.Config,
	group updateCheckGroup,
	selected []string,
	checks []UpdateCheck,
	revision string,
	checkedAt string,
) *UpdateCheckWarning {
	if hasCheckErrors(checks) {
		return nil
	}
	store := updatecache.New(cfg.Home)
	complete := len(selected) == len(group.Keys)
	results := checkResults(checks)
	if !complete {
		record, found, err := store.Read(group.Identity)
		if err != nil || !found || record.LockFingerprint != group.Fingerprint || !cacheHasChecks(record, group.Keys) {
			return nil
		}
		for key, check := range results {
			record.Checks[key] = check
		}
		record.CheckedAt = checkedAt
		record.LastAttemptAt = checkedAt
		record.RetryAfter = ""
		record.LastFailure = ""
		record.ResolvedRevision = revision
		if err := store.Write(group.Identity, record); err != nil {
			warning := cacheIgnoredWarning(group.SourceKey)
			return &warning
		}
		return nil
	}
	return writeCompletePreflightCache(cfg, group, checks, revision, checkedAt)
}

func writeCompletePreflightCache(
	cfg config.Config,
	group updateCheckGroup,
	checks []UpdateCheck,
	revision string,
	checkedAt string,
) *UpdateCheckWarning {
	record := updatecache.Record{
		CheckedAt:        checkedAt,
		LastAttemptAt:    checkedAt,
		ResolvedRevision: revision,
		LockFingerprint:  group.Fingerprint,
		Checks:           checkResults(checks),
	}
	if err := updatecache.New(cfg.Home).Write(group.Identity, record); err != nil {
		warning := cacheIgnoredWarning(group.SourceKey)
		return &warning
	}
	return nil
}

func checkResults(checks []UpdateCheck) map[string]updatecache.SkillResult {
	results := make(map[string]updatecache.SkillResult, len(checks))
	for _, check := range checks {
		results[check.StoreName] = updatecache.SkillResult{
			Name:             check.Name,
			CurrentRevision:  check.CurrentRevision,
			ResolvedRevision: check.ResolvedRevision,
			CurrentHash:      check.CurrentHash,
			NewHash:          check.NewHash,
			Changed:          check.Changed,
		}
	}
	return results
}

func cacheIgnoredWarning(sourceKey string) UpdateCheckWarning {
	return UpdateCheckWarning{
		Type:      UpdateCheckWarningCacheIgnored,
		SourceKey: sourceKey,
		Message:   "update-check cache ignored",
	}
}

func remoteDeferredWarning(sourceKey string) UpdateCheckWarning {
	return UpdateCheckWarning{
		Type:      UpdateCheckWarningRemoteDeferred,
		SourceKey: sourceKey,
		Message:   "automatic update check deferred",
	}
}

func probeRemoteRevision(sourceURL string, ref string) (string, error) {
	if isFullCommitSHA(ref) {
		return ref, nil
	}
	args := []string{"ls-remote"}
	if ref == "" {
		args = append(args, "--symref", sourceURL, "HEAD")
	} else {
		args = append(args, sourceURL, "refs/heads/"+ref, "refs/tags/"+ref, "refs/tags/"+ref+"^{}")
	}
	output, err := runPreflightGit(args...)
	if err != nil {
		return "", err
	}
	if ref == "" {
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == "HEAD" && fields[0] != "ref:" {
				return fields[0], nil
			}
		}
		return "", fmt.Errorf("remote HEAD did not resolve")
	}
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			values[fields[1]] = fields[0]
		}
	}
	if revision := values["refs/heads/"+ref]; revision != "" {
		return revision, nil
	}
	if revision := values["refs/tags/"+ref+"^{}"]; revision != "" {
		return revision, nil
	}
	if revision := values["refs/tags/"+ref]; revision != "" {
		return revision, nil
	}
	return "", fmt.Errorf("remote ref %q did not resolve", ref)
}

func materializeGitForPreflight(entry LockEntry) (string, func(), string, error) {
	tmp, err := makeTempDir("", "upskill-*")
	if err != nil {
		return "", noopMaterializeCleanup, "", err
	}
	cleanup := func() { _ = removeTree(tmp) }
	args := []string{"clone", "--depth", "1"}
	pinnedCommit := isFullCommitSHA(entry.Ref)
	if pinnedCommit {
		args = []string{"clone"}
	} else if entry.Ref != "" {
		args = append(args, "--branch", entry.Ref)
	}
	args = append(args, entry.SourceURL, tmp)
	if _, err := runPreflightGit(args...); err != nil {
		cleanup()
		return "", noopMaterializeCleanup, "", err
	}
	if pinnedCommit {
		if _, err := runPreflightGit("-C", tmp, "checkout", "--detach", entry.Ref); err != nil {
			cleanup()
			return "", noopMaterializeCleanup, "", err
		}
	}
	revision, err := runPreflightGit("-C", tmp, "rev-parse", "HEAD")
	if err != nil {
		cleanup()
		return "", noopMaterializeCleanup, "", err
	}
	return tmp, cleanup, strings.TrimSpace(revision), nil
}

func runPreflightGit(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), preflightGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("git command timed out")
	}
	if err != nil {
		return "", fmt.Errorf("git command failed")
	}
	return string(output), nil
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func intersectSorted(values []string, selected map[string]struct{}) []string {
	var result []string
	for _, value := range values {
		if _, ok := selected[value]; ok {
			result = append(result, value)
		}
	}
	return result
}

func hasCheckErrors(checks []UpdateCheck) bool {
	for _, check := range checks {
		if check.Error != "" {
			return true
		}
	}
	return false
}

func selectChecks(checks []UpdateCheck, keys []string) []UpdateCheck {
	selected := stringSet(keys)
	var result []UpdateCheck
	for _, check := range checks {
		if _, ok := selected[check.StoreName]; ok {
			result = append(result, check)
		}
	}
	sortUpdateChecks(result)
	return result
}

func sortUpdateChecks(checks []UpdateCheck) {
	sort.SliceStable(checks, func(i, j int) bool {
		return checks[i].StoreName < checks[j].StoreName
	})
}

func sortUpdateWarnings(warnings []UpdateCheckWarning) {
	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].SourceKey == warnings[j].SourceKey {
			return warnings[i].Type < warnings[j].Type
		}
		return warnings[i].SourceKey < warnings[j].SourceKey
	})
}
