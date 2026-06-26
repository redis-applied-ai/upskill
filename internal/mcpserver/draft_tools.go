package mcpserver

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"upskill/internal/config"
	"upskill/internal/draft"
	"upskill/internal/fsutil"
	"upskill/internal/library"
	"upskill/internal/pathutil"
	"upskill/internal/render"
)

var (
	mcpCopyDir     = fsutil.CopyDir
	mcpDraftRemove = draft.Remove
	mcpOsRemoveAll = os.RemoveAll
	mcpOsStat      = os.Stat
	mcpOsWriteFile = os.WriteFile
)

func draftToolList() []map[string]any {
	return []map[string]any{
		tool("create_skill_draft", "Create a mutable draft skill. Existing base skills create version drafts by default; unknown names create new-skill drafts.", map[string]any{
			"skill_name":       stringProp("Draft skill name."),
			"from_skill":       stringProp("Optional installed base skill to copy into a version draft."),
			"from_rendered":    boolProp("Create the draft from rendered output instead of base files."),
			"template":         stringProp("Optional template label for new draft skeletons."),
			"allow_existing":   boolProp("Return an existing draft only when it matches the requested draft kind."),
			"replace_existing": boolProp("Delete an existing same-named draft before creating the requested draft."),
		}, []string{"skill_name"}),
		tool("list_skill_drafts", "List mutable skill drafts.", map[string]any{
			"query": stringProp("Optional search text."),
			"limit": intProp("Maximum number of drafts to return."),
		}, []string{}),
		tool("read_skill_draft", "Read one draft skill entrypoint and optional file manifest.", map[string]any{
			"draft_id":      stringProp("Draft identifier, for example draft:redis."),
			"include_files": boolProp("Include a file manifest."),
			"file_limit":    intProp("Maximum number of files to include in the manifest."),
		}, []string{"draft_id"}),
		tool("list_skill_draft_files", "List files in a draft skill bundle.", map[string]any{
			"draft_id":       stringProp("Draft identifier, for example draft:redis."),
			"path_prefix":    stringProp("Optional relative path prefix."),
			"recursive":      boolProp("Recurse into nested directories."),
			"include_hashes": boolProp("Include SHA-256 hashes for files."),
			"limit":          intProp("Maximum number of entries to return."),
		}, []string{"draft_id"}),
		tool("read_skill_draft_file", "Read one text file from a draft skill bundle.", map[string]any{
			"draft_id":  stringProp("Draft identifier, for example draft:redis."),
			"path":      stringProp("Relative file path."),
			"max_bytes": intProp("Maximum bytes to return."),
		}, []string{"draft_id", "path"}),
		tool("upsert_skill_draft_file", "Create or update one draft file. This never writes base skills.", map[string]any{
			"draft_id":       stringProp("Draft identifier, for example draft:redis."),
			"path":           stringProp("Relative file path."),
			"content":        stringProp("Plain-text file content."),
			"base64_content": stringProp("Base64-encoded file content."),
		}, []string{"draft_id", "path"}),
		tool("delete_skill_draft_file", "Delete one draft file. This never deletes base skills or draft metadata.", map[string]any{
			"draft_id": stringProp("Draft identifier, for example draft:redis."),
			"path":     stringProp("Relative file path."),
		}, []string{"draft_id", "path"}),
		tool("delete_skill_draft", "Delete one mutable draft bundle. This never deletes base skills.", map[string]any{
			"draft_id":   stringProp("Draft identifier, for example draft:redis."),
			"skill_id":   stringProp("Alias for draft_id."),
			"skill_name": stringProp("Alias for draft name."),
		}, []string{}),
		tool("import_skill_draft_archive", "Import a zip or tar.gz skill bundle into a draft from an allowed local path or URL.", map[string]any{
			"draft_id":         stringProp("Draft identifier, for example draft:redis."),
			"archive_path":     stringProp("Local archive path. MCP calls must use configured import directories."),
			"archive_url":      stringProp("Archive URL."),
			"sha256":           stringProp("Expected sha256 checksum, with optional sha256: prefix."),
			"strip_components": intProp("Number of leading path components to strip."),
			"inspect":          boolProp("Preview the import without writing files."),
		}, []string{"draft_id"}),
		tool("import_skill_draft_file", "Import one resource file into a draft from an allowed local path or URL.", map[string]any{
			"draft_id":    stringProp("Draft identifier, for example draft:redis."),
			"source_path": stringProp("Local source file path. MCP calls must use configured import directories."),
			"source_url":  stringProp("Source URL."),
			"path":        stringProp("Draft-relative destination path."),
			"sha256":      stringProp("Expected sha256 checksum, with optional sha256: prefix."),
			"inspect":     boolProp("Preview the import without writing files."),
		}, []string{"draft_id", "path"}),
		tool("render_skill_draft", "Render one draft skill without mutating base skills.", map[string]any{
			"draft_id":       stringProp("Draft identifier, for example draft:redis."),
			"skill_id":       stringProp("Alias for draft_id."),
			"agent":          stringProp("Optional agent name for overlay selection and copy target selection."),
			"copy":           boolProp("Copy rendered output to configured target directories."),
			"apply_overlays": boolProp("Apply global and agent overlays while rendering this draft."),
		}, []string{}),
		tool("diff_skill_draft", "Diff a draft against its current base skill when it is a version draft.", map[string]any{
			"draft_id": stringProp("Draft identifier, for example draft:redis."),
		}, []string{"draft_id"}),
		tool("rebase_skill_draft", "Check whether a version draft can be rebased onto the current base skill.", map[string]any{
			"draft_id": stringProp("Draft identifier, for example draft:redis."),
		}, []string{"draft_id"}),
		tool("promote_skill_draft", "Copy a draft into a local upstream checkout or named local source. Dry-run is true by default. Managed upskill store, rendered, overlay, draft, and import directories are rejected.", map[string]any{
			"draft_id":   stringProp("Draft identifier, for example draft:redis."),
			"skill_id":   stringProp("Alias for draft_id."),
			"skill_name": stringProp("Alias for draft name."),
			"to_path":    stringProp("Local upstream checkout or skill directory destination."),
			"to_source":  stringProp("Configured local source name to promote into."),
			"dry_run":    boolProp("Preview planned writes without changing files. Defaults to true."),
		}, []string{}),
	}
}

func findSkills(cfg config.Config, query string, limit int) ([]map[string]any, error) {
	installed, err := library.ListInstalled(cfg)
	if err != nil {
		return nil, err
	}
	drafts, err := draft.List(cfg)
	if err != nil {
		return nil, err
	}
	var entries []map[string]any
	for _, skill := range installed {
		if !matchesSearch(query, skill.StoreName, skill.Name, skill.Description, skill.Body) {
			continue
		}
		entries = append(entries, map[string]any{
			"kind":        "base",
			"skill_id":    skill.StoreName,
			"store_name":  skill.StoreName,
			"name":        skill.Name,
			"description": skill.Description,
			"path":        skill.Path,
		})
		if limit > 0 && len(entries) >= limit {
			return entries, nil
		}
	}
	for _, item := range drafts {
		body := readOptional(filepath.Join(item.Path, "SKILL.md"))
		description := draftDescription(item)
		if !matchesSearch(query, item.ID, item.StoreName, item.Metadata.Type, item.Metadata.ParentSkill, description, body) {
			continue
		}
		entries = append(entries, map[string]any{
			"kind":        "draft",
			"skill_id":    item.ID,
			"store_name":  item.StoreName,
			"name":        item.StoreName,
			"description": description,
			"path":        item.Path,
			"metadata":    item.Metadata,
		})
		if limit > 0 && len(entries) >= limit {
			return entries, nil
		}
	}
	return entries, nil
}

func createSkillDraft(cfg config.Config, args map[string]any) (any, error) {
	name := strArg(args, "skill_name")
	if name == "" {
		return nil, fmt.Errorf("skill_name is required")
	}
	if draft.Exists(cfg, name) {
		if boolArg(args, "replace_existing", false) {
			if err := mcpDraftRemove(cfg, name); err != nil {
				return nil, err
			}
		} else if boolArg(args, "allow_existing", false) {
			item, err := draft.Read(cfg, name)
			if err != nil {
				return nil, err
			}
			desiredType, desiredParent := requestedDraftShape(cfg, name, strArg(args, "from_skill"), boolArg(args, "from_rendered", false))
			if !draftMatchesRequest(item, desiredType, desiredParent) {
				return nil, fmt.Errorf("existing draft %s is a %s draft; requested %s draft%s. Use delete_skill_draft or replace_existing to replace it", item.ID, item.Metadata.Type, desiredType, requestedParentSuffix(desiredParent))
			}
			summary := draftSummary(item)
			summary["existing"] = true
			return summary, nil
		} else {
			return nil, fmt.Errorf("draft already exists: %s", draft.MakeID(name))
		}
	}
	item, err := createDraftForMCP(cfg, name, strArg(args, "from_skill"), boolArg(args, "from_rendered", false), strArg(args, "template"))
	if err != nil {
		return nil, err
	}
	return draftSummary(item), nil
}

func listSkillDrafts(cfg config.Config, args map[string]any) (any, error) {
	items, err := draft.List(cfg)
	if err != nil {
		return nil, err
	}
	limit := intArg(args, "limit", 100)
	query := strArg(args, "query")
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		body := readOptional(filepath.Join(item.Path, "SKILL.md"))
		summary := draftSummary(item)
		if matchesSearch(query, item.ID, item.StoreName, item.Metadata.Type, item.Metadata.ParentSkill, body) {
			out = append(out, summary)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func readSkillDraft(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(item.Path, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"skill_id": item.ID,
		"content":  string(data),
		"metadata": item.Metadata,
	}
	if boolArg(args, "include_files", true) {
		files, err := fileManifest(item.Path, "", true, false, intArg(args, "file_limit", 100))
		if err != nil {
			return nil, err
		}
		result["files"] = files
	}
	return result, nil
}

func listSkillDraftFiles(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	return fileManifest(item.Path, strArg(args, "path_prefix"), boolArg(args, "recursive", true), boolArg(args, "include_hashes", false), intArg(args, "limit", 500))
}

func readSkillDraftFile(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	return readFileFromRoot(item.Path, args)
}

func upsertSkillDraftFile(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	path := strArg(args, "path")
	if err := validateMutableDraftPath(path); err != nil {
		return nil, err
	}
	content, hasContent := args["content"]
	base64Content, hasBase64 := args["base64_content"]
	if hasContent == hasBase64 {
		return nil, fmt.Errorf("exactly one of content or base64_content is required")
	}
	var data []byte
	if hasBase64 {
		value, ok := base64Content.(string)
		if !ok {
			return nil, fmt.Errorf("base64_content must be a string")
		}
		data, err = base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
	} else {
		value, ok := content.(string)
		if !ok {
			return nil, fmt.Errorf("content must be a string")
		}
		data = []byte(value)
	}
	target, err := pathutil.SafeJoin(item.Path, path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"draft_id": item.ID, "path": filepath.ToSlash(filepath.Clean(path)), "updated": true, "base_mutated": false}, nil
}

func deleteSkillDraftFile(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	path := strArg(args, "path")
	if err := validateMutableDraftPath(path); err != nil {
		return nil, err
	}
	target, err := pathutil.SafeJoin(item.Path, path)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(target); err != nil {
		return nil, err
	}
	return map[string]any{"draft_id": item.ID, "path": filepath.ToSlash(filepath.Clean(path)), "deleted": true, "base_mutated": false}, nil
}

func deleteSkillDraft(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	if err := mcpDraftRemove(cfg, item.StoreName); err != nil {
		return nil, err
	}
	return map[string]any{"draft_id": item.ID, "deleted": true, "base_mutated": false}, nil
}

func importSkillDraftArchive(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	return draft.Import(cfg, item.StoreName, draft.ImportOptions{
		ArchivePath:       strArg(args, "archive_path"),
		ArchiveURL:        strArg(args, "archive_url"),
		SHA256:            strArg(args, "sha256"),
		StripComponents:   intArg(args, "strip_components", 0),
		Inspect:           boolArg(args, "inspect", false),
		EnforceImportDirs: true,
	})
}

func importSkillDraftFile(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	return draft.Import(cfg, item.StoreName, draft.ImportOptions{
		FilePath:          strArg(args, "source_path"),
		FileURL:           strArg(args, "source_url"),
		DestPath:          strArg(args, "path"),
		SHA256:            strArg(args, "sha256"),
		Inspect:           boolArg(args, "inspect", false),
		EnforceImportDirs: true,
	})
}

func renderDraftTool(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	return draft.Render(cfg, item.StoreName, draft.RenderOptions{
		Agent:         strArg(args, "agent"),
		Copy:          boolArg(args, "copy", false),
		ApplyOverlays: boolArg(args, "apply_overlays", false),
	})
}

func diffSkillDraft(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	return draft.Diff(cfg, item.StoreName)
}

func rebaseSkillDraft(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	return draft.Rebase(cfg, item.StoreName)
}

func promoteSkillDraft(cfg config.Config, args map[string]any) (any, error) {
	item, err := draftFromArgs(cfg, args)
	if err != nil {
		return nil, err
	}
	toSource := strArg(args, "to_source")
	toPath := strArg(args, "to_path")
	if toSource != "" {
		if toPath != "" {
			return nil, fmt.Errorf("use only one of to_path or to_source")
		}
		toPath, err = library.ResolveLocalSourceDestination(cfg, toSource)
		if err != nil {
			return nil, err
		}
	}
	if toPath == "" {
		return nil, fmt.Errorf("promotion destination is required")
	}
	if err := rejectManagedPromotionDestination(cfg, toPath); err != nil {
		return nil, err
	}
	return draft.Promote(cfg, item.StoreName, draft.PromoteOptions{
		ToPath:   toPath,
		ToSource: toSource,
		DryRun:   boolArg(args, "dry_run", true),
	})
}

func draftFromID(cfg config.Config, id string) (draft.Draft, error) {
	if id == "" {
		return draft.Draft{}, fmt.Errorf("draft_id is required")
	}
	if !draft.IsID(id) {
		return draft.Draft{}, fmt.Errorf("draft_id must use draft:<skill-name>")
	}
	return draft.Read(cfg, id)
}

func draftFromArgs(cfg config.Config, args map[string]any) (draft.Draft, error) {
	id := strArg(args, "draft_id")
	if id == "" {
		id = strArg(args, "skill_id")
	}
	if id == "" {
		name := strArg(args, "skill_name")
		if name != "" {
			return draft.Read(cfg, name)
		}
	}
	return draftFromID(cfg, id)
}

func isDraftID(id string) bool {
	return draft.IsID(id)
}

func resultSkillID(id string) string {
	if draft.IsID(id) {
		return draft.MakeID(id)
	}
	return library.NormalizeName(id)
}

func createDraftForMCP(cfg config.Config, name string, from string, fromRendered bool, template string) (draft.Draft, error) {
	storeName := library.NormalizeName(name)
	if storeName == "" {
		return draft.Draft{}, fmt.Errorf("draft name is required")
	}
	sourceName := library.NormalizeName(from)
	if sourceName == "" && storeSkillExistsForMCP(cfg, storeName) {
		sourceName = storeName
	}
	if sourceName != "" || fromRendered {
		if sourceName == "" {
			sourceName = storeName
		}
		return createVersionDraftForMCP(cfg, storeName, sourceName, fromRendered)
	}
	return createNewDraftForMCP(cfg, storeName, template)
}

func createNewDraftForMCP(cfg config.Config, storeName string, template string) (draft.Draft, error) {
	item, err := draft.Save(cfg, draft.Metadata{Name: storeName, Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton})
	if err != nil {
		return draft.Draft{}, err
	}
	skillPath := filepath.Join(item.Path, "SKILL.md")
	if _, err := mcpOsStat(skillPath); err == nil {
		return item, nil
	} else if !os.IsNotExist(err) {
		return draft.Draft{}, err
	}
	displayName := storeName
	if template != "" {
		displayName = fmt.Sprintf("%s (%s)", storeName, template)
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s skill instructions\n---\n\n# %s\n\nAdd skill instructions here.\n", storeName, storeName, displayName)
	if err := mcpOsWriteFile(skillPath, []byte(body), 0o644); err != nil {
		return draft.Draft{}, err
	}
	return item, nil
}

func createVersionDraftForMCP(cfg config.Config, storeName string, sourceName string, fromRendered bool) (draft.Draft, error) {
	baseDir := filepath.Join(cfg.StoreDir, sourceName)
	info, err := mcpOsStat(baseDir)
	if err != nil {
		return draft.Draft{}, err
	}
	if !info.IsDir() {
		return draft.Draft{}, fmt.Errorf("%s is not a directory", baseDir)
	}
	sourceDir := baseDir
	createdFrom := draft.CreatedFromBase
	if fromRendered {
		results, err := (render.Renderer{Config: cfg}).Render(render.Options{Skill: sourceName, Copy: false})
		if err != nil {
			return draft.Draft{}, err
		}
		sourceDir = results[0].RenderedPath
		createdFrom = draft.CreatedFromRendered
	}
	dst := draft.Path(cfg, storeName)
	if err := mcpOsRemoveAll(dst); err != nil {
		return draft.Draft{}, err
	}
	if err := mcpCopyDir(sourceDir, dst, func(rel string, _ os.DirEntry) bool {
		return rel == render.MarkerFile
	}); err != nil {
		return draft.Draft{}, err
	}
	parentSource, parentRevision, parentHash, err := parentMetadataForMCP(cfg, sourceName, baseDir)
	if err != nil {
		return draft.Draft{}, err
	}
	return draft.Save(cfg, draft.Metadata{
		Name:           storeName,
		Type:           draft.TypeVersion,
		ParentSkill:    sourceName,
		ParentSource:   parentSource,
		ParentRevision: parentRevision,
		ParentHash:     parentHash,
		CreatedFrom:    createdFrom,
	})
}

func parentMetadataForMCP(cfg config.Config, sourceName string, baseDir string) (string, string, string, error) {
	lock, err := library.ReadLock(cfg)
	if err != nil {
		return "", "", "", err
	}
	if entry, ok := lock.Skills[sourceName]; ok {
		return entry.Source, entry.Ref, entry.Hash, nil
	}
	hash, err := library.HashDir(baseDir)
	if err != nil {
		return "", "", "", err
	}
	return "", "", hash, nil
}

func storeSkillExistsForMCP(cfg config.Config, name string) bool {
	info, err := mcpOsStat(filepath.Join(cfg.StoreDir, library.NormalizeName(name)))
	return err == nil && info.IsDir()
}

func requestedDraftShape(cfg config.Config, name string, from string, fromRendered bool) (string, string) {
	storeName := library.NormalizeName(name)
	sourceName := library.NormalizeName(from)
	if sourceName == "" && storeSkillExistsForMCP(cfg, storeName) {
		sourceName = storeName
	}
	if sourceName != "" || fromRendered {
		if sourceName == "" {
			sourceName = storeName
		}
		return draft.TypeVersion, sourceName
	}
	return draft.TypeNew, ""
}

func draftMatchesRequest(item draft.Draft, desiredType string, desiredParent string) bool {
	if item.Metadata.Type != desiredType {
		return false
	}
	if desiredType == draft.TypeVersion && desiredParent != "" && item.Metadata.ParentSkill != desiredParent {
		return false
	}
	return true
}

func requestedParentSuffix(parent string) string {
	if parent == "" {
		return ""
	}
	return " of " + parent
}

func rejectManagedPromotionDestination(cfg config.Config, path string) error {
	managed := []struct {
		name string
		path string
	}{
		{"store", cfg.StoreDir},
		{"rendered", cfg.RenderedDir},
		{"overlays", cfg.OverlaysDir},
		{"drafts", cfg.DraftsDir},
		{"imports", cfg.ImportsDir},
	}
	for _, dir := range cfg.ImportDirs {
		managed = append(managed, struct {
			name string
			path string
		}{name: "import", path: dir})
	}
	for _, item := range managed {
		if item.path == "" {
			continue
		}
		if isInsidePath(path, item.path) {
			return fmt.Errorf("promotion destination is inside managed %s directory: %s", item.name, item.path)
		}
	}
	return nil
}

func isInsidePath(path string, root string) bool {
	absPath, _ := filepath.Abs(path)
	absRoot, _ := filepath.Abs(root)
	rel, _ := filepath.Rel(absRoot, absPath)
	return rel == "." || rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func readFileFromRoot(root string, args map[string]any) (any, error) {
	path := strArg(args, "path")
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	target, err := pathutil.SafeJoin(root, path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	originalSize := len(data)
	maxBytes := intArg(args, "max_bytes", 200000)
	if maxBytes > 0 && len(data) > maxBytes {
		data = data[:maxBytes]
	}
	return map[string]any{"path": path, "content": string(data), "size": originalSize}, nil
}

func validateMutableDraftPath(path string) error {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == draft.MetadataFile {
		return fmt.Errorf("%s cannot be changed through MCP tools", draft.MetadataFile)
	}
	return nil
}

func matchesSearch(query string, values ...string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func draftSummary(item draft.Draft) map[string]any {
	return map[string]any{
		"kind":        "draft",
		"skill_id":    item.ID,
		"store_name":  item.StoreName,
		"name":        item.StoreName,
		"description": draftDescription(item),
		"path":        item.Path,
		"metadata":    item.Metadata,
	}
}

func draftDescription(item draft.Draft) string {
	if item.Metadata.Type == draft.TypeVersion && item.Metadata.ParentSkill != "" {
		return "version draft of " + item.Metadata.ParentSkill
	}
	return "new draft skill"
}

func readOptional(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
