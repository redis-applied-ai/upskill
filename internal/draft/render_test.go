package draft

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/config"
	"upskill/internal/library"
	"upskill/internal/render"
)

func TestRenderDraftAndCopy(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		DraftsDir:    filepath.Join(root, "drafts"),
		OverlaysDir:  filepath.Join(root, "overlays"),
		RenderedDir:  filepath.Join(root, "rendered"),
		AgentOutputs: []config.AgentOutput{{Name: "codex", Path: filepath.Join(root, "codex")}},
	}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "demo", "SKILL.md"), "# Demo\n")
	results, err := Render(cfg, "demo", RenderOptions{Agent: "codex", Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Mode != "draft" || len(results[0].CopiedTo) != 1 {
		t.Fatalf("results = %#v", results)
	}
	marker := readFile(t, filepath.Join(results[0].RenderedPath, render.MarkerFile))
	if !strings.Contains(marker, `"kind": "draft"`) || !strings.Contains(marker, `"skill_id": "draft:demo"`) {
		t.Fatalf("marker = %s", marker)
	}
	if readFile(t, filepath.Join(root, "codex", "demo", "SKILL.md")) != "# Demo\n" {
		t.Fatal("draft was not copied to agent output")
	}
	if _, err := os.Stat(filepath.Join(results[0].RenderedPath, MetadataFile)); !os.IsNotExist(err) {
		t.Fatalf("rendered metadata should be omitted: %v", err)
	}
}

func TestRenderDraftWithOverlays(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		DraftsDir:   filepath.Join(root, "drafts"),
		OverlaysDir: filepath.Join(root, "overlays"),
		RenderedDir: filepath.Join(root, "rendered"),
	}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "demo", "SKILL.md"), "Base.\n")
	writeFile(t, filepath.Join(root, "overlays", "demo", "SKILL.append.md"), "Overlay.\n")
	results, err := Render(cfg, "demo", RenderOptions{ApplyOverlays: true})
	if err != nil {
		t.Fatal(err)
	}
	rendered := readFile(t, filepath.Join(results[0].RenderedPath, "SKILL.md"))
	if !strings.Contains(rendered, "Overlay.") || results[0].Mode != "draft-with-overlays" {
		t.Fatalf("rendered = %q results=%#v", rendered, results)
	}
}

func TestDiffRebaseAndPromote(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		StoreDir:  filepath.Join(root, "store"),
		DraftsDir: filepath.Join(root, "drafts"),
	}
	writeFile(t, filepath.Join(root, "store", "demo", "SKILL.md"), "Base.\n")
	hash, err := library.HashDir(filepath.Join(root, "store", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeVersion, ParentSkill: "demo", ParentHash: hash, CreatedFrom: CreatedFromBase}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "demo", "SKILL.md"), "Draft.\n")
	diff, err := Diff(cfg, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(diff.Lines, "\n"), "+Draft.") {
		t.Fatalf("diff = %#v", diff)
	}
	rebase, err := Rebase(cfg, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if rebase.Rebased || len(rebase.Conflicts) != 0 {
		t.Fatalf("rebase same = %#v", rebase)
	}
	writeFile(t, filepath.Join(root, "store", "demo", "README.md"), "changed\n")
	rebase, err = Rebase(cfg, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(rebase.Conflicts) != 1 {
		t.Fatalf("rebase changed = %#v", rebase)
	}

	destRoot := filepath.Join(root, "repo")
	dryRun, err := Promote(cfg, "demo", PromoteOptions{ToPath: destRoot, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || dryRun.Destination != filepath.Join(destRoot, "demo") || len(dryRun.Files) != 1 {
		t.Fatalf("dry run = %#v", dryRun)
	}
	sourceDryRun, err := Promote(cfg, "demo", PromoteOptions{ToPath: destRoot, ToSource: "vault", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if sourceDryRun.Source != "vault" || !strings.Contains(strings.Join(sourceDryRun.NextSteps, "\n"), "upskill add vault") {
		t.Fatalf("source dry run = %#v", sourceDryRun)
	}
	if _, err := os.Stat(filepath.Join(destRoot, "demo")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote destination: %v", err)
	}
	written, err := Promote(cfg, "demo", PromoteOptions{ToPath: destRoot})
	if err != nil {
		t.Fatal(err)
	}
	if written.DryRun || readFile(t, filepath.Join(destRoot, "demo", "SKILL.md")) != "Draft.\n" {
		t.Fatalf("written = %#v", written)
	}
}

func TestDraftRenderErrorBranches(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts"), StoreDir: filepath.Join(root, "store")}
	if _, err := Render(cfg, "missing", RenderOptions{}); err == nil {
		t.Fatal("expected missing render error")
	}
	if _, err := Diff(cfg, "missing"); err == nil {
		t.Fatal("expected missing diff error")
	}
	if _, err := Rebase(cfg, "missing"); err == nil {
		t.Fatal("expected missing rebase error")
	}
	if _, err := Promote(cfg, "missing", PromoteOptions{ToPath: root}); err == nil {
		t.Fatal("expected missing promote error")
	}
	if _, err := Save(cfg, Metadata{Name: "new", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "new", "SKILL.md"), "New.\n")
	if _, err := Rebase(cfg, "new"); err == nil {
		t.Fatal("expected new draft rebase error")
	}
	if _, err := Promote(cfg, "new", PromoteOptions{}); err == nil {
		t.Fatal("expected promote destination error")
	}
	if dest, err := promotionDestination(filepath.Join(root, "new"), "new"); err != nil || dest != filepath.Join(root, "new") {
		t.Fatalf("destination skill dir = %s %v", dest, err)
	}
	writeFile(t, filepath.Join(root, "existing", "SKILL.md"), "Existing.\n")
	if dest, err := promotionDestination(filepath.Join(root, "existing"), "other"); err != nil || dest != filepath.Join(root, "existing") {
		t.Fatalf("destination existing skill = %s %v", dest, err)
	}
}

func TestDraftRenderAdditionalBranches(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		StoreDir:    filepath.Join(root, "store"),
		DraftsDir:   filepath.Join(root, "drafts"),
		RenderedDir: filepath.Join(root, "rendered"),
		AgentOutputs: []config.AgentOutput{
			{Name: "codex", Path: filepath.Join(root, "codex")},
			{Name: "claude", Path: filepath.Join(root, "claude")},
		},
	}
	if _, err := Save(cfg, Metadata{Name: "empty", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "empty", "SKILL.md.j2"), "{% bad")
	if _, err := Render(cfg, "empty", RenderOptions{}); err == nil {
		t.Fatal("render with bad template unexpectedly succeeded")
	}

	if _, err := Save(cfg, Metadata{Name: "new", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "new", "SKILL.md"), "New.\n")
	diff, err := Diff(cfg, "new")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(diff.Lines, "\n"), "+New.") {
		t.Fatalf("new diff = %#v", diff)
	}

	writeFile(t, filepath.Join(root, "same-base", "SKILL.md"), "Same.\n")
	writeFile(t, filepath.Join(root, "same-draft", "SKILL.md"), "Same.\n")
	lines, err := diffDirs(filepath.Join(root, "same-base"), filepath.Join(root, "same-draft"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("same diff = %#v", lines)
	}
	if files, err := readFiles(""); err != nil || len(files) != 0 {
		t.Fatalf("empty readFiles = %#v %v", files, err)
	}
	if files, err := readFiles(filepath.Join(root, "missing")); err != nil || len(files) != 0 {
		t.Fatalf("missing readFiles = %#v %v", files, err)
	}

	if _, err := Save(cfg, Metadata{Name: "orphan", Type: TypeVersion, ParentSkill: "missing", ParentHash: "sha256:old", CreatedFrom: CreatedFromBase}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "orphan", "SKILL.md"), "Orphan.\n")
	if _, err := Rebase(cfg, "orphan"); err == nil {
		t.Fatal("orphan rebase unexpectedly succeeded")
	}

	renderedPath := filepath.Join(root, "rendered-source")
	writeFile(t, filepath.Join(renderedPath, "SKILL.md"), "Rendered.\n")
	copied, err := copyRenderedToAgents(cfg, "new", "codex", renderedPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 1 || !strings.Contains(copied[0], "codex") {
		t.Fatalf("filtered copy = %#v", copied)
	}
	parentFile := filepath.Join(root, "dest-file")
	writeFile(t, parentFile, "file")
	badCfg := cfg
	badCfg.AgentOutputs = []config.AgentOutput{{Name: "bad", Path: filepath.Join(parentFile, "child")}}
	if _, err := copyRenderedToAgents(badCfg, "new", "", renderedPath); err == nil {
		t.Fatal("copy destination remove failure unexpectedly succeeded")
	}
	badCfg.AgentOutputs = []config.AgentOutput{{Name: "bad", Path: filepath.Join(root, "bad-copy")}}
	if _, err := copyRenderedToAgents(badCfg, "new", "", filepath.Join(root, "missing-rendered")); err == nil {
		t.Fatal("copy destination copy failure unexpectedly succeeded")
	}

	src := filepath.Join(root, "copy-src")
	dst := filepath.Join(root, "copy-dst")
	writeFile(t, filepath.Join(src, "b.md"), "B")
	writeFile(t, filepath.Join(src, "a.md"), "A")
	writeFile(t, filepath.Join(dst, "a.md"), "old")
	files, err := plannedCopy(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "a.md" || !files[0].Overwrite {
		t.Fatalf("planned files = %#v", files)
	}
	if _, err := plannedCopy(filepath.Join(root, "missing-src"), dst); err == nil {
		t.Fatal("planned copy missing src unexpectedly succeeded")
	}

	blocker := filepath.Join(root, "promote-parent-file")
	writeFile(t, blocker, "file")
	if _, err := Promote(cfg, "new", PromoteOptions{ToPath: filepath.Join(blocker, "dest")}); err == nil {
		t.Fatal("promote remove failure unexpectedly succeeded")
	}
}

func TestDraftRenderInjectedErrorBranches(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		StoreDir:     filepath.Join(root, "store"),
		DraftsDir:    filepath.Join(root, "drafts"),
		RenderedDir:  filepath.Join(root, "rendered"),
		AgentOutputs: []config.AgentOutput{{Name: "codex", Path: filepath.Join(root, "codex")}},
	}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "drafts", "demo", "SKILL.md"), "# Demo\n")

	oldMkdirTemp := osMkdirTemp
	oldCopyDir := fsCopyDir
	oldRemoveAll := osRemoveAll
	oldWriteFile := osWriteFile
	oldStat := osStat
	oldReadFile := osReadFile
	oldWalkFiles := fsWalkFiles
	oldAbs := filepathAbs
	t.Cleanup(func() {
		osMkdirTemp = oldMkdirTemp
		fsCopyDir = oldCopyDir
		osRemoveAll = oldRemoveAll
		osWriteFile = oldWriteFile
		osStat = oldStat
		osReadFile = oldReadFile
		fsWalkFiles = oldWalkFiles
		filepathAbs = oldAbs
	})

	osMkdirTemp = func(string, string) (string, error) { return "", errors.New("mkdir temp failed") }
	if _, err := Render(cfg, "demo", RenderOptions{}); err == nil {
		t.Fatal("expected mkdir temp render error")
	}
	osMkdirTemp = oldMkdirTemp

	fsCopyDir = func(string, string, func(string, os.DirEntry) bool) error { return errors.New("copy failed") }
	if _, err := Render(cfg, "demo", RenderOptions{}); err == nil {
		t.Fatal("expected render copy error")
	}
	fsCopyDir = oldCopyDir

	osWriteFile = func(string, []byte, os.FileMode) error { return errors.New("marker failed") }
	if _, err := Render(cfg, "demo", RenderOptions{}); err == nil {
		t.Fatal("expected marker write error")
	}
	osWriteFile = oldWriteFile

	copyCalls := 0
	fsCopyDir = func(src string, dst string, skip func(string, os.DirEntry) bool) error {
		copyCalls++
		if copyCalls == 2 {
			return errors.New("destination copy failed")
		}
		return oldCopyDir(src, dst, skip)
	}
	if _, err := Render(cfg, "demo", RenderOptions{Copy: true}); err == nil {
		t.Fatal("expected render copy-to-agent error")
	}
	fsCopyDir = oldCopyDir

	filepathAbs = func(string) (string, error) { return "", errors.New("abs failed") }
	if _, err := promotionDestination("bad", "demo"); err == nil {
		t.Fatal("expected promotion destination abs error")
	}
	if _, err := Promote(cfg, "demo", PromoteOptions{ToPath: "bad"}); err == nil {
		t.Fatal("expected promote destination error")
	}
	filepathAbs = oldAbs

	fsWalkFiles = func(string) ([]string, error) { return nil, errors.New("walk failed") }
	if _, err := Promote(cfg, "demo", PromoteOptions{ToPath: filepath.Join(root, "repo")}); err == nil {
		t.Fatal("expected promote planned-copy error")
	}
	if _, err := readFiles(filepath.Join(root, "drafts", "demo")); err == nil {
		t.Fatal("expected readFiles walk error")
	}
	fsWalkFiles = oldWalkFiles

	osRemoveAll = func(string) error { return errors.New("remove failed") }
	if _, err := Promote(cfg, "demo", PromoteOptions{ToPath: filepath.Join(root, "repo")}); err == nil {
		t.Fatal("expected promote remove error")
	}
	osRemoveAll = oldRemoveAll

	fsCopyDir = func(string, string, func(string, os.DirEntry) bool) error { return errors.New("promote copy failed") }
	if _, err := Promote(cfg, "demo", PromoteOptions{ToPath: filepath.Join(root, "repo")}); err == nil {
		t.Fatal("expected promote copy error")
	}
	fsCopyDir = oldCopyDir

	osStat = func(path string) (os.FileInfo, error) {
		if path == filepath.Join(root, "stat-error") {
			return nil, errors.New("stat failed")
		}
		return oldStat(path)
	}
	if _, err := readFiles(filepath.Join(root, "stat-error")); err == nil {
		t.Fatal("expected readFiles stat error")
	}
	osStat = func(path string) (os.FileInfo, error) {
		if strings.HasSuffix(path, "SKILL.md") {
			return nil, errors.New("file stat failed")
		}
		return oldStat(path)
	}
	if _, err := plannedCopy(filepath.Join(root, "drafts", "demo"), filepath.Join(root, "repo")); err == nil {
		t.Fatal("expected planned copy stat error")
	}
	osStat = oldStat

	osReadFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }
	if _, err := readFiles(filepath.Join(root, "drafts", "demo")); err == nil {
		t.Fatal("expected readFiles read error")
	}
	osReadFile = oldReadFile

	osStat = func(path string) (os.FileInfo, error) {
		if path == filepath.Join(root, "drafts", "demo") {
			return nil, errors.New("base stat failed")
		}
		return oldStat(path)
	}
	if _, err := Diff(cfg, "demo"); err == nil {
		t.Fatal("expected Diff read error")
	}
	if _, err := diffDirs(filepath.Join(root, "drafts", "demo"), filepath.Join(root, "same-draft")); err == nil {
		t.Fatal("expected diff base read error")
	}
	osStat = func(path string) (os.FileInfo, error) {
		if path == filepath.Join(root, "same-draft") {
			return nil, errors.New("draft stat failed")
		}
		return oldStat(path)
	}
	if _, err := diffDirs("", filepath.Join(root, "same-draft")); err == nil {
		t.Fatal("expected diff draft read error")
	}
	osStat = oldStat
}
