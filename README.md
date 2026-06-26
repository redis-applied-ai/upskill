# upskill

`upskill` is a standalone Go CLI and stdio MCP server for agent skills. It can
install skills from local or Git sources, update the synced base store, render
global and agent-specific overlays, and expose skills to agents through MCP
without allowing agents to overwrite base skills.

## Build and Test

```bash
make test
make
```

The binary is written to `bin/upskill`.

See [docs/workflows.md](docs/workflows.md) for task-oriented examples covering
installation, overlays through MCP, drafts, promotion to PRs, updates, and
Jinja template overlays.

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
upskill draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect]
upskill draft render <skill-name> [--agent AGENT] [--copy=true|false] [--apply-overlays]
upskill draft diff <skill-name>
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

For a filesystem or Obsidian-style vault, use:

```bash
upskill vault init ~/SkillVault --name personal
upskill add personal
```

The vault keeps skill source bundles under `source/` and local overlays,
drafts, and imports in sibling directories.

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

Legacy `targets` configuration is still accepted for compatibility. If no
outputs are configured, `upskill` only auto-detects well-known directories that
already exist.

## Overlays

Rendering applies:

1. Base skill from `store/<skill-name>/`
2. Global overlay from `overlays/<skill-name>/`
3. Agent overlay from `overlays/<skill-name>/agents/<agent>/`

Overlay operations:

```text
append/
files/
replace/
patches/
templates/
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
# Redis Skill

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
