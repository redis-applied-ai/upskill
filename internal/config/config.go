package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	makeDirAll  = os.MkdirAll
	filepathRel = filepath.Rel
)

var WellKnownAgentOutputs = map[string]string{
	"claude-code": "~/.claude/skills",
	"codex":       "~/.codex/skills",
}

type AgentOutput struct {
	Name string
	Path string
}

type Source struct {
	Name    string `json:"name,omitempty"`
	Source  string `json:"source"`
	Ref     string `json:"ref,omitempty"`
	Subpath string `json:"subpath,omitempty"`
}

type Config struct {
	Home         string
	StoreDir     string
	OverlaysDir  string
	DraftsDir    string
	ImportsDir   string
	ImportDirs   []string
	RenderedDir  string
	SourceIgnore []string
	ConfigPath   string
	LockPath     string
	SkillsBin    string
	AgentOutputs []AgentOutput
	Sources      map[string]Source
}

type rawConfig struct {
	StoreDir     string          `json:"store_dir"`
	OverlaysDir  string          `json:"overlays_dir"`
	DraftsDir    string          `json:"drafts_dir"`
	ImportsDir   string          `json:"imports_dir"`
	ImportDirs   json.RawMessage `json:"import_dirs"`
	RenderedDir  string          `json:"rendered_dir"`
	SourceIgnore json.RawMessage `json:"source_ignore"`
	LockPath     string          `json:"lock_path"`
	SkillsBin    string          `json:"skills_bin"`
	Agents       json.RawMessage `json:"agents"`
	Sources      json.RawMessage `json:"sources"`
}

var DefaultSourceIgnore = []string{
	".git",
	"node_modules",
	"dist",
	"build",
	"__pycache__",
	".obsidian",
	".trash",
	".upskill",
	"overlays",
	"drafts",
	"imports",
	"rendered",
}

func Load() (Config, error) {
	home, err := expandPath(envDefault("UPSKILL_HOME", "~/.skills"))
	if err != nil {
		return Config{}, err
	}
	configPath, err := expandPath(envDefault("UPSKILL_CONFIG", filepath.Join(home, "config.json")))
	if err != nil {
		return Config{}, err
	}

	raw, err := readRawConfig(configPath)
	if err != nil {
		return Config{}, err
	}

	storeDir, err := expandPath(envDefault("UPSKILL_STORE_DIR", defaultString(raw.StoreDir, filepath.Join(home, "store"))))
	if err != nil {
		return Config{}, err
	}
	overlaysDir, err := expandPath(envDefault("UPSKILL_OVERLAYS_DIR", defaultString(raw.OverlaysDir, filepath.Join(home, "overlays"))))
	if err != nil {
		return Config{}, err
	}
	draftsDir, err := expandPath(envDefault("UPSKILL_DRAFTS_DIR", defaultString(raw.DraftsDir, filepath.Join(home, "drafts"))))
	if err != nil {
		return Config{}, err
	}
	importsDir, err := expandPath(envDefault("UPSKILL_IMPORTS_DIR", defaultString(raw.ImportsDir, filepath.Join(home, "imports"))))
	if err != nil {
		return Config{}, err
	}
	importDirs, err := parseImportDirs(raw.ImportDirs, importsDir)
	if err != nil {
		return Config{}, err
	}
	renderedDir, err := expandPath(envDefault("UPSKILL_RENDERED_DIR", defaultString(raw.RenderedDir, filepath.Join(home, "rendered"))))
	if err != nil {
		return Config{}, err
	}
	sourceIgnore, err := parseSourceIgnore(raw.SourceIgnore, os.Getenv("UPSKILL_SOURCE_IGNORE"))
	if err != nil {
		return Config{}, err
	}
	lockPath, err := expandPath(envDefault("UPSKILL_LOCK", defaultString(raw.LockPath, filepath.Join(home, "upskill-lock.json"))))
	if err != nil {
		return Config{}, err
	}

	agentOutputs, err := parseAgentOutputs(raw.Agents)
	if err != nil {
		return Config{}, err
	}
	if len(agentOutputs) == 0 {
		agentOutputs = detectWellKnownAgentOutputs()
	}
	sources, err := parseSources(raw.Sources)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Home:         home,
		StoreDir:     storeDir,
		OverlaysDir:  overlaysDir,
		DraftsDir:    draftsDir,
		ImportsDir:   importsDir,
		ImportDirs:   importDirs,
		RenderedDir:  renderedDir,
		SourceIgnore: sourceIgnore,
		ConfigPath:   configPath,
		LockPath:     lockPath,
		SkillsBin:    envDefault("UPSKILL_SKILLS_BIN", defaultString(raw.SkillsBin, "skills")),
		AgentOutputs: agentOutputs,
		Sources:      sources,
	}, nil
}

func OverlayDir(cfg Config, skill string, agent string) string {
	root := filepath.Join(cfg.OverlaysDir, skill)
	if agent == "" {
		return root
	}
	return filepath.Join(root, "agents", agent)
}

func DraftDir(cfg Config, skill string) string {
	return filepath.Join(cfg.DraftsDir, skill)
}

func ImportPathAllowed(cfg Config, path string) (string, error) {
	abs, err := expandPath(path)
	if err != nil {
		return "", err
	}
	dirs := cfg.ImportDirs
	if len(dirs) == 0 && cfg.ImportsDir != "" {
		dirs = []string{cfg.ImportsDir}
	}
	for _, dir := range dirs {
		if isInsideDir(abs, dir) {
			return abs, nil
		}
	}
	return "", errors.New("path is outside configured import directories")
}

func DiscoverSkills(storeDir string) ([]string, error) {
	entries, err := os.ReadDir(storeDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var skills []string
	for _, entry := range entries {
		if entry.IsDir() {
			skills = append(skills, entry.Name())
		}
	}
	return skills, nil
}

func readRawConfig(path string) (rawConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return rawConfig{}, nil
	}
	if err != nil {
		return rawConfig{}, err
	}
	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return rawConfig{}, err
	}
	return raw, nil
}

func SaveSources(cfg Config, sources map[string]Source) error {
	raw, err := readRawConfig(cfg.ConfigPath)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(SourceRecords(sources))
	raw.Sources = data
	return writeRawConfig(cfg.ConfigPath, raw)
}

func SaveConfig(cfg Config) error {
	raw, err := readRawConfig(cfg.ConfigPath)
	if err != nil {
		return err
	}
	raw.StoreDir = cfg.StoreDir
	raw.OverlaysDir = cfg.OverlaysDir
	raw.DraftsDir = cfg.DraftsDir
	raw.ImportsDir = cfg.ImportsDir
	raw.RenderedDir = cfg.RenderedDir
	raw.LockPath = cfg.LockPath
	raw.SkillsBin = cfg.SkillsBin
	raw.ImportDirs = mustMarshal(cfg.ImportDirs)
	raw.SourceIgnore = mustMarshal(cfg.SourceIgnore)
	raw.Sources = mustMarshal(SourceRecords(cfg.Sources))
	return writeRawConfig(cfg.ConfigPath, raw)
}

func writeRawConfig(path string, raw rawConfig) error {
	out, _ := json.MarshalIndent(raw, "", "  ")
	out = append(out, '\n')
	if err := makeDirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func mustMarshal(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func parseAgentOutputs(data json.RawMessage) ([]AgentOutput, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}

	var objectOutputs map[string]string
	if err := json.Unmarshal(data, &objectOutputs); err == nil && objectOutputs != nil {
		outputs := make([]AgentOutput, 0, len(objectOutputs))
		for name, path := range objectOutputs {
			expanded, err := expandPath(path)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, AgentOutput{Name: name, Path: expanded})
		}
		return outputs, nil
	}

	var listOutputs []struct {
		Name      string `json:"name"`
		Agent     string `json:"agent"`
		Path      string `json:"path"`
		SkillsDir string `json:"skills_dir"`
	}
	if err := json.Unmarshal(data, &listOutputs); err != nil {
		return nil, err
	}
	outputs := make([]AgentOutput, 0, len(listOutputs))
	for _, output := range listOutputs {
		name := defaultString(output.Name, output.Agent)
		path := defaultString(output.Path, output.SkillsDir)
		if name == "" || path == "" {
			continue
		}
		expanded, err := expandPath(path)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, AgentOutput{Name: name, Path: expanded})
	}
	return outputs, nil
}

func parseSources(data json.RawMessage) (map[string]Source, error) {
	sources := map[string]Source{}
	if len(data) == 0 || string(data) == "null" {
		return sources, nil
	}

	var objectSources map[string]Source
	if err := json.Unmarshal(data, &objectSources); err == nil && objectSources != nil {
		for name, source := range objectSources {
			if source.Source == "" {
				continue
			}
			if source.Name == "" && name != SourceKey(source) {
				source.Name = name
			}
			sources[SourceKey(source)] = source
		}
		return sources, nil
	}

	var stringSources map[string]string
	if err := json.Unmarshal(data, &stringSources); err == nil && stringSources != nil {
		for name, source := range stringSources {
			if source == "" {
				continue
			}
			item := Source{Name: name, Source: source}
			sources[SourceKey(item)] = item
		}
		return sources, nil
	}

	var listSources []Source
	if err := json.Unmarshal(data, &listSources); err != nil {
		return nil, err
	}
	for _, source := range listSources {
		if source.Source == "" {
			continue
		}
		sources[SourceKey(source)] = source
	}
	return sources, nil
}

func SourceKey(source Source) string {
	if source.Name != "" {
		return source.Name
	}
	key := source.Source
	if source.Ref != "" {
		key += "#ref=" + source.Ref
	}
	if source.Subpath != "" {
		key += "#subpath=" + filepath.ToSlash(source.Subpath)
	}
	return key
}

func SourceDisplayName(source Source) string {
	if source.Name != "" {
		return source.Name
	}
	return source.Source
}

func SourceRecords(sources map[string]Source) []Source {
	records := make([]Source, 0, len(sources))
	for key, source := range sources {
		if source.Source == "" {
			continue
		}
		if source.Name != "" && key != "" && key != source.Name {
			source.Name = key
		}
		records = append(records, source)
	}
	sortSources(records)
	return records
}

func sortSources(sources []Source) {
	sort.Slice(sources, func(i, j int) bool {
		left := SourceDisplayName(sources[i])
		right := SourceDisplayName(sources[j])
		if left != right {
			return left < right
		}
		if sources[i].Source != sources[j].Source {
			return sources[i].Source < sources[j].Source
		}
		if sources[i].Ref != sources[j].Ref {
			return sources[i].Ref < sources[j].Ref
		}
		return sources[i].Subpath < sources[j].Subpath
	})
}

func parseImportDirs(data json.RawMessage, fallback string) ([]string, error) {
	if len(data) == 0 || string(data) == "null" {
		if fallback == "" {
			return nil, nil
		}
		return []string{fallback}, nil
	}
	var raw []string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(raw))
	for _, dir := range raw {
		if dir == "" {
			continue
		}
		expanded, err := expandPath(dir)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, expanded)
	}
	if len(dirs) == 0 && fallback != "" {
		dirs = append(dirs, fallback)
	}
	return dirs, nil
}

func parseSourceIgnore(data json.RawMessage, env string) ([]string, error) {
	values := append([]string{}, DefaultSourceIgnore...)
	if len(data) > 0 && string(data) != "null" {
		var configured []string
		if err := json.Unmarshal(data, &configured); err != nil {
			return nil, err
		}
		values = append(values, configured...)
	}
	if env != "" {
		for _, item := range strings.Split(env, ",") {
			values = append(values, strings.TrimSpace(item))
		}
	}
	return uniqueNonEmpty(values), nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func detectWellKnownAgentOutputs() []AgentOutput {
	var outputs []AgentOutput
	for name, path := range WellKnownAgentOutputs {
		expanded, err := expandPath(path)
		if err != nil {
			continue
		}
		if info, err := os.Stat(expanded); err == nil && info.IsDir() {
			outputs = append(outputs, AgentOutput{Name: name, Path: expanded})
		}
	}
	return outputs
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Abs(path)
}

func envDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func isInsideDir(path string, dir string) bool {
	rel, err := filepathRel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
