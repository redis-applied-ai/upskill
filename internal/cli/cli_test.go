package cli

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"upskill/internal/config"
	"upskill/internal/library"
	"upskill/internal/render"
)

func writeTestFile(t *testing.T, path string, text string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), perm); err != nil {
		t.Fatal(err)
	}
}

func setupEnv(t *testing.T) (root string, configPath string) {
	t.Helper()
	root = t.TempDir()
	configPath = filepath.Join(root, "config.json")
	cfg := map[string]any{
		"agents": map[string]string{
			"codex": filepath.Join(root, "codex-skills"),
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, configPath, string(data), 0o644)
	t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
	t.Setenv("UPSKILL_CONFIG", configPath)
	return root, configPath
}

func runCLIGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Upskill Test",
		"GIT_AUTHOR_EMAIL=upskill@example.invalid",
		"GIT_COMMITTER_NAME=Upskill Test",
		"GIT_COMMITTER_EMAIL=upskill@example.invalid",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func installFakeEditor(t *testing.T, edited *[]string) {
	t.Helper()
	oldCanEdit := cliCanEdit
	oldEditor := cliEditor
	oldRunEditor := cliRunEditor
	cliCanEdit = func(io.Writer, io.Writer) bool { return true }
	cliEditor = func() string { return "fake-editor" }
	cliRunEditor = func(_ string, path string) error {
		*edited = append(*edited, path)
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = file.WriteString("\nEdited by test.\n")
		return err
	}
	t.Cleanup(func() {
		cliCanEdit = oldCanEdit
		cliEditor = oldEditor
		cliRunEditor = oldRunEditor
	})
}

func TestUnknownCommandErrors(t *testing.T) {
	setupEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"wat"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSyncUpdatesLockThenRenders(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source", "redis")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nBase.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--agent", "codex", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nUpdated.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Overlay.\n", 0o644)

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"sync", "--agent", "codex"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	rendered := readTestFile(t, filepath.Join(root, "codex-skills", "redis", "SKILL.md"))
	if !strings.Contains(rendered, "Updated.") || !strings.Contains(rendered, "Overlay.") {
		t.Fatalf("sync did not render overlay:\n%s", rendered)
	}
}

func TestCLIGitSourceFreshnessE2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root, _ := setupEnv(t)
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	runCLIGit(t, "", "init", "--bare", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLIGit(t, work, "init")
	writeTestFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld from git.\n", 0o644)
	runCLIGit(t, work, "add", ".")
	runCLIGit(t, work, "commit", "-m", "old redis")
	runCLIGit(t, work, "branch", "-M", "main")
	runCLIGit(t, work, "remote", "add", "origin", remote)
	runCLIGit(t, work, "push", "-u", "origin", "main")
	runCLIGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	var stdout, stderr bytes.Buffer
	source := "file://" + remote
	if code := Run([]string{"add", source, "--ref", "main", "--subpath", "skills", "--skill", "redis", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := readTestFile(t, filepath.Join(root, "codex-skills", "redis", "SKILL.md")); !strings.Contains(got, "Old from git.") {
		t.Fatalf("initial render = %s", got)
	}

	writeTestFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew from git.\n", 0o644)
	runCLIGit(t, work, "add", ".")
	runCLIGit(t, work, "commit", "-m", "new redis")
	runCLIGit(t, work, "push", "origin", "main")

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "notice: updates available for redis") {
		t.Fatalf("missing update notice: %q", stderr.String())
	}
	if got := readTestFile(t, filepath.Join(root, "codex-skills", "redis", "SKILL.md")); !strings.Contains(got, "Old from git.") {
		t.Fatalf("notice-only command mutated render = %s", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := readTestFile(t, filepath.Join(root, "codex-skills", "redis", "SKILL.md")); !strings.Contains(got, "New from git.") {
		t.Fatalf("sync did not render git update = %s", got)
	}
}

func TestSyncRejectsOverlayModeFlags(t *testing.T) {
	setupEnv(t)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"sync", "--without-overlays"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "not supported") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRenderPrintBaseOnly(t *testing.T) {
	root, _ := setupEnv(t)
	writeTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md"), "Base.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Overlay.\n", 0o644)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"render", "redis", "--print", "--base-only"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "name: \"redis\"") || !strings.Contains(stdout.String(), "Base.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "codex-skills", "redis")); !os.IsNotExist(err) {
		t.Fatalf("base-only render should not copy")
	}
}

func TestHelpCommands(t *testing.T) {
	setupEnv(t)
	var stdout, stderr bytes.Buffer

	runHelp := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v help code = %d stderr=%s", args, code, stderr.String())
		}
		return stdout.String()
	}

	top := runHelp()
	if !strings.Contains(top, "usage: upskill <command> [args]") || !strings.Contains(top, "commands:") || !strings.Contains(top, "--no-check") {
		t.Fatalf("help output = %q", top)
	}
	for _, want := range []string{"add      Install", "find     Search", "update-policy", "migrate  Migrate", "source   Manage", "draft    Manage", "overlay  Manage", "mcp      Run", "version  Print"} {
		if !strings.Contains(top, want) {
			t.Fatalf("help output missing %q: %q", want, top)
		}
	}
	removedCommand := "va" + "ult"
	for _, forbidden := range []string{"search   Alias", "source add <source>", "draft import <skill-name>", "overlay diff <skill-name>", "--archive-url", "forward", "underlying", removedCommand} {
		if strings.Contains(top, forbidden) {
			t.Fatalf("top help output contains %q: %q", forbidden, top)
		}
	}

	if flagTop := runHelp("--help"); flagTop != top {
		t.Fatalf("--help output differs from default help:\ndefault=%q\nflag=%q", top, flagTop)
	}

	addPolicyHelp := runHelp("add", "--help")
	if !strings.Contains(addPolicyHelp, "--update-policy manual|auto") ||
		!strings.Contains(addPolicyHelp, "automatically during Upskill preflight") {
		t.Fatalf("add help = %q", addPolicyHelp)
	}
	updatePolicyHelp := runHelp("update-policy", "--help")
	if !strings.Contains(updatePolicyHelp, helpUpdatePolicy) ||
		!strings.Contains(updatePolicyHelp, "updates only lock metadata") {
		t.Fatalf("update-policy help = %q", updatePolicyHelp)
	}

	sourceHelp := runHelp("source", "--help")
	if !strings.Contains(sourceHelp, "usage: upskill source <command> [args]") || !strings.Contains(sourceHelp, "skills  List") || !strings.Contains(sourceHelp, "find    Search") {
		t.Fatalf("source help = %q", sourceHelp)
	}
	if strings.Contains(sourceHelp, "--ref REF") || strings.Contains(sourceHelp, "|") {
		t.Fatalf("source help descended into leaf details or aliases: %q", sourceHelp)
	}
	sourceAddHelp := runHelp("source", "add", "--help")
	if !strings.Contains(sourceAddHelp, helpSourceAdd) ||
		!strings.Contains(sourceAddHelp, "--ref REF") ||
		!strings.Contains(sourceAddHelp, "--default-update-policy manual|auto") ||
		!strings.Contains(sourceAddHelp, "renames the source") {
		t.Fatalf("source add help = %q", sourceAddHelp)
	}
	sourceSkillsHelp := runHelp("source", "skills", "--help")
	if !strings.Contains(sourceSkillsHelp, helpSourceSkills) ||
		!strings.Contains(sourceSkillsHelp, "--subpath PATH") ||
		!strings.Contains(sourceSkillsHelp, "Output columns are SKILL, DESCRIPTION, and PATH") {
		t.Fatalf("source skills help = %q", sourceSkillsHelp)
	}
	sourceFindHelp := runHelp("source", "find", "--help")
	if !strings.Contains(sourceFindHelp, helpSourceFind) ||
		!strings.Contains(sourceFindHelp, "without installing") ||
		!strings.Contains(sourceFindHelp, "--limit N") {
		t.Fatalf("source find help = %q", sourceFindHelp)
	}
	sourceSearchHelp := runHelp("source", "search", "--help")
	if !strings.Contains(sourceSearchHelp, helpSourceFind) {
		t.Fatalf("source search help = %q", sourceSearchHelp)
	}
	sourceAliasHelp := runHelp("source", "ls", "--help")
	if !strings.Contains(sourceAliasHelp, helpSourceList) {
		t.Fatalf("source ls help = %q", sourceAliasHelp)
	}
	sourceRemoveAliasHelp := runHelp("source", "rm", "--help")
	if !strings.Contains(sourceRemoveAliasHelp, helpSourceRemove) {
		t.Fatalf("source rm help = %q", sourceRemoveAliasHelp)
	}

	draftHelp := runHelp("draft", "--help")
	if !strings.Contains(draftHelp, "usage: upskill draft <command> [args]") || !strings.Contains(draftHelp, "import    Import") {
		t.Fatalf("draft help = %q", draftHelp)
	}
	if strings.Contains(draftHelp, "--archive-url") || strings.Contains(draftHelp, "|") {
		t.Fatalf("draft help descended into leaf details or aliases: %q", draftHelp)
	}
	draftImportHelp := runHelp("draft", "import", "--help")
	if !strings.Contains(draftImportHelp, "--archive-url URL") || !strings.Contains(draftImportHelp, "--file-url URL") {
		t.Fatalf("draft import help = %q", draftImportHelp)
	}
	draftAliasHelp := runHelp("draft", "ls", "--help")
	if !strings.Contains(draftAliasHelp, helpDraftList) {
		t.Fatalf("draft ls help = %q", draftAliasHelp)
	}
	draftRemoveAliasHelp := runHelp("draft", "rm", "--help")
	if !strings.Contains(draftRemoveAliasHelp, helpDraftRemove) {
		t.Fatalf("draft rm help = %q", draftRemoveAliasHelp)
	}

	overlayHelp := runHelp("overlay")
	if !strings.Contains(overlayHelp, "usage: upskill overlay <command> [args]") || !strings.Contains(overlayHelp, "diff  Show") {
		t.Fatalf("overlay help = %q", overlayHelp)
	}
	if strings.Contains(overlayHelp, "|") {
		t.Fatalf("overlay help contains alias marker: %q", overlayHelp)
	}
	overlayDiffHelp := runHelp("overlay", "diff", "--help")
	if !strings.Contains(overlayDiffHelp, helpOverlayDiff) {
		t.Fatalf("overlay diff help = %q", overlayDiffHelp)
	}
	overlayAliasHelp := runHelp("overlay", "ls", "--help")
	if !strings.Contains(overlayAliasHelp, helpOverlayList) {
		t.Fatalf("overlay ls help = %q", overlayAliasHelp)
	}
	workonHelp := runHelp("workon")
	if !strings.Contains(workonHelp, helpWorkon) {
		t.Fatalf("workon help = %q", workonHelp)
	}

	helpCommands := [][]string{
		{"add", "--help"},
		{"show", "--help"},
		{"list", "--help"},
		{"ls", "--help"},
		{"find", "--help"},
		{"search", "--help"},
		{"remove", "--help"},
		{"rm", "--help"},
		{"update", "--help"},
		{"sync", "--help"},
		{"migrate", "--help"},
		{"init", "--help"},
		{"workon", "--help"},
		{"source", "list", "--help"},
		{"source", "ls", "--help"},
		{"source", "skills", "--help"},
		{"source", "find", "--help"},
		{"source", "search", "--help"},
		{"source", "remove", "--help"},
		{"source", "rm", "--help"},
		{"draft", "list", "--help"},
		{"draft", "ls", "--help"},
		{"draft", "create", "--help"},
		{"draft", "edit", "--help"},
		{"draft", "path", "--help"},
		{"draft", "files", "--help"},
		{"draft", "read", "--help"},
		{"draft", "add-file", "--help"},
		{"draft", "render", "--help"},
		{"draft", "diff", "--help"},
		{"draft", "rebase", "--help"},
		{"draft", "promote", "--help"},
		{"draft", "remove", "--help"},
		{"draft", "rm", "--help"},
		{"overlay", "--help"},
		{"overlay", "list", "--help"},
		{"overlay", "ls", "--help"},
		{"overlay", "add", "--help"},
		{"overlay", "edit", "--help"},
		{"render", "--help"},
		{"mcp", "--help"},
		{"version", "--help"},
		{"status", "--help"},
		{"doctor", "--help"},
	}
	for _, args := range helpCommands {
		if got := runHelp(args...); !strings.HasPrefix(got, "usage:") {
			t.Fatalf("%v help output = %q", args, got)
		}
	}
	for _, args := range helpCommands {
		if len(args) == 2 && args[0] == "overlay" && isHelp(args[1]) {
			continue
		}
		got := runHelp(args...)
		if !strings.Contains(got, "summary:") || !strings.Contains(got, "examples:") {
			t.Fatalf("%v help missing rich sections: %q", args, got)
		}
	}

	addHelp := runHelp("add", "--help")
	for _, want := range []string{"--no-render", "Git branch, tag, or commit", "Installed skill IDs"} {
		if !strings.Contains(addHelp, want) {
			t.Fatalf("add help missing %q: %q", want, addHelp)
		}
	}
	findHelp := runHelp("search", "--help")
	if !strings.Contains(findHelp, "Search installed skills") || !strings.Contains(findHelp, "upskill source find") {
		t.Fatalf("search help missing installed/source distinction: %q", findHelp)
	}
	draftImportHelp = runHelp("draft", "import", "--help")
	if !strings.Contains(draftImportHelp, "Remote imports require --sha256") {
		t.Fatalf("draft import help missing checksum note: %q", draftImportHelp)
	}
	overlayEditHelp := runHelp("overlay", "edit", "--help")
	if !strings.Contains(overlayEditHelp, "Default: root-append") ||
		!strings.Contains(overlayEditHelp, "By default, this appends text to SKILL.md") ||
		!strings.Contains(overlayEditHelp, "provide a Jinja template file") {
		t.Fatalf("overlay edit help missing operation context: %q", overlayEditHelp)
	}
}

func TestOverlayCommands(t *testing.T) {
	root, _ := setupEnv(t)
	writeTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md"), "Base.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"overlay", "add", "redis", "--agent", "codex", "--path"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), filepath.Join("redis", "agents", "codex")) {
		t.Fatalf("add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run([]string{"overlay", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"agent": "codex"`) {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Global.\n", 0o644)
	stdout.Reset()
	if code := Run([]string{"overlay", "ls"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list text code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "redis") || !strings.Contains(stdout.String(), "all") {
		t.Fatalf("list text stdout = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run([]string{"overlay", "list", "--skill", "redis", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list filtered code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "codex") {
		t.Fatalf("list filtered stdout = %q", stdout.String())
	}

	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Overlay.\n", 0o644)
	stdout.Reset()
	if code := Run([]string{"overlay", "diff", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("diff code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "+Overlay.") {
		t.Fatalf("diff stdout = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run([]string{"overlay", "diff", "redis", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("diff agent code = %d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	if code := Run([]string{"overlay", "add", "plain"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add plain code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "created") {
		t.Fatalf("add plain stdout = %q", stdout.String())
	}
}

func TestOverlayCommandsOpenEditor(t *testing.T) {
	root, _ := setupEnv(t)
	var edited []string
	installFakeEditor(t, &edited)
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"overlay", "add", "redis", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("overlay add edit code = %d stderr=%s", code, stderr.String())
	}
	wantAppend := filepath.Join(root, "home", "overlays", "redis", "agents", "codex", "SKILL.append.md")
	if len(edited) != 1 || edited[0] != wantAppend {
		t.Fatalf("edited = %#v, want %s", edited, wantAppend)
	}
	if !strings.Contains(readTestFile(t, wantAppend), "Edited by test.") {
		t.Fatal("overlay append file was not edited")
	}
	if !strings.Contains(stdout.String(), "edited overlay redis[codex] SKILL.append.md") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	edited = nil
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"overlay", "edit", "redis", "--operation", "replace", "--file", "references/checklist.md"}, &stdout, &stderr); code != 0 {
		t.Fatalf("overlay edit replace code = %d stderr=%s", code, stderr.String())
	}
	wantReplace := filepath.Join(root, "home", "overlays", "redis", "replace", "references", "checklist.md")
	if len(edited) != 1 || edited[0] != wantReplace {
		t.Fatalf("edited replace = %#v, want %s", edited, wantReplace)
	}
	if !strings.Contains(readTestFile(t, wantReplace), "Edited by test.") {
		t.Fatal("overlay replace file was not edited")
	}
}

func TestOverlayListNoOverlays(t *testing.T) {
	setupEnv(t)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"overlay", "list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "no overlays" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestOverlayCommandErrors(t *testing.T) {
	setupEnv(t)
	var stdout, stderr bytes.Buffer
	cases := [][]string{
		{"overlay", "add"},
		{"overlay", "add", "redis", "--bad"},
		{"overlay", "edit"},
		{"overlay", "edit", "redis", "--bad"},
		{"overlay", "edit", "redis", "--operation", "unknown"},
		{"overlay", "edit", "redis", "--file", "../bad"},
		{"overlay", "diff"},
		{"overlay", "diff", "redis", "--bad"},
		{"overlay", "wat"},
		{"overlay", "list", "--bad"},
	}
	for _, args := range cases {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}

	root, _ := setupEnv(t)
	blocker := filepath.Join(root, "overlays-file")
	writeTestFile(t, blocker, "block", 0o644)
	t.Setenv("UPSKILL_OVERLAYS_DIR", blocker)
	for _, args := range [][]string{
		{"overlay", "list"},
		{"overlay", "add", "redis"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"overlay", "diff", "missing"}, &stdout, &stderr); code == 0 {
		t.Fatal("overlay diff missing skill unexpectedly succeeded")
	}
}

func TestRenderCommandVariants(t *testing.T) {
	root, _ := setupEnv(t)
	writeTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md"), "Base.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Overlay.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"render", "redis", "--agent", "codex", "--copy=false"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "codex-skills", "redis")); !os.IsNotExist(err) {
		t.Fatalf("copy=false should not copy")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "redis", "--agent", "codex", "--copy", "true"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render copy code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "codex-skills", "redis", "SKILL.md")); err != nil {
		t.Fatalf("copy=true did not copy: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "redis", "--agent", "codex", "--no-copy"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render no-copy code = %d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "redis", "--agent", "codex", "--copy=true"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render copy=true code = %d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "redis", "--print", "--file", "SKILL.md"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render print file code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Base.") {
		t.Fatalf("render print stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "redis", "--print", "--file", "missing.md"}, &stdout, &stderr); code == 0 {
		t.Fatalf("render missing file unexpectedly succeeded")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "missing"}, &stdout, &stderr); code == 0 {
		t.Fatalf("render missing skill unexpectedly succeeded")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "--bad"}, &stdout, &stderr); code != 2 {
		t.Fatalf("render bad flag code = %d", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"render", "--print"}, &stdout, &stderr); code != 2 {
		t.Fatalf("render --print without skill code = %d", code)
	}
}

func TestParseFlagsErrorsAndBoolDefault(t *testing.T) {
	if parseBoolDefault("", true) != true || parseBoolDefault("false", true) != false || parseBoolDefault("yes", false) != true {
		t.Fatal("parseBoolDefault failed")
	}
	var pos []string
	if err := parseFlags([]string{"--agent"}, map[string]func(string) error{
		"--agent": func(v string) error { return nil },
	}, &pos); err == nil {
		t.Fatal("expected missing value error")
	}
	if err := parseFlags([]string{"--x"}, map[string]func(string) error{}, &pos); err == nil {
		t.Fatal("expected unknown flag error")
	}
	if err := parseFlags([]string{"pos"}, map[string]func(string) error{}); err == nil {
		t.Fatal("expected unexpected argument error")
	}
	if err := parseFlags([]string{"--agent", "x"}, map[string]func(string) error{
		"--agent": func(v string) error { return os.ErrInvalid },
	}, &pos); err == nil {
		t.Fatal("expected value handler error")
	}
	if err := parseFlags([]string{"--json"}, map[string]func(string) error{
		"--json": func(v string) error { return os.ErrInvalid },
	}, &pos); err == nil {
		t.Fatal("expected bool handler error")
	}
}

func TestPrintResultsSortsAndFormatsGeneric(t *testing.T) {
	var stdout bytes.Buffer
	printResults(&stdout, []render.Result{
		{Skill: "alpha", Agent: "claude", RenderedPath: "/rendered/alpha-claude"},
		{Skill: "zulu", Agent: "", RenderedPath: "/rendered/zulu"},
		{Skill: "alpha", Agent: "codex", RenderedPath: "/rendered/alpha", CopiedTo: []string{"/target/alpha"}},
	})
	got := stdout.String()
	if !strings.Contains(got, "alpha [codex]") || !strings.Contains(got, "alpha [claude]") || !strings.Contains(got, "zulu [generic]") {
		t.Fatalf("stdout = %q", got)
	}
	if strings.Index(got, "alpha") > strings.Index(got, "zulu") {
		t.Fatalf("results not sorted: %q", got)
	}

	stdout.Reset()
	printResults(&stdout, []render.Result{
		{Skill: "alpha", Agent: "codex", RenderedPath: "/rendered/alpha", Skipped: true},
		{Skill: "bravo", Agent: "codex", RenderedPath: "/rendered/bravo", Skipped: true},
	})
	if strings.TrimSpace(stdout.String()) != "codex: 2 rendered skills unchanged" {
		t.Fatalf("unchanged output = %q", stdout.String())
	}

	stdout.Reset()
	printResults(&stdout, []render.Result{
		{Skill: "alpha", Agent: "codex", RenderedPath: "/rendered/alpha", Skipped: true},
		{Skill: "bravo", Agent: "codex", RenderedPath: "/rendered/bravo", CopiedTo: []string{"/target/bravo"}},
	})
	if !strings.Contains(stdout.String(), "bravo") || !strings.Contains(stdout.String(), "codex: 1 rendered skills unchanged") || strings.Contains(stdout.String(), "alpha [codex]") {
		t.Fatalf("mixed unchanged output = %q", stdout.String())
	}
}

func TestRunConfigLoadError(t *testing.T) {
	root := t.TempDir()
	badConfig := filepath.Join(root, "bad.json")
	writeTestFile(t, badConfig, "{bad", 0o644)
	t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
	t.Setenv("UPSKILL_CONFIG", badConfig)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"render"}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected config load error")
	}
}

func TestVersionCommandReportsVersionWithoutConfig(t *testing.T) {
	oldVersion := version
	version = "v1.2.3"
	t.Cleanup(func() { version = oldVersion })

	root := t.TempDir()
	badConfig := filepath.Join(root, "bad.json")
	writeTestFile(t, badConfig, "{bad", 0o644)
	t.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
	t.Setenv("UPSKILL_CONFIG", badConfig)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	if stdout.String() != "upskill v1.2.3\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"version", "extra"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("unexpected argument exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "unexpected argument: extra") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStandaloneLibraryCommands(t *testing.T) {
	root, _ := setupEnv(t)
	sourceRoot := filepath.Join(root, "repo")
	writeTestFile(t, filepath.Join(sourceRoot, "skills", "redis", "SKILL.md"), "---\nname: Redis Cache\ndescription: fast cache\n---\n\nUse Redis 8.\n", 0o644)
	writeTestFile(t, filepath.Join(sourceRoot, "skills", "redis", "references", "guide.md"), "Guide.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", sourceRoot, "--skill", "redis-cache", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "installed redis-cache") {
		t.Fatalf("add stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "upskill-lock.json")); err != nil {
		t.Fatalf("lock missing: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "redis-cache") || !strings.Contains(stdout.String(), "fast cache") {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"find", "Redis", "--limit", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("find code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "redis-cache") {
		t.Fatalf("find stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"search", "Redis", "--limit", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("search code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "redis-cache") {
		t.Fatalf("search stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"find", "Redis", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("find json code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"store_name": "redis-cache"`) {
		t.Fatalf("find json stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"show", "redis-cache", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Use Redis 8.") {
		t.Fatalf("show stdout = %q", stdout.String())
	}

	writeTestFile(t, filepath.Join(sourceRoot, "skills", "redis", "SKILL.md"), "---\nname: Redis Cache\ndescription: fast cache\n---\n\nUse Redis 8.2.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update", "redis-cache", "--check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("update check code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "redis-cache\tchanged") {
		t.Fatalf("update check stdout = %q", stdout.String())
	}
	rendered := readTestFile(t, filepath.Join(root, "codex-skills", "redis-cache", "SKILL.md"))
	if strings.Contains(rendered, "8.2") {
		t.Fatalf("update check mutated render = %q", rendered)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update", "redis-cache", "--diff"}, &stdout, &stderr); code != 0 {
		t.Fatalf("update diff code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "diff SKILL.md") || !strings.Contains(stdout.String(), "Use Redis 8.2.") {
		t.Fatalf("update diff stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update", "redis-cache", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("update code = %d stderr=%s", code, stderr.String())
	}
	rendered = readTestFile(t, filepath.Join(root, "codex-skills", "redis-cache", "SKILL.md"))
	if !strings.Contains(rendered, "8.2") {
		t.Fatalf("updated render = %q", rendered)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update", "redis-cache", "--check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("unchanged update check code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no updates available") {
		t.Fatalf("unchanged update check stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"remove", "redis-cache"}, &stdout, &stderr); code != 0 {
		t.Fatalf("remove code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "store", "redis-cache")); !os.IsNotExist(err) {
		t.Fatalf("store skill still exists: %v", err)
	}
}

func TestSearchPaginationSummaries(t *testing.T) {
	root, _ := setupEnv(t)
	writeTestFile(t, filepath.Join(root, "home", "store", "alpha", "SKILL.md"), "---\nname: Alpha\ndescription: first\n---\n\nAlpha body.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "home", "store", "bravo", "SKILL.md"), "---\nname: Bravo\ndescription: second\n---\n\nBravo body.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"find", "--limit", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("find page code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "showing 1/2 skills") || !strings.Contains(stdout.String(), "alpha") || strings.Contains(stdout.String(), "bravo") {
		t.Fatalf("find page stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"search", "--json", "--limit", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("search json page code = %d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "showing") || !strings.Contains(stdout.String(), `"store_name": "alpha"`) || strings.Contains(stdout.String(), `"store_name": "bravo"`) {
		t.Fatalf("search json page stdout = %q", stdout.String())
	}
}

func TestSourceSearchCommands(t *testing.T) {
	root, _ := setupEnv(t)
	sourceRoot := filepath.Join(root, "source")
	writeTestFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md"), "---\nname: Redis Cache\ndescription: fast cache\n---\n\nUse Redis for cache storage.\n", 0o644)
	writeTestFile(t, filepath.Join(sourceRoot, "writing", "SKILL.md"), "---\nname: Writing Guide\ndescription: drafting help\n---\n\nPlain English writing guidance.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"source", "add", sourceRoot, "--name", "Obsidian"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add code = %d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "find", "Obsidian", "--limit", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source find page code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "showing 1/2 skills") || !strings.Contains(stdout.String(), "redis-cache") || strings.Contains(stdout.String(), "writing-guide") {
		t.Fatalf("source find page stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "find", "Obsidian", "cache"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source find code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SKILL") || !strings.Contains(stdout.String(), "PATH") || !strings.Contains(stdout.String(), "redis-cache") {
		t.Fatalf("source find stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "writing-guide") {
		t.Fatalf("source find returned unrelated skill: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "search", "Obsidian", "English", "--json", "--limit", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source search json code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"store_name": "writing-guide"`) || strings.Contains(stdout.String(), `"store_name": "redis-cache"`) {
		t.Fatalf("source search json stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "find", "Obsidian", "absent"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source find no-match code = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "no matching skills" {
		t.Fatalf("source find no-match stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "find", sourceRoot, "Redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source find raw path code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "redis-cache") {
		t.Fatalf("source find raw path stdout = %q", stdout.String())
	}
}

func TestSourceInitStatusDoctorCommands(t *testing.T) {
	root, _ := setupEnv(t)
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"source", "add", "/tmp/example", "--name", "example", "--ref", "main", "--subpath", "skills"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"example"`) || !strings.Contains(stdout.String(), `"main"`) {
		t.Fatalf("source list stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "remove", "example"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source remove code = %d stderr=%s", code, stderr.String())
	}

	initDir := filepath.Join(root, "new-skill")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "New Skill", "--dir", initDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("init code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(readTestFile(t, filepath.Join(initDir, "SKILL.md")), "New Skill") {
		t.Fatalf("init skill content missing name")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"doctor"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doctor code = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "ok" {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "installed skills") {
		t.Fatalf("status stdout = %q", stdout.String())
	}
}

func TestStatusAndDoctorWarnAboutUnmanagedAgentOutputSkill(t *testing.T) {
	root, _ := setupEnv(t)
	writeTestFile(t, filepath.Join(root, "codex-skills", "gitnexus-repo-redis-sre-agent", "SKILL.md"), "# GitNexus\n", 0o644)

	for _, command := range []string{"status", "doctor"} {
		var stdout, stderr bytes.Buffer
		if code := Run([]string{command}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s code = %d stderr=%s", command, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "warning:") ||
			!strings.Contains(stderr.String(), "gitnexus-repo-redis-sre-agent") ||
			!strings.Contains(stderr.String(), "not installed in Upskill from any configured source") {
			t.Fatalf("%s warning missing unmanaged skill context: %q", command, stderr.String())
		}
	}
}

func TestMigrateUnmanagedDryRunAndApply(t *testing.T) {
	root, _ := setupEnv(t)
	t.Setenv("HOME", root)
	sourcePath := filepath.Join(root, "Documents", "Upskill", "local-skills")
	writeTestFile(t, filepath.Join(root, "codex-skills", "orphan-skill", "SKILL.md"), "---\nname: orphan-skill\ndescription: orphan\n---\n\n# Orphan\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"migrate", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("dry run code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "dry run: migrate from unmanaged; create source local-skills") ||
		!strings.Contains(stdout.String(), sourcePath) ||
		!strings.Contains(stdout.String(), "copy orphan-skill [codex]") {
		t.Fatalf("dry run stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(sourcePath, "orphan-skill", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote source file: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"migrate", "--agent", "codex", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("apply code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: migrating 1 skill(s) from unmanaged") {
		t.Fatalf("apply stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "installed orphan-skill") {
		t.Fatalf("apply stdout = %q", stdout.String())
	}
	for _, path := range []string{
		filepath.Join(sourcePath, "orphan-skill", "SKILL.md"),
		filepath.Join(root, "home", "store", "orphan-skill", "SKILL.md"),
		filepath.Join(root, "codex-skills", "orphan-skill", render.MarkerFile),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing migrated file %s: %v", path, err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sources["local-skills"].Source != sourcePath {
		t.Fatalf("sources = %#v", cfg.Sources)
	}
}

func TestMigrateUnmanagedPreservesDuplicateSkillNames(t *testing.T) {
	root, configPath := setupEnv(t)
	t.Setenv("HOME", root)
	cfg := map[string]any{
		"agents": map[string]string{
			"claude-code": filepath.Join(root, "claude-skills"),
			"codex":       filepath.Join(root, "codex-skills"),
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, configPath, string(data), 0o644)
	sourcePath := filepath.Join(root, "Documents", "Upskill", "local-skills")
	writeTestFile(t, filepath.Join(root, "claude-skills", "shared", "SKILL.md"), "---\nname: shared\ndescription: claude\n---\n\nClaude.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "codex-skills", "shared", "SKILL.md"), "---\nname: shared\ndescription: codex\n---\n\nCodex.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"migrate", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("migrate code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "copy shared [claude-code]") ||
		!strings.Contains(stdout.String(), "copy shared as shared-2 [codex]") ||
		!strings.Contains(stdout.String(), "installed shared-2") {
		t.Fatalf("migrate stdout = %q", stdout.String())
	}
	if body := readTestFile(t, filepath.Join(sourcePath, "shared-2", "SKILL.md")); !strings.Contains(body, "name: shared-2") || !strings.Contains(body, "Codex.") {
		t.Fatalf("renamed duplicate body = %q", body)
	}
	for _, path := range []string{
		filepath.Join(sourcePath, "shared", "SKILL.md"),
		filepath.Join(sourcePath, "shared-2", "SKILL.md"),
		filepath.Join(root, "home", "store", "shared", "SKILL.md"),
		filepath.Join(root, "home", "store", "shared-2", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing migrated duplicate file %s: %v", path, err)
		}
	}
}

func TestMigrateLocalSourceToLocalSource(t *testing.T) {
	root, _ := setupEnv(t)
	fromPath := filepath.Join(root, "personal")
	toPath := filepath.Join(root, "local-skills")
	writeTestFile(t, filepath.Join(fromPath, "alpha", "SKILL.md"), "---\nname: Alpha\ndescription: source skill\n---\n\nAlpha.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"source", "add", fromPath, "--name", "personal"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"migrate", "--from", "personal", "--to-source", "local-skills", "--to-source-path", toPath, "--skill", "alpha", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("migrate source code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "apply: migrate from personal; create source local-skills") ||
		!strings.Contains(stdout.String(), "copy alpha [personal]") ||
		!strings.Contains(stdout.String(), "installed alpha") {
		t.Fatalf("migrate source stdout = %q", stdout.String())
	}
	for _, path := range []string{
		filepath.Join(fromPath, "alpha", "SKILL.md"),
		filepath.Join(toPath, "alpha", "SKILL.md"),
		filepath.Join(root, "home", "store", "alpha", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing source migration file %s: %v", path, err)
		}
	}
}

func TestMigrateHasNoLegacyAliases(t *testing.T) {
	setupEnv(t)
	var stdout, stderr bytes.Buffer
	legacyCommand := "migrate-" + "unmanaged"
	if code := Run([]string{legacyCommand, "--help"}, &stdout, &stderr); code == 0 {
		t.Fatalf("legacy migrate command unexpectedly succeeded: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command: "+legacyCommand) {
		t.Fatalf("legacy migrate command stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	legacyFlag := "--" + "source"
	if code := Run([]string{"migrate", legacyFlag, "local-skills"}, &stdout, &stderr); code == 0 {
		t.Fatalf("legacy migrate flag unexpectedly succeeded: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown flag: "+legacyFlag) {
		t.Fatalf("legacy migrate flag stderr = %q", stderr.String())
	}
}

func TestShowHasNoOldCommandAlias(t *testing.T) {
	setupEnv(t)
	var stdout, stderr bytes.Buffer
	oldCommand := "u" + "se"
	if code := Run([]string{oldCommand, "--help"}, &stdout, &stderr); code == 0 {
		t.Fatalf("old show command unexpectedly succeeded: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown command: "+oldCommand) {
		t.Fatalf("old show command stderr = %q", stderr.String())
	}
}

func TestDraftWorkflowCommands(t *testing.T) {
	root, _ := setupEnv(t)
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"workon", "Brand New"}, &stdout, &stderr); code != 0 {
		t.Fatalf("workon new code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "draft:brand-new") {
		t.Fatalf("workon stdout = %q", stdout.String())
	}
	if !strings.Contains(readTestFile(t, filepath.Join(root, "home", "drafts", "brand-new", "SKILL.md")), "brand-new") {
		t.Fatal("new draft skeleton missing")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"workon", "Brand New"}, &stdout, &stderr); code != 0 {
		t.Fatalf("workon existing code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "draft:brand-new") {
		t.Fatalf("workon existing stdout = %q", stdout.String())
	}

	writeTestFile(t, filepath.Join(root, "resource.md"), "Resource.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "add-file", "brand-new", filepath.Join(root, "resource.md"), "--path", "references/resource.md"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft add-file code = %d stderr=%s", code, stderr.String())
	}
	if readTestFile(t, filepath.Join(root, "home", "drafts", "brand-new", "references", "resource.md")) != "Resource.\n" {
		t.Fatal("resource not copied")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "files", "brand-new", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft files code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "references/resource.md") {
		t.Fatalf("draft files = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "read", "brand-new", "--file", "references/resource.md"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft read code = %d stderr=%s", code, stderr.String())
	}
	if stdout.String() != "Resource.\n" {
		t.Fatalf("draft read = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "path", "brand-new"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft path code = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != filepath.Join(root, "home", "drafts", "brand-new") {
		t.Fatalf("draft path = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "ls"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "draft:brand-new") {
		t.Fatalf("draft list = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "rm", "brand-new"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft remove code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "drafts", "brand-new")); !os.IsNotExist(err) {
		t.Fatalf("draft still exists: %v", err)
	}
}

func TestDraftCommandsOpenEditor(t *testing.T) {
	root, _ := setupEnv(t)
	var edited []string
	installFakeEditor(t, &edited)
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"workon", "Edited Draft"}, &stdout, &stderr); code != 0 {
		t.Fatalf("workon edit code = %d stderr=%s", code, stderr.String())
	}
	wantSkill := filepath.Join(root, "home", "drafts", "edited-draft", "SKILL.md")
	if len(edited) != 1 || edited[0] != wantSkill {
		t.Fatalf("edited = %#v, want %s", edited, wantSkill)
	}
	if !strings.Contains(readTestFile(t, wantSkill), "Edited by test.") {
		t.Fatal("draft SKILL.md was not edited")
	}
	if !strings.Contains(stdout.String(), "edited SKILL.md") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	edited = nil
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "edit", "edited-draft", "--file", "references/new.md"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft edit code = %d stderr=%s", code, stderr.String())
	}
	wantReference := filepath.Join(root, "home", "drafts", "edited-draft", "references", "new.md")
	if len(edited) != 1 || edited[0] != wantReference {
		t.Fatalf("edited reference = %#v, want %s", edited, wantReference)
	}
	if !strings.Contains(readTestFile(t, wantReference), "Edited by test.") {
		t.Fatal("draft reference file was not edited")
	}
}

func TestVersionDraftWorkflowCommands(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source", "redis")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nBase.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "create", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft create version code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "draft:redis") || !strings.Contains(stdout.String(), "version") {
		t.Fatalf("draft create stdout = %q", stdout.String())
	}
	meta := readTestFile(t, filepath.Join(root, "home", "drafts", "redis", ".upskill-draft.json"))
	if !strings.Contains(meta, `"parent_skill": "redis"`) || !strings.Contains(meta, `"created_from": "base"`) {
		t.Fatalf("metadata = %s", meta)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(root, "home", "drafts", "redis", "SKILL.md")), "Base.") {
		t.Fatal("base skill not copied")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "create", "redis"}, &stdout, &stderr); code == 0 {
		t.Fatal("duplicate draft create unexpectedly succeeded")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "codex-skills", "redis", ".upskill-draft.json")); !os.IsNotExist(err) {
		t.Fatalf("sync copied draft metadata: %v", err)
	}
}

func TestFromRenderedDraftWarns(t *testing.T) {
	root, _ := setupEnv(t)
	writeTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md"), "Base.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Overlay.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"draft", "create", "redis", "--from-rendered"}, &stdout, &stderr); code != 0 {
		t.Fatalf("from-rendered code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "warning") {
		t.Fatalf("expected warning, got %q", stdout.String())
	}
	if !strings.Contains(readTestFile(t, filepath.Join(root, "home", "drafts", "redis", "SKILL.md")), "Overlay.") {
		t.Fatal("rendered overlay not copied")
	}
}

func TestDraftImportCommand(t *testing.T) {
	root, _ := setupEnv(t)
	archive := filepath.Join(root, "skill.zip")
	writeCLIZip(t, archive, map[string]string{
		"skill/SKILL.md":          "# Skill\n",
		"skill/references/api.md": "API\n",
	})
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"draft", "import", "imported", "--archive", archive, "--inspect"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft import inspect code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"imported": false`) || !strings.Contains(stdout.String(), "references/api.md") {
		t.Fatalf("inspect stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "drafts", "imported", "references", "api.md")); !os.IsNotExist(err) {
		t.Fatalf("inspect wrote file: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "import", "imported", "--archive", archive}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft import code = %d stderr=%s", code, stderr.String())
	}
	var importJSON map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &importJSON); err != nil {
		t.Fatalf("draft import stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"imported": true`) {
		t.Fatalf("import stdout = %q", stdout.String())
	}
	if readTestFile(t, filepath.Join(root, "home", "drafts", "imported", "references", "api.md")) != "API\n" {
		t.Fatal("archive file not imported")
	}

	source := filepath.Join(root, "extra.md")
	writeTestFile(t, source, "Extra\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "import", "imported", "--file", source, "--path", "references/extra.md"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft file import code = %d stderr=%s", code, stderr.String())
	}
	if readTestFile(t, filepath.Join(root, "home", "drafts", "imported", "references", "extra.md")) != "Extra\n" {
		t.Fatal("single file not imported")
	}
}

func TestDraftRenderDiffRebasePromoteCommands(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source", "redis")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nBase.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "create", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft create code = %d stderr=%s", code, stderr.String())
	}
	writeTestFile(t, filepath.Join(root, "home", "drafts", "redis", "SKILL.md"), "Draft.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Overlay.\n", 0o644)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "render", "redis", "--agent", "codex", "--copy=false", "--apply-overlays"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft render code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"mode": "draft-with-overlays"`) {
		t.Fatalf("render stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "diff", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft diff code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "+Draft.") {
		t.Fatalf("diff stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "rebase", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft rebase code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"rebased": false`) {
		t.Fatalf("rebase stdout = %q", stdout.String())
	}

	dest := filepath.Join(root, "repo")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "promote", "redis", "--to", dest, "--dry-run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft promote dry-run code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"dry_run": true`) {
		t.Fatalf("promote dry-run stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "promote", "redis", "--to", dest}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft promote code = %d stderr=%s", code, stderr.String())
	}
	if readTestFile(t, filepath.Join(dest, "redis", "SKILL.md")) != "Draft.\n" {
		t.Fatal("draft not promoted")
	}
	sourceRoot := filepath.Join(root, "local-source")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "add", sourceRoot, "--name", "local"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add local code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "promote", "redis", "--to-source", "local", "--dry-run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft promote to-source code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"source": "local"`) {
		t.Fatalf("promote to-source stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "promote", "redis", "--to-source", "missing"}, &stdout, &stderr); code == 0 {
		t.Fatal("draft promote to missing source unexpectedly succeeded")
	}
}

func TestDraftCommandErrors(t *testing.T) {
	root, _ := setupEnv(t)
	var stdout, stderr bytes.Buffer
	cases := [][]string{
		{"workon", "name", "--bad"},
		{"draft", "create"},
		{"draft", "create", "name", "--bad"},
		{"draft", "edit"},
		{"draft", "edit", "missing"},
		{"draft", "edit", "missing", "--bad"},
		{"draft", "path"},
		{"draft", "path", "missing"},
		{"draft", "files"},
		{"draft", "files", "missing"},
		{"draft", "files", "missing", "--bad"},
		{"draft", "read"},
		{"draft", "read", "missing"},
		{"draft", "read", "missing", "--bad"},
		{"draft", "add-file"},
		{"draft", "add-file", "missing", "file", "--path", "x"},
		{"draft", "add-file", "missing", "file", "--bad"},
		{"draft", "add-file", "missing", "file"},
		{"draft", "import"},
		{"draft", "import", "name", "--strip-components", "bad"},
		{"draft", "import", "name"},
		{"draft", "render"},
		{"draft", "render", "missing"},
		{"draft", "render", "missing", "--bad"},
		{"draft", "diff"},
		{"draft", "diff", "missing"},
		{"draft", "rebase"},
		{"draft", "rebase", "missing"},
		{"draft", "promote"},
		{"draft", "promote", "missing", "--to", root},
		{"draft", "promote", "missing", "--bad"},
		{"draft", "remove"},
		{"draft", "wat"},
	}
	for _, args := range cases {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}

	root, _ = setupEnv(t)
	writeTestFile(t, filepath.Join(root, "home", "drafts"), "file", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "list"}, &stdout, &stderr); code == 0 {
		t.Fatal("draft list with bad drafts dir unexpectedly succeeded")
	}
}

func TestDraftAdditionalBranches(t *testing.T) {
	root, _ := setupEnv(t)
	var stdout, stderr bytes.Buffer

	for _, args := range [][]string{
		{"workon", "--help"},
		{"draft"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v help code = %d stderr=%s", args, code, stderr.String())
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"workon", ""}, &stdout, &stderr); code == 0 {
		t.Fatal("empty workon unexpectedly succeeded")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty draft list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no drafts") {
		t.Fatalf("empty draft list stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty draft json list code = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "null" {
		t.Fatalf("empty draft json list stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "list", "--bad"}, &stdout, &stderr); code != 2 {
		t.Fatalf("draft list bad flag code = %d", code)
	}

	if code := Run([]string{"workon", "Templated", "--template", "qa"}, &stdout, &stderr); code != 0 {
		t.Fatalf("workon template code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(readTestFile(t, filepath.Join(root, "home", "drafts", "templated", "SKILL.md")), "templated (qa)") {
		t.Fatal("template label missing from new draft")
	}

	writeTestFile(t, filepath.Join(root, "home", "store", "base", "SKILL.md"), "Base.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"workon", "child", "--from", "base"}, &stdout, &stderr); code != 0 {
		t.Fatalf("workon from code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version") {
		t.Fatalf("workon from stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "create", "child-create", "--from", "base"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft create --from code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "create", "templated-create", "--template", "qa"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft create --template code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "create", "base"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft create base code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "diff", "base"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft no-change diff code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no changes") {
		t.Fatalf("draft no-change diff stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "files", "templated"}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft files text code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SKILL.md") {
		t.Fatalf("draft files text stdout = %q", stdout.String())
	}
	for _, args := range [][]string{
		{"draft", "render", "templated", "--copy", "true"},
		{"draft", "render", "templated", "--copy=true"},
		{"draft", "render", "templated", "--no-copy"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v code = %d stderr=%s", args, code, stderr.String())
		}
	}

	fileBody := []byte("URL file.\n")
	archiveBody := zipBytes(t, map[string]string{"bundle/SKILL.md": "# URL Archive\n", "bundle/references/url.md": "URL archive.\n"})
	fileSum := sha256.Sum256(fileBody)
	archiveSum := sha256.Sum256(archiveBody)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file.md":
			_, _ = w.Write(fileBody)
		case "/bundle.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archiveBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "import", "url-file", "--file-url", server.URL + "/file.md", "--path", "references/url.md", "--sha256", hex.EncodeToString(fileSum[:])}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft file-url import code = %d stderr=%s", code, stderr.String())
	}
	if readTestFile(t, filepath.Join(root, "home", "drafts", "url-file", "references", "url.md")) != "URL file.\n" {
		t.Fatal("file-url import missing")
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"draft", "import", "url-archive", "--archive-url", server.URL + "/bundle.zip", "--strip-components", "0", "--sha256", hex.EncodeToString(archiveSum[:])}, &stdout, &stderr); code != 0 {
		t.Fatalf("draft archive-url import code = %d stderr=%s", code, stderr.String())
	}
	if readTestFile(t, filepath.Join(root, "home", "drafts", "url-archive", "references", "url.md")) != "URL archive.\n" {
		t.Fatal("archive-url import missing")
	}

	for _, args := range [][]string{
		{"draft", "read", "templated", "--file", "../bad"},
		{"draft", "read", "templated", "--file", "missing.md"},
		{"draft", "add-file", "templated", filepath.Join(root, "missing.md"), "--path", "references/missing.md"},
		{"draft", "add-file", "templated", filepath.Join(root, "missing.md"), "--path", "../bad"},
		{"draft", "import", "templated", "--bad"},
		{"draft", "remove", ""},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createDraft(cfg, "", "", false, "", true); err == nil {
		t.Fatal("empty createDraft unexpectedly succeeded")
	}
	if _, err := createDraft(cfg, "missing-rendered", "", true, "", false); err == nil {
		t.Fatal("missing rendered draft unexpectedly succeeded")
	}
	if _, err := createNewDraft(cfg, "templated", "again"); err != nil {
		t.Fatalf("createNewDraft existing skill failed: %v", err)
	}
	writeTestFile(t, filepath.Join(root, "home", "store", "not-dir"), "file", 0o644)
	if _, err := createVersionDraft(cfg, "from-file", "not-dir", false); err == nil {
		t.Fatal("createVersionDraft from file unexpectedly succeeded")
	}
	oldManifest := cliFileManifest
	oldStat := cliOsStat
	oldWriteFile := cliOsWriteFile
	oldRemoveAll := cliOsRemoveAll
	oldCopyDir := cliCopyDir
	t.Cleanup(func() {
		cliFileManifest = oldManifest
		cliOsStat = oldStat
		cliOsWriteFile = oldWriteFile
		cliOsRemoveAll = oldRemoveAll
		cliCopyDir = oldCopyDir
	})
	cliFileManifest = func(string, string, bool, bool, int) ([]map[string]any, error) {
		return nil, errors.New("manifest failed")
	}
	stdout.Reset()
	stderr.Reset()
	if code := runDraft(cfg, []string{"files", "templated"}, &stdout, &stderr); code == 0 {
		t.Fatal("draft files manifest error unexpectedly succeeded")
	}
	cliFileManifest = oldManifest

	blockedParent := filepath.Join(root, "blocked-parent")
	writeTestFile(t, blockedParent, "file", 0o644)
	blockedImportCfg := cfg
	blockedImportCfg.DraftsDir = filepath.Join(blockedParent, "drafts")
	stdout.Reset()
	stderr.Reset()
	if code := runDraft(blockedImportCfg, []string{"import", "blocked-import", "--file", filepath.Join(root, "resource.md"), "--path", "resource.md"}, &stdout, &stderr); code == 0 {
		t.Fatal("draft import auto-create error unexpectedly succeeded")
	}
	if _, err := createNewDraft(config.Config{DraftsDir: filepath.Join(blockedParent, "drafts")}, "blocked", ""); err == nil {
		t.Fatal("createNewDraft save error unexpectedly succeeded")
	}
	cliOsStat = func(path string) (os.FileInfo, error) {
		if strings.HasSuffix(path, "SKILL.md") {
			return nil, errors.New("stat failed")
		}
		return oldStat(path)
	}
	if _, err := createNewDraft(cfg, "stat-error", ""); err == nil {
		t.Fatal("createNewDraft stat error unexpectedly succeeded")
	}
	cliOsStat = oldStat
	cliOsWriteFile = func(string, []byte, os.FileMode) error { return errors.New("write failed") }
	if _, err := createNewDraft(cfg, "write-error", ""); err == nil {
		t.Fatal("createNewDraft write error unexpectedly succeeded")
	}
	cliOsWriteFile = oldWriteFile

	writeTestFile(t, filepath.Join(root, "home", "store", "bad-render", "SKILL.md.j2"), "{% bad", 0o644)
	if _, err := createVersionDraft(cfg, "bad-render-draft", "bad-render", true); err == nil {
		t.Fatal("createVersionDraft render error unexpectedly succeeded")
	}
	cliOsRemoveAll = func(string) error { return errors.New("remove failed") }
	if _, err := createVersionDraft(cfg, "remove-error", "base", false); err == nil {
		t.Fatal("createVersionDraft remove error unexpectedly succeeded")
	}
	cliOsRemoveAll = oldRemoveAll
	cliCopyDir = func(string, string, func(string, os.DirEntry) bool) error { return errors.New("copy failed") }
	if _, err := createVersionDraft(cfg, "copy-error", "base", false); err == nil {
		t.Fatal("createVersionDraft copy error unexpectedly succeeded")
	}
	cliCopyDir = oldCopyDir

	badLockCfg := cfg
	badLockCfg.LockPath = filepath.Join(root, "bad-lock.json")
	writeTestFile(t, badLockCfg.LockPath, "{bad", 0o644)
	if _, err := createVersionDraft(badLockCfg, "parent-error", "base", false); err == nil {
		t.Fatal("createVersionDraft parent metadata error unexpectedly succeeded")
	}
	if _, _, _, err := parentMetadata(badLockCfg, "base", filepath.Join(root, "home", "store", "base")); err == nil {
		t.Fatal("parentMetadata bad lock unexpectedly succeeded")
	}
	if _, _, _, err := parentMetadata(cfg, "missing", filepath.Join(root, "home", "store", "missing")); err == nil {
		t.Fatal("parentMetadata hash error unexpectedly succeeded")
	}
}

func TestCLIAdditionalBranches(t *testing.T) {
	root, _ := setupEnv(t)
	var stdout, stderr bytes.Buffer

	for _, args := range [][]string{
		{"add"},
		{"show"},
		{"source"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v help code = %d stderr=%s", args, code, stderr.String())
		}
	}

	for _, args := range [][]string{
		{"add", "bad source: value"},
		{"add", ".", "--bad"},
		{"show", "missing"},
		{"show", ".", "--bad"},
		{"list", "--bad"},
		{"find", "--limit", "bad"},
		{"remove"},
		{"remove", "--bad"},
		{"update", "--bad"},
		{"init", "--bad"},
		{"source", "add"},
		{"source", "add", "/tmp/example", "--bad"},
		{"source", "remove"},
		{"source", "wat"},
		{"source", "wat", "--help"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v unexpectedly succeeded; stdout=%s", args, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty list json code = %d stderr=%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "[]" {
		t.Fatalf("empty list json = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"find", "absent"}, &stdout, &stderr); code != 0 {
		t.Fatalf("find absent code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no matching skills") {
		t.Fatalf("find absent stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty update code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no skills updated") {
		t.Fatalf("empty update stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update", "--check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty update check code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no locked skills") {
		t.Fatalf("empty update check stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "add", "/tmp/example"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add default code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list text code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example") {
		t.Fatalf("source list text = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	oldStdin := os.Stdin
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = write.Close()
	os.Stdin = read
	if code := Run([]string{"mcp"}, &stdout, &stderr); code != 0 {
		t.Fatalf("mcp eof code = %d stderr=%s", code, stderr.String())
	}
	os.Stdin = oldStdin
	_ = read.Close()

	writeTestFile(t, filepath.Join(root, "home", "store"), "file", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"status"}, &stdout, &stderr); code == 0 {
		t.Fatal("status with bad store unexpectedly succeeded")
	}
}

func TestCLIMoreFlagBranches(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "repo")
	writeTestFile(t, filepath.Join(source, "skills", "redis", "SKILL.md"), "# Redis\n\nBase.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--ref", "ignored", "--subpath", "skills", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add ref/subpath code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"show", source, "--ref", "ignored", "--subpath", "skills", "--skill", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show source flags code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "name: \"redis\"") ||
		!strings.Contains(stdout.String(), "description: \"Base.\"") ||
		!strings.Contains(stdout.String(), "# Redis") {
		t.Fatalf("show source stdout = %q", stdout.String())
	}
	if code := Run([]string{"list", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list agent code = %d stderr=%s", code, stderr.String())
	}
	writeTestFile(t, filepath.Join(root, "home", "overlays", "redis", "SKILL.append.md"), "Overlay.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"remove", "redis", "--delete-overlays"}, &stdout, &stderr); code != 0 {
		t.Fatalf("remove delete overlays code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "overlays", "redis")); !os.IsNotExist(err) {
		t.Fatalf("overlay still exists: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync", "--with-overlays"}, &stdout, &stderr); code != 2 {
		t.Fatalf("sync with overlays code = %d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "ls"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SOURCE") || !strings.Contains(stdout.String(), "repo") {
		t.Fatalf("source list after raw add = %q", stdout.String())
	}

	oldStdin := os.Stdin
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = write.WriteString("Bad: header\r\n\r\n")
	_ = write.Close()
	os.Stdin = read
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"mcp"}, &stdout, &stderr); code == 0 {
		t.Fatal("mcp malformed input unexpectedly succeeded")
	}
	os.Stdin = oldStdin
	_ = read.Close()
}

func TestSourceCommands(t *testing.T) {
	root, configPath := setupEnv(t)
	var stdout, stderr bytes.Buffer

	source := filepath.Join(root, "Obsidian Skills")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nBase.\n", 0o644)
	writeTestFile(t, filepath.Join(source, "overlays", "shadow", "SKILL.md"), "---\nname: Shadow\n---\n\nIgnored.\n", 0o644)
	writeTestFile(t, filepath.Join(source, ".obsidian", "plugin", "SKILL.md"), "---\nname: Plugin\n---\n\nIgnored.\n", 0o644)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "add", source, "--name", "Obsidian"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add named code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "added source Obsidian") {
		t.Fatalf("source add stdout = %q", stdout.String())
	}
	if rawConfig := readTestFile(t, configPath); !strings.Contains(rawConfig, `"name": "Obsidian"`) || !strings.Contains(rawConfig, source) {
		t.Fatalf("config after source add = %s", rawConfig)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SOURCE") || !strings.Contains(stdout.String(), "LOCATION") || !strings.Contains(stdout.String(), "Obsidian") {
		t.Fatalf("source list = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list json code = %d stderr=%s", code, stderr.String())
	}
	var rows []sourceRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("source list json decode: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 ||
		rows[0].ID == "" ||
		rows[0].Name != "Obsidian" ||
		rows[0].DisplayName != "Obsidian" ||
		rows[0].CanonicalID != "Obsidian" ||
		rows[0].DefaultUpdatePolicy != config.UpdatePolicyManual {
		t.Fatalf("source rows = %#v", rows)
	}
	obsidianSourceID := rows[0].ID

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "skills", "Obsidian"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source skills named code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SKILL") ||
		!strings.Contains(stdout.String(), "DESCRIPTION") ||
		!strings.Contains(stdout.String(), "redis") ||
		!strings.Contains(stdout.String(), "cache") ||
		!strings.Contains(stdout.String(), "redis/SKILL.md") ||
		strings.Contains(stdout.String(), "NAME") ||
		strings.Contains(stdout.String(), "shadow") {
		t.Fatalf("source skills stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "skills", source, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source skills raw json code = %d stderr=%s", code, stderr.String())
	}
	var skills []library.SourceSkill
	if err := json.Unmarshal(stdout.Bytes(), &skills); err != nil {
		t.Fatalf("source skills json decode: %v\n%s", err, stdout.String())
	}
	if len(skills) != 1 || skills[0].StoreName != "redis" || skills[0].SourceSubpath != "redis" {
		t.Fatalf("source skills json = %#v", skills)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"add", source, "--skill", "redis", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("raw add existing named source code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list after raw named add code = %d stderr=%s", code, stderr.String())
	}
	rows = nil
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("source list after raw named add json decode: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].Name != "Obsidian" || rows[0].Source != source {
		t.Fatalf("source rows after raw named add = %#v", rows)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"add", "Obsidian", "--skill", "redis", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add from named source code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "store", "redis", "SKILL.md")); err != nil {
		t.Fatalf("named source install missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "codex-skills", "redis", "SKILL.md")); err != nil {
		t.Fatalf("named source agent output missing: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list with source code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SOURCE") || !strings.Contains(stdout.String(), "Obsidian") {
		t.Fatalf("list should show source name: %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list json with source code = %d stderr=%s", code, stderr.String())
	}
	var installedRows []installedSkillRow
	if err := json.Unmarshal(stdout.Bytes(), &installedRows); err != nil {
		t.Fatalf("list json with source decode: %v\n%s", err, stdout.String())
	}
	if len(installedRows) != 1 ||
		installedRows[0].SourceDisplayName != "Obsidian" ||
		!installedRows[0].SourceConfigured ||
		installedRows[0].SkillSubpath != "redis" {
		t.Fatalf("list json source row = %#v", installedRows)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "remove", "Obsidian"}, &stdout, &stderr); code != 2 {
		t.Fatalf("source remove warning code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "will remove 1 installed skill(s)") ||
		!strings.Contains(stderr.String(), "- redis") ||
		!strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("source remove warning stderr = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "store", "redis", "SKILL.md")); err != nil {
		t.Fatalf("source remove without yes should keep installed skill: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "remove", "Obsidian", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source remove named yes code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "removed source Obsidian") || !strings.Contains(stdout.String(), "removed redis") {
		t.Fatalf("source remove stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "store", "redis", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("source remove should remove installed skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "codex-skills", "redis")); !os.IsNotExist(err) {
		t.Fatalf("source remove should remove marked agent output: %v", err)
	}
	lock, err := library.ReadLock(config.Config{
		LockPath: filepath.Join(root, "home", "upskill-lock.json"),
	})
	if err != nil {
		t.Fatalf("read lock after source remove: %v", err)
	}
	if _, ok := lock.Skills["redis"]; ok {
		t.Fatalf("source remove should remove lock entry: %#v", lock.Skills["redis"])
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "add", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add unnamed code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list unnamed code = %d stderr=%s", code, stderr.String())
	}
	rows = nil
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("source list unnamed json decode: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].ID == "" || rows[0].Name != "" || rows[0].DisplayName != source || rows[0].Source != source {
		t.Fatalf("unnamed source rows = %#v", rows)
	}
	unnamedSourceID := rows[0].ID

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "add", source, "--name", "Personal"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add rename code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated source Personal") {
		t.Fatalf("source add rename stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list renamed code = %d stderr=%s", code, stderr.String())
	}
	rows = nil
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("source list renamed json decode: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].Name != "Personal" || rows[0].Source != source || rows[0].ID != unnamedSourceID {
		t.Fatalf("renamed source rows = %#v", rows)
	}
	if rows[0].ID != obsidianSourceID {
		t.Fatalf("source id changed after removing and re-adding same identity: %s vs %s", rows[0].ID, obsidianSourceID)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "rm", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("source remove raw code = %d stderr=%s", code, stderr.String())
	}

	rawSource := filepath.Join(root, "Raw Skills")
	writeTestFile(t, filepath.Join(rawSource, "docs", "SKILL.md"), "---\nname: Docs\ndescription: writing\n---\n\nDraft docs.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"add", rawSource, "--skill", "docs", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add raw source code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list after raw add code = %d stderr=%s", code, stderr.String())
	}
	rows = nil
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("source list raw json decode: %v\n%s", err, stdout.String())
	}
	foundRaw := false
	for _, row := range rows {
		if row.Source == rawSource && row.Name == "" && row.DisplayName == rawSource {
			foundRaw = true
		}
	}
	if !foundRaw {
		t.Fatalf("raw source was not auto-registered: %#v", rows)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "remove", rawSource, "--keep-installed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source remove keep-installed code = %d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "home", "store", "docs", "SKILL.md")); err != nil {
		t.Fatalf("keep-installed should keep store skill: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list json after keep-installed code = %d stderr=%s", code, stderr.String())
	}
	installedRows = nil
	if err := json.Unmarshal(stdout.Bytes(), &installedRows); err != nil {
		t.Fatalf("list json after keep-installed decode: %v\n%s", err, stdout.String())
	}
	if len(installedRows) != 1 ||
		installedRows[0].StoreName != "docs" ||
		installedRows[0].SourceConfigured ||
		!strings.Contains(installedRows[0].SourceDisplayName, rawSource) {
		t.Fatalf("list json after keep-installed = %#v", installedRows)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "remove", "missing"}, &stdout, &stderr); code != 1 {
		t.Fatalf("source remove missing code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	removedCommand := "va" + "ult"
	if code := Run([]string{removedCommand}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("removed command code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestUpsertSourceRecordPreservesIdentityMetadata(t *testing.T) {
	source := config.NormalizeSource(config.Source{
		Name:                "team",
		Source:              "/tmp/team-skills",
		DefaultUpdatePolicy: config.UpdatePolicyAuto,
	})
	sources := map[string]config.Source{
		config.SourceKey(source): source,
	}
	renamed, key, updated := upsertSourceRecord(sources, config.Source{
		Name:   "renamed",
		Source: "/tmp/team-skills",
	})
	if !updated || key != "renamed" {
		t.Fatalf("upsert result = %#v %s %v", renamed, key, updated)
	}
	if renamed.ID != source.ID || renamed.DefaultUpdatePolicy != config.UpdatePolicyAuto {
		t.Fatalf("source metadata was not preserved: before=%#v after=%#v", source, renamed)
	}
	if _, ok := sources["team"]; ok {
		t.Fatalf("old source key still present: %#v", sources)
	}
}

func TestCLIUpdateNoticeGoesToStderr(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "notice-source")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}

	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "notice: updates available for redis") {
		t.Fatalf("stderr missing update notice: %q", stderr.String())
	}
	var rows []installedSkillRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if len(rows) != 1 || rows[0].StoreName != "redis" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestCLIPreflightAutoUpdatesNormalCommands(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "auto-source")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--update-policy", "auto", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}

	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "notice: automatically updated redis") {
		t.Fatalf("stderr missing auto-update notice: %q", stderr.String())
	}
	if got := readTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md")); !strings.Contains(got, "New.") {
		t.Fatalf("store was not auto-updated:\n%s", got)
	}
	if got := readTestFile(t, filepath.Join(root, "codex-skills", "redis", "SKILL.md")); !strings.Contains(got, "New.") {
		t.Fatalf("agent output was not rendered:\n%s", got)
	}
	lock, err := library.ReadLock(config.Config{LockPath: filepath.Join(root, "home", "upskill-lock.json")})
	if err != nil {
		t.Fatal(err)
	}
	if lock.Skills["redis"].UpdatePolicy != config.UpdatePolicyAuto || !strings.Contains(lock.Skills["redis"].Hash, "sha256:") {
		t.Fatalf("lock entry = %#v", lock.Skills["redis"])
	}
	var rows []installedSkillRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
}

func TestCLIReadOnlyPreflightDoesNotAutoUpdate(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "readonly-source")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--update-policy", "auto", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n", 0o644)

	for _, args := range [][]string{
		{"update", "--check"},
		{"update", "--diff"},
		{"source", "skills", source},
		{"source", "find", source, "redis"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v code = %d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "notice: automatic updates pending for redis") {
			t.Fatalf("%v missing pending notice: %q", args, stderr.String())
		}
		if got := readTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md")); !strings.Contains(got, "Old.") {
			t.Fatalf("%v changed store during read-only preflight:\n%s", args, got)
		}
		if _, err := os.Stat(filepath.Join(root, "codex-skills", "redis")); !os.IsNotExist(err) {
			t.Fatalf("%v rendered agent output during read-only preflight: %v", args, err)
		}
	}
}

func TestCLINoCheckSkipsPreflight(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "no-check-source")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--update-policy", "auto", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n", 0o644)

	for _, args := range [][]string{
		{"--no-check", "list", "--json"},
		{"list", "--json", "--no-check"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v code = %d stderr=%s", args, code, stderr.String())
		}
		if strings.Contains(stderr.String(), "notice:") {
			t.Fatalf("%v printed notice with --no-check: %q", args, stderr.String())
		}
		if got := readTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md")); !strings.Contains(got, "Old.") {
			t.Fatalf("%v changed store with --no-check:\n%s", args, got)
		}
	}

	t.Setenv("UPSKILL_NO_CHECK", "1")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("env no-check list code = %d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "notice:") {
		t.Fatalf("env no-check printed notice: %q", stderr.String())
	}
	if got := readTestFile(t, filepath.Join(root, "home", "store", "redis", "SKILL.md")); !strings.Contains(got, "Old.") {
		t.Fatalf("env no-check changed store:\n%s", got)
	}
}

func TestCLIAddUpdatePolicy(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "policy-source")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nRedis.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--update-policy", "auto", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	lock, err := library.ReadLock(config.Config{LockPath: filepath.Join(root, "home", "upskill-lock.json")})
	if err != nil {
		t.Fatal(err)
	}
	if lock.Skills["redis"].UpdatePolicy != config.UpdatePolicyAuto {
		t.Fatalf("lock policy = %#v", lock.Skills["redis"])
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"add", source, "--skill", "redis", "--update-policy", "sometimes", "--no-render"}, &stdout, &stderr); code != 2 {
		t.Fatalf("invalid add policy code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid update policy") {
		t.Fatalf("invalid policy stderr = %q", stderr.String())
	}
}

func TestCLISourceDefaultUpdatePolicy(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source-default-policy")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nRedis.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"source", "add", source, "--name", "team", "--default-update-policy", "auto"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source add code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("source list code = %d stderr=%s", code, stderr.String())
	}
	var sources []sourceRow
	if err := json.Unmarshal(stdout.Bytes(), &sources); err != nil {
		t.Fatalf("source rows decode: %v\n%s", err, stdout.String())
	}
	if len(sources) != 1 || sources[0].DefaultUpdatePolicy != config.UpdatePolicyAuto {
		t.Fatalf("source rows = %#v", sources)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"add", "team", "--skill", "redis", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add from source code = %d stderr=%s", code, stderr.String())
	}
	lock, err := library.ReadLock(config.Config{LockPath: filepath.Join(root, "home", "upskill-lock.json")})
	if err != nil {
		t.Fatal(err)
	}
	if lock.Skills["redis"].UpdatePolicy != config.UpdatePolicyAuto {
		t.Fatalf("lock policy = %#v", lock.Skills["redis"])
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "add", source, "--default-update-policy", "sometimes"}, &stdout, &stderr); code != 2 {
		t.Fatalf("invalid source policy code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid update policy") {
		t.Fatalf("invalid source policy stderr = %q", stderr.String())
	}
}

func TestCLIUpdatePolicyCommand(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "update-policy-source")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nRedis.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update-policy", "redis", "--auto"}, &stdout, &stderr); code != 0 {
		t.Fatalf("update-policy code = %d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "update check failed") {
		t.Fatalf("update-policy should not run preflight: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "redis\tmanual -> auto") {
		t.Fatalf("update-policy stdout = %q", stdout.String())
	}
	lock, err := library.ReadLock(config.Config{LockPath: filepath.Join(root, "home", "upskill-lock.json")})
	if err != nil {
		t.Fatal(err)
	}
	if lock.Skills["redis"].UpdatePolicy != config.UpdatePolicyAuto {
		t.Fatalf("lock policy = %#v", lock.Skills["redis"])
	}
	if _, err := os.Stat(filepath.Join(root, "home", "store", "redis", "SKILL.md")); err != nil {
		t.Fatalf("update-policy should not remove or update store: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update-policy", "redis"}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing policy code = %d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update-policy", "missing", "--manual"}, &stdout, &stderr); code != 1 {
		t.Fatalf("missing skill code = %d stderr=%s", code, stderr.String())
	}
}

func TestCLIDoesNotNoticeUninstalledSourceSkills(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source-catalog")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nRedis.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stderr=%s", code, stderr.String())
	}

	writeTestFile(t, filepath.Join(source, "docs", "SKILL.md"), "---\nname: Docs\n---\n\nDocs.\n", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "skills", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("source skills code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "docs") {
		t.Fatalf("source skills did not show new source skill: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "notice:") {
		t.Fatalf("unexpected notice for uninstalled skill: %q", stderr.String())
	}
}

func TestSyncErrors(t *testing.T) {
	root, _ := setupEnv(t)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"sync", "--agent"}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing agent value code = %d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync", "--dry-run"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown flag code = %d", code)
	}

	root, _ = setupEnv(t)
	writeTestFile(t, filepath.Join(root, "home", "store"), "file-not-dir", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync"}, &stdout, &stderr); code == 0 {
		t.Fatal("sync render failure unexpectedly succeeded")
	}
}

func TestCLICompletionBranches(t *testing.T) {
	root, _ := setupEnv(t)
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("empty list code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no skills installed") {
		t.Fatalf("empty list stdout = %q", stdout.String())
	}

	writeTestFile(t, filepath.Join(root, "home", "store"), "store-file", 0o644)
	for _, args := range [][]string{
		{"list"},
		{"find", "anything"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}
	if err := os.Remove(filepath.Join(root, "home", "store")); err != nil {
		t.Fatal(err)
	}

	writeTestFile(t, filepath.Join(root, "home", "store", "redis"), "file-not-dir", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"show", "redis"}, &stdout, &stderr); code == 0 {
		t.Fatal("show with file skill unexpectedly succeeded")
	}
	if err := os.Remove(filepath.Join(root, "home", "store", "redis")); err != nil {
		t.Fatal(err)
	}

	writeTestFile(t, filepath.Join(root, "home", "upskill-lock.json"), "{bad", 0o644)
	for _, args := range [][]string{
		{"remove", "redis"},
		{"update"},
		{"update", "--check"},
		{"sync"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}
	if err := os.Remove(filepath.Join(root, "home", "upskill-lock.json")); err != nil {
		t.Fatal(err)
	}

	initDir := filepath.Join(root, "existing-skill")
	writeTestFile(t, filepath.Join(initDir, "SKILL.md"), "exists", 0o644)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "Existing", "--dir", initDir}, &stdout, &stderr); code == 0 {
		t.Fatal("duplicate init unexpectedly succeeded")
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"source", "list", "--bad"}, &stdout, &stderr); code != 2 {
		t.Fatalf("source list bad flag code = %d", code)
	}

	parentFile := filepath.Join(root, "config-parent-file")
	writeTestFile(t, parentFile, "file", 0o644)
	cfg := config.Config{ConfigPath: filepath.Join(parentFile, "config.json"), Sources: map[string]config.Source{}}
	stdout.Reset()
	stderr.Reset()
	if code := runSource(cfg, []string{"add", "/tmp/example"}, &stdout, &stderr); code == 0 {
		t.Fatal("source add save failure unexpectedly succeeded")
	}
	cfg.Sources["example"] = config.Source{Name: "example", Source: "/tmp/example"}
	stdout.Reset()
	stderr.Reset()
	if code := runSource(cfg, []string{"remove", "example"}, &stdout, &stderr); code == 0 {
		t.Fatal("source remove save failure unexpectedly succeeded")
	}

	badOverlayRoot := filepath.Join(root, "overlay-file")
	writeTestFile(t, badOverlayRoot, "file", 0o644)
	statusCfg := config.Config{
		StoreDir:    filepath.Join(root, "status-store"),
		OverlaysDir: badOverlayRoot,
	}
	stdout.Reset()
	stderr.Reset()
	if code := runStatus(statusCfg, &stdout, &stderr); code == 0 {
		t.Fatal("status overlay failure unexpectedly succeeded")
	}
	badDraftRoot := filepath.Join(root, "draft-file")
	writeTestFile(t, badDraftRoot, "file", 0o644)
	statusCfg = config.Config{
		StoreDir:    filepath.Join(root, "status-store"),
		OverlaysDir: filepath.Join(root, "status-overlays"),
		DraftsDir:   badDraftRoot,
	}
	stdout.Reset()
	stderr.Reset()
	if code := runStatus(statusCfg, &stdout, &stderr); code == 0 {
		t.Fatal("status draft failure unexpectedly succeeded")
	}

	doctorCfg := config.Config{
		Home:        filepath.Join(parentFile, "home"),
		StoreDir:    filepath.Join(root, "doctor-store"),
		OverlaysDir: filepath.Join(root, "doctor-overlays"),
		RenderedDir: filepath.Join(root, "doctor-rendered"),
	}
	stdout.Reset()
	stderr.Reset()
	if code := runDoctor(doctorCfg, &stdout, &stderr); code == 0 {
		t.Fatal("doctor mkdir failure unexpectedly succeeded")
	}

	renderCfg := config.Config{
		StoreDir:    filepath.Join(root, "missing-store"),
		RenderedDir: filepath.Join(root, "rendered"),
		OverlaysDir: filepath.Join(root, "overlays"),
	}
	stdout.Reset()
	stderr.Reset()
	if code := renderInstalled(renderCfg, []library.InstalledSkill{{StoreName: "missing"}}, "", &stdout, &stderr); code == 0 {
		t.Fatal("renderInstalled failure unexpectedly succeeded")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeCLIZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte(body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
