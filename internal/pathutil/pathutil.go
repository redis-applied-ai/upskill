package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

func SafeJoin(root string, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty relative path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal is not allowed: %s", rel)
	}
	return filepath.Join(root, clean), nil
}

func StripJ2(rel string) string {
	return strings.TrimSuffix(rel, ".j2")
}

func AppendTargetName(name string) string {
	return strings.Replace(name, ".append.", ".", 1)
}
