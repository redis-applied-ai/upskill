package preview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"upskill/internal/config"
)

// Normalize adds the common preview contract to a mutation-specific result.
func Normalize(cfg config.Config, name string, value any, dryRun bool) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	result := map[string]any{}
	if err := json.Unmarshal(data, &result); err != nil {
		return value
	}
	if actual, ok := result["dry_run"].(bool); ok {
		dryRun = actual
	} else {
		result["dry_run"] = dryRun
	}
	result["applied"] = !dryRun
	if _, ok := result["warnings"]; !ok {
		result["warnings"] = []any{}
	}
	result["actions"] = mutationActions(cfg, name, result)
	result["canonical_resources"] = canonicalResources(result)
	return result
}

func mutationActions(cfg config.Config, name string, result map[string]any) []map[string]any {
	actions := make([]map[string]any, 0)
	operation := "write"
	if name == "delete_skill_overlay_file" || name == "delete_skill_draft_file" || name == "delete_skill_draft" {
		operation = "delete"
	}

	switch name {
	case "promote_skill_draft":
		destination, _ := result["destination"].(string)
		actions = appendFileActions(actions, operation, destination, result["files"])
	case "import_skill_draft_archive", "import_skill_draft_file":
		draftID, _ := result["draft_id"].(string)
		draftName := strings.TrimPrefix(draftID, "draft:")
		actions = appendFileActions(actions, operation, filepath.Join(cfg.DraftsDir, draftName), result["files"])
	case "sync_skills":
		actions = appendSyncActions(actions, cfg, result)
	default:
		target := firstString(result, "abs_path", "target", "path", "destination")
		if target != "" {
			action := map[string]any{"operation": operation, "target": target}
			if operation == "write" {
				action["overwrite"], _ = result["overwrite"].(bool)
			}
			actions = append(actions, action)
		}
	}
	sort.Slice(actions, func(i, j int) bool {
		left, right := fmt.Sprint(actions[i]["target"]), fmt.Sprint(actions[j]["target"])
		if left != right {
			return left < right
		}
		return fmt.Sprint(actions[i]["operation"]) < fmt.Sprint(actions[j]["operation"])
	})
	return actions
}

func appendFileActions(actions []map[string]any, operation string, root string, rawFiles any) []map[string]any {
	files, _ := rawFiles.([]any)
	for _, rawFile := range files {
		file, _ := rawFile.(map[string]any)
		path, _ := file["path"].(string)
		if path == "" {
			continue
		}
		target := path
		if root != "" && !filepath.IsAbs(path) {
			target = filepath.Join(root, filepath.FromSlash(path))
		}
		action := map[string]any{"operation": operation, "target": target}
		if operation == "write" {
			action["overwrite"], _ = file["overwrite"].(bool)
		}
		actions = append(actions, action)
	}
	return actions
}

func appendSyncActions(actions []map[string]any, cfg config.Config, result map[string]any) []map[string]any {
	for _, key := range []string{"planned_updates", "updated"} {
		items, _ := result[key].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			name := firstString(item, "store_name", "StoreName")
			if name != "" {
				actions = append(actions, map[string]any{"operation": "sync", "target": filepath.Join(cfg.StoreDir, name)})
			}
		}
	}
	for _, key := range []string{"planned_renders", "rendered"} {
		items, _ := result[key].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			if target := firstString(item, "RenderedPath", "rendered_path"); target != "" {
				actions = append(actions, map[string]any{"operation": "render", "target": target, "overwrite": true})
			}
			for _, copied := range stringSlice(firstValue(item, "CopiedTo", "copied_to")) {
				actions = append(actions, map[string]any{"operation": "write", "target": copied, "overwrite": true})
			}
		}
	}
	return actions
}

func canonicalResources(result map[string]any) map[string]any {
	resources := map[string]any{}
	for _, key := range []string{"skill_id", "draft_id", "source_id", "source_name", "source_display_name", "resolved_path"} {
		if value, ok := result[key]; ok && value != "" {
			resources[key] = value
		}
	}
	return resources
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func stringSlice(value any) []string {
	raw, _ := value.([]any)
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			values = append(values, text)
		}
	}
	return values
}

// MarkPartialActionStatuses prevents a partial apply from claiming full success.
func MarkPartialActionStatuses(value any) any {
	result, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result["applied"] = false
	actions, _ := result["actions"].([]map[string]any)
	for _, action := range actions {
		target, _ := action["target"].(string)
		if _, err := os.Stat(target); err == nil {
			action["status"] = "completed"
		} else {
			action["status"] = "failed"
		}
	}
	return result
}
