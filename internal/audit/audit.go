package audit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"upskill/internal/config"
	"upskill/internal/diagnostic"
	"upskill/internal/library"
	"upskill/internal/render"
)

const (
	ReasonMissingRenderMarker = "missing Upskill render marker"
	ReasonNotInstalled        = "not installed in Upskill from any configured source"
)

type AgentOutputSkillWarning struct {
	Agent      string `json:"agent"`
	OutputPath string `json:"output_path"`
	Skill      string `json:"skill"`
	SkillPath  string `json:"skill_path"`
	Reason     string `json:"reason"`
}

type AgentOutputAuditSummary struct {
	WarningCount         int                       `json:"warning_count"`
	UnmanagedSkillCount  int                       `json:"unmanaged_skill_count"`
	UniqueSkillCount     int                       `json:"unique_skill_count"`
	MissingMarkerCount   int                       `json:"missing_marker_count"`
	InspectionErrorCount int                       `json:"inspection_error_count"`
	Migratable           bool                      `json:"migratable"`
	Agents               []AgentOutputAgentSummary `json:"agents"`
	Items                []AgentOutputSkillWarning `json:"items"`
}

type AgentOutputAgentSummary struct {
	Agent                string   `json:"agent"`
	OutputPath           string   `json:"output_path"`
	WarningCount         int      `json:"warning_count"`
	UnmanagedSkillCount  int      `json:"unmanaged_skill_count"`
	MissingMarkerCount   int      `json:"missing_marker_count"`
	InspectionErrorCount int      `json:"inspection_error_count"`
	Skills               []string `json:"skills"`
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
			reason := ReasonMissingRenderMarker
			if !isInstalled {
				reason = ReasonNotInstalled
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
	return AgentOutputWarningMessagesFrom(AgentOutputWarnings(cfg, agent))
}

func AgentOutputWarningMessagesFrom(items []AgentOutputSkillWarning) []string {
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

func SummarizeAgentOutputWarnings(items []AgentOutputSkillWarning) AgentOutputAuditSummary {
	ordered := make([]AgentOutputSkillWarning, len(items))
	copy(ordered, items)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Agent != ordered[j].Agent {
			return ordered[i].Agent < ordered[j].Agent
		}
		if ordered[i].OutputPath != ordered[j].OutputPath {
			return ordered[i].OutputPath < ordered[j].OutputPath
		}
		if ordered[i].Skill != ordered[j].Skill {
			return ordered[i].Skill < ordered[j].Skill
		}
		if ordered[i].SkillPath != ordered[j].SkillPath {
			return ordered[i].SkillPath < ordered[j].SkillPath
		}
		return ordered[i].Reason < ordered[j].Reason
	})

	summary := AgentOutputAuditSummary{
		WarningCount: len(ordered),
		Agents:       []AgentOutputAgentSummary{},
		Items:        ordered,
	}
	uniqueSkills := map[string]struct{}{}
	type agentKey struct {
		Agent      string
		OutputPath string
	}
	agentIndexes := map[agentKey]int{}
	agentSkills := map[agentKey]map[string]struct{}{}

	for _, item := range ordered {
		key := agentKey{Agent: item.Agent, OutputPath: item.OutputPath}
		index, ok := agentIndexes[key]
		if !ok {
			index = len(summary.Agents)
			agentIndexes[key] = index
			agentSkills[key] = map[string]struct{}{}
			summary.Agents = append(summary.Agents, AgentOutputAgentSummary{
				Agent:      item.Agent,
				OutputPath: item.OutputPath,
				Skills:     []string{},
			})
		}
		agentSummary := &summary.Agents[index]
		agentSummary.WarningCount++

		if item.Skill == "" {
			summary.InspectionErrorCount++
			agentSummary.InspectionErrorCount++
			continue
		}
		uniqueSkills[item.Skill] = struct{}{}
		agentSkills[key][item.Skill] = struct{}{}
		switch item.Reason {
		case ReasonNotInstalled:
			summary.UnmanagedSkillCount++
			agentSummary.UnmanagedSkillCount++
		case ReasonMissingRenderMarker:
			summary.MissingMarkerCount++
			agentSummary.MissingMarkerCount++
		}
	}

	summary.UniqueSkillCount = len(uniqueSkills)
	summary.Migratable = summary.UnmanagedSkillCount > 0
	for key, index := range agentIndexes {
		skills := make([]string, 0, len(agentSkills[key]))
		for skill := range agentSkills[key] {
			skills = append(skills, skill)
		}
		sort.Strings(skills)
		summary.Agents[index].Skills = skills
	}
	return summary
}

func (summary AgentOutputAuditSummary) Diagnostics(agent string) []diagnostic.Diagnostic {
	items := make([]diagnostic.Diagnostic, 0, 3)
	if summary.UnmanagedSkillCount > 0 {
		preview := []string{"upskill", "migrate"}
		apply := []string{"upskill", "migrate"}
		if strings.TrimSpace(agent) != "" {
			preview = append(preview, "--agent", agent)
			apply = append(apply, "--agent", agent)
		}
		apply = append(apply, "--yes")
		items = append(items, diagnostic.Diagnostic{
			Phase:    diagnostic.PhaseAudit,
			Severity: diagnostic.SeverityWarning,
			Code:     diagnostic.CodeUnmanagedAgentOutput,
			Message:  "unmanaged skills found in agent outputs",
			Details: map[string]any{
				"occurrence_count":   summary.UnmanagedSkillCount,
				"unique_skill_count": summary.UniqueSkillCount,
			},
			Actions: []diagnostic.Action{
				{Label: "details", Command: []string{"upskill", "doctor"}},
				{Label: "preview migration", Command: preview},
				{Label: "apply migration", Command: apply},
			},
		})
	}
	if summary.MissingMarkerCount > 0 {
		command := []string{"upskill", "sync"}
		if strings.TrimSpace(agent) != "" {
			command = append(command, "--agent", agent)
		}
		items = append(items, diagnostic.Diagnostic{
			Phase:    diagnostic.PhaseAudit,
			Severity: diagnostic.SeverityWarning,
			Code:     diagnostic.CodeMissingRenderMarker,
			Message:  "installed skills are missing Upskill render markers",
			Details:  map[string]any{"occurrence_count": summary.MissingMarkerCount},
			Actions:  []diagnostic.Action{{Label: "repair managed outputs", Command: command}},
		})
	}
	if summary.InspectionErrorCount > 0 {
		items = append(items, diagnostic.Diagnostic{
			Phase:    diagnostic.PhaseAudit,
			Severity: diagnostic.SeverityWarning,
			Code:     diagnostic.CodeAgentOutputInspectionFailed,
			Message:  "one or more agent outputs could not be inspected",
			Details:  map[string]any{"occurrence_count": summary.InspectionErrorCount},
			Actions:  []diagnostic.Action{{Label: "details", Command: []string{"upskill", "doctor"}}},
		})
	}
	diagnostic.Sort(items)
	return items
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
