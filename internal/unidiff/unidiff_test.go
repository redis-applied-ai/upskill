package unidiff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareDirsProducesContextualUnifiedDiff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	before := filepath.Join(root, "before")
	after := filepath.Join(root, "after")
	writeTestFile(t, filepath.Join(before, "SKILL.md"), strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"old value",
		"line 6",
		"line 7",
		"line 8",
		"line 9",
	}, "\n")+"\n")
	writeTestFile(t, filepath.Join(after, "SKILL.md"), strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"new value",
		"line 6",
		"line 7",
		"line 8",
		"line 9",
	}, "\n")+"\n")

	lines, err := CompareDirs(before, after, Options{Context: 2})
	if err != nil {
		t.Fatal(err)
	}
	for index, line := range lines {
		if strings.Contains(line, "\n") {
			t.Fatalf("line %d contains an embedded newline: %q", index, line)
		}
	}
	diff := strings.Join(lines, "\n")
	for _, want := range []string{
		"diff --git a/SKILL.md b/SKILL.md",
		"--- a/SKILL.md",
		"+++ b/SKILL.md",
		"@@ -3,5 +3,5 @@",
		" line 3",
		"-old value",
		"+new value",
		" line 7",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	for _, unwanted := range []string{" line 1", " line 9"} {
		if strings.Contains(diff, unwanted) {
			t.Fatalf("diff includes out-of-context line %q:\n%s", unwanted, diff)
		}
	}
}

func TestCompareDirsHandlesAddedDeletedAndMissingFinalNewline(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	before := filepath.Join(root, "before")
	after := filepath.Join(root, "after")
	writeTestFile(t, filepath.Join(before, "removed.txt"), "removed without newline")
	writeTestFile(t, filepath.Join(after, "added.txt"), "added without newline")

	lines, err := CompareDirs(before, after, Options{})
	if err != nil {
		t.Fatal(err)
	}
	diff := strings.Join(lines, "\n")
	for _, want := range []string{
		"diff --git a/added.txt b/added.txt",
		"--- /dev/null",
		"+++ b/added.txt",
		"@@ -0,0 +1 @@",
		"+added without newline\n\\ No newline at end of file",
		"diff --git a/removed.txt b/removed.txt",
		"--- a/removed.txt",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-removed without newline\n\\ No newline at end of file",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	if strings.Index(diff, "a/added.txt") > strings.Index(diff, "a/removed.txt") {
		t.Fatalf("diff files are not sorted:\n%s", diff)
	}
}

func TestCompareDirsDetectsFinalNewlineOnlyChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	before := filepath.Join(root, "before")
	after := filepath.Join(root, "after")
	writeTestFile(t, filepath.Join(before, "SKILL.md"), "same text\n")
	writeTestFile(t, filepath.Join(after, "SKILL.md"), "same text")

	lines, err := CompareDirs(before, after, Options{})
	if err != nil {
		t.Fatal(err)
	}
	diff := strings.Join(lines, "\n")
	for _, want := range []string{
		"-same text",
		"+same text\n\\ No newline at end of file",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
}

func TestCompareDirsSummarizesBinaryAndOversizedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	before := filepath.Join(root, "before")
	after := filepath.Join(root, "after")
	writeTestBytes(t, filepath.Join(before, "asset.bin"), []byte{0, 1, 2})
	writeTestBytes(t, filepath.Join(after, "asset.bin"), []byte{0, 1, 3})
	writeTestFile(t, filepath.Join(before, "large.txt"), "0123456789 before")
	writeTestFile(t, filepath.Join(after, "large.txt"), "0123456789 after")

	lines, err := CompareDirs(before, after, Options{MaxTextBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	diff := strings.Join(lines, "\n")
	for _, want := range []string{
		"Binary files a/asset.bin and b/asset.bin differ",
		"Files a/large.txt and b/large.txt differ (content diff omitted: exceeds 8 bytes)",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	for _, unwanted := range []string{"0123456789 before", "0123456789 after"} {
		if strings.Contains(diff, unwanted) {
			t.Fatalf("diff leaked oversized content %q:\n%s", unwanted, diff)
		}
	}
}

func TestCompareDirsSupportsOptionalBeforeAndIgnoredFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	after := filepath.Join(root, "after")
	writeTestFile(t, filepath.Join(after, "SKILL.md"), "new\n")
	writeTestFile(t, filepath.Join(after, ".marker"), "generated\n")

	lines, err := CompareDirs("", after, Options{
		BeforeOptional: true,
		Ignore: func(path string) bool {
			return path == ".marker"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	diff := strings.Join(lines, "\n")
	if !strings.Contains(diff, "+++ b/SKILL.md") || strings.Contains(diff, ".marker") {
		t.Fatalf("unexpected optional-root diff:\n%s", diff)
	}
	if _, err := CompareDirs(filepath.Join(root, "missing"), after, Options{}); err == nil {
		t.Fatal("missing required before directory unexpectedly succeeded")
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	writeTestBytes(t, path, []byte(content))
}

func writeTestBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
