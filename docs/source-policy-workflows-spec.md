# Source Policy Workflows Spec

## Status

Draft.

## Summary

Upskill should support self-improving agent workflows without making `store/`
or rendered agent output editable. Agents should be able to choose between:

- writing overlays for local, team, or agent-specific customization;
- creating drafts for upstream skill changes;
- promoting drafts into writable source checkouts;
- directly editing source skill files when the source policy allows it.

The missing feature is source policy metadata. Upskill currently knows where a
source lives, but it does not know whether a source is personal, shared by a
team, externally owned, writable by agents, or overlay-preferred. This spec adds
source metadata through CLI flags, config fields, and optional checked-in
`upskill.toml` source manifests. CLI and MCP surfaces will expose that metadata
as guidance so agents can ask the right question before changing a skill.

## Problem

Upskill already separates the layers:

- Source directories and Git repos are the upstream skill collections.
- `store/` is the installed synced copy that rendering uses.
- `overlays/` and `drafts/` are managed user or agent working state.
- Rendered directories and agent output directories are generated output.

That separation prevents agents from silently rewriting installed base skills.
However, it also leaves agents without enough context when the user asks to
improve a skill. The agent cannot reliably tell whether it should make an
overlay, create a draft intended for upstream promotion, or directly edit a
personal source file.

The current MCP safety boundary is correct: no direct `store/` writes, no
install tool, and no GitHub branch, commit, push, PR, or merge automation. The
gap is that source mutation workflows are not policy-aware enough.

## Goals

- Add explicit source policy metadata for personal, team, external, and
  read-only skill sources.
- Let users mark configured sources as shared/team sources from the CLI.
- Let source repos publish default policy metadata in `upskill.toml`.
- Expose source policy through CLI JSON, human-readable CLI output, and MCP
  tool results.
- Guide agents toward overlays for local behavior changes and drafts/source
  edits for upstream skill changes.
- Allow direct source edits for writable personal sources when explicitly
  requested.
- Keep draft promotion available for team sources, with dry-run-first behavior
  and no VCS side effects.
- Preserve existing config compatibility and existing lock-file semantics.
- Add robust tests for config parsing, manifest discovery, CLI UX, MCP API
  contracts, policy enforcement, source edit behavior, and docs/prompts.

## Non-Goals

- Do not let MCP tools edit `store/`, rendered directories, managed overlays
  outside the overlay tools, drafts outside the draft tools, or configured
  import directories.
- Do not make Upskill commit, push, merge, create branches, or open pull
  requests.
- Do not auto-install every new skill that appears in a source.
- Do not require every source to include `upskill.toml`.
- Do not require users to fork a team skill repo to keep long-lived local
  customizations. Overlays remain first-class.
- Do not replace drafts with overlays or overlays with drafts. Both workflows
  remain available.

## Terminology

- Source: A configured or raw upstream skill collection.
- Source manifest: Optional `upskill.toml` checked into a source root.
- Source policy: Effective metadata after merging source manifest, local config,
  and CLI flags.
- Personal source: A source owned by the current user where direct source edits
  are acceptable.
- Team source: A source shared by multiple users, typically backed by GitHub.
- External source: A source owned by another project or vendor.
- Overlay-preferred: A policy that tells agents to prefer overlays unless the
  user explicitly asks for an upstream source change.
- Draft-promote: A policy that tells agents to stage base changes in a draft and
  promote only after review.

## Source Policy Model

Extend `config.Source` with optional policy fields:

```go
type Source struct {
    ID                  string   `json:"id,omitempty"`
    Name                string   `json:"name,omitempty"`
    Source              string   `json:"source"`
    Ref                 string   `json:"ref,omitempty"`
    Subpath             string   `json:"subpath,omitempty"`
    DefaultUpdatePolicy string   `json:"default_update_policy,omitempty"`
    Scope               string   `json:"scope,omitempty"`
    WritePolicy         string   `json:"write_policy,omitempty"`
    Authors             []string `json:"authors,omitempty"`
    Owners              []string `json:"owners,omitempty"`
    LLMGuidance         string   `json:"llm_guidance,omitempty"`
}
```

Allowed `scope` values:

- `personal`
- `team`
- `external`

Allowed `write_policy` values:

- `direct_allowed`: MCP source-edit tools may write local source files after
  normal validation.
- `overlay_preferred`: Agents should ask whether the user wants an overlay
  before creating a draft or writing source files.
- `draft_promote`: Agents should use draft plus dry-run promotion for source
  changes.
- `read_only`: Source edit and promotion tools must reject writes.

Default policy:

- Local configured source: `scope=personal`, `write_policy=direct_allowed`.
- Git or GitHub source: `scope=external`, `write_policy=read_only`.
- `--shared` source: `scope=team`, `write_policy=draft_promote`.
- Manifest-provided policy applies when the local config does not override it.

## Source Manifest

Upskill should look for `upskill.toml` in the resolved source root. For a source
configured with `subpath`, the resolver should check both:

1. the repository or local source root;
2. the effective subpath root.

When both files exist, the subpath manifest can override source-root metadata
for that configured source.

Example:

```toml
schema_version = 1
name = "redis-applied-ai team skills"
scope = "team"
write_policy = "draft_promote"
authors = ["Redis Applied AI"]
owners = ["redis-applied-ai"]
llm_guidance = "Confirm whether the user wants an upstream team-skill change or a local overlay before creating a draft."
```

Invalid manifests should not make source discovery unusable by default. CLI and
MCP output should include a warning with the manifest parse error, then fall
back to configured/default policy. A strict validation command can fail hard.

## Policy Merge Rules

Effective source policy is resolved in this order:

1. explicit CLI flags passed to `source add`;
2. existing local config fields;
3. `upskill.toml` from the source;
4. source-type defaults.

Local config wins because it represents the user's local trust decision. Source
manifest wins over source-type defaults because team repos should be able to
publish their intended workflow.

## CLI Surface

### `upskill source add`

Add flags:

```text
--shared
--personal
--external
--write-policy direct_allowed|overlay_preferred|draft_promote|read_only
```

Rules:

- `--shared` sets `scope=team` and defaults `write_policy=draft_promote`.
- `--personal` sets `scope=personal` and defaults
  `write_policy=direct_allowed`.
- `--external` sets `scope=external` and defaults `write_policy=read_only`.
- Explicit `--write-policy` overrides scope defaults.
- Mutually exclusive scope flags must be rejected.
- `source add` should read `upskill.toml` and show any inferred policy in the
  result.

### `upskill source list --json`

Include:

```json
{
  "scope": "team",
  "write_policy": "draft_promote",
  "authors": ["Redis Applied AI"],
  "owners": ["redis-applied-ai"],
  "llm_guidance": "...",
  "policy_source": "config|manifest|default",
  "manifest_path": "...",
  "manifest_warning": ""
}
```

Human output should show `scope` and `write_policy` without making the table too
wide. If necessary, use a compact suffix such as `team/draft_promote`.

### `upskill source show`

If the project does not already have source detail output, add it or extend the
existing `show` behavior to display source policy, manifest path, and guidance.

### `upskill draft create`

When creating a draft from an installed skill or source skill, print policy
guidance:

- Team or overlay-preferred source: ask the user to confirm that an upstream
  skill change is intended instead of an overlay.
- Personal direct-allowed source: mention direct source edit as an option.
- Read-only or external source: explain that a draft can be rendered locally but
  cannot be promoted back through Upskill without a writable local source.

### `upskill draft promote`

Keep current behavior:

- dry-run by default;
- writes only when explicitly requested;
- rejects managed Upskill directories;
- rejects remote sources without writable local paths;
- does not perform VCS operations.

Add policy checks:

- `read_only` rejects promotion.
- `overlay_preferred` allows promotion but prints an overlay reminder.
- `draft_promote` is the normal team-source path.
- `direct_allowed` allows promotion.

### `upskill source edit`

Add a direct source-edit command only if the implementation also adds an MCP
source-edit tool. The command should edit a source skill file in the resolved
source checkout, not `store/`.

Possible shape:

```bash
upskill source edit <source> <skill-name> --file SKILL.md
```

Policy:

- allowed for `direct_allowed`;
- allowed with warning for `overlay_preferred`;
- rejected for `draft_promote` unless `--yes-upstream-change` is supplied;
- rejected for `read_only`.

## MCP API Surface

All MCP policy data should be structured, not only prose.

### Existing Tools

Update these tools to return source policy when a source or installed skill can
be resolved:

- `list_sources`
- `find_source_skills`
- `find_skills`
- `read_skill`
- `create_skill_draft`
- `read_skill_draft`
- `promote_skill_draft`
- `render_skill_draft`

Add a `source_policy` object:

```json
{
  "source_id": "src_...",
  "source_name": "team-skills",
  "scope": "team",
  "write_policy": "draft_promote",
  "policy_source": "config",
  "llm_guidance": "Confirm whether the user wants an upstream team-skill change or a local overlay.",
  "recommended_workflow": "overlay|draft|direct_source_edit|read_only"
}
```

Add `notices` when the agent starts a draft/edit workflow against a source where
the choice matters.

### New Tool: `upsert_source_skill_file`

Add a direct source edit MCP tool only for configured local sources.

Input:

```json
{
  "source": "personal",
  "skill_id": "redis",
  "path": "SKILL.md",
  "content": "...",
  "base64_content": "",
  "dry_run": true,
  "confirmed_upstream_change": false,
  "reason": "User asked to update the personal source skill."
}
```

Output:

```json
{
  "source": "personal",
  "skill_id": "redis",
  "path": "/abs/source/redis/SKILL.md",
  "dry_run": true,
  "changed": true,
  "policy": {...},
  "base_mutated": false,
  "store_mutated": false,
  "next_steps": [
    "review the source diff",
    "run upskill update redis or upskill add personal --skill redis if it is new",
    "render the skill for the target agent"
  ]
}
```

Policy:

- Default `dry_run=true`.
- Reject paths outside the skill bundle.
- Reject writes into managed Upskill directories.
- Reject `read_only`.
- Reject `draft_promote` unless `confirmed_upstream_change=true` and `reason`
  is non-empty.
- For `overlay_preferred`, allow dry-run and return a notice; require
  `confirmed_upstream_change=true` for actual writes.

## Frontend And User-Facing UX

Upskill has no web frontend today. The user-facing surfaces are CLI output, CLI
help, docs, generated prompts, and structured MCP responses. This feature must
still treat UX as a first-class domain.

UX requirements:

- Human CLI output must show policy clearly without overwhelming source lists.
- JSON output must include complete policy fields.
- MCP output must include structured guidance that agents can act on.
- Prompt guidance in `prompts/AGENTS.md` must explain how to choose overlay,
  draft, direct source edit, and promotion.
- Docs must show examples for personal source, team source, overlay-first
  customization, and team draft-promotion.

## Backend Design

Backend work includes:

- source policy structs and validation;
- `upskill.toml` discovery and parse logic;
- source policy merge function;
- source policy fields in configured source serialization;
- source policy lookup for installed skills through lock entries and matching
  configured sources;
- source edit path resolution for configured local sources;
- policy checks shared by CLI and MCP;
- existing draft promotion path updated to use policy checks.

The source manifest parser should be isolated from config loading so config
compatibility remains simple. Loading `config.json` should not require accessing
every configured source on disk or network.

## Compatibility And Migration

- Existing `config.json` files must continue to load.
- Existing source records without policy fields use defaults.
- Existing lock files do not need schema changes for source policy.
- Existing `source list --json` consumers must tolerate added fields.
- Existing MCP tools must retain current input compatibility.
- Existing draft and overlay workflows must keep their current behavior when no
  policy metadata exists.

## Security And Safety

- Never write `store/`, rendered output, agent output directories, drafts, or
  overlays through source edit tools.
- Never write outside a resolved source skill bundle.
- Never perform VCS side effects.
- Keep MCP direct source writes dry-run by default.
- Require explicit confirmation fields for team/upstream source writes where
  policy requires it.
- Preserve existing promotion managed-directory rejection.
- Include `base_mutated=false` and `store_mutated=false` in source edit output
  so agents do not confuse source writes with installed-store writes.

## Implementation Tasks

### 1. Source Policy Backend

- Add source policy fields to `config.Source`.
- Add validation helpers for `scope` and `write_policy`.
- Add default policy resolution by source type.
- Add `EffectiveSourcePolicy(cfg, source)` and policy merge helpers.
- Add source manifest discovery for local source roots and subpath roots.
- Add `upskill.toml` parser and warning model.
- Add policy lookup from installed skill lock entries back to configured
  sources.

Testing tasks:

- Unit test existing config files without policy fields.
- Unit test object, map, and list source config shapes with policy fields.
- Unit test invalid `scope` and `write_policy` behavior.
- Unit test source-type defaults for local, GitHub shorthand, Git URL, and raw
  path sources.
- Unit test manifest discovery at source root and subpath root.
- Unit test config-overrides-manifest-overrides-default merge order.
- Unit test malformed manifest warning fallback.
- Unit test installed skill policy lookup through lock entries.

### 2. CLI Source Policy

- Add `source add` scope and write-policy flags.
- Add mutually exclusive validation for scope flags.
- Include source policy in `source list --json`.
- Include compact source policy in human `source list`.
- Add or extend source detail output to show policy, manifest path, and guidance.
- Update source-related help text.

Testing tasks:

- CLI test `source add --shared` writes `scope=team` and
  `write_policy=draft_promote`.
- CLI test `source add --personal` writes `scope=personal` and
  `write_policy=direct_allowed`.
- CLI test `source add --external` writes `scope=external` and
  `write_policy=read_only`.
- CLI test explicit `--write-policy` overrides scope defaults.
- CLI test mutually exclusive scope flags fail.
- CLI snapshot or substring test for human `source list`.
- CLI JSON test for all new policy fields.
- CLI help test for new flags and policy notes.

### 3. Draft And Promotion Policy

- Add source policy to draft creation summaries when a parent skill/source is
  known.
- Add notices for overlay-preferred and team draft creation.
- Add promotion policy checks.
- Keep promotion dry-run by default for MCP and existing CLI behavior.
- Preserve local-source-only promotion for named sources.

Testing tasks:

- Unit test draft create from team skill returns overlay-vs-upstream guidance.
- Unit test draft create from personal skill returns direct-edit alternative.
- Unit test draft create from read-only source returns read-only guidance.
- Unit test promotion rejects read-only source.
- Unit test promotion allows draft-promote source with dry-run and real write.
- Unit test promotion warns but allows overlay-preferred source.
- Regression test managed directory promotion rejection still passes.

### 4. Direct Source Edit Workflow

- Add source skill file path resolution for configured local sources.
- Add CLI `source edit` only if MCP direct edit is implemented.
- Add MCP `upsert_source_skill_file`.
- Make source edit dry-run by default for MCP.
- Add path traversal protection for source file writes.
- Add policy enforcement and confirmation fields.
- Return next steps explaining update/install/render follow-up.

Testing tasks:

- Unit test source edit resolves local source skill file.
- Unit test source edit rejects Git/GitHub remote source without local checkout.
- Unit test source edit rejects path traversal.
- Unit test source edit rejects managed Upskill directories.
- Unit test direct_allowed source can dry-run and write.
- Unit test overlay_preferred source requires confirmation for actual write.
- Unit test draft_promote source requires confirmation and reason for actual
  write.
- Unit test read_only source rejects all writes.
- MCP contract test for `upsert_source_skill_file` output shape.
- CLI test for `source edit` behavior if the CLI command is included.

### 5. MCP API Guidance

- Include `source_policy` in source and skill results where resolvable.
- Include `recommended_workflow` in policy output.
- Add notices when tools are used against policy-sensitive sources.
- Update MCP tool descriptions and JSON schemas.
- Keep no install-capable MCP tool unless explicitly revisiting that separate
  product decision.

Testing tasks:

- MCP test `list_sources` includes policy fields.
- MCP test `find_source_skills` includes policy fields.
- MCP test `find_skills` includes installed skill source policy.
- MCP test `read_skill` includes policy for installed base skills.
- MCP test `create_skill_draft` emits notices for team/overlay-preferred
  sources.
- MCP test no install-capable tool is exposed.
- MCP schema test for new tool descriptions and required fields.

### 6. User-Facing Docs And Prompts

- Update `README.md` source and overlay sections.
- Update `docs/getting-started.md` with personal and team source examples.
- Update `docs/workflows.md` with overlay, draft-promote, and direct source edit
  decision flow.
- Update `prompts/AGENTS.md` so agents know how to choose between overlay,
  draft, direct source edit, and promotion.
- Add an example `upskill.toml` to `examples/` or docs.

Testing tasks:

- Run docs lint or repository text checks if present.
- Run help text tests covering new CLI help.
- Add prompt fixture tests if the repo has prompt validation.
- Add example manifest parse test using the checked-in example.

### 7. End-To-End Scenarios

- Personal local source direct edit lifecycle.
- Team source overlay-first lifecycle.
- Team source draft-promote lifecycle.
- External read-only source rejection lifecycle.

Testing tasks:

- E2E test personal source: add source, install skill, edit source file, update
  installed skill, render, and verify rendered content.
- E2E test team source: add `--shared`, create overlay, render, and verify
  overlay applied without source mutation.
- E2E test team draft-promote: create draft from installed skill, modify draft,
  dry-run promote, real promote to local source, update installed skill, render,
  and verify content.
- E2E test read-only external source: attempt direct edit and promotion; verify
  both fail without store mutation.
- E2E test MCP agent path using stdio requests for source list, draft create,
  source edit dry-run, promotion dry-run, and render.

## Acceptance Criteria

- Users can mark a source as shared/team from the CLI.
- Sources can publish `upskill.toml` policy metadata.
- CLI and MCP output expose effective policy.
- Agents receive actionable guidance before changing team/shared skills.
- Overlays, drafts, promotion, and direct source edit are all available where
  policy allows.
- MCP still cannot edit `store/`, install new source skills, or perform VCS
  operations.
- Existing configs and workflows keep working without modification.
- Tests cover backend, CLI, MCP/API, user-facing UX, policy enforcement, and
  end-to-end lifecycle behavior.

## Open Questions

- Should `upskill.toml` be the manifest name, or should the project use JSON to
  avoid adding a TOML dependency?
- Should direct source editing be CLI plus MCP, or MCP-only at first?
- Should `write_policy=draft_promote` require confirmation for real promotion,
  or is draft promotion itself enough confirmation?
- Should policy metadata be copied into the lock file for historical reporting,
  or resolved live from configured sources only?
- Should raw local path installs infer `personal/direct_allowed` but warn when
  the path is inside a Git checkout without configured source metadata?
