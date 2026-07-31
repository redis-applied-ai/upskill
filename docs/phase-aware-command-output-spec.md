# Phase-Aware Command Output Spec

## Status

Draft.

## Summary

Upskill should make the result of a command obvious even when update checks,
rendering, and agent-output audits produce additional messages.

The current CLI preserves a useful machine boundary: primary output goes to
stdout, while notices and warnings go to stderr. That boundary does not solve
the human problem. A terminal presents both streams together, so an ordinary
`upskill add` can look like one long list of unrelated warnings and paths. The
user cannot quickly tell whether the requested skill was installed, where it
was rendered, whether any warning affected the requested operation, or which
lines came from checks outside that operation.

This feature introduces phase-aware command reporting:

- the primary result appears first after a successful operation;
- non-fatal preflight diagnostics appear in a labeled `Preflight` section;
- post-operation agent-output findings appear in a labeled
  `Agent output audit` section;
- render results label the managed render path and copied agent-output path;
- ordinary mutation commands summarize repeated audit findings by agent;
- `status` and `doctor` retain detailed audit findings for diagnosis;
- structured CLI and MCP results receive an additive agent-output audit
  summary while preserving existing fields;
- stdout, stderr, exit-code, config, lock-file, and generated-file semantics do
  not change.

The first version uses plain text labels and indentation. Meaning must not
depend on color, Unicode symbols, terminal width, or TTY detection.

## Problem

The current `add` lifecycle is:

```text
cli.Run
  -> runCLIPreflight
    -> print update warnings and notices to stderr
  -> runAdd
    -> print installed skill and digest to stdout
    -> renderInstalled
      -> print render and copy paths to stdout
      -> warnAgentOutputSkills
        -> print one full warning per audit finding to stderr
```

For the motivating command, the terminal showed four kinds of information
without structural separation:

1. an update-check failure for `agent-plan` from implicit preflight;
2. an available update for `categorizing-transactions` from implicit
   preflight;
3. the successful `agent-plan` install and two render/copy results;
4. eight unmanaged agent-output occurrences from a post-render audit.

The command succeeded, but the first visible line said `warning`, the successful
result was buried in the middle, the render syntax did not label the two paths,
and the audit repeated a long sentence for every occurrence. The final hint did
not explicitly say that the audit findings were left unchanged.

The implementation has three specific causes:

- preflight prints directly before command dispatch instead of returning
  structured diagnostics for later presentation;
- `printResults` formats `rendered_path (copied_path)`, which requires the user
  to know what the parentheses mean;
- `warnAgentOutputSkills` converts structured audit findings into one string
  per item instead of summarizing them. It also obtains structured warnings and
  then calls a helper that scans the agent outputs again to build messages.

This is a presentation and reporting problem, not an install, update, render,
or audit correctness problem.

## Goals

- Put the requested operation's successful result before non-fatal incidental
  diagnostics in a combined terminal view.
- Identify every incidental message by lifecycle phase.
- Make install, update, render, copy, skip, and no-change outcomes explicit.
- Distinguish the managed render cache from configured agent-output copies.
- Summarize repeated agent-output findings without hiding their severity or
  remediation.
- State when audit findings were observed but left unchanged.
- Preserve complete diagnostic detail through `upskill status` and
  `upskill doctor`.
- Preserve stdout for primary command output and stderr for notices, warnings,
  hints, and errors.
- Preserve byte-for-byte payload output for commands that return file content
  or JSON where compatibility requires it.
- Give structured CLI and MCP consumers stable audit counts and items without
  removing existing `warnings` or `notices` fields.
- Map one structured preflight or audit result into every presentation surface
  without repeating source checks or filesystem scans.
- Add deterministic backend, terminal UX, CLI, JSON, MCP, and end-to-end tests.

## Non-Goals

- Do not change how skills are installed, updated, rendered, copied, migrated,
  or removed.
- Do not change update policy, preflight freshness, caching, or error severity.
- Do not suppress preflight warnings or agent-output findings.
- Do not automatically migrate, delete, overwrite, or otherwise change an
  unmanaged skill.
- Do not add a web application or browser-based frontend.
- Do not add an HTTP API.
- Do not add a new config file field, lock-file field, persistent report, or
  telemetry event.
- Do not add color, animation, progress bars, spinners, or a terminal UI
  dependency in the first version.
- Do not make presentation depend on whether a stream is attached to a TTY.
- Do not delay confirmation prompts, destructive-action warnings, or errors
  that must appear before an operation proceeds.
- Do not redesign every existing table, help screen, draft message, policy
  notice, or preview action in this change.
- Do not remove legacy string warnings from JSON or MCP responses in the first
  version.

## Terminology

- **Execution order:** The order in which Upskill performs work: preflight,
  primary operation, then post-operation audit.
- **Presentation order:** The order in which a successful combined terminal
  view displays information: primary result, preflight diagnostics, then audit
  diagnostics.
- **Primary result:** Output requested by the command, such as installed skills,
  updated skills, rendered paths, copied paths, file content, or a list.
- **Preflight diagnostic:** A non-fatal update warning, update notice, or
  automatic-update result produced before the primary operation.
- **Agent-output audit:** The read-only inspection that detects unmanaged
  skills, installed skills without an Upskill render marker, and agent-output
  directories that could not be inspected.
- **Audit occurrence:** One finding in one configured agent output. The same
  skill found in both Claude Code and Codex counts as two occurrences.
- **Unique audit skill:** One normalized skill name regardless of the number of
  agent outputs in which it appears.
- **Detailed audit mode:** The output used by `status` and `doctor`, including
  agent names, skill names, paths, and reasons.
- **Concise audit mode:** The output used after ordinary mutations, including
  totals by category and agent but not one full sentence per occurrence.
- **Payload command:** A command whose stdout is itself data, such as `show`,
  `render --print`, or a supported JSON mode.
- **Fatal error:** A failure that prevents or invalidates the requested
  operation and retains its current nonzero exit code.

## Domain Ownership

| Domain | Responsibility in this feature |
| --- | --- |
| Backend | Structured diagnostic types, audit aggregation, deterministic sorting, and single-scan data flow |
| Frontend | Human terminal hierarchy, labels, indentation, detail policy, and accessibility |
| CLI | Collection timing, stdout/stderr routing, command integration, exit behavior, and payload compatibility |
| JSON CLI API | Additive structured audit summary for existing structured mutation output |
| MCP API | Preserve preflight `notices`, preserve legacy `warnings`, and add the same structured audit summary to sync results |
| Documentation | Explain phases, stream behavior, audit detail commands, and migration actions |

Upskill has no web frontend. The human terminal view is the frontend for this
feature. MCP and existing JSON modes are the programmatic API surfaces.

## Requirement Traceability

| Requirement | Design sections | Implementation and test tasks |
| --- | --- | --- |
| Separate preflight, result, and audit output | Presentation Contract; CLI Integration | B1, B2, C1, C2, F1, F2 |
| Make the requested result easy to identify | Result Hierarchy | F1, F2, C1, C2 |
| Reduce repeated unmanaged-skill warnings | Agent-Output Audit Summary | B3, B4, F3, F4 |
| Clarify rendered and copied paths | Render Result Semantics | F1, F2 |
| Preserve stdout/stderr and payload compatibility | Stream Contract; Command Behavior Matrix | C1, C2 |
| Cover JSON and MCP APIs | Structured API Contract | A1, A2 |
| Cover the complete lifecycle | End-to-End Scenarios; Rollout Plan | E1, E2, R1, R2 |

## Decision

Upskill will separate execution from presentation for passive diagnostics.
Preflight still executes before the primary operation, but successful commands
will display their primary result before deferred preflight and audit sections.

Fatal preflight failures remain immediate and stop command dispatch. Safety
prompts and warnings that must be read before a write also remain immediate.
Only passive, non-fatal diagnostics are deferred.

The human format will be stable plain text:

- section names use title case;
- section bodies use two-space indentation;
- status labels use `OK`, `PLAN`, `NOTICE`, and `WARNING`;
- labels, not color, communicate status;
- blank lines separate non-empty sections;
- empty sections are omitted;
- list order is deterministic;
- paths remain absolute so output can be copied without expansion rules;
- digests remain complete in the first version.

### Presentation Contract

On successful commands, the combined presentation order is:

```text
Result
Preflight
Agent output audit
```

Only non-empty sections appear.

On a primary-operation failure after a non-fatal preflight, the order is:

```text
Error
Preflight
```

No `Result` section appears. An audit does not run after a failed render or
copy operation unless the current command already guarantees that behavior.

On a fatal preflight failure, Upskill prints the existing error and exits before
the primary operation. It does not print an empty result or audit section.

Automatic updates that happen during preflight remain classified as preflight
even though they modify installed state and rendered output. Their diagnostic
must say `automatically updated`; it must not look like part of the requested
operation.

### Motivating Example

The motivating command should produce this combined human view. Paths and the
digest below are illustrative, but labels and ordering are normative.

```text
Result
  OK installed agent-plan
    digest: sha256:9ad1302bc323cabe82a8803ddf5cdbe682c1b14b0d205c6ed83ee023baf58887
  OK rendered agent-plan for claude-code
    rendered: /Users/example/.skills/rendered/claude-code/agent-plan
    copied: /Users/example/.claude/skills/agent-plan
  OK rendered agent-plan for codex
    rendered: /Users/example/.skills/rendered/codex/agent-plan
    copied: /Users/example/.codex/skills/agent-plan

Preflight
  WARNING agent-plan: update check failed: locked skill not found in source
  NOTICE categorizing-transactions: update available
    next: upskill update --diff categorizing-transactions

Agent output audit
  WARNING found 8 unmanaged skill occurrences across 2 agents (5 unique skills)
    claude-code: 3
    codex: 5
    unmanaged skills were left unchanged
    details: upskill doctor
    preview migration: upskill migrate
    apply migration: upskill migrate --yes
```

The output must not say or imply that the `agent-plan` installation failed.
The audit text must not imply that `upskill add` changed the unmanaged skills.

## Backend Design

### Structured Diagnostic Model

Add a focused internal diagnostic model. It must contain facts and recommended
actions without owning terminal styling.

One suitable shape is:

```go
type Phase string

const (
    PhasePreflight Phase = "preflight"
    PhaseAudit     Phase = "audit"
)

type Severity string

const (
    SeverityNotice  Severity = "notice"
    SeverityWarning Severity = "warning"
)

type Action struct {
    Label   string   `json:"label"`
    Command []string `json:"command"`
}

type Diagnostic struct {
    Phase    Phase          `json:"phase"`
    Severity Severity       `json:"severity"`
    Code     string         `json:"code"`
    Subject  string         `json:"subject,omitempty"`
    Message  string         `json:"message"`
    Details  map[string]any `json:"details,omitempty"`
    Actions  []Action       `json:"actions,omitempty"`
}
```

The exact package name may follow repository conventions. Requirements matter
more than the name:

- `phase`, `severity`, and `code` are enums or validated constants;
- `code` is stable and machine-safe;
- display strings are derived from structured fields;
- command actions use argument arrays internally so user-controlled values are
  not assembled into misleading shell text;
- actions are recommendations only and are never executed by the reporter;
- empty slices and maps normalize consistently for API output;
- diagnostics sort deterministically by phase, severity, subject, code, and
  message where source order is not meaningful.

Required initial codes:

| Code | Phase | Severity |
| --- | --- | --- |
| `update_check_failed` | preflight | warning |
| `remote_check_deferred` | preflight | warning |
| `remote_check_failed` | preflight | warning |
| `cache_ignored` | preflight | warning |
| `update_available` | preflight | notice |
| `automatic_update_pending` | preflight | notice |
| `automatically_updated` | preflight | notice |
| `unmanaged_agent_output` | audit | warning |
| `missing_render_marker` | audit | warning |
| `agent_output_inspection_failed` | audit | warning |

The model must not replace domain result types such as
`library.UpdateCheckResult`, `library.InstalledSkill`, `render.Result`, or
`audit.AgentOutputSkillWarning`. Small adapters map those types into
diagnostics and summaries.

### Preflight Collection

Refactor CLI preflight so non-fatal findings are returned rather than printed
as they are discovered.

The preflight result should distinguish:

- a fatal error and exit code;
- automatically updated installed skills;
- deferred warnings;
- available-update notices;
- source/cache warning metadata already exposed by the library.

The CLI must call `AutoUpdateSkills` or `CheckSkillUpdatesForPreflight` exactly
once per existing preflight path. Formatting must not repeat an update check.

`--no-check` and `UPSKILL_NO_CHECK=1` continue to return an empty preflight
result. Commands that already skip implicit preflight continue to skip it.

### Agent-Output Audit Summary

Build one summary from one call to `audit.AgentOutputWarnings`:

```go
type AgentOutputAuditSummary struct {
    WarningCount          int                       `json:"warning_count"`
    UnmanagedSkillCount   int                       `json:"unmanaged_skill_count"`
    UniqueSkillCount      int                       `json:"unique_skill_count"`
    MissingMarkerCount    int                       `json:"missing_marker_count"`
    InspectionErrorCount  int                       `json:"inspection_error_count"`
    Migratable            bool                      `json:"migratable"`
    Agents                []AgentOutputAgentSummary `json:"agents"`
    Items                 []AgentOutputSkillWarning `json:"items"`
}

type AgentOutputAgentSummary struct {
    Agent                string   `json:"agent"`
    OutputPath           string   `json:"output_path"`
    WarningCount         int      `json:"warning_count"`
    UnmanagedSkillCount  int      `json:"unmanaged_skill_count"`
    MissingMarkerCount   int      `json:"missing_marker_count"`
    InspectionErrorCount int      `json:"inspection_error_count"`
    Skills               []string `json:"skills"`
}
```

The exact Go field names may vary, but the JSON field names are the target API
contract.

Counting rules:

- `warning_count` counts every item, including directory inspection failures;
- `unmanaged_skill_count` counts named items whose reason is “not installed in
  Upskill from any configured source”;
- `missing_marker_count` counts installed named items without the Upskill
  render marker;
- `inspection_error_count` counts items without a skill name caused by an
  unreadable or otherwise uninspectable output directory;
- `unique_skill_count` counts distinct non-empty skill names across agents and
  reasons;
- `migratable` is true when at least one unmanaged skill occurrence exists;
- agent counts describe occurrences in that configured output;
- agent and skill lists are sorted;
- `items` preserves the existing detailed fields and deterministic order;
- an empty audit uses zero counts and non-nil empty `agents` and `items` in
  structured mutation results.

The summary builder must not read the filesystem. It receives the findings from
one audit scan. String compatibility helpers may format the same `Items` slice;
they must not call `AgentOutputWarnings` again.

### Audit Remediation

The human reporter chooses remediation from summary categories:

- unmanaged skills: `upskill migrate` for preview and
  `upskill migrate --yes` to apply;
- missing render markers: `upskill sync` or the agent-limited equivalent;
- inspection failures: show the failed agent output and reason in detailed mode
  without suggesting migration as a fix;
- mixed findings: show every applicable action once.

When the command was limited with `--agent`, generated actions must include the
same agent selector. Action rendering must quote an unusual agent name safely.

### Render Result Semantics

The renderer already returns enough data to explain what happened. Human output
must map it explicitly:

| `render.Result` state | Human meaning |
| --- | --- |
| `RenderedPath` | `rendered:` managed generated path |
| each `CopiedTo` item | `copied:` configured agent-output path |
| empty `CopiedTo` | `not copied` |
| `Skipped=true` with copied paths | rendered content unchanged; managed copy verified or refreshed according to current renderer behavior |
| `Skipped=true` without copied paths | unchanged and not copied |
| preview mode | planned render/copy, not completed work |

The formatter must not infer filesystem writes beyond the fields returned by
the renderer. It must preserve the full digest and full paths.

## Frontend And Human Terminal UX

### Result Hierarchy

Mutation result output should answer these questions in order:

1. Did the requested operation succeed?
2. Which skill or resource changed?
3. What was rendered?
4. Where was it copied?
5. What incidental conditions need attention?

The result section uses one status line per logical outcome and indented detail
lines. Repeated paths must not be compressed into unlabeled parentheses.

For multiple skills or agents:

- sort skills by store name;
- sort agents by configured agent name;
- preserve multiple copied paths in sorted order;
- repeat `copied:` for each destination;
- do not collapse distinct failures into counts.

### Concise Audit Mode

`add`, non-preview `sync`, `update` when it renders, and `render --copy=true`
use concise audit mode.

Concise mode shows:

- total occurrences by category;
- number of affected agents;
- number of unique named skills;
- one count per agent;
- an explicit statement that findings were left unchanged;
- applicable next commands once.

It does not show every absolute skill path. Users can run `upskill doctor` for
that detail.

### Detailed Audit Mode

`status` and `doctor` use detailed audit mode. They group findings by agent and
show:

- configured output root;
- each skill name and path;
- the reason for each finding;
- inspection failures;
- applicable next commands.

Detailed mode replaces repeated `warning:` prose with one section and nested
items. It does not reduce or omit diagnostic information.

### Accessibility And Portability

- Use ASCII status words. Do not require symbols to understand the output.
- Do not rely on red, yellow, or green.
- Do not truncate paths based on terminal width.
- Do not overwrite lines with carriage returns.
- Keep one fact per line so screen readers and logs remain understandable.
- Render the same hierarchy when output is redirected.
- Keep commands copyable as plain text.

Color may be considered later only as optional reinforcement with `NO_COLOR`
support. It is not part of this feature.

## CLI Integration

### Stream Contract

The existing stream boundary remains authoritative:

- stdout contains the primary result or requested payload;
- stderr contains preflight sections, audit sections, hints, safety prompts,
  and errors;
- JSON stdout remains valid JSON without terminal headings;
- exit codes keep their current meaning.

For sectioned mutation text, `Result` and its body are written to stdout.
`Preflight` and `Agent output audit` are written to stderr after the primary
operation returns. This produces the intended order when the process writes
both streams to one terminal while preserving redirection behavior.

The CLI entry point should own a deferred diagnostic collector for one command
invocation. Command handlers add passive diagnostics to it instead of printing
them directly. `Run` flushes the collector exactly once before returning.

Immediate errors and safety messages continue to write directly to stderr.

### Command Behavior Matrix

| Command class | Primary stdout | Deferred preflight | Audit detail |
| --- | --- | --- | --- |
| `add` | Sectioned install and render result | After result | Concise after render |
| `update` | Sectioned update/render result where applicable | Existing skip behavior | Concise only when current behavior audits |
| `sync` text | Sectioned update/render result | Existing skip behavior | Concise |
| `sync --dry-run` text | Sectioned preview with explicit `PLAN` labels | Existing skip behavior | Concise, labeled as current audit state |
| `sync --json` | Valid JSON, no headings | Existing skip behavior | Structured summary in JSON |
| `render --copy=true` | Sectioned render/copy result | After result | Concise |
| `render --copy=false` | Sectioned render-only result | After result | None |
| `render --print` | Byte-for-byte skill file content | After payload when present | None |
| `show` installed/source | Existing content bytes | After payload when present | None |
| `list`/`find` human | Existing table/text | After result | None |
| existing JSON array commands | Valid JSON array | stderr after result | None |
| `status` | Existing state facts unchanged | After result | Detailed |
| `doctor` | Existing `ok` result unchanged | After result | Detailed |
| `migrate` | Existing preview/apply and safety timing | After result | No extra post-migrate audit in this feature |
| help/version/MCP server | Existing output | Existing skip behavior | None |

Payload commands must not gain a `Result` heading because that would corrupt
their stdout contract. Their preflight diagnostics may still be deferred to
stderr.

### No-Change Results

A successful operation with no changed skills is still a result. Use explicit
text such as:

```text
Result
  OK no skills updated
```

Do not use a warning for a normal no-change result. Preview mode must use
`PLAN`; it must not use `OK installed`, `OK updated`, or `OK rendered` for work
that did not happen.

### Error Results

Retain the current `upskill: <error>` prefix for compatibility. A future global
error redesign is outside this spec.

If passive preflight diagnostics were collected before a primary error, flush
them after the error so the user can distinguish context from cause. Do not
convert a non-fatal preflight diagnostic into the command's error.

## Structured API Contract

### CLI JSON

Existing JSON commands remain valid and retain their existing fields.

Add `agent_output_audit` to `sync --json` and `sync --dry-run --json` using the
summary shape defined above. The field is always present for these sync result
objects, including an empty summary. Existing `warnings` remains present under
the common mutation response contract and retains detailed legacy messages.

Do not add human section labels to JSON. Do not add a JSON flag to commands that
do not already support one as part of this feature.

### MCP

The MCP `sync_skills` result receives the same `agent_output_audit` object for
preview and apply calls.

Generic update information remains in the existing top-level `notices` array.
Add the optional field `phase: "preflight"` to each notice so clients can
classify it without inferring from `type`:

```json
{
  "type": "update_available",
  "phase": "preflight",
  "skill_id": "categorizing-transactions",
  "message": "update available for categorizing-transactions"
}
```

Sync-specific audit findings remain in the mutation result, not top-level
preflight notices:

```json
{
  "dry_run": false,
  "applied": true,
  "updated": [],
  "rendered": [],
  "warnings": [
    "agent output codex contains unmanaged skill example at ..."
  ],
  "agent_output_audit": {
    "warning_count": 1,
    "unmanaged_skill_count": 1,
    "unique_skill_count": 1,
    "missing_marker_count": 0,
    "inspection_error_count": 0,
    "migratable": true,
    "agents": [
      {
        "agent": "codex",
        "output_path": "/absolute/codex/skills",
        "warning_count": 1,
        "unmanaged_skill_count": 1,
        "missing_marker_count": 0,
        "inspection_error_count": 0,
        "skills": ["example"]
      }
    ],
    "items": [
      {
        "agent": "codex",
        "output_path": "/absolute/codex/skills",
        "skill": "example",
        "skill_path": "/absolute/codex/skills/example",
        "reason": "not installed in Upskill from any configured source"
      }
    ]
  }
}
```

MCP tool input schemas do not change. `content`, `structuredContent`, and
`isError` behavior do not change. The additive fields must appear in the JSON
text content wherever the current sync result is encoded.

### Other APIs

Upskill exposes no HTTP or browser API in this repository. No REST, GraphQL,
WebSocket, or web-frontend work is required.

## Failure Semantics

| Condition | Primary operation | Presentation |
| --- | --- | --- |
| Non-fatal update check for one skill fails | Continue under current policy | Result first, then preflight warning |
| Update is available | Continue under current policy | Result first, then preflight notice and next command |
| Automatic update succeeds | Continue | Preflight notice explicitly says it was automatic |
| Automatic update application fails fatally | Do not run primary operation | Existing error and exit code; no result |
| Install fails | No successful install result | Existing error, then any collected passive preflight diagnostics |
| Render/copy fails | Preserve current partial/failure semantics | Never claim failed target succeeded |
| Audit finds unmanaged skills | Requested operation remains successful | Concise or detailed audit warning; findings left unchanged |
| Audit finds missing marker | Requested operation remains successful | Audit warning and sync action |
| Audit cannot inspect directory | Requested operation remains successful | Detailed reason; no migration claim for that error |
| Audit returns no findings | Requested operation remains successful | Omit audit section; structured summary contains zero counts |
| Diagnostic formatting fails unexpectedly | Do not alter completed operation state | Return an internal error only if output cannot be safely emitted; never rerun work |

## Compatibility And Migration

- No config, source record, lock, store, overlay, draft, render marker, or
  activity-log migration is required.
- CLI command names, input flags, and exit codes remain compatible.
- Human mutation output intentionally changes. Scripts must not depend on the
  old unlabeled `rendered_path (copied_path)` text.
- Payload stdout for `show`, `render --print`, and existing JSON modes remains
  compatible.
- Existing JSON and MCP fields remain available.
- New JSON and MCP fields are additive.
- Existing MCP input schemas remain byte-for-byte unchanged unless generated
  schema ordering already varies.
- Legacy detailed `warnings` are not removed by this feature. Removing them
  requires a separate compatibility decision.
- Stdout and stderr retain their current roles.
- `--no-check` and preflight environment behavior remain unchanged.
- Audit and update checks must not run more often than before.

No feature flag is required. The output renderer is a local presentation layer
that can be reverted without reverting the structured summary types.

## Security And Privacy

- Continue to show only paths and source identifiers already exposed by the
  corresponding command.
- Do not add source contents, credentials, environment values, or remote URLs
  to diagnostics.
- Do not turn action hints into executable links or automatic subprocesses.
- Quote action arguments when rendering commands.
- Keep unmanaged-skill handling read-only unless the user separately runs
  `migrate --yes`.
- Do not claim an unreadable directory contains or does not contain particular
  skills.
- Keep MCP structured results within the same local trust boundary as existing
  path-bearing sync results.

## Performance

This feature should reduce repeated work:

- one preflight operation produces both behavior and diagnostics;
- one agent-output scan produces summary counts, detailed items, human text,
  CLI JSON, and MCP JSON;
- formatting is linear in the number of findings and render results;
- no new source scan, hash, Git process, render, filesystem copy, or agent-output
  directory walk is allowed for presentation alone.

For a fixture with 1,000 audit items, summary and formatting should complete in
well under 100 milliseconds on a development machine. CI tests should assert
operation counts and deterministic output rather than a fragile wall-clock
threshold.

## Implementation Tasks

Each implementation task has a separate test task. A build task is not complete
until its paired test task passes.

### B1. Add Structured Diagnostics

- Add phase, severity, action, and diagnostic types.
- Add adapters from update-check warnings, failed checks, changed checks, and
  automatically updated skills.
- Keep stable codes separate from display text.
- Normalize empty fields and deterministic ordering.

Depends on: none.

### B2. Test Structured Diagnostics

- Unit test every required initial code.
- Unit test phase and severity mapping.
- Unit test update-check failure, remote deferral, remote failure, stale cached
  status, ignored cache, manual update availability, automatic pending update,
  and successful automatic update.
- Unit test action argument arrays and deterministic ordering.
- Unit test empty input produces no diagnostics.
- Unit test adapters do not mutate their domain inputs.

Depends on: B1.

### B3. Add Agent-Output Audit Aggregation

- Add the summary and per-agent summary types.
- Build the summary from an existing findings slice.
- Update detailed string compatibility formatting to accept a findings slice or
  summary instead of scanning again.
- Add category-specific remediation metadata.

Depends on: B1.

### B4. Test Agent-Output Audit Aggregation

- Unit test zero findings and non-nil empty collections.
- Unit test one unmanaged skill in one agent.
- Unit test the same skill across two agents counts as two occurrences and one
  unique skill.
- Unit test several skills and deterministic agent/skill ordering.
- Unit test installed skill without a render marker.
- Unit test agent-output inspection failure without a skill name.
- Unit test mixed unmanaged, missing-marker, and inspection-error findings.
- Unit test `migratable` is true only for unmanaged findings.
- Unit test legacy warning strings are derived from the existing items without
  another filesystem scan.

Depends on: B3.

### C1. Collect And Flush Passive CLI Diagnostics

- Refactor preflight to return fatal state and passive diagnostics.
- Add one command-scoped deferred diagnostic collector.
- Pass the collector to command paths that perform post-operation audits.
- Flush the collector exactly once after primary stdout or a primary error.
- Keep immediate safety prompts and fatal errors outside the collector.
- Preserve `--no-check`, explicit-operation skips, and current exit codes.

Depends on: B1, B3.

### C2. Test CLI Collection, Ordering, And Streams

- Test successful operation order with stdout and stderr using one combined
  writer.
- Test stdout alone contains only the primary result.
- Test stderr alone contains only deferred preflight/audit sections and errors.
- Test a primary failure appears before collected non-fatal preflight context.
- Test fatal preflight prevents command dispatch and prints no result.
- Test automatic update diagnostics remain classified as preflight.
- Test `--no-check` and `UPSKILL_NO_CHECK=1` collect nothing.
- Test preflight runs once per invocation.
- Test diagnostics flush once even when a command returns through an error
  branch.

Depends on: C1.

### F1. Add Human Result And Section Formatters

- Add the plain-text section renderer.
- Add explicit install, digest, render, copy, unchanged, not-copied, preview,
  and no-change formatting.
- Add concise and detailed audit renderers.
- Add safe action-command rendering.
- Keep output independent of color, Unicode, terminal width, and TTY state.

Depends on: B1, B3.

### F2. Test Human Result Formatting

- Golden-test the complete motivating `add` result.
- Test one and many installed skills.
- Test one and many agents and copied destinations.
- Test render-only, copied, unchanged, not-copied, and preview states.
- Test full digest and absolute path preservation.
- Test deterministic sort order.
- Test empty sections are omitted and blank lines appear only between non-empty
  sections.
- Test command rendering quotes unusual agent names safely.
- Test output contains no ANSI escapes or carriage-return updates.

Depends on: F1.

### F3. Add Concise And Detailed Audit UX

- Use concise mode after `add`, applicable `update`, non-preview `sync`, and
  copied `render` operations.
- Use detailed mode for `status` and `doctor`.
- State that unmanaged findings were left unchanged.
- Print each applicable remediation once.
- Include the command's agent selector in applicable actions.

Depends on: F1, C1.

### F4. Test Concise And Detailed Audit UX

- Test concise mode reports totals, agents, unique skills, and no item paths.
- Test detailed mode reports every item path and reason grouped by agent.
- Test duplicate skills across agents are not miscounted.
- Test mixed audit categories produce the correct actions.
- Test missing markers suggest sync but not migration by themselves.
- Test inspection failures do not claim migration will fix them.
- Test agent-limited commands produce agent-limited action hints.
- Regression test no findings produce no human audit section.

Depends on: F3.

### C3. Integrate Result Formatting By Command Class

- Integrate sectioned results into `add`, text `sync`, and copied/non-copied
  `render` paths.
- Integrate update rendering only where the command already returns comparable
  install/render results.
- Preserve preview wording and safety timing for sync and migrate.
- Preserve content and table output for payload and inspection commands.
- Update status and doctor to use detailed audit mode.

Depends on: C1, F1, F3.

### C4. Test Command Integration And Compatibility

- CLI integration test `add` with two configured agents.
- CLI integration test `sync` apply, dry run, and no-change results.
- CLI integration test render with copy enabled and disabled.
- Regression test `show` stdout remains byte-for-byte skill content.
- Regression test `render --print` stdout remains byte-for-byte rendered file
  content.
- Regression test JSON array commands still decode as arrays.
- Regression test existing JSON object commands still decode as objects.
- Regression test status and doctor keep their current facts plus grouped audit
  detail.
- Regression test command exit codes and help routing.

Depends on: C3.

### A1. Add Structured Audit Data To JSON And MCP

- Add `agent_output_audit` to CLI sync preview and apply result objects.
- Add `agent_output_audit` to MCP `sync_skills` preview and apply results.
- Add `phase: "preflight"` to MCP update notices.
- Retain legacy `warnings` and existing top-level `notices` behavior.
- Reuse the same audit findings and summary used by human output.
- Keep all MCP input schemas unchanged.

Depends on: B3.

### A2. Test JSON And MCP Contracts

- CLI JSON contract test for an empty audit summary.
- CLI JSON contract test for unmanaged, missing-marker, and inspection-error
  findings.
- CLI JSON regression test for existing sync fields and common preview fields.
- MCP `tools/list` regression test proves sync input schema is unchanged.
- MCP preview and apply tests for the structured audit shape.
- MCP test proves preflight notices include `phase=preflight`.
- MCP test proves audit findings do not appear as preflight notices.
- MCP test proves legacy warning strings remain available.
- MCP JSON text-content test and structured error regression test.

Depends on: A1.

### D1. Update Documentation And Help

- Update the root specification with the phase and stream contract.
- Update README examples for install/render output.
- Update workflow documentation with concise audit output, detailed doctor
  output, and migration preview/apply commands.
- Clarify that preflight executes first but passive diagnostics may display
  after the primary result.
- Clarify that unmanaged findings are read-only until `migrate --yes`.
- Update help only where it currently describes audit or stream behavior; do
  not add a new flag.

Depends on: C3, A1.

### D2. Test Documentation And Help

- Run repository documentation and prompt lint checks.
- Add or update help tests for status, doctor, sync, render, and migrate text.
- Test documented section names and commands match formatter constants.
- Test docs never imply that `upskill add` migrated unmanaged skills.
- Test docs distinguish `upskill migrate` preview from `upskill migrate --yes`
  apply.

Depends on: D1.

### E1. Add End-To-End Lifecycle Coverage

- Add a temporary-home fixture with two configured agent outputs.
- Seed one stale locked skill that produces a preflight check warning.
- Seed one installed skill with an available update notice.
- Seed unmanaged skills shared across agents plus agent-specific unmanaged
  skills.
- Run `add` through the public CLI entry path.
- Verify installation, lock update, render cache, configured copies, combined
  human output, stream separation, audit counts, and unchanged unmanaged files.
- Call MCP sync preview and apply against the same class of fixture and verify
  API parity.

Depends on: C4, A2, D2.

### E2. Run End-To-End And Regression Verification

- Run the new end-to-end scenario.
- Run focused `internal/audit`, `internal/cli`, `internal/mcpserver`, and
  `cmd/upskill` tests.
- Run `go test ./...`.
- Run `go vet ./...`.
- Run `go test -race ./...` if it remains an established repository check.
- Run `git diff --check`.
- Verify no fixture writes outside its temporary Upskill home and configured
  agent-output directories.
- Verify the formatter causes no additional update checks or agent-output
  scans.

Depends on: E1.

### R1. Roll Out The New Reporting Path

- Land structured backend types and tests first.
- Land structured API additions while retaining legacy fields.
- Switch preflight and audit callers to the collector.
- Switch target human commands to the new formatter.
- Update documentation after behavior and output tests are stable.
- Verify against a temporary home before using a real user configuration.

Depends on: B2, B4, C2, F2, F4, C4, A2, D2.

### R2. Verify Rollout And Rollback Safety

- Compare the old and new command side effects in identical temporary-home
  fixtures.
- Prove config, lock, store, render, copy, and unmanaged-skill bytes match
  except for the intentionally requested install/update work.
- Prove API additions are backward compatible with clients that ignore unknown
  fields.
- Document that rollback may restore the old text formatter without reverting
  harmless additive summary fields.
- Run the full repository checks after any rollback-path adjustment.

Depends on: R1.

## End-To-End Scenarios

### Successful Add With Incidental Findings

1. Configure Claude Code and Codex output directories.
2. Seed unmanaged skills in both, including at least one duplicated skill name.
3. Seed one failed preflight check and one available manual update.
4. Add and render a new skill for both agents.
5. Verify the skill is installed and copied to both configured outputs.
6. Verify `Result` appears before `Preflight` and `Agent output audit` in a
   combined stream.
7. Verify concise audit counts occurrences, agents, and unique skills exactly.
8. Verify every pre-existing unmanaged file is byte-for-byte unchanged.

### Add Failure After Non-Fatal Preflight

1. Seed a non-fatal available-update notice.
2. Run `add` against an invalid or unavailable requested source.
3. Verify the command returns its existing nonzero exit code.
4. Verify no successful result appears.
5. Verify the primary error appears before the deferred preflight notice.
6. Verify no post-render audit runs.

### Fatal Automatic Update Failure

1. Configure an automatic update whose apply step fails under the current fatal
   policy.
2. Run an ordinary command.
3. Verify the requested primary operation does not run.
4. Verify the existing preflight error and exit code remain unchanged.
5. Verify no result or audit section appears.

### Detailed Doctor Audit

1. Seed unmanaged, missing-marker, and unreadable-output findings.
2. Run `upskill doctor`.
3. Verify findings group by agent and retain every path and reason that could be
   inspected.
4. Verify migration, sync, and inspection remediation are category-correct.
5. Verify doctor makes no changes to the found skills.

### Payload Compatibility

1. Seed a skill with known content.
2. Run installed `show`, source `show`, and `render --print` with passive
   preflight notices available.
3. Verify stdout exactly matches the expected content for each command.
4. Verify preflight sections appear only on stderr after payload emission.
5. Verify consumers can redirect stdout without receiving headings or
   diagnostics.

### Structured Sync Parity

1. Seed the same mixed audit findings used by the human test.
2. Run CLI sync preview and apply in JSON mode.
3. Call MCP `sync_skills` preview and apply.
4. Verify both surfaces return equivalent `agent_output_audit` counts and
   items.
5. Verify MCP preflight notices carry `phase=preflight` and remain separate
   from sync audit findings.
6. Verify legacy warning strings remain available.

## Rollout Plan

1. Add diagnostic and audit summary models with unit tests.
2. Add backward-compatible JSON and MCP fields with contract tests.
3. Add the CLI collector while retaining existing human formatters behind the
   new data flow.
4. Add human section/result formatters and focused golden tests.
5. Switch `add`, `sync`, `render`, `status`, and `doctor` to the new format.
6. Run end-to-end tests against a temporary home with two agent outputs.
7. Update the root specification, README, workflows, and help.
8. Run the full test, vet, race, and diff checks.
9. Manually compare the motivating command's old and new output using a
   disposable configuration, not the user's live skill directories.

No data migration or feature flag is required.

## Acceptance Criteria

- A successful `upskill add` presents its install and render result before
  passive preflight and audit diagnostics in a combined terminal view.
- Preflight and audit messages appear under distinct labeled sections.
- Rendered and copied paths have explicit labels.
- Concise audit mode reports correct occurrence, agent, category, and unique
  skill counts without printing one full warning per item.
- Detailed audit mode retains every available path and reason.
- Audit output states that unmanaged findings were left unchanged.
- Migration hints distinguish preview from apply.
- Missing-marker and inspection failures receive category-correct guidance.
- Primary stdout and diagnostic stderr remain separate.
- `show`, `render --print`, and existing JSON payloads remain compatible.
- Existing command inputs and exit codes remain compatible.
- CLI sync JSON and MCP sync results expose equivalent structured audit data.
- Existing MCP notices and warning strings remain available.
- MCP preflight notices identify their phase.
- Formatting performs no extra update check, agent-output scan, render, or copy.
- No config, lock, store, source, overlay, draft, activity, or marker schema
  changes are required.
- Focused backend, frontend, CLI, JSON, MCP, end-to-end, and regression tests
  pass.
- The full repository test suite and required static checks pass.

## Open Questions

None block the first version.

Future work may consider optional color, a global structured-output convention
for commands that do not yet support JSON, or removal of legacy string warnings
after a documented compatibility window.
