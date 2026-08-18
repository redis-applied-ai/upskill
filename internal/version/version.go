package version

import (
	"strings"
)

var value = "dev"

// Current returns the version injected by a release build.
func Current() string {
	return value
}

// Semver returns the current version without a leading v.
func Semver() string {
	return strings.TrimPrefix(value, "v")
}
