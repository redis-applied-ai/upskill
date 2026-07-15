package mcpserver

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var callableToolsMarker = regexp.MustCompile(`<!--\s*upskill-callable-tools(-ignore)?:\s*([^>]+?)\s*-->`)

func lintCallableToolMarkers(content string) error {
	registered := map[string]bool{}
	for _, name := range toolNames() {
		registered[name] = true
	}
	ignored := map[string]bool{}
	marked := make([]string, 0)
	for _, match := range callableToolsMarker.FindAllStringSubmatch(content, -1) {
		for _, rawName := range strings.Split(match[2], ",") {
			name := strings.TrimSpace(rawName)
			if name == "" {
				continue
			}
			if match[1] != "" {
				ignored[name] = true
				continue
			}
			marked = append(marked, name)
		}
	}
	unknown := make([]string, 0)
	for _, name := range marked {
		if !registered[name] && !ignored[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("documentation marks unregistered callable tools: %s", strings.Join(unknown, ", "))
}
