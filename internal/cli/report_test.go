package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"upskill/internal/audit"
	"upskill/internal/config"
	"upskill/internal/diagnostic"
	"upskill/internal/library"
	"upskill/internal/render"
)

func TestHumanReportMatchesMotivatingAddOutput(t *testing.T) {
	digest := "sha256:9ad1302bc323cabe82a8803ddf5cdbe682c1b14b0d205c6ed83ee023baf58887"
	collector := newCLIDiagnosticCollector()
	collector.Add(
		diagnostic.Diagnostic{
			Phase: diagnostic.PhasePreflight, Severity: diagnostic.SeverityWarning,
			Code: diagnostic.CodeUpdateCheckFailed, Subject: "agent-plan",
			Message: "locked skill not found in source", Details: map[string]any{}, Actions: []diagnostic.Action{},
		},
		diagnostic.Diagnostic{
			Phase: diagnostic.PhasePreflight, Severity: diagnostic.SeverityNotice,
			Code: diagnostic.CodeUpdateAvailable, Subject: "categorizing-transactions",
			Message: "update available", Details: map[string]any{},
			Actions: []diagnostic.Action{{Label: "next", Command: []string{"upskill", "update", "--diff", "categorizing-transactions"}}},
		},
	)
	items := []audit.AgentOutputSkillWarning{
		{Agent: "claude-code", OutputPath: "/Users/example/.claude/skills", Skill: "alpha", SkillPath: "/Users/example/.claude/skills/alpha", Reason: audit.ReasonNotInstalled},
		{Agent: "claude-code", OutputPath: "/Users/example/.claude/skills", Skill: "beta", SkillPath: "/Users/example/.claude/skills/beta", Reason: audit.ReasonNotInstalled},
		{Agent: "claude-code", OutputPath: "/Users/example/.claude/skills", Skill: "shared", SkillPath: "/Users/example/.claude/skills/shared", Reason: audit.ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/Users/example/.codex/skills", Skill: "alpha", SkillPath: "/Users/example/.codex/skills/alpha", Reason: audit.ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/Users/example/.codex/skills", Skill: "beta", SkillPath: "/Users/example/.codex/skills/beta", Reason: audit.ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/Users/example/.codex/skills", Skill: "delta", SkillPath: "/Users/example/.codex/skills/delta", Reason: audit.ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/Users/example/.codex/skills", Skill: "gamma", SkillPath: "/Users/example/.codex/skills/gamma", Reason: audit.ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/Users/example/.codex/skills", Skill: "shared", SkillPath: "/Users/example/.codex/skills/shared", Reason: audit.ReasonNotInstalled},
	}
	collector.AddAudit("", cliAuditConcise, audit.SummarizeAgentOutputWarnings(items))

	var output bytes.Buffer
	writeResultSection(
		&output,
		formatInstalledResultLines([]library.InstalledSkill{{StoreName: "agent-plan", Hash: digest}}, "installed", false),
		formatRenderResultLines([]render.Result{
			{Skill: "agent-plan", Agent: "claude-code", RenderedPath: "/Users/example/.skills/rendered/claude-code/agent-plan", CopiedTo: []string{"/Users/example/.claude/skills/agent-plan"}},
			{Skill: "agent-plan", Agent: "codex", RenderedPath: "/Users/example/.skills/rendered/codex/agent-plan", CopiedTo: []string{"/Users/example/.codex/skills/agent-plan"}},
		}),
	)
	collector.Flush(&output)

	want := "Result\n" +
		"  OK installed agent-plan\n" +
		"    digest: " + digest + "\n" +
		"  OK rendered agent-plan for claude-code\n" +
		"    rendered: /Users/example/.skills/rendered/claude-code/agent-plan\n" +
		"    copied: /Users/example/.claude/skills/agent-plan\n" +
		"  OK rendered agent-plan for codex\n" +
		"    rendered: /Users/example/.skills/rendered/codex/agent-plan\n" +
		"    copied: /Users/example/.codex/skills/agent-plan\n" +
		"\nPreflight\n" +
		"  WARNING agent-plan: update check failed: locked skill not found in source\n" +
		"  NOTICE categorizing-transactions: update available\n" +
		"    next: upskill update --diff categorizing-transactions\n" +
		"\nAgent output audit\n" +
		"  WARNING found 8 unmanaged skill occurrences across 2 agents (5 unique skills)\n" +
		"    claude-code: 3\n" +
		"    codex: 5\n" +
		"    unmanaged skills were left unchanged\n" +
		"    details: upskill doctor\n" +
		"    preview migration: upskill migrate\n" +
		"    apply migration: upskill migrate --yes\n"
	if output.String() != want {
		t.Fatalf("motivating report mismatch\nwant:\n%s\ngot:\n%s", want, output.String())
	}
	assertPlainStaticOutput(t, output.String())
}

func TestHumanInstalledResultsAreCompleteAndDeterministic(t *testing.T) {
	installed := []library.InstalledSkill{
		{StoreName: "zeta", Hash: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
		{StoreName: "alpha", Hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
	}
	var output bytes.Buffer
	writeResultSection(&output, formatInstalledResultLines(installed, "installed", false))
	want := "Result\n" +
		"  OK installed alpha\n" +
		"    digest: sha256:0000000000000000000000000000000000000000000000000000000000000000\n" +
		"  OK installed zeta\n" +
		"    digest: sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\n"
	if output.String() != want {
		t.Fatalf("installed result mismatch\nwant:\n%s\ngot:\n%s", want, output.String())
	}
	if installed[0].StoreName != "zeta" {
		t.Fatalf("formatter mutated input: %#v", installed)
	}
}

func TestHumanRenderResultsCoverAllStatesAndSortPaths(t *testing.T) {
	results := []render.Result{
		{Skill: "zeta", Agent: "codex", RenderedPath: "/rendered/zeta", CopiedTo: []string{"/copy/zeta-b", "/copy/zeta-a"}},
		{Skill: "beta", Agent: "codex", RenderedPath: "/rendered/beta-codex", CopiedTo: []string{"/copy/beta"}, Skipped: true},
		{Skill: "alpha", RenderedPath: "/rendered/alpha"},
		{Skill: "gamma", Agent: "plan-agent", RenderedPath: "/rendered/gamma", CopiedTo: []string{"/copy/gamma"}, Mode: "preview"},
		{Skill: "beta", Agent: "claude-code", RenderedPath: "/rendered/beta-claude", Skipped: true},
	}
	var output bytes.Buffer
	writeResultSection(&output, formatRenderResultLines(results))
	want := "Result\n" +
		"  OK rendered alpha for generic\n" +
		"    rendered: /rendered/alpha\n" +
		"    not copied\n" +
		"  OK unchanged beta for claude-code\n" +
		"    rendered: /rendered/beta-claude\n" +
		"    not copied\n" +
		"  OK unchanged beta for codex\n" +
		"    rendered: /rendered/beta-codex\n" +
		"    copied: /copy/beta\n" +
		"  PLAN render gamma for plan-agent\n" +
		"    render: /rendered/gamma\n" +
		"    copy: /copy/gamma\n" +
		"  OK rendered zeta for codex\n" +
		"    rendered: /rendered/zeta\n" +
		"    copied: /copy/zeta-a\n" +
		"    copied: /copy/zeta-b\n"
	if output.String() != want {
		t.Fatalf("render result mismatch\nwant:\n%s\ngot:\n%s", want, output.String())
	}
	if results[0].CopiedTo[0] != "/copy/zeta-b" {
		t.Fatalf("formatter mutated copied paths: %#v", results[0].CopiedTo)
	}
	assertPlainStaticOutput(t, output.String())
}

func TestHumanSectionsOmitEmptyContentAndSeparateNonEmptySections(t *testing.T) {
	var output bytes.Buffer
	writeHumanSections(&output, []humanSection{
		{Title: humanSectionResult, Lines: []string{"OK no changes"}},
		{Title: humanSectionPreflight, Lines: nil},
		{Title: humanSectionAudit, Lines: []string{"WARNING review output"}},
	})
	want := "Result\n  OK no changes\n\nAgent output audit\n  WARNING review output\n"
	if output.String() != want {
		t.Fatalf("section spacing mismatch\nwant:%q\ngot: %q", want, output.String())
	}
	if got := formatNoChangeResultLines(true); len(got) != 1 || got[0] != "PLAN no changes" {
		t.Fatalf("planned no-change result = %#v", got)
	}
	if got := formatInstalledResultLines(nil, "updated", false); len(got) != 1 || got[0] != "OK no skills updated" {
		t.Fatalf("empty update result = %#v", got)
	}
}

func TestHumanCommandRenderingQuotesUnsafeArguments(t *testing.T) {
	got := formatCommand([]string{"upskill", "sync", "--agent", "team ops;$(bad)'x", ""})
	want := "upskill sync --agent 'team ops;$(bad)'\"'\"'x' ''"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestConciseAuditReportsCountsWithoutItemPaths(t *testing.T) {
	summary := mixedHumanAuditSummary()
	output := strings.Join(formatConciseAuditLines(summary, ""), "\n")
	for _, want := range []string{
		"WARNING found 4 agent output audit findings across 2 agents (2 unique skills)",
		"unmanaged skill occurrences: 2",
		"missing render markers: 1",
		"inspection failures: 1",
		"claude-code: 2",
		"codex: 2",
		"unmanaged skills were left unchanged",
		"preview migration: upskill migrate",
		"apply migration: upskill migrate --yes",
		"repair managed outputs: upskill sync",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("concise audit missing %q:\n%s", want, output)
		}
	}
	for _, hidden := range []string{"/agents/", audit.ReasonNotInstalled, audit.ReasonMissingRenderMarker, "could not inspect"} {
		if strings.Contains(output, hidden) {
			t.Fatalf("concise audit exposed item detail %q:\n%s", hidden, output)
		}
	}
	if strings.Count(output, "details: upskill doctor") != 1 {
		t.Fatalf("details remediation was not deduplicated:\n%s", output)
	}
}

func TestConciseAuditPluralizesHeadlineCounts(t *testing.T) {
	tests := []struct {
		name     string
		findings []audit.AgentOutputSkillWarning
		want     string
	}{
		{
			name: "unmanaged",
			findings: []audit.AgentOutputSkillWarning{{
				Agent: "codex", OutputPath: "/agents/codex", Skill: "local",
				SkillPath: "/agents/codex/local", Reason: audit.ReasonNotInstalled,
			}},
			want: "WARNING found 1 unmanaged skill occurrence across 1 agent (1 unique skill)",
		},
		{
			name: "other audit finding",
			findings: []audit.AgentOutputSkillWarning{{
				Agent: "codex", OutputPath: "/agents/codex", Skill: "managed",
				SkillPath: "/agents/codex/managed", Reason: audit.ReasonMissingRenderMarker,
			}},
			want: "WARNING found 1 agent output audit finding across 1 agent (1 unique skill)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := audit.SummarizeAgentOutputWarnings(test.findings)
			output := strings.Join(formatConciseAuditLines(summary, ""), "\n")
			if !strings.Contains(output, test.want) {
				t.Fatalf("headline missing %q:\n%s", test.want, output)
			}
		})
	}
}

func TestDetailedAuditGroupsEveryPathAndReasonByAgent(t *testing.T) {
	summary := mixedHumanAuditSummary()
	output := strings.Join(formatDetailedAuditLines(summary, ""), "\n")
	if strings.Index(output, "WARNING claude-code") > strings.Index(output, "WARNING codex") {
		t.Fatalf("agents are not sorted:\n%s", output)
	}
	for _, want := range []string{
		"output: /agents/claude",
		"path: /agents/claude/shared",
		"reason: " + audit.ReasonNotInstalled,
		"inspection failed",
		"reason: could not inspect agent output directory: permission denied",
		"output: /agents/codex",
		"path: /agents/codex/managed",
		"reason: " + audit.ReasonMissingRenderMarker,
		"path: /agents/codex/shared",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("detailed audit missing %q:\n%s", want, output)
		}
	}
	if strings.Count(output, "details: upskill doctor") != 1 {
		t.Fatalf("detailed remediation was not deduplicated:\n%s", output)
	}
}

func TestAuditGuidanceMatchesFindingCategories(t *testing.T) {
	t.Run("missing marker", func(t *testing.T) {
		summary := audit.SummarizeAgentOutputWarnings([]audit.AgentOutputSkillWarning{{
			Agent: "codex", OutputPath: "/agents/codex", Skill: "managed",
			SkillPath: "/agents/codex/managed", Reason: audit.ReasonMissingRenderMarker,
		}})
		output := strings.Join(formatConciseAuditLines(summary, "codex"), "\n")
		if !strings.Contains(output, "repair managed outputs: upskill sync --agent codex") {
			t.Fatalf("missing-marker audit lacks scoped sync guidance:\n%s", output)
		}
		if strings.Contains(output, "migrate") {
			t.Fatalf("missing-marker audit suggests migration:\n%s", output)
		}
	})

	t.Run("inspection failure", func(t *testing.T) {
		summary := audit.SummarizeAgentOutputWarnings([]audit.AgentOutputSkillWarning{{
			Agent: "codex", OutputPath: "/agents/codex",
			Reason: "could not inspect agent output directory: permission denied",
		}})
		output := strings.Join(formatDetailedAuditLines(summary, "codex"), "\n")
		if !strings.Contains(output, "inspection failed") || !strings.Contains(output, "details: upskill doctor") {
			t.Fatalf("inspection audit lacks diagnostic guidance:\n%s", output)
		}
		if strings.Contains(output, "migrate") || strings.Contains(output, "upskill sync") {
			t.Fatalf("inspection audit suggests an inapplicable repair:\n%s", output)
		}
	})
}

func TestAuditGuidanceQuotesAgentSelectorAndOmitsEmptySection(t *testing.T) {
	summary := audit.SummarizeAgentOutputWarnings([]audit.AgentOutputSkillWarning{{
		Agent: "team ops", OutputPath: "/agents/team ops", Skill: "local",
		SkillPath: "/agents/team ops/local", Reason: audit.ReasonNotInstalled,
	}})
	output := strings.Join(formatConciseAuditLines(summary, "team ops"), "\n")
	for _, want := range []string{
		"preview migration: upskill migrate --agent 'team ops'",
		"apply migration: upskill migrate --agent 'team ops' --yes",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("agent-limited audit missing %q:\n%s", want, output)
		}
	}

	empty := audit.SummarizeAgentOutputWarnings(nil)
	if lines := formatConciseAuditLines(empty, ""); len(lines) != 0 {
		t.Fatalf("empty concise audit = %#v", lines)
	}
	if lines := formatDetailedAuditLines(empty, ""); len(lines) != 0 {
		t.Fatalf("empty detailed audit = %#v", lines)
	}
	collector := newCLIDiagnosticCollector()
	collector.AddAudit("", cliAuditConcise, empty)
	var flushed bytes.Buffer
	collector.Flush(&flushed)
	if flushed.Len() != 0 {
		t.Fatalf("empty audit rendered a section: %q", flushed.String())
	}
}

func TestPhaseAwareAddIntegratesTwoAgentResultsAndConciseAudit(t *testing.T) {
	root, configPath := setupEnv(t)
	claudeOutput := filepath.Join(root, "claude-skills")
	codexOutput := filepath.Join(root, "codex-skills")
	writeCLIConfig(t, configPath, map[string]string{
		"codex":       codexOutput,
		"claude-code": claudeOutput,
	})
	writeTestFile(t, filepath.Join(claudeOutput, "unmanaged-claude", "SKILL.md"), "# Claude local\n", 0o644)
	writeTestFile(t, filepath.Join(codexOutput, "unmanaged-codex", "SKILL.md"), "# Codex local\n", 0o644)
	source := filepath.Join(root, "source", "redis")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Redis\n---\n\nRedis body.\n", 0o644)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--no-check", "add", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"Result\n",
		"OK installed redis",
		"OK rendered redis for claude-code",
		"rendered: " + filepath.Join(root, "home", "rendered", "claude-code", "redis"),
		"copied: " + filepath.Join(claudeOutput, "redis"),
		"OK rendered redis for codex",
		"copied: " + filepath.Join(codexOutput, "redis"),
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("add stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Index(stdout.String(), "claude-code") > strings.Index(stdout.String(), "codex") {
		t.Fatalf("agent results are not deterministic:\n%s", stdout.String())
	}
	for _, path := range []string{filepath.Join(claudeOutput, "redis", "SKILL.md"), filepath.Join(codexOutput, "redis", "SKILL.md")} {
		if got := readTestFile(t, path); !strings.Contains(got, "Redis body.") {
			t.Fatalf("copy %s = %q", path, got)
		}
	}
	for _, want := range []string{humanSectionAudit, "2 unmanaged skill occurrences across 2 agents", "claude-code: 1", "codex: 1"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("add audit missing %q:\n%s", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), filepath.Join(claudeOutput, "unmanaged-claude")) {
		t.Fatalf("concise add audit exposed item path:\n%s", stderr.String())
	}
}

func TestPhaseAwareSyncAndRenderIntegration(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source", "redis")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--no-check", "add", source, "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n", 0o644)
	storePath := filepath.Join(root, "home", "store", "redis", "SKILL.md")
	oldStore := readTestFile(t, storePath)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "sync", "--dry-run", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync preview code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "PLAN update redis") || !strings.Contains(stdout.String(), "PLAN render redis for codex") {
		t.Fatalf("sync preview lacks planned outcomes:\n%s", stdout.String())
	}
	if got := readTestFile(t, storePath); got != oldStore {
		t.Fatalf("sync preview changed store: %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "sync", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync apply code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK updated redis") || !strings.Contains(stdout.String(), "OK rendered redis for codex") {
		t.Fatalf("sync apply lacks completed outcomes:\n%s", stdout.String())
	}
	if got := readTestFile(t, storePath); !strings.Contains(got, "New.") {
		t.Fatalf("sync apply did not update store: %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "sync", "--agent", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync no-change code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK no skills updated") || !strings.Contains(stdout.String(), "OK unchanged redis for codex") {
		t.Fatalf("sync no-change result is ambiguous:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "render", "redis", "--agent", "generic", "--copy=false"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render-only code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Result\n") || !strings.Contains(stdout.String(), "not copied") {
		t.Fatalf("render-only result lacks copy state:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), humanSectionAudit) {
		t.Fatalf("render-only unexpectedly audited agent output:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "render", "redis", "--copy=true"}, &stdout, &stderr); code != 0 {
		t.Fatalf("copied render code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Result\n") || !strings.Contains(stdout.String(), "redis for codex") {
		t.Fatalf("copied render lacks result hierarchy:\n%s", stdout.String())
	}
}

func TestPhaseAwarePayloadJSONStatusAndRoutingCompatibility(t *testing.T) {
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source", "redis")
	baseContent := "---\nname: Redis\n---\n\nKnown payload.\n"
	writeTestFile(t, filepath.Join(source, "SKILL.md"), baseContent, 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--no-check", "add", source, "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "show", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show code = %d stderr=%s", code, stderr.String())
	}
	if stdout.String() != baseContent {
		t.Fatalf("show payload changed\nwant:%q\ngot: %q", baseContent, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "render", "redis", "--agent", "generic", "--copy=false"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render code = %d stderr=%s", code, stderr.String())
	}
	renderedContent := readTestFile(t, filepath.Join(root, "home", "rendered", "generic", "redis", "SKILL.md"))
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "render", "redis", "--agent", "generic", "--print"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render print code = %d stderr=%s", code, stderr.String())
	}
	if stdout.String() != renderedContent {
		t.Fatalf("render --print payload changed\nwant:%q\ngot: %q", renderedContent, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list JSON code = %d stderr=%s", code, stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("list JSON array decode: rows=%#v err=%v payload=%s", rows, err, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "sync", "--json", "--dry-run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync JSON code = %d stderr=%s", code, stderr.String())
	}
	var object map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &object); err != nil || object["dry_run"] != true {
		t.Fatalf("sync JSON object decode: object=%#v err=%v payload=%s", object, err, stdout.String())
	}

	writeTestFile(t, filepath.Join(root, "codex-skills", "unmanaged", "SKILL.md"), "# Local\n", 0o644)
	for _, command := range []string{"status", "doctor"} {
		stdout.Reset()
		stderr.Reset()
		if code := Run([]string{"--no-check", command}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s code = %d stderr=%s", command, code, stderr.String())
		}
		if command == "status" && (!strings.Contains(stdout.String(), "home:") || !strings.Contains(stdout.String(), "installed skills: 1")) {
			t.Fatalf("status facts changed: %q", stdout.String())
		}
		if command == "doctor" && strings.TrimSpace(stdout.String()) != "ok" {
			t.Fatalf("doctor fact changed: %q", stdout.String())
		}
		for _, want := range []string{humanSectionAudit, "WARNING codex", "skill: unmanaged", filepath.Join(root, "codex-skills", "unmanaged"), audit.ReasonNotInstalled} {
			if !strings.Contains(stderr.String(), want) {
				t.Fatalf("%s detailed audit missing %q:\n%s", command, want, stderr.String())
			}
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"add", "--help"}, &stdout, &stderr); code != 0 || !strings.HasPrefix(stdout.String(), "usage: upskill add") || strings.Contains(stdout.String(), humanSectionResult) {
		t.Fatalf("add help routing changed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown command code = %d, want 2", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "render", "--print"}, &stdout, &stderr); code != 2 {
		t.Fatalf("render --print missing-skill code = %d, want 2", code)
	}
}

func TestCLISyncJSONAlwaysIncludesEmptyAuditSummary(t *testing.T) {
	setupEnv(t)
	for _, args := range [][]string{
		{"--no-check", "sync", "--dry-run", "--json"},
		{"--no-check", "sync", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v code = %d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
			t.Fatalf("%v JSON decode: %v\n%s", args, err, stdout.String())
		}
		for _, field := range []string{"dry_run", "applied", "updated", "rendered", "warnings", "actions", "canonical_resources", "agent_output_audit"} {
			if _, ok := payload[field]; !ok {
				t.Fatalf("%v missing existing or additive field %q: %#v", args, field, payload)
			}
		}
		auditPayload, ok := payload["agent_output_audit"].(map[string]any)
		if !ok {
			t.Fatalf("%v audit summary = %#v", args, payload["agent_output_audit"])
		}
		for _, field := range []string{"warning_count", "unmanaged_skill_count", "unique_skill_count", "missing_marker_count", "inspection_error_count"} {
			if auditPayload[field] != float64(0) {
				t.Fatalf("%v %s = %#v", args, field, auditPayload[field])
			}
		}
		if auditPayload["migratable"] != false {
			t.Fatalf("%v migratable = %#v", args, auditPayload["migratable"])
		}
		if agents, ok := auditPayload["agents"].([]any); !ok || len(agents) != 0 {
			t.Fatalf("%v empty agents = %#v", args, auditPayload["agents"])
		}
		if items, ok := auditPayload["items"].([]any); !ok || len(items) != 0 {
			t.Fatalf("%v empty items = %#v", args, auditPayload["items"])
		}
	}
}

func TestCLISyncJSONReportsMixedAuditFindingsAndLegacyWarnings(t *testing.T) {
	root, configPath := setupEnv(t)
	writeCLIConfig(t, configPath, map[string]string{
		"unmanaged": filepath.Join(root, "outputs", "unmanaged"),
		"missing":   filepath.Join(root, "outputs", "missing"),
		"broken":    filepath.Join(root, "outputs", "broken-file"),
	})
	source := filepath.Join(root, "source", "managed")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "---\nname: Managed\n---\n\nManaged.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--no-check", "add", source, "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	writeTestFile(t, filepath.Join(root, "outputs", "unmanaged", "local", "SKILL.md"), "# Local\n", 0o644)
	writeTestFile(t, filepath.Join(root, "outputs", "missing", "managed", "SKILL.md"), "# Unmarked copy\n", 0o644)
	writeTestFile(t, filepath.Join(root, "outputs", "broken-file"), "not a directory", 0o644)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-check", "sync", "--dry-run", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync JSON code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("sync JSON decode: %v\n%s", err, stdout.String())
	}
	auditPayload := payload["agent_output_audit"].(map[string]any)
	for field, want := range map[string]float64{
		"warning_count":          3,
		"unmanaged_skill_count":  1,
		"unique_skill_count":     2,
		"missing_marker_count":   1,
		"inspection_error_count": 1,
	} {
		if auditPayload[field] != want {
			t.Fatalf("%s = %#v, want %v; audit=%#v", field, auditPayload[field], want, auditPayload)
		}
	}
	if auditPayload["migratable"] != true || len(auditPayload["agents"].([]any)) != 3 || len(auditPayload["items"].([]any)) != 3 {
		t.Fatalf("mixed audit shape = %#v", auditPayload)
	}
	warnings, ok := payload["warnings"].([]any)
	if !ok || len(warnings) != 3 {
		t.Fatalf("legacy warnings = %#v", payload["warnings"])
	}
	for _, want := range []string{"not installed in Upskill", "missing Upskill render marker", "could not inspect agent output directory"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("legacy JSON warnings missing %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("JSON audit leaked human headings to stderr: %q", stderr.String())
	}
}

func TestPhaseAwareReportingPreservesDomainPipelineSideEffects(t *testing.T) {
	baselineRoot := filepath.Join(t.TempDir(), "baseline")
	baselineConfig := writeReportingSideEffectFixture(t, baselineRoot)
	t.Setenv("UPSKILL_HOME", filepath.Join(baselineRoot, "home"))
	t.Setenv("UPSKILL_CONFIG", baselineConfig)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := library.InstallSource(cfg, "fixture", library.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range installed {
		if _, err := (render.Renderer{Config: cfg}).Render(render.Options{Skill: item.StoreName, Copy: true}); err != nil {
			t.Fatal(err)
		}
	}
	_ = audit.AgentOutputWarnings(cfg, "")
	baseline := reportingSideEffectSnapshot(t, baselineRoot)

	sectionedRoot := filepath.Join(t.TempDir(), "sectioned")
	sectionedConfig := writeReportingSideEffectFixture(t, sectionedRoot)
	t.Setenv("UPSKILL_HOME", filepath.Join(sectionedRoot, "home"))
	t.Setenv("UPSKILL_CONFIG", sectionedConfig)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--no-check", "add", "fixture"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sectioned add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	sectioned := reportingSideEffectSnapshot(t, sectionedRoot)
	if !reflect.DeepEqual(sectioned, baseline) {
		t.Fatalf("reporting changed command side effects\nbaseline: %#v\nsectioned: %#v", baseline, sectioned)
	}
}

func writeReportingSideEffectFixture(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "source")
	output := filepath.Join(root, "agent-output")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nStable.\n", 0o644)
	writeTestFile(t, filepath.Join(output, "unmanaged", "SKILL.md"), "# Unmanaged\n", 0o644)
	configPath := filepath.Join(root, "config.json")
	data, err := json.Marshal(map[string]any{
		"agents":  map[string]string{"codex": output},
		"sources": map[string]string{"fixture": source},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, configPath, string(data), 0o644)
	return configPath
}

func reportingSideEffectSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.Name() == render.MarkerFile {
			var marker map[string]any
			if err := json.Unmarshal(data, &marker); err != nil {
				return err
			}
			delete(marker, "rendered_at")
			data, err = json.Marshal(marker)
			if err != nil {
				return err
			}
		} else if entry.Name() == "upskill-lock.json" {
			var lock map[string]any
			if err := json.Unmarshal(data, &lock); err != nil {
				return err
			}
			if skills, ok := lock["skills"].(map[string]any); ok {
				for _, raw := range skills {
					if skill, ok := raw.(map[string]any); ok {
						delete(skill, "source_id")
						delete(skill, "updated_at")
					}
				}
			}
			data, err = json.Marshal(lock)
			if err != nil {
				return err
			}
		}
		snapshot[relative] = strings.ReplaceAll(string(data), root, "<ROOT>")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeCLIConfig(t *testing.T, path string, agents map[string]string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"agents": agents})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, string(data), 0o644)
}

func mixedHumanAuditSummary() audit.AgentOutputAuditSummary {
	return audit.SummarizeAgentOutputWarnings([]audit.AgentOutputSkillWarning{
		{Agent: "codex", OutputPath: "/agents/codex", Skill: "shared", SkillPath: "/agents/codex/shared", Reason: audit.ReasonNotInstalled},
		{Agent: "claude-code", OutputPath: "/agents/claude", Skill: "shared", SkillPath: "/agents/claude/shared", Reason: audit.ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/agents/codex", Skill: "managed", SkillPath: "/agents/codex/managed", Reason: audit.ReasonMissingRenderMarker},
		{Agent: "claude-code", OutputPath: "/agents/claude", Reason: "could not inspect agent output directory: permission denied"},
	})
}

func assertPlainStaticOutput(t *testing.T, output string) {
	t.Helper()
	if strings.Contains(output, "\x1b[") {
		t.Fatalf("output contains ANSI escape: %q", output)
	}
	if strings.ContainsRune(output, '\r') {
		t.Fatalf("output contains carriage return: %q", output)
	}
}

func TestCLICollectorFlushesDiagnosticsOnceInLifecycleOrder(t *testing.T) {
	collector := newCLIDiagnosticCollector()
	collector.Add(diagnostic.Diagnostic{
		Phase:    diagnostic.PhasePreflight,
		Severity: diagnostic.SeverityNotice,
		Code:     diagnostic.CodeUpdateAvailable,
		Subject:  "redis",
		Message:  "update available",
		Details:  map[string]any{},
		Actions:  []diagnostic.Action{},
	})
	collector.AddAudit("codex", cliAuditConcise, audit.SummarizeAgentOutputWarnings([]audit.AgentOutputSkillWarning{{
		Agent:      "codex",
		OutputPath: "/tmp/codex",
		Skill:      "local-skill",
		SkillPath:  "/tmp/codex/local-skill",
		Reason:     audit.ReasonNotInstalled,
	}}))

	var combined bytes.Buffer
	combined.WriteString("primary result\n")
	collector.Flush(&combined)
	collector.Flush(&combined)

	output := combined.String()
	if strings.Count(output, "primary result") != 1 ||
		strings.Count(output, humanSectionPreflight) != 1 ||
		strings.Count(output, humanSectionAudit) != 1 {
		t.Fatalf("collector did not flush each item exactly once:\n%s", output)
	}
	resultIndex := strings.Index(output, "primary result")
	preflightIndex := strings.Index(output, humanSectionPreflight)
	auditIndex := strings.Index(output, humanSectionAudit)
	if resultIndex >= preflightIndex || preflightIndex >= auditIndex {
		t.Fatalf("lifecycle order = result:%d preflight:%d audit:%d\n%s", resultIndex, preflightIndex, auditIndex, output)
	}
}

func TestCLIDefersPreflightAndAuditUntilAfterPrimaryResult(t *testing.T) {
	root := setupCLIOutputOrderingFixture(t, config.UpdatePolicyManual)

	var combined bytes.Buffer
	if code := Run([]string{"render", "redis"}, &combined, &combined); code != 0 {
		t.Fatalf("render code = %d\n%s", code, combined.String())
	}
	output := combined.String()
	resultIndex := strings.Index(output, filepath.Join(root, "home", "rendered", "codex", "redis"))
	preflightIndex := strings.Index(output, humanSectionPreflight)
	auditIndex := strings.Index(output, humanSectionAudit)
	if resultIndex < 0 || preflightIndex < 0 || auditIndex < 0 {
		t.Fatalf("combined output missing lifecycle content:\n%s", output)
	}
	if resultIndex >= preflightIndex || preflightIndex >= auditIndex {
		t.Fatalf("combined lifecycle order = result:%d preflight:%d audit:%d\n%s", resultIndex, preflightIndex, auditIndex, output)
	}
}

func TestCLISeparatesPrimaryAndDiagnosticStreams(t *testing.T) {
	root := setupCLIOutputOrderingFixture(t, config.UpdatePolicyManual)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"render", "redis"}, &stdout, &stderr); code != 0 {
		t.Fatalf("render code = %d stderr=%s", code, stderr.String())
	}
	renderedPath := filepath.Join(root, "home", "rendered", "codex", "redis")
	if !strings.Contains(stdout.String(), renderedPath) {
		t.Fatalf("stdout missing primary render result: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), humanSectionPreflight) || strings.Contains(stdout.String(), humanSectionAudit) {
		t.Fatalf("stdout contains passive diagnostics: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), renderedPath) {
		t.Fatalf("stderr contains primary render result: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), humanSectionPreflight) ||
		!strings.Contains(stderr.String(), humanSectionAudit) {
		t.Fatalf("stderr missing deferred diagnostics: %q", stderr.String())
	}
}

func TestCLIPrimaryFailurePrecedesPassivePreflight(t *testing.T) {
	setupCLIOutputOrderingFixture(t, config.UpdatePolicyManual)

	var combined bytes.Buffer
	if code := Run([]string{"show", "missing"}, &combined, &combined); code == 0 {
		t.Fatalf("show unexpectedly succeeded:\n%s", combined.String())
	}
	output := combined.String()
	errorIndex := strings.Index(output, "upskill:")
	preflightIndex := strings.Index(output, humanSectionPreflight)
	if errorIndex < 0 || preflightIndex < 0 || errorIndex >= preflightIndex {
		t.Fatalf("primary error did not precede passive preflight: error=%d preflight=%d\n%s", errorIndex, preflightIndex, output)
	}
}

func TestCLIFatalPreflightPreventsCommandDispatch(t *testing.T) {
	root := setupCLIOutputOrderingFixture(t, config.UpdatePolicyAuto)
	if err := os.RemoveAll(filepath.Join(root, "source")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"list"}, &stdout, &stderr); code != 1 {
		t.Fatalf("fatal preflight code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("fatal preflight dispatched list: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "automatic update check failed for redis") {
		t.Fatalf("stderr missing fatal preflight error: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), humanSectionPreflight) || strings.Contains(stderr.String(), humanSectionAudit) {
		t.Fatalf("fatal preflight flushed passive diagnostics: %q", stderr.String())
	}
}

func TestCLIAutomaticUpdateDiagnosticRemainsPreflight(t *testing.T) {
	setupCLIOutputOrderingFixture(t, config.UpdatePolicyAuto)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	result := runCLIPreflight(cfg, []string{"list"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	found := false
	for _, item := range result.Diagnostics {
		if item.Code != diagnostic.CodeAutomaticallyUpdated {
			continue
		}
		found = true
		if item.Phase != diagnostic.PhasePreflight || item.Subject != "redis" {
			t.Fatalf("automatic update diagnostic = %#v", item)
		}
	}
	if !found {
		t.Fatalf("automatic update diagnostic missing: %#v", result.Diagnostics)
	}
}

func TestCLIRunsOneRemotePreflightCheckPerInvocation(t *testing.T) {
	root, _ := setupEnv(t)
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	runCLIGit(t, "", "init", "--bare", remote)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLIGit(t, work, "init")
	writeTestFile(t, filepath.Join(work, "skills", "redis", "SKILL.md"), "---\nname: Redis\n---\n\nRedis.\n", 0o644)
	runCLIGit(t, work, "add", ".")
	runCLIGit(t, work, "commit", "-m", "initial")
	runCLIGit(t, work, "branch", "-M", "main")
	runCLIGit(t, work, "remote", "add", "origin", remote)
	runCLIGit(t, work, "push", "-u", "origin", "main")
	runCLIGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", "file://" + remote, "--ref", "main", "--subpath", "skills", "--skill", "redis", "--update-policy", "auto", "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	countPath := installCLIGitCounter(t)
	clearCLIGitOperationCounts(t, countPath)
	t.Setenv("UPSKILL_PREFLIGHT_CHECK_TTL", "0")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if counts := cliGitOperationCounts(t, countPath); counts["ls-remote"] != 1 {
		t.Fatalf("preflight Git operations = %#v, want one ls-remote", counts)
	}
}

func setupCLIOutputOrderingFixture(t *testing.T, policy string) string {
	t.Helper()
	root, _ := setupEnv(t)
	source := filepath.Join(root, "source")
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nOld.\n", 0o644)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"add", source, "--skill", "redis", "--update-policy", policy, "--no-render"}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	writeTestFile(t, filepath.Join(source, "redis", "SKILL.md"), "---\nname: Redis\n---\n\nNew.\n", 0o644)
	writeTestFile(t, filepath.Join(root, "codex-skills", "unmanaged", "SKILL.md"), "---\nname: Unmanaged\n---\n", 0o644)
	return root
}
