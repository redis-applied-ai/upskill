package mcpserver

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

type argumentValidationError struct {
	message string
}

func (e argumentValidationError) Error() string { return e.message }

func describeTool(name string) (map[string]any, error) {
	definition, ok := findToolDefinition(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	result := make(map[string]any, len(definition)+1)
	for key, value := range definition {
		result[key] = value
	}
	result["accepted_parameter_names"] = acceptedParameterNames(definition)
	return result, nil
}

func findToolDefinition(name string) (map[string]any, bool) {
	for _, definition := range toolList() {
		if definition["name"] == name {
			return definition, true
		}
	}
	return nil, false
}

func validateToolArguments(name string, args map[string]any) error {
	definition, ok := findToolDefinition(name)
	if !ok {
		return fmt.Errorf("unknown tool: %s", name)
	}
	properties := toolProperties(definition)
	unknown := unknownParameterNames(definition, args)
	if len(unknown) > 0 {
		parts := make([]string, 0, len(unknown))
		accepted := acceptedParameterNames(definition)
		for _, parameter := range unknown {
			part := fmt.Sprintf("unknown parameter %q", parameter)
			if suggestion := suggestParameter(parameter, accepted); suggestion != "" {
				part += fmt.Sprintf("; did you mean %q?", suggestion)
			}
			parts = append(parts, part)
		}
		return argumentValidationError{message: strings.Join(parts, "; ")}
	}
	if err := validateRequiredArguments(definition, args); err != nil {
		return err
	}

	for _, parameter := range acceptedParameterNames(definition) {
		value, present := args[parameter]
		if !present {
			continue
		}
		property, _ := properties[parameter].(map[string]any)
		expected, _ := property["type"].(string)
		if expected != "" && !matchesSchemaType(value, expected) {
			return argumentValidationError{message: fmt.Sprintf("parameter %q must be %s", parameter, expected)}
		}
		if choices, ok := property["enum"].([]string); ok {
			selected, _ := value.(string)
			if !slices.Contains(choices, selected) {
				return argumentValidationError{message: fmt.Sprintf("parameter %q must be one of %s", parameter, quotedList(choices))}
			}
		}
	}
	return nil
}

func validateRequiredArguments(definition map[string]any, args map[string]any) error {
	schema, _ := definition["inputSchema"].(map[string]any)
	required, _ := schema["required"].([]string)
	for _, parameter := range required {
		if _, ok := args[parameter]; !ok {
			return argumentValidationError{message: fmt.Sprintf("parameter %q is required", parameter)}
		}
	}
	constraints, _ := schema["allOf"].([]any)
	for _, rawConstraint := range constraints {
		constraint, _ := rawConstraint.(map[string]any)
		for _, keyword := range []string{"anyOf", "oneOf"} {
			rawAlternatives, ok := constraint[keyword].([]any)
			if !ok {
				continue
			}
			names := make([]string, 0, len(rawAlternatives))
			present := 0
			for _, rawAlternative := range rawAlternatives {
				alternative, _ := rawAlternative.(map[string]any)
				requiredNames, _ := alternative["required"].([]string)
				if len(requiredNames) != 1 {
					continue
				}
				name := requiredNames[0]
				names = append(names, name)
				if _, ok := args[name]; ok {
					present++
				}
			}
			if keyword == "anyOf" && present == 0 {
				return argumentValidationError{message: fmt.Sprintf("one of %s is required", quotedList(names))}
			}
			if keyword == "oneOf" && present == 0 {
				return argumentValidationError{message: fmt.Sprintf("exactly one of %s is required", quotedList(names))}
			}
			if keyword == "oneOf" && present > 1 {
				return argumentValidationError{message: fmt.Sprintf("use only one of %s", quotedList(names))}
			}
		}
	}
	return nil
}

func quotedList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, " or ")
}

func toolCallErrorPayload(name string, args map[string]any, callErr error) map[string]any {
	payload := map[string]any{
		"schema_version": 1,
		"error":          "tool_call_failed",
		"message":        callErr.Error(),
		"tool":           name,
	}
	definition, ok := findToolDefinition(name)
	if !ok {
		payload["error"] = "unknown_tool"
		payload["available_tool_names"] = toolNames()
		if suggestion := suggestParameter(name, toolNames()); suggestion != "" {
			payload["did_you_mean"] = map[string]string{name: suggestion}
		}
		return payload
	}
	var validationErr argumentValidationError
	if errors.As(callErr, &validationErr) {
		payload["error"] = "invalid_tool_arguments"
		payload["validation_path"] = []string{}
	}

	payload["accepted_parameter_names"] = acceptedParameterNames(definition)
	payload["inputSchema"] = definition["inputSchema"]
	unknown := unknownParameterNames(definition, args)
	if len(unknown) > 0 {
		payload["unknown_parameter_names"] = unknown
		suggestions := map[string]string{}
		accepted := acceptedParameterNames(definition)
		for _, parameter := range unknown {
			if suggestion := suggestParameter(parameter, accepted); suggestion != "" {
				suggestions[parameter] = suggestion
			}
		}
		if len(suggestions) > 0 {
			payload["did_you_mean"] = suggestions
		}
	}
	return payload
}

func serverInstructions() string {
	definitions := toolList()
	registered := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		name, _ := definition["name"].(string)
		registered[name] = true
	}
	var out strings.Builder
	out.WriteString("# Upskill MCP tools\n\n")
	out.WriteString("This reference is generated from the live tool registry. The schemas returned by `tools/list` and `describe_tool` are authoritative.\n\n")
	previewTools := make([]string, 0)
	for _, definition := range definitions {
		if _, ok := toolProperties(definition)["dry_run"]; ok {
			previewTools = append(previewTools, definition["name"].(string))
		}
	}
	if len(previewTools) > 0 {
		out.WriteString("## Preview convention\n\n")
		out.WriteString("For preview-capable mutations, call with `dry_run: true`, inspect the returned targets and actions, then repeat with `dry_run: false` to apply. Preview-capable tools: ")
		for index, name := range previewTools {
			if index > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "`%s`", name)
		}
		out.WriteString(".\n\n")
	}
	if registered["read_skill"] && registered["read_skill_file"] && registered["get_skill_stats"] && registered["record_skill_use"] && registered["record_skill_feedback"] {
		out.WriteString("## Skill activity\n\n")
		out.WriteString("Successful installed-skill calls to `read_skill` and `read_skill_file` each record one retrieval after content is read. Discovery and metadata operations such as `find_skills`, `list_skill_files`, and `get_skill_stats` do not record retrievals. Draft and source reads do not count.\n\n")
		out.WriteString("Retrieval is not use. Call `record_skill_use` only after materially applying the skill to a task; use an `idempotency_key` when a retry is possible.\n\n")
		out.WriteString("Call `record_skill_feedback` after real use when there is actionable positive or negative feedback. Every feedback call requires `sentiment`, `situation`, `details` describing what went right or wrong, and a concrete `proposed_change`. Feedback does not increment use.\n\n")
		out.WriteString("Statistics and feedback remain in Upskill's local activity log. They never change skills, sources, overlays, drafts, rendered output, or VCS state unless a user or agent explicitly performs a separate action. Automatic retrieval-accounting failures return valid content with `retrieval_recorded: false` and `activity_warnings`; explicit use and feedback calls fail if their event cannot be stored.\n\n")
	}
	out.WriteString("## Registry\n\n")
	for _, definition := range definitions {
		name, _ := definition["name"].(string)
		description, _ := definition["description"].(string)
		fmt.Fprintf(&out, "- `%s`: %s", name, description)
		parameters := acceptedParameterNames(definition)
		if len(parameters) == 0 {
			out.WriteString(" Accepted parameters: none.\n")
			continue
		}
		out.WriteString(" Accepted parameters: ")
		for index, parameter := range parameters {
			if index > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "`%s`", parameter)
		}
		out.WriteString(".\n")
	}
	return out.String()
}

func toolNames() []string {
	names := make([]string, 0, len(toolList()))
	for _, definition := range toolList() {
		if name, ok := definition["name"].(string); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func toolProperties(definition map[string]any) map[string]any {
	schema, _ := definition["inputSchema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return map[string]any{}
	}
	return properties
}

func acceptedParameterNames(definition map[string]any) []string {
	properties := toolProperties(definition)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func unknownParameterNames(definition map[string]any, args map[string]any) []string {
	properties := toolProperties(definition)
	unknown := make([]string, 0)
	for name := range args {
		if _, ok := properties[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func matchesSchemaType(value any, expected string) bool {
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch number := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float64:
			return !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
		default:
			return false
		}
	case "array":
		switch items := value.(type) {
		case []string:
			return true
		case []any:
			for _, item := range items {
				if _, ok := item.(string); !ok {
					return false
				}
			}
			return true
		default:
			return false
		}
	default:
		return true
	}
}

func suggestParameter(input string, accepted []string) string {
	aliases := map[string][]string{
		"destination": {"to_source", "to_path"},
		"source_name": {"source", "to_source"},
		"target":      {"to_source", "to_path", "path"},
	}
	for _, alias := range aliases[strings.ToLower(input)] {
		for _, candidate := range accepted {
			if candidate == alias {
				return candidate
			}
		}
	}
	best := ""
	bestDistance := -1
	for _, candidate := range accepted {
		distance := levenshteinDistance(strings.ToLower(input), strings.ToLower(candidate))
		if bestDistance < 0 || distance < bestDistance || distance == bestDistance && candidate < best {
			best = candidate
			bestDistance = distance
		}
	}
	limit := 2
	inputLength := len([]rune(input))
	if candidateLimit := (inputLength + 1) / 2; candidateLimit > limit {
		limit = candidateLimit
	}
	if bestDistance > limit {
		return ""
	}
	return best
}

func levenshteinDistance(left string, right string) int {
	a := []rune(left)
	b := []rune(right)
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for index := range previous {
		previous[index] = index
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = minInt(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

func minInt(values ...int) int {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}
