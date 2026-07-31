package diagnostic

import (
	"sort"

	"upskill/internal/config"
	"upskill/internal/library"
)

type Phase string

const (
	PhasePreflight Phase = "preflight"
	PhaseAudit     Phase = "audit"
)

func (p Phase) Valid() bool {
	switch p {
	case PhasePreflight, PhaseAudit:
		return true
	default:
		return false
	}
}

type Severity string

const (
	SeverityNotice  Severity = "notice"
	SeverityWarning Severity = "warning"
)

func (s Severity) Valid() bool {
	switch s {
	case SeverityNotice, SeverityWarning:
		return true
	default:
		return false
	}
}

type Code string

const (
	CodeUpdateCheckFailed           Code = "update_check_failed"
	CodeRemoteCheckDeferred         Code = "remote_check_deferred"
	CodeRemoteCheckFailed           Code = "remote_check_failed"
	CodeCacheIgnored                Code = "cache_ignored"
	CodeUpdateAvailable             Code = "update_available"
	CodeAutomaticUpdatePending      Code = "automatic_update_pending"
	CodeAutomaticallyUpdated        Code = "automatically_updated"
	CodeUnmanagedAgentOutput        Code = "unmanaged_agent_output"
	CodeMissingRenderMarker         Code = "missing_render_marker"
	CodeAgentOutputInspectionFailed Code = "agent_output_inspection_failed"
)

func (c Code) Valid() bool {
	switch c {
	case CodeUpdateCheckFailed,
		CodeRemoteCheckDeferred,
		CodeRemoteCheckFailed,
		CodeCacheIgnored,
		CodeUpdateAvailable,
		CodeAutomaticUpdatePending,
		CodeAutomaticallyUpdated,
		CodeUnmanagedAgentOutput,
		CodeMissingRenderMarker,
		CodeAgentOutputInspectionFailed:
		return true
	default:
		return false
	}
}

type Action struct {
	Label   string   `json:"label"`
	Command []string `json:"command"`
}

type Diagnostic struct {
	Phase    Phase          `json:"phase"`
	Severity Severity       `json:"severity"`
	Code     Code           `json:"code"`
	Subject  string         `json:"subject,omitempty"`
	Message  string         `json:"message"`
	Details  map[string]any `json:"details,omitempty"`
	Actions  []Action       `json:"actions,omitempty"`
}

func FromUpdateResult(result library.UpdateCheckResult, updated []library.InstalledSkill) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, len(result.Warnings)+len(result.Checks)+len(updated))
	hasStale := false
	for _, check := range result.Checks {
		if check.Stale {
			hasStale = true
			break
		}
	}

	for _, warning := range result.Warnings {
		item := Diagnostic{
			Phase:    PhasePreflight,
			Severity: SeverityWarning,
			Code:     warningCode(warning.Type),
			Subject:  warning.SourceKey,
			Message:  warning.Message,
			Details:  map[string]any{},
			Actions:  []Action{},
		}
		if warning.SourceKey != "" {
			item.Details["source_key"] = warning.SourceKey
		}
		if warning.Type == library.UpdateCheckWarningRemoteDeferred {
			item.Details["stale"] = hasStale
		}
		diagnostics = append(diagnostics, item)
	}

	updatedSet := make(map[string]struct{}, len(updated))
	for _, skill := range updated {
		updatedSet[skill.StoreName] = struct{}{}
		diagnostics = append(diagnostics, Diagnostic{
			Phase:    PhasePreflight,
			Severity: SeverityNotice,
			Code:     CodeAutomaticallyUpdated,
			Subject:  skill.StoreName,
			Message:  "automatically updated",
			Details:  map[string]any{"hash": skill.Hash},
			Actions:  []Action{},
		})
	}

	for _, check := range result.Checks {
		if check.Error != "" {
			diagnostics = append(diagnostics, Diagnostic{
				Phase:    PhasePreflight,
				Severity: SeverityWarning,
				Code:     CodeUpdateCheckFailed,
				Subject:  check.StoreName,
				Message:  check.Error,
				Details:  updateCheckDetails(check),
				Actions:  []Action{},
			})
			continue
		}
		if !check.Changed {
			continue
		}
		if _, ok := updatedSet[check.StoreName]; ok {
			continue
		}

		code := CodeUpdateAvailable
		message := "update available"
		actions := []Action{{
			Label:   "next",
			Command: []string{"upskill", "update", "--diff", check.StoreName},
		}}
		if check.UpdatePolicy == config.UpdatePolicyAuto {
			code = CodeAutomaticUpdatePending
			message = "automatic update pending"
			actions = []Action{{Label: "next", Command: []string{"upskill", "sync"}}}
		}
		diagnostics = append(diagnostics, Diagnostic{
			Phase:    PhasePreflight,
			Severity: SeverityNotice,
			Code:     code,
			Subject:  check.StoreName,
			Message:  message,
			Details:  updateCheckDetails(check),
			Actions:  actions,
		})
	}

	Sort(diagnostics)
	return diagnostics
}

func Sort(items []Diagnostic) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if phaseRank(left.Phase) != phaseRank(right.Phase) {
			return phaseRank(left.Phase) < phaseRank(right.Phase)
		}
		if severityRank(left.Severity) != severityRank(right.Severity) {
			return severityRank(left.Severity) < severityRank(right.Severity)
		}
		if left.Subject != right.Subject {
			return left.Subject < right.Subject
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}

func warningCode(warningType string) Code {
	switch warningType {
	case library.UpdateCheckWarningRemoteDeferred:
		return CodeRemoteCheckDeferred
	case library.UpdateCheckWarningRemoteFailed:
		return CodeRemoteCheckFailed
	case library.UpdateCheckWarningCacheIgnored:
		return CodeCacheIgnored
	default:
		return CodeUpdateCheckFailed
	}
}

func updateCheckDetails(check library.UpdateCheck) map[string]any {
	details := map[string]any{
		"changed":       check.Changed,
		"stale":         check.Stale,
		"update_policy": check.UpdatePolicy,
	}
	if check.CheckOrigin != "" {
		details["check_origin"] = check.CheckOrigin
	}
	if check.CheckedAt != "" {
		details["checked_at"] = check.CheckedAt
	}
	return details
}

func severityRank(severity Severity) int {
	if severity == SeverityWarning {
		return 0
	}
	return 1
}

func phaseRank(phase Phase) int {
	if phase == PhasePreflight {
		return 0
	}
	return 1
}
