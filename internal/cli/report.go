package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"upskill/internal/audit"
	"upskill/internal/config"
	"upskill/internal/diagnostic"
	"upskill/internal/library"
	"upskill/internal/render"
)

type cliAuditDetail int

const (
	cliAuditConcise cliAuditDetail = iota
	cliAuditDetailed
)

type cliAuditReport struct {
	Agent   string
	Detail  cliAuditDetail
	Summary audit.AgentOutputAuditSummary
}

type cliDiagnosticCollector struct {
	diagnostics []diagnostic.Diagnostic
	audits      []cliAuditReport
	flushed     bool
}

const (
	humanSectionResult    = "Result"
	humanSectionPreflight = "Preflight"
	humanSectionAudit     = "Agent output audit"
)

type humanSection struct {
	Title string
	Lines []string
}

func newCLIDiagnosticCollector() *cliDiagnosticCollector {
	return &cliDiagnosticCollector{
		diagnostics: []diagnostic.Diagnostic{},
		audits:      []cliAuditReport{},
	}
}

func (c *cliDiagnosticCollector) Add(items ...diagnostic.Diagnostic) {
	if c == nil || len(items) == 0 {
		return
	}
	c.diagnostics = append(c.diagnostics, items...)
}

func (c *cliDiagnosticCollector) ScanAudit(cfg config.Config, agent string) audit.AgentOutputAuditSummary {
	items := audit.AgentOutputWarnings(cfg, agent)
	return audit.SummarizeAgentOutputWarnings(items)
}

func (c *cliDiagnosticCollector) AddAudit(agent string, detail cliAuditDetail, summary audit.AgentOutputAuditSummary) {
	if c != nil && summary.WarningCount > 0 {
		c.audits = append(c.audits, cliAuditReport{Agent: agent, Detail: detail, Summary: summary})
	}
}

func (c *cliDiagnosticCollector) CollectAudit(cfg config.Config, agent string, detail cliAuditDetail) audit.AgentOutputAuditSummary {
	summary := c.ScanAudit(cfg, agent)
	c.AddAudit(agent, detail, summary)
	return summary
}

func (c *cliDiagnosticCollector) Flush(w io.Writer) {
	if c == nil || c.flushed {
		return
	}
	c.flushed = true
	sections := make([]humanSection, 0, 2)
	if lines := formatPreflightLines(c.diagnostics); len(lines) > 0 {
		sections = append(sections, humanSection{Title: humanSectionPreflight, Lines: lines})
	}
	auditLines := []string{}
	for _, report := range c.audits {
		if report.Detail == cliAuditDetailed {
			auditLines = append(auditLines, formatDetailedAuditLines(report.Summary, report.Agent)...)
		} else {
			auditLines = append(auditLines, formatConciseAuditLines(report.Summary, report.Agent)...)
		}
	}
	if len(auditLines) > 0 {
		sections = append(sections, humanSection{Title: humanSectionAudit, Lines: auditLines})
	}
	if len(sections) > 0 {
		fmt.Fprintln(w)
		writeHumanSections(w, sections)
	}
}

func writeHumanSections(w io.Writer, sections []humanSection) {
	written := false
	for _, section := range sections {
		if strings.TrimSpace(section.Title) == "" || len(section.Lines) == 0 {
			continue
		}
		if written {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, section.Title)
		for _, line := range section.Lines {
			fmt.Fprintf(w, "  %s\n", line)
		}
		written = true
	}
}

func writeResultSection(w io.Writer, groups ...[]string) {
	lines := []string{}
	for _, group := range groups {
		lines = append(lines, group...)
	}
	writeHumanSections(w, []humanSection{{Title: humanSectionResult, Lines: lines}})
}

func formatInstalledResultLines(installed []library.InstalledSkill, verb string, planned bool) []string {
	ordered := append([]library.InstalledSkill(nil), installed...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].StoreName < ordered[j].StoreName })
	if len(ordered) == 0 {
		status := "OK"
		if planned {
			status = "PLAN"
		}
		return []string{status + " no skills " + verb}
	}
	status := "OK"
	action := verb
	if planned {
		status = "PLAN"
		action = plannedVerb(verb)
	}
	lines := make([]string, 0, len(ordered)*2)
	for _, item := range ordered {
		lines = append(lines, fmt.Sprintf("%s %s %s", status, action, item.StoreName))
		if item.Hash != "" {
			lines = append(lines, "  digest: "+item.Hash)
		}
	}
	return lines
}

func formatNoChangeResultLines(planned bool) []string {
	if planned {
		return []string{"PLAN no changes"}
	}
	return []string{"OK no changes"}
}

func formatPlannedUpdateResultLines(checks []library.UpdateCheck) []string {
	ordered := append([]library.UpdateCheck(nil), checks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].StoreName < ordered[j].StoreName })
	lines := []string{}
	for _, check := range ordered {
		if !check.Changed || check.Error != "" {
			continue
		}
		lines = append(lines, "PLAN update "+check.StoreName)
		if check.CurrentHash != "" || check.NewHash != "" {
			lines = append(lines, fmt.Sprintf("  digest: %s -> %s", check.CurrentHash, check.NewHash))
		}
	}
	return lines
}

func formatRenderResultLines(results []render.Result) []string {
	ordered := append([]render.Result(nil), results...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Skill != ordered[j].Skill {
			return ordered[i].Skill < ordered[j].Skill
		}
		if ordered[i].Agent != ordered[j].Agent {
			return ordered[i].Agent < ordered[j].Agent
		}
		return ordered[i].RenderedPath < ordered[j].RenderedPath
	})
	if len(ordered) == 0 {
		return []string{"OK no skills rendered"}
	}
	lines := []string{}
	for _, result := range ordered {
		planned := result.Mode == "preview"
		status := "OK"
		verb := "rendered"
		if planned {
			status = "PLAN"
			verb = "render"
		} else if result.Skipped {
			verb = "unchanged"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s for %s", status, verb, result.Skill, resultAgent(result)))
		if planned {
			lines = append(lines, "  render: "+result.RenderedPath)
		} else {
			lines = append(lines, "  rendered: "+result.RenderedPath)
		}
		copied := append([]string(nil), result.CopiedTo...)
		sort.Strings(copied)
		if len(copied) == 0 {
			lines = append(lines, "  not copied")
			continue
		}
		label := "copied"
		if planned {
			label = "copy"
		}
		for _, path := range copied {
			lines = append(lines, fmt.Sprintf("  %s: %s", label, path))
		}
	}
	return lines
}

func plannedVerb(verb string) string {
	switch verb {
	case "installed":
		return "install"
	case "updated":
		return "update"
	case "rendered":
		return "render"
	default:
		return verb
	}
}

func formatPreflightLines(items []diagnostic.Diagnostic) []string {
	ordered := append([]diagnostic.Diagnostic(nil), items...)
	diagnostic.Sort(ordered)
	lines := []string{}
	for _, item := range ordered {
		if item.Phase != diagnostic.PhasePreflight {
			continue
		}
		lines = append(lines, strings.ToUpper(string(item.Severity))+" "+formatPreflightMessage(item))
		for _, action := range item.Actions {
			lines = append(lines, fmt.Sprintf("  %s: %s", action.Label, formatCommand(action.Command)))
		}
	}
	return lines
}

func formatPreflightMessage(item diagnostic.Diagnostic) string {
	prefix := ""
	if item.Subject != "" {
		prefix = item.Subject + ": "
	}
	switch item.Code {
	case diagnostic.CodeUpdateCheckFailed:
		return prefix + "update check failed: " + item.Message
	case diagnostic.CodeRemoteCheckDeferred:
		message := prefix + "automatic update check deferred"
		if stale, _ := item.Details["stale"].(bool); stale {
			message += "; using cached status"
		}
		return message
	case diagnostic.CodeRemoteCheckFailed, diagnostic.CodeCacheIgnored:
		return prefix + item.Message
	case diagnostic.CodeUpdateAvailable:
		return prefix + "update available"
	case diagnostic.CodeAutomaticUpdatePending:
		return prefix + "automatic update pending"
	case diagnostic.CodeAutomaticallyUpdated:
		return prefix + "automatically updated"
	default:
		return prefix + item.Message
	}
}

func formatConciseAuditLines(summary audit.AgentOutputAuditSummary, agent string) []string {
	if summary.WarningCount == 0 {
		return []string{}
	}
	pureUnmanaged := summary.UnmanagedSkillCount == summary.WarningCount
	headline := fmt.Sprintf(
		"WARNING found %d agent output audit %s across %d %s (%d unique %s)",
		summary.WarningCount,
		pluralize(summary.WarningCount, "finding", "findings"),
		len(summary.Agents),
		pluralize(len(summary.Agents), "agent", "agents"),
		summary.UniqueSkillCount,
		pluralize(summary.UniqueSkillCount, "skill", "skills"),
	)
	if pureUnmanaged {
		headline = fmt.Sprintf(
			"WARNING found %d unmanaged skill %s across %d %s (%d unique %s)",
			summary.UnmanagedSkillCount,
			pluralize(summary.UnmanagedSkillCount, "occurrence", "occurrences"),
			len(summary.Agents),
			pluralize(len(summary.Agents), "agent", "agents"),
			summary.UniqueSkillCount,
			pluralize(summary.UniqueSkillCount, "skill", "skills"),
		)
	}
	lines := []string{headline}
	if summary.UnmanagedSkillCount > 0 && !pureUnmanaged {
		lines = append(lines, fmt.Sprintf("  unmanaged skill occurrences: %d", summary.UnmanagedSkillCount))
	}
	if summary.MissingMarkerCount > 0 {
		lines = append(lines, fmt.Sprintf("  missing render markers: %d", summary.MissingMarkerCount))
	}
	if summary.InspectionErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("  inspection failures: %d", summary.InspectionErrorCount))
	}
	for _, item := range summary.Agents {
		lines = append(lines, fmt.Sprintf("  %s: %d", item.Agent, item.WarningCount))
	}
	if summary.UnmanagedSkillCount > 0 {
		lines = append(lines, "  unmanaged skills were left unchanged")
	}
	return append(lines, formatAuditActionLines(summary, agent)...)
}

func pluralize(count int, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func formatDetailedAuditLines(summary audit.AgentOutputAuditSummary, agent string) []string {
	if summary.WarningCount == 0 {
		return []string{}
	}
	lines := []string{}
	for _, agentSummary := range summary.Agents {
		lines = append(lines, fmt.Sprintf("WARNING %s", agentSummary.Agent))
		lines = append(lines, "  output: "+agentSummary.OutputPath)
		for _, item := range summary.Items {
			if item.Agent != agentSummary.Agent || item.OutputPath != agentSummary.OutputPath {
				continue
			}
			if item.Skill == "" {
				lines = append(lines, "  inspection failed")
				lines = append(lines, "    reason: "+item.Reason)
				continue
			}
			lines = append(lines, "  skill: "+item.Skill)
			lines = append(lines, "    path: "+item.SkillPath)
			lines = append(lines, "    reason: "+item.Reason)
		}
	}
	if summary.UnmanagedSkillCount > 0 {
		lines = append(lines, "unmanaged skills were left unchanged")
	}
	return append(lines, formatAuditActionLines(summary, agent)...)
}

func formatAuditActionLines(summary audit.AgentOutputAuditSummary, agent string) []string {
	lines := []string{}
	seen := map[string]struct{}{}
	for _, item := range summary.Diagnostics(agent) {
		for _, action := range item.Actions {
			line := fmt.Sprintf("%s: %s", action.Label, formatCommand(action.Command))
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

func formatCommand(arguments []string) string {
	parts := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		if shellSafeArgument(argument) {
			parts = append(parts, argument)
			continue
		}
		parts = append(parts, "'"+strings.ReplaceAll(argument, "'", "'\"'\"'")+"'")
	}
	return strings.Join(parts, " ")
}

func shellSafeArgument(argument string) bool {
	if argument == "" {
		return false
	}
	for _, char := range argument {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune("_@%+=:,./-", char) {
			continue
		}
		return false
	}
	return true
}
