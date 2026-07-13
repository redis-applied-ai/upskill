package overlay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"upskill/internal/config"
	"upskill/internal/fsutil"
)

type Summary struct {
	Skill string `json:"skill"`
	Agent string `json:"agent,omitempty"`
	Path  string `json:"path"`
	Files int    `json:"files"`
}

var countFiles = fileCount

func Add(cfg config.Config, skill string, agent string) (string, error) {
	dir := config.OverlayDir(cfg, skill, agent)
	for _, sub := range []string{"append", "files", "replace", "patches", "templates"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	appendPath := filepath.Join(dir, "SKILL.append.md")
	if _, err := os.Stat(appendPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(appendPath, []byte(""), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func List(cfg config.Config, skillFilter string, agentFilter string) ([]Summary, error) {
	root := cfg.OverlaysDir
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var summaries []Summary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill := entry.Name()
		if skillFilter != "" && skillFilter != skill {
			continue
		}
		dir := filepath.Join(root, skill)
		count, err := countFiles(dir, true)
		if err != nil {
			return nil, err
		}
		if count > 0 && agentFilter == "" {
			summaries = append(summaries, Summary{Skill: skill, Path: dir, Files: count})
		}
		agentsDir := filepath.Join(dir, "agents")
		agents, err := os.ReadDir(agentsDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, agentEntry := range agents {
			if !agentEntry.IsDir() {
				continue
			}
			agent := agentEntry.Name()
			if agentFilter != "" && agentFilter != agent {
				continue
			}
			agentDir := filepath.Join(agentsDir, agent)
			count, err := countFiles(agentDir, false)
			if err != nil {
				return nil, err
			}
			if count > 0 {
				summaries = append(summaries, Summary{Skill: skill, Agent: agent, Path: agentDir, Files: count})
			}
		}
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Skill != summaries[j].Skill {
			return summaries[i].Skill < summaries[j].Skill
		}
		return summaries[i].Agent < summaries[j].Agent
	})
	return summaries, nil
}

func Chain(cfg config.Config, skill string, agent string) []string {
	var dirs []string
	global := config.OverlayDir(cfg, skill, "")
	if info, err := os.Stat(global); err == nil && info.IsDir() {
		dirs = append(dirs, global)
	}
	if agent != "" {
		agentDir := config.OverlayDir(cfg, skill, agent)
		if info, err := os.Stat(agentDir); err == nil && info.IsDir() {
			dirs = append(dirs, agentDir)
		}
	}
	return dirs
}

func MarshalSummaries(summaries []Summary) []byte {
	data, _ := json.MarshalIndent(summaries, "", "  ")
	return data
}

type WriteOptions struct {
	Skill         string
	Agent         string
	Operation     string
	Path          string
	Content       string
	Base64Content string
	DryRun        bool
}

func UpsertFile(cfg config.Config, opts WriteOptions) (string, error) {
	target, err := overlayOperationPath(cfg, opts)
	if err != nil {
		return "", err
	}
	content := []byte(opts.Content)
	if opts.Base64Content != "" {
		content, err = base64.StdEncoding.DecodeString(opts.Base64Content)
		if err != nil {
			return "", err
		}
	}
	if opts.Operation == "template" && looksLikeJSON(content) {
		return "", fmt.Errorf("template overlays must contain Jinja template content in a .j2 file; JSON variable maps are not supported")
	}
	if opts.DryRun {
		return target, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	return target, os.WriteFile(target, content, 0o644)
}

func EnsureFile(cfg config.Config, opts WriteOptions, content string) (string, error) {
	target, err := overlayOperationPath(cfg, opts)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory", target)
		}
		return target, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return target, os.WriteFile(target, []byte(content), 0o644)
}

func DeleteFile(cfg config.Config, opts WriteOptions) (string, error) {
	target, err := overlayOperationPath(cfg, opts)
	if err != nil {
		return "", err
	}
	if opts.DryRun {
		if _, err := os.Stat(target); err != nil {
			return "", err
		}
		return target, nil
	}
	if err := os.Remove(target); err != nil {
		return "", err
	}
	return target, nil
}

func overlayOperationPath(cfg config.Config, opts WriteOptions) (string, error) {
	if opts.Skill == "" {
		return "", fmt.Errorf("skill is required")
	}
	if err := validateOverlayRel(opts.Path); err != nil {
		return "", err
	}
	operation := opts.Operation
	if operation == "" {
		operation = "append"
	}
	dir := config.OverlayDir(cfg, opts.Skill, opts.Agent)
	switch operation {
	case "append":
		return safeOverlayJoin(dir, filepath.Join("append", opts.Path))
	case "root_append":
		name := opts.Path
		if !strings.Contains(name, ".append.") {
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			name = base + ".append" + ext
		}
		return safeOverlayJoin(dir, name)
	case "files":
		return safeOverlayJoin(dir, filepath.Join("files", opts.Path))
	case "replace":
		return safeOverlayJoin(dir, filepath.Join("replace", opts.Path))
	case "template":
		if !strings.HasSuffix(opts.Path, ".j2") {
			return "", fmt.Errorf("template overlay paths must end in .j2")
		}
		return safeOverlayJoin(dir, filepath.Join("templates", opts.Path))
	case "patch":
		if !strings.HasSuffix(opts.Path, ".json") {
			return "", fmt.Errorf("patch overlay paths must end in .json")
		}
		return safeOverlayJoin(dir, filepath.Join("patches", opts.Path))
	default:
		return "", fmt.Errorf("unknown overlay operation: %s", operation)
	}
}

func validateOverlayRel(rel string) error {
	if rel == "" {
		return fmt.Errorf("path is required")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path traversal is not allowed: %s", rel)
	}
	return nil
}

func safeOverlayJoin(root string, rel string) (string, error) {
	return filepath.Join(root, filepath.Clean(rel)), nil
}

func looksLikeJSON(content []byte) bool {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return false
	}
	return json.Valid([]byte(trimmed)) && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "["))
}

func fileCount(root string, skipAgents bool) (int, error) {
	files, err := fsutil.WalkFiles(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, file := range files {
		rel, _ := filepath.Rel(root, file)
		if skipAgents && (rel == "agents" || strings.HasPrefix(rel, "agents"+string(filepath.Separator))) {
			continue
		}
		count++
	}
	return count, nil
}
