package mcpserver

import (
	"upskill/internal/config"
	"upskill/internal/preview"
)

type partialResultProvider interface {
	PartialResult() any
}

func mutationResponse(cfg config.Config, name string, args map[string]any, value any) any {
	definition, ok := findToolDefinition(name)
	if !ok {
		return value
	}
	if _, previewCapable := toolProperties(definition)["dry_run"]; !previewCapable {
		return value
	}
	dryRun := boolArg(args, "dry_run", dryRunDefault(definition))
	return preview.Normalize(cfg, name, value, dryRun)
}

func dryRunDefault(definition map[string]any) bool {
	property, _ := toolProperties(definition)["dry_run"].(map[string]any)
	value, _ := property["default"].(bool)
	return value
}

func markPartialActionStatuses(value any) any {
	return preview.MarkPartialActionStatuses(value)
}
