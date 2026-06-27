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
	"strconv"
	"strings"

	"upskill/internal/config"
	"upskill/internal/library"
	"upskill/internal/overlay"
	"upskill/internal/render"
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
	Agent         string                   `json:"agent,omitempty"`
	Updated       []library.InstalledSkill `json:"updated"`
	UpdatedCount  int                      `json:"updated_count"`
	Rendered      []render.Result          `json:"rendered"`
	RenderedCount int                      `json:"rendered_count"`
	Warnings      []string                 `json:"warnings,omitempty"`
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
			"serverInfo": map[string]any{"name": "upskill", "version": "0.1.0"},
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
		if err != nil {
			resp.Result = toolResult(err.Error(), true)
			return resp
		}
		resp.Result = toolResult(result, false)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return resp
}

func toolList() []map[string]any {
	tools := []map[string]any{
		tool("find_skills", "Find or list installed skills.", map[string]any{
			"query": stringProp("Optional search text."),
			"limit": intProp("Maximum number of skills to return."),
		}, []string{}),
		tool("read_skill", "Read a base or rendered skill entrypoint and optional file manifest.", map[string]any{
			"skill_id":      stringProp("Skill identifier."),
			"agent":         stringProp("Optional agent name for rendered overlays."),
			"rendered":      boolProp("Read rendered output when true; read base skill when false."),
			"base_only":     boolProp("Render without overlays."),
			"include_files": boolProp("Include a file manifest."),
			"file_limit":    intProp("Maximum number of files to include in the manifest."),
		}, []string{"skill_id"}),
		tool("list_skill_files", "List files in a base or rendered skill bundle.", map[string]any{
			"skill_id":       stringProp("Skill identifier."),
			"agent":          stringProp("Optional agent name for rendered overlays."),
			"rendered":       boolProp("List rendered output when true; list base skill when false."),
			"base_only":      boolProp("Render without overlays."),
			"path_prefix":    stringProp("Optional relative path prefix."),
			"recursive":      boolProp("Recurse into nested directories."),
			"include_hashes": boolProp("Include SHA-256 hashes for files."),
			"limit":          intProp("Maximum number of entries to return."),
		}, []string{"skill_id"}),
		tool("read_skill_file", "Read one text file from a base or rendered skill bundle.", map[string]any{
			"skill_id":  stringProp("Skill identifier."),
			"path":      stringProp("Relative file path."),
			"agent":     stringProp("Optional agent name for rendered overlays."),
			"rendered":  boolProp("Read rendered output when true; read base skill when false."),
			"base_only": boolProp("Render without overlays."),
			"max_bytes": intProp("Maximum bytes to return."),
		}, []string{"skill_id", "path"}),
		tool("render_skill", "Render one skill without mutating base skills.", map[string]any{
			"skill_id":       stringProp("Skill identifier."),
			"agent":          stringProp("Optional agent name for overlays and configured output selection."),
			"copy":           boolProp("Copy rendered output to configured agent output directories."),
			"base_only":      boolProp("Render base skills without overlays."),
			"apply_overlays": boolProp("Apply overlays when rendering draft skills."),
		}, []string{"skill_id"}),
		tool("sync_skills", "Update locked base skills and render final outputs. Returns counts, rendered paths, and warnings.", map[string]any{
			"agent": stringProp("Optional agent name for rendered output selection."),
		}, []string{}),
		tool("upsert_skill_overlay_file", "Create or update one overlay file. This never writes base skills. Template overlays must be complete Jinja files under a .j2 path, not JSON variable maps.", map[string]any{
			"skill_id":       stringProp("Skill identifier."),
			"skill_name":     stringProp("Fallback skill name when skill_id is omitted."),
			"agent":          stringProp("Optional agent-specific overlay name."),
			"operation":      stringProp("Overlay operation: append, root_append, files, replace, template, or patch."),
			"path":           stringProp("Relative overlay path."),
			"content":        stringProp("Plain-text file content."),
			"base64_content": stringProp("Base64-encoded file content."),
		}, []string{"skill_id", "path"}),
		tool("delete_skill_overlay_file", "Delete one overlay file. This never deletes base skills.", map[string]any{
			"skill_id":  stringProp("Skill identifier."),
			"agent":     stringProp("Optional agent-specific overlay name."),
			"operation": stringProp("Overlay operation: append, root_append, files, replace, template, or patch."),
			"path":      stringProp("Relative overlay path."),
		}, []string{"skill_id", "path"}),
		tool("list_skill_overlays", "List global and agent-specific overlays.", map[string]any{
			"skill_id": stringProp("Optional skill identifier."),
			"agent":    stringProp("Optional agent-specific overlay name."),
		}, []string{}),
	}
	return append(tools, draftToolList()...)
}

func tool(name string, description string, properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": true,
		},
	}
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func intProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func callTool(cfg config.Config, name string, args map[string]any) (any, error) {
	switch name {
	case "find_skills":
		return findSkills(cfg, strArg(args, "query"), intArg(args, "limit", 10))
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

func readSkill(cfg config.Config, args map[string]any) (any, error) {
	root, err := skillRoot(cfg, args)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"skill_id": resultSkillID(strArg(args, "skill_id")),
		"content":  string(data),
	}
	if boolArg(args, "include_files", true) {
		files, err := fileManifest(root, "", true, false, intArg(args, "file_limit", 100))
		if err != nil {
			return nil, err
		}
		result["files"] = files
	}
	return result, nil
}

func readSkillFile(cfg config.Config, args map[string]any) (any, error) {
	root, err := skillRoot(cfg, args)
	if err != nil {
		return nil, err
	}
	return readFileFromRoot(root, args)
}

func syncSkillsTool(cfg config.Config, args map[string]any) (syncSkillsResult, error) {
	agent := strArg(args, "agent")
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
		Updated:       nonNilInstalled(updated),
		UpdatedCount:  len(updated),
		Rendered:      nonNilRendered(rendered),
		RenderedCount: len(rendered),
	}
	if len(updated) == 0 && len(rendered) == 0 {
		result.Warnings = []string{"no locked or installed skills were found"}
	}
	return result, nil
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
	path, err := overlay.UpsertFile(cfg, overlay.WriteOptions{
		Skill:         skill,
		Agent:         strArg(args, "agent"),
		Operation:     strArgDefault(args, "operation", "append"),
		Path:          strArg(args, "path"),
		Content:       strArg(args, "content"),
		Base64Content: strArg(args, "base64_content"),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill_id": skill, "path": path, "updated": true, "base_mutated": false}, nil
}

func deleteOverlay(cfg config.Config, args map[string]any) (any, error) {
	if isDraftID(strArg(args, "skill_id")) {
		return nil, fmt.Errorf("overlays apply to base skills; use draft file tools for draft skills")
	}
	skill := library.NormalizeName(strArg(args, "skill_id"))
	path, err := overlay.DeleteFile(cfg, overlay.WriteOptions{
		Skill:     skill,
		Agent:     strArg(args, "agent"),
		Operation: strArgDefault(args, "operation", "append"),
		Path:      strArg(args, "path"),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill_id": skill, "path": path, "deleted": true, "base_mutated": false}, nil
}

func toolResult(value any, isError bool) map[string]any {
	text := ""
	switch v := value.(type) {
	case string:
		text = v
	default:
		data, _ := json.MarshalIndent(v, "", "  ")
		text = string(data)
	}
	return map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": isError,
	}
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

func EncodeForTest(message any) []byte {
	data, _ := json.Marshal(message)
	var out bytes.Buffer
	_, _ = fmt.Fprintf(&out, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return out.Bytes()
}

func DecodeBase64ForTest(value string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(value)
}
