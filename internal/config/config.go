package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	makeDirAll  = os.MkdirAll
	filepathRel = filepath.Rel
)

var WellKnownTargets = map[string]string{
	"claude":      "~/.claude/skills",
	"claude-code": "~/.claude/skills",
	"codex":       "~/.codex/skills",
}

type Target struct {
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
	Home        string
	StoreDir    string
	OverlaysDir string
	DraftsDir   string
	ImportsDir  string
	ImportDirs  []string
	RenderedDir string
	ConfigPath  string
	LockPath    string
	SkillsBin   string
	Targets     []Target
	Sources     map[string]Source
}

type rawConfig struct {
	StoreDir    string          `json:"store_dir"`
	OverlaysDir string          `json:"overlays_dir"`
	DraftsDir   string          `json:"drafts_dir"`
	ImportsDir  string          `json:"imports_dir"`
	ImportDirs  json.RawMessage `json:"import_dirs"`
	RenderedDir string          `json:"rendered_dir"`
	LockPath    string          `json:"lock_path"`
	SkillsBin   string          `json:"skills_bin"`
	Agents      json.RawMessage `json:"agents"`
	Targets     json.RawMessage `json:"targets"`
	Sources     json.RawMessage `json:"sources"`
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
	lockPath, err := expandPath(envDefault("UPSKILL_LOCK", defaultString(raw.LockPath, filepath.Join(home, "upskill-lock.json"))))
	if err != nil {
		return Config{}, err
	}

	targets, err := parseTargets(firstRaw(raw.Agents, raw.Targets))
	if err != nil {
		return Config{}, err
	}
	if len(targets) == 0 {
		targets = detectWellKnownTargets()
	}
	sources, err := parseSources(raw.Sources)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Home:        home,
		StoreDir:    storeDir,
		OverlaysDir: overlaysDir,
		DraftsDir:   draftsDir,
		ImportsDir:  importsDir,
		ImportDirs:  importDirs,
		RenderedDir: renderedDir,
		ConfigPath:  configPath,
		LockPath:    lockPath,
		SkillsBin:   envDefault("UPSKILL_SKILLS_BIN", defaultString(raw.SkillsBin, "skills")),
		Targets:     targets,
		Sources:     sources,
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
	data, _ := json.Marshal(sources)
	raw.Sources = data
	out, _ := json.MarshalIndent(raw, "", "  ")
	out = append(out, '\n')
	if err := makeDirAll(filepath.Dir(cfg.ConfigPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfg.ConfigPath, out, 0o644)
}

func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 && string(value) != "null" {
			return value
		}
	}
	return nil
}

func parseTargets(data json.RawMessage) ([]Target, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}

	var objectTargets map[string]string
	if err := json.Unmarshal(data, &objectTargets); err == nil && objectTargets != nil {
		targets := make([]Target, 0, len(objectTargets))
		for name, path := range objectTargets {
			expanded, err := expandPath(path)
			if err != nil {
				return nil, err
			}
			targets = append(targets, Target{Name: name, Path: expanded})
		}
		return targets, nil
	}

	var listTargets []struct {
		Name      string `json:"name"`
		Agent     string `json:"agent"`
		Path      string `json:"path"`
		SkillsDir string `json:"skills_dir"`
	}
	if err := json.Unmarshal(data, &listTargets); err != nil {
		return nil, err
	}
	targets := make([]Target, 0, len(listTargets))
	for _, target := range listTargets {
		name := defaultString(target.Name, target.Agent)
		path := defaultString(target.Path, target.SkillsDir)
		if name == "" || path == "" {
			continue
		}
		expanded, err := expandPath(path)
		if err != nil {
			return nil, err
		}
		targets = append(targets, Target{Name: name, Path: expanded})
	}
	return targets, nil
}

func parseSources(data json.RawMessage) (map[string]Source, error) {
	sources := map[string]Source{}
	if len(data) == 0 || string(data) == "null" {
		return sources, nil
	}

	var objectSources map[string]Source
	if err := json.Unmarshal(data, &objectSources); err == nil && objectSources != nil {
		for name, source := range objectSources {
			source.Name = defaultString(source.Name, name)
			sources[name] = source
		}
		return sources, nil
	}

	var stringSources map[string]string
	if err := json.Unmarshal(data, &stringSources); err == nil && stringSources != nil {
		for name, source := range stringSources {
			sources[name] = Source{Name: name, Source: source}
		}
		return sources, nil
	}

	var listSources []Source
	if err := json.Unmarshal(data, &listSources); err != nil {
		return nil, err
	}
	for _, source := range listSources {
		if source.Name == "" || source.Source == "" {
			continue
		}
		sources[source.Name] = source
	}
	return sources, nil
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

func detectWellKnownTargets() []Target {
	var targets []Target
	for name, path := range WellKnownTargets {
		expanded, err := expandPath(path)
		if err != nil {
			continue
		}
		if info, err := os.Stat(expanded); err == nil && info.IsDir() {
			targets = append(targets, Target{Name: name, Path: expanded})
		}
	}
	return targets
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
