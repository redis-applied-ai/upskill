package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	SourceScopePersonal = "personal"
	SourceScopeTeam     = "team"
	SourceScopeExternal = "external"

	WritePolicyDirectAllowed    = "direct_allowed"
	WritePolicyOverlayPreferred = "overlay_preferred"
	WritePolicyDraftPromote     = "draft_promote"
	WritePolicyReadOnly         = "read_only"

	PolicySourceConfig   = "config"
	PolicySourceManifest = "manifest"
	PolicySourceDefault  = "default"

	SourceManifestName = "upskill.toml"
)

type SourceManifest struct {
	SchemaVersion int      `toml:"schema_version" json:"schema_version,omitempty"`
	Name          string   `toml:"name" json:"name,omitempty"`
	Scope         string   `toml:"scope" json:"scope,omitempty"`
	WritePolicy   string   `toml:"write_policy" json:"write_policy,omitempty"`
	Authors       []string `toml:"authors" json:"authors,omitempty"`
	Owners        []string `toml:"owners" json:"owners,omitempty"`
	LLMGuidance   string   `toml:"llm_guidance" json:"llm_guidance,omitempty"`
}

type ManifestResolution struct {
	Manifest *SourceManifest
	Path     string
	Warning  string
}

type SourcePolicy struct {
	Scope               string   `json:"scope"`
	WritePolicy         string   `json:"write_policy"`
	Authors             []string `json:"authors,omitempty"`
	Owners              []string `json:"owners,omitempty"`
	LLMGuidance         string   `json:"llm_guidance,omitempty"`
	PolicySource        string   `json:"policy_source"`
	ManifestPath        string   `json:"manifest_path,omitempty"`
	ManifestWarning     string   `json:"manifest_warning,omitempty"`
	RecommendedWorkflow string   `json:"recommended_workflow"`
}

func NormalizeSourceScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "":
		return ""
	case SourceScopePersonal:
		return SourceScopePersonal
	case SourceScopeTeam:
		return SourceScopeTeam
	case SourceScopeExternal:
		return SourceScopeExternal
	default:
		return strings.TrimSpace(scope)
	}
}

func ValidSourceScope(scope string) bool {
	switch NormalizeSourceScope(scope) {
	case "", SourceScopePersonal, SourceScopeTeam, SourceScopeExternal:
		return true
	default:
		return false
	}
}

func NormalizeWritePolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "":
		return ""
	case WritePolicyDirectAllowed:
		return WritePolicyDirectAllowed
	case WritePolicyOverlayPreferred:
		return WritePolicyOverlayPreferred
	case WritePolicyDraftPromote:
		return WritePolicyDraftPromote
	case WritePolicyReadOnly:
		return WritePolicyReadOnly
	default:
		return strings.TrimSpace(policy)
	}
}

func ValidWritePolicy(policy string) bool {
	switch NormalizeWritePolicy(policy) {
	case "", WritePolicyDirectAllowed, WritePolicyOverlayPreferred, WritePolicyDraftPromote, WritePolicyReadOnly:
		return true
	default:
		return false
	}
}

func ValidateSourcePolicyFields(source Source) error {
	if !ValidSourceScope(source.Scope) {
		return fmt.Errorf("invalid source scope: %s", source.Scope)
	}
	if !ValidWritePolicy(source.WritePolicy) {
		return fmt.Errorf("invalid source write policy: %s", source.WritePolicy)
	}
	return nil
}

func DefaultWritePolicyForScope(scope string) string {
	switch NormalizeSourceScope(scope) {
	case SourceScopePersonal:
		return WritePolicyDirectAllowed
	case SourceScopeTeam:
		return WritePolicyDraftPromote
	case SourceScopeExternal:
		return WritePolicyReadOnly
	default:
		return ""
	}
}

func DefaultSourcePolicyForType(sourceType string) SourcePolicy {
	switch sourceType {
	case "local":
		return SourcePolicy{
			Scope:               SourceScopePersonal,
			WritePolicy:         WritePolicyDirectAllowed,
			PolicySource:        PolicySourceDefault,
			RecommendedWorkflow: RecommendedWorkflow(WritePolicyDirectAllowed),
		}
	default:
		return SourcePolicy{
			Scope:               SourceScopeExternal,
			WritePolicy:         WritePolicyReadOnly,
			PolicySource:        PolicySourceDefault,
			RecommendedWorkflow: RecommendedWorkflow(WritePolicyReadOnly),
		}
	}
}

func ResolveSourcePolicy(source Source, sourceType string, manifest ManifestResolution) SourcePolicy {
	source = NormalizeSource(source)
	policy := DefaultSourcePolicyForType(sourceType)
	policy.ManifestPath = manifest.Path
	policy.ManifestWarning = manifest.Warning

	if manifest.Manifest != nil {
		manifestHadScope := NormalizeSourceScope(manifest.Manifest.Scope) != ""
		manifestHadWritePolicy := NormalizeWritePolicy(manifest.Manifest.WritePolicy) != ""
		applyManifestPolicy(&policy, *manifest.Manifest)
		if manifestHadScope && !manifestHadWritePolicy {
			policy.WritePolicy = DefaultWritePolicyForScope(policy.Scope)
		}
		policy.PolicySource = PolicySourceManifest
	}
	if source.Scope != "" {
		policy.Scope = source.Scope
		if source.WritePolicy == "" {
			policy.WritePolicy = DefaultWritePolicyForScope(policy.Scope)
		}
		policy.PolicySource = PolicySourceConfig
	}
	if source.WritePolicy != "" {
		policy.WritePolicy = source.WritePolicy
		policy.PolicySource = PolicySourceConfig
	}
	if len(source.Authors) > 0 {
		policy.Authors = append([]string{}, source.Authors...)
		policy.PolicySource = PolicySourceConfig
	}
	if len(source.Owners) > 0 {
		policy.Owners = append([]string{}, source.Owners...)
		policy.PolicySource = PolicySourceConfig
	}
	if source.LLMGuidance != "" {
		policy.LLMGuidance = source.LLMGuidance
		policy.PolicySource = PolicySourceConfig
	}
	if policy.WritePolicy == "" {
		policy.WritePolicy = DefaultWritePolicyForScope(policy.Scope)
	}
	if policy.Scope == "" {
		policy.Scope = DefaultSourcePolicyForType(sourceType).Scope
	}
	if policy.WritePolicy == "" {
		policy.WritePolicy = DefaultSourcePolicyForType(sourceType).WritePolicy
	}
	policy.RecommendedWorkflow = RecommendedWorkflow(policy.WritePolicy)
	return policy
}

func applyManifestPolicy(policy *SourcePolicy, manifest SourceManifest) {
	manifest.Scope = NormalizeSourceScope(manifest.Scope)
	manifest.WritePolicy = NormalizeWritePolicy(manifest.WritePolicy)
	if manifest.Scope != "" {
		policy.Scope = manifest.Scope
	}
	if manifest.WritePolicy != "" {
		policy.WritePolicy = manifest.WritePolicy
	}
	if len(manifest.Authors) > 0 {
		policy.Authors = append([]string{}, manifest.Authors...)
	}
	if len(manifest.Owners) > 0 {
		policy.Owners = append([]string{}, manifest.Owners...)
	}
	if manifest.LLMGuidance != "" {
		policy.LLMGuidance = manifest.LLMGuidance
	}
}

func RecommendedWorkflow(writePolicy string) string {
	switch NormalizeWritePolicy(writePolicy) {
	case WritePolicyDirectAllowed:
		return "direct_source_edit"
	case WritePolicyOverlayPreferred:
		return "overlay_first"
	case WritePolicyDraftPromote:
		return "draft_then_promote"
	case WritePolicyReadOnly:
		return "read_only"
	default:
		return "inspect_policy"
	}
}

func PolicyNotices(policy SourcePolicy) []string {
	switch NormalizeWritePolicy(policy.WritePolicy) {
	case WritePolicyDirectAllowed:
		return []string{"direct source edits are allowed by this source policy"}
	case WritePolicyOverlayPreferred:
		return []string{"overlay is the preferred workflow; confirm the user wants a source change before writing source files"}
	case WritePolicyDraftPromote:
		return []string{"draft and dry-run promotion is the preferred workflow for this source"}
	case WritePolicyReadOnly:
		return []string{"this source is read-only; use overlays or drafts rendered locally instead of source writes"}
	default:
		return []string{"source write policy is unknown; inspect source policy before changing files"}
	}
}

func ParseSourceManifest(path string) (SourceManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourceManifest{}, err
	}
	var manifest SourceManifest
	if err := toml.Unmarshal(data, &manifest); err != nil {
		return SourceManifest{}, err
	}
	manifest.Scope = NormalizeSourceScope(manifest.Scope)
	manifest.WritePolicy = NormalizeWritePolicy(manifest.WritePolicy)
	if !ValidSourceScope(manifest.Scope) {
		return SourceManifest{}, fmt.Errorf("invalid source scope: %s", manifest.Scope)
	}
	if !ValidWritePolicy(manifest.WritePolicy) {
		return SourceManifest{}, fmt.Errorf("invalid source write policy: %s", manifest.WritePolicy)
	}
	return manifest, nil
}

func DiscoverSourceManifest(root string, subpath string) ManifestResolution {
	var result ManifestResolution
	for _, path := range manifestCandidatePaths(root, subpath) {
		manifest, err := ParseSourceManifest(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			warning := fmt.Sprintf("%s: %v", path, err)
			if result.Warning == "" {
				result.Warning = warning
			} else {
				result.Warning += "; " + warning
			}
			if result.Path == "" {
				result.Path = path
			}
			continue
		}
		copy := manifest
		result.Manifest = &copy
		result.Path = path
	}
	return result
}

func manifestCandidatePaths(root string, subpath string) []string {
	if root == "" {
		return nil
	}
	paths := []string{filepath.Join(root, SourceManifestName)}
	subpath = filepath.Clean(filepath.FromSlash(strings.TrimSpace(subpath)))
	if subpath != "." && subpath != "" {
		paths = append(paths, filepath.Join(root, subpath, SourceManifestName))
	}
	return paths
}
