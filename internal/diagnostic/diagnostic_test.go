package diagnostic

import (
	"reflect"
	"testing"

	"upskill/internal/config"
	"upskill/internal/library"
)

func TestDiagnosticConstantsAreValid(t *testing.T) {
	for _, phase := range []Phase{PhasePreflight, PhaseAudit} {
		if !phase.Valid() {
			t.Fatalf("phase %q should be valid", phase)
		}
	}
	if Phase("other").Valid() {
		t.Fatal("unknown phase should be invalid")
	}

	for _, severity := range []Severity{SeverityNotice, SeverityWarning} {
		if !severity.Valid() {
			t.Fatalf("severity %q should be valid", severity)
		}
	}
	if Severity("other").Valid() {
		t.Fatal("unknown severity should be invalid")
	}

	for _, code := range []Code{
		CodeUpdateCheckFailed,
		CodeRemoteCheckDeferred,
		CodeRemoteCheckFailed,
		CodeCacheIgnored,
		CodeUpdateAvailable,
		CodeAutomaticUpdatePending,
		CodeAutomaticallyUpdated,
		CodeUnmanagedAgentOutput,
		CodeMissingRenderMarker,
		CodeAgentOutputInspectionFailed,
	} {
		if !code.Valid() {
			t.Fatalf("code %q should be valid", code)
		}
	}
	if Code("other").Valid() {
		t.Fatal("unknown code should be invalid")
	}
}

func TestFromUpdateResultMapsAndSortsDiagnostics(t *testing.T) {
	result := library.UpdateCheckResult{
		Warnings: []library.UpdateCheckWarning{
			{Type: library.UpdateCheckWarningRemoteDeferred, SourceKey: "source-b", Message: "automatic Git update check deferred"},
			{Type: library.UpdateCheckWarningRemoteFailed, SourceKey: "source-a", Message: "automatic Git update check failed"},
			{Type: library.UpdateCheckWarningCacheIgnored, SourceKey: "source-c", Message: "update-check cache ignored"},
		},
		Checks: []library.UpdateCheck{
			{StoreName: "failed", Error: "locked skill not found in source", CheckOrigin: library.UpdateCheckOriginLiveLocal},
			{StoreName: "manual", Changed: true, UpdatePolicy: config.UpdatePolicyManual, CheckOrigin: library.UpdateCheckOriginCache, CheckedAt: "2026-07-31T12:00:00Z"},
			{StoreName: "auto", Changed: true, UpdatePolicy: config.UpdatePolicyAuto, CheckOrigin: library.UpdateCheckOriginLockRevision},
			{StoreName: "stale", Stale: true, UpdatePolicy: config.UpdatePolicyAuto, CheckOrigin: library.UpdateCheckOriginCache},
			{StoreName: "done", Changed: true, UpdatePolicy: config.UpdatePolicyAuto, CheckOrigin: library.UpdateCheckOriginLiveGit},
		},
	}
	updated := []library.InstalledSkill{{StoreName: "done", Hash: "sha256:done"}}

	resultBefore := cloneUpdateCheckResult(result)
	updatedBefore := append([]library.InstalledSkill(nil), updated...)
	diagnostics := FromUpdateResult(result, updated)

	if !reflect.DeepEqual(result, resultBefore) {
		t.Fatalf("result mutated:\n got: %#v\nwant: %#v", result, resultBefore)
	}
	if !reflect.DeepEqual(updated, updatedBefore) {
		t.Fatalf("updated mutated:\n got: %#v\nwant: %#v", updated, updatedBefore)
	}

	wantOrder := []struct {
		code    Code
		subject string
	}{
		{CodeUpdateCheckFailed, "failed"},
		{CodeRemoteCheckFailed, "source-a"},
		{CodeRemoteCheckDeferred, "source-b"},
		{CodeCacheIgnored, "source-c"},
		{CodeAutomaticUpdatePending, "auto"},
		{CodeAutomaticallyUpdated, "done"},
		{CodeUpdateAvailable, "manual"},
	}
	if len(diagnostics) != len(wantOrder) {
		t.Fatalf("diagnostic count = %d, want %d: %#v", len(diagnostics), len(wantOrder), diagnostics)
	}
	for index, want := range wantOrder {
		got := diagnostics[index]
		if got.Code != want.code || got.Subject != want.subject {
			t.Fatalf("diagnostic %d = %s/%s, want %s/%s", index, got.Code, got.Subject, want.code, want.subject)
		}
		if got.Phase != PhasePreflight || !got.Severity.Valid() || !got.Code.Valid() {
			t.Fatalf("diagnostic %d has invalid classification: %#v", index, got)
		}
	}

	deferred := findDiagnostic(t, diagnostics, CodeRemoteCheckDeferred)
	if stale, ok := deferred.Details["stale"].(bool); !ok || !stale {
		t.Fatalf("deferred stale detail = %#v", deferred.Details["stale"])
	}

	failed := findDiagnostic(t, diagnostics, CodeUpdateCheckFailed)
	if failed.Message != "locked skill not found in source" || failed.Details["check_origin"] != library.UpdateCheckOriginLiveLocal {
		t.Fatalf("failed diagnostic = %#v", failed)
	}

	manual := findDiagnostic(t, diagnostics, CodeUpdateAvailable)
	wantManualAction := []Action{{Label: "next", Command: []string{"upskill", "update", "--diff", "manual"}}}
	if !reflect.DeepEqual(manual.Actions, wantManualAction) || manual.Details["checked_at"] != "2026-07-31T12:00:00Z" {
		t.Fatalf("manual diagnostic = %#v", manual)
	}

	automatic := findDiagnostic(t, diagnostics, CodeAutomaticUpdatePending)
	wantAutomaticAction := []Action{{Label: "next", Command: []string{"upskill", "sync"}}}
	if !reflect.DeepEqual(automatic.Actions, wantAutomaticAction) {
		t.Fatalf("automatic actions = %#v", automatic.Actions)
	}

	completed := findDiagnostic(t, diagnostics, CodeAutomaticallyUpdated)
	if completed.Details["hash"] != "sha256:done" || len(completed.Actions) != 0 {
		t.Fatalf("completed diagnostic = %#v", completed)
	}
}

func TestFromUpdateResultReturnsNonNilEmptySlice(t *testing.T) {
	diagnostics := FromUpdateResult(library.UpdateCheckResult{}, nil)
	if diagnostics == nil || len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want non-nil empty slice", diagnostics)
	}
}

func TestFromUpdateResultMapsUnknownWarningToStableCode(t *testing.T) {
	diagnostics := FromUpdateResult(library.UpdateCheckResult{
		Warnings: []library.UpdateCheckWarning{{Type: "future_warning", Message: "future warning"}},
	}, nil)
	if len(diagnostics) != 1 || diagnostics[0].Code != CodeUpdateCheckFailed || !diagnostics[0].Code.Valid() {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func findDiagnostic(t *testing.T, diagnostics []Diagnostic, code Code) Diagnostic {
	t.Helper()
	for _, item := range diagnostics {
		if item.Code == code {
			return item
		}
	}
	t.Fatalf("diagnostic %q not found in %#v", code, diagnostics)
	return Diagnostic{}
}

func cloneUpdateCheckResult(result library.UpdateCheckResult) library.UpdateCheckResult {
	return library.UpdateCheckResult{
		Checks:   append([]library.UpdateCheck(nil), result.Checks...),
		Warnings: append([]library.UpdateCheckWarning(nil), result.Warnings...),
	}
}
