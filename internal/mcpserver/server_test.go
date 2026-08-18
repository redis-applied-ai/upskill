package mcpserver

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"upskill/internal/activity"
	"upskill/internal/config"
	"upskill/internal/draft"
	"upskill/internal/library"
	"upskill/internal/render"
)

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func testConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		Home:         filepath.Join(root, "home"),
		StoreDir:     filepath.Join(root, "home", "store"),
		OverlaysDir:  filepath.Join(root, "home", "overlays"),
		RenderedDir:  filepath.Join(root, "home", "rendered"),
		DraftsDir:    filepath.Join(root, "home", "drafts"),
		ImportsDir:   filepath.Join(root, "home", "imports"),
		ImportDirs:   []string{filepath.Join(root, "home", "imports")},
		ConfigPath:   filepath.Join(root, "home", "config.json"),
		LockPath:     filepath.Join(root, "home", "upskill-lock.json"),
		StatsPath:    filepath.Join(root, "home", "upskill-stats.jsonl"),
		AgentOutputs: []config.AgentOutput{{Name: "codex", Path: filepath.Join(root, "codex-skills")}},
		Sources:      map[string]config.Source{},
	}
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nBase.\n")
	writeFile(t, filepath.Join(cfg.StoreDir, "redis", "references", "guide.md"), "Guide.\n")
	return cfg
}

func TestRetrievalCountingBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		wantDelta uint64
		setup     func(*testing.T, config.Config) map[string]any
		tool      string
		args      map[string]any
	}{
		{name: "find installed", tool: "find_skills", args: map[string]any{"query": "redis"}},
		{name: "list files", tool: "list_skill_files", args: map[string]any{"skill_id": "redis", "rendered": false}},
		{name: "read skill", wantDelta: 1, tool: "read_skill", args: map[string]any{"skill_id": "redis", "rendered": false, "include_files": false}},
		{name: "read support file", wantDelta: 1, tool: "read_skill_file", args: map[string]any{"skill_id": "redis", "path": "references/guide.md", "rendered": false}},
		{name: "render only", tool: "render_skill", args: map[string]any{"skill_id": "redis", "copy": false, "base_only": true}},
		{name: "check updates", tool: "check_skill_updates", args: map[string]any{}},
		{name: "sync preview", tool: "sync_skills", args: map[string]any{"dry_run": true}},
		{
			name: "source inspection",
			tool: "find_source_skills",
			setup: func(t *testing.T, cfg config.Config) map[string]any {
				source := filepath.Join(cfg.Home, "source")
				writeFile(t, filepath.Join(source, "source-skill", "SKILL.md"), "---\nname: Source Skill\ndescription: source\n---\n\nSource.\n")
				return map[string]any{"source": source}
			},
		},
		{
			name: "draft read",
			tool: "read_skill_draft",
			setup: func(t *testing.T, cfg config.Config) map[string]any {
				if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis", "from_skill": "redis"}); err != nil {
					t.Fatal(err)
				}
				return map[string]any{"draft_id": "draft:redis"}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig(t)
			args := test.args
			if test.setup != nil {
				args = test.setup(t, cfg)
			}
			before, err := activity.Stats(cfg, "redis", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := callTool(cfg, test.tool, args); err != nil {
				t.Fatalf("%s: %v", test.tool, err)
			}
			after, err := activity.Stats(cfg, "redis", 0)
			if err != nil {
				t.Fatal(err)
			}
			if delta := after.RetrievalCount - before.RetrievalCount; delta != test.wantDelta {
				t.Fatalf("retrieval delta = %d, want %d", delta, test.wantDelta)
			}
		})
	}
}

func TestFailedContentReadDoesNotIncrementRetrieval(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "read_skill_file", map[string]any{
		"skill_id": "redis",
		"path":     "references/missing.md",
		"rendered": false,
	}); err == nil {
		t.Fatal("missing support file unexpectedly read")
	}
	stats, err := activity.Stats(cfg, "redis", 0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RetrievalCount != 0 {
		t.Fatalf("failed read recorded %d retrievals", stats.RetrievalCount)
	}
}

func TestReadSkillWithManifestIncrementsRetrievalOnce(t *testing.T) {
	cfg := testConfig(t)
	value, err := callTool(cfg, "read_skill", map[string]any{
		"skill_id":      "redis",
		"rendered":      false,
		"include_files": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if _, ok := result["files"]; !ok || result["retrieval_recorded"] != true {
		t.Fatalf("read result = %#v", result)
	}
	stats, err := activity.Stats(cfg, "redis", 0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RetrievalCount != 1 {
		t.Fatalf("retrieval count = %d", stats.RetrievalCount)
	}
}

func TestSupportFileReadsCountOncePerCall(t *testing.T) {
	cfg := testConfig(t)
	for call := uint64(1); call <= 3; call++ {
		value, err := callTool(cfg, "read_skill_file", map[string]any{
			"skill_id": "redis",
			"path":     "references/guide.md",
			"rendered": false,
		})
		if err != nil {
			t.Fatal(err)
		}
		result := value.(map[string]any)
		counts := result["stats"].(map[string]any)
		if result["retrieval_recorded"] != true || counts["retrieval_count"] != call {
			t.Fatalf("call %d result = %#v", call, result)
		}
	}
	stats, err := activity.Stats(cfg, "redis", 0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RetrievalCount != 3 {
		t.Fatalf("retrieval count = %d", stats.RetrievalCount)
	}
}

func TestDraftReadsAndRenderingDoNotIncrementInstalledRetrievals(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis", "from_skill": "redis"}); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{tool: "read_skill", args: map[string]any{"skill_id": "draft:redis", "include_files": true}},
		{tool: "read_skill_file", args: map[string]any{"skill_id": "draft:redis", "path": "SKILL.md"}},
		{tool: "render_skill_draft", args: map[string]any{"draft_id": "draft:redis", "copy": false}},
	} {
		if _, err := callTool(cfg, call.tool, call.args); err != nil {
			t.Fatalf("%s: %v", call.tool, err)
		}
	}
	stats, err := activity.Stats(cfg, "redis", 0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.RetrievalCount != 0 {
		t.Fatalf("draft operations recorded %d installed retrievals", stats.RetrievalCount)
	}
}

func TestAutomaticRecordingFailureStillReturnsContent(t *testing.T) {
	cfg := testConfig(t)
	cfg.StatsPath = cfg.Home
	value, err := callTool(cfg, "read_skill", map[string]any{
		"skill_id":      "redis",
		"rendered":      false,
		"include_files": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if result["content"] == "" || result["retrieval_recorded"] != false {
		t.Fatalf("read result = %#v", result)
	}
	warnings, ok := result["activity_warnings"].([]string)
	if !ok || len(warnings) != 1 || !strings.Contains(warnings[0], cfg.StatsPath) {
		t.Fatalf("activity warnings = %#v", result["activity_warnings"])
	}
}

func TestSkillActivityToolsAreRegisteredWithAuthoritativeSchemas(t *testing.T) {
	for _, name := range []string{"get_skill_stats", "record_skill_use", "record_skill_feedback"} {
		definition, ok := findToolDefinition(name)
		if !ok {
			t.Fatalf("tool %s is not registered", name)
		}
		described, err := callTool(testConfig(t), "describe_tool", map[string]any{"name": name})
		if err != nil {
			t.Fatal(err)
		}
		if mustJSON(described.(map[string]any)["inputSchema"]) != mustJSON(definition["inputSchema"]) {
			t.Fatalf("describe_tool schema drift for %s", name)
		}
	}

	feedback, _ := findToolDefinition("record_skill_feedback")
	schema := feedback["inputSchema"].(map[string]any)
	if got, want := mustJSON(schema["required"]), mustJSON([]string{"skill_id", "sentiment", "situation", "details", "proposed_change"}); got != want {
		t.Fatalf("feedback required fields = %s, want %s", got, want)
	}
	properties := schema["properties"].(map[string]any)
	if mustJSON(properties["sentiment"].(map[string]any)["enum"]) != mustJSON([]string{"positive", "negative"}) {
		t.Fatalf("feedback sentiment schema = %#v", properties["sentiment"])
	}
	for _, field := range []string{"situation", "details", "proposed_change"} {
		if properties[field].(map[string]any)["maxLength"] != activity.MaxFeedbackTextBytes {
			t.Fatalf("%s schema = %#v", field, properties[field])
		}
	}

	stats, _ := findToolDefinition("get_skill_stats")
	limit := stats["inputSchema"].(map[string]any)["properties"].(map[string]any)["feedback_limit"].(map[string]any)
	if limit["default"] != activity.DefaultFeedbackLimit || limit["minimum"] != 0 || limit["maximum"] != activity.MaxFeedbackLimit {
		t.Fatalf("feedback_limit schema = %#v", limit)
	}
}

func TestMCPActivityArgumentValidationContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tool    string
		args    map[string]any
		wantErr string
	}{
		{name: "stats missing skill", tool: "get_skill_stats", args: map[string]any{}, wantErr: `parameter "skill_id" is required`},
		{name: "stats wrong limit type", tool: "get_skill_stats", args: map[string]any{"skill_id": "redis", "feedback_limit": "ten"}, wantErr: `parameter "feedback_limit" must be integer`},
		{name: "use unknown field", tool: "record_skill_use", args: map[string]any{"skill_id": "redis", "used": true}, wantErr: `unknown parameter "used"`},
		{name: "feedback missing details", tool: "record_skill_feedback", args: map[string]any{"skill_id": "redis", "sentiment": "positive", "situation": "task", "proposed_change": "keep it"}, wantErr: `parameter "details" is required`},
		{name: "feedback wrong sentiment type", tool: "record_skill_feedback", args: map[string]any{"skill_id": "redis", "sentiment": true, "situation": "task", "details": "worked", "proposed_change": "keep it"}, wantErr: `parameter "sentiment" must be string`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolArguments(tc.tool, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validation error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestGetSkillStatsDoesNotRecordRetrieval(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "include_files": false}); err != nil {
		t.Fatal(err)
	}
	value, err := callTool(cfg, "get_skill_stats", map[string]any{"skill_id": "redis", "feedback_limit": 1000})
	if err != nil {
		t.Fatal(err)
	}
	stats := value.(activity.SkillStats)
	if stats.RetrievalCount != 1 || stats.UseCount != 0 || len(stats.RecentFeedback) != 0 {
		t.Fatalf("stats result = %#v", stats)
	}
	after, err := activity.Stats(cfg, "redis", 0)
	if err != nil {
		t.Fatal(err)
	}
	if after.RetrievalCount != 1 {
		t.Fatalf("get_skill_stats changed retrieval count to %d", after.RetrievalCount)
	}
}

func TestMCPUseAndFeedbackReturnUpdatedCounts(t *testing.T) {
	cfg := testConfig(t)
	useValue, err := callTool(cfg, "record_skill_use", map[string]any{
		"skill_id":        "redis",
		"agent":           "codex",
		"idempotency_key": "task-1:use",
	})
	if err != nil {
		t.Fatal(err)
	}
	use := useValue.(activity.RecordResult)
	if !use.Recorded || use.Duplicate || use.EventID == "" || use.SkillID != "redis" || use.RetrievalCount != 0 || use.UseCount != 1 {
		t.Fatalf("use result = %#v", use)
	}

	feedbackValue, err := callTool(cfg, "record_skill_feedback", map[string]any{
		"skill_id":        "redis",
		"sentiment":       "positive",
		"situation":       "Applying the skill during a review",
		"details":         "The checklist caught a missing validation",
		"proposed_change": "Keep the validation checklist",
		"agent":           "codex",
		"idempotency_key": "task-1:feedback",
	})
	if err != nil {
		t.Fatal(err)
	}
	feedback := feedbackValue.(activity.RecordResult)
	if !feedback.Recorded || feedback.Duplicate || feedback.EventID == "" || feedback.Sentiment != activity.SentimentPositive || feedback.UseCount != 1 || feedback.FeedbackCount != 1 || feedback.PositiveFeedbackCount != 1 || feedback.NegativeFeedbackCount != 0 {
		t.Fatalf("feedback result = %#v", feedback)
	}
}

func TestMCPActivityIdempotencyMatchesSharedCLIContract(t *testing.T) {
	cfg := testConfig(t)
	useArgs := map[string]any{"skill_id": "redis", "idempotency_key": "same-use"}
	firstValue, err := callTool(cfg, "record_skill_use", useArgs)
	if err != nil {
		t.Fatal(err)
	}
	secondValue, err := callTool(cfg, "record_skill_use", useArgs)
	if err != nil {
		t.Fatal(err)
	}
	first := firstValue.(activity.RecordResult)
	second := secondValue.(activity.RecordResult)
	if !first.Recorded || first.Duplicate || second.Recorded || !second.Duplicate || first.EventID != second.EventID || second.UseCount != 1 {
		t.Fatalf("use idempotency results = %#v %#v", first, second)
	}

	feedbackArgs := map[string]any{
		"skill_id":        "redis",
		"sentiment":       "negative",
		"situation":       "Applying the skill",
		"details":         "A needed step was absent",
		"proposed_change": "Add the missing step",
		"idempotency_key": "same-feedback",
	}
	firstFeedbackValue, err := callTool(cfg, "record_skill_feedback", feedbackArgs)
	if err != nil {
		t.Fatal(err)
	}
	secondFeedbackValue, err := callTool(cfg, "record_skill_feedback", feedbackArgs)
	if err != nil {
		t.Fatal(err)
	}
	firstFeedback := firstFeedbackValue.(activity.RecordResult)
	secondFeedback := secondFeedbackValue.(activity.RecordResult)
	if !firstFeedback.Recorded || secondFeedback.Recorded || !secondFeedback.Duplicate || firstFeedback.EventID != secondFeedback.EventID || secondFeedback.FeedbackCount != 1 {
		t.Fatalf("feedback idempotency results = %#v %#v", firstFeedback, secondFeedback)
	}

	if _, err := callTool(cfg, "record_skill_feedback", map[string]any{
		"skill_id":        "redis",
		"sentiment":       "positive",
		"situation":       "Different event type",
		"details":         "Conflicting idempotency key",
		"proposed_change": "Use distinct keys",
		"idempotency_key": "same-use",
	}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("idempotency conflict error = %v", err)
	}
}

func TestRetrievedAndUsedScenarioE2E(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "include_files": false}); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"skill_id": "redis", "agent": "codex", "idempotency_key": "scenario:redis:use"}
	firstValue, err := callTool(cfg, "record_skill_use", args)
	if err != nil {
		t.Fatal(err)
	}
	secondValue, err := callTool(cfg, "record_skill_use", args)
	if err != nil {
		t.Fatal(err)
	}
	first := firstValue.(activity.RecordResult)
	second := secondValue.(activity.RecordResult)
	if !first.Recorded || !second.Duplicate || first.EventID != second.EventID {
		t.Fatalf("use results = %#v %#v", first, second)
	}
	value, err := callTool(cfg, "get_skill_stats", map[string]any{"skill_id": "redis"})
	if err != nil {
		t.Fatal(err)
	}
	stats := value.(activity.SkillStats)
	if stats.RetrievalCount != 1 || stats.UseCount != 1 || stats.FeedbackCount != 0 {
		t.Fatalf("scenario stats = %#v", stats)
	}
}

func TestPositiveAndNegativeFeedbackScenariosPreserveRevisionAndManagedState(t *testing.T) {
	cfg := testConfig(t)
	identity, err := activity.ResolveSkill(cfg, "redis")
	if err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(cfg.StoreDir, "redis", "SKILL.md")
	storeBefore := readFile(t, storePath)

	for _, tc := range []struct {
		sentiment activity.Sentiment
		details   string
		change    string
	}{
		{sentiment: activity.SentimentNegative, details: "The skill omitted inline review threads", change: "Require inline thread inspection"},
		{sentiment: activity.SentimentPositive, details: "The dry-run prevented an unintended write", change: "Keep the dry-run requirement"},
	} {
		if _, err := callTool(cfg, "record_skill_feedback", map[string]any{
			"skill_id":        "redis",
			"sentiment":       string(tc.sentiment),
			"situation":       "Applying the skill to a real task",
			"details":         tc.details,
			"proposed_change": tc.change,
			"idempotency_key": "scenario:feedback:" + string(tc.sentiment),
		}); err != nil {
			t.Fatal(err)
		}
	}

	value, err := callTool(cfg, "get_skill_stats", map[string]any{"skill_id": "redis", "feedback_limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	stats := value.(activity.SkillStats)
	if stats.FeedbackCount != 2 || stats.PositiveFeedbackCount != 1 || stats.NegativeFeedbackCount != 1 || stats.UseCount != 0 {
		t.Fatalf("feedback scenario stats = %#v", stats)
	}
	for _, feedback := range stats.RecentFeedback {
		if feedback.SkillHash == "" || feedback.SkillHash != identity.SkillHash {
			t.Fatalf("feedback revision = %q, want %q", feedback.SkillHash, identity.SkillHash)
		}
	}
	if got := readFile(t, storePath); got != storeBefore {
		t.Fatalf("feedback changed installed skill:\n%s", got)
	}
	for _, path := range []string{cfg.OverlaysDir, cfg.DraftsDir, cfg.RenderedDir, cfg.LockPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("feedback unexpectedly created or changed %s: %v", path, err)
		}
	}
}

func TestActivityWriteFailureIsNonBlockingOnlyForAutomaticRetrieval(t *testing.T) {
	cfg := testConfig(t)
	cfg.StatsPath = cfg.Home
	value, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "include_files": false})
	if err != nil {
		t.Fatalf("automatic read failed: %v", err)
	}
	read := value.(map[string]any)
	if read["content"] == "" || read["retrieval_recorded"] != false || len(read["activity_warnings"].([]string)) == 0 {
		t.Fatalf("automatic failure result = %#v", read)
	}
	if _, err := callTool(cfg, "record_skill_use", map[string]any{"skill_id": "redis"}); err == nil {
		t.Fatal("explicit use unexpectedly succeeded without persistence")
	}
	if _, err := callTool(cfg, "record_skill_feedback", map[string]any{
		"skill_id":        "redis",
		"sentiment":       "negative",
		"situation":       "Testing unavailable storage",
		"details":         "The event could not be persisted",
		"proposed_change": "Restore writable activity storage",
	}); err == nil {
		t.Fatal("explicit feedback unexpectedly succeeded without persistence")
	}
}

func TestActivityWritesDoNotMutateOtherManagedState(t *testing.T) {
	cfg := testConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "team-source")
	cfg.Sources["team"] = config.Source{ID: "src_team", Name: "team", Source: sourceRoot}
	files := map[string]string{
		cfg.ConfigPath: `{"stats_path":"custom"}`,
		filepath.Join(sourceRoot, "redis", "SKILL.md"):               "Source skill.\n",
		filepath.Join(cfg.OverlaysDir, "redis", "note.md"):           "Overlay state.\n",
		filepath.Join(cfg.DraftsDir, "redis", "SKILL.md"):            "Draft state.\n",
		filepath.Join(cfg.RenderedDir, "redis", "SKILL.md"):          "Rendered state.\n",
		filepath.Join(cfg.AgentOutputs[0].Path, "redis", "SKILL.md"): "Agent output state.\n",
	}
	for path, content := range files {
		writeFile(t, path, content)
	}
	if err := library.WriteLock(cfg, library.Lock{Skills: map[string]library.LockEntry{
		"redis": {
			Name:         "Redis",
			Source:       "team",
			SourceID:     "src_team",
			SourceType:   "local",
			SourceURL:    sourceRoot,
			SkillSubpath: "redis",
			Hash:         "sha256:locked-revision",
		},
	}}); err != nil {
		t.Fatal(err)
	}
	files[cfg.LockPath] = readFile(t, cfg.LockPath)
	files[filepath.Join(cfg.StoreDir, "redis", "SKILL.md")] = readFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md"))

	identity, err := activity.ResolveSkill(cfg, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activity.RecordRetrieval(cfg, identity, activity.ChannelCLI, "show", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(cfg, "record_skill_use", map[string]any{"skill_id": "redis"}); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(cfg, "record_skill_feedback", map[string]any{
		"skill_id":        "redis",
		"sentiment":       "positive",
		"situation":       "Verifying local activity isolation",
		"details":         "The activity write stayed isolated",
		"proposed_change": "Keep activity storage separate",
	}); err != nil {
		t.Fatal(err)
	}

	for path, before := range files {
		if after := readFile(t, path); after != before {
			t.Fatalf("activity changed %s\nbefore: %q\n after: %q", path, before, after)
		}
	}
}

func writeFile(t *testing.T, path string, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runMCPGit(t *testing.T, dir string, args ...string) string {
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

func installMCPGitCounter(t *testing.T, lsRemoteAction ...string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	countPath := filepath.Join(root, "operations.log")
	action := ""
	if len(lsRemoteAction) > 0 && strings.TrimSpace(lsRemoteAction[0]) != "" {
		action = fmt.Sprintf("if [ \"$1\" = \"ls-remote\" ]; then\n  %s\nfi", lsRemoteAction[0])
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> %q\n%s\nexec %q \"$@\"\n", countPath, action, realGit)
	writeFile(t, filepath.Join(root, "git"), script)
	if err := os.Chmod(filepath.Join(root, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return countPath
}

func mcpGitOperationCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]int{}
	}
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, operation := range strings.Fields(string(data)) {
		counts[operation]++
	}
	return counts
}

func configWithInstalledPolicySource(t *testing.T, scope string, writePolicy string) config.Config {
	t.Helper()
	cfg := testConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "policy-source")
	writeFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md"), "---\nname: Redis\ndescription: cache\n---\n\nBase.\n")
	writeFile(t, filepath.Join(sourceRoot, "redis", "references", "guide.md"), "Guide.\n")
	source := config.NormalizeSource(config.Source{
		Name:        "policy-source",
		Source:      sourceRoot,
		Scope:       scope,
		WritePolicy: writePolicy,
	})
	cfg.Sources[config.SourceKey(source)] = source
	if _, err := library.InstallSource(cfg, "policy-source", library.InstallOptions{SkillFilter: "redis"}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeMCPZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := out.Close(); err != nil {
			t.Errorf("close zip output: %v", err)
		}
	}()
	writer := zip.NewWriter(out)
	defer func() {
		if err := writer.Close(); err != nil {
			t.Errorf("close zip writer: %v", err)
		}
	}()
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandleProtocolMethods(t *testing.T) {
	cfg := testConfig(t)
	id := json.RawMessage(`1`)
	initResp := handle(cfg, request{ID: id, Method: "initialize"})
	if initResp.Error != nil || initResp.Result == nil {
		t.Fatalf("initialize response = %#v", initResp)
	}
	serverInfo := initResp.Result.(map[string]any)["serverInfo"].(map[string]any)
	if serverInfo["version"] != "dev" {
		t.Fatalf("server version = %#v", serverInfo["version"])
	}
	toolsResp := handle(cfg, request{ID: id, Method: "tools/list"})
	data, _ := json.Marshal(toolsResp.Result)
	if !strings.Contains(string(data), "upsert_skill_overlay_file") {
		t.Fatalf("tools response = %s", data)
	}
	for _, want := range []string{"list_sources", "find_source_skills", "check_skill_updates"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("tools response missing %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), "set_skill_update_policy") {
		t.Fatalf("tools response includes removed update policy tool: %s", data)
	}
	if !strings.Contains(string(data), "create_skill_draft") || !strings.Contains(string(data), "render_skill_draft") {
		t.Fatalf("tools response missing draft tools: %s", data)
	}
	if !strings.Contains(string(data), `"skill_id"`) || !strings.Contains(string(data), `"properties"`) {
		t.Fatalf("tools response missing input schema properties: %s", data)
	}
	missing := handle(cfg, request{ID: id, Method: "missing"})
	if missing.Error == nil || missing.Error.Code != -32601 {
		t.Fatalf("missing response = %#v", missing)
	}
	bad := handle(cfg, request{ID: id, Method: "tools/call", Params: json.RawMessage(`{bad`)})
	if bad.Error == nil || bad.Error.Code != -32602 {
		t.Fatalf("bad call response = %#v", bad)
	}
	unknownParams, _ := json.Marshal(callParams{Name: "missing", Arguments: map[string]any{}})
	unknown := handle(cfg, request{ID: id, Method: "tools/call", Params: unknownParams})
	if result, ok := unknown.Result.(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("unknown tool response = %#v", unknown)
	}
	successParams, _ := json.Marshal(callParams{Name: "find_skills", Arguments: map[string]any{"query": "cache"}})
	success := handle(cfg, request{ID: id, Method: "tools/call", Params: successParams})
	if success.Error != nil {
		t.Fatalf("successful tool response = %#v", success)
	}
	result, ok := success.Result.(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("successful tool result = %#v", success.Result)
	}
}

func TestMCPRegistryDescriptionAndGeneratedInstructions(t *testing.T) {
	described, err := callTool(testConfig(t), "describe_tool", map[string]any{"name": "promote_skill_draft"})
	if err != nil {
		t.Fatal(err)
	}
	text := mustJSON(described)
	for _, want := range []string{`"inputSchema"`, `"accepted_parameter_names"`, `"to_source"`, `"additionalProperties":false`} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe_tool missing %q: %s", want, text)
		}
	}

	instructions := serverInstructions()
	for _, definition := range toolList() {
		name := definition["name"].(string)
		if !strings.Contains(instructions, "`"+name+"`") {
			t.Fatalf("generated instructions missing registered tool %s", name)
		}
	}
	if strings.Contains(instructions, "`upsert_skill_file`") {
		t.Fatalf("generated instructions contain unregistered tool: %s", instructions)
	}
	for _, want := range []string{
		"Preview convention", "`sync_skills`", "`dry_run`",
		"Successful installed-skill calls to `read_skill` and `read_skill_file` each record one retrieval",
		"Retrieval is not use", "`record_skill_use` only after materially applying",
		"`sentiment`, `situation`, `details`", "concrete `proposed_change`",
		"remain in Upskill's local activity log",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("generated instructions missing %q: %s", want, instructions)
		}
	}

	initialized := handle(testConfig(t), request{ID: json.RawMessage(`1`), Method: "initialize"})
	result := initialized.Result.(map[string]any)
	if result["instructions"] != instructions {
		t.Fatalf("initialize instructions are not registry-generated")
	}
}

func TestSkillActivityDocumentationContract(t *testing.T) {
	for _, path := range []string{
		"../../README.md",
		"../../SPEC.md",
		"../../docs/getting-started.md",
		"../../docs/workflows.md",
		"../../prompts/AGENTS.md",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		for _, want := range []string{"--nocontent", "get_skill_stats", "record_skill_use", "record_skill_feedback"} {
			if !strings.Contains(content, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
		if strings.Contains(content, "--no-content") {
			t.Fatalf("%s uses incorrect --no-content spelling", path)
		}
		normalized := strings.Join(strings.Fields(content), " ")
		distinguishesUse := strings.Contains(normalized, "retrieval is not a use") ||
			strings.Contains(normalized, "Retrieval is not a use") ||
			strings.Contains(normalized, "Retrieval does not mean") ||
			strings.Contains(normalized, "retrieval also does not mean") ||
			strings.Contains(normalized, "Retrieval never implies use") ||
			strings.Contains(normalized, "separate events")
		if !distinguishesUse {
			t.Fatalf("%s does not distinguish retrieval from use", path)
		}
	}
}

func TestAgentPromptRequiresSkillActivityGuidance(t *testing.T) {
	data, err := os.ReadFile("../../prompts/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	prompt := strings.Join(strings.Fields(string(data)), " ")
	for _, want := range []string{
		"## Recording Skill Activity",
		"A retrieval is not a use",
		"only after materially",
		"every required field",
		"what went right or wrong",
		"specific `proposed_change`",
		"local activity log",
		"Never include secrets",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("agent prompt missing %q", want)
		}
	}
}

func TestAgentPromptRequiresCurrentToolAndPreflightGuidance(t *testing.T) {
	data, err := os.ReadFile("../../prompts/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	prompt := strings.Join(strings.Fields(string(data)), " ")
	for _, want := range []string{
		"## Discovering Tool Contracts",
		"`tools/list`",
		"`describe_tool`",
		"`inputSchema`",
		"`accepted_parameter_names`",
		"Do not guess mutation parameters",
		"`dry_run: true`",
		"`dry_run: false`",
		"Read or list the destination again",
		"explicit draft read tools",
		"`retrieval_recorded: false`",
		"`activity_warnings`",
		"explicit, authoritative live comparison",
		"Manual Git sources are cache-only",
		"means unknown, not current",
		"Automatic Git sources reuse fresh cache results",
		"`find_source_skills` remain live",
		"suppress only implicit preflight",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("agent prompt missing %q", want)
		}
	}
}

func TestAgentGuidanceDistributionContract(t *testing.T) {
	for _, path := range []string{"../../README.md", "../../SPEC.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := strings.Join(strings.Fields(string(data)), " ")
		for _, want := range []string{
			"always-on bootstrap and safety",
			"complement",
			"not replace",
			"one maintained source or contract-test",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("%s missing agent-guidance strategy %q", path, want)
			}
		}
	}
}

func TestCallableToolDocumentationMarkers(t *testing.T) {
	for name, content := range map[string]string{
		"valid":   "<!-- upskill-callable-tools: find_skills, read_skill -->",
		"ignored": "<!-- upskill-callable-tools: find_skills, old_tool -->\n<!-- upskill-callable-tools-ignore: old_tool -->",
	} {
		t.Run(name, func(t *testing.T) {
			if err := lintCallableToolMarkers(content); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := lintCallableToolMarkers("<!-- upskill-callable-tools: upsert_skill_file -->"); err == nil || !strings.Contains(err.Error(), "upsert_skill_file") {
		t.Fatalf("stale callable marker error = %v", err)
	}

	for _, path := range []string{
		"../../README.md",
		"../../prompts/AGENTS.md",
		"../../docs/getting-started.md",
		"../../docs/workflows.md",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := lintCallableToolMarkers(string(content)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}

	prompt, err := os.ReadFile("../../prompts/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string]bool{}
	for _, match := range callableToolsMarker.FindAllStringSubmatch(string(prompt), -1) {
		if match[1] != "" {
			continue
		}
		for _, name := range strings.Split(match[2], ",") {
			marked[strings.TrimSpace(name)] = true
		}
	}
	for _, name := range toolNames() {
		if !marked[name] {
			t.Fatalf("agent prompt callable marker missing registered tool %s", name)
		}
	}
}

func TestMCPRegistryDefinitionsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range toolList() {
		name, nameOK := definition["name"].(string)
		description, descriptionOK := definition["description"].(string)
		schema, schemaOK := definition["inputSchema"].(map[string]any)
		if !nameOK || strings.TrimSpace(name) == "" {
			t.Fatalf("tool has no name: %#v", definition)
		}
		if seen[name] {
			t.Fatalf("duplicate tool name: %s", name)
		}
		seen[name] = true
		if !descriptionOK || strings.TrimSpace(description) == "" {
			t.Fatalf("tool %s has no description", name)
		}
		if !schemaOK || schema["type"] != "object" {
			t.Fatalf("tool %s has invalid object schema: %#v", name, schema)
		}
		if _, ok := schema["properties"].(map[string]any); !ok {
			t.Fatalf("tool %s has invalid properties: %#v", name, schema["properties"])
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("tool %s must reject unknown parameters: %#v", name, schema)
		}
	}
}

func TestMCPDescribeToolMatchesEveryRegistryDefinition(t *testing.T) {
	cfg := testConfig(t)
	for _, definition := range toolList() {
		name := definition["name"].(string)
		t.Run(name, func(t *testing.T) {
			described, err := callTool(cfg, "describe_tool", map[string]any{"name": name})
			if err != nil {
				t.Fatal(err)
			}
			payload := described.(map[string]any)
			if mustJSON(payload["inputSchema"]) != mustJSON(definition["inputSchema"]) {
				t.Fatalf("described schema drift for %s\nwant: %s\n got: %s", name, mustJSON(definition["inputSchema"]), mustJSON(payload["inputSchema"]))
			}
			if mustJSON(payload["accepted_parameter_names"]) != mustJSON(acceptedParameterNames(definition)) {
				t.Fatalf("accepted names drift for %s: %#v", name, payload)
			}
		})
	}
}

func TestMCPRegistryArgumentValidationCases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tool    string
		args    map[string]any
		wantErr string
	}{
		{name: "required", tool: "read_skill", args: map[string]any{}, wantErr: `parameter "skill_id" is required`},
		{name: "wrong type", tool: "find_skills", args: map[string]any{"limit": "ten"}, wantErr: `parameter "limit" must be integer`},
		{name: "one of missing", tool: "upsert_skill_draft_file", args: map[string]any{"draft_id": "draft:redis", "path": "SKILL.md"}, wantErr: `exactly one of "content" or "base64_content" is required`},
		{name: "one of duplicate", tool: "upsert_skill_draft_file", args: map[string]any{"draft_id": "draft:redis", "path": "SKILL.md", "content": "a", "base64_content": "Yg=="}, wantErr: `use only one of "content" or "base64_content"`},
		{name: "any of", tool: "render_skill_draft", args: map[string]any{}, wantErr: `one of "draft_id" or "skill_id" or "skill_name" is required`},
		{name: "unknown", tool: "find_skills", args: map[string]any{"bogus": true}, wantErr: `unknown parameter "bogus"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolArguments(tc.tool, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateToolArguments(%s, %#v) error = %v, want %q", tc.tool, tc.args, err, tc.wantErr)
			}
		})
	}
}

func TestMCPUnknownParametersAvoidWeakSuggestionsAndValues(t *testing.T) {
	cfg := testConfig(t)
	params, _ := json.Marshal(callParams{Name: "find_skills", Arguments: map[string]any{
		"completely_unrelated_parameter": "do-not-echo-this-value",
	}})
	resp := handle(cfg, request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if _, ok := structured["did_you_mean"]; ok {
		t.Fatalf("weak parameter suggestion returned: %#v", structured)
	}
	if strings.Contains(mustJSON(structured), "do-not-echo-this-value") {
		t.Fatalf("validation error exposed argument value: %#v", structured)
	}

	params, _ = json.Marshal(callParams{Name: "find_skills", Arguments: map[string]any{
		"limit": "another-secret-value",
	}})
	resp = handle(cfg, request{ID: json.RawMessage(`2`), Method: "tools/call", Params: params})
	result = resp.Result.(map[string]any)
	if strings.Contains(mustJSON(result["structuredContent"]), "another-secret-value") {
		t.Fatalf("type error exposed argument value: %#v", result)
	}
}

func TestMCPUnknownToolErrorSuggestsRegisteredTool(t *testing.T) {
	params, _ := json.Marshal(callParams{Name: "describe_too", Arguments: map[string]any{}})
	resp := handle(testConfig(t), request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["error"] != "unknown_tool" || structured["schema_version"] != 1 {
		t.Fatalf("unknown tool payload = %#v", structured)
	}
	if structured["did_you_mean"].(map[string]string)["describe_too"] != "describe_tool" {
		t.Fatalf("unknown tool suggestion = %#v", structured)
	}
	if !strings.Contains(mustJSON(structured["available_tool_names"]), "describe_tool") {
		t.Fatalf("available names missing describe_tool: %#v", structured)
	}
}

func TestMCPStdioRegistryAndValidationLifecycle(t *testing.T) {
	var input bytes.Buffer
	for _, message := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": callParams{Name: "describe_tool", Arguments: map[string]any{"name": "find_skills"}}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": callParams{Name: "find_skills", Arguments: map[string]any{"bogus": true}}},
		{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": callParams{Name: "find_skills", Arguments: map[string]any{"query": "cache"}}},
	} {
		input.Write(EncodeForTest(message))
	}
	var output bytes.Buffer
	if err := Serve(testConfig(t), &input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	responses := make([]string, 0, 5)
	for index := 0; index < 5; index++ {
		frame, err := readMessage(reader)
		if err != nil {
			t.Fatalf("read response %d: %v", index+1, err)
		}
		responses = append(responses, string(frame))
		var response map[string]any
		if err := json.Unmarshal(frame, &response); err != nil {
			t.Fatalf("response %d is not JSON: %v\n%s", index+1, err, frame)
		}
		if response["id"] != float64(index+1) {
			t.Fatalf("response %d id = %#v", index+1, response["id"])
		}
	}
	if _, err := readMessage(reader); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected extra stdio response: %v", err)
	}
	text := strings.Join(responses, "\n")
	for _, want := range []string{"Upskill MCP tools", `"tools"`, `"accepted_parameter_names"`, `"invalid_tool_arguments"`, "cache"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdio lifecycle missing %q in %s", want, text)
		}
	}
}

func TestMCPToolErrorsIncludeSchemaAndUnknownKeySuggestion(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(callParams{Name: "promote_skill_draft", Arguments: map[string]any{
		"draft_id":    "draft:redis",
		"destination": "Obsidian",
	}})
	resp := handle(cfg, request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	result := resp.Result.(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["message"] != `unknown parameter "destination"; did you mean "to_source"?` {
		t.Fatalf("unknown parameter message = %#v", structured["message"])
	}
	if structured["schema_version"] != 1 {
		t.Fatalf("error schema version = %#v", structured["schema_version"])
	}
	if path, ok := structured["validation_path"].([]string); !ok || len(path) != 0 {
		t.Fatalf("validation path = %#v", structured["validation_path"])
	}
	if structured["did_you_mean"].(map[string]string)["destination"] != "to_source" ||
		!strings.Contains(mustJSON(structured["inputSchema"]), "to_source") {
		t.Fatalf("unknown parameter payload = %#v", structured)
	}

	params, _ = json.Marshal(callParams{Name: "promote_skill_draft", Arguments: map[string]any{
		"draft_id": "draft:redis",
	}})
	resp = handle(cfg, request{ID: json.RawMessage(`2`), Method: "tools/call", Params: params})
	result = resp.Result.(map[string]any)
	structured = result["structuredContent"].(map[string]any)
	if structured["error"] != "invalid_tool_arguments" ||
		structured["message"] != `exactly one of "to_path" or "to_source" is required` ||
		!strings.Contains(mustJSON(structured["accepted_parameter_names"]), "to_source") ||
		!strings.Contains(mustJSON(structured["inputSchema"]), "to_path") {
		t.Fatalf("missing destination payload = %#v", structured)
	}
}

func TestMCPToolResultIncludesUpdateNotices(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "notice-source")
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	if _, err := library.InstallSource(cfg, source, library.InstallOptions{SkillFilter: "redis"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")

	id := json.RawMessage(`1`)
	params, _ := json.Marshal(callParams{Name: "read_skill", Arguments: map[string]any{
		"skill_id":      "redis",
		"rendered":      false,
		"include_files": false,
	}})
	resp := handle(cfg, request{ID: id, Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("tool response = %#v", resp)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("tool result = %#v", resp.Result)
	}
	notices, ok := result["notices"].([]notice)
	if !ok || len(notices) != 1 || notices[0].Type != "update_available" || notices[0].SkillID != "redis" {
		t.Fatalf("result notices = %#v", result["notices"])
	}
	content, ok := result["content"].([]map[string]string)
	if !ok || len(content) != 1 {
		t.Fatalf("tool content = %#v", result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content[0]["text"]), &payload); err != nil {
		t.Fatalf("payload decode: %v\n%s", err, content[0]["text"])
	}
	if _, ok := payload["notices"]; ok {
		t.Fatalf("payload should be unchanged by wrapper notices: %#v", payload)
	}

	params, _ = json.Marshal(callParams{Name: "find_skills", Arguments: map[string]any{"query": "redis"}})
	resp = handle(cfg, request{ID: id, Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("array tool response = %#v", resp)
	}
	result = resp.Result.(map[string]any)
	notices, ok = result["notices"].([]notice)
	if !ok || len(notices) != 1 || notices[0].Type != "update_available" {
		t.Fatalf("array result notices = %#v", result["notices"])
	}
}

func TestMCPExplicitGitCheckDoesNotRepeatLiveWorkForNotices(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	runMCPGit(t, "", "init", "--bare", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runMCPGit(t, work, "init")
	writeFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	runMCPGit(t, work, "add", ".")
	runMCPGit(t, work, "commit", "-m", "old")
	runMCPGit(t, work, "branch", "-M", "main")
	runMCPGit(t, work, "remote", "add", "origin", remote)
	runMCPGit(t, work, "push", "-u", "origin", "main")
	runMCPGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	cfg := testConfig(t)
	source := "file://" + remote
	if _, err := library.InstallSource(cfg, source, library.InstallOptions{
		Ref:          "main",
		Subpath:      "skills",
		SkillFilter:  "redis",
		UpdatePolicy: config.UpdatePolicyManual,
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")
	runMCPGit(t, work, "add", ".")
	runMCPGit(t, work, "commit", "-m", "new")
	runMCPGit(t, work, "push", "origin", "main")
	countPath := installMCPGitCounter(t)

	if err := os.RemoveAll(filepath.Join(cfg.Home, "cache", "update-checks")); err != nil {
		t.Fatal(err)
	}
	readParams, _ := json.Marshal(callParams{Name: "read_skill", Arguments: map[string]any{
		"skill_id":      "redis",
		"rendered":      false,
		"include_files": false,
	}})
	readResp := handle(cfg, request{ID: json.RawMessage(`1`), Method: "tools/call", Params: readParams})
	if readResp.Error != nil {
		t.Fatalf("manual cache-miss response = %#v", readResp)
	}
	readResult := readResp.Result.(map[string]any)
	if notices, ok := readResult["notices"].([]notice); ok && len(notices) != 0 {
		t.Fatalf("manual cache miss notices = %#v", notices)
	}
	if counts := mcpGitOperationCounts(t, countPath); len(counts) != 0 {
		t.Fatalf("manual MCP cache miss operations = %#v", counts)
	}
	if err := os.Remove(countPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	params, _ := json.Marshal(callParams{Name: "check_skill_updates", Arguments: map[string]any{
		"skill_id": "redis",
	}})
	resp := handle(cfg, request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("tool response = %#v", resp)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("tool result = %#v", resp.Result)
	}
	if counts := mcpGitOperationCounts(t, countPath); counts["clone"] != 1 || counts["ls-remote"] != 0 {
		t.Fatalf("explicit check plus notices operations = %#v", counts)
	}
	notices, ok := result["notices"].([]notice)
	if !ok || len(notices) != 1 || notices[0].Type != "update_available" || notices[0].SkillID != "redis" {
		t.Fatalf("notices = %#v", result["notices"])
	}
	content := result["content"].([]map[string]string)
	var payload struct {
		Checks []library.UpdateCheck `json:"checks"`
	}
	if err := json.Unmarshal([]byte(content[0]["text"]), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Checks) != 1 || !payload.Checks[0].Changed || payload.Checks[0].CheckOrigin != library.UpdateCheckOriginLiveGit {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestMCPRemotePreflightFailureWarnsWithoutBlockingToolResult(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	runMCPGit(t, "", "init", "--bare", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runMCPGit(t, work, "init")
	writeFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	runMCPGit(t, work, "add", ".")
	runMCPGit(t, work, "commit", "-m", "old")
	runMCPGit(t, work, "branch", "-M", "main")
	runMCPGit(t, work, "remote", "add", "origin", remote)
	runMCPGit(t, work, "push", "-u", "origin", "main")
	runMCPGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	cfg := testConfig(t)
	cfg.PreflightCheckTTL = 0
	if _, err := library.InstallSource(cfg, "file://"+remote, library.InstallOptions{
		Ref:          "main",
		Subpath:      "skills",
		SkillFilter:  "redis",
		UpdatePolicy: config.UpdatePolicyAuto,
	}); err != nil {
		t.Fatal(err)
	}
	countPath := installMCPGitCounter(t, "exit 1")
	params, _ := json.Marshal(callParams{Name: "read_skill", Arguments: map[string]any{
		"skill_id":      "redis",
		"rendered":      false,
		"include_files": false,
	}})

	for iteration := 0; iteration < 2; iteration++ {
		if err := os.Remove(countPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		resp := handle(cfg, request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
		if resp.Error != nil {
			t.Fatalf("tool response = %#v", resp)
		}
		result, ok := resp.Result.(map[string]any)
		if !ok || result["isError"] != false {
			t.Fatalf("tool result = %#v", resp.Result)
		}
		notices, ok := result["notices"].([]notice)
		if !ok || len(notices) != 1 ||
			notices[0].Type != library.UpdateCheckWarningRemoteDeferred ||
			notices[0].SourceKey == "" ||
			!notices[0].Stale {
			t.Fatalf("notices = %#v", result["notices"])
		}
		content := result["content"].([]map[string]string)
		if !strings.Contains(content[0]["text"], "Old.") {
			t.Fatalf("primary tool result missing: %#v", content)
		}
		counts := mcpGitOperationCounts(t, countPath)
		if iteration == 0 && (counts["ls-remote"] != 1 || counts["clone"] != 0) {
			t.Fatalf("first deferred operations = %#v", counts)
		}
		if iteration == 1 && len(counts) != 0 {
			t.Fatalf("backoff operations = %#v", counts)
		}
	}

	if err := os.Rename(remote, remote+".offline"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(countPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	checkParams, _ := json.Marshal(callParams{Name: "check_skill_updates", Arguments: map[string]any{
		"skill_id": "redis",
	}})
	checkResp := handle(cfg, request{ID: json.RawMessage(`2`), Method: "tools/call", Params: checkParams})
	if checkResp.Error != nil {
		t.Fatalf("explicit offline response = %#v", checkResp)
	}
	checkResult := checkResp.Result.(map[string]any)
	content := checkResult["content"].([]map[string]string)
	var payload struct {
		FailedCount int                   `json:"failed_count"`
		Checks      []library.UpdateCheck `json:"checks"`
	}
	if err := json.Unmarshal([]byte(content[0]["text"]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.FailedCount != 1 || len(payload.Checks) != 1 || payload.Checks[0].Error == "" {
		t.Fatalf("explicit offline payload = %#v", payload)
	}
	if counts := mcpGitOperationCounts(t, countPath); counts["clone"] != 1 || counts["ls-remote"] != 0 {
		t.Fatalf("explicit offline operations = %#v", counts)
	}
}

func TestMCPListSources(t *testing.T) {
	cfg := testConfig(t)
	source := config.NormalizeSource(config.Source{
		Name:                "team",
		Source:              filepath.Join(cfg.Home, "team-skills"),
		Ref:                 "main",
		Subpath:             "skills",
		DefaultUpdatePolicy: config.UpdatePolicyAuto,
	})
	cfg.Sources[config.SourceKey(source)] = source

	listed, err := callTool(cfg, "list_sources", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := listed.([]sourceRow)
	if !ok || len(rows) != 1 {
		t.Fatalf("listed = %#v", listed)
	}
	if rows[0].ID == "" ||
		rows[0].Name != "team" ||
		rows[0].DisplayName != "team" ||
		rows[0].Ref != "main" ||
		rows[0].Subpath != "skills" ||
		rows[0].DefaultUpdatePolicy != config.UpdatePolicyAuto ||
		rows[0].Scope != config.SourceScopePersonal ||
		rows[0].WritePolicy != config.WritePolicyDirectAllowed ||
		rows[0].RecommendedWorkflow != "direct_source_edit" {
		t.Fatalf("source row = %#v", rows[0])
	}
}

func TestMCPSourceIdentifiersResolveByNameIDAndPath(t *testing.T) {
	cfg := testConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "obsidian-skills")
	writeFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nSource.\n")
	source := config.NormalizeSource(config.Source{Name: "Obsidian", Source: sourceRoot})
	cfg.Sources[config.SourceKey(source)] = source

	identifiers := []string{"Obsidian", "obsidian", source.ID, sourceRoot, filepath.Join(sourceRoot, ".")}
	for _, identifier := range identifiers {
		found, err := callTool(cfg, "find_source_skills", map[string]any{"source": identifier})
		if err != nil {
			t.Fatalf("find source by %q: %v", identifier, err)
		}
		payload := found.(map[string]any)
		if payload["total"] != 1 {
			t.Fatalf("find source by %q = %#v", identifier, payload)
		}
	}

	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
	for _, identifier := range identifiers {
		promoted, err := callTool(cfg, "promote_skill_draft", map[string]any{
			"draft_id":  "draft:redis",
			"to_source": identifier,
			"dry_run":   true,
		})
		if err != nil {
			t.Fatalf("promote by %q: %v", identifier, err)
		}
		payload := promoted.(map[string]any)
		if payload["source"] != "Obsidian" ||
			payload["source_identifier"] != identifier ||
			payload["source_id"] != source.ID ||
			payload["source_display_name"] != "Obsidian" ||
			payload["resolved_path"] != sourceRoot {
			t.Fatalf("promotion source resolution by %q = %#v", identifier, payload)
		}

		edited, err := callTool(cfg, "upsert_source_skill_file", map[string]any{
			"source":   identifier,
			"skill_id": "redis",
			"path":     "SKILL.md",
			"content":  "Preview only.\n",
			"dry_run":  true,
		})
		if err != nil {
			t.Fatalf("source edit by %q: %v", identifier, err)
		}
		editPayload := edited.(library.SourceSkillFileWriteResult)
		if editPayload.SourceID != source.ID || editPayload.SourceDisplayName != "Obsidian" || editPayload.ResolvedPath != sourceRoot {
			t.Fatalf("source edit resolution by %q = %#v", identifier, editPayload)
		}
	}
}

func TestMCPFindSourceSkillsDoesNotInstall(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "live-source")
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew source copy.\n")
	writeFile(t, filepath.Join(source, "docs", "SKILL.md"), "---\nname: Docs\n---\n\nDocs.\n")

	found, err := callTool(cfg, "find_source_skills", map[string]any{"source": source, "query": "", "limit": float64(10)})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := found.(map[string]any)
	if !ok {
		t.Fatalf("found = %#v", found)
	}
	skills, ok := payload["skills"].([]library.SourceSkill)
	if !ok || len(skills) != 2 {
		t.Fatalf("skills = %#v", payload["skills"])
	}
	if !strings.Contains(mustJSON(payload["source_policy"]), config.WritePolicyDirectAllowed) ||
		payload["recommended_workflow"] != "direct_source_edit" {
		t.Fatalf("source policy missing from source search: %#v", payload)
	}
	if got := readFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); strings.Contains(got, "New source copy.") {
		t.Fatalf("source inspection mutated installed store:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.StoreDir, "docs")); !os.IsNotExist(err) {
		t.Fatalf("source inspection installed new skill: %v", err)
	}

	found, err = callTool(cfg, "find_source_skills", map[string]any{"source": source, "query": "docs"})
	if err != nil {
		t.Fatal(err)
	}
	payload = found.(map[string]any)
	skills = payload["skills"].([]library.SourceSkill)
	if len(skills) != 1 || skills[0].StoreName != "docs" {
		t.Fatalf("filtered skills = %#v", skills)
	}
}

func TestMCPDoesNotExposeSourceInstallTool(t *testing.T) {
	for _, tool := range toolList() {
		name, _ := tool["name"].(string)
		if strings.Contains(name, "install") || name == "add_skill" || name == "add_source_skill" {
			t.Fatalf("unexpected MCP install-capable tool exposed: %s", name)
		}
	}
	if _, err := callTool(testConfig(t), "install_skill", map[string]any{}); err == nil {
		t.Fatal("install_skill unexpectedly succeeded")
	}
}

func TestMCPInstalledSkillPolicyResults(t *testing.T) {
	cfg := configWithInstalledPolicySource(t, config.SourceScopeTeam, config.WritePolicyDraftPromote)
	found, err := callTool(cfg, "find_skills", map[string]any{"query": "redis", "limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	text := mustJSON(found)
	if !strings.Contains(text, `"source_policy"`) ||
		!strings.Contains(text, config.WritePolicyDraftPromote) ||
		!strings.Contains(text, `"recommended_workflow":"draft_then_promote"`) {
		t.Fatalf("find_skills policy = %s", text)
	}
	read, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "include_files": false})
	if err != nil {
		t.Fatal(err)
	}
	text = mustJSON(read)
	if !strings.Contains(text, `"source_policy"`) ||
		!strings.Contains(text, config.SourceScopeTeam) ||
		!strings.Contains(text, config.WritePolicyDraftPromote) {
		t.Fatalf("read_skill policy = %s", text)
	}
}

func TestMCPSourcePolicyToolSchema(t *testing.T) {
	var sourceEdit map[string]any
	for _, tool := range toolList() {
		if tool["name"] == "upsert_source_skill_file" {
			sourceEdit = tool
			break
		}
	}
	if sourceEdit == nil {
		t.Fatal("upsert_source_skill_file tool missing")
	}
	text := mustJSON(sourceEdit)
	for _, want := range []string{"dry_run", "confirm_source_write", "reason", "source edit instead of an overlay", "never writes store"} {
		if !strings.Contains(text, want) {
			t.Fatalf("schema missing %q: %s", want, text)
		}
	}
}

func TestMCPCheckSkillUpdates(t *testing.T) {
	cfg := testConfig(t)
	source := filepath.Join(cfg.Home, "check-source")
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	if _, err := library.InstallSource(cfg, source, library.InstallOptions{SkillFilter: "redis", UpdatePolicy: config.UpdatePolicyAuto}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")

	checked, err := callTool(cfg, "check_skill_updates", map[string]any{"skill_id": "redis", "include_diff": true})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := checked.(map[string]any)
	if !ok {
		t.Fatalf("checked = %#v", checked)
	}
	if payload["checked_count"] != 1 || payload["changed_count"] != 1 || payload["failed_count"] != 0 || payload["base_mutated"] != false {
		t.Fatalf("payload = %#v", payload)
	}
	checks, ok := payload["checks"].([]library.UpdateCheck)
	if !ok || len(checks) != 1 || !checks[0].Changed || checks[0].UpdatePolicy != config.UpdatePolicyAuto || len(checks[0].Diff) == 0 {
		t.Fatalf("checks = %#v", payload["checks"])
	}
	diff := strings.Join(checks[0].Diff, "\n")
	for _, want := range []string{
		"diff --git a/SKILL.md b/SKILL.md",
		"--- a/SKILL.md",
		"+++ b/SKILL.md",
		"@@ -2,4 +2,4 @@",
		"-Old.",
		"+New.",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("check diff missing %q: %#v", want, checks[0].Diff)
		}
	}
	for index, line := range checks[0].Diff {
		if strings.Contains(line, "\n") {
			t.Fatalf("check diff line %d contains embedded newline: %q", index, line)
		}
	}
	if got := readFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md")); strings.Contains(got, "New.") {
		t.Fatalf("check mutated installed store:\n%s", got)
	}

	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	checked, err = callTool(cfg, "check_skill_updates", map[string]any{"skill_ids": []any{"redis"}})
	if err != nil {
		t.Fatal(err)
	}
	payload = checked.(map[string]any)
	if payload["failed_count"] != 1 {
		t.Fatalf("failed payload = %#v", payload)
	}
	checks = payload["checks"].([]library.UpdateCheck)
	if checks[0].Error == "" {
		t.Fatalf("failed check missing error: %#v", checks[0])
	}
}

func TestMCPSetSkillUpdatePolicyRemoved(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "set_skill_update_policy", map[string]any{"skill_id": "redis", "policy": "auto"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("removed update policy tool err = %v", err)
	}
}

func TestToolCallsReadAndOverlayOnlyWrites(t *testing.T) {
	cfg := testConfig(t)
	found, err := callTool(cfg, "find_skills", map[string]any{"query": "cache", "limit": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	foundEntries := found.([]map[string]any)
	if len(foundEntries) != 1 || foundEntries[0]["kind"] != "base" {
		t.Fatalf("found = %#v", found)
	}
	base, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "rendered": false, "include_files": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(base), "Base.") {
		t.Fatalf("base = %#v", base)
	}
	files, err := callTool(cfg, "list_skill_files", map[string]any{"skill_id": "redis", "rendered": false, "include_hashes": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(files), "references/guide.md") {
		t.Fatalf("files = %#v", files)
	}
	file, err := callTool(cfg, "read_skill_file", map[string]any{"skill_id": "redis", "path": "references/guide.md", "rendered": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(file), "Guide.") {
		t.Fatalf("file = %#v", file)
	}

	upsert, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id":  "redis",
		"path":      "SKILL.md",
		"content":   "Overlay.\n",
		"agent":     "codex",
		"operation": "append",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(upsert), "base_mutated") {
		t.Fatalf("upsert = %#v", upsert)
	}
	baseBytes, _ := os.ReadFile(filepath.Join(cfg.StoreDir, "redis", "SKILL.md"))
	if strings.Contains(string(baseBytes), "Overlay.") {
		t.Fatalf("base was mutated: %s", baseBytes)
	}
	rendered, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rendered), "Overlay.") {
		t.Fatalf("rendered = %#v", rendered)
	}
	if _, err := callTool(cfg, "render_skill", map[string]any{"skill_id": "redis", "agent": "codex", "copy": false}); err != nil {
		t.Fatal(err)
	}
	synced, err := callTool(cfg, "sync_skills", map[string]any{"agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(synced), `"updated":[]`) || !strings.Contains(mustJSON(synced), `"rendered_count":1`) {
		t.Fatalf("synced = %#v", synced)
	}
	overlays, err := callTool(cfg, "list_skill_overlays", map[string]any{"skill_id": "redis", "agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(overlays), "codex") {
		t.Fatalf("overlays = %#v", overlays)
	}
	deleted, err := callTool(cfg, "delete_skill_overlay_file", map[string]any{"skill_id": "redis", "path": "SKILL.md", "agent": "codex", "operation": "append"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(deleted), "deleted") {
		t.Fatalf("deleted = %#v", deleted)
	}
}

func TestMCPSyncWarnsAboutUnmanagedAgentOutputSkill(t *testing.T) {
	cfg := testConfig(t)
	writeFile(t, filepath.Join(cfg.AgentOutputs[0].Path, "gitnexus-repo-redis-sre-agent", "SKILL.md"), "# GitNexus\n")

	synced, err := callTool(cfg, "sync_skills", map[string]any{"agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	text := mustJSON(synced)
	if !strings.Contains(text, `"warnings"`) ||
		!strings.Contains(text, "gitnexus-repo-redis-sre-agent") ||
		!strings.Contains(text, "not installed in Upskill from any configured source") {
		t.Fatalf("sync warnings missing unmanaged skill context: %s", text)
	}
}

func TestMCPSyncDryRunPreviewsWithoutWriting(t *testing.T) {
	cfg := testConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "sync-preview-source")
	writeFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n")
	if _, err := library.InstallSource(cfg, sourceRoot, library.InstallOptions{SkillFilter: "redis"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n")

	previewed, err := callTool(cfg, "sync_skills", map[string]any{"agent": "codex", "dry_run": true})
	if err != nil {
		t.Fatal(err)
	}
	preview := previewed.(syncSkillsResult)
	if !preview.DryRun || preview.Applied || preview.PlannedUpdateCount != 1 || preview.PlannedRenderedCount != 1 {
		t.Fatalf("sync preview = %#v", preview)
	}
	if strings.Contains(readFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md")), "New.") {
		t.Fatal("sync preview updated the installed skill")
	}
	if _, err := os.Stat(filepath.Join(cfg.AgentOutputs[0].Path, "redis", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("sync preview copied agent output: %v", err)
	}

	applied, err := callTool(cfg, "sync_skills", map[string]any{"agent": "codex", "dry_run": false})
	if err != nil {
		t.Fatal(err)
	}
	applyResult := applied.(syncSkillsResult)
	if applyResult.DryRun || !applyResult.Applied || applyResult.UpdatedCount != 1 || applyResult.RenderedCount != 1 {
		t.Fatalf("sync apply = %#v", applyResult)
	}
	if !strings.Contains(readFile(t, filepath.Join(cfg.StoreDir, "redis", "SKILL.md")), "New.") {
		t.Fatal("sync apply did not update the installed skill")
	}
}

func TestMCPMutableFileDryRunsDoNotWriteOrDelete(t *testing.T) {
	cfg := testConfig(t)

	overlayPreview, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id": "redis",
		"path":     "SKILL.md",
		"content":  "Overlay.\n",
		"dry_run":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	overlayPayload := overlayPreview.(map[string]any)
	if overlayPayload["updated"] != false || overlayPayload["dry_run"] != true {
		t.Fatalf("overlay preview = %#v", overlayPayload)
	}
	if _, err := os.Stat(overlayPayload["path"].(string)); !os.IsNotExist(err) {
		t.Fatalf("overlay preview wrote target: %v", err)
	}
	if _, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id": "redis",
		"path":     "SKILL.md",
		"content":  "Overlay.\n",
	}); err != nil {
		t.Fatal(err)
	}
	overlayDelete, err := callTool(cfg, "delete_skill_overlay_file", map[string]any{
		"skill_id": "redis",
		"path":     "SKILL.md",
		"dry_run":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	deletePayload := overlayDelete.(map[string]any)
	if deletePayload["deleted"] != false {
		t.Fatalf("overlay delete preview = %#v", deletePayload)
	}
	if _, err := os.Stat(deletePayload["path"].(string)); err != nil {
		t.Fatalf("overlay delete preview removed target: %v", err)
	}

	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
	draftPreview, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{
		"draft_id": "draft:redis",
		"path":     "references/preview.md",
		"content":  "Preview.\n",
		"dry_run":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	draftPayload := draftPreview.(map[string]any)
	if draftPayload["updated"] != false || draftPayload["dry_run"] != true {
		t.Fatalf("draft file preview = %#v", draftPayload)
	}
	if _, err := os.Stat(draftPayload["target"].(string)); !os.IsNotExist(err) {
		t.Fatalf("draft file preview wrote target: %v", err)
	}
	draftDelete, err := callTool(cfg, "delete_skill_draft", map[string]any{
		"draft_id": "draft:redis",
		"dry_run":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	draftDeletePayload := draftDelete.(map[string]any)
	if draftDeletePayload["deleted"] != false || !draft.Exists(cfg, "redis") {
		t.Fatalf("draft delete preview = %#v", draftDeletePayload)
	}
}

func TestMCPMutationPreviewContractAndActionParity(t *testing.T) {
	cfg := testConfig(t)
	args := map[string]any{
		"skill_id": "redis",
		"path":     "SKILL.md",
		"content":  "Overlay.\n",
		"dry_run":  true,
	}
	preview := successfulToolPayload(t, cfg, "upsert_skill_overlay_file", args)
	if preview["dry_run"] != true || preview["applied"] != false {
		t.Fatalf("preview contract = %#v", preview)
	}
	if len(preview["warnings"].([]any)) != 0 {
		t.Fatalf("preview warnings = %#v", preview["warnings"])
	}
	resources := preview["canonical_resources"].(map[string]any)
	if resources["skill_id"] != "redis" {
		t.Fatalf("canonical resources = %#v", resources)
	}
	previewActions := preview["actions"].([]any)
	if len(previewActions) != 1 || previewActions[0].(map[string]any)["operation"] != "write" || previewActions[0].(map[string]any)["overwrite"] != false {
		t.Fatalf("preview actions = %#v", previewActions)
	}
	path := previewActions[0].(map[string]any)["target"].(string)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preview wrote %s: %v", path, err)
	}

	args["dry_run"] = false
	applied := successfulToolPayload(t, cfg, "upsert_skill_overlay_file", args)
	if applied["dry_run"] != false || applied["applied"] != true {
		t.Fatalf("apply contract = %#v", applied)
	}
	if !reflect.DeepEqual(preview["actions"], applied["actions"]) {
		t.Fatalf("preview/apply actions differ:\npreview=%#v\napply=%#v", preview["actions"], applied["actions"])
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("apply did not write %s: %v", path, err)
	}

	args["dry_run"] = true
	overwrite := successfulToolPayload(t, cfg, "upsert_skill_overlay_file", args)
	if overwrite["actions"].([]any)[0].(map[string]any)["overwrite"] != true {
		t.Fatalf("overwrite action = %#v", overwrite["actions"])
	}
	deleteArgs := map[string]any{"skill_id": "redis", "path": "SKILL.md", "dry_run": true}
	deletePreview := successfulToolPayload(t, cfg, "delete_skill_overlay_file", deleteArgs)
	deleteAction := deletePreview["actions"].([]any)[0].(map[string]any)
	if deleteAction["operation"] != "delete" || deleteAction["target"] != path {
		t.Fatalf("delete action = %#v", deleteAction)
	}
	deleteArgs["dry_run"] = false
	deleteApply := successfulToolPayload(t, cfg, "delete_skill_overlay_file", deleteArgs)
	if !reflect.DeepEqual(deletePreview["actions"], deleteApply["actions"]) {
		t.Fatalf("delete preview/apply actions differ: %#v %#v", deletePreview["actions"], deleteApply["actions"])
	}
}

func TestMutationContractCoversEveryPreviewCapableTool(t *testing.T) {
	cfg := testConfig(t)
	results := map[string]any{
		"sync_skills":                syncSkillsResult{DryRun: true, Applied: false},
		"upsert_source_skill_file":   library.SourceSkillFileWriteResult{DryRun: true, SourceID: "src_test", SkillID: "redis", AbsPath: "/tmp/source/SKILL.md"},
		"upsert_skill_overlay_file":  map[string]any{"dry_run": true, "skill_id": "redis", "path": "/tmp/overlay", "overwrite": false},
		"delete_skill_overlay_file":  map[string]any{"dry_run": true, "skill_id": "redis", "path": "/tmp/overlay"},
		"upsert_skill_draft_file":    map[string]any{"dry_run": true, "draft_id": "draft:redis", "target": "/tmp/draft/file", "overwrite": false},
		"delete_skill_draft_file":    map[string]any{"dry_run": true, "draft_id": "draft:redis", "target": "/tmp/draft/file"},
		"delete_skill_draft":         map[string]any{"dry_run": true, "draft_id": "draft:redis", "path": "/tmp/draft"},
		"import_skill_draft_archive": draft.ImportResult{DraftID: "draft:redis", DryRun: true, Files: []draft.ImportedFile{{Path: "SKILL.md"}}},
		"import_skill_draft_file":    draft.ImportResult{DraftID: "draft:redis", DryRun: true, Files: []draft.ImportedFile{{Path: "reference.md"}}},
		"promote_skill_draft":        draft.PromoteResult{DraftID: "draft:redis", DryRun: true, Destination: "/tmp/source/redis", Files: []draft.ImportedFile{{Path: "SKILL.md"}}},
	}
	for name, result := range results {
		t.Run(name, func(t *testing.T) {
			payload, ok := mutationResponse(cfg, name, map[string]any{"dry_run": true}, result).(map[string]any)
			if !ok {
				t.Fatalf("payload type = %T", result)
			}
			for _, field := range []string{"dry_run", "applied", "actions", "warnings", "canonical_resources"} {
				if _, ok := payload[field]; !ok {
					t.Fatalf("%s missing %s: %#v", name, field, payload)
				}
			}
		})
	}
}

func TestMutationActionsSortAndPartialFailureStatus(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	completed := filepath.Join(root, "a.md")
	if err := os.WriteFile(completed, []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := draft.PromoteResult{
		DraftID:     "draft:redis",
		Destination: root,
		DryRun:      false,
		Files: []draft.ImportedFile{
			{Path: "z.md"},
			{Path: "a.md"},
		},
	}
	payload := mutationResponse(cfg, "promote_skill_draft", map[string]any{"dry_run": false}, result)
	marked := markPartialActionStatuses(payload).(map[string]any)
	actions := marked["actions"].([]map[string]any)
	if actions[0]["target"] != completed || actions[0]["status"] != "completed" || actions[1]["status"] != "failed" {
		t.Fatalf("partial actions = %#v", actions)
	}
	if marked["applied"] != false {
		t.Fatalf("partial apply claimed success: %#v", marked)
	}
}

func TestMutationDefaultsRemainBackwardCompatible(t *testing.T) {
	cfg := testConfig(t)
	payload := successfulToolPayload(t, cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id": "redis",
		"path":     "SKILL.md",
		"content":  "Applied by legacy default.\n",
	})
	if payload["dry_run"] != false || payload["applied"] != true {
		t.Fatalf("legacy default changed: %#v", payload)
	}
	path := payload["actions"].([]any)[0].(map[string]any)["target"].(string)
	if !strings.Contains(readFile(t, path), "legacy default") {
		t.Fatalf("legacy default did not write %s", path)
	}
}

func TestMCPDraftWorkflow(t *testing.T) {
	cfg := testConfig(t)
	created, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(created), `"type":"version"`) {
		t.Fatalf("created = %#v", created)
	}

	found, err := callTool(cfg, "find_skills", map[string]any{"query": "draft:redis", "limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(found), `"kind":"draft"`) {
		t.Fatalf("found = %#v", found)
	}
	listed, err := callTool(cfg, "list_skill_drafts", map[string]any{"query": "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(listed), "draft:redis") {
		t.Fatalf("listed = %#v", listed)
	}
	read, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "draft:redis", "include_files": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(read), "Base.") || !strings.Contains(mustJSON(read), "references/guide.md") {
		t.Fatalf("read = %#v", read)
	}
	draftRead, err := callTool(cfg, "read_skill_draft", map[string]any{"draft_id": "draft:redis", "include_files": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(draftRead), `"metadata"`) {
		t.Fatalf("draft read = %#v", draftRead)
	}
	files, err := callTool(cfg, "list_skill_draft_files", map[string]any{"draft_id": "draft:redis", "include_hashes": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(files), "references/guide.md") {
		t.Fatalf("files = %#v", files)
	}
	file, err := callTool(cfg, "read_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "references/guide.md", "max_bytes": 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(file), `"content":"Gu"`) || !strings.Contains(mustJSON(file), `"size":7`) {
		t.Fatalf("file = %#v", file)
	}

	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{
		"draft_id":       "draft:redis",
		"path":           "references/new.md",
		"base64_content": base64.StdEncoding.EncodeToString([]byte("New resource.")),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{
		"draft_id": "draft:redis",
		"path":     "references/plain.md",
		"content":  "Plain resource.",
	}); err != nil {
		t.Fatal(err)
	}
	baseBytes, _ := os.ReadFile(filepath.Join(cfg.StoreDir, "redis", "references", "new.md"))
	if strings.Contains(string(baseBytes), "New resource.") {
		t.Fatalf("base was mutated: %s", baseBytes)
	}
	plain, err := callTool(cfg, "read_skill_file", map[string]any{"skill_id": "draft:redis", "path": "references/plain.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(plain), "Plain resource.") {
		t.Fatalf("plain = %#v", plain)
	}
	if _, err := callTool(cfg, "delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "references/plain.md"}); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(cfg.ImportsDir, "extra.txt"), "imported")
	importedFile, err := callTool(cfg, "import_skill_draft_file", map[string]any{
		"draft_id":    "draft:redis",
		"source_path": filepath.Join(cfg.ImportsDir, "extra.txt"),
		"path":        "references/imported.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(importedFile), `"imported":true`) {
		t.Fatalf("importedFile = %#v", importedFile)
	}
	archive := filepath.Join(cfg.ImportsDir, "bundle.zip")
	writeMCPZip(t, archive, map[string]string{
		"bundle/SKILL.md":             "---\nname: Redis\ndescription: imported\n---\n\nImported.\n",
		"bundle/references/zip.md":    "Zip resource.",
		"bundle/references/plain.txt": "Plain import.",
	})
	inspected, err := callTool(cfg, "import_skill_draft_archive", map[string]any{
		"draft_id":     "draft:redis",
		"archive_path": archive,
		"inspect":      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(inspected), `"imported":false`) {
		t.Fatalf("inspected = %#v", inspected)
	}
	if _, err := callTool(cfg, "import_skill_draft_archive", map[string]any{"draft_id": "draft:redis", "archive_path": archive}); err != nil {
		t.Fatal(err)
	}
	rendered, err := callTool(cfg, "render_skill_draft", map[string]any{"draft_id": "draft:redis", "agent": "codex", "copy": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rendered), `"mode":"draft"`) {
		t.Fatalf("rendered = %#v", rendered)
	}
	renderedAlias, err := callTool(cfg, "render_skill", map[string]any{"skill_id": "draft:redis", "apply_overlays": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(renderedAlias), `"skill_id":"draft:redis"`) {
		t.Fatalf("renderedAlias = %#v", renderedAlias)
	}
	diff, err := callTool(cfg, "diff_skill_draft", map[string]any{"draft_id": "draft:redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(diff), "references/imported.txt") {
		t.Fatalf("diff = %#v", diff)
	}
	rebase, err := callTool(cfg, "rebase_skill_draft", map[string]any{"draft_id": "draft:redis"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rebase), `"rebased":false`) {
		t.Fatalf("rebase = %#v", rebase)
	}
	promote, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": filepath.Join(t.TempDir(), "repo")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(promote), `"dry_run":true`) {
		t.Fatalf("promote = %#v", promote)
	}
}

func TestMCPDraftCreationIncludesSourcePolicyGuidance(t *testing.T) {
	cases := []struct {
		name        string
		scope       string
		writePolicy string
		want        string
	}{
		{"team overlay preferred", config.SourceScopeTeam, config.WritePolicyOverlayPreferred, "overlay is the preferred workflow"},
		{"personal direct", config.SourceScopePersonal, config.WritePolicyDirectAllowed, "direct source edits are allowed"},
		{"external read only", config.SourceScopeExternal, config.WritePolicyReadOnly, "this source is read-only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := configWithInstalledPolicySource(t, tc.scope, tc.writePolicy)
			created, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"})
			if err != nil {
				t.Fatal(err)
			}
			text := mustJSON(created)
			if !strings.Contains(text, `"source_policy"`) ||
				!strings.Contains(text, tc.writePolicy) ||
				!strings.Contains(text, tc.want) {
				t.Fatalf("created draft missing policy guidance: %s", text)
			}
		})
	}
}

func TestMCPDraftPromotionPolicy(t *testing.T) {
	cfg := configWithInstalledPolicySource(t, config.SourceScopeTeam, config.WritePolicyDraftPromote)
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "team-source")
	cfg.Sources["team-dest"] = config.NormalizeSource(config.Source{
		Name:        "team-dest",
		Source:      dest,
		Scope:       config.SourceScopeTeam,
		WritePolicy: config.WritePolicyDraftPromote,
	})
	dryRun, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "team-dest"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(dryRun), `"dry_run":true`) || !strings.Contains(mustJSON(dryRun), `"draft_promote"`) {
		t.Fatalf("dry-run promote = %#v", dryRun)
	}
	written, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "team-dest", "dry_run": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dest, "redis", "SKILL.md")), "Base.") ||
		!strings.Contains(mustJSON(written), `"dry_run":false`) {
		t.Fatalf("written promote = %#v", written)
	}

	overlayDest := filepath.Join(t.TempDir(), "overlay-source")
	cfg.Sources["overlay-dest"] = config.NormalizeSource(config.Source{
		Name:        "overlay-dest",
		Source:      overlayDest,
		Scope:       config.SourceScopeTeam,
		WritePolicy: config.WritePolicyOverlayPreferred,
	})
	overlay, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "overlay-dest"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(overlay), `"overlay_preferred"`) ||
		!strings.Contains(mustJSON(overlay), "overlay is the preferred workflow") {
		t.Fatalf("overlay promote = %#v", overlay)
	}

	readOnlyDest := filepath.Join(t.TempDir(), "read-only-source")
	cfg.Sources["read-only-dest"] = config.NormalizeSource(config.Source{
		Name:        "read-only-dest",
		Source:      readOnlyDest,
		Scope:       config.SourceScopeExternal,
		WritePolicy: config.WritePolicyReadOnly,
	})
	if _, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "read-only-dest"}); err == nil {
		t.Fatal("read-only promotion unexpectedly succeeded")
	}
}

func TestMCPUpsertSourceSkillFileContract(t *testing.T) {
	cfg := testConfig(t)
	sourceRoot := filepath.Join(cfg.Home, "editable-source")
	writeFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nBase.\n")
	cfg.Sources["editable"] = config.NormalizeSource(config.Source{
		Name:        "editable",
		Source:      sourceRoot,
		Scope:       config.SourceScopePersonal,
		WritePolicy: config.WritePolicyDirectAllowed,
	})

	dryRun, err := callTool(cfg, "upsert_source_skill_file", map[string]any{
		"source":   "editable",
		"skill_id": "redis",
		"path":     "SKILL.md",
		"content":  "Edited.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := mustJSON(dryRun)
	if !strings.Contains(text, `"dry_run":true`) ||
		!strings.Contains(text, `"source_mutated":false`) ||
		!strings.Contains(text, `"base_mutated":false`) ||
		!strings.Contains(text, `"source_policy"`) ||
		!strings.Contains(readFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md")), "Base.") {
		t.Fatalf("dry-run source edit = %s", text)
	}

	written, err := callTool(cfg, "upsert_source_skill_file", map[string]any{
		"source":               "editable",
		"skill_name":           "redis",
		"path":                 "SKILL.md",
		"content":              "Edited.\n",
		"dry_run":              false,
		"confirm_source_write": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(written), `"source_mutated":true`) ||
		readFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md")) != "Edited.\n" {
		t.Fatalf("written source edit = %#v", written)
	}
}

func TestE2ETeamSourceOverlayFirstLifecycle(t *testing.T) {
	cfg := configWithInstalledPolicySource(t, config.SourceScopeTeam, config.WritePolicyOverlayPreferred)
	source := cfg.Sources["policy-source"].Source
	originalSource := readFile(t, filepath.Join(source, "redis", "SKILL.md"))
	if _, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id": "redis",
		"path":     "SKILL.md",
		"content":  "\nOverlay guidance.\n",
	}); err != nil {
		t.Fatal(err)
	}
	rendered, err := callTool(cfg, "render_skill", map[string]any{"skill_id": "redis", "agent": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rendered), `"redis"`) {
		t.Fatalf("rendered = %#v", rendered)
	}
	read, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "agent": "codex", "rendered": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(read), "Overlay guidance.") {
		t.Fatalf("rendered skill missing overlay: %#v", read)
	}
	if readFile(t, filepath.Join(source, "redis", "SKILL.md")) != originalSource {
		t.Fatal("overlay workflow mutated source")
	}
}

func TestE2EMCPStdioAgentLifecycle(t *testing.T) {
	cfg := configWithInstalledPolicySource(t, config.SourceScopePersonal, config.WritePolicyDirectAllowed)
	source := cfg.Sources["policy-source"].Source
	var input bytes.Buffer
	for i, msg := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": callParams{Name: "list_sources", Arguments: map[string]any{}}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": callParams{Name: "describe_tool", Arguments: map[string]any{"name": "promote_skill_draft"}}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": callParams{Name: "create_skill_draft", Arguments: map[string]any{"skill_name": "redis"}}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": callParams{Name: "upsert_source_skill_file", Arguments: map[string]any{"source": "policy-source", "skill_id": "redis", "path": "SKILL.md", "content": "Dry run.\n"}}},
		{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": callParams{Name: "promote_skill_draft", Arguments: map[string]any{"draft_id": "draft:redis", "to_source": "policy-source"}}},
		{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": callParams{Name: "promote_skill_draft", Arguments: map[string]any{"draft_id": "draft:redis", "to_source": "policy-source", "dry_run": false}}},
		{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": callParams{Name: "find_source_skills", Arguments: map[string]any{"source": "policy-source", "query": "redis"}}},
		{"jsonrpc": "2.0", "id": 8, "method": "tools/call", "params": callParams{Name: "render_skill_draft", Arguments: map[string]any{"draft_id": "draft:redis", "copy": false}}},
	} {
		if msg["id"].(int) != i+1 {
			t.Fatalf("bad test message id: %#v", msg)
		}
		input.Write(EncodeForTest(msg))
	}
	var output bytes.Buffer
	if err := Serve(cfg, &input, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"policy-source", "source_policy", "draft:redis", "dry_run", "applied", "actions", "canonical_resources", "inputSchema", "rendered_path"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdio lifecycle missing %q in %s", want, text)
		}
	}
	if strings.Contains(readFile(t, filepath.Join(source, "redis", "SKILL.md")), "Dry run.") {
		t.Fatal("stdio source edit dry run mutated source")
	}
}

func TestREADMEMCPJSONExamplesReplayThroughStdio(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, mcpSection, found := strings.Cut(string(readme), "## MCP Server")
	if !found {
		t.Fatal("README is missing the MCP Server section")
	}
	if nextSection := strings.Index(mcpSection, "\n## "); nextSection >= 0 {
		mcpSection = mcpSection[:nextSection]
	}

	var examples []callParams
	for remaining := mcpSection; ; {
		_, afterFence, ok := strings.Cut(remaining, "```json\n")
		if !ok {
			break
		}
		body, afterBlock, ok := strings.Cut(afterFence, "\n```")
		if !ok {
			t.Fatal("README has an unterminated JSON example")
		}
		var example callParams
		if err := json.Unmarshal([]byte(body), &example); err != nil {
			t.Fatalf("parse README MCP example %q: %v", body, err)
		}
		examples = append(examples, example)
		remaining = afterBlock
	}
	if len(examples) != 3 {
		t.Fatalf("README MCP JSON example count = %d, want 3", len(examples))
	}

	cfg := configWithInstalledPolicySource(t, config.SourceScopePersonal, config.WritePolicyDirectAllowed)
	source := cfg.Sources["policy-source"]
	delete(cfg.Sources, "policy-source")
	source.Name = "personal"
	cfg.Sources["personal"] = source
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}

	var input bytes.Buffer
	for index, example := range examples {
		input.Write(EncodeForTest(map[string]any{
			"jsonrpc": "2.0",
			"id":      index + 1,
			"method":  "tools/call",
			"params":  example,
		}))
	}
	var output bytes.Buffer
	if err := Serve(cfg, &input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	var resultText strings.Builder
	for index := range examples {
		frame, err := readMessage(reader)
		if err != nil {
			t.Fatalf("read README example response %d: %v", index+1, err)
		}
		var response struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(frame, &response); err != nil {
			t.Fatalf("decode README example response %d: %v", index+1, err)
		}
		if response.Result.IsError {
			t.Fatalf("README MCP example %d failed: %s", index+1, frame)
		}
		for _, content := range response.Result.Content {
			resultText.WriteString(content.Text)
		}
	}
	if !strings.Contains(resultText.String(), `"dry_run": true`) ||
		!strings.Contains(resultText.String(), `"applied": true`) {
		t.Fatalf("README MCP lifecycle missing preview or apply result: %s", resultText.String())
	}
	if _, err := os.Stat(filepath.Join(source.Source, "redis", "SKILL.md")); err != nil {
		t.Fatalf("README MCP apply result was not readable: %v", err)
	}
}

func TestMCPNewDraftWorkflow(t *testing.T) {
	cfg := testConfig(t)
	created, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "brand-new", "template": "qa"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(created), `"type":"new"`) {
		t.Fatalf("created = %#v", created)
	}
	existing, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "brand-new", "allow_existing": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(existing), "draft:brand-new") {
		t.Fatalf("existing = %#v", existing)
	}
	rendered, err := callTool(cfg, "render_skill_draft", map[string]any{"skill_id": "draft:brand-new"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(rendered), "draft:brand-new") {
		t.Fatalf("rendered = %#v", rendered)
	}
	if _, err := callTool(cfg, "rebase_skill_draft", map[string]any{"draft_id": "draft:brand-new"}); err == nil {
		t.Fatal("new draft rebase unexpectedly succeeded")
	}
	renderedDraft, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis-rendered", "from_skill": "redis", "from_rendered": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(renderedDraft), `"created_from":"rendered"`) {
		t.Fatalf("renderedDraft = %#v", renderedDraft)
	}
}

func TestMCPTranscriptRegressionWorkflow(t *testing.T) {
	cfg := testConfig(t)
	createdNew, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "team-greeting-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(createdNew), `"type":"new"`) {
		t.Fatalf("createdNew = %#v", createdNew)
	}
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "team-greeting-demo", "from_skill": "redis", "allow_existing": true}); err == nil {
		t.Fatal("conflicting allow_existing draft unexpectedly succeeded")
	}
	replaced, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "team-greeting-demo", "from_skill": "redis", "replace_existing": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(replaced), `"type":"version"`) {
		t.Fatalf("replaced = %#v", replaced)
	}
	deleted, err := callTool(cfg, "delete_skill_draft", map[string]any{"skill_name": "team-greeting-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(deleted), `"deleted":true`) {
		t.Fatalf("deleted = %#v", deleted)
	}
	if draft.Exists(cfg, "team-greeting-demo") {
		t.Fatal("draft still exists after delete")
	}

	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(t.TempDir(), "local", "source")
	cfg.Sources["local"] = config.Source{Name: "local", Source: sourceRoot}
	promoted, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "local", "dry_run": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(promoted), `"source":"local"`) || !strings.Contains(readFile(t, filepath.Join(sourceRoot, "redis", "SKILL.md")), "Base.") {
		t.Fatalf("promoted = %#v", promoted)
	}
	if _, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": cfg.StoreDir}); err == nil {
		t.Fatal("managed store promotion unexpectedly succeeded")
	}
	if _, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": sourceRoot, "to_source": "local"}); err == nil {
		t.Fatal("to_path plus to_source unexpectedly succeeded")
	}
	cfg.Sources["remote"] = config.Source{Name: "remote", Source: "owner/repo"}
	if _, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_source": "remote"}); err == nil {
		t.Fatal("remote source promotion unexpectedly succeeded")
	}

	if _, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id":  "redis",
		"operation": "template",
		"path":      "SKILL.md.j2",
		"content":   `{"tone":"casual"}`,
	}); err == nil {
		t.Fatal("JSON template overlay unexpectedly succeeded")
	}
	template, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{
		"skill_id":  "redis",
		"operation": "template",
		"path":      "SKILL.md.j2",
		"content":   `{% extends "base/SKILL.md.j2" %}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(template), `"updated":true`) {
		t.Fatalf("template = %#v", template)
	}
}

func TestMCPDraftSafetyErrors(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "outside")
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"create_skill_draft", map[string]any{}},
		{"create_skill_draft", map[string]any{"skill_name": "redis"}},
		{"create_skill_draft", map[string]any{"skill_name": "missing-rendered", "from_rendered": true}},
		{"read_skill_draft", map[string]any{}},
		{"read_skill_draft", map[string]any{"draft_id": "redis"}},
		{"read_skill_draft", map[string]any{"draft_id": "draft:missing"}},
		{"list_skill_draft_files", map[string]any{"draft_id": "redis"}},
		{"read_skill_draft_file", map[string]any{"draft_id": "draft:redis"}},
		{"read_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "../bad"}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "x.md"}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "x.md", "content": "", "base64_content": ""}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "x.md", "base64_content": "bad"}},
		{"upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": ".upskill-draft.json", "content": "bad"}},
		{"delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": ".upskill-draft.json"}},
		{"delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "missing.md"}},
		{"delete_skill_draft", map[string]any{"draft_id": "draft:missing"}},
		{"import_skill_draft_archive", map[string]any{"draft_id": "draft:redis"}},
		{"import_skill_draft_file", map[string]any{"draft_id": "draft:redis", "source_path": outside, "path": "outside.txt"}},
		{"render_skill_draft", map[string]any{}},
		{"diff_skill_draft", map[string]any{}},
		{"rebase_skill_draft", map[string]any{"draft_id": "draft:missing"}},
		{"promote_skill_draft", map[string]any{"draft_id": "draft:redis"}},
		{"upsert_skill_overlay_file", map[string]any{"skill_id": "draft:redis", "path": "SKILL.md", "content": "bad"}},
		{"delete_skill_overlay_file", map[string]any{"skill_id": "draft:redis", "path": "SKILL.md"}},
	} {
		if _, err := callTool(cfg, tc.name, tc.args); err == nil {
			t.Fatalf("%s unexpectedly succeeded with %#v", tc.name, tc.args)
		}
	}
}

func TestMCPDraftAdditionalBranches(t *testing.T) {
	cfg := testConfig(t)
	if _, err := callTool(cfg, "create_skill_draft", map[string]any{"skill_name": "redis"}); err != nil {
		t.Fatal(err)
	}

	foundDraftOnly, err := callTool(cfg, "find_skills", map[string]any{"query": "draft:redis", "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(foundDraftOnly), `"kind":"draft"`) {
		t.Fatalf("foundDraftOnly = %#v", foundDraftOnly)
	}
	foundLimited, err := callTool(cfg, "find_skills", map[string]any{"query": "Redis", "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(foundLimited.([]map[string]any)) != 1 {
		t.Fatalf("foundLimited = %#v", foundLimited)
	}
	draftOnlyCfg := cfg
	draftOnlyCfg.StoreDir = filepath.Join(t.TempDir(), "empty-store")
	if _, err := draft.Save(draftOnlyCfg, draft.Metadata{Name: "alpha", Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Save(draftOnlyCfg, draft.Metadata{Name: "beta", Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	noMatches, err := findSkills(draftOnlyCfg, "nomatch", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(noMatches) != 0 {
		t.Fatalf("noMatches = %#v", noMatches)
	}
	draftLimited, err := findSkills(draftOnlyCfg, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(draftLimited) != 1 || draftLimited[0]["kind"] != "draft" {
		t.Fatalf("draftLimited = %#v", draftLimited)
	}
	if _, err := findSkills(config.Config{StoreDir: filepath.Join(cfg.StoreDir, "redis", "SKILL.md")}, "", 10); err == nil {
		t.Fatal("findSkills bad store unexpectedly succeeded")
	}
	if _, err := findSkills(config.Config{StoreDir: cfg.StoreDir, DraftsDir: filepath.Join(cfg.StoreDir, "redis", "SKILL.md")}, "", 10); err == nil {
		t.Fatal("findSkills bad drafts unexpectedly succeeded")
	}

	noSkillCfg := cfg
	if _, err := draft.Save(noSkillCfg, draft.Metadata{Name: "source-only", Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(noSkillCfg, "read_skill_draft", map[string]any{"draft_id": "draft:source-only"}); err == nil {
		t.Fatal("read draft without SKILL.md unexpectedly succeeded")
	}
	badExistingCfg := cfg
	badExisting := filepath.Join(badExistingCfg.DraftsDir, "bad-existing")
	if err := os.MkdirAll(badExisting, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badExisting, draft.MetadataFile), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := createSkillDraft(badExistingCfg, map[string]any{"skill_name": "bad-existing", "allow_existing": true}); err == nil {
		t.Fatal("allow_existing read error unexpectedly succeeded")
	}
	if _, err := listSkillDrafts(config.Config{DraftsDir: filepath.Join(cfg.StoreDir, "redis", "SKILL.md")}, map[string]any{}); err == nil {
		t.Fatal("listSkillDrafts bad drafts dir unexpectedly succeeded")
	}
	limitedDrafts, err := listSkillDrafts(draftOnlyCfg, map[string]any{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limitedDrafts.([]map[string]any)) != 1 {
		t.Fatalf("limitedDrafts = %#v", limitedDrafts)
	}
	oldManifest := fileManifest
	fileManifest = func(string, string, bool, bool, int) ([]map[string]any, error) {
		return nil, errors.New("manifest failed")
	}
	t.Cleanup(func() { fileManifest = oldManifest })
	if _, err := callTool(cfg, "read_skill_draft", map[string]any{"draft_id": "draft:redis"}); err == nil {
		t.Fatal("read draft manifest failure unexpectedly succeeded")
	}
	fileManifest = oldManifest
	if _, err := readSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("readSkillDraftFile without draft unexpectedly succeeded")
	}
	if _, err := upsertSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("upsertSkillDraftFile without draft unexpectedly succeeded")
	}
	if _, err := deleteSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("deleteSkillDraftFile without draft unexpectedly succeeded")
	}
	if _, err := promoteSkillDraft(cfg, map[string]any{}); err == nil {
		t.Fatal("promoteSkillDraft without draft unexpectedly succeeded")
	}
	if _, err := deleteSkillDraft(cfg, map[string]any{}); err == nil {
		t.Fatal("deleteSkillDraft without draft unexpectedly succeeded")
	}
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "draft:missing"}); err == nil {
		t.Fatal("read_skill missing draft unexpectedly succeeded")
	}

	for _, tc := range []map[string]any{
		{"draft_id": "draft:redis", "path": "bad.md", "content": 1},
		{"draft_id": "draft:redis", "path": "bad.md", "base64_content": 1},
		{"draft_id": "draft:redis", "path": "../bad.md", "content": "bad"},
	} {
		if _, err := callTool(cfg, "upsert_skill_draft_file", tc); err == nil {
			t.Fatalf("upsert unexpectedly succeeded with %#v", tc)
		}
	}
	writeFile(t, filepath.Join(cfg.DraftsDir, "redis", "blocked"), "file")
	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "blocked/file.md", "content": "bad"}); err == nil {
		t.Fatal("upsert mkdir failure unexpectedly succeeded")
	}
	if err := os.MkdirAll(filepath.Join(cfg.DraftsDir, "redis", "as-dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(cfg, "upsert_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "as-dir.md", "content": "bad"}); err == nil {
		t.Fatal("upsert write failure unexpectedly succeeded")
	}
	if _, err := callTool(cfg, "delete_skill_draft_file", map[string]any{"draft_id": "draft:redis", "path": "../bad"}); err == nil {
		t.Fatal("delete traversal unexpectedly succeeded")
	}

	if _, err := importSkillDraftArchive(cfg, map[string]any{}); err == nil {
		t.Fatal("import archive without draft unexpectedly succeeded")
	}
	if _, err := importSkillDraftFile(cfg, map[string]any{}); err == nil {
		t.Fatal("import file without draft unexpectedly succeeded")
	}
	dest := filepath.Join(t.TempDir(), "repo")
	promoted, err := callTool(cfg, "promote_skill_draft", map[string]any{"draft_id": "draft:redis", "to_path": dest, "dry_run": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(promoted), `"dry_run":false`) || !strings.Contains(readFile(t, filepath.Join(dest, "redis", "SKILL.md")), "Base.") {
		t.Fatalf("promoted = %#v", promoted)
	}

	if _, err := createDraftForMCP(cfg, "", "", false, ""); err == nil {
		t.Fatal("empty createDraftForMCP unexpectedly succeeded")
	}
	if _, err := createNewDraftForMCP(cfg, "redis", "again"); err != nil {
		t.Fatalf("createNewDraftForMCP existing failed: %v", err)
	}
	oldMCPStat := mcpOsStat
	oldMCPWriteFile := mcpOsWriteFile
	oldMCPRemoveAll := mcpOsRemoveAll
	oldMCPCopyDir := mcpCopyDir
	oldMCPDraftRemove := mcpDraftRemove
	t.Cleanup(func() {
		mcpOsStat = oldMCPStat
		mcpOsWriteFile = oldMCPWriteFile
		mcpOsRemoveAll = oldMCPRemoveAll
		mcpCopyDir = oldMCPCopyDir
		mcpDraftRemove = oldMCPDraftRemove
	})
	blockedParent := filepath.Join(t.TempDir(), "blocked-parent")
	writeFile(t, blockedParent, "file")
	if _, err := createNewDraftForMCP(config.Config{DraftsDir: filepath.Join(blockedParent, "drafts")}, "blocked", ""); err == nil {
		t.Fatal("createNewDraftForMCP save error unexpectedly succeeded")
	}
	mcpOsStat = func(path string) (os.FileInfo, error) {
		if strings.HasSuffix(path, "SKILL.md") {
			return nil, errors.New("stat failed")
		}
		return oldMCPStat(path)
	}
	if _, err := createNewDraftForMCP(cfg, "stat-error", ""); err == nil {
		t.Fatal("createNewDraftForMCP stat error unexpectedly succeeded")
	}
	mcpOsStat = oldMCPStat
	mcpOsWriteFile = func(string, []byte, os.FileMode) error { return errors.New("write failed") }
	if _, err := createNewDraftForMCP(cfg, "write-error", ""); err == nil {
		t.Fatal("createNewDraftForMCP write error unexpectedly succeeded")
	}
	mcpOsWriteFile = oldMCPWriteFile
	writeFile(t, filepath.Join(cfg.StoreDir, "not-dir"), "file")
	if _, err := createVersionDraftForMCP(cfg, "from-file", "not-dir", false); err == nil {
		t.Fatal("createVersionDraftForMCP from file unexpectedly succeeded")
	}
	writeFile(t, filepath.Join(cfg.StoreDir, "bad-render", "SKILL.md.j2"), "{% bad")
	if _, err := createVersionDraftForMCP(cfg, "bad-render-draft", "bad-render", true); err == nil {
		t.Fatal("createVersionDraftForMCP render error unexpectedly succeeded")
	}
	mcpOsRemoveAll = func(string) error { return errors.New("remove failed") }
	if _, err := createVersionDraftForMCP(cfg, "remove-error", "redis", false); err == nil {
		t.Fatal("createVersionDraftForMCP remove error unexpectedly succeeded")
	}
	mcpOsRemoveAll = oldMCPRemoveAll
	mcpCopyDir = func(string, string, func(string, os.DirEntry) bool) error { return errors.New("copy failed") }
	if _, err := createVersionDraftForMCP(cfg, "copy-error", "redis", false); err == nil {
		t.Fatal("createVersionDraftForMCP copy error unexpectedly succeeded")
	}
	mcpCopyDir = oldMCPCopyDir
	mcpDraftRemove = func(config.Config, string) error { return errors.New("remove draft failed") }
	if _, err := createSkillDraft(cfg, map[string]any{"skill_name": "redis", "replace_existing": true}); err == nil {
		t.Fatal("replace_existing remove error unexpectedly succeeded")
	}
	if _, err := deleteSkillDraft(cfg, map[string]any{"draft_id": "draft:redis"}); err == nil {
		t.Fatal("delete draft remove error unexpectedly succeeded")
	}
	mcpDraftRemove = oldMCPDraftRemove
	badLockCfg := cfg
	badLockCfg.LockPath = filepath.Join(t.TempDir(), "lock.json")
	writeFile(t, badLockCfg.LockPath, "{bad")
	if _, err := createVersionDraftForMCP(badLockCfg, "parent-error", "redis", false); err == nil {
		t.Fatal("createVersionDraftForMCP parent metadata error unexpectedly succeeded")
	}
	if _, _, _, err := parentMetadataForMCP(badLockCfg, "redis", filepath.Join(cfg.StoreDir, "redis")); err == nil {
		t.Fatal("parent metadata bad lock unexpectedly succeeded")
	}
	lockCfg := cfg
	if err := library.WriteLock(lockCfg, library.Lock{Skills: map[string]library.LockEntry{"redis": {Source: "repo", Ref: "main", Hash: "sha256:lock"}}}); err != nil {
		t.Fatal(err)
	}
	source, ref, hash, err := parentMetadataForMCP(lockCfg, "redis", filepath.Join(cfg.StoreDir, "redis"))
	if err != nil || source != "repo" || ref != "main" || hash != "sha256:lock" {
		t.Fatalf("parent metadata lock = %q %q %q %v", source, ref, hash, err)
	}
	if _, _, _, err := parentMetadataForMCP(cfg, "missing", filepath.Join(cfg.StoreDir, "missing")); err == nil {
		t.Fatal("parent metadata hash failure unexpectedly succeeded")
	}
	if !matchesSearch("", "anything") || matchesSearch("needle", "hay") {
		t.Fatal("matchesSearch branches failed")
	}
	if got := readOptional(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("readOptional missing = %q", got)
	}
}

func TestServeAndFraming(t *testing.T) {
	cfg := testConfig(t)
	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	input := bytes.NewReader(EncodeForTest(msg))
	var output bytes.Buffer
	if err := Serve(cfg, input, &output); err != nil {
		t.Fatal(err)
	}
	frame, err := readMessage(bufio.NewReader(&output))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), "tools") {
		t.Fatalf("frame = %s", frame)
	}

	lineInput := bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"))
	line, err := readMessage(lineInput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), "initialize") {
		t.Fatalf("line = %s", line)
	}
	var lineOutput bytes.Buffer
	if err := Serve(cfg, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n"), &lineOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lineOutput.String(), "{") || !strings.HasSuffix(lineOutput.String(), "\n") {
		t.Fatalf("line-framed response = %q", lineOutput.String())
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader("Bad: header\r\n\r\n{}"))); err == nil {
		t.Fatal("missing content length unexpectedly succeeded")
	}
	if err := writeMessage(errWriter{}, response{}); err == nil {
		t.Fatal("writeMessage unexpectedly succeeded")
	}
	if _, err := DecodeBase64ForTest("bad"); err == nil {
		t.Fatal("bad base64 unexpectedly decoded")
	}
	if err := writeMessage(io.Discard, response{Result: math.Inf(1)}); err == nil {
		t.Fatal("writeMessage marshal error unexpectedly succeeded")
	}

	var notificationOut bytes.Buffer
	notification := map[string]any{"jsonrpc": "2.0", "method": "initialized"}
	if err := Serve(cfg, bytes.NewReader(EncodeForTest(notification)), &notificationOut); err != nil {
		t.Fatal(err)
	}
	if notificationOut.Len() != 0 {
		t.Fatalf("notification output = %q", notificationOut.String())
	}
	if err := Serve(cfg, strings.NewReader("Bad: header\r\n\r\n"), io.Discard); err == nil {
		t.Fatal("bad header unexpectedly served")
	}
	if err := Serve(cfg, bytes.NewReader([]byte("Content-Length: 4\r\n\r\n{bad")), errWriter{}); err == nil {
		t.Fatal("parse error write failure unexpectedly succeeded")
	}
	if err := Serve(cfg, bytes.NewReader(EncodeForTest(msg)), errWriter{}); err == nil {
		t.Fatal("valid response write failure unexpectedly succeeded")
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader("Content-Length: 4\r\n"))); err == nil {
		t.Fatal("header EOF unexpectedly succeeded")
	}
}

func TestServeParseError(t *testing.T) {
	var output bytes.Buffer
	input := bytes.NewReader([]byte("Content-Length: 4\r\n\r\n{bad"))
	if err := Serve(testConfig(t), input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "-32700") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestToolArgumentErrors(t *testing.T) {
	cfg := testConfig(t)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"read_skill", map[string]any{}},
		{"read_skill", map[string]any{"skill_id": "missing", "rendered": false}},
		{"list_skill_files", map[string]any{}},
		{"list_skill_files", map[string]any{"skill_id": "missing"}},
		{"list_skill_files", map[string]any{"skill_id": "missing", "rendered": false}},
		{"read_skill_file", map[string]any{}},
		{"read_skill_file", map[string]any{"skill_id": "redis"}},
		{"read_skill_file", map[string]any{"skill_id": "redis", "path": "../bad", "rendered": false}},
		{"read_skill_file", map[string]any{"skill_id": "redis", "path": "missing.md", "rendered": false}},
		{"render_skill", map[string]any{}},
		{"upsert_skill_overlay_file", map[string]any{"skill_id": "redis", "path": "../bad"}},
		{"delete_skill_overlay_file", map[string]any{"skill_id": "redis", "path": "missing.md"}},
	} {
		if _, err := callTool(cfg, tc.name, tc.args); err == nil {
			t.Fatalf("%s unexpectedly succeeded", tc.name)
		}
	}
	if _, err := readMessage(bufio.NewReader(bytes.NewReader(nil))); !errors.Is(err, io.EOF) {
		t.Fatalf("empty read err = %v", err)
	}
	if _, err := callTool(config.Config{LockPath: filepath.Join(t.TempDir(), "missing", "lock.json")}, "sync_skills", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	emptyCfg := config.Config{StoreDir: filepath.Join(t.TempDir(), "empty"), RenderedDir: filepath.Join(t.TempDir(), "rendered"), LockPath: filepath.Join(t.TempDir(), "lock.json")}
	emptySync, err := callTool(emptyCfg, "sync_skills", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(emptySync), `"warnings"`) || !strings.Contains(mustJSON(emptySync), `"rendered":[]`) {
		t.Fatalf("emptySync = %#v", emptySync)
	}
	if len(nonNilInstalled([]library.InstalledSkill{{StoreName: "x"}})) != 1 || len(nonNilRendered([]render.Result{{Skill: "x"}})) != 1 {
		t.Fatal("nonNil helper passthrough failed")
	}
	badLock := filepath.Join(t.TempDir(), "lock.json")
	writeFile(t, badLock, "{bad")
	if _, err := callTool(config.Config{LockPath: badLock}, "sync_skills", map[string]any{}); err == nil {
		t.Fatal("bad lock sync unexpectedly succeeded")
	}
	truncated, err := callTool(cfg, "read_skill_file", map[string]any{"skill_id": "redis", "path": "references/guide.md", "rendered": false, "max_bytes": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(truncated), `"content":"Gu"`) {
		t.Fatalf("truncated = %#v", truncated)
	}
	if _, err := callTool(cfg, "upsert_skill_overlay_file", map[string]any{"skill_name": "Redis", "path": "SKILL.md", "content": "x"}); err != nil {
		t.Fatal(err)
	}
	badSkill := filepath.Join(cfg.StoreDir, "bad")
	writeFile(t, filepath.Join(badSkill, "README.md"), "no entrypoint")
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "bad", "rendered": false}); err == nil {
		t.Fatal("read skill without SKILL.md unexpectedly succeeded")
	}
	oldManifest := fileManifest
	fileManifest = func(string, string, bool, bool, int) ([]map[string]any, error) {
		return nil, errors.New("manifest failed")
	}
	if _, err := callTool(cfg, "read_skill", map[string]any{"skill_id": "redis", "rendered": false, "include_files": true}); err == nil {
		t.Fatal("read skill manifest error unexpectedly succeeded")
	}
	fileManifest = oldManifest

	stringResult := toolResult("plain", false)
	if !strings.Contains(mustJSON(stringResult), "plain") {
		t.Fatalf("string result = %#v", stringResult)
	}
	jsonResult := toolResult(map[string]any{"answer": 1}, false)
	if !strings.Contains(mustJSON(jsonResult), "answer") {
		t.Fatalf("json result = %#v", jsonResult)
	}
	if intArg(map[string]any{"n": 3}, "n", 0) != 3 || intArg(map[string]any{"n": "4"}, "n", 0) != 4 || intArg(map[string]any{"n": "bad"}, "n", 5) != 5 {
		t.Fatal("intArg variants failed")
	}
	if boolArg(map[string]any{"b": "bad"}, "b", true) != true {
		t.Fatal("boolArg fallback failed")
	}
	if err := rejectManagedPromotionDestination(config.Config{}, filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatal(err)
	}
	if isInsidePath(filepath.Join(t.TempDir(), "child"), t.TempDir()) {
		t.Fatal("unrelated inside path reported true")
	}
	if typ, parent := requestedDraftShape(cfg, "redis", "", false); typ != draft.TypeVersion || parent != "redis" {
		t.Fatalf("implicit requested shape = %s %s", typ, parent)
	}
	if typ, parent := requestedDraftShape(config.Config{StoreDir: filepath.Join(t.TempDir(), "store")}, "fresh", "", true); typ != draft.TypeVersion || parent != "fresh" {
		t.Fatalf("rendered requested shape = %s %s", typ, parent)
	}
	if draftMatchesRequest(draft.Draft{Metadata: draft.Metadata{Type: draft.TypeVersion, ParentSkill: "redis"}}, draft.TypeVersion, "other") {
		t.Fatal("parent mismatch unexpectedly matched")
	}
	if requestedParentSuffix("") != "" {
		t.Fatal("empty parent suffix failed")
	}
	badStore := filepath.Join(t.TempDir(), "store-file")
	writeFile(t, badStore, "file")
	if _, err := syncSkillsTool(config.Config{StoreDir: badStore, RenderedDir: filepath.Join(t.TempDir(), "rendered"), LockPath: filepath.Join(t.TempDir(), "lock.json")}, map[string]any{}); err == nil {
		t.Fatal("sync render error unexpectedly succeeded")
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func successfulToolPayload(t *testing.T, cfg config.Config, name string, args map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(callParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	resp := handle(cfg, request{ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("%s RPC error: %#v", name, resp.Error)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] != false {
		t.Fatalf("%s tool error: %#v", name, result)
	}
	content := result["content"].([]map[string]string)
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(content[0]["text"]), &payload); err != nil {
		t.Fatalf("%s payload: %v\n%s", name, err, content[0]["text"])
	}
	return payload
}
