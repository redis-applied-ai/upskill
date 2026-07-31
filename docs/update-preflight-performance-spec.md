# Update Preflight Performance Spec

## Status

Draft.

## Summary

Upskill should not clone an unrelated Git repository before every ordinary CLI
command or MCP tool call.

The current preflight path checks every locked skill before command dispatch.
For each Git-backed skill, it creates a temporary directory, runs a fresh
`git clone`, hashes the selected skill, and removes the clone. This work happens
even when the skill uses the default `manual` update policy and even when the
requested command only reads an installed local skill.

This spec changes preflight without weakening explicit update checks:

- local sources remain live on every preflight;
- manual Git sources never perform implicit network work;
- auto Git sources use a one-hour freshness cache;
- stale auto Git sources use `git ls-remote` before materialization;
- unchanged remote revisions do not clone;
- changed Git sources are materialized once per repository and ref, not once
  per installed skill;
- explicit checks, diffs, sync previews, and source catalog reads remain live;
- failed implicit remote refreshes warn and defer instead of blocking an
  unrelated command.

The lock file remains authoritative for installed state. The new cache is
advisory, stored separately, and safe to discard.

## Problem

The current command path is:

```text
cli.Run
  -> runCLIPreflight
    -> library.AutoUpdateSkills
      -> library.CheckSkillUpdates
        -> ResolveMaterializedSource
          -> git clone
```

`CheckSkillUpdates` iterates over lock entries, not unique sources. It
materializes each Git source before `AutoUpdateSkills` filters check results by
update policy. A manual Git skill therefore pays the same network and clone cost
as an auto-updating Git skill.

The same live check also runs when the MCP server attaches generic update
notices to a tool result. An explicit `check_skill_updates` tool call can perform
its requested live check and then perform another live check while attaching
notices.

Live measurements from the affected installation:

| Command | Current | With `--no-check` |
|---|---:|---:|
| `upskill list` | 2.6–2.9 seconds | about 0.01 seconds |
| `upskill show team-greeting-demo` | 2.8–3.1 seconds | about 0.01 seconds |
| Check one local skill | 0.01–0.05 seconds | not applicable |
| Check one manual Git skill | about 2.4 seconds | not applicable |

The installed lock contained 33 local skills and one Git skill. The single Git
check accounted for nearly all observed latency. `team-greeting-demo` was not
backed by that Git source, but `show` still waited for the unrelated clone.

The behavior is correct according to the current root specification, which says
that every command with skill or source behavior runs a live preflight. The
performance problem therefore requires an explicit contract change, not only a
local optimization inside `materialize`.

## Goals

- Make ordinary installed-skill commands local and fast in the default
  `manual` policy configuration.
- Prevent manual Git sources from causing implicit network traffic.
- Preserve live local-source notices and local automatic updates.
- Preserve bounded automatic updates for Git skills whose policy is `auto`.
- Preserve fully live behavior for explicit update checks, diffs, sync
  previews, source catalog reads, installs, and updates.
- Avoid cloning an unchanged Git source.
- Materialize a changed Git repository/ref at most once per check operation,
  regardless of how many installed skills use it.
- Prevent a transient network failure from blocking an unrelated command.
- Keep existing CLI and MCP output compatible.
- Keep `upskill-lock.json` deterministic and free of transient timestamps.
- Add deterministic tests that count Git operations and prove the fast path
  performs no network work.

## Non-Goals

- Do not add a daemon, background worker, or detached refresh process.
- Do not make source catalog commands answer from stale data.
- Do not change the meaning of `upskill update`, `upskill sync`, or their MCP
  equivalents.
- Do not cache local source contents.
- Do not store Git credentials, access tokens, or remote URLs in cache files.
- Do not add update-check timestamps to `upskill-lock.json`.
- Do not guarantee a fixed duration for explicit network operations.
- Do not optimize the rendering path or unrelated command startup work.
- Do not make automatic updates install new skills that appear upstream.

## Terminology

- Preflight check: The implicit installed-skill freshness check that runs before
  an ordinary CLI command or while attaching generic MCP notices.
- Live check: A check that reads the local source now or contacts the Git remote
  now and inspects the requested revision.
- Explicit check: A user-requested operation such as `update --check`,
  `update --diff`, `sync --dry-run`, or MCP `check_skill_updates`.
- Materialization identity: The source type, canonical Git URL, and Git ref that
  identify one repository checkout. Source and skill subpaths do not change the
  repository checkout.
- Lock fingerprint: A deterministic hash of the lock fields that affect update
  status for every installed skill in one materialization group.
- Fresh cache record: A valid record whose lock fingerprint matches and whose
  successful check time is within the configured preflight time-to-live.
- Stale cache record: A structurally valid record that is older than the
  preflight time-to-live.

## Decision

Upskill will expose two internal update-check modes:

1. `live`: authoritative and synchronous;
2. `preflight`: source-aware, cached for auto Git sources, and non-blocking on
   implicit remote refresh failure.

The existing exported-internal `CheckSkillUpdates` function retains live
semantics for compatibility. A new preflight entry point handles implicit CLI
and MCP notices. This separation limits the risk of changing a function used by
CLI update/diff/sync code and MCP tools.

### Behavior Matrix

| Caller | Local source | Manual Git source | Auto Git source |
|---|---|---|---|
| Ordinary CLI preflight | Live filesystem check | Cached result only; no network | Fresh cache, or remote revision probe when stale |
| Generic MCP notices | Live filesystem check | Cached result only; no network | Fresh cache, or remote revision probe when stale |
| `update --check` | Live | Live | Live |
| `update --diff` | Live | Live | Live |
| `sync --dry-run` | Live | Live | Live |
| MCP `check_skill_updates` | Live | Live | Live |
| `source skills/find/search` primary operation | Live | Live | Live |
| `update` or `sync` primary operation | Live and apply | Live and apply | Live and apply |
| Preflight disabled with `--no-check` | Skipped | Skipped | Skipped |

`--no-check` suppresses only implicit preflight. It must not suppress the
primary live operation when the user explicitly runs `update --check`,
`update --diff`, `sync`, or a source catalog command.

### Manual Git Policy

`manual` means Git freshness is discovered by an explicit live operation.
Ordinary commands do not contact a manual Git remote.

A successful explicit check may seed a cached result. Later ordinary commands
may repeat a known update notice from that record while the lock fingerprint
still matches. They must not present the absence of a cache record as proof that
the source is current.

Local manual sources remain live because they require no network and measured
check cost is negligible.

### Auto Git Policy

An auto Git source may be checked implicitly, but not more than once per
preflight time-to-live. The default time-to-live is one hour.

Add configuration:

```json
{
  "preflight_check_ttl": "1h"
}
```

Add environment override:

```text
UPSKILL_PREFLIGHT_CHECK_TTL=1h
```

Rules:

- The environment value overrides config.
- The default is `1h`.
- Go duration syntax is accepted.
- `0` forces auto Git revision validation on every preflight.
- Negative or invalid values fail config loading with an actionable error.
- `--no-check` and `UPSKILL_NO_CHECK=1` still skip all preflight work.

The time-to-live bounds automatic Git freshness. It does not affect explicit
live operations.

## Backend Design

### Update Check API

Keep the current function as a live compatibility wrapper:

```go
func CheckSkillUpdates(
    cfg config.Config,
    names []string,
    includeDiff bool,
) ([]UpdateCheck, error)
```

Add an options-based internal implementation:

```go
type UpdateCheckMode string

const (
    UpdateCheckLive      UpdateCheckMode = "live"
    UpdateCheckPreflight UpdateCheckMode = "preflight"
)

type UpdateCheckOptions struct {
    Names       []string
    IncludeDiff bool
    Mode        UpdateCheckMode
}

type UpdateCheckResult struct {
    Checks   []UpdateCheck
    Warnings []UpdateCheckWarning
}

func CheckSkillUpdatesWithOptions(
    cfg config.Config,
    opts UpdateCheckOptions,
) (UpdateCheckResult, error)
```

The existing function calls `CheckSkillUpdatesWithOptions` with `Mode=live` and
returns `result.Checks`.

Add a narrow preflight wrapper:

```go
func CheckSkillUpdatesForPreflight(
    cfg config.Config,
) (UpdateCheckResult, error)
```

Preflight never includes diffs. A request with `IncludeDiff=true` must use live
mode.

### Result Metadata

Extend `UpdateCheck` with optional fields:

```go
CheckOrigin string `json:"check_origin,omitempty"`
CheckedAt   string `json:"checked_at,omitempty"`
Stale       bool   `json:"stale,omitempty"`
```

Allowed `check_origin` values:

- `live_local`
- `live_git`
- `cache`
- `lock_revision`

Add structured warnings:

```go
type UpdateCheckWarning struct {
    Type      string `json:"type"`
    SourceKey string `json:"source_key,omitempty"`
    Message   string `json:"message"`
}
```

Initial warning types:

- `remote_check_deferred`
- `remote_check_failed`
- `cache_ignored`

Human CLI output may continue using the existing `warning:` prefix. MCP notices
must map these warnings to structured notice objects.

### Materialization Groups

Before checking, group selected Git lock entries by:

```text
source_type + NUL + canonical_source_url + NUL + ref
```

Do not include `source_subpath` or `skill_subpath` in the materialization key.
Those fields select content inside one checkout.

Sort group keys and lock keys before processing so check output remains
deterministic.

One changed or force-live Git group gets one `ResolveMaterializedSource` call.
Every lock entry in the group is then resolved from that shared root using its
own source and skill subpaths.

Local entries may continue using their direct paths. They do not need a
persistent cache or a shared checkout.

### Lock Fingerprint

Compute one lock fingerprint per materialization group from sorted records
containing:

```text
store_name
source_type
source_url
ref
source_subpath
skill_subpath
resolved_revision
hash
update_policy
```

Use SHA-256 and prefix the result with `sha256:`.

The cache is invalid when:

- its schema version is unsupported;
- its lock fingerprint differs;
- a required cached skill result is missing;
- a timestamp cannot be parsed;
- cached result data is malformed.

An invalid cache is ignored. It never changes the lock file or installed store.

### Cache Storage

Store one record per materialization identity:

```text
${UPSKILL_HOME}/cache/update-checks/<sha256-key>.json
```

The filename key is the SHA-256 hash of the materialization identity. The
identity itself is not written to the filename.

Example record:

```json
{
  "schema_version": 1,
  "checked_at": "2026-07-30T18:30:00Z",
  "last_attempt_at": "2026-07-30T18:30:00Z",
  "retry_after": "",
  "last_failure": "",
  "resolved_revision": "0123456789abcdef",
  "lock_fingerprint": "sha256:...",
  "checks": {
    "github-issue-triage": {
      "name": "GitHub Issue Triage",
      "current_revision": "fedcba9876543210",
      "resolved_revision": "0123456789abcdef",
      "current_hash": "sha256:...",
      "new_hash": "sha256:...",
      "changed": true
    }
  }
}
```

Cache rules:

- Create the cache directory lazily with mode `0700`.
- Write records with mode `0600`.
- Write to a unique temporary file in the same directory, then rename.
- A reader must see either the old complete record or the new complete record.
- Concurrent last-writer-wins behavior is acceptable because the cache is
  advisory and lock fingerprints prevent incorrect reuse.
- Do not store source URLs, command output, credentials, or raw Git errors.
- `last_failure` stores only a bounded code such as `remote_probe_failed`.
- A corrupt record is ignored; do not delete it during ordinary preflight.
- `doctor` may report corrupt or orphaned records in a later change.

No lock schema migration is required.

### Remote Revision Probe

Add a Git revision probe that does not create a working tree.

Pinned commit:

```text
40-character commit ref -> use the ref directly; run no Git command
```

Default branch:

```text
git ls-remote --symref <url> HEAD
```

Select the commit from the `HEAD` result.

Explicit branch or tag:

```text
git ls-remote <url> \
  refs/heads/<ref> \
  refs/tags/<ref> \
  refs/tags/<ref>^{}
```

Resolution order:

1. exact branch;
2. peeled annotated tag;
3. exact lightweight tag.

No match is a source-resolution failure.

All probe commands set:

```text
GIT_TERMINAL_PROMPT=0
GIT_LFS_SKIP_SMUDGE=1
```

The probe must use `exec.CommandContext` with a two-second preflight timeout.
Explicit live operations retain their current timeout behavior in this change.

If a preflight probe times out or fails:

- do not mutate installed state;
- do not fail the primary command;
- return a structured warning;
- reuse a valid previous successful record, marked `stale=true`, if one exists;
- otherwise omit results for that Git group;
- record a five-minute retry backoff without persisting raw error output.

During the retry window, ordinary preflight performs no Git operation.

### Auto Git Preflight Algorithm

For each auto Git materialization group:

1. Read and validate the cache record against the lock fingerprint.
2. If the record is fresh, return cached checks.
3. If a retry backoff is active, return a stale successful record when
   available and emit `remote_check_deferred`.
4. Resolve the current remote revision with the lightweight probe.
5. If the remote revision matches every selected lock entry's resolved
   revision, construct unchanged checks from lock metadata, write a fresh cache
   record, and do not clone.
6. If the remote revision matches a valid stale cache record's resolved
   revision, refresh its timestamp and return its cached checks.
7. Otherwise materialize the Git group once, discover and hash every selected
   installed skill in that group, and write a fresh record.
8. Return checks to `AutoUpdateSkills`.

If a cached check reports an auto update, `AutoUpdateSkills` uses the existing
live `UpdateSkills` path before the primary command. An update application
failure remains fatal because installed or rendered state may otherwise be
inconsistent.

Successful install and update operations should write through an unchanged
cache record for their touched Git groups. The lock fingerprint also guarantees
that an old changed record cannot survive a lock update.

### Manual Git Preflight Algorithm

For each manual Git materialization group:

1. Read the cache record.
2. If it matches the current lock fingerprint, return its known results without
   checking age and without network work.
3. If no valid record exists, return no update result for that group.

An explicit live check writes the record and can therefore make a known manual
update notice available to later ordinary commands.

The preflight path must not synthesize an `unchanged` result when no manual Git
cache exists. Unknown and unchanged are different states.

### Local Preflight Algorithm

Local source checks retain current behavior:

1. Resolve the recorded path.
2. Discover the locked skill.
3. Hash its current directory.
4. Compare the hash with the lock entry.
5. Apply an automatic update or print a manual notice according to policy.

Local check failures retain current behavior:

- an auto-policy failure stops automatic update preflight;
- a manual-policy failure prints a warning and allows the primary command.

### Cache Write-Through

These successful operations seed or refresh cache records:

- Git `InstallSource`;
- `UpdateSkills`;
- live `CheckSkillUpdates`;
- CLI and MCP sync previews;
- explicit CLI and MCP update checks.

A partial named live check may merge results into an existing matching record.
If safe merging would be ambiguous, it may leave the existing record unchanged.
It must not replace a complete record with an incomplete one.

Source catalog reads may seed only the resolved revision. They are not required
to hash installed skills solely to populate update-check cache data.

## CLI Behavior

### Ordinary Commands

Commands such as:

```text
upskill list
upskill show <installed-skill>
upskill find <query>
upskill status
upskill doctor
```

must not run Git for manual sources.

With only manual Git sources and local sources, their runtime should be
equivalent to local source hashing plus their primary command.

If an auto Git refresh fails, print:

```text
warning: automatic update check deferred for <source>; using cached status
```

If no cached status exists:

```text
warning: automatic update check deferred for <source>
```

Do not print a warning for a manual Git source that has never been explicitly
checked.

### Explicit Commands

These commands bypass preflight cache for their primary operation:

```text
upskill update --check
upskill update --diff
upskill sync --dry-run
upskill update
upskill sync
upskill source skills
upskill source find
upskill source search
```

They retain current live error behavior and return current remote state.

### Help

Update top-level help for `--no-check`:

```text
--no-check  Skip implicit local and cached/automatic preflight checks.
            Explicit update and source operations remain live.
```

Document `UPSKILL_PREFLIGHT_CHECK_TTL`.

## MCP Behavior

Generic notices attached after a tool call use preflight mode. They must not
perform an uncached live check of every lock entry.

The `check_skill_updates` tool remains live and bypasses preflight cache for its
primary result. Attaching generic notices after that tool call must reuse
preflight cache and must not trigger a second remote operation.

Add optional notice fields:

```json
{
  "type": "remote_check_deferred",
  "message": "automatic update check deferred",
  "source_key": "sha256:...",
  "stale": true
}
```

Do not expose the source URL through `source_key`.

No MCP input schema changes are required.

## Failure Semantics

| Failure | Live explicit operation | Implicit preflight |
|---|---|---|
| Local source read/hash | Existing failed check behavior | Manual warns; auto fails |
| Git revision probe | Failed check | Warn, use stale cache if valid, continue |
| Git clone/checkout | Failed check | Warn, use stale cache if valid, continue |
| Cache read/parse | Not relevant to authoritative result | Ignore cache and continue |
| Cache write | Return live result; warn if surfaced | Use computed result; warn and continue |
| Automatic update apply | Error | Error before primary command |

Preflight may be stale or unavailable, but it must never claim a failed or
skipped remote check is current.

## Compatibility And Migration

- Existing config files load with the one-hour default.
- Existing lock schema 1 and schema 2 files remain readable.
- The lock schema does not change.
- Existing CLI commands and flags retain their syntax.
- Existing MCP tool inputs retain their schemas.
- Added JSON fields are optional.
- `CheckSkillUpdates` remains live for existing internal callers.
- `--no-check` keeps its current placement-independent parsing.
- Existing caches are absent on upgrade. Manual Git sources immediately use the
  fast path. Auto Git sources perform one revision probe, then seed cache state.
- The root specification must be updated during implementation: replace the
  requirement that every preflight is fully live with the source-aware behavior
  in this document.
- README and workflow documentation must explain that manual Git update
  discovery is explicit while local manual discovery remains implicit.

## Security And Privacy

- Cache filenames reveal only a SHA-256 key.
- Cache contents omit source URLs and Git command output.
- Cache directory and file modes protect local metadata.
- Git commands remain non-interactive.
- Cache data never authorizes source writes.
- A cache record never overrides lock source identity, installed hashes, or
  update policy.
- Path resolution and source discovery keep their current traversal checks.
- Corrupt or attacker-controlled cache data can cause only a cache miss, never a
  source path change or installed-state mutation.

## Observability

Tests and structured results must distinguish:

- live local checks;
- live Git checks;
- cache hits;
- lock-revision fast paths;
- deferred remote checks;
- stale cache reuse.

Do not print per-command cache diagnostics by default. Normal human output
should change only when an update notice or warning is actionable.

Performance tests should count executed Git subcommands through a fake `git`
binary on `PATH`. Production code must not contain test-only branches.

## Implementation Tasks

### 1. Check Mode Separation

- Add `UpdateCheckMode`, `UpdateCheckOptions`, `UpdateCheckResult`, and warning
  types.
- Keep `CheckSkillUpdates` as a live wrapper.
- Add `CheckSkillUpdatesForPreflight`.
- Route CLI implicit preflight through the new preflight function.
- Keep CLI update/diff/sync preview paths live.
- Route MCP generic notices through preflight.
- Keep MCP `check_skill_updates` and sync preview live.

Testing tasks:

- Regression test every current live caller.
- Test `--no-check` still suppresses only implicit preflight.
- Test MCP explicit check does not perform a second live Git check while
  attaching notices.
- Test live and preflight result ordering remains deterministic.

### 2. Cache Store And Configuration

- Parse `preflight_check_ttl` and `UPSKILL_PREFLIGHT_CHECK_TTL`.
- Add cache record structs in a focused internal package.
- Add materialization identity and lock fingerprint helpers.
- Add atomic per-source record reads and writes.
- Add strict permissions and safe cache-key generation.
- Add deterministic clock injection through a function or small clock
  interface, not production test branches.

Testing tasks:

- Test default, config, environment override, zero, negative, and invalid TTL.
- Test deterministic materialization keys and lock fingerprints.
- Test cache hit, miss, stale, malformed, wrong schema, and fingerprint
  mismatch.
- Test atomic replacement and permissions.
- Test cache records do not contain URLs, credentials, or raw errors.
- Test concurrent readers never observe partial JSON.

### 3. Git Revision Probe And Grouped Checks

- Add remote revision resolution for default branch, branch, annotated tag,
  lightweight tag, and pinned commit.
- Add the two-second preflight command timeout.
- Group Git checks by materialization identity.
- Implement unchanged-revision fast path.
- Materialize changed groups once and check all selected lock entries.
- Preserve per-skill errors when one skill is missing or cannot be hashed.

Testing tasks:

- Use local bare Git remotes; do not require public network access.
- Test default branch, mutable branch, moved tag, annotated tag, lightweight
  tag, missing ref, and pinned commit.
- Test a pinned commit executes no remote probe.
- Test an unchanged stale auto source runs one `ls-remote` and zero clones.
- Test a changed source with multiple installed skills runs one clone.
- Test multiple refs from one repository form separate groups.
- Test source and skill subpaths resolve correctly from a shared checkout.
- Test probe timeout and retry backoff.

### 4. Policy-Aware Preflight

- Keep local manual and auto checks live.
- Make manual Git preflight cache-only.
- Apply TTL behavior to auto Git groups.
- Reuse stale successful auto results after a failed refresh.
- Add warning mapping for CLI and MCP.
- Keep actual auto-update application failures fatal.

Testing tasks:

- Test ordinary `list` with a manual Git skill executes no Git process.
- Test installed `show` with an unrelated manual Git skill executes no Git
  process.
- Test manual Git cache miss emits no false unchanged result.
- Test explicit manual Git check seeds a notice cache.
- Test fresh auto Git cache executes no Git process.
- Test stale auto Git cache refreshes once.
- Test remote failure does not block the primary command.
- Test local auto-update behavior remains unchanged.

### 5. Cache Write-Through

- Seed cache after Git install.
- Refresh cache after successful update and sync.
- Populate cache after successful live checks.
- Ensure changed cache records are invalid after lock updates.
- Avoid replacing complete records with partial named-check records.

Testing tasks:

- Test install followed by preflight does not clone.
- Test update followed by preflight does not report the old changed result.
- Test lock hash, revision, policy, or subpath change invalidates cache.
- Test failed writes do not affect authoritative update results.

### 6. Documentation And Benchmarks

- Update the root specification's preflight contract.
- Update README automatic update documentation.
- Update getting-started and workflow examples.
- Document the TTL config and environment variable.
- Document manual Git explicit-check semantics.
- Add a repeatable benchmark command or Go benchmark fixture.

Testing tasks:

- Run repository text/help tests.
- Add help coverage for the TTL environment variable and clarified
  `--no-check`.
- Run the baseline benchmark before and after implementation.

## Performance Acceptance Criteria

Use a fixture with at least 30 local locked skills and one manual Git locked
skill.

Development-machine verification on 2026-07-30 used the checked-in
`TestWarmInstalledListAndShowPreflightPerformance` fixture with 30 local skills
and one manual Git skill:

| Command | Before | After warm p95 | Git subprocesses |
|---|---:|---:|---:|
| `upskill list` | 2.6–2.9 seconds | 13.98 milliseconds | 0 |
| `upskill show <installed-local-skill>` | 2.8–3.1 seconds | 11.18 milliseconds | 0 |

The repeatable Go benchmark command is:

```bash
go test ./internal/cli -run '^$' \
  -bench 'BenchmarkWarmInstalled(List|Show)$' \
  -benchtime=20x -count=1
```

On the same machine it measured mean warm times of 14.18 milliseconds for
`list` and 9.81 milliseconds for installed `show`.

The final read-only check against the affected installation used a lock with 33
local manual skills and one manual Git skill. The worktree-built binary
completed `upskill list --json` in 0.05 seconds and installed
`upskill show agent-plan` in 0.11 seconds. A wrapper placed ahead of `git` on
`PATH` recorded zero Git subprocesses across both commands.

- Warm `upskill list` p95 is below 100 milliseconds on the development machine.
- Warm installed `upskill show <skill>` p95 is below 100 milliseconds.
- Both commands execute zero Git subprocesses for manual Git sources.
- A fresh auto Git cache executes zero Git subprocesses.
- An unchanged stale auto Git source executes one `ls-remote` and zero clones.
- A changed Git source shared by multiple installed skills is cloned at most
  once per check operation.
- A pinned commit executes no remote probe during preflight.
- An unreachable auto Git remote cannot delay preflight longer than the
  two-second timeout, and the five-minute backoff prevents repeated delays.
- Explicit update checks still observe a remote change immediately.

Wall-clock network assertions should be limited to the timeout boundary. All
other performance tests should assert operation counts and fast-path behavior,
which are stable in CI.

## End-To-End Scenarios

### Manual Git Source And Installed Show

1. Install a skill from a Git source with `update_policy=manual`.
2. Install or create an unrelated local skill.
3. Clear update-check cache.
4. Run `upskill show <local-skill>`.
5. Verify the command shows the local skill.
6. Verify no Git command ran and no remote status was claimed.

### Explicit Manual Check

1. Advance the manual Git remote.
2. Run `upskill update --check <git-skill>`.
3. Verify the live result reports the update.
4. Run `upskill list`.
5. Verify the cached manual update notice appears without a Git command.

### Auto Git Source

1. Install a Git skill with `update_policy=auto`.
2. Verify install seeds a fresh cache record.
3. Run `upskill list` and verify no Git command runs.
4. Advance time beyond the TTL without changing the remote.
5. Run `upskill list` and verify one revision probe and no clone.
6. Advance the remote.
7. Run `upskill list` and verify one grouped materialization, automatic update,
   render, and refreshed cache record.
8. Run `upskill list` again and verify no Git command runs.

### Offline Auto Source

1. Seed a successful auto Git cache.
2. Make the remote unavailable and advance time beyond the TTL.
3. Run an ordinary installed-skill command.
4. Verify the primary command succeeds with a deferred warning.
5. Run it again inside the retry window.
6. Verify no Git command runs.
7. Run an explicit update check.
8. Verify the explicit operation reports the live remote failure.

### MCP Explicit Check

1. Call `check_skill_updates` for a Git skill.
2. Verify one live source operation supplies the tool result.
3. Verify generic notice attachment performs no second live source operation.
4. Verify structured notice and check metadata remain valid JSON.

## Rollout Plan

1. Land check-mode separation with existing behavior still selected everywhere.
2. Land cache storage, Git probing, and grouped live checks behind preflight
   mode tests.
3. Switch CLI implicit preflight to preflight mode.
4. Switch MCP generic notices to preflight mode.
5. Update root specification and user-facing documentation.
6. Run the full test suite and before/after benchmarks.
7. Verify live commands against a local bare Git remote.
8. Verify the affected real installation: `list` and installed `show` must run
   without cloning the manual team source.

No feature flag is required because `UPSKILL_PREFLIGHT_CHECK_TTL=0` can restore
per-command validation for auto Git sources, while explicit live commands retain
their old behavior throughout the rollout.

## Acceptance Criteria

- Ordinary CLI commands and generic MCP notices do not contact manual Git
  sources.
- Local update notices and local automatic updates remain live.
- Auto Git checks are bounded by the configured TTL.
- Unchanged auto Git revisions do not clone.
- Changed Git repositories are materialized once per source/ref group.
- Explicit checks and source catalog reads remain live.
- Implicit remote failure cannot block an unrelated command.
- Actual automatic update application failure remains fatal.
- Cache state is separate from and subordinate to the lock file.
- Existing config and lock files require no migration.
- Existing command and MCP input syntax remains compatible.
- Focused CLI, library, MCP, cache, Git, failure, and performance tests pass.
- The full repository test suite passes.

## Open Questions

No open question blocks implementation.

Future changes may consider:

- a user-facing cache status or clear command;
- a persistent Git object cache to reduce the cost of actual changed-source
  materialization;
- separate TTL defaults for manual-notice refresh and auto-update refresh if
  product requirements later restore implicit manual Git checks.
