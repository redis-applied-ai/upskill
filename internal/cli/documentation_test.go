package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"upskill/internal/audit"
)

func TestPhaseAwareCommandHelpContracts(t *testing.T) {
	tests := []struct {
		command string
		help    string
		want    []string
	}{
		{
			command: "sync",
			help:    helpSync,
			want:    []string{"PLAN labels", "do not write", "agent_output_audit", "warnings"},
		},
		{
			command: "render",
			help:    helpRender,
			want:    []string{"managed rendered paths", "configured copied paths", "--copy=false reports not copied", "--print writes only file content to stdout"},
		},
		{
			command: "migrate",
			help:    helpMigrate,
			want:    []string{"Without --yes", "previews the plan", "read-only", "leave unmanaged skills unchanged"},
		},
		{
			command: "status",
			help:    helpStatus,
			want:    []string{"detailed agent-output audit", "missing render markers", "inspection failures", "read-only", "add --yes to apply"},
		},
		{
			command: "doctor",
			help:    helpDoctor,
			want:    []string{"detailed agent-output audit", "missing render markers", "inspection failures", "does not change those skills", "add --yes to apply"},
		},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			normalizedHelp := strings.Join(strings.Fields(test.help), " ")
			for _, want := range test.want {
				if !strings.Contains(normalizedHelp, want) {
					t.Fatalf("%s help missing %q:\n%s", test.command, want, test.help)
				}
			}
			var stdout, stderr bytes.Buffer
			if code := Run([]string{test.command, "--help"}, &stdout, &stderr); code != 0 {
				t.Fatalf("%s --help code = %d stderr=%s", test.command, code, stderr.String())
			}
			if stdout.String() != test.help+"\n" {
				t.Fatalf("%s routed help differs from constant", test.command)
			}
			if stderr.Len() != 0 {
				t.Fatalf("%s help wrote stderr: %q", test.command, stderr.String())
			}
		})
	}
}

func TestPhaseAwareDocumentationMatchesFormatterAndActions(t *testing.T) {
	paths := []string{
		"../../README.md",
		"../../SPEC.md",
		"../../docs/workflows.md",
		"../../docs/phase-aware-command-output-spec.md",
	}
	combined := ""
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		combined += "\n" + content
		for _, section := range []string{humanSectionResult, humanSectionPreflight, humanSectionAudit} {
			if !strings.Contains(content, section) {
				t.Fatalf("%s missing formatter section %q", path, section)
			}
		}
	}

	summary := audit.SummarizeAgentOutputWarnings([]audit.AgentOutputSkillWarning{
		{Agent: "codex", OutputPath: "/codex", Skill: "local", SkillPath: "/codex/local", Reason: audit.ReasonNotInstalled},
		{Agent: "codex", OutputPath: "/codex", Skill: "managed", SkillPath: "/codex/managed", Reason: audit.ReasonMissingRenderMarker},
		{Agent: "claude-code", OutputPath: "/claude", Reason: "could not inspect agent output directory"},
	})
	for _, item := range summary.Diagnostics("") {
		for _, action := range item.Actions {
			command := formatCommand(action.Command)
			if !strings.Contains(combined, command) {
				t.Fatalf("documentation missing formatter action %q", command)
			}
		}
	}

	readme := strings.Join(strings.Fields(readDocumentationFile(t, "../../README.md")), " ")
	if !strings.Contains(readme, "The audit is read-only: `add`, `status`, and `doctor` leave those skills unchanged") {
		t.Fatal("README does not state that add leaves unmanaged audit findings unchanged")
	}
	workflows := strings.Join(strings.Fields(readDocumentationFile(t, "../../docs/workflows.md")), " ")
	if !strings.Contains(workflows, "`upskill migrate` to preview") ||
		!strings.Contains(workflows, "`upskill migrate --yes` to apply") {
		t.Fatal("workflow does not distinguish migration preview from apply")
	}
	for _, want := range []string{"stdout", "stderr", "executes preflight before", "primary result"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("documentation missing lifecycle or stream term %q", want)
		}
	}
}

func readDocumentationFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
