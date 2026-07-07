package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"upskill/internal/config"
	"upskill/internal/fsutil"
	"upskill/internal/pathutil"
)

const LockSchemaVersion = 2

var skillEntrypoints = []string{"SKILL.md.j2", "SKILL.md"}

var (
	githubShorthand  = regexp.MustCompile(`^([^/\s:]+)/([^/\s:@#]+)(?:/(.+))?$`)
	fullCommitSHA    = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	absPath          = filepath.Abs
	makeTempDir      = os.MkdirTemp
	removeTree       = os.RemoveAll
	makeAll          = os.MkdirAll
	renameFS         = os.Rename
	hashTree         = HashDir
	infoForEntry     = func(entry fs.DirEntry) (fs.FileInfo, error) { return entry.Info() }
	readSkillFromDir = readSkill
)

func noopMaterializeCleanup() {
	return
}

type ParsedSource struct {
	Input            string
	Type             string
	URL              string
	LocalPath        string
	Ref              string
	Subpath          string
	SkillFilter      string
	SourceID         string
	UpdatePolicy     string
	ResolvedRevision string
}

type Skill struct {
	StoreName   string            `json:"store_name"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Path        string            `json:"path"`
	SkillPath   string            `json:"skill_path"`
	Body        string            `json:"body,omitempty"`
	Frontmatter map[string]string `json:"frontmatter,omitempty"`
	Content     string            `json:"-"`
}

type Lock struct {
	SchemaVersion int                  `json:"schema_version"`
	Skills        map[string]LockEntry `json:"skills"`
}

type LockEntry struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	Source           string `json:"source"`
	SourceID         string `json:"source_id,omitempty"`
	SourceType       string `json:"source_type"`
	SourceURL        string `json:"source_url"`
	Ref              string `json:"ref,omitempty"`
	Subpath          string `json:"subpath,omitempty"`
	SourceSubpath    string `json:"source_subpath,omitempty"`
	SkillSubpath     string `json:"skill_subpath,omitempty"`
	SkillPath        string `json:"skill_path,omitempty"`
	ResolvedRevision string `json:"resolved_revision,omitempty"`
	UpdatePolicy     string `json:"update_policy,omitempty"`
	Hash             string `json:"hash"`
	UpdatedAt        string `json:"updated_at"`
}

func (entry LockEntry) EffectiveSourceSubpath() string {
	if entry.SourceSubpath != "" {
		return entry.SourceSubpath
	}
	return entry.Subpath
}

func (entry LockEntry) EffectiveSkillSubpath() string {
	if entry.SkillSubpath != "" {
		return entry.SkillSubpath
	}
	return entry.Subpath
}

func (entry LockEntry) EffectiveUpdatePolicy() string {
	return config.NormalizeUpdatePolicy(entry.UpdatePolicy)
}

type InstallOptions struct {
	SkillFilter  string
	Ref          string
	Subpath      string
	UpdatePolicy string
}

type InstalledSkill struct {
	StoreName string
	Name      string
	Hash      string
	Path      string
}

type UpdatePreview struct {
	StoreName   string   `json:"store_name"`
	Name        string   `json:"name"`
	CurrentHash string   `json:"current_hash,omitempty"`
	NewHash     string   `json:"new_hash,omitempty"`
	Changed     bool     `json:"changed"`
	Diff        []string `json:"diff,omitempty"`
}

type UpdateCheck struct {
	StoreName        string   `json:"store_name"`
	Name             string   `json:"name"`
	Source           string   `json:"source,omitempty"`
	SourceID         string   `json:"source_id,omitempty"`
	SourceType       string   `json:"source_type,omitempty"`
	SourceURL        string   `json:"source_url,omitempty"`
	Ref              string   `json:"ref,omitempty"`
	UpdatePolicy     string   `json:"update_policy"`
	CurrentRevision  string   `json:"current_revision,omitempty"`
	ResolvedRevision string   `json:"resolved_revision,omitempty"`
	CurrentHash      string   `json:"current_hash,omitempty"`
	NewHash          string   `json:"new_hash,omitempty"`
	Changed          bool     `json:"changed"`
	Diff             []string `json:"diff,omitempty"`
	Error            string   `json:"error,omitempty"`
}

type UpdatePolicyChange struct {
	StoreName string `json:"store_name"`
	Name      string `json:"name"`
	OldPolicy string `json:"old_policy"`
	NewPolicy string `json:"new_policy"`
}

type AutoUpdateResult struct {
	Updated []InstalledSkill
	Checks  []UpdateCheck
}

type SourceSkill struct {
	StoreName     string `json:"store_name"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	SourceSubpath string `json:"source_subpath"`
	SkillPath     string `json:"skill_path"`
}

type MaterializedSource struct {
	Root             string
	Cleanup          func()
	SourceType       string
	SourceID         string
	SourceIdentity   string
	ResolvedRevision string
}

func ParseSource(input string) (ParsedSource, error) {
	if strings.TrimSpace(input) == "" {
		return ParsedSource{}, fmt.Errorf("source is required")
	}
	input = strings.TrimSpace(input)
	noFragment, ref, skill := splitFragment(input)
	parsed := ParsedSource{Input: input, Ref: ref, SkillFilter: skill}

	if isLocalPath(noFragment) {
		path, err := expandLocalSourcePath(noFragment)
		if err != nil {
			return ParsedSource{}, err
		}
		parsed.Type = "local"
		parsed.LocalPath = path
		parsed.URL = path
		return parsed, nil
	}

	if strings.HasPrefix(noFragment, "github:") {
		noFragment = strings.TrimPrefix(noFragment, "github:")
	}

	if strings.HasPrefix(noFragment, "git@") || strings.HasPrefix(noFragment, "ssh://") || strings.HasPrefix(noFragment, "file://") || strings.HasSuffix(noFragment, ".git") {
		parsed.Type = "git"
		parsed.URL = noFragment
		return parsed, nil
	}

	if strings.HasPrefix(noFragment, "http://") || strings.HasPrefix(noFragment, "https://") {
		return parseHTTPSource(parsed, noFragment)
	}

	if at := strings.LastIndex(noFragment, "@"); at > 0 {
		parsed.Ref = noFragment[at+1:]
		noFragment = noFragment[:at]
	}
	if match := githubShorthand.FindStringSubmatch(noFragment); match != nil {
		owner, repo := match[1], match[2]
		parsed.Type = "git"
		parsed.URL = fmt.Sprintf("https://github.com/%s/%s.git", owner, strings.TrimSuffix(repo, ".git"))
		if match[3] != "" {
			parsed.Subpath = match[3]
		}
		if err := validateOptionalSubpath(parsed.Subpath); err != nil {
			return ParsedSource{}, err
		}
		return parsed, nil
	}

	return ParsedSource{}, fmt.Errorf("unsupported source: %s", input)
}

func ApplySourceOverrides(parsed ParsedSource, ref string, subpath string, skill string) (ParsedSource, error) {
	if ref != "" {
		parsed.Ref = ref
	}
	if subpath != "" {
		parsed.Subpath = subpath
	}
	if skill != "" {
		parsed.SkillFilter = skill
	}
	if err := validateOptionalSubpath(parsed.Subpath); err != nil {
		return ParsedSource{}, err
	}
	return parsed, nil
}

func CanonicalSource(input string, name string, ref string, subpath string) (config.Source, string, error) {
	parsed, err := ParseSource(input)
	if err != nil {
		return config.Source{}, "", err
	}
	parsed, err = ApplySourceOverrides(parsed, ref, subpath, "")
	if err != nil {
		return config.Source{}, "", err
	}
	source := config.Source{Name: name, Ref: parsed.Ref, Subpath: parsed.Subpath}
	if parsed.Type == "local" {
		source.Source = parsed.LocalPath
	} else {
		source.Source = parsed.URL
	}
	return source, config.SourceKey(source), nil
}

func ResolveNamedSource(cfg config.Config, name string) (config.Source, bool) {
	source, ok := cfg.Sources[name]
	return source, ok
}

func ResolveSource(cfg config.Config, source string, opts InstallOptions) (ParsedSource, bool, error) {
	namedSource := false
	if named, ok := ResolveNamedSource(cfg, source); ok {
		namedSource = true
		source = named.Source
		if opts.Ref == "" {
			opts.Ref = named.Ref
		}
		if opts.Subpath == "" {
			opts.Subpath = named.Subpath
		}
	}
	parsed, err := ParseSource(source)
	if err != nil {
		return ParsedSource{}, namedSource, err
	}
	parsed, err = ApplySourceOverrides(parsed, opts.Ref, opts.Subpath, opts.SkillFilter)
	if err != nil {
		return ParsedSource{}, namedSource, err
	}
	if matched, ok := configuredSourceForParsed(cfg, parsed); ok {
		parsed.SourceID = matched.ID
		parsed.UpdatePolicy = matched.DefaultUpdatePolicy
	}
	if opts.UpdatePolicy != "" {
		if !config.ValidUpdatePolicy(opts.UpdatePolicy) {
			return ParsedSource{}, namedSource, fmt.Errorf("invalid update policy: %s", opts.UpdatePolicy)
		}
		parsed.UpdatePolicy = opts.UpdatePolicy
	}
	parsed.UpdatePolicy = config.NormalizeUpdatePolicy(parsed.UpdatePolicy)
	return parsed, namedSource, nil
}

func configuredSourceForParsed(cfg config.Config, parsed ParsedSource) (config.Source, bool) {
	for _, source := range config.SourceRecords(cfg.Sources) {
		sourceParsed, err := ParseSource(source.Source)
		if err != nil {
			continue
		}
		sourceParsed, err = ApplySourceOverrides(sourceParsed, source.Ref, source.Subpath, "")
		if err != nil {
			continue
		}
		if sourceParsed.URL == parsed.URL &&
			sourceParsed.Type == parsed.Type &&
			sourceParsed.Ref == parsed.Ref &&
			filepath.ToSlash(strings.Trim(sourceParsed.Subpath, "/")) == filepath.ToSlash(strings.Trim(parsed.Subpath, "/")) {
			return source, true
		}
	}
	return config.Source{}, false
}

func ResolveMaterializedSource(parsed ParsedSource) (MaterializedSource, error) {
	root, cleanup, err := materialize(parsed)
	if err != nil {
		return MaterializedSource{}, err
	}
	if cleanup == nil {
		cleanup = noopMaterializeCleanup
	}
	resolvedRevision := parsed.ResolvedRevision
	if parsed.Type == "git" {
		revision, err := gitResolvedRevision(root)
		if err != nil {
			cleanup()
			return MaterializedSource{}, err
		}
		resolvedRevision = revision
	}
	return MaterializedSource{
		Root:             root,
		Cleanup:          cleanup,
		SourceType:       parsed.Type,
		SourceID:         parsed.SourceID,
		SourceIdentity:   parsedSourceIdentity(parsed),
		ResolvedRevision: resolvedRevision,
	}, nil
}

func parsedSourceIdentity(parsed ParsedSource) string {
	return config.SourceIdentity(config.Source{
		Source:  parsed.URL,
		Ref:     parsed.Ref,
		Subpath: parsed.Subpath,
	})
}

func DiscoverSkills(root string, subpath string) ([]Skill, error) {
	return DiscoverSkillsWithIgnore(root, subpath, config.DefaultSourceIgnore)
}

func DiscoverSkillsWithIgnore(root string, subpath string, ignore []string) ([]Skill, error) {
	if subpath != "" {
		joined, err := pathutil.SafeJoin(root, subpath)
		if err != nil {
			return nil, err
		}
		root = joined
	}
	ignoreSet := ignoreNameSet(ignore)
	seen := map[string]struct{}{}
	skills := []Skill{}
	err := walkSkillDirs(root, 0, 6, ignoreSet, func(dir string) error {
		skill, err := readSkillFromDir(dir)
		if err != nil {
			return err
		}
		key := NormalizeName(skill.Name)
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		skills = append(skills, skill)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].StoreName < skills[j].StoreName
	})
	return skills, nil
}

func ResolveLocalSourceDestination(cfg config.Config, name string) (string, error) {
	source, ok := ResolveNamedSource(cfg, name)
	if !ok {
		return "", fmt.Errorf("source not found: %s", name)
	}
	parsed, err := ParseSource(source.Source)
	if err != nil {
		return "", err
	}
	if parsed.Type != "local" {
		return "", fmt.Errorf("source %s is not a local filesystem source", name)
	}
	subpath := source.Subpath
	if subpath == "" {
		subpath = parsed.Subpath
	}
	if subpath == "" {
		return parsed.LocalPath, nil
	}
	return pathutil.SafeJoin(parsed.LocalPath, subpath)
}

func InstallSource(cfg config.Config, source string, opts InstallOptions) ([]InstalledSkill, error) {
	parsed, _, err := ResolveSource(cfg, source, opts)
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		return nil, err
	}
	defer resolved.Cleanup()
	parsed.ResolvedRevision = resolved.ResolvedRevision

	skills, err := DiscoverSkillsWithIgnore(resolved.Root, parsed.Subpath, cfg.SourceIgnore)
	if err != nil {
		return nil, err
	}
	skills = filterSkills(skills, parsed.SkillFilter)
	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills found")
	}

	lock, err := ReadLock(cfg)
	if err != nil {
		return nil, err
	}
	var installed []InstalledSkill
	for _, skill := range skills {
		result, entry, err := installSkill(cfg, resolved.Root, parsed, skill)
		if err != nil {
			return nil, err
		}
		lock.Skills[result.StoreName] = entry
		installed = append(installed, result)
	}
	if err := WriteLock(cfg, lock); err != nil {
		return nil, err
	}
	return installed, nil
}

func UpdateSkills(cfg config.Config, names []string) ([]InstalledSkill, error) {
	lock, err := ReadLock(cfg)
	if err != nil {
		return nil, err
	}
	if len(lock.Skills) == 0 {
		return nil, nil
	}
	requested := map[string]struct{}{}
	for _, name := range names {
		requested[NormalizeName(name)] = struct{}{}
	}
	var keys []string
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

	var updated []InstalledSkill
	for _, key := range keys {
		entry := lock.Skills[key]
		parsed := ParsedSource{
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
		resolved, err := ResolveMaterializedSource(parsed)
		if err != nil {
			return nil, err
		}
		parsed.ResolvedRevision = resolved.ResolvedRevision
		skills, err := DiscoverSkillsWithIgnore(resolved.Root, parsed.Subpath, cfg.SourceIgnore)
		if err != nil {
			resolved.Cleanup()
			return nil, err
		}
		skills = filterSkills(skills, key)
		if len(skills) == 0 {
			resolved.Cleanup()
			return nil, fmt.Errorf("locked skill not found in source: %s", key)
		}
		result, newEntry, err := installSkill(cfg, rootFromEntry(parsed, resolved.Root), parsed, skills[0])
		resolved.Cleanup()
		if err != nil {
			return nil, err
		}
		newEntry.Source = entry.Source
		if newEntry.SourceID == "" {
			newEntry.SourceID = entry.SourceID
		}
		newEntry.UpdatePolicy = entry.EffectiveUpdatePolicy()
		lock.Skills[key] = newEntry
		updated = append(updated, result)
	}
	if err := WriteLock(cfg, lock); err != nil {
		return nil, err
	}
	return updated, nil
}

func SetUpdatePolicy(cfg config.Config, names []string, policy string) ([]UpdatePolicyChange, error) {
	if !config.ValidUpdatePolicy(policy) {
		return nil, fmt.Errorf("invalid update policy: %s", policy)
	}
	policy = config.NormalizeUpdatePolicy(policy)
	lock, err := ReadLock(cfg)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one skill name is required")
	}
	changes := make([]UpdatePolicyChange, 0, len(names))
	for _, name := range names {
		key := NormalizeName(name)
		entry, ok := lock.Skills[key]
		if !ok {
			return nil, fmt.Errorf("skill not installed: %s", name)
		}
		oldPolicy := entry.EffectiveUpdatePolicy()
		entry.UpdatePolicy = policy
		lock.Skills[key] = entry
		changes = append(changes, UpdatePolicyChange{
			StoreName: key,
			Name:      entry.Name,
			OldPolicy: oldPolicy,
			NewPolicy: policy,
		})
	}
	if err := WriteLock(cfg, lock); err != nil {
		return nil, err
	}
	return changes, nil
}

func AutoUpdateSkills(cfg config.Config) (AutoUpdateResult, error) {
	checks, err := CheckSkillUpdates(cfg, nil, false)
	if err != nil {
		return AutoUpdateResult{}, err
	}
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.UpdatePolicy != config.UpdatePolicyAuto {
			continue
		}
		if check.Error != "" {
			return AutoUpdateResult{Checks: checks}, fmt.Errorf("automatic update check failed for %s: %s", check.StoreName, check.Error)
		}
		if check.Changed {
			names = append(names, check.StoreName)
		}
	}
	if len(names) == 0 {
		return AutoUpdateResult{Checks: checks}, nil
	}
	updated, err := UpdateSkills(cfg, names)
	if err != nil {
		return AutoUpdateResult{Checks: checks}, err
	}
	return AutoUpdateResult{Updated: updated, Checks: checks}, nil
}

func PreviewUpdates(cfg config.Config, names []string, includeDiff bool) ([]UpdatePreview, error) {
	checks, err := CheckSkillUpdates(cfg, names, includeDiff)
	if err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		return nil, nil
	}
	previews := make([]UpdatePreview, 0, len(checks))
	for _, check := range checks {
		if check.Error != "" {
			return nil, fmt.Errorf("%s: %s", check.StoreName, check.Error)
		}
		previews = append(previews, UpdatePreview{
			StoreName:   check.StoreName,
			Name:        check.Name,
			CurrentHash: check.CurrentHash,
			NewHash:     check.NewHash,
			Changed:     check.Changed,
			Diff:        check.Diff,
		})
	}
	return previews, nil
}

func CheckSkillUpdates(cfg config.Config, names []string, includeDiff bool) ([]UpdateCheck, error) {
	lock, err := ReadLock(cfg)
	if err != nil {
		return nil, err
	}
	if len(lock.Skills) == 0 {
		return nil, nil
	}
	requested := map[string]struct{}{}
	for _, name := range names {
		requested[NormalizeName(name)] = struct{}{}
	}
	var keys []string
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

	var checks []UpdateCheck
	for _, key := range keys {
		entry := lock.Skills[key]
		check := UpdateCheck{
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
		}
		parsed := ParsedSource{
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
		resolved, err := ResolveMaterializedSource(parsed)
		if err != nil {
			check.Error = err.Error()
			checks = append(checks, check)
			continue
		}
		parsed.ResolvedRevision = resolved.ResolvedRevision
		check.ResolvedRevision = resolved.ResolvedRevision
		skills, err := DiscoverSkillsWithIgnore(resolved.Root, parsed.Subpath, cfg.SourceIgnore)
		if err != nil {
			resolved.Cleanup()
			check.Error = err.Error()
			checks = append(checks, check)
			continue
		}
		skills = filterSkills(skills, key)
		if len(skills) == 0 {
			resolved.Cleanup()
			check.Error = "locked skill not found in source"
			checks = append(checks, check)
			continue
		}
		check.Name = skills[0].Name
		newHash, err := hashTree(skills[0].Path)
		if err != nil {
			resolved.Cleanup()
			check.Error = err.Error()
			checks = append(checks, check)
			continue
		}
		check.NewHash = newHash
		check.Changed = entry.Hash != newHash
		if includeDiff && check.Changed {
			diff, err := diffUpdateDirs(filepath.Join(cfg.StoreDir, key), skills[0].Path)
			if err != nil {
				resolved.Cleanup()
				check.Error = err.Error()
				checks = append(checks, check)
				continue
			}
			check.Diff = diff
		}
		resolved.Cleanup()
		checks = append(checks, check)
	}
	return checks, nil
}

func ListInstalled(cfg config.Config) ([]Skill, error) {
	names, err := config.DiscoverSkills(cfg.StoreDir)
	if err != nil {
		return nil, err
	}
	skills := []Skill{}
	for _, name := range names {
		skill, err := readSkill(filepath.Join(cfg.StoreDir, name))
		if err != nil {
			continue
		}
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].StoreName < skills[j].StoreName
	})
	return skills, nil
}

func FindInstalled(cfg config.Config, query string, limit int) ([]Skill, int, error) {
	skills, err := ListInstalled(cfg)
	if err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(query) == "" {
		total := len(skills)
		if limit > 0 && len(skills) > limit {
			return skills[:limit], total, nil
		}
		return skills, total, nil
	}
	q := strings.ToLower(query)
	var matches []Skill
	total := 0
	for _, skill := range skills {
		text := strings.ToLower(strings.Join([]string{skill.StoreName, skill.Name, skill.Description, skill.Body}, "\n"))
		if strings.Contains(text, q) {
			total++
			if limit > 0 && len(matches) >= limit {
				continue
			}
			matches = append(matches, skill)
		}
	}
	return matches, total, nil
}

func SearchSource(cfg config.Config, source string, query string, opts InstallOptions, limit int) ([]SourceSkill, int, error) {
	parsed, _, err := ResolveSource(cfg, source, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("source resolution failed: %w", err)
	}
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		return nil, 0, err
	}
	defer resolved.Cleanup()
	skills, err := DiscoverSkillsWithIgnore(resolved.Root, parsed.Subpath, cfg.SourceIgnore)
	if err != nil {
		return nil, 0, err
	}
	skills = filterSkills(skills, parsed.SkillFilter)
	skills, total := filterSkillsByQuery(skills, query, limit)
	return sourceSkillRows(resolved.Root, skills), total, nil
}

func ReadSourceSkill(cfg config.Config, source string, opts InstallOptions) (Skill, error) {
	parsed, _, err := ResolveSource(cfg, source, opts)
	if err != nil {
		return Skill{}, err
	}
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		return Skill{}, err
	}
	defer resolved.Cleanup()
	skills, err := DiscoverSkillsWithIgnore(resolved.Root, parsed.Subpath, cfg.SourceIgnore)
	if err != nil {
		return Skill{}, err
	}
	skills = filterSkills(skills, parsed.SkillFilter)
	if len(skills) == 0 {
		return Skill{}, fmt.Errorf("no skills found")
	}
	return skills[0], nil
}

func InspectSource(cfg config.Config, source string, opts InstallOptions) ([]SourceSkill, error) {
	parsed, _, err := ResolveSource(cfg, source, opts)
	if err != nil {
		return nil, fmt.Errorf("source resolution failed: %w", err)
	}
	resolved, err := ResolveMaterializedSource(parsed)
	if err != nil {
		return nil, err
	}
	defer resolved.Cleanup()
	skills, err := DiscoverSkillsWithIgnore(resolved.Root, parsed.Subpath, cfg.SourceIgnore)
	if err != nil {
		return nil, err
	}
	skills = filterSkills(skills, parsed.SkillFilter)
	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills found")
	}
	return sourceSkillRows(resolved.Root, skills), nil
}

func filterSkillsByQuery(skills []Skill, query string, limit int) ([]Skill, int) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		total := len(skills)
		if limit > 0 && len(skills) > limit {
			return skills[:limit], total
		}
		return skills, total
	}
	matches := make([]Skill, 0, len(skills))
	total := 0
	for _, skill := range skills {
		text := strings.ToLower(strings.Join([]string{
			skill.StoreName,
			skill.Name,
			skill.Description,
			skill.Body,
			skill.SkillPath,
			skill.Path,
		}, "\n"))
		if !strings.Contains(text, q) {
			continue
		}
		total++
		if limit > 0 && len(matches) >= limit {
			continue
		}
		matches = append(matches, skill)
	}
	return matches, total
}

func sourceSkillRows(root string, skills []Skill) []SourceSkill {
	rows := make([]SourceSkill, 0, len(skills))
	for _, skill := range skills {
		rel, err := filepath.Rel(root, skill.Path)
		if err != nil {
			rel = skill.Path
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		skillPath, err := filepath.Rel(root, skill.SkillPath)
		if err != nil {
			skillPath = filepath.Join(rel, filepath.Base(skill.SkillPath))
		}
		skillPath = filepath.ToSlash(skillPath)
		rows = append(rows, SourceSkill{
			StoreName:     skill.StoreName,
			Name:          skill.Name,
			Description:   skill.Description,
			SourceSubpath: rel,
			SkillPath:     skillPath,
		})
	}
	return rows
}

func RemoveSkills(cfg config.Config, names []string, deleteOverlays bool) ([]string, error) {
	lock, err := ReadLock(cfg)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, name := range names {
		key := NormalizeName(name)
		if err := removeTree(filepath.Join(cfg.StoreDir, key)); err != nil {
			return nil, err
		}
		if err := removeTree(filepath.Join(cfg.RenderedDir, "generic", key)); err != nil {
			return nil, err
		}
		if deleteOverlays {
			if err := removeTree(filepath.Join(cfg.OverlaysDir, key)); err != nil {
				return nil, err
			}
		}
		delete(lock.Skills, key)
		removed = append(removed, key)
	}
	if err := WriteLock(cfg, lock); err != nil {
		return nil, err
	}
	return removed, nil
}

func InitSkill(dir string, name string) (string, error) {
	if name == "" {
		name = filepath.Base(dir)
	}
	if dir == "" {
		dir = NormalizeName(name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s skill instructions\n---\n\n# %s\n\nAdd skill instructions here.\n", name, name, name)
	return path, os.WriteFile(path, []byte(body), 0o644)
}

func ReadLock(cfg config.Config) (Lock, error) {
	data, err := os.ReadFile(cfg.LockPath)
	if errors.Is(err, os.ErrNotExist) {
		return Lock{SchemaVersion: LockSchemaVersion, Skills: map[string]LockEntry{}}, nil
	}
	if err != nil {
		return Lock{}, err
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lock{}, err
	}
	if lock.SchemaVersion == 0 {
		lock.SchemaVersion = LockSchemaVersion
	}
	if lock.Skills == nil {
		lock.Skills = map[string]LockEntry{}
	}
	for key, entry := range lock.Skills {
		if entry.UpdatePolicy == "" {
			entry.UpdatePolicy = config.UpdatePolicyManual
		} else {
			entry.UpdatePolicy = config.NormalizeUpdatePolicy(entry.UpdatePolicy)
		}
		if entry.SkillSubpath == "" && entry.SourceSubpath == "" && entry.Subpath != "" {
			entry.SkillSubpath = entry.Subpath
		}
		lock.Skills[key] = entry
	}
	return lock, nil
}

func WriteLock(cfg config.Config, lock Lock) error {
	lock.SchemaVersion = LockSchemaVersion
	if lock.Skills == nil {
		lock.Skills = map[string]LockEntry{}
	}
	for key, entry := range lock.Skills {
		entry.UpdatePolicy = config.NormalizeUpdatePolicy(entry.UpdatePolicy)
		if entry.SkillSubpath == "" && entry.SourceSubpath == "" && entry.Subpath != "" {
			entry.SkillSubpath = entry.Subpath
		}
		lock.Skills[key] = entry
	}
	data, _ := json.MarshalIndent(lock, "", "  ")
	data = append(data, '\n')
	if err := makeAll(filepath.Dir(cfg.LockPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfg.LockPath, data, 0o644)
}

func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var out strings.Builder
	dash := false
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
			dash = false
			continue
		}
		if !dash && out.Len() > 0 {
			out.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func HashDir(root string) (string, error) {
	files, err := fsutil.WalkFiles(root)
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, file := range files {
		rel, _ := filepath.Rel(root, file)
		hash.Write([]byte(filepath.ToSlash(rel)))
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		hash.Write(data)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func diffUpdateDirs(current string, next string) ([]string, error) {
	currentFiles, err := readUpdateFiles(current)
	if err != nil {
		return nil, err
	}
	nextFiles, err := readUpdateFiles(next)
	if err != nil {
		return nil, err
	}
	keys := map[string]struct{}{}
	for key := range currentFiles {
		keys[key] = struct{}{}
	}
	for key := range nextFiles {
		keys[key] = struct{}{}
	}
	var sorted []string
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	var lines []string
	for _, key := range sorted {
		before, hadBefore := currentFiles[key]
		after, hasAfter := nextFiles[key]
		if hadBefore && hasAfter && before == after {
			continue
		}
		lines = append(lines, "diff "+key)
		if hadBefore {
			lines = append(lines, "-"+before)
		}
		if hasAfter {
			lines = append(lines, "+"+after)
		}
	}
	return lines, nil
}

func readUpdateFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	paths, err := fsutil.WalkFiles(root)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files[rel] = string(data)
	}
	return files, nil
}

func splitFragment(input string) (string, string, string) {
	hash := strings.Index(input, "#")
	if hash < 0 {
		return input, "", ""
	}
	head := input[:hash]
	fragment := input[hash+1:]
	if fragment == "" {
		return input, "", ""
	}
	at := strings.Index(fragment, "@")
	if at < 0 {
		return head, fragment, ""
	}
	return head, fragment[:at], fragment[at+1:]
}

func parseHTTPSource(parsed ParsedSource, raw string) (ParsedSource, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ParsedSource{}, err
	}
	if u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
			parsed.Type = "git"
			parsed.URL = fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
			if len(parts) >= 4 && parts[2] == "tree" {
				parsed.Ref = parts[3]
				if len(parts) > 4 {
					parsed.Subpath = strings.Join(parts[4:], "/")
				}
			}
			if err := validateOptionalSubpath(parsed.Subpath); err != nil {
				return ParsedSource{}, err
			}
			return parsed, nil
		}
	}
	parsed.Type = "git"
	parsed.URL = raw
	return parsed, nil
}

func isLocalPath(input string) bool {
	return filepath.IsAbs(input) || input == "." || input == ".." || strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") || input == "~" || strings.HasPrefix(input, "~/")
}

func expandLocalSourcePath(input string) (string, error) {
	if input == "~" || strings.HasPrefix(input, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if input == "~" {
			input = home
		} else {
			input = filepath.Join(home, input[2:])
		}
	}
	return absPath(input)
}

func validateOptionalSubpath(path string) error {
	if path == "" {
		return nil
	}
	_, err := pathutil.SafeJoin("/", path)
	return err
}

func materialize(parsed ParsedSource) (string, func(), error) {
	if parsed.Type == "local" {
		return parsed.LocalPath, func() {}, nil
	}
	tmp, err := makeTempDir("", "upskill-*")
	if err != nil {
		return "", noopMaterializeCleanup, err
	}
	args := []string{"clone", "--depth", "1"}
	pinnedCommit := isFullCommitSHA(parsed.Ref)
	if pinnedCommit {
		args = []string{"clone"}
	} else if parsed.Ref != "" {
		args = append(args, "--branch", parsed.Ref)
	}
	args = append(args, parsed.URL, tmp)
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = removeTree(tmp)
		return "", noopMaterializeCleanup, fmt.Errorf("git clone failed: %s", strings.TrimSpace(string(output)))
	}
	if pinnedCommit {
		cmd := exec.Command("git", "-C", tmp, "checkout", "--detach", parsed.Ref)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			_ = removeTree(tmp)
			return "", noopMaterializeCleanup, fmt.Errorf("git checkout failed: %s", strings.TrimSpace(string(output)))
		}
	}
	return tmp, func() { _ = removeTree(tmp) }, nil
}

func isFullCommitSHA(ref string) bool {
	return fullCommitSHA.MatchString(ref)
}

func gitResolvedRevision(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %s", strings.TrimSpace(string(output)))
	}
	revision := strings.TrimSpace(string(output))
	if revision == "" {
		return "", fmt.Errorf("git rev-parse returned empty revision")
	}
	return revision, nil
}

func rootFromEntry(parsed ParsedSource, root string) string {
	if parsed.Type == "local" {
		return parsed.LocalPath
	}
	return root
}

func installSkill(cfg config.Config, sourceRoot string, parsed ParsedSource, skill Skill) (InstalledSkill, LockEntry, error) {
	storeName := NormalizeName(skill.Name)
	if storeName == "" {
		return InstalledSkill{}, LockEntry{}, fmt.Errorf("skill has empty normalized name: %s", skill.Path)
	}
	tmp := filepath.Join(cfg.StoreDir, "."+storeName+".tmp")
	dst := filepath.Join(cfg.StoreDir, storeName)
	if err := removeTree(tmp); err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	if err := fsutil.CopyDir(skill.Path, tmp, nil); err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	hash, err := hashTree(tmp)
	if err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	if err := removeTree(dst); err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	if err := makeAll(filepath.Dir(dst), 0o755); err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	if err := renameFS(tmp, dst); err != nil {
		return InstalledSkill{}, LockEntry{}, err
	}
	rel, _ := filepath.Rel(sourceRoot, skill.Path)
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	sourceSubpath := filepath.ToSlash(strings.Trim(parsed.Subpath, "/"))
	skillSubpath := rel
	if sourceSubpath != "" {
		switch {
		case rel == sourceSubpath:
			skillSubpath = ""
		case strings.HasPrefix(rel, sourceSubpath+"/"):
			skillSubpath = strings.TrimPrefix(rel, sourceSubpath+"/")
		}
	}
	entry := LockEntry{
		Name:             skill.Name,
		Description:      skill.Description,
		Source:           parsed.Input,
		SourceID:         parsed.SourceID,
		SourceType:       parsed.Type,
		SourceURL:        parsed.URL,
		Ref:              parsed.Ref,
		Subpath:          rel,
		SourceSubpath:    sourceSubpath,
		SkillSubpath:     skillSubpath,
		SkillPath:        relativeSkillPath(sourceRoot, skill, rel),
		ResolvedRevision: parsed.ResolvedRevision,
		UpdatePolicy:     config.NormalizeUpdatePolicy(parsed.UpdatePolicy),
		Hash:             hash,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	return InstalledSkill{StoreName: storeName, Name: skill.Name, Hash: hash, Path: dst}, entry, nil
}

func relativeSkillPath(root string, skill Skill, fallbackDir string) string {
	rel, err := filepath.Rel(root, skill.SkillPath)
	if err != nil {
		return filepath.ToSlash(filepath.Join(fallbackDir, filepath.Base(skill.SkillPath)))
	}
	return filepath.ToSlash(rel)
}

func filterSkills(skills []Skill, filter string) []Skill {
	if filter == "" {
		return skills
	}
	normalized := NormalizeName(filter)
	var out []Skill
	for _, skill := range skills {
		if skill.StoreName == normalized || NormalizeName(skill.Name) == normalized || strings.EqualFold(skill.Name, filter) {
			out = append(out, skill)
		}
	}
	return out
}

func walkSkillDirs(root string, depth int, maxDepth int, ignore map[string]struct{}, fn func(string) error) error {
	if depth > maxDepth {
		return nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	if hasSkill(root) {
		if err := fn(root); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || skipDir(entry.Name(), ignore) {
			continue
		}
		if err := walkSkillDirs(filepath.Join(root, entry.Name()), depth+1, maxDepth, ignore, fn); err != nil {
			return err
		}
	}
	return nil
}

func hasSkill(dir string) bool {
	_, ok := skillEntrypoint(dir)
	return ok
}

func skipDir(name string, ignore map[string]struct{}) bool {
	_, ok := ignore[name]
	return ok
}

func ignoreNameSet(ignore []string) map[string]struct{} {
	values := ignore
	if len(values) == 0 {
		values = config.DefaultSourceIgnore
	}
	set := map[string]struct{}{}
	for _, name := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func readSkill(dir string) (Skill, error) {
	path, ok := skillEntrypoint(dir)
	if !ok {
		return Skill{}, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	content := string(data)
	frontmatter, body := parseFrontmatter(content)
	hasFrontmatter := HasFrontmatter(content)
	name := frontmatter["name"]
	if name == "" {
		name = filepath.Base(dir)
		frontmatter["name"] = name
	}
	description := frontmatter["description"]
	if description == "" && !hasFrontmatter {
		description = DefaultSkillDescription(name, body)
		frontmatter["description"] = description
	}
	storeName := NormalizeName(name)
	return Skill{
		StoreName:   storeName,
		Name:        name,
		Description: description,
		Path:        dir,
		SkillPath:   path,
		Body:        body,
		Frontmatter: frontmatter,
		Content:     content,
	}, nil
}

func skillEntrypoint(dir string) (string, bool) {
	for _, name := range skillEntrypoints {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func parseFrontmatter(text string) (map[string]string, string) {
	fields := map[string]string{}
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return fields, text
	}
	lines := strings.SplitAfter(text, "\n")
	var bodyStart int
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			bodyStart = i + 1
			break
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			fields[strings.TrimSpace(key)] = trimScalar(value)
		}
	}
	if bodyStart == 0 {
		return fields, text
	}
	return fields, strings.Join(lines[bodyStart:], "")
}

func (skill Skill) EntrypointText() string {
	content := skill.Content
	if content == "" {
		content = skill.Body
	}
	return EnsureDefaultFrontmatter(content, skill.Name, skill.Description)
}

func EnsureDefaultFrontmatter(text string, name string, description string) string {
	if HasFrontmatter(text) {
		return text
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "skill"
	}
	description = strings.TrimSpace(description)
	if description == "" {
		description = DefaultSkillDescription(name, text)
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", frontmatterScalar(name), frontmatterScalar(description), text)
}

func HasFrontmatter(text string) bool {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return false
	}
	lines := strings.SplitAfter(text, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return true
		}
	}
	return false
}

func DefaultSkillDescription(name string, body string) string {
	if description := firstDescriptionParagraph(body); description != "" {
		return limitDescription(description)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "skill"
	}
	return limitDescription(name + " skill instructions")
}

func firstDescriptionParagraph(body string) string {
	lines := strings.Split(body, "\n")
	paragraph := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, line)
	}
	return strings.Join(strings.Fields(strings.Join(paragraph, " ")), " ")
}

func limitDescription(description string) string {
	description = strings.Join(strings.Fields(description), " ")
	const maxDescription = 240
	if len(description) <= maxDescription {
		return description
	}
	cut := strings.LastIndex(description[:maxDescription-3], " ")
	if cut < 80 {
		cut = maxDescription - 3
	}
	return strings.TrimSpace(description[:cut]) + "..."
}

func frontmatterScalar(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func trimScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}

func FileManifest(root string, prefix string, recursive bool, hashes bool, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 500
	}
	var files []map[string]any
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			if entry.IsDir() && !strings.HasPrefix(prefix, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !recursive && strings.Contains(strings.TrimPrefix(rel, strings.TrimSuffix(prefix, "/")), "/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := infoForEntry(entry)
		if err != nil {
			return err
		}
		item := map[string]any{
			"path": rel,
			"kind": "file",
			"size": info.Size(),
		}
		if entry.IsDir() {
			item["kind"] = "directory"
		}
		if hashes && !entry.IsDir() {
			if hash, err := fileHash(path); err == nil {
				item["sha256"] = hash
			}
		}
		files = append(files, item)
		if len(files) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return files, err
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
