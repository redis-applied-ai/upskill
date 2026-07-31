# Skill Usage, Retrieval, and Feedback Spec

## Status

Draft.

## Summary

Upskill should record how often agents retrieve and use installed skills, and
it should let agents leave structured feedback based on real use.

Retrieval accounting is automatic. A successful operation that returns content
from an installed skill records one retrieval. Agents explicitly record a use
only after they actually apply the skill to a task. Agents may also submit
positive or negative feedback, but feedback must describe the situation, what
went right or wrong, and the proposed change.

The CLI exposes counts and recent feedback through:

```bash
upskill show <skill> --stats
upskill show <skill> --stats --nocontent
```

The first form preserves the skill content and appends its statistics. The
second form returns only statistics and does not itself count as a retrieval.

Activity data is local mutable state. It must not be stored in
`upskill-lock.json`, copied into skill bundles, rendered into agent output, or
sent to a source automatically.

## Problem

Upskill can install, find, read, render, customize, and revise skills, but it
cannot answer basic questions about their value:

- How often did an agent retrieve this skill?
- How often did an agent report that it actually used the skill?
- Which skills are frequently retrieved but rarely used?
- What happened when an agent followed the skill?
- What concrete change would make the skill more useful?
- Did feedback apply to the current skill revision or an older one?

Retrieval and use are different signals. An agent may retrieve a skill and
decide that it does not fit the task. It may also retrieve a skill several times
while completing one task. Upskill must preserve that distinction instead of
guessing that every retrieval was a use.

Unstructured feedback is also insufficient. A note such as "this skill is bad"
does not explain where it failed or how to improve it. Positive feedback without
the situation is equally hard to reuse. Feedback needs a small required schema
that captures experience rather than opinion alone.

The existing lock file is not the right storage location. It describes the
installed source and revision state, and install or update commands rewrite it.
Runtime activity is independently mutable, may grow over time, and must survive
skill updates without changing source-lock semantics.

## Goals

- Automatically record successful installed-skill content retrievals.
- Keep retrieval count distinct from agent-reported use count.
- Let agents explicitly increment use count after applying a skill.
- Let agents submit positive or negative feedback for an installed skill.
- Require each feedback record to include the situation, outcome details, and a
  proposed change.
- Tie every event to a stable skill identity and snapshot the current skill hash
  when available.
- Show retrieval count, use count, feedback counts, timestamps, and recent
  feedback through `upskill show <skill> --stats`.
- Make `upskill show <skill> --stats --nocontent` return only statistics without
  recording a retrieval.
- Give CLI-only agents and MCP clients equivalent ways to record use and
  feedback.
- Keep activity accounting local, concurrency-safe, and separate from the
  installed store, lock file, sources, overlays, drafts, and rendered output.
- Preserve successful skill reads if activity recording fails, while making the
  missed event visible to the caller.
- Add tests for counting boundaries, feedback validation, concurrency,
  idempotency, compatibility, and output contracts.

## Non-Goals

- Do not infer use from retrieval.
- Do not infer sentiment or feedback from task transcripts.
- Do not capture prompts, task content, model output, or conversation history
  automatically.
- Do not send statistics or feedback to an upstream source, Git host, remote
  service, or analytics service.
- Do not edit a skill, create an overlay, create a draft, or open a pull request
  automatically from feedback.
- Do not count internal store scans, search indexing, rendering, syncing,
  update checks, or file-manifest generation as retrievals.
- Do not track raw source previews or mutable drafts in the first version.
- Do not treat retrieval count or use count as a count of unique users,
  sessions, or tasks.
- Do not add a background daemon, uploader, or scheduled aggregation job.
- Do not add activity deletion, retention, export, or compaction commands in the
  first version.
- Do not change the `upskill-lock.json` schema for activity accounting.

## Terminology

- Installed skill: A base skill directory under `store/`, addressed by its
  normalized store name.
- Skill identity: The stable identity used to associate activity with the same
  installed skill across source updates.
- Skill revision: The installed content hash at the time an event is recorded.
- Retrieval: One successful user-facing operation that returns content from an
  installed skill.
- Use: One explicit agent report that it applied an installed skill to a task.
- Feedback: A structured positive or negative report based on use experience.
- Situation: The task context in which the skill was used.
- Details: What went right or wrong when the agent followed the skill.
- Proposed change: The concrete instruction, example, resource, or behavior that
  should change or be preserved.
- Activity event: One append-only retrieval, use, or feedback record.
- Statistics: Counts and timestamps derived from activity events.

## Requirement Traceability

| Requirement | Design surface | Acceptance evidence |
| --- | --- | --- |
| Automatically track retrievals | CLI and MCP read instrumentation | Successful content reads increment exactly once |
| Let agents bump use count | `stats use` and `record_skill_use` | Explicit call increments use count exactly once |
| Accept structured feedback | `stats feedback` and `record_skill_feedback` | Missing required feedback fields are rejected |
| Support positive feedback | `sentiment=positive` | Positive count and details appear in stats |
| Show counts with content | `show <skill> --stats` | Content and post-retrieval counts are both printed |
| Show only stats | `show <skill> --stats --nocontent` | No skill content and no retrieval increment |
| Preserve installed metadata semantics | Separate activity store | Lock-file bytes do not change during activity writes |

## Core Semantics

### Eligible Skills

The first version records activity only for installed base skills.

The CLI and MCP resolve the supplied identifier using the same normalized
installed-skill rules already used by `show`, `read_skill`, and related tools.
This feature must not add a separate content scan or fuzzy resolver.

These targets are not eligible:

- `draft:<name>` identifiers;
- raw local or Git source previews;
- configured sources that have not installed the selected skill;
- skill directories found only in rendered agent output;
- missing or malformed installed skills.

Use and feedback operations against an ineligible target fail without writing an
event. `show --stats` against a source preview fails with an error explaining
that statistics require an installed skill.

### Skill Identity

Statistics must survive a normal update of the same installed skill without
combining unrelated skills that happen to reuse one store name.

For a lock-backed installed skill, derive `skill_key` from:

1. the lock entry's `source_id`, when present, otherwise its canonical source
   type and source URL;
2. the effective source subpath;
3. the effective skill subpath;
4. the normalized store name.

The implementation should hash the length-delimited tuple with SHA-256 and store
the result as `sha256:<hex>`. Human output continues to use the normalized skill
ID and display name; the key is an internal correlation value.

The installed content hash is not part of `skill_key`. An update from one
revision to the next therefore keeps lifetime counts for that skill. Every event
also snapshots `skill_hash`, so feedback can still be attributed to the revision
that the agent used.

For an installed skill without a lock entry, derive a fallback key from the
normalized store name and canonical absolute store path. If a later install
creates a lock entry, the new lock-backed identity starts a new series. The
first version does not merge unlocked history into a new lock-backed identity.

Reinstalling the same store name from a different source creates a different
`skill_key`. Old events remain in the activity log but are not included in the
new skill's current statistics.

### What Counts as a Retrieval

A retrieval is counted per successful content-returning operation, not per task
or session.

| Surface | Operation | Count retrieval? |
| --- | --- | --- |
| CLI | `upskill show <installed-skill>` | Yes |
| CLI | `upskill show <installed-skill> --stats` | Yes |
| CLI | `upskill show <installed-skill> --stats --nocontent` | No |
| CLI | `upskill render <skill> --print ...` | Yes |
| CLI | `upskill list`, `find`, `source skills`, or `source find` | No |
| CLI | `upskill show <source> --skill ...` | No |
| MCP | `read_skill` for an installed skill | Yes |
| MCP | `read_skill_file` for an installed skill | Yes |
| MCP | `get_skill_stats` | No |
| MCP | `find_skills` or `list_skill_files` | No |
| MCP | `render_skill`, `sync_skills`, or `check_skill_updates` | No |
| MCP | Draft read or render tools | No |

Each successful call in the table records at most one retrieval, even if the
response contains both `SKILL.md` and a file manifest. Reading three support
files through three `read_skill_file` calls records three retrievals.

Record the event only after Upskill has resolved the skill and obtained the
content that it will return. Failed resolution, rendering, file reads, output
validation, and argument validation do not increment the count.

For `show --stats`, record the retrieval before aggregating statistics so the
displayed count includes the current content read.

### What Counts as a Use

Upskill never derives use from retrieval. A use event exists only when an agent
or user invokes the explicit use-recording CLI command or MCP tool.

One successful call increments `use_count` by one. There is no `amount`
parameter and no command that sets a count directly.

The caller should record one use after it materially applies the skill to a
task. A skill that was merely opened, considered, or rejected should not receive
a use event.

The optional `idempotency_key` prevents duplicate increments caused by a retried
request. Reusing the same non-empty key for the same skill and event type returns
the original event and does not append another event. Reusing it for a different
skill or event type fails as a conflict.

### Feedback Requirements

Feedback fields:

```text
skill_id         required
sentiment        required: positive | negative
situation        required, non-empty
details          required, non-empty
proposed_change  required, non-empty
agent            optional
idempotency_key  optional
```

`details` must say what went wrong or right. `proposed_change` must be concrete
enough for a later agent or maintainer to act on. Positive feedback may propose
preserving an instruction, extending a successful pattern, or adding a similar
example elsewhere.

Examples:

```text
sentiment: negative
situation: Addressing unresolved review threads on a Go pull request.
details: The skill checked the top-level review state but did not direct the
         agent to inspect inline comments, so one blocking thread was missed.
proposed_change: Require inline review-thread inspection before declaring the
                 pull request ready.
```

```text
sentiment: positive
situation: Promoting a local skill revision into a team source.
details: The dry-run and source-policy checks made the target clear and
         prevented an accidental write to managed output.
proposed_change: Preserve the dry-run requirement and add the same target
                 summary to the CLI example.
```

Whitespace-only values are invalid. The first version limits each required text
field to 16 KiB, `agent` to 256 bytes, and the complete serialized event to
64 KiB. Validation errors identify the invalid field and do not write a partial
event.

Submitting feedback does not automatically increment use count. The caller may
record a use separately. Keeping the events independent avoids a double count
when an agent has already recorded the use.

## Activity Storage

### Location

Add a configurable activity path:

```text
config field: stats_path
environment:  UPSKILL_STATS
default:      ~/.skills/upskill-stats.jsonl
```

Extend `config.Config` with `StatsPath`. The environment override follows the
same expansion and precedence rules as the existing config and lock paths.

The default layout becomes:

```text
~/.skills/
  store/
  overlays/
  drafts/
  imports/
  rendered/
  sources/
  config.json
  upskill-lock.json
  upskill-stats.jsonl
```

Commands create the parent directory lazily on the first activity write.
Reading statistics when the file does not exist returns zero counts and an empty
feedback list.

The activity path is managed state but not generated output. Render, sync,
update, add, remove, source inspection, draft, and overlay operations must not
rewrite or copy it.

### Event Format

The activity file is append-only JSON Lines. Each line is one complete event:

```json
{
  "schema_version": 1,
  "event_id": "evt_01k...",
  "event_type": "retrieval",
  "skill_key": "sha256:...",
  "skill_id": "redis",
  "skill_name": "Redis",
  "skill_hash": "sha256:...",
  "source_id": "src_01j...",
  "timestamp": "2026-07-30T20:15:02.123456Z",
  "channel": "mcp",
  "operation": "read_skill",
  "agent": "codex",
  "idempotency_key": ""
}
```

Use events use the same envelope with `event_type="use"`. Feedback events add:

```json
{
  "event_type": "feedback",
  "sentiment": "negative",
  "situation": "...",
  "details": "...",
  "proposed_change": "..."
}
```

Rules:

- `schema_version`, `event_id`, `event_type`, `skill_key`, `skill_id`, and
  `timestamp` are required on every event.
- Timestamps are generated by Upskill in UTC with sub-second precision.
- Event IDs are monotonic-sortable, collision-resistant local identifiers.
- `channel` is `cli` or `mcp` in the first version.
- `operation` names the command or tool that produced the event.
- `skill_hash` snapshots the lock hash when available; an unlocked skill uses a
  freshly computed directory hash when the operation already has one, otherwise
  it may be omitted.
- Retrieval events never store returned content, filenames, prompts, or task
  details.
- Use events store no situation unless a future schema adds an explicit field.
- Feedback stores only caller-supplied fields. Upskill does not enrich it from a
  transcript or environment.
- Unknown fields must be ignored by readers for forward compatibility.
- Unknown event types must be skipped with a warning rather than counted.

### Append and Concurrency Contract

Multiple CLI and MCP processes may record activity at the same time. The
activity package must provide one append API and one aggregation API shared by
all callers.

For append:

1. Validate and resolve the installed skill.
2. Construct the complete event in memory.
3. Acquire an exclusive advisory lock associated with the activity file.
4. If `idempotency_key` is non-empty, check existing events for a matching key
   and enforce the idempotency rules.
5. Open the activity file with append semantics.
6. Serialize the event as compact JSON plus one newline.
7. Write the complete line with one file write.
8. Flush and sync the file before releasing the lock.

Lock acquisition must have a bounded timeout and return a clear error. The
implementation may use a small cross-platform lock dependency or isolated
platform-specific files, but all event writers must use the same lock.

This contract prevents concurrent writers from interleaving lines or losing
increments. It also keeps the event log append-only, so an interrupted write
cannot overwrite prior history.

Readers:

- ignore blank lines;
- ignore one malformed, unterminated final line with a warning because it may
  be the result of an interrupted append;
- skip any other malformed line with a warning and continue aggregating valid
  events;
- return warnings alongside statistics so corruption is never silent.

Explicit `use` and `feedback` mutations fail if the event cannot be recorded.
Automatic retrieval accounting follows the non-blocking failure behavior
defined below.

### Aggregated Statistics

Aggregate events matching the current installed skill's `skill_key` into:

```go
type SkillStats struct {
    SkillKey              string
    SkillID               string
    SkillName             string
    CurrentSkillHash      string
    RetrievalCount        uint64
    UseCount              uint64
    FeedbackCount         uint64
    PositiveFeedbackCount uint64
    NegativeFeedbackCount uint64
    FirstRetrievedAt      *time.Time
    LastRetrievedAt       *time.Time
    FirstUsedAt           *time.Time
    LastUsedAt            *time.Time
    LastFeedbackAt        *time.Time
    RecentFeedback        []Feedback
    Warnings              []string
}
```

`RecentFeedback` is sorted newest first. Human `show --stats` output includes at
most the 10 newest feedback records and says when more records exist. The MCP
statistics tool accepts `feedback_limit`, defaults to 10, and caps it at 100.

Counts are lifetime counts for the current skill identity across content
updates. `CurrentSkillHash` describes the currently installed revision; each
feedback item also returns the hash captured when it was submitted.

## CLI Surface

### `upskill show`

Extend the command:

```bash
upskill show <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--stats] [--nocontent]
```

Rules:

- `--stats` is valid only when the argument resolves to an installed skill.
- `--nocontent` is valid only with `--stats`.
- `--stats` without `--nocontent` returns skill content, records one retrieval,
  then appends the post-retrieval statistics.
- `--stats --nocontent` returns statistics only and records no retrieval.
- `--agent` still controls which rendered content is returned. Counts belong to
  the installed skill identity, not to one rendered agent variant.
- Source-only flags retain their current behavior when `--stats` is absent.
- Raw or configured source preview plus `--stats` fails with exit code 2 and a
  message that statistics are available only for installed skills.
- Existing `show` output remains byte-for-byte content-compatible when neither
  statistics flag is present, except for a possible activity warning on
  stderr.

Example human statistics:

```text
--- upskill stats ---
Skill: redis
Retrievals: 12
Uses: 4
Feedback: 3 (2 positive, 1 negative)
Last retrieved: 2026-07-30T20:15:02Z
Last used: 2026-07-30T20:18:41Z
Last feedback: 2026-07-30T20:20:10Z

Recent feedback:
- negative, 2026-07-30T20:20:10Z, skill sha256:...
  Situation: Addressing unresolved review threads on a Go pull request.
  Details: The skill did not direct the agent to inspect inline comments.
  Proposed change: Require inline review-thread inspection before completion.
```

Zero-value statistics must be explicit:

```text
Retrievals: 0
Uses: 0
Feedback: 0 (0 positive, 0 negative)
```

Do not print Go zero timestamps. Use `never` when a timestamp has no value.

### Explicit Activity Commands

Add a focused top-level namespace so CLI-only agents can record the same events
as MCP clients:

```bash
upskill stats use <skill> [--agent AGENT] [--idempotency-key KEY] [--json]
upskill stats feedback <skill> \
  --sentiment positive|negative \
  --situation TEXT \
  --details TEXT \
  --proposed-change TEXT \
  [--agent AGENT] \
  [--idempotency-key KEY] \
  [--json]
```

`stats use` prints the updated retrieval and use counts. `stats feedback` prints
the feedback event ID plus updated counts. `--json` returns the same structured
result shape as the corresponding MCP tool.

Both commands:

- require an installed skill;
- perform no render, sync, update, overlay, draft, source, or VCS writes;
- use the shared event validation and append implementation;
- return exit code 2 for invalid arguments and exit code 1 for storage or skill
  resolution failures.

The CLI does not add a count-setting or count-decrementing command.

### CLI Help

Top-level help adds `stats`:

```text
stats    Record installed-skill use and feedback
```

Leaf help must document:

- the difference between retrieval and use;
- that `show --stats --nocontent` does not count as a retrieval;
- all required feedback fields;
- positive and negative feedback examples;
- local-only storage;
- idempotency behavior.

## MCP API Surface

### New Tool: `get_skill_stats`

Input:

```json
{
  "skill_id": "redis",
  "feedback_limit": 10
}
```

Output:

```json
{
  "skill_id": "redis",
  "skill_key": "sha256:...",
  "current_skill_hash": "sha256:...",
  "retrieval_count": 12,
  "use_count": 4,
  "feedback_count": 3,
  "positive_feedback_count": 2,
  "negative_feedback_count": 1,
  "first_retrieved_at": "2026-07-29T17:11:00Z",
  "last_retrieved_at": "2026-07-30T20:15:02Z",
  "first_used_at": "2026-07-29T17:14:00Z",
  "last_used_at": "2026-07-30T20:18:41Z",
  "last_feedback_at": "2026-07-30T20:20:10Z",
  "recent_feedback": [],
  "warnings": []
}
```

This tool is read-only and does not record a retrieval.

### New Tool: `record_skill_use`

Input:

```json
{
  "skill_id": "redis",
  "agent": "codex",
  "idempotency_key": "session-123:redis:use"
}
```

Output:

```json
{
  "recorded": true,
  "duplicate": false,
  "event_id": "evt_01k...",
  "skill_id": "redis",
  "retrieval_count": 12,
  "use_count": 5
}
```

If an idempotency match already exists, return `recorded=false`,
`duplicate=true`, the original event ID, and unchanged counts.

The tool description must tell agents to call it only after materially applying
the skill.

### New Tool: `record_skill_feedback`

Input:

```json
{
  "skill_id": "redis",
  "sentiment": "negative",
  "situation": "Addressing unresolved review threads on a Go pull request.",
  "details": "The skill did not direct the agent to inspect inline comments.",
  "proposed_change": "Require inline review-thread inspection before completion.",
  "agent": "codex",
  "idempotency_key": "session-123:redis:feedback:inline-comments"
}
```

Output:

```json
{
  "recorded": true,
  "duplicate": false,
  "event_id": "evt_01k...",
  "skill_id": "redis",
  "sentiment": "negative",
  "retrieval_count": 12,
  "use_count": 5,
  "feedback_count": 4,
  "positive_feedback_count": 2,
  "negative_feedback_count": 2
}
```

All feedback fields are required by the MCP schema and validated again by the
activity package.

### Existing Tool Changes

Instrument successful installed-skill calls:

- `read_skill`
- `read_skill_file`

Do not instrument:

- `find_skills`
- `list_skill_files`
- draft reads
- source inspection
- render-only or sync operations

After a successful retrieval, add this response metadata:

```json
{
  "retrieval_recorded": true,
  "stats": {
    "retrieval_count": 12,
    "use_count": 4,
    "feedback_count": 3
  }
}
```

Do not include feedback text automatically in `read_skill` or
`read_skill_file`. Agents request it deliberately through `get_skill_stats`.

The MCP initialization instructions generated from the registry must explain:

- which read tools increment retrieval count;
- that `find_skills` does not;
- when to call `record_skill_use`;
- the required feedback fields;
- that statistics and feedback remain local until a user or agent explicitly
  acts on them.

### Automatic Recording Failure

Activity accounting must not prevent an agent from obtaining a valid installed
skill.

If automatic retrieval recording fails:

- CLI content commands return the content with their normal success exit code
  and write a concise warning to stderr.
- MCP read tools return the content with:

```json
{
  "retrieval_recorded": false,
  "activity_warnings": ["..."]
}
```

- the failed retrieval is not included in counts;
- Upskill does not retry in the background.

Explicit `record_skill_use` and `record_skill_feedback` calls fail when their
event cannot be stored. Their purpose is the mutation, so returning success
without a record would be misleading.

## Backend Design

Add an `internal/activity` package responsible for:

- resolving `skill_key` and current metadata from an installed skill and lock
  entry;
- validating event fields;
- generating event IDs and timestamps;
- appending events under the shared lock;
- enforcing optional idempotency;
- scanning the JSON Lines store;
- aggregating `SkillStats`;
- returning parse and storage warnings;
- formatting no CLI output directly.

CLI and MCP layers should call this package after their existing skill
resolution and content-read logic. They must not duplicate counting logic.

Suggested public package surface:

```go
func ResolveSkill(cfg config.Config, skillID string) (SkillIdentity, error)
func RecordRetrieval(cfg config.Config, identity SkillIdentity, channel, operation, agent string) (SkillStats, error)
func RecordUse(cfg config.Config, input UseInput) (RecordResult, error)
func RecordFeedback(cfg config.Config, input FeedbackInput) (RecordResult, error)
func Stats(cfg config.Config, skillID string, feedbackLimit int) (SkillStats, error)
```

Keep automatic recording at the outer content-return boundary. Lower-level
helpers such as `library.ListInstalled`, `library.FindInstalled`, renderer file
reads, and frontmatter parsing are used by both content and non-content flows
and must not record activity themselves.

The current CLI `runShow` should branch before rendering when both `--stats` and
`--nocontent` are present. This prevents a content read and guarantees that the
statistics-only command has no retrieval side effect.

The current MCP `read_skill` and `read_skill_file` handlers should record only
after their content read succeeds. Draft identifiers must bypass the activity
package.

## Compatibility and Migration

- Existing config files without `stats_path` continue to load and use the
  default path.
- Existing activity files do not exist, so every installed skill begins with
  zero counts.
- No migration of `upskill-lock.json` is required.
- Existing `show` arguments remain valid.
- Existing `show` stdout remains unchanged when `--stats` and `--nocontent` are
  absent.
- Existing MCP tool inputs remain valid.
- Existing MCP read responses gain optional fields; clients must ignore unknown
  result fields as required by the current compatibility model.
- Skill updates preserve activity because `skill_key` excludes content hash.
- `remove` does not delete activity. Reinstalling the same source identity and
  skill path restores its prior counts.
- Reinstalling a different source under the same store name starts separate
  counts.
- Older Upskill versions ignore the separate JSON Lines file.

## Security and Privacy

- Activity stays on the local filesystem unless the user explicitly copies or
  publishes it.
- Retrieval events store no returned content, prompt, task, transcript, or
  model output.
- Use events store only skill identity, timestamps, operation metadata, and
  optional caller attribution.
- Feedback stores only the fields explicitly supplied by the caller.
- MCP tool descriptions must tell agents not to include secrets, credentials,
  private customer data, or unnecessary personal data in feedback.
- Feedback does not grant authority to edit a skill or source.
- Activity writes must never resolve paths from event content.
- `stats_path` receives the same path expansion rules as other managed config
  paths, but activity event fields cannot change it.
- File permissions should match other user-owned Upskill state. The file must
  not be made group- or world-writable by Upskill.
- Malformed or oversized events are rejected before append.

## Observability

Human and structured results must make accounting outcomes explicit:

- `retrieval_recorded`
- `recorded`
- `duplicate`
- `event_id`
- current counts
- parse or storage warnings

Do not print a warning for a normal zero-count skill. Do warn when:

- automatic recording failed;
- one or more malformed events were skipped;
- the final line appears interrupted;
- lock acquisition timed out;
- an unknown event type was ignored.

Warnings must identify the activity path but must not print feedback text.

## Implementation Tasks

### 1. Config and Activity Store

- Add `StatsPath` to `config.Config`.
- Parse `stats_path` and `UPSKILL_STATS`.
- Add the default activity path to directory-layout documentation.
- Implement version 1 event structs.
- Implement event JSON validation and size limits.
- Implement lock-protected append and flush.
- Implement scan behavior for valid, malformed, and interrupted lines.
- Implement event aggregation and recent-feedback ordering.

Testing tasks:

- Unit test default, config, and environment path precedence.
- Unit test missing file returns zero statistics.
- Unit test event round trips.
- Unit test unknown fields are ignored.
- Unit test malformed complete lines and interrupted final lines return warnings.
- Unit test text and event size validation.
- Unit test concurrent writers preserve every increment and every line.
- Unit test lock timeout behavior.

### 2. Installed Skill Identity

- Implement lock-backed `skill_key` derivation.
- Implement unlocked fallback identity.
- Snapshot current skill hash and source metadata.
- Reuse installed-skill name resolution.
- Reject drafts, sources, rendered-only skills, and missing skills.

Testing tasks:

- Unit test an update changes `skill_hash` but not `skill_key`.
- Unit test a different source under one store name changes `skill_key`.
- Unit test configured source rename does not change a stable source-ID-backed
  key.
- Unit test unlocked fallback identity.

### 3. Automatic Retrieval Accounting

- Instrument installed `show`.
- Instrument installed `render --print`.
- Instrument MCP `read_skill`.
- Instrument MCP `read_skill_file`.
- Keep list, find, manifest, source, draft, render-only, sync, and update paths
  uninstrumented.
- Add non-blocking warning behavior for automatic event failures.

Testing tasks:

- Table-test every counting and non-counting operation in this spec.
- Test successful calls increment once.
- Test failed content reads do not increment.
- Test one `read_skill` with a manifest increments once.
- Test repeated support-file reads increment once per call.
- Test draft reads do not increment.
- Test automatic recording failure still returns content.

### 4. CLI Statistics Display

- Add `--stats` and `--nocontent` parsing and help.
- Reject `--nocontent` without `--stats`.
- Reject source preview with statistics flags.
- Append post-retrieval stats to content output.
- Add the statistics-only branch that never reads skill content.
- Format zero values, timestamps, counts, sentiment totals, and recent feedback.

Testing tasks:

- CLI test `show <skill> --stats` prints content and updated counts.
- CLI test `show <skill> --stats --nocontent` prints no content and does not
  increment retrieval.
- CLI test source statistics fail clearly.
- CLI test zero stats print `never` rather than a zero time.
- Regression test plain `show` stdout remains unchanged.

### 5. CLI Use and Feedback

- Add top-level `stats` routing and leaf help.
- Implement `stats use`.
- Implement `stats feedback`.
- Add human and JSON results.
- Enforce required fields, sentiment values, and idempotency.

Testing tasks:

- CLI test use increments exactly once.
- CLI test a duplicate idempotency key does not increment.
- CLI test feedback rejects each missing field.
- CLI test positive and negative feedback counts.
- CLI test feedback does not increment use.
- CLI JSON test matches MCP result fields.

### 6. MCP Tools and Registry

- Register `get_skill_stats`.
- Register `record_skill_use`.
- Register `record_skill_feedback`.
- Add schemas, required fields, limits, and tool descriptions.
- Add retrieval metadata to successful installed-skill read responses.
- Add activity warnings to successful reads when recording fails.
- Regenerate MCP instructions from the live registry.
- Update callable-tool documentation markers and lint tests.

Testing tasks:

- `tools/list` includes all three tools with authoritative schemas.
- `describe_tool` matches each live schema.
- Unknown parameters and wrong types use the shared validation contract.
- `get_skill_stats` does not increment retrieval.
- Use and feedback tools return updated counts.
- Feedback tool schema requires all experience fields.
- MCP idempotency behavior matches CLI behavior.
- MCP read response reports successful and failed automatic recording.

### 7. Documentation and Agent Guidance

- Update the root command surface.
- Update `SPEC.md` command, config, directory, and MCP sections after
  implementation lands.
- Update `README.md`, getting started, and workflow examples.
- Update `prompts/AGENTS.md` to distinguish retrieval, use, and feedback.
- Document that feedback remains local and does not mutate skills.
- Include positive and negative feedback examples.

Testing tasks:

- Help and docs use the exact `--nocontent` spelling.
- Docs never imply that retrieval equals use.
- MCP registry docs list the new tools.
- Prompt lint requires the use and feedback guidance.

## End-to-End Scenarios

### Retrieved but Not Used

1. An agent calls `find_skills` and receives metadata. Counts do not change.
2. The agent calls `read_skill`. Retrieval count becomes 1.
3. The agent decides the skill does not fit. It does not call
   `record_skill_use`.
4. `show --stats --nocontent` reports 1 retrieval and 0 uses without changing
   either count.

### Retrieved and Used

1. An agent calls `read_skill`. Retrieval count increases.
2. The agent materially follows the skill while completing the task.
3. The agent calls `record_skill_use` once.
4. The tool returns the updated use count.
5. A retry with the same idempotency key returns the original event and does
   not increment again.

### Negative Feedback

1. An agent uses a skill and encounters a specific failure.
2. It records use if it has not already done so.
3. It calls `record_skill_feedback` with negative sentiment, situation,
   details, and proposed change.
4. Upskill stores the current skill hash with the feedback.
5. A maintainer reads the feedback through `show --stats --nocontent` or
   `get_skill_stats`.
6. No overlay, draft, source write, or VCS operation occurs automatically.

### Positive Feedback

1. An agent uses a skill successfully.
2. It submits positive feedback explaining the situation, the successful
   behavior, and what should be preserved or extended.
3. Positive feedback count increases.
4. A later revision can retain the proven instruction while acting on other
   feedback.

### Activity Write Failure

1. An agent calls `read_skill`.
2. Upskill reads the skill content successfully.
3. The activity lock times out or the activity file is not writable.
4. The read still succeeds with `retrieval_recorded=false` and a warning.
5. A later explicit `record_skill_use` fails until storage is available; it
   never reports a use that was not persisted.

## Acceptance Criteria

- Every successful installed-skill content retrieval listed in this spec
  appends exactly one retrieval event.
- Non-content discovery, manifest, statistics, source, draft, render-only, sync,
  and update operations append no retrieval event.
- Use count changes only through an explicit use operation.
- Feedback requires sentiment, situation, details, and proposed change.
- Positive and negative feedback are both supported and counted separately.
- Every stored event has a stable skill key and timestamp.
- Feedback snapshots the installed skill hash when available.
- `upskill show <skill> --stats` prints content plus post-retrieval counts.
- `upskill show <skill> --stats --nocontent` prints only statistics and does not
  change retrieval count.
- CLI-only agents and MCP clients can record use and feedback.
- Repeated explicit mutations with one idempotency key do not duplicate events.
- Concurrent writers do not lose increments or interleave JSON Lines.
- Activity writes do not change `upskill-lock.json`, base skills, overlays,
  drafts, rendered output, configured agent output, or sources.
- Automatic activity failures do not block valid content reads and are visible
  to the caller.
- Explicit use and feedback operations fail if their event is not persisted.
- Existing config, CLI inputs, and MCP read-tool inputs remain compatible.

## Open Questions

None for the first version. Retention, export, compaction, cross-machine
aggregation, and upstream feedback workflows require separate decisions.
