package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyDirCopyFileWalkFilesAndIsJ2(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "skip", "b.txt"), "b")
	write(t, filepath.Join(src, "template.md.j2"), "j2")

	err := CopyDir(src, dst, func(rel string, entry os.DirEntry) bool {
		return rel == "skip" || IsJ2(rel)
	})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "a.txt")); err != nil || string(data) != "a" {
		t.Fatalf("copy failed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "skip")); !os.IsNotExist(err) {
		t.Fatalf("skip dir copied")
	}
	if _, err := os.Stat(filepath.Join(dst, "template.md.j2")); !os.IsNotExist(err) {
		t.Fatalf("j2 file copied")
	}
	files, err := WalkFiles(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %#v", files)
	}
	if !IsJ2("x.j2") || IsJ2("x.md") {
		t.Fatal("IsJ2 failed")
	}
}

func TestCopyDirCopiesNestedDirectoriesAndErrors(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	write(t, filepath.Join(src, "nested", "file.txt"), "x")
	if err := CopyDir(src, dst, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "nested", "file.txt")); err != nil || string(data) != "x" {
		t.Fatalf("nested copy = %q %v", data, err)
	}
	if err := CopyDir(filepath.Join(root, "missing"), filepath.Join(root, "x"), nil); err == nil {
		t.Fatal("expected missing source error")
	}
	if _, err := WalkFiles(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected WalkFiles missing error")
	}
	if err := CopyFile(filepath.Join(root, "missing"), filepath.Join(root, "out")); err == nil {
		t.Fatal("expected missing source CopyFile error")
	}
	blocker := filepath.Join(root, "blocker")
	write(t, blocker, "block")
	if err := CopyFile(filepath.Join(src, "nested", "file.txt"), filepath.Join(blocker, "child")); err == nil {
		t.Fatal("expected mkdir blocker error")
	}
	if err := CopyFile(filepath.Join(src, "nested", "file.txt"), dst); err == nil {
		t.Fatal("expected destination directory open error")
	}
	if err := CopyFile(filepath.Join(src, "nested", "file.txt"), "bad\x00path"); err == nil {
		t.Fatal("expected invalid destination open error")
	}
}
