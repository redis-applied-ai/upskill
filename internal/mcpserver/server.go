package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"upskill/internal/activity"
	"upskill/internal/audit"
	"upskill/internal/config"
	"upskill/internal/library"
	"upskill/internal/overlay"
	"upskill/internal/render"
	appversion "upskill/internal/version"
)

var fileManifest = library.FileManifest

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type syncSkillsResult struct {
	Agent                string                        `json:"agent,omitempty"`
	DryRun               bool                          `json:"dry_run"`
	Applied              bool                          `json:"applied"`
	Updated              []library.InstalledSkill      `json:"updated"`
	UpdatedCount         int                           `json:"updated_count"`
	Rendered             []render.Result               `json:"rendered"`
	RenderedCount        int                           `json:"rendered_count"`
	PlannedUpdates       []library.UpdateCheck         `json:"planned_updates,omitempty"`
	PlannedUpdateCount   int                           `json:"planned_update_count,omitempty"`
	PlannedRenders       []render.Result               `json:"planned_renders,omitempty"`
	PlannedRenderedCount int                           `json:"planned_rendered_count,omitempty"`
	Warnings             []string                      `json:"warnings"`
	AgentOutputAudit     audit.AgentOutputAuditSummary `json:"agent_output_audit"`
}

type notice struct {
	Type      string `json:"type"`
	Phase     string `json:"phase,omitempty"`
	SkillID   string `json:"skill_id,omitempty"`
	Message   string `json:"message"`
	Error     string `json:"error,omitempty"`
	SourceKey string `json:"source_key,omitempty"`
	Stale     bool   `json:"stale,omitempty"`
}

type sourceRow struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name,omitempty"`
	DisplayName         string   `json:"display_name"`
	Source              string   `json:"source"`
	Ref                 string   `json:"ref,omitempty"`
	Subpath             string   `json:"subpath,omitempty"`
	CanonicalID         string   `json:"canonical_id"`
	DefaultUpdatePolicy string   `json:"default_update_policy"`
	Scope               string   `json:"scope"`
	WritePolicy         string   `json:"write_policy"`
	Authors             []string `json:"authors,omitempty"`
	Owners              []string `json:"owners,omitempty"`
	LLMGuidance         string   `json:"llm_guidance,omitempty"`
	PolicySource        string   `json:"policy_source"`
	ManifestPath        string   `json:"manifest_path,omitempty"`
	ManifestWarning     string   `json:"manifest_warning,omitempty"`
	RecommendedWorkflow string   `json:"recommended_workflow"`
	ResolvedPath        string   `json:"resolved_path,omitempty"`
}

func Serve(cfg config.Config, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	for {
		data, lineMode, err := readMessageFrame(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var req request
		if err := json.Unmarshal(data, &req); err != nil {
			if err := writeMessageFrame(out, response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: err.Error()}}, lineMode); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		resp := handle(cfg, req)
		if err := writeMessageFrame(out, resp, lineMode); err != nil {
			return err
		}
	}
}

func handle(cfg config.Config, req request) response {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo":   map[string]any{"name": "upskill", "version": appversion.Semver()},
			"instructions": serverInstructions(),
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolList()}
	case "tools/call":
		var params callParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		result, err := callTool(cfg, params.Name, params.Arguments)
		notices := []notice(nil)
		if !isActivityOnlyTool(params.Name) {
			notices = updateNotices(cfg)
		}
		if err != nil {
			payload := toolCallErrorPayload(params.Name, params.Arguments, err)
			if partial, ok := err.(partialResultProvider); ok {
				common := mutationResponse(cfg, params.Name, params.Arguments, partial.PartialResult())
				if commonMap, commonOK := markPartialActionStatuses(common).(map[string]any); commonOK {
					for key, value := range commonMap {
						payload[key] = value
					}
				}
			}
			resp.Result = toolResult(payload, true, notices)
			return resp
		}
		result = mutationResponse(cfg, params.Name, params.Arguments, result)
		resp.Result = toolResult(result, false, notices)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return resp
}

func isActivityOnlyTool(name string) bool {
	switch name {
	case "get_skill_stats", "record_skill_use", "record_skill_feedback":
		return true
	default:
		return false
	}
}

func updateNotices(cfg config.Config) []notice {
	result, err := library.CheckSkillUpdatesForPreflight(cfg)
	if err != nil {
		return []notice{{
			Type:    "update_check_failed",
			Phase:   "preflight",
			Message: "update check failed",
			Error:   err.Error(),
		}}
	}
	notices := []notice{}
	for _, warning := range result.Warnings {
		notices = append(notices, notice{
			Type:      warning.Type,
			Phase:     "preflight",
			Message:   warning.Message,
			SourceKey: warning.SourceKey,
			Stale:     warning.Type == library.UpdateCheckWarningRemoteDeferred && hasStaleUpdateCheck(result.Checks),
		})
	}
	for _, check := range result.Checks {
		if check.Error != "" {
			notices = append(notices, notice{
				Type:    "update_check_failed",
				Phase:   "preflight",
				SkillID: check.StoreName,
				Message: fmt.Sprintf("update check failed for %s", check.StoreName),
				Error:   check.Error,
			})
			continue
		}
		if check.Changed {
			notices = append(notices, notice{
				Type:    "update_available",
				Phase:   "preflight",
				SkillID: check.StoreName,
				Message: fmt.Sprintf("update available for %s", check.StoreName),
			})
		}
	}
	return notices
}

func hasStaleUpdateCheck(checks []library.UpdateCheck) bool {
	for _, check := range checks {
		if check.Stale {
			return true
		}
	}
	return false
}

func toolList() []map[string]any {
	tools := []map[string]any{
		tool("describe_tool", "Return one tool's authoritative description and full input schema from the live registry.", map[string]any{
			"name": stringProp("Exact tool name to describe."),
		}, []string{"name"}),
		tool("find_skills", "Find or list installed skills without recording a retrieval.", map[string]any{
			"query": stringProp("Optional search text."),
			"limit": intProp("Maximum number of skills to return."),
		}, []string{}),
		tool("list_sources", "List configured skill sources and source defaults.", map[string]any{}, []string{}),
		tool("find_source_skills", "Inspect or search the current skills available from a configured or raw source without installing them.", map[string]any{
			"source":  stringProp("Configured source name, ID, configured path, raw local path, GitHub shorthand, or Git URL."),
			"query":   stringProp("Optional search text. Omit to list source skills."),
			"limit":   intProp("Maximum number of source skills to return."),
			"ref":     stringProp("Optional Git branch, tag, or commit override."),
			"subpath": stringProp("Optional source subpath override."),
			"skill":   stringProp("Optional skill ID or name filter."),
		}, []string{"source"}),
		tool("upsert_source_skill_file", "Dry-run or write one file in a configured local source skill. This never writes store, rendered output, overlays, drafts, imports, or VCS state.", map[string]any{
			"source":               stringProp("Configured local source stable ID, configured name, display name (case-insensitive), exact source string, or normalized local path."),
			"skill_id":             stringProp("Skill identifier inside the source."),
			"skill_name":           stringProp("Alias for skill_id."),
			"path":                 stringProp("Skill-relative file path. Defaults to SKILL.md."),
			"content":              stringProp("Plain-text file content."),
			"base64_content":       stringProp("Base64-encoded file content."),
			"dry_run":              boolPropDefault("Preview planned write without changing files. Defaults to true.", true),
			"confirm_source_write": boolProp("Set after confirming the user wants a source edit instead of an overlay or draft workflow."),
			"reason":               stringProp("Reason for a direct source edit; required by draft_promote policy."),
		}, []string{"source"}),
		tool("check_skill_updates", "Check installed skills against their locked sources without updating them.", map[string]any{
			"skill_id":     stringProp("Optional installed skill identifier."),
			"skill_ids":    arrayProp("Optional installed skill identifiers."),
			"include_diff": boolProp("Include contextual unified-diff lines for changed skills."),
		}, []string{}),
		tool("read_skill", "Read a base or generated skill entrypoint and optional file manifest. A successful installed-skill read records one retrieval; a draft read does not.", map[string]any{
			"skill_id":      stringProp("Skill identifier."),
			"agent":         stringProp("Optional agent name for generated output customizations."),
			"rendered":      boolProp("Read generated output when true; read base skill when false."),
			"base_only":     boolProp("Build without customizations."),
			"include_files": boolProp("Include a file manifest."),
			"file_limit":    intProp("Maximum number of files to include in the manifest."),
		}, []string{"skill_id"}),
		tool("list_skill_files", "List files in a base or generated skill bundle without recording a retrieval.", map[string]any{
			"skill_id":       stringProp("Skill identifier."),
			"agent":          stringProp("Optional agent name for generated output customizations."),
			"rendered":       boolProp("List generated output when true; list base skill when false."),
			"base_only":      boolProp("Build without customizations."),
			"path_prefix":    stringProp("Optional relative path prefix."),
			"recursive":      boolProp("Recurse into nested directories."),
			"include_hashes": boolProp("Include SHA-256 hashes for files."),
			"limit":          intProp("Maximum number of entries to return."),
		}, []string{"skill_id"}),
		tool("read_skill_file", "Read one text file from a base or generated skill bundle. A successful installed-skill read records one retrieval; a draft read does not.", map[string]any{
			"skill_id":  stringProp("Skill identifier."),
			"path":      stringProp("Relative file path."),
			"agent":     stringProp("Optional agent name for generated output customizations."),
			"rendered":  boolProp("Read generated output when true; read base skill when false."),
			"base_only": boolProp("Build without customizations."),
			"max_bytes": intProp("Maximum bytes to return."),
		}, []string{"skill_id", "path"}),
		tool("get_skill_stats", "Return local retrieval, explicit use, and structured feedback statistics for an installed skill without recording a retrieval.", map[string]any{
			"skill_id": stringProp("Installed skill identifier."),
			"feedback_limit": map[string]any{
				"type":        "integer",
				"description": "Maximum recent feedback records to return. Defaults to 10 and is capped at 100.",
				"default":     activity.DefaultFeedbackLimit,
				"minimum":     0,
				"maximum":     activity.MaxFeedbackLimit,
			},
		}, []string{"skill_id"}),
		tool("record_skill_use", "Record one explicit use only after an agent materially applied an installed skill to a task. An idempotency key safely deduplicates retries.", map[string]any{
			"skill_id": stringProp("Installed skill identifier that was materially applied."),
			"agent": map[string]any{
				"type":        "string",
				"description": "Optional agent name associated with the use.",
				"maxLength":   activity.MaxAgentBytes,
			},
			"idempotency_key": stringProp("Optional caller-generated key. Reusing it for the same skill and event type returns the original event; conflicting reuse fails."),
		}, []string{"skill_id"}),
		tool("record_skill_feedback", "Record local positive or negative feedback from real skill use. Describe the situation, what went right or wrong, and a concrete proposed change. Feedback does not increment use. Do not include secrets, credentials, private customer data, or unnecessary personal data.", map[string]any{
			"skill_id": stringProp("Installed skill identifier the feedback concerns."),
			"sentiment": map[string]any{
				"type":        "string",
				"description": "Whether the use experience was positive or negative.",
				"enum":        []string{string(activity.SentimentPositive), string(activity.SentimentNegative)},
			},
			"situation": map[string]any{
				"type":        "string",
				"description": "The concrete task situation in which the skill was applied.",
				"maxLength":   activity.MaxFeedbackTextBytes,
			},
			"details": map[string]any{
				"type":        "string",
				"description": "What went wrong or right while following the skill.",
				"maxLength":   activity.MaxFeedbackTextBytes,
			},
			"proposed_change": map[string]any{
				"type":        "string",
				"description": "A concrete change that would improve or preserve the skill behavior.",
				"maxLength":   activity.MaxFeedbackTextBytes,
			},
			"agent": map[string]any{
				"type":        "string",
				"description": "Optional agent name associated with the feedback.",
				"maxLength":   activity.MaxAgentBytes,
			},
			"idempotency_key": stringProp("Optional caller-generated key. Reusing it for the same skill and event type returns the original event; conflicting reuse fails."),
		}, []string{"skill_id", "sentiment", "situation", "details", "proposed_change"}),
		tool("render_skill", "Build one skill's generated output without mutating base skills.", map[string]any{
			"skill_id":       stringProp("Skill identifier."),
			"agent":          stringProp("Optional agent name for customizations and configured output selection."),
			"copy":           boolProp("Copy generated output to configured agent output directories."),
			"base_only":      boolProp("Build base skills without customizations."),
			"apply_overlays": boolProp("Apply customizations when rendering draft skills."),
		}, []string{"skill_id"}),
		tool("sync_skills", "Update locked base skills and build final outputs. Use dry_run first to preview updates and render/copy targets. Returns counts, generated paths, and warnings.", map[string]any{
			"agent":   stringProp("Optional agent name for generated output selection."),
			"dry_run": boolPropDefault("Preview source updates and render/copy targets without changing files. Defaults to false for backward compatibility.", false),
		}, []string{}),
		tool("upsert_skill_overlay_file", "Create or update one local customization file. Use dry_run first to preview the target and overwrite status. This never writes base skills. Template customizations must be complete Jinja files under a .j2 path, not JSON variable maps.", map[string]any{
			"skill_id":       stringProp("Skill identifier."),
			"skill_name":     stringProp("Fallback skill name when skill_id is omitted."),
			"agent":          stringProp("Optional agent-specific customization name."),
			"operation":      overlayOperationProp(),
			"path":           stringProp("Relative overlay path."),
			"content":        stringProp("Plain-text file content."),
			"base64_content": stringProp("Base64-encoded file content."),
			"dry_run":        boolPropDefault("Preview the target and overwrite status without changing files. Defaults to false for backward compatibility.", false),
		}, []string{"path"}),
		tool("delete_skill_overlay_file", "Delete one overlay file. Use dry_run first to confirm the target. This never deletes base skills.", map[string]any{
			"skill_id":  stringProp("Skill identifier."),
			"agent":     stringProp("Optional agent-specific overlay name."),
			"operation": overlayOperationProp(),
			"path":      stringProp("Relative overlay path."),
			"dry_run":   boolPropDefault("Preview the deletion target without changing files. Defaults to false for backward compatibility.", false),
		}, []string{"skill_id", "path"}),
		tool("list_skill_overlays", "List global and agent-specific overlays.", map[string]any{
			"skill_id": stringProp("Optional skill identifier."),
			"agent":    stringProp("Optional agent-specific overlay name."),
		}, []string{}),
	}
	return append(tools, draftToolList()...)
}

func tool(name string, description string, properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
	addToolSchemaConstraints(name, schema)
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": schema,
	}
}

func addToolSchemaConstraints(name string, schema map[string]any) {
	switch name {
	case "upsert_source_skill_file":
		schema["allOf"] = []any{
			requiredAlternatives("anyOf", "skill_id", "skill_name"),
			requiredAlternatives("oneOf", "content", "base64_content"),
		}
	case "upsert_skill_overlay_file":
		schema["allOf"] = []any{
			requiredAlternatives("anyOf", "skill_id", "skill_name"),
			requiredAlternatives("oneOf", "content", "base64_content"),
		}
	case "upsert_skill_draft_file":
		schema["allOf"] = []any{requiredAlternatives("oneOf", "content", "base64_content")}
	case "delete_skill_draft", "render_skill_draft":
		schema["allOf"] = []any{requiredAlternatives("anyOf", "draft_id", "skill_id", "skill_name")}
	case "import_skill_draft_archive":
		schema["allOf"] = []any{requiredAlternatives("oneOf", "archive_path", "archive_url")}
	case "import_skill_draft_file":
		schema["allOf"] = []any{requiredAlternatives("oneOf", "source_path", "source_url")}
	case "promote_skill_draft":
		schema["allOf"] = []any{
			requiredAlternatives("anyOf", "draft_id", "skill_id", "skill_name"),
			requiredAlternatives("oneOf", "to_path", "to_source"),
		}
	}
}

func requiredAlternatives(keyword string, names ...string) map[string]any {
	alternatives := make([]any, 0, len(names))
	for _, name := range names {
		alternatives = append(alternatives, map[string]any{"required": []string{name}})
	}
	return map[string]any{keyword: alternatives}
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func boolPropDefault(description string, defaultValue bool) map[string]any {
	return map[string]any{"type": "boolean", "description": description, "default": defaultValue}
}

func intProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func arrayProp(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": description}
}

func callTool(cfg config.Config, name string, args map[string]any) (any, error) {
	if args == nil {
		args = map[string]any{}
	}
	if err := validateToolArguments(name, args); err != nil {
		return nil, err
	}
	switch name {
	case "describe_tool":
		return describeTool(strArg(args, "name"))
	case "find_skills":
		return findSkills(cfg, strArg(args, "query"), intArg(args, "limit", 10))
	case "list_sources":
		return listSources(cfg), nil
	case "find_source_skills":
		return findSourceSkills(cfg, args)
	case "upsert_source_skill_file":
		return upsertSourceSkillFile(cfg, args)
	case "check_skill_updates":
		return checkSkillUpdates(cfg, args)
	case "read_skill":
		return readSkill(cfg, args)
	case "list_skill_files":
		root, err := skillRoot(cfg, args)
		if err != nil {
			return nil, err
		}
		return fileManifest(root, strArg(args, "path_prefix"), boolArg(args, "recursive", true), boolArg(args, "include_hashes", false), intArg(args, "limit", 500))
	case "read_skill_file":
		return readSkillFile(cfg, args)
	case "get_skill_stats":
		return activity.Stats(cfg, strArg(args, "skill_id"), intArg(args, "feedback_limit", activity.DefaultFeedbackLimit))
	case "record_skill_use":
		return activity.RecordUse(cfg, activity.UseInput{
			SkillID:        strArg(args, "skill_id"),
			Agent:          strArg(args, "agent"),
			IdempotencyKey: strArg(args, "idempotency_key"),
			Channel:        activity.ChannelMCP,
			Operation:      "record_skill_use",
		})
	case "record_skill_feedback":
		return activity.RecordFeedback(cfg, activity.FeedbackInput{
			SkillID:        strArg(args, "skill_id"),
			Sentiment:      activity.Sentiment(strArg(args, "sentiment")),
			Situation:      strArg(args, "situation"),
			Details:        strArg(args, "details"),
			ProposedChange: strArg(args, "proposed_change"),
			Agent:          strArg(args, "agent"),
			IdempotencyKey: strArg(args, "idempotency_key"),
			Channel:        activity.ChannelMCP,
			Operation:      "record_skill_feedback",
		})
	case "render_skill":
		if isDraftID(strArg(args, "skill_id")) {
			return renderDraftTool(cfg, args)
		}
		skill := library.NormalizeName(strArg(args, "skill_id"))
		if skill == "" {
			return nil, fmt.Errorf("skill_id is required")
		}
		return (render.Renderer{Config: cfg}).Render(render.Options{
			Skill:    skill,
			Agent:    strArg(args, "agent"),
			Copy:     boolArg(args, "copy", false),
			BaseOnly: boolArg(args, "base_only", false),
		})
	case "sync_skills":
		return syncSkillsTool(cfg, args)
	case "upsert_skill_overlay_file":
		return writeOverlay(cfg, args)
	case "delete_skill_overlay_file":
		return deleteOverlay(cfg, args)
	case "list_skill_overlays":
		return overlay.List(cfg, library.NormalizeName(strArg(args, "skill_id")), strArg(args, "agent"))
	case "create_skill_draft":
		return createSkillDraft(cfg, args)
	case "list_skill_drafts":
		return listSkillDrafts(cfg, args)
	case "read_skill_draft":
		return readSkillDraft(cfg, args)
	case "list_skill_draft_files":
		return listSkillDraftFiles(cfg, args)
	case "read_skill_draft_file":
		return readSkillDraftFile(cfg, args)
	case "upsert_skill_draft_file":
		return upsertSkillDraftFile(cfg, args)
	case "delete_skill_draft_file":
		return deleteSkillDraftFile(cfg, args)
	case "delete_skill_draft":
		return deleteSkillDraft(cfg, args)
	case "import_skill_draft_archive":
		return importSkillDraftArchive(cfg, args)
	case "import_skill_draft_file":
		return importSkillDraftFile(cfg, args)
	case "render_skill_draft":
		return renderDraftTool(cfg, args)
	case "diff_skill_draft":
		return diffSkillDraft(cfg, args)
	case "rebase_skill_draft":
		return rebaseSkillDraft(cfg, args)
	case "promote_skill_draft":
		return promoteSkillDraft(cfg, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func listSources(cfg config.Config) []sourceRow {
	sources := config.SourceRecords(cfg.Sources)
	rows := make([]sourceRow, 0, len(sources))
	for _, source := range sources {
		source = config.NormalizeSource(source)
		policy := library.SourcePolicyForSource(cfg, source)
		resolvedPath := ""
		if resolution, err := library.ResolveConfiguredSource(cfg, source.ID); err == nil {
			resolvedPath = resolution.ResolvedPath
		}
		rows = append(rows, sourceRow{
			ID:                  source.ID,
			Name:                source.Name,
			DisplayName:         config.SourceDisplayName(source),
			Source:              source.Source,
			Ref:                 source.Ref,
			Subpath:             source.Subpath,
			CanonicalID:         config.SourceKey(source),
			DefaultUpdatePolicy: config.NormalizeUpdatePolicy(source.DefaultUpdatePolicy),
			Scope:               policy.Scope,
			WritePolicy:         policy.WritePolicy,
			Authors:             policy.Authors,
			Owners:              policy.Owners,
			LLMGuidance:         policy.LLMGuidance,
			PolicySource:        policy.PolicySource,
			ManifestPath:        policy.ManifestPath,
			ManifestWarning:     policy.ManifestWarning,
			RecommendedWorkflow: policy.RecommendedWorkflow,
			ResolvedPath:        resolvedPath,
		})
	}
	return rows
}

func findSourceSkills(cfg config.Config, args map[string]any) (map[string]any, error) {
	source := strArg(args, "source")
	if source == "" {
		return nil, fmt.Errorf("source is required")
	}
	limit := intArg(args, "limit", 100)
	opts := library.InstallOptions{
		SkillFilter: strArg(args, "skill"),
		Ref:         strArg(args, "ref"),
		Subpath:     strArg(args, "subpath"),
	}
	skills, total, err := library.SearchSource(cfg, source, strArg(args, "query"), opts, limit)
	if err != nil {
		return nil, err
	}
	policy, err := library.SourcePolicyForInput(cfg, source, opts)
	if err != nil {
		return nil, err
	}
	if skills == nil {
		skills = []library.SourceSkill{}
	}
	result := map[string]any{
		"source":               source,
		"query":                strArg(args, "query"),
		"total":                total,
		"skills":               skills,
		"source_policy":        policy,
		"recommended_workflow": policy.RecommendedWorkflow,
		"base_mutated":         false,
	}
	if resolution, resolveErr := library.ResolveConfiguredSource(cfg, source); resolveErr == nil {
		result["source_id"] = resolution.Source.ID
		result["source_name"] = resolution.ConfiguredAs
		result["source_display_name"] = config.SourceDisplayName(resolution.Source)
		result["resolved_path"] = resolution.ResolvedPath
	} else {
		var resolutionFailure library.SourceResolutionError
		if errors.As(resolveErr, &resolutionFailure) && resolutionFailure.Ambiguous {
			return nil, resolveErr
		}
	}
	return result, nil
}

func upsertSourceSkillFile(cfg config.Config, args map[string]any) (any, error) {
	skill := strArg(args, "skill_id")
	if skill == "" {
		skill = strArg(args, "skill_name")
	}
	return library.UpsertSourceSkillFile(cfg, library.SourceSkillFileWriteOptions{
		SourceName:         strArg(args, "source"),
		Skill:              skill,
		Path:               strArgDefault(args, "path", "SKILL.md"),
		Content:            strArg(args, "content"),
		Base64Content:      strArg(args, "base64_content"),
		DryRun:             boolArg(args, "dry_run", true),
		ConfirmSourceWrite: boolArg(args, "confirm_source_write", false),
		Reason:             strArg(args, "reason"),
	})
}

func checkSkillUpdates(cfg config.Config, args map[string]any) (map[string]any, error) {
	names := stringListArg(args, "skill_ids")
	if skill := strArg(args, "skill_id"); skill != "" {
		names = append(names, skill)
	}
	checks, err := library.CheckSkillUpdates(cfg, names, boolArg(args, "include_diff", false))
	if err != nil {
		return nil, err
	}
	if checks == nil {
		checks = []library.UpdateCheck{}
	}
	changed := 0
	failed := 0
	for _, check := range checks {
		if check.Error != "" {
			failed++
			continue
		}
		if check.Changed {
			changed++
		}
	}
	return map[string]any{
		"checks":        checks,
		"checked_count": len(checks),
		"changed_count": changed,
		"failed_count":  failed,
		"base_mutated":  false,
	}, nil
}

func readSkill(cfg config.Config, args map[string]any) (any, error) {
	rawID := strArg(args, "skill_id")
	root, err := skillRoot(cfg, args)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"skill_id": resultSkillID(rawID),
		"content":  string(data),
	}
	if !isDraftID(rawID) {
		if policy, ok, err := library.SourcePolicyForInstalledSkill(cfg, rawID); err == nil && ok {
			result["source_policy"] = policy
			result["recommended_workflow"] = policy.RecommendedWorkflow
			result["policy_notices"] = config.PolicyNotices(policy)
		}
	}
	if boolArg(args, "include_files", true) {
		files, err := fileManifest(root, "", true, false, intArg(args, "file_limit", 100))
		if err != nil {
			return nil, err
		}
		result["files"] = files
	}
	if !isDraftID(rawID) {
		addMCPRetrievalResult(cfg, result, rawID, "read_skill", strArg(args, "agent"))
	}
	return result, nil
}

func addMCPRetrievalResult(cfg config.Config, result map[string]any, skillID, operation, agent string) {
	identity, err := activity.ResolveSkill(cfg, skillID)
	if err == nil {
		var stats activity.SkillStats
		stats, err = activity.RecordRetrieval(cfg, identity, activity.ChannelMCP, operation, agent)
		if err == nil {
			result["retrieval_recorded"] = true
			result["stats"] = map[string]any{
				"retrieval_count": stats.RetrievalCount,
				"use_count":       stats.UseCount,
				"feedback_count":  stats.FeedbackCount,
			}
			return
		}
	}
	result["retrieval_recorded"] = false
	result["activity_warnings"] = []string{fmt.Sprintf("activity recording at %s failed: %v", cfg.StatsPath, err)}
}

func readSkillFile(cfg config.Config, args map[string]any) (any, error) {
	rawID := strArg(args, "skill_id")
	root, err := skillRoot(cfg, args)
	if err != nil {
		return nil, err
	}
	value, err := readFileFromRoot(root, args)
	if err != nil {
		return nil, err
	}
	if !isDraftID(rawID) {
		if result, ok := value.(map[string]any); ok {
			addMCPRetrievalResult(cfg, result, rawID, "read_skill_file", strArg(args, "agent"))
		}
	}
	return value, nil
}

func syncSkillsTool(cfg config.Config, args map[string]any) (syncSkillsResult, error) {
	agent := strArg(args, "agent")
	if boolArg(args, "dry_run", false) {
		return previewSyncSkills(cfg, agent)
	}
	updated, err := library.UpdateSkills(cfg, nil)
	if err != nil {
		return syncSkillsResult{}, err
	}
	rendered, err := (render.Renderer{Config: cfg}).Render(render.Options{Agent: agent, Copy: true})
	if err != nil {
		return syncSkillsResult{}, err
	}
	result := syncSkillsResult{
		Agent:         agent,
		DryRun:        false,
		Applied:       true,
		Updated:       nonNilInstalled(updated),
		UpdatedCount:  len(updated),
		Rendered:      nonNilRendered(rendered),
		RenderedCount: len(rendered),
	}
	if len(updated) == 0 && len(rendered) == 0 {
		result.Warnings = []string{"no locked or installed skills were found"}
	}
	findings := audit.AgentOutputWarnings(cfg, agent)
	result.AgentOutputAudit = audit.SummarizeAgentOutputWarnings(findings)
	result.Warnings = append(result.Warnings, audit.AgentOutputWarningMessagesFrom(findings)...)
	return result, nil
}

func previewSyncSkills(cfg config.Config, agent string) (syncSkillsResult, error) {
	checks, err := library.CheckSkillUpdates(cfg, nil, true)
	if err != nil {
		return syncSkillsResult{}, err
	}
	installed, err := library.ListInstalled(cfg)
	if err != nil {
		return syncSkillsResult{}, err
	}
	plannedUpdateCount := 0
	for _, check := range checks {
		if check.Changed && check.Error == "" {
			plannedUpdateCount++
		}
	}
	plannedRenders := plannedSyncRenders(cfg, agent, installed)
	findings := audit.AgentOutputWarnings(cfg, agent)
	auditSummary := audit.SummarizeAgentOutputWarnings(findings)
	result := syncSkillsResult{
		Agent:                agent,
		DryRun:               true,
		Applied:              false,
		Updated:              []library.InstalledSkill{},
		Rendered:             []render.Result{},
		PlannedUpdates:       checks,
		PlannedUpdateCount:   plannedUpdateCount,
		PlannedRenders:       plannedRenders,
		PlannedRenderedCount: len(plannedRenders),
		Warnings:             audit.AgentOutputWarningMessagesFrom(findings),
		AgentOutputAudit:     auditSummary,
	}
	if len(checks) == 0 && len(plannedRenders) == 0 {
		result.Warnings = append([]string{"no locked or installed skills were found"}, result.Warnings...)
	}
	return result, nil
}

func plannedSyncRenders(cfg config.Config, agent string, skills []library.Skill) []render.Result {
	outputs := make([]config.AgentOutput, 0, len(cfg.AgentOutputs))
	for _, output := range cfg.AgentOutputs {
		if agent == "" || output.Name == agent {
			outputs = append(outputs, output)
		}
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Name < outputs[j].Name })
	results := make([]render.Result, 0)
	for _, skill := range skills {
		if len(outputs) == 0 {
			agentKey := agent
			if agentKey == "" {
				agentKey = "generic"
			}
			results = append(results, render.Result{
				Skill:        skill.StoreName,
				Agent:        agent,
				RenderedPath: filepath.Join(cfg.RenderedDir, agentKey, skill.StoreName),
				Mode:         "preview",
			})
			continue
		}
		for _, output := range outputs {
			results = append(results, render.Result{
				Skill:        skill.StoreName,
				Agent:        output.Name,
				RenderedPath: filepath.Join(cfg.RenderedDir, output.Name, skill.StoreName),
				CopiedTo:     []string{filepath.Join(output.Path, skill.StoreName)},
				Mode:         "preview",
			})
		}
	}
	return results
}

func nonNilInstalled(items []library.InstalledSkill) []library.InstalledSkill {
	if items == nil {
		return []library.InstalledSkill{}
	}
	return items
}

func nonNilRendered(items []render.Result) []render.Result {
	if items == nil {
		return []render.Result{}
	}
	return items
}

func skillRoot(cfg config.Config, args map[string]any) (string, error) {
	rawID := strArg(args, "skill_id")
	if isDraftID(rawID) {
		item, err := draftFromID(cfg, rawID)
		if err != nil {
			return "", err
		}
		return item.Path, nil
	}
	skill := library.NormalizeName(rawID)
	if skill == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if !boolArg(args, "rendered", true) {
		root := filepath.Join(cfg.StoreDir, skill)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root, nil
		}
		return "", fmt.Errorf("skill not found: %s", skill)
	}
	results, err := (render.Renderer{Config: cfg}).Render(render.Options{
		Skill:    skill,
		Agent:    strArg(args, "agent"),
		Copy:     false,
		BaseOnly: boolArg(args, "base_only", false),
	})
	if err != nil {
		return "", err
	}
	return results[0].RenderedPath, nil
}

func writeOverlay(cfg config.Config, args map[string]any) (any, error) {
	if isDraftID(strArg(args, "skill_id")) {
		return nil, fmt.Errorf("overlays apply to base skills; use draft file tools for draft skills")
	}
	skill := library.NormalizeName(strArg(args, "skill_id"))
	if skill == "" {
		skill = library.NormalizeName(strArg(args, "skill_name"))
	}
	dryRun := boolArg(args, "dry_run", false)
	opts := overlay.WriteOptions{
		Skill:         skill,
		Agent:         strArg(args, "agent"),
		Operation:     strArgDefault(args, "operation", "append"),
		Path:          strArg(args, "path"),
		Content:       strArg(args, "content"),
		Base64Content: strArg(args, "base64_content"),
		DryRun:        true,
	}
	path, err := overlay.UpsertFile(cfg, opts)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	if !dryRun {
		opts.DryRun = false
		path, err = overlay.UpsertFile(cfg, opts)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"skill_id":     skill,
		"path":         path,
		"dry_run":      dryRun,
		"updated":      !dryRun,
		"overwrite":    statErr == nil,
		"base_mutated": false,
	}, nil
}

func deleteOverlay(cfg config.Config, args map[string]any) (any, error) {
	if isDraftID(strArg(args, "skill_id")) {
		return nil, fmt.Errorf("overlays apply to base skills; use draft file tools for draft skills")
	}
	skill := library.NormalizeName(strArg(args, "skill_id"))
	dryRun := boolArg(args, "dry_run", false)
	path, err := overlay.DeleteFile(cfg, overlay.WriteOptions{
		Skill:     skill,
		Agent:     strArg(args, "agent"),
		Operation: strArgDefault(args, "operation", "append"),
		Path:      strArg(args, "path"),
		DryRun:    dryRun,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill_id": skill, "path": path, "dry_run": dryRun, "deleted": !dryRun, "base_mutated": false}, nil
}

func toolResult(value any, isError bool, notices ...[]notice) map[string]any {
	text := ""
	switch v := value.(type) {
	case string:
		text = v
	default:
		data, _ := json.MarshalIndent(v, "", "  ")
		text = string(data)
	}
	result := map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": isError,
	}
	if isError {
		if structured, ok := value.(map[string]any); ok {
			result["structuredContent"] = structured
		}
	}
	if len(notices) > 0 && len(notices[0]) > 0 {
		result["notices"] = notices[0]
	}
	return result
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	data, _, err := readMessageFrame(reader)
	return data, err
}

func readMessageFrame(reader *bufio.Reader) ([]byte, bool, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, false, err
	}
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		return []byte(strings.TrimSpace(line)), true, nil
	}
	length := 0
	for {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(value))
		}
		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, false, err
		}
	}
	if length <= 0 {
		return nil, false, fmt.Errorf("missing Content-Length")
	}
	data := make([]byte, length)
	_, err = io.ReadFull(reader, data)
	return data, false, err
}

func writeMessage(out io.Writer, resp response) error {
	return writeMessageFrame(out, resp, false)
}

func writeMessageFrame(out io.Writer, resp response, lineMode bool) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if lineMode {
		_, err = fmt.Fprintf(out, "%s\n", data)
		return err
	}
	_, err = fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func strArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func strArgDefault(args map[string]any, key string, fallback string) string {
	if value := strArg(args, key); value != "" {
		return value
	}
	return fallback
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	boolValue, ok := value.(bool)
	if !ok {
		return fallback
	}
	return boolValue
}

func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func stringListArg(args map[string]any, key string) []string {
	value, ok := args[key]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func EncodeForTest(message any) []byte {
	data, _ := json.Marshal(message)
	var out bytes.Buffer
	_, _ = fmt.Fprintf(&out, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return out.Bytes()
}

func DecodeBase64ForTest(value string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(value)
}

func overlayOperationProp() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "Overlay operation. Use files to add a file; defaults to append.",
		"enum":        []string{"append", "root_append", "files", "replace", "template", "patch"},
	}
}
