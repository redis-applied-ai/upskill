package pathutil

import (
	"path/filepath"
	"testing"
)

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	got, err := SafeJoin(root, filepath.Join("a", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "a", "b.txt") {
		t.Fatalf("got %s", got)
	}
	for _, rel := range []string{"", "/tmp/x", "..", "../x"} {
		if _, err := SafeJoin(root, rel); err == nil {
			t.Fatalf("expected error for %q", rel)
		}
	}
}

func TestNameHelpers(t *testing.T) {
	if StripJ2("SKILL.md.j2") != "SKILL.md" {
		t.Fatal("StripJ2 failed")
	}
	if AppendTargetName("SKILL.append.md") != "SKILL.md" {
		t.Fatal("AppendTargetName failed")
	}
}
