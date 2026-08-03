# upskill

`upskill` is a standalone Go CLI and stdio MCP server for sharing agent skills
with a team. It can install skills from local or Git sources, keep installed
skills in sync with upstream sources, build generated output from local
customizations, and expose skills to agents through MCP or folders on disk
without allowing agents to overwrite base skills.

What makes Upskill effective for teams is that you can customize the behavior of
any installed skill without changing the skill. Agents see a rendered version of
the skill with your changes applied. To make team skills easier to customize,
you can optionally author skill files as Jinja templates, and users of the skill
can change them with Jinja syntax.

## Quick Start

```bash
make
mkdir -p ~/.local/bin
install -m 0755 bin/upskill ~/.local/bin/upskill
upskill doctor
upskill add ./examples --skill hello-skill --agent codex
upskill show hello-skill --agent codex
```

The binary is written to `bin/upskill`. Installing it to `~/.local/bin` is
optional; you can also run `bin/upskill` directly.

Start with [docs/getting-started.md](docs/getting-started.md) for installation,
first-run config, the local example skill, and MCP setup. Use
[docs/workflows.md](docs/workflows.md) for task-oriented examples covering
local customizations through MCP, drafts, promotion to PRs, updates, and Jinja
template customizations.

For repository validation:

```bash
make test
make coverage
```

## Commands

```bash
upskill add <source> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--update-policy manual|auto] [--no-render]
upskill show <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--stats] [--nocontent]
upskill list|ls [--json] [--agent AGENT]
upskill find|search [query] [--json] [--limit N]
upskill remove|rm <skill-name>... [--delete-overlays]
upskill update [skill-name...] [--agent AGENT] [--no-render] [--check] [--diff]
upskill sync [--agent AGENT]
upskill migrate [--from unmanaged|SOURCE] [--agent AGENT] [--skill NAME] [--to-source NAME] [--to-source-path DIR] [--yes]
upskill init [name] [--dir DIR]
upskill workon <skill-name> [--from SKILL] [--template NAME] [--no-edit]
upskill status
upskill doctor
upskill mcp
upskill stats use <skill> [--agent AGENT] [--idempotency-key KEY] [--json]
upskill stats feedback <skill> --sentiment positive|negative --situation TEXT --details TEXT --proposed-change TEXT [--agent AGENT] [--idempotency-key KEY] [--json]
```

Source shortcuts:

```bash
upskill source list|ls [--json]
upskill source add <source> [--name NAME] [--ref REF] [--subpath PATH] [--shared|--personal|--external] [--write-policy POLICY] [--default-update-policy manual|auto]
upskill source show <source-or-name> [--json]
upskill source edit <source-name> <skill-name> --file PATH (--content TEXT | --base64-content DATA) [--yes] [--reason TEXT]
upskill source skills <source-or-name> [--json] [--ref REF] [--subpath PATH]
upskill source find|search <source-or-name> [query] [--json] [--limit N] [--ref REF] [--subpath PATH]
upskill source remove|rm <source-or-name> [--yes] [--keep-installed]
```

Global option:

```bash
upskill <command> --no-check
```

`--no-check` skips implicit update notices and automatic updates for one
command. Set `UPSKILL_NO_CHECK=1` for the same behavior in the environment.
Explicit update, sync, and source catalog commands still run their requested
live operation.

Local customizations:

```bash
upskill overlay list|ls [--skill NAME] [--agent AGENT] [--json]
upskill overlay add <skill-name> [--agent AGENT] [--path] [--no-edit]
upskill overlay edit <skill-name> [--agent AGENT] [--file PATH] [--operation OPERATION] [--no-edit]
upskill overlay diff <skill-name> [--agent AGENT]
```

Generated output:

```bash
upskill render [skill-name] [--agent AGENT] [--copy=false]
upskill render <skill-name> --print [--file SKILL.md] [--base-only]
```

Drafts:

```bash
upskill draft list|ls [--json]
upskill draft create <skill-name> [--from SKILL] [--from-rendered] [--template NAME] [--no-edit]
upskill draft edit <skill-name> [--file PATH] [--no-edit]
upskill draft path <skill-name>
upskill draft files <skill-name> [--json]
upskill draft read <skill-name> [--file PATH]
upskill draft add-file <skill-name> <local-path> --path PATH [--no-edit]
upskill draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect] [--no-edit]
upskill draft render <skill-name> [--agent AGENT] [--copy=true|false] [--apply-overlays]
upskill draft diff <skill-name>
upskill draft rebase <skill-name>
upskill draft promote <skill-name> (--to PATH | --to-source NAME) [--yes]
upskill draft remove|rm <skill-name>
```

Interactive draft commands use `$VISUAL` or `$EDITOR`: `workon`, `draft
create`, `draft edit`, `draft add-file`, and non-inspection `draft import`
create the needed file and open it when running in a terminal. Use `--no-edit`
for automation.

## Sources

Supported source inputs include local paths, Git URLs, GitHub shorthand, refs,
and subpaths:

```text
./skills
owner/repo
owner/repo/path/to/skills
owner/repo@ref
owner/repo#ref
https://github.com/owner/repo/tree/ref/path/to/skills
git@github.com:owner/repo.git
```

`upskill add` discovers directories containing `SKILL.md`, copies full skill
directories into the base store, writes `upskill-lock.json`, and renders by
default. `upskill source skills` and `upskill source find` inspect the current
source every time. Local sources are read directly from disk; Git sources are
resolved from the requested upstream ref instead of from the installed store.

Installed skills are separate from source catalogs. `upskill update --check`
previews locked source changes without writing. `upskill update --diff`
previews incoming file changes. `upskill update` applies updates, and
`upskill sync` updates all locked skills and renders final agent outputs.
Upskill does not install newly discovered upstream skills automatically; run
`upskill add <source> --skill <name>` once for each new skill you want managed.

When a `SKILL.md` has no frontmatter block, Upskill infers default `name` and
`description` values from the directory name and first prose paragraph for
search/list output and rendered agent copies. The original source file is left
unchanged.

The primary local example source is `./examples`:

```bash
upskill add ./examples --skill hello-skill --agent codex
```

A successful mutation groups its primary result and labels the managed render
path separately from the configured agent-output copy:

```text
Result
  OK installed hello-skill
    digest: sha256:<full-digest>
  OK rendered hello-skill for codex
    rendered: /Users/example/.skills/rendered/codex/hello-skill
    copied: /Users/example/.codex/skills/hello-skill
```

Upskill still executes preflight before the requested command. It defers only
non-fatal preflight messages so a successful result appears first in a combined
terminal view. Primary results remain on stdout. Passive `Preflight` and
`Agent output audit` sections remain on stderr. Payload commands such as
`show`, `render --print`, and JSON modes do not gain a `Result` heading.

## Layout

By default, `upskill` uses:

```text
~/.skills/
  store/            # synced base skills; do not edit directly
  overlays/         # global and agent-specific overlay definitions
  drafts/           # mutable draft skill bundles
  imports/          # shared MCP-safe import inbox
  rendered/         # generated output, safe to delete
  cache/             # advisory update-check cache, safe to delete
  config.json       # sources and output directories
  upskill-lock.json # installed skill source metadata
  upskill-stats.jsonl # local retrieval, use, and feedback activity
```

Environment overrides:

```text
UPSKILL_HOME
UPSKILL_STORE_DIR
UPSKILL_OVERLAYS_DIR
UPSKILL_DRAFTS_DIR
UPSKILL_IMPORTS_DIR
UPSKILL_RENDERED_DIR
UPSKILL_CONFIG
UPSKILL_LOCK
UPSKILL_STATS
UPSKILL_SOURCE_IGNORE
UPSKILL_NO_CHECK
UPSKILL_PREFLIGHT_CHECK_TTL
```

`UPSKILL_PREFLIGHT_CHECK_TTL` accepts Go duration syntax, defaults to `1h`, and
controls how long implicit automatic Git checks reuse a successful result. Set
it to `0` to probe automatic Git remotes on every preflight.

## Skill Activity

Upskill records one retrieval after a command or MCP tool successfully returns
installed skill content. A retrieval is not a use: record a use only after an
agent materially applies the skill to a task.

```bash
upskill show hello-skill --stats
upskill show hello-skill --stats --nocontent
upskill stats use hello-skill --agent codex --idempotency-key task-123:hello:use
```

The first `show` command returns content, records its retrieval, and prints the
updated counts. The `--nocontent` form prints only statistics and does not
record a retrieval.

Agents can leave structured feedback from real use:

```bash
upskill stats feedback hello-skill \
  --sentiment negative \
  --situation "Reviewing unresolved comments on a Go pull request" \
  --details "The skill did not direct the agent to inspect inline review threads" \
  --proposed-change "Require inline review-thread inspection before completion"

upskill stats feedback hello-skill \
  --sentiment positive \
  --situation "Promoting a local skill revision into a team source" \
  --details "The dry-run made the write target clear and prevented an unintended change" \
  --proposed-change "Keep the dry-run requirement and show the same target summary in CLI examples"
```

Feedback stays in the local `upskill-stats.jsonl` activity log. It never edits
a skill, source, overlay, draft, rendered output, or VCS state. Do not include
secrets, credentials, private customer data, or unnecessary personal data.

## Doctor And Status

Use `upskill doctor` after installation or when changing `UPSKILL_HOME`. It
creates the default local directories when they do not exist:

```text
store/
overlays/
drafts/
imports/
rendered/
```

Most commands create the managed files and directories they need automatically.
Use `upskill status` to inspect the current home, store path, installed skill
count, overlay count, and draft count. `doctor` and `status` show a detailed
`Agent output audit` when configured output directories contain unmanaged
skills, managed skills without render markers, or directories that cannot be
inspected. The audit is read-only: `add`, `status`, and `doctor` leave those
skills unchanged. Run `upskill migrate` to preview copying unmanaged skills
into a local source; run `upskill migrate --yes` to apply the migration,
install the copied skills, and render managed output. By default, `migrate`
reads from unmanaged agent output directories and writes to a `local-skills`
source at `~/Documents/Upskill/local-skills`. The command is a dry run unless
`--yes` is passed. Duplicate skill names are preserved with numeric suffixes
such as `redis`, `redis-2`, and `redis-3`. `migrate` can also copy from one
configured local source to another with `--from SOURCE --to-source NAME`.

## Local Sources

A source is where skills come from. It can be a GitHub repo, a Git URL, a GitHub
subdirectory, or a local directory such as an Obsidian skills folder. `upskill`
does not require local skills to be moved into a special layout.

The important distinction is:

- A source is the upstream skill collection.
- `store/` is the installed copy that `upskill` renders from and agents read.
- `overlays/`, `drafts/`, and `imports/` are managed `upskill` working state.

Use `upskill list` to see which source owns each installed skill. If a source
record was removed while an old installed copy remains, `list` marks that source
as unconfigured.

Add an existing local directory as a named source:

```bash
upskill source add ~/Documents/Notes/Memory/Skills --name Obsidian --personal
upskill source skills Obsidian
upskill source find Obsidian redis
upskill add Obsidian --skill redis
```

Source records can include policy metadata that tells agents which write
workflow to prefer. Use `--personal` for local sources where direct source edits
are acceptable, `--shared` for team sources where draft promotion is the normal
upstream path, and `--external` for read-only sources. `--write-policy` can be
`direct_allowed`, `overlay_preferred`, `draft_promote`, or `read_only`.
`upskill source list --json` and `upskill source show` include the effective
policy, including metadata read from an optional checked-in `upskill.toml`.

Removing a source also removes skills installed from that source, plus managed
rendered output and marked agent-output copies. When installed skills would be
removed, `upskill` prints the affected skill list and requires `--yes`. Source
files, overlays, and drafts are left alone. Use
`upskill source remove <source> --keep-installed` only when you want to detach
the source record but keep the installed copies.

You can also install directly from a raw path or Git source. Raw sources are
recorded automatically and are displayed by their canonical source string:

```bash
upskill add ~/Documents/Notes/Memory/Skills --skill redis
upskill add owner/repo --subpath skills --skill redis
```

`upskill find` and `upskill search` search installed skills in the local store.
`upskill source find` and `upskill source search` search one live source by
skill ID, display name, description, `SKILL.md` body, and path without
installing anything.

## Source Freshness And Updates

Upskill treats source catalogs and installed copies as different things:

- Source inspection commands always read the current source catalog and can show
  newly added upstream skills.
- Installed skill checks compare the locked copy in `store/` with the current
  source version of that same installed skill.
- New upstream skills do not produce generic notifications and are not installed
  until you run `upskill add`.

Most commands run a source-aware preflight check. Local skills are checked live.
Manual Git skills never contact their remote during ordinary commands; run an
explicit check to discover Git changes. A successful explicit check can seed a
cached notice for later ordinary commands. Automatic Git skills reuse a
successful check for one hour by default, then probe the remote revision and
clone only when that revision changed.

If a known installed skill change has policy `manual`, Upskill prints a notice
to stderr. JSON stdout stays machine-readable.

```text
Preflight
  NOTICE redis: update available
    next: upskill update --diff redis
```

Use manual updates when you want to review changes:

```bash
upskill update --check
upskill update redis --diff
upskill sync
```

`upskill update --check`, `upskill update --diff`, `upskill sync --dry-run`,
and source catalog commands are always live. They are not suppressed by
`--no-check`.

Use automatic updates when you want a skill refreshed whenever Upskill runs a
normal command:

```bash
upskill add Obsidian --skill redis --update-policy auto
upskill source add ~/Documents/Notes/Memory/Skills --name Obsidian --default-update-policy auto
```

To change an installed skill's policy later, reinstall it from the same source:

```bash
upskill add Obsidian --skill redis --update-policy manual
```

Automatic updates refresh installed base skill files, update the lock entry,
and render/copy configured agent outputs before the command's main behavior.
`upskill update --check`, `upskill update --diff`, and `upskill sync --dry-run`
inspect installed sources live without applying updates. `upskill source
skills` and `upskill source find` inspect their requested source catalog live.
These explicit commands skip implicit automatic-update preflight. Use
`--no-check` or `UPSKILL_NO_CHECK=1` to skip implicit preflight for other
commands. Configure the automatic Git freshness window in `config.json` or the
environment:

```json
{
  "preflight_check_ttl": "1h"
}
```

```bash
UPSKILL_PREFLIGHT_CHECK_TTL=30m upskill list
```

If an implicit automatic Git refresh fails, Upskill warns, reuses a stale
successful status when available, and continues the primary command. A failure
while actually applying an automatic update remains fatal.

## Agent Outputs

Agent output directories can be configured with `agents`:

```json
{
  "agents": {
    "claude-code": "~/.claude/skills",
    "codex": "~/.codex/skills"
  }
}
```

If no outputs are configured, `upskill` only auto-detects well-known
directories that already exist.

## Overlays

An overlay is a local customization layer for an installed skill. Use one when
you want to change how a skill behaves for yourself, a team, or a specific agent
without editing the synced copy in `store/`. The base skill can still update
from its source; your overlay is reapplied the next time generated output is
built.

An overlay can append to `SKILL.md`, add a support file, replace a file, apply a
JSON Patch to a JSON file, or provide a `.j2` template file. `upskill` applies
those changes when it builds the effective skill output.

For CLI users, overlays are editor-first. `upskill overlay add` creates the
default `SKILL.md` append file and opens `$VISUAL` or `$EDITOR` immediately.
`upskill overlay edit` revisits an existing overlay file or creates the
requested operation file before opening the editor. `--path` and `--no-edit` are
available for scripts. MCP-connected agents can create overlays with
`upsert_skill_overlay_file`.

Example: add Codex-only guidance to `hello-skill`:

```bash
upskill add ./examples --skill hello-skill --agent codex
upskill overlay add hello-skill --agent codex
upskill render hello-skill --agent codex
upskill show hello-skill --agent codex
```

The editor opens the Codex-specific append overlay for `SKILL.md`. Save the
guidance there, then render or sync to build generated output.

During render, `upskill` builds the effective skill in this order:

1. Base skill from `store/<skill-name>/`
2. Global overlay from `overlays/<skill-name>/`
3. Agent overlay from `overlays/<skill-name>/agents/<agent>/`

The overlay directory layout controls the operation:

```text
overlays/<skill>/SKILL.append.md                    # append to SKILL.md for every agent
overlays/<skill>/agents/codex/SKILL.append.md       # append only for Codex
overlays/<skill>/files/<path>                       # add a new file
overlays/<skill>/replace/<path>                     # replace or create a file
overlays/<skill>/patches/*.json                     # JSON Patch for a JSON file
overlays/<skill>/templates/<path>.j2                # Jinja template file
```

`files/<path>` adds a new file and fails if the file already exists.

`replace/<path>` overwrites or creates a file.

`append/<path>` appends text with a deterministic separator.

Root-level append shorthand is supported:

```text
SKILL.append.md  -> SKILL.md
AGENTS.append.md -> AGENTS.md
```

`patches/*.json` applies JSON Patch operations to `.json` files only:

```json
{
  "target": "metadata.json",
  "operations": [
    {"op": "add", "path": "/tags/-", "value": "codex"}
  ]
}
```

## Generated Output

Most commands build generated output automatically. `add`, `update`, and `sync`
render by default unless a command has a `--no-render` flag and you use it.

Use `upskill render` when you need to rebuild or inspect generated output
without updating base skills. This is useful after editing overlay files
directly, changing agent output config, recreating `rendered/`, or checking the
base skill with `--base-only`. Output is skipped when the base skill, overlays,
mode, and agent are unchanged.

Human render results always label the two path roles:

```text
Result
  OK rendered redis for codex
    rendered: /Users/example/.skills/rendered/codex/redis
    copied: /Users/example/.codex/skills/redis
```

`render --copy=false` reports `not copied`. `render --print` keeps stdout as
the requested file content without headings.

## Jinja Templates

Only `.j2` files are rendered through Jinja-style templates using
`github.com/nikolalohinski/gonja/v2`. Non-`.j2` files are copied literally.

Base template:

```jinja
---
name: Template Skill
description: Example skill with a Jinja-rendered entrypoint
---

# Template Skill

{% block guidance %}
Base guidance.
{% endblock %}
```

Overlay template:

```jinja
{% extends "parent/SKILL.md.j2" %}

{% block guidance %}
{{ super() }}

Codex-specific guidance.
{% endblock %}
```

Template output strips `.j2`:

```text
templates/SKILL.md.j2 -> SKILL.md
```

## MCP Server

<!-- upskill-callable-tools: describe_tool, promote_skill_draft, get_skill_stats, record_skill_use, record_skill_feedback -->

Run:

```bash
upskill mcp
```

The server publishes generated instructions from its live tool registry during
MCP initialization. Use `tools/list` for the complete registry and call
`describe_tool` with a tool name for its authoritative `inputSchema` and
accepted parameter names. Tool errors include the same schema and accepted
parameter list, so client-visible guidance cannot drift from the registered
surface.

The activity tools are `get_skill_stats`, `record_skill_use`, and
`record_skill_feedback`. Successful installed calls to `read_skill` and
`read_skill_file` record retrievals; discovery, file manifests, statistics,
source reads, and draft reads do not. `record_skill_use` is only for material
use, and structured feedback never increments use or changes a skill.

Use the same discover, describe, preview, apply, and read-back lifecycle for
mutations:

1. Discover `promote_skill_draft` with `tools/list`.
2. Call `describe_tool` with `name: "promote_skill_draft"` and use only names
   from `accepted_parameter_names`.
3. Call `promote_skill_draft` with `dry_run: true` and inspect `actions`,
   `warnings`, and `canonical_resources`.
4. Repeat the same arguments with `dry_run: false` only after the preview is
   approved.
5. Read the destination or source again to confirm the applied state.

For example, the tool-call arguments for steps 2 through 4 are:

```json
{"name":"describe_tool","arguments":{"name":"promote_skill_draft"}}
```

```json
{"name":"promote_skill_draft","arguments":{"draft_id":"draft:redis","to_source":"personal","dry_run":true}}
```

```json
{"name":"promote_skill_draft","arguments":{"draft_id":"draft:redis","to_source":"personal","dry_run":false}}
```

These argument examples are illustrative because they require a matching draft
and configured local source. The stdio integration suite runs the equivalent
lifecycle in a disposable environment.

Mutating tools that expose `dry_run` follow the same preview convention: call
with `dry_run: true`, inspect the planned targets and actions, then repeat with
`dry_run: false` to apply. Existing apply-by-default tools retain that default
for compatibility, while draft promotion and direct source edits remain
preview-by-default.

MCP writes are limited to overlays, mutable drafts, dry-run-first draft
promotion, and policy-checked source-file edits in configured local sources.
Agents can inspect configured sources, search live source skills, check
installed updates, and read base or generated skills, but they cannot install
new source skills, change update policy, perform VCS operations, or overwrite
`store/` through the MCP server. To change an installed skill's update policy,
reinstall it with `upskill add` plus `--skill <name> --update-policy
manual|auto`. Draft promotion and source-file edits reject managed upskill
directories such as `store/`, `rendered/`, `overlays/`, `drafts/`, and
`imports/`.

Copy `prompts/AGENTS.md` into an agent configuration when you want an agent to
use `upskill` MCP tools for skill management instead of built-in skill servers.
The prompt's `Using Skills` section tells agents that agent-local `SKILL.md`
files are generated output, not the source to edit when a skill needs to change.

### Agent Guidance Distribution

Keep `prompts/AGENTS.md` as the always-on bootstrap and safety contract. An
agent needs those instructions before it can discover Upskill's registry or
load an Upskill usage skill, so an installable skill should complement the
prompt, not replace it. The prompt should retain tool discovery, live-schema,
write-target, and generated-output rules; a skill can carry longer task recipes
and examples on demand.

The repository does not yet ship that self-usage skill. Before adding one,
generate both surfaces from one maintained source or contract-test their shared
requirements so tool names, preview behavior, and source semantics cannot
drift independently.

## Sync Semantics

`upskill sync` updates locked base skills, builds generated output, and copies
the effective final skill directories to configured outputs. If overlays exist,
they are always applied. There is no `--with-overlays` or `--without-overlays`
mode.

Text sync output uses `OK` for applied results and `PLAN` for `--dry-run`
targets. `sync --json` and MCP `sync_skills` preserve their legacy `warnings`
and add `agent_output_audit`, including counts, per-agent summaries, and the
detailed findings used to build those warnings. MCP preflight notices include
`phase: "preflight"`; audit findings stay in the sync result rather than the
notice list.

To inspect the base state:

```bash
upskill render my-skill --print --base-only
```

Every rendered skill contains `.upskill-render.json`, which records render mode,
agent, base path, overlay paths, overlay hash, and timestamp.

## Current Limits

- Installed-skill search is local to the store. Source search inspects one
  requested source live. Broad remote registry search is not implemented.
- Draft promotion writes to a local checkout or named local source. It does not
  create branches, commit, push, or open pull requests.
- `sync` updates locked skills and renders outputs. It does not discover a new
  skill that has never been installed; run `upskill add <source> --skill <name>`
  once for new skills.
- Template customization files are full `.j2` files. They are not JSON or YAML
  variable maps.
