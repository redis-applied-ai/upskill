package library

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"upskill/internal/config"
	"upskill/internal/pathutil"
)

type SourceSkillFileTarget struct {
	SourceID          string              `json:"source_id"`
	SourceName        string              `json:"source_name"`
	SourceDisplayName string              `json:"source_display_name"`
	ResolvedPath      string              `json:"resolved_path"`
	SkillID           string              `json:"skill_id"`
	Path              string              `json:"path"`
	AbsPath           string              `json:"abs_path"`
	SkillRoot         string              `json:"skill_root"`
	Policy            config.SourcePolicy `json:"source_policy"`
}

type SourceSkillFileWriteOptions struct {
	SourceName         string
	Skill              string
	Path               string
	Content            string
	Base64Content      string
	DryRun             bool
	ConfirmSourceWrite bool
	Reason             string
}

type SourceSkillFileWriteResult struct {
	SourceID          string              `json:"source_id"`
	SourceName        string              `json:"source_name"`
	SourceDisplayName string              `json:"source_display_name"`
	ResolvedPath      string              `json:"resolved_path"`
	SkillID           string              `json:"skill_id"`
	Path              string              `json:"path"`
	AbsPath           string              `json:"abs_path"`
	DryRun            bool                `json:"dry_run"`
	Overwrite         bool                `json:"overwrite"`
	Updated           bool                `json:"updated"`
	SourceMutated     bool                `json:"source_mutated"`
	BaseMutated       bool                `json:"base_mutated"`
	SourcePolicy      config.SourcePolicy `json:"source_policy"`
	PolicyNotices     []string            `json:"policy_notices,omitempty"`
	RequiredReason    string              `json:"required_reason,omitempty"`
}

func SourcePolicyForSource(cfg config.Config, source config.Source) config.SourcePolicy {
	parsed, err := ParseSource(source.Source)
	if err != nil {
		return config.ResolveSourcePolicy(source, "", config.ManifestResolution{
			Warning: err.Error(),
		})
	}
	parsed, err = ApplySourceOverrides(parsed, source.Ref, source.Subpath, "")
	if err != nil {
		return config.ResolveSourcePolicy(source, parsed.Type, config.ManifestResolution{
			Warning: err.Error(),
		})
	}
	return sourcePolicyForParsedSource(cfg, source, parsed)
}

func SourcePolicyForInput(cfg config.Config, source string, opts InstallOptions) (config.SourcePolicy, error) {
	if named, ok := ResolveNamedSource(cfg, source); ok {
		if opts.Ref == "" {
			opts.Ref = named.Ref
		}
		if opts.Subpath == "" {
			opts.Subpath = named.Subpath
		}
		if opts.Ref == named.Ref && opts.Subpath == named.Subpath {
			return SourcePolicyForSource(cfg, named), nil
		}
	}
	parsed, _, err := ResolveSource(cfg, source, opts)
	if err != nil {
		return config.SourcePolicy{}, err
	}
	sourceRecord := config.Source{Source: parsed.URL, Ref: parsed.Ref, Subpath: parsed.Subpath}
	if parsed.Type == "local" {
		sourceRecord.Source = parsed.LocalPath
	}
	return sourcePolicyForParsedSource(cfg, sourceRecord, parsed), nil
}

func SourcePolicyForInstalledSkill(cfg config.Config, skill string) (config.SourcePolicy, bool, error) {
	lock, err := ReadLock(cfg)
	if err != nil {
		return config.SourcePolicy{}, false, err
	}
	entry, ok := lock.Skills[NormalizeName(skill)]
	if !ok {
		return config.SourcePolicy{}, false, nil
	}
	return SourcePolicyForLockEntry(cfg, entry), true, nil
}

func SourcePolicyForLockEntry(cfg config.Config, entry LockEntry) config.SourcePolicy {
	if entry.SourceID != "" {
		for _, source := range config.SourceRecords(cfg.Sources) {
			if source.ID == entry.SourceID {
				return SourcePolicyForSource(cfg, source)
			}
		}
	}
	parsed := ParsedSource{
		Input:       entry.Source,
		Type:        entry.SourceType,
		URL:         entry.SourceURL,
		LocalPath:   entry.SourceURL,
		Ref:         entry.Ref,
		Subpath:     entry.EffectiveSourceSubpath(),
		SkillFilter: entry.Name,
		SourceID:    entry.SourceID,
	}
	if matched, ok := configuredSourceForParsed(cfg, parsed); ok {
		return SourcePolicyForSource(cfg, matched)
	}
	source := config.Source{Source: entry.SourceURL, Ref: entry.Ref, Subpath: entry.EffectiveSourceSubpath()}
	return sourcePolicyForParsedSource(cfg, source, parsed)
}

func sourcePolicyForParsedSource(_ config.Config, source config.Source, parsed ParsedSource) config.SourcePolicy {
	manifest := config.ManifestResolution{}
	if parsed.Type == "local" {
		manifest = config.DiscoverSourceManifest(parsed.LocalPath, parsed.Subpath)
	}
	return config.ResolveSourcePolicy(source, parsed.Type, manifest)
}

func ResolveSourceSkillFile(cfg config.Config, sourceName string, skillName string, path string) (SourceSkillFileTarget, error) {
	resolution, err := ResolveConfiguredSource(cfg, sourceName)
	if err != nil {
		return SourceSkillFileTarget{}, err
	}
	source := resolution.Source
	root, err := ResolveLocalSourceDestination(cfg, sourceName)
	if err != nil {
		return SourceSkillFileTarget{}, err
	}
	if err := RejectManagedPath(cfg, root, "source edit"); err != nil {
		return SourceSkillFileTarget{}, err
	}
	skillName = NormalizeName(skillName)
	if skillName == "" {
		return SourceSkillFileTarget{}, fmt.Errorf("skill name is required")
	}
	skills, err := DiscoverSkillsWithIgnore(root, "", cfg.SourceIgnore)
	if err != nil {
		return SourceSkillFileTarget{}, err
	}
	matches := filterSkills(skills, skillName)
	if len(matches) == 0 {
		return SourceSkillFileTarget{}, fmt.Errorf("skill not found in source %s: %s", sourceName, skillName)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Path < matches[j].Path })
	path = filepath.ToSlash(filepath.Clean(defaultString(path, "SKILL.md")))
	target, err := pathutil.SafeJoin(matches[0].Path, path)
	if err != nil {
		return SourceSkillFileTarget{}, err
	}
	if err := RejectManagedPath(cfg, target, "source edit"); err != nil {
		return SourceSkillFileTarget{}, err
	}
	return SourceSkillFileTarget{
		SourceID:          source.ID,
		SourceName:        resolution.ConfiguredAs,
		SourceDisplayName: config.SourceDisplayName(source),
		ResolvedPath:      resolution.ResolvedPath,
		SkillID:           matches[0].StoreName,
		Path:              path,
		AbsPath:           target,
		SkillRoot:         matches[0].Path,
		Policy:            SourcePolicyForSource(cfg, source),
	}, nil
}

func UpsertSourceSkillFile(cfg config.Config, opts SourceSkillFileWriteOptions) (SourceSkillFileWriteResult, error) {
	target, err := ResolveSourceSkillFile(cfg, opts.SourceName, opts.Skill, opts.Path)
	if err != nil {
		return SourceSkillFileWriteResult{}, err
	}
	data, err := sourceWriteContent(opts)
	if err != nil {
		return SourceSkillFileWriteResult{}, err
	}
	if err := authorizeSourceWrite(target.Policy, opts.DryRun, opts.ConfirmSourceWrite, opts.Reason); err != nil {
		return SourceSkillFileWriteResult{}, err
	}
	result := SourceSkillFileWriteResult{
		SourceID:          target.SourceID,
		SourceName:        target.SourceName,
		SourceDisplayName: target.SourceDisplayName,
		ResolvedPath:      target.ResolvedPath,
		SkillID:           target.SkillID,
		Path:              target.Path,
		AbsPath:           target.AbsPath,
		DryRun:            opts.DryRun,
		Overwrite:         pathExists(target.AbsPath),
		Updated:           false,
		SourceMutated:     false,
		BaseMutated:       false,
		SourcePolicy:      target.Policy,
		PolicyNotices:     config.PolicyNotices(target.Policy),
		RequiredReason:    strings.TrimSpace(opts.Reason),
	}
	if opts.DryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(target.AbsPath), 0o755); err != nil {
		return SourceSkillFileWriteResult{}, err
	}
	if err := os.WriteFile(target.AbsPath, data, 0o644); err != nil {
		return SourceSkillFileWriteResult{}, err
	}
	result.Updated = true
	result.SourceMutated = true
	return result, nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func RejectManagedPath(cfg config.Config, path string, action string) error {
	managed := []struct {
		name string
		path string
	}{
		{"store", cfg.StoreDir},
		{"rendered", cfg.RenderedDir},
		{"overlays", cfg.OverlaysDir},
		{"drafts", cfg.DraftsDir},
		{"imports", cfg.ImportsDir},
	}
	for _, dir := range cfg.ImportDirs {
		managed = append(managed, struct {
			name string
			path string
		}{name: "import", path: dir})
	}
	for _, item := range managed {
		if item.path == "" {
			continue
		}
		if isInsidePath(path, item.path) {
			return fmt.Errorf("%s destination is inside managed %s directory: %s", action, item.name, item.path)
		}
	}
	return nil
}

func sourceWriteContent(opts SourceSkillFileWriteOptions) ([]byte, error) {
	hasContent := opts.Content != ""
	hasBase64 := opts.Base64Content != ""
	if hasContent == hasBase64 {
		return nil, fmt.Errorf("exactly one of content or base64_content is required")
	}
	if hasBase64 {
		data, err := base64.StdEncoding.DecodeString(opts.Base64Content)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return []byte(opts.Content), nil
}

func authorizeSourceWrite(policy config.SourcePolicy, dryRun bool, confirmed bool, reason string) error {
	writePolicy := config.NormalizeWritePolicy(policy.WritePolicy)
	if writePolicy == config.WritePolicyReadOnly {
		return fmt.Errorf("source policy is read_only; source writes are not allowed")
	}
	if dryRun {
		return nil
	}
	switch writePolicy {
	case config.WritePolicyDirectAllowed:
		return nil
	case config.WritePolicyOverlayPreferred:
		if !confirmed {
			return fmt.Errorf("source policy is overlay_preferred; set confirm_source_write after confirming the user wants a source edit instead of an overlay")
		}
	case config.WritePolicyDraftPromote:
		if !confirmed {
			return fmt.Errorf("source policy is draft_promote; set confirm_source_write after confirming the user wants a direct source edit")
		}
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("source policy is draft_promote; reason is required for a direct source write")
		}
	}
	return nil
}

func isInsidePath(path string, root string) bool {
	absPath, _ := filepath.Abs(path)
	absRoot, _ := filepath.Abs(root)
	rel, _ := filepath.Rel(absRoot, absPath)
	return rel == "." || rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
