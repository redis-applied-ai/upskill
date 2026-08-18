package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var raw string

// Current returns the release version declared for this source tree.
func Current() string {
	return strings.TrimSpace(raw)
}
