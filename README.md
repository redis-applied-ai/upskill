# upskill

`upskill` is a standalone Go CLI and stdio MCP server for agent skills. It can
install skills from local or Git sources, update the synced base store, render
global and agent-specific overlays, and expose skills to agents through MCP
without allowing agents to overwrite base skills.

## Quick Start

```bash
make
mkdir -p ~/.local/bin
install -m 0755 bin/upskill ~/.local/bin/upskill
upskill doctor
upskill add ./examples --skill hello-skill --agent codex
upskill render hello-skill --print --agent codex
```

The binary is written to `bin/upskill`. Installing it to `~/.local/bin` is
optional; you can also run `bin/upskill` directly.

Start with [docs/getting-started.md](docs/getting-started.md) for installation,
first-run config, the local example skill, and MCP setup. Use
[docs/workflows.md](docs/workflows.md) for task-oriented examples covering
overlays through MCP, drafts, promotion to PRs, updates, and Jinja template
overlays.

For repository validation:

```bash
make test
make coverage
```

## Commands

```bash
upskill add <source> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--no-render]
upskill use <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH]
upskill list|ls [--json] [--agent AGENT]
upskill find [query] [--json] [--limit N]
upskill remove|rm <skill-name>... [--delete-overlays]
upskill update [skill-name...] [--agent AGENT] [--no-render] [--check] [--diff]
upskill sync [--agent AGENT]
upskill init [name] [--dir DIR]
upskill workon <skill-name> [--from SKILL] [--template NAME]
upskill status
upskill doctor
upskill mcp
```

Source shortcuts:

```bash
upskill source list [--json]
upskill source add <source> [--name NAME] [--ref REF] [--subpath PATH]
upskill source remove <name>
```

Vault-backed local sources:

```bash
upskill vault init <path> [--name NAME] [--source-dir DIR] [--no-config]
upskill vault status [--name NAME] [--json]
```

Overlay rendering:

```bash
upskill overlay list [--skill NAME] [--agent AGENT] [--json]
upskill overlay add <skill-name> [--agent AGENT] [--path]
upskill overlay diff <skill-name> [--agent AGENT]
upskill render [skill-name] [--agent AGENT] [--copy=false]
upskill render <skill-name> --print [--file SKILL.md] [--base-only]
```

Drafts:

```bash
upskill draft list [--json]
upskill draft create <skill-name> [--from SKILL] [--from-rendered] [--template NAME]
upskill draft path <skill-name>
upskill draft files <skill-name> [--json]
upskill draft read <skill-name> [--file PATH]
upskill draft add-file <skill-name> <local-path> --path PATH
upskill draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect]
upskill draft render <skill-name> [--agent AGENT] [--copy=true|false] [--apply-overlays]
upskill draft diff <skill-name>
upskill draft rebase <skill-name>
upskill draft promote <skill-name> (--to PATH | --to-source NAME) [--dry-run]
upskill draft remove <skill-name>
```

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
default. `upskill update --check` previews locked source changes without
writing. `upskill update --diff` previews incoming file changes. `upskill
update` applies updates, and `upskill sync` updates all locked skills and
renders final agent outputs.

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

`doctor` does not create `config.json`; create that file when you want explicit
agent output directories or import allowlists. Use `upskill status` to inspect
the current home, store path, installed skill count, overlay count, and draft
count.

## Vaults

A vault is a local, editable skills workspace that `upskill` treats as a named
source. Use one when your skills already live as normal files in an Obsidian
vault, a synced folder, or a personal/team skills directory, and you want a
low-friction place to draft and revise skills before installing them into the
managed `store/`.

The important distinction is:

- `source/` is your editable upstream skill collection.
- `store/` is the installed copy that `upskill` renders from and agents read.
- `overlays/`, `drafts/`, and `imports/` are local working state kept alongside
  the source, but ignored when discovering base skills.

You do not need a vault if you only install from Git repositories. Use a vault
when you want local files to be the upstream source of truth.

To create one:

```bash
upskill vault init ~/SkillVault --name personal
upskill add personal
```

`upskill vault init` creates `source/`, `overlays/`, `drafts/`, and `imports/`,
then records the vault source in `config.json` under the name you choose.

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

An overlay is a local customization layered on top of an installed base skill.
Use overlays when you want to change how a skill behaves for yourself, a team,
or a specific agent without editing the synced copy in `store/`. The base skill
can still update from its source; your overlay is reapplied the next time the
skill is rendered.

You can create overlay files directly, or let an MCP-connected agent create
them with `upsert_skill_overlay_file`. The CLI creates overlay directories,
renders the final result, and shows diffs; it does not open an editor or invent
overlay text for you.

Example: add Codex-only guidance to `hello-skill`:

```bash
upskill add ./examples --skill hello-skill --agent codex
overlay_dir=$(upskill overlay add hello-skill --agent codex --path)
cat > "$overlay_dir/SKILL.append.md" <<'EOF'

For Codex, keep verification responses concise.
EOF
upskill render hello-skill --print --agent codex
```

That creates this overlay file:

```text
~/.skills/overlays/hello-skill/agents/codex/SKILL.append.md
```

During render, `upskill` builds the effective skill in this order:

1. Base skill from `store/<skill-name>/`
2. Global overlay from `overlays/<skill-name>/`
3. Agent overlay from `overlays/<skill-name>/agents/<agent>/`

Rendering is repeatable, not a one-time migration. `add`, `update`, and `sync`
render by default unless a command has a `--no-render` flag and you use it.
Run `upskill render <skill-name> --agent <agent>` manually while iterating on
overlay files. `sync` always renders the final base-plus-overlay state; use
`render --print --base-only` when you want to inspect the synced base by itself.

The overlay directory layout controls the operation:

```text
overlays/<skill>/SKILL.append.md                    # append to SKILL.md for every agent
overlays/<skill>/agents/codex/SKILL.append.md       # append only for Codex
overlays/<skill>/files/<path>                       # add a new file
overlays/<skill>/replace/<path>                     # replace or create a file
overlays/<skill>/patches/*.json                     # JSON Patch for a JSON file
overlays/<skill>/templates/<path>.j2                # Jinja template overlay
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

## Jinja Templates

Only `.j2` files are rendered through Jinja-style templates using
`github.com/nikolalohinski/gonja/v2`. Non-`.j2` files are copied literally.

Base template:

```jinja
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

MCP writes are limited to overlays and mutable drafts. Agents can read base or
rendered skills, but they cannot overwrite `store/` through the MCP server.
Draft promotion can write only to a local checkout or named local source and
rejects managed upskill directories such as `store/`, `rendered/`, `overlays/`,
`drafts/`, and `imports/`.

Copy `prompts/AGENTS.md` into an agent configuration when you want an agent to
use `upskill` MCP tools for skill management instead of built-in skill servers.

## Sync Semantics

`upskill sync` updates locked base skills, renders overlays, and copies the
effective final skill directories to configured outputs. If overlays exist,
they are always applied. There is no `--with-overlays` or `--without-overlays`
mode.

To inspect the base state:

```bash
upskill render my-skill --print --base-only
```

Every rendered skill contains `.upskill-render.json`, which records render mode,
agent, base path, overlay paths, overlay hash, and timestamp.

## Current Limits

- Search is local only; remote registry search is not implemented.
- Draft promotion writes to a local checkout or named local source. It does not
  create branches, commit, push, or open pull requests.
- `sync` updates locked skills and renders outputs. It does not discover a new
  skill that has never been installed; run `upskill add <source> --skill <name>`
  once for new skills.
- Template overlays are full `.j2` files. They are not JSON or YAML variable
  maps.
