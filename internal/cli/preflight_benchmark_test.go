package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func setupPreflightPerformanceFixture(tb testing.TB) string {
	tb.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		tb.Skip("git not available")
	}
	root := tb.TempDir()
	configPath := filepath.Join(root, "config.json")
	configData, err := json.Marshal(map[string]any{
		"preflight_check_ttl": "1h",
		"agents": map[string]string{
			"codex": filepath.Join(root, "codex-skills"),
		},
	})
	if err != nil {
		tb.Fatal(err)
	}
	writePreflightFixtureFile(tb, configPath, string(configData), 0o644)
	tb.Setenv("UPSKILL_HOME", filepath.Join(root, "home"))
	tb.Setenv("UPSKILL_CONFIG", configPath)

	localSource := filepath.Join(root, "local-source")
	for index := 0; index < 30; index++ {
		name := fmt.Sprintf("local-%02d", index)
		writePreflightFixtureFile(
			tb,
			filepath.Join(localSource, name, "SKILL.md"),
			fmt.Sprintf("---\nname: %s\n---\n\nLocal performance fixture %d.\n", name, index),
			0o644,
		)
	}
	runPreflightFixtureCLI(tb, []string{"add", localSource, "--no-render"})

	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	runPreflightFixtureGit(tb, "", "init", "--bare", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		tb.Fatal(err)
	}
	runPreflightFixtureGit(tb, work, "init")
	writePreflightFixtureFile(
		tb,
		filepath.Join(work, "skills", "manual-git", "SKILL.md"),
		"---\nname: Manual Git\n---\n\nManual Git performance fixture.\n",
		0o644,
	)
	runPreflightFixtureGit(tb, work, "add", ".")
	runPreflightFixtureGit(tb, work, "commit", "-m", "manual Git fixture")
	runPreflightFixtureGit(tb, work, "branch", "-M", "main")
	runPreflightFixtureGit(tb, work, "remote", "add", "origin", remote)
	runPreflightFixtureGit(tb, work, "push", "-u", "origin", "main")
	runPreflightFixtureGit(tb, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runPreflightFixtureCLI(tb, []string{
		"add", "file://" + remote,
		"--ref", "main",
		"--subpath", "skills",
		"--skill", "manual-git",
		"--no-render",
	})

	wrapper := filepath.Join(root, "git-wrapper")
	countPath := filepath.Join(wrapper, "operations.log")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> %q\nexec %q \"$@\"\n", countPath, realGit)
	writePreflightFixtureFile(tb, filepath.Join(wrapper, "git"), script, 0o755)
	tb.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	return countPath
}

func writePreflightFixtureFile(tb testing.TB, path string, content string, mode os.FileMode) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		tb.Fatal(err)
	}
}

func runPreflightFixtureGit(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
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
		tb.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runPreflightFixtureCLI(tb testing.TB, args []string) {
	tb.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(args, &stdout, &stderr); code != 0 {
		tb.Fatalf("%v code = %d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
	}
}

func assertPreflightFixtureRanNoGit(tb testing.TB, countPath string) {
	tb.Helper()
	data, err := os.ReadFile(countPath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		tb.Fatal(err)
	}
	if len(bytes.TrimSpace(data)) != 0 {
		tb.Fatalf("warm preflight executed Git operations: %q", data)
	}
}

func preflightP95(durations []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := (95*len(sorted) + 99) / 100
	if index == 0 {
		return 0
	}
	return sorted[index-1]
}

func TestWarmInstalledListAndShowPreflightPerformance(t *testing.T) {
	countPath := setupPreflightPerformanceFixture(t)
	runPreflightFixtureCLI(t, []string{"list", "--json"})
	runPreflightFixtureCLI(t, []string{"show", "local-00"})

	const iterations = 20
	listDurations := make([]time.Duration, 0, iterations)
	showDurations := make([]time.Duration, 0, iterations)
	for index := 0; index < iterations; index++ {
		start := time.Now()
		runPreflightFixtureCLI(t, []string{"list", "--json"})
		listDurations = append(listDurations, time.Since(start))

		start = time.Now()
		runPreflightFixtureCLI(t, []string{"show", "local-00"})
		showDurations = append(showDurations, time.Since(start))
	}
	assertPreflightFixtureRanNoGit(t, countPath)

	listP95 := preflightP95(listDurations)
	showP95 := preflightP95(showDurations)
	t.Logf("warm fixture p95: list=%s show=%s", listP95, showP95)
	if runtime.GOOS == "darwin" {
		const limit = 100 * time.Millisecond
		if listP95 >= limit || showP95 >= limit {
			t.Fatalf("warm p95 exceeds %s: list=%s show=%s", limit, listP95, showP95)
		}
	}
}

func BenchmarkWarmInstalledList(b *testing.B) {
	countPath := setupPreflightPerformanceFixture(b)
	runPreflightFixtureCLI(b, []string{"list", "--json"})
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		runPreflightFixtureCLI(b, []string{"list", "--json"})
	}
	b.StopTimer()
	assertPreflightFixtureRanNoGit(b, countPath)
}

func BenchmarkWarmInstalledShow(b *testing.B) {
	countPath := setupPreflightPerformanceFixture(b)
	runPreflightFixtureCLI(b, []string{"show", "local-00"})
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		runPreflightFixtureCLI(b, []string{"show", "local-00"})
	}
	b.StopTimer()
	assertPreflightFixtureRanNoGit(b, countPath)
}
