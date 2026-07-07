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
upskill show <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH]
upskill list|ls [--json] [--agent AGENT]
upskill find|search [query] [--json] [--limit N]
upskill remove|rm <skill-name>... [--delete-overlays]
upskill update [skill-name...] [--agent AGENT] [--no-render] [--check] [--diff]
upskill sync [--agent AGENT]
upskill update-policy <skill-name>... (--manual | --auto)
upskill migrate [--from unmanaged|SOURCE] [--agent AGENT] [--skill NAME] [--to-source NAME] [--to-source-path DIR] [--yes]
upskill init [name] [--dir DIR]
upskill workon <skill-name> [--from SKILL] [--template NAME] [--no-edit]
upskill status
upskill doctor
upskill mcp
```

Source shortcuts:

```bash
upskill source list|ls [--json]
upskill source add <source> [--name NAME] [--ref REF] [--subpath PATH] [--default-update-policy manual|auto]
upskill source skills <source-or-name> [--json] [--ref REF] [--subpath PATH]
upskill source find|search <source-or-name> [query] [--json] [--limit N] [--ref REF] [--subpath PATH]
upskill source remove|rm <source-or-name> [--yes] [--keep-installed]
```

Global option:

```bash
upskill <command> --no-check
```

`--no-check` skips source/update preflight for one command. Set
`UPSKILL_NO_CHECK=1` for the same behavior in the environment.

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
upskill draft promote <skill-name> (--to PATH | --to-source NAME) [--dry-run]
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

## Layout

By default, `upskill` uses:

```text
~/.skills/
  store/            # synced base skills; do not edit directly
  overlays/         # global and agent-specific overlay definitions
  drafts/           # mutable draft skill bundles
  imports/          # shared MCP-safe import inbox
  rendered/         # generated output, safe to delete
  config.json       # sources and output directories
  upskill-lock.json # installed skill source metadata
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
UPSKILL_SOURCE_IGNORE
UPSKILL_NO_CHECK
```

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
count, overlay count, and draft count. `doctor` and `status` warn when
configured agent output directories contain skill folders that are not installed
in Upskill from configured sources. Use `upskill migrate` to copy those
unmanaged agent-output skills into a local source, install them from that
source, and render managed output. By default, `migrate` reads from unmanaged
agent output directories and writes to a `local-skills` source at
`~/Documents/Upskill/local-skills`. The command is a dry run unless `--yes` is
passed. Duplicate skill names are preserved with numeric suffixes such as
`redis`, `redis-2`, and `redis-3`. `migrate` can also copy from one configured
local source to another with `--from SOURCE --to-source NAME`.

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
upskill source add ~/Documents/Notes/Memory/Skills --name Obsidian
upskill source skills Obsidian
upskill source find Obsidian redis
upskill add Obsidian --skill redis
```

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

Most commands run a preflight check. If an installed skill has changed upstream
and its policy is `manual`, Upskill prints a notice to stderr. JSON stdout stays
machine-readable.

```text
notice: updates available for redis; run upskill sync or upskill update --diff redis
```

Use manual updates when you want to review changes:

```bash
upskill update --check
upskill update redis --diff
upskill sync
```

Use automatic updates when you want a skill refreshed whenever Upskill runs a
normal command:

```bash
upskill add Obsidian --skill redis --update-policy auto
upskill source add ~/Documents/Notes/Memory/Skills --name Obsidian --default-update-policy auto
upskill update-policy redis --auto
upskill update-policy redis --manual
```

Automatic updates refresh installed base skill files, update the lock entry,
and render/copy configured agent outputs before the command's main behavior.
Read-only commands such as `upskill update --check`, `upskill update --diff`,
`upskill source skills`, and `upskill source find` report pending automatic
updates without applying them. Use `--no-check` or `UPSKILL_NO_CHECK=1` to skip
preflight entirely for one invocation.

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

Run:

```bash
upskill mcp
```

Tools:

```text
find_skills
list_sources
find_source_skills
check_skill_updates
set_skill_update_policy
read_skill
list_skill_files
read_skill_file
render_skill
sync_skills
upsert_skill_overlay_file
delete_skill_overlay_file
list_skill_overlays
create_skill_draft
list_skill_drafts
read_skill_draft
list_skill_draft_files
read_skill_draft_file
upsert_skill_draft_file
delete_skill_draft_file
delete_skill_draft
import_skill_draft_archive
import_skill_draft_file
render_skill_draft
diff_skill_draft
rebase_skill_draft
promote_skill_draft
```

MCP writes are limited to lock update-policy metadata, overlays, and mutable
drafts. Agents can inspect configured sources, search live source skills, check
installed updates, and read base or generated skills, but they cannot install
new source skills or overwrite `store/` through the MCP server. Draft promotion
can write only to a local checkout or named local source and rejects managed
upskill directories such as `store/`, `rendered/`, `overlays/`, `drafts/`, and
`imports/`.

Copy `prompts/AGENTS.md` into an agent configuration when you want an agent to
use `upskill` MCP tools for skill management instead of built-in skill servers.
The prompt's `Using Skills` section tells agents that agent-local `SKILL.md`
files are generated output, not the source to edit when a skill needs to change.

## Sync Semantics

`upskill sync` updates locked base skills, builds generated output, and copies
the effective final skill directories to configured outputs. If overlays exist,
they are always applied. There is no `--with-overlays` or `--without-overlays`
mode.

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
