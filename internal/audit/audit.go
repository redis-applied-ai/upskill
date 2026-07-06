package audit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"upskill/internal/config"
	"upskill/internal/library"
	"upskill/internal/render"
)

type AgentOutputSkillWarning struct {
	Agent      string `json:"agent"`
	OutputPath string `json:"output_path"`
	Skill      string `json:"skill"`
	SkillPath  string `json:"skill_path"`
	Reason     string `json:"reason"`
}

func AgentOutputWarnings(cfg config.Config, agent string) []AgentOutputSkillWarning {
	installed := installedSkillSet(cfg)
	outputs := agentOutputs(cfg, agent)
	warnings := []AgentOutputSkillWarning{}
	seen := map[string]struct{}{}
	for _, output := range outputs {
		entries, err := os.ReadDir(output.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			warnings = append(warnings, AgentOutputSkillWarning{
				Agent:      output.Name,
				OutputPath: output.Path,
				Reason:     fmt.Sprintf("could not inspect agent output directory: %v", err),
			})
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || name == "" || name[0] == '.' {
				continue
			}
			skillPath := filepath.Join(output.Path, name)
			if !hasSkillEntrypoint(skillPath) {
				continue
			}
			markerKey := filepath.Clean(output.Path) + "\x00" + name
			if _, ok := seen[markerKey]; ok {
				continue
			}
			seen[markerKey] = struct{}{}
			_, markerErr := os.Stat(filepath.Join(skillPath, render.MarkerFile))
			hasMarker := markerErr == nil
			_, isInstalled := installed[name]
			if isInstalled && hasMarker {
				continue
			}
			reason := "missing Upskill render marker"
			if !isInstalled {
				reason = "not installed in Upskill from any configured source"
			}
			warnings = append(warnings, AgentOutputSkillWarning{
				Agent:      output.Name,
				OutputPath: output.Path,
				Skill:      name,
				SkillPath:  skillPath,
				Reason:     reason,
			})
		}
	}
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].Agent != warnings[j].Agent {
			return warnings[i].Agent < warnings[j].Agent
		}
		return warnings[i].Skill < warnings[j].Skill
	})
	return warnings
}

func AgentOutputWarningMessages(cfg config.Config, agent string) []string {
	items := AgentOutputWarnings(cfg, agent)
	messages := make([]string, 0, len(items))
	for _, item := range items {
		if item.Skill == "" {
			messages = append(messages, fmt.Sprintf("agent output %s at %s: %s", item.Agent, item.OutputPath, item.Reason))
			continue
		}
		messages = append(messages, fmt.Sprintf("agent output %s contains unmanaged skill %s at %s: %s", item.Agent, item.Skill, item.SkillPath, item.Reason))
	}
	return messages
}

func installedSkillSet(cfg config.Config) map[string]struct{} {
	installed := map[string]struct{}{}
	names, err := config.DiscoverSkills(cfg.StoreDir)
	if err == nil {
		for _, name := range names {
			installed[name] = struct{}{}
		}
	}
	lock, err := library.ReadLock(cfg)
	if err == nil {
		for name := range lock.Skills {
			installed[name] = struct{}{}
		}
	}
	return installed
}

func agentOutputs(cfg config.Config, agent string) []config.AgentOutput {
	outputs := make([]config.AgentOutput, 0, len(cfg.AgentOutputs))
	for _, output := range cfg.AgentOutputs {
		if agent == "" || output.Name == agent {
			outputs = append(outputs, output)
		}
	}
	sort.Slice(outputs, func(i, j int) bool {
		if outputs[i].Path != outputs[j].Path {
			return outputs[i].Path < outputs[j].Path
		}
		return outputs[i].Name < outputs[j].Name
	})
	return outputs
}

func hasSkillEntrypoint(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !info.IsDir()
}
