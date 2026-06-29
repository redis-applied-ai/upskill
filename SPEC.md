# upskill Specification

This is a design and implementation reference, not the first-run guide. Start
with `README.md` and `docs/getting-started.md` when learning how to use the
project. The staged implementation notes near the end are historical planning
context unless a task explicitly says otherwise.

## Purpose

`upskill` is a standalone Go CLI and stdio MCP server for agent skill
management. It installs skills from local or Git sources, keeps the base skill
store synced, renders local and agent-specific overlays, and lets agents read
skills and write overlays without mutating synced base skills.

The command model is informed by the MIT-licensed `vercel-labs/skills` CLI:
small verbs, source strings that can point at local paths or Git repos, skill
discovery by `SKILL.md`, and agent-aware installation.

## Goals

- Provide a single `upskill` command with no user-visible dependency on another
  skills CLI.
- Install skills from local paths, Git URLs, GitHub shorthand, and configured
  sources.
- Support a filesystem or Obsidian-style vault as a local source of immutable
  base skills plus sibling local overlay, draft, and import directories.
- Track installed skill source metadata in a deterministic lock file.
- Update installed skills from their recorded sources.
- Render base skills plus global and agent-specific overlays into effective
  skill directories.
- Apply overlays by default whenever syncing, updating, or copying skills to
  agents.
- Support agent-specific overrides for well-known agents such as `claude`,
  `claude-code`, and `codex`.
- Support non-template overlays: append, replace, files, and JSON Patch.
- Support Jinja-style template inheritance only for files named `.j2` using the
  maintained `github.com/nikolalohinski/gonja/v2` implementation.
- Provide a stdio MCP server that agents can use as an alternate skill server.
- Let MCP clients search, read, render, and inspect skills and skill files.
- Let MCP clients upsert and delete overlay files only.
- Support mutable draft skill workspaces for low-friction skill creation and
  skill revision before promotion to an upstream repository.
- Distinguish new-skill drafts from version drafts of existing base skills.
- Let agents import draft skill bundles and resource files from a configured
  shared filesystem inbox or from verified URLs, without sending large files
  through MCP arguments.
- Ship a copyable `AGENTS.md` prompt that teaches agents to use the `upskill`
  MCP server for skill work instead of built-in skill servers.

## Non-Goals

- Provide remote registry search in the first standalone version.
- Apply JSON Patch to Markdown, YAML, or plain text.
- Treat every file as a template.
- Provide `--with-overlays` or `--without-overlays` sync modes.
- Expose arbitrary shell execution or unrestricted filesystem reads from
  templates or MCP tools.
- Allow MCP clients to overwrite, delete, or directly edit synced base skills.
- Mount arbitrary Claude Desktop chat/code-execution sandbox directories into
  the user's local filesystem. Draft import assumes an explicitly shared path,
  URL, or MCP file content.
- Publish directly to remote Git hosts from MCP tools. Promotion writes to a
  local upstream checkout; branch creation, commit, push, and PR creation are
  separate user or agent workflows.
- Convert every draft edit into overlay syntax while the user is iterating.
  Drafts are full mutable skill bundles; overlays remain stable local
  customizations.

## Command Surface

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

upskill source list [--json]
upskill source add <source> [--name NAME] [--ref REF] [--subpath PATH]
upskill source remove <name>

upskill vault init <path> [--name NAME] [--source-dir DIR] [--no-config]
upskill vault status [--name NAME] [--json]

upskill draft list [--json]
upskill draft create <skill-name> [--from SKILL] [--from-rendered] [--template NAME]
upskill draft path <skill-name>
upskill draft files <skill-name> [--json]
upskill draft read <skill-name> [--file PATH]
upskill draft add-file <skill-name> <local-path> --path PATH
upskill draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect]
upskill draft import <skill-name> --archive-url URL --sha256 SHA256 [--strip-components N] [--inspect]
upskill draft import <skill-name> --file PATH --path DEST [--sha256 SHA256]
upskill draft import <skill-name> --file-url URL --path DEST --sha256 SHA256
upskill draft render <skill-name> [--agent AGENT] [--copy=true|false] [--apply-overlays]
upskill draft diff <skill-name>
upskill draft rebase <skill-name>
upskill draft promote <skill-name> (--to PATH | --to-source NAME) [--dry-run]
upskill draft remove <skill-name>

upskill overlay list [--skill NAME] [--agent AGENT] [--json]
upskill overlay add <skill-name> [--agent AGENT] [--path]
upskill overlay diff <skill-name> [--agent AGENT]

upskill render [skill-name] [--agent AGENT] [--copy=true|false]
upskill render <skill-name> --print [--file SKILL.md] [--base-only] [--agent AGENT]

upskill mcp
upskill status
upskill doctor
```

Aliases:

```text
ls -> list
rm -> remove
claude -> claude-code, when resolving built-in agent defaults
```

## Source Strings

Supported source inputs:

```text
/absolute/path
./relative/path
../relative/path
owner/repo
owner/repo/path/to/skills
owner/repo@ref
owner/repo#ref
owner/repo#ref@skill-name
https://github.com/owner/repo.git
https://github.com/owner/repo/tree/ref/path/to/skills
git@github.com:owner/repo.git
ssh://git@example.com/owner/repo.git
```

Parsing rules:

- Local paths are resolved to absolute paths.
- `owner/repo` shorthand expands to `https://github.com/owner/repo.git`.
- `@ref` or `#ref` selects a Git branch, tag, or commit.
- `#ref@skill-name` selects both a Git ref and a skill filter.
- `--ref`, `--subpath`, and `--skill` override values inferred from the source
  string.
- Subpaths must be relative and must not contain `..`.

## Skill Discovery

An install source is scanned for skill directories containing `SKILL.md`.

Discovery order:

1. The source root itself.
2. `skills/`.
3. `skills/.curated/`.
4. `skills/.experimental/`.
5. `skills/.system/`.
6. Well-known agent project skill directories such as `.agents/skills`,
   `.claude/skills`, `.codex/skills`, and `.github/skills`.
7. A bounded recursive scan, ignoring `.git`, `node_modules`, `dist`, `build`,
   and `__pycache__`.

Rules:

- `SKILL.md` frontmatter `name` is preferred as the skill name.
- If frontmatter has no name, the directory name is used.
- `description` is optional but included in search/list output when present.
- Installed skill names are normalized to lowercase kebab-case for store paths.
- The full skill directory, including support files, is copied into the store.

## Directory Layout

Default home:

```text
~/.skills/
```

Default layout:

```text
~/.skills/
  store/                 # synced base skills; never edited by overlays or MCP writes
  overlays/              # user and agent overlay definitions
  drafts/                # mutable draft skill bundles
  imports/               # optional shared inbox for MCP-safe local imports
  rendered/              # generated effective skills; safe to delete
  sources/               # persistent clones/cache, if used
  config.json            # sources, output directories, optional settings
  upskill-lock.json      # installed skill source metadata
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

## Configuration

Agent output directories are configured as `agents`:

```json
{
  "agents": {
    "claude-code": "~/.claude/skills",
    "codex": "~/.codex/skills"
  }
}
```

Draft and import directories may be configured explicitly:

```json
{
  "drafts_dir": "~/.skills/drafts",
  "imports_dir": "~/.skills/imports",
  "import_dirs": [
    "~/.skills/imports",
    "~/Desktop/upskill-imports"
  ],
  "source_ignore": [
    ".obsidian",
    "overlays",
    "drafts",
    "imports",
    "rendered"
  ]
}
```

Rules:

- `drafts_dir` stores mutable draft bundles.
- `imports_dir` is the default shared inbox where agents may place archives or
  resource files for local import.
- `import_dirs` is the allowlist used by MCP import tools when they read local
  paths. If omitted, only `imports_dir` is allowed.
- CLI imports may read any user-provided local path because the local user
  invoked the command directly.
- MCP imports from local paths must resolve inside one of the configured import
  directories. This prevents an agent from using import tools to read arbitrary
  local files.
- `source_ignore` extends the built-in source discovery ignore list. Built-ins
  include `.git`, `node_modules`, `dist`, `build`, `__pycache__`, `.obsidian`,
  `.trash`, `.upskill`, `overlays`, `drafts`, `imports`, and `rendered`.
  `UPSKILL_SOURCE_IGNORE` appends comma-separated names.

### Vault-backed local sources

`upskill vault init <path>` creates this layout and, unless `--no-config` is
used, writes it into `config.json`:

```text
<path>/
  source/    # local upstream skill bundles; copied into store by add/update
  overlays/  # local overlay definitions
  drafts/    # mutable draft bundles
  imports/   # shared MCP-safe import inbox
```

The vault `source/` directory is treated like any other local source. `store/`
remains the immutable installed copy used by render and MCP reads. Draft and
overlay writes stay local to the configured `drafts/` and `overlays/`
directories.

For Obsidian use, keep skill bundles under the vault `source/` folder and let
notes, attachments, overlays, drafts, and import inboxes remain outside source
or under ignored names. This avoids discovering local working files as skills.

If no output directories are configured, `upskill` may auto-detect well-known
directories that already exist:

```text
claude-code -> ${CLAUDE_CONFIG_DIR:-~/.claude}/skills
claude      -> ${CLAUDE_CONFIG_DIR:-~/.claude}/skills
codex       -> ${CODEX_HOME:-~/.codex}/skills
```

Auto-detection must not create directories. Explicitly configured output
directories may be created during rendering.

## Lock File

`upskill-lock.json` is deterministic JSON:

```json
{
  "schema_version": 1,
  "skills": {
    "redis": {
      "name": "Redis",
      "source": "vercel-labs/agent-skills",
      "source_type": "git",
      "source_url": "https://github.com/vercel-labs/agent-skills.git",
      "ref": "main",
      "subpath": "skills/redis",
      "skill_path": "skills/redis/SKILL.md",
      "hash": "sha256:...",
      "updated_at": "2026-06-23T00:00:00Z"
    }
  }
}
```

Rules:

- Keys are sorted on write.
- Local sources store absolute `source_url` paths.
- Git sources store canonical clone URLs.
- `hash` is computed from all files in the installed skill directory.
- `sync` and `update` use this file to refresh base skills.

## Command Behavior

### `add`

`upskill add <source>` discovers skills from a local or Git source, copies the
selected skill directories into `store/`, updates the lock file, and renders by
default.

Rules:

- Multiple discovered skills are installed unless `--skill` filters the result.
- Existing store skills are replaced atomically.
- Existing overlays are preserved.
- `--no-render` installs only into the base store.

### `use`

`upskill use <source-or-skill>` prints a selected skill body for immediate agent
use. If the argument names an installed skill, it prints the rendered
`SKILL.md`. If the argument is a source string, it resolves the source in a
temporary directory and prints the selected `SKILL.md` without installing.

### `list` / `ls`

Lists installed base skills with source and overlay status. With `--agent`, the
overlay status reflects that agent's effective overlay chain.

### `find`

Searches installed skills by normalized name, display name, description, and
`SKILL.md` body. It is local search in this version.

### `remove` / `rm`

Removes base skills from `store/` and the lock file. It does not delete overlays
unless `--delete-overlays` is provided.

### `update`

`upskill update [skill-name...]` refreshes the named skills, or all locked
skills when no names are provided, from recorded sources. It renders by default
unless `--no-render` is used.

`--check` materializes the recorded sources, compares their hashes against the
local lock file, and prints changed or unchanged rows without writing to
`store/`, `rendered/`, the lock file, or agent outputs.

`--diff` implies `--check` and also prints simple file-oriented diffs for
changed skills. It is read-only.

### `sync`

`upskill sync` is equivalent to:

1. `upskill update --no-render`
2. `upskill render --copy=true`

If overlays exist, they are always applied. There is no `--with-overlays` flag
because overlays are the default. There is no `--without-overlays` flag because
sync publishes the effective final state. Use `render --print --base-only` to
inspect the base state.

### `init`

Creates a new local skill skeleton with `SKILL.md` and frontmatter.

### `source`

`source` commands manage named source shortcuts in `config.json`. Named sources
are optional conveniences for repeated `add` calls.

### `vault`

`vault init` creates a filesystem-backed skill vault and configures it as a
named local source. It also points overlays, drafts, imports, and local import
allowlist entries at sibling vault directories.

`vault status` reports the named local source path, configured overlay/draft/
import paths, installed skill count, source skill count, overlay count, and
draft count.

### `workon`

`upskill workon <skill-name>` is the recommended low-friction entry point for
users and agents that want to create or revise a skill.

Behavior:

- If a draft already exists for `<skill-name>`, print the draft path and
  metadata.
- If an installed base skill named `<skill-name>` exists, create a version
  draft from the pristine base skill.
- If no installed base skill exists, create a new-skill draft skeleton.
- `--from SKILL` creates a version draft from a different installed base skill.
- `--template NAME` selects a built-in draft skeleton for new-skill drafts.
- The command never mutates `store/`, `overlays/`, or an upstream repository.

### `draft`

Draft commands manage mutable local skill bundles under `drafts/`.

Rules:

- Drafts are full skill bundles with `SKILL.md` and any resource files.
- Drafts are intended for rapid iteration. Users and agents edit normal files,
  not overlay patch syntax.
- Drafts do not replace synced base skills automatically.
- Drafts are not copied to agent output directories by `sync`.
- Draft rendering is explicit through `upskill draft render` or MCP draft tools.
- Draft promotion writes the draft into a local upstream checkout and produces
  a repo-ready change. It does not push or open a PR.

Draft types:

- `new`: a brand-new skill with no upstream parent.
- `version`: a new version of an existing installed base skill.

Draft metadata is stored at `drafts/<skill-name>/.upskill-draft.json`:

```json
{
  "schema_version": 1,
  "name": "redis",
  "type": "version",
  "parent_skill": "redis",
  "parent_source": "vercel-labs/skills",
  "parent_revision": "abc123",
  "parent_hash": "sha256:...",
  "created_from": "base",
  "created_at": "2026-06-25T00:00:00Z",
  "updated_at": "2026-06-25T00:00:00Z"
}
```

For version drafts:

- The default source is the pristine base bundle from `store/<skill-name>/`.
- Existing overlays are not applied by default because a version draft is meant
  to represent an upstream change.
- `--from-rendered` is allowed for the explicit case where the user wants to
  turn the current effective local skill into a draft. The draft metadata must
  record `created_from: "rendered"` and promotion must warn that local overlays
  may be included.
- `upskill draft diff` compares the draft against the captured parent snapshot.
- `upskill draft rebase` refreshes the captured parent snapshot from the current
  installed base and reports conflicts rather than silently overwriting draft
  edits.

For new-skill drafts:

- `upskill draft create <skill-name>` writes a minimal `SKILL.md` skeleton.
- `upskill draft import` may replace or merge the skeleton with an imported
  bundle containing `SKILL.md`.
- Promotion requires a destination checkout path because there is no recorded
  parent source.

Draft identifiers:

- Drafts are exposed to MCP clients as `draft:<skill-name>`.
- Base skills keep their normalized names, such as `redis`.
- `find_skills` returns both base and draft entries, with a `kind` field of
  `base` or `draft`.
- MCP clients should pass the exact returned `skill_id` to subsequent read and
  render calls. They should not assume a draft shadows a base skill with the
  same display name.

Draft rendering:

- `upskill draft render` renders the draft bundle as-is by default.
- `--apply-overlays` applies global and agent overlays after the draft files.
- Draft render markers use `mode: "draft"` or `mode: "draft-with-overlays"`.
- Draft render output is cached under `rendered/drafts/<skill-name>/`.

Draft promotion:

- `upskill draft promote <skill-name> --to PATH` copies the draft bundle into a
  local upstream checkout.
- `upskill draft promote <skill-name> --to-source NAME` resolves a configured
  local source and promotes into that source. This is the preferred path for a
  vault-backed source.
- For version drafts, the destination path is inferred from parent metadata
  when possible; otherwise the command asks for or requires the skill subpath.
- For new-skill drafts, the destination path must be provided or inferred from
  the checkout's skill discovery layout.
- `--dry-run` prints the file operations and diff without writing.
- Promotion results include `base_mutated: false`, `overwrite`, and
  `next_steps`. Promoting into a source does not install the skill by itself:
  use `upskill add <source> --skill <skill>` for a new skill, or
  `upskill update <skill>` after the promoted source change is merged or ready.
- Promotion never deletes the local draft automatically.

## Draft Bundle and Resource Import

Agents may need to add complete skill bundles that contain resource files such
as references, scripts, examples, data, images, or templates. Large bundles
should not be sent through a single MCP argument.

Supported import sources:

```text
local archive path   # preferred when agent and upskill share a filesystem
local file path
archive URL
file URL
inline text/base64   # small files only, through upsert tools
```

Shared filesystem flow:

1. The agent creates a complete `.zip` or `.tar.gz` skill bundle.
2. The agent writes it into a path visible to `upskill`, preferably
   `imports/`.
3. The agent calls `upskill draft import <skill> --archive PATH` or the MCP
   `import_skill_draft_archive` tool with `archive_path`.
4. `upskill` opens the path from its own filesystem, validates the archive, and
   extracts it into `drafts/<skill>/`.
5. The agent verifies the result with draft file listing and file reads.

Archive import commands:

```bash
upskill draft import my-skill --archive ~/.skills/imports/my-skill.zip --sha256 ...
upskill draft import my-skill --archive-url https://example.com/my-skill.zip --sha256 ...
```

Single resource import commands:

```bash
upskill draft import my-skill --file ./api.md --path references/api.md
upskill draft import my-skill --file-url https://example.com/api.md --path references/api.md --sha256 ...
```

Archive layout detection:

- If the archive root contains `SKILL.md`, import from the archive root.
- If the archive contains exactly one top-level directory and that directory
  contains `SKILL.md`, import from that directory.
- `--strip-components N` may be used to remove leading path components.
- If multiple candidate skill roots exist, fail and ask for an explicit root or
  strip setting.
- An import may overwrite files inside the draft only after reporting the
  planned overwrite in normal mode or through `--inspect`.

Validation and safety:

- Supported archive types are `.zip` and `.tar.gz`.
- Reject absolute paths and `..` traversal.
- Reject symlinks by default.
- Reject device files, sockets, and other non-regular file entries.
- Enforce maximum archive byte size, maximum extracted byte size, maximum file
  count, and maximum single-file size.
- Remote URL imports require `sha256` in MCP mode and should warn without it in
  CLI mode.
- Local path imports through MCP must resolve under configured `import_dirs`.
- Imported files must stay inside the draft directory after path cleaning.
- Imported bundles must contain `SKILL.md` after root detection.

Import result shape:

```json
{
  "draft_id": "draft:my-skill",
  "imported": true,
  "source": "/Users/example/.skills/imports/my-skill.zip",
  "files": [
    {"path": "SKILL.md", "size": 612},
    {"path": "references/api.md", "size": 2048}
  ],
  "warnings": []
}
```

## Overlay Model

For a given skill and agent, overlays apply in this order:

1. Base skill from `store/<skill-name>/`
2. Global overlay from `overlays/<skill-name>/`
3. Agent overlay from `overlays/<skill-name>/agents/<agent>/`

Later layers can add to or override earlier layers.

Each overlay may contain:

```text
append/
files/
replace/
patches/
templates/
```

Root-level append shorthand is supported:

```text
SKILL.append.md  -> append to SKILL.md
AGENTS.append.md -> append to AGENTS.md
```

### `files/`

`files/<relative-path>` copies a new file into the rendered skill and fails if
that destination already exists.

### `replace/`

`replace/<relative-path>` overwrites or creates the rendered file.

### `append/`

`append/<relative-path>` appends text to a rendered file using a deterministic
separator.

### `patches/`

Patch files apply JSON Patch operations to JSON files only:

```json
{
  "target": "metadata.json",
  "operations": [
    {"op": "add", "path": "/tags/-", "value": "codex"}
  ]
}
```

Rules:

- `target` must end in `.json`.
- `target` must be relative and inside the rendered skill.
- Patch operations use standard JSON Pointer paths.

### `templates/`

Only files ending in `.j2` are rendered as Jinja templates:

```text
templates/SKILL.md.j2 -> SKILL.md
templates/references/example.md.j2 -> references/example.md
```

Non-`.j2` files are copied literally only when placed under `files/` or
`replace/`.

## Jinja Template Layering

Base skill:

```text
store/template-skill/SKILL.md.j2
```

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

Overlay guidance.
{% endblock %}
```

Template namespaces:

```text
parent/  # previous layer for the same template path
base/    # pristine base skill directory
overlay/ # current overlay template directory
```

Helpers:

```text
base_file(path)
current_file(path)
skill
agent
```

Helpers reject absolute paths and `..` traversal.

## Render Algorithm

For each skill and output agent:

1. Read the base skill from `store/<skill-name>/`.
2. Create a clean temporary render directory.
3. Copy base files into the temporary render directory, excluding `.j2` sources.
4. Render base `.j2` templates into non-`.j2` output files.
5. Apply the global overlay, if present.
6. Apply the agent overlay, if present.
7. Write `.upskill-render.json`.
8. Replace the final rendered cache directory atomically when possible.
9. If copy is enabled, replace only the output skill directory.

Within each overlay layer, operations apply in this order:

1. `files/`
2. `replace/`
3. `patches/`
4. `templates/`
5. `append/` and root-level append shorthand

## Render Marker

Every rendered skill contains `.upskill-render.json`:

```json
{
  "schema_version": 1,
  "skill": "redis",
  "agent": "codex",
  "mode": "with-overlays",
  "base_path": "/Users/example/.skills/store/redis",
  "overlays": [
    "/Users/example/.skills/overlays/redis",
    "/Users/example/.skills/overlays/redis/agents/codex"
  ],
  "overlay_hash": "sha256:...",
  "rendered_at": "2026-06-23T00:00:00Z"
}
```

Agent output directories receive the same marker, so users can inspect the
current final state.

Draft render markers use the same shape with draft fields:

```json
{
  "schema_version": 1,
  "skill": "redis",
  "skill_id": "draft:redis",
  "kind": "draft",
  "draft_type": "version",
  "agent": "codex",
  "mode": "draft",
  "draft_path": "/Users/example/.skills/drafts/redis",
  "parent_skill": "redis",
  "parent_hash": "sha256:...",
  "rendered_at": "2026-06-25T00:00:00Z"
}
```

## MCP Server

`upskill mcp` starts a stdio MCP server. It uses the same config, store,
overlays, rendered cache, and lock file as the CLI.

Protocol requirements:

- Respond to `initialize`.
- Respond to `tools/list`.
- Respond to `tools/call`.
- Return JSON-RPC errors for invalid arguments.
- Never expose tools that directly write `store/`.

Tools:

```text
find_skills(query?, limit?, agent?)
read_skill(skill_id, agent?, rendered=true, include_files=true, file_limit=100)
list_skill_files(skill_id, agent?, rendered=true, path_prefix?, recursive=true, include_hashes=false, limit=500)
read_skill_file(skill_id, path, agent?, rendered=true, max_bytes=200000)
render_skill(skill_id, agent?, copy=false, base_only=false)
sync_skills(agent?)
upsert_skill_overlay_file(skill_id, path, content?, base64_content?, agent?, operation="append")
delete_skill_overlay_file(skill_id, path, agent?, operation="append")
list_skill_overlays(skill_id?, agent?)
create_skill_draft(skill_name, from_skill?, from_rendered=false, template?, allow_existing=false, replace_existing=false)
list_skill_drafts(query?, limit?)
read_skill_draft(draft_id, include_files=true, file_limit=100)
list_skill_draft_files(draft_id, path_prefix?, recursive=true, include_hashes=false, limit=500)
read_skill_draft_file(draft_id, path, max_bytes=200000)
upsert_skill_draft_file(draft_id, path, content?, base64_content?)
delete_skill_draft_file(draft_id, path)
delete_skill_draft(draft_id? | skill_name?)
import_skill_draft_archive(draft_id, archive_path?, archive_url?, sha256?, strip_components?, inspect=false)
import_skill_draft_file(draft_id, source_path?, source_url?, path, sha256?)
render_skill_draft(draft_id, agent?, copy=false, apply_overlays=false)
diff_skill_draft(draft_id)
rebase_skill_draft(draft_id)
promote_skill_draft(draft_id, to_path? | to_source?, dry_run=true)
```

Read semantics:

- `rendered=true` reads the effective rendered skill and applies overlays first.
- `rendered=false` reads the synced base skill from `store/`.
- `read_skill` returns the `SKILL.md` body plus a file manifest.
- `skill_id` may be the normalized store name or frontmatter name.
- `skill_id` may be a draft identifier such as `draft:redis`; draft reads use
  draft semantics and do not apply overlays unless the tool explicitly requests
  draft rendering with overlays.

Write semantics:

- `upsert_skill_overlay_file` writes only under `overlays/`.
- It never writes under `store/` or `rendered/`.
- `agent` selects `overlays/<skill>/agents/<agent>/`; absent `agent` selects
  `overlays/<skill>/`.
- `operation` maps to overlay storage:
  - `append` -> `append/<path>`
  - `root_append` -> `<name>.append.<ext>` at overlay root
  - `files` -> `files/<path>`
  - `replace` -> `replace/<path>`
  - `template` -> `templates/<path>` and requires `.j2`
  - `patch` -> `patches/<path>` and requires `.json`
- Paths must be relative and must not contain `..`.
- Deleting overlay files follows the same operation mapping.
- `upsert_skill_draft_file` and `delete_skill_draft_file` write only under
  `drafts/<skill>/`.
- `delete_skill_draft` removes a mutable draft bundle only. It never removes
  base skills.
- Draft write paths must be relative, must not contain `..`, and must not write
  `.upskill-draft.json` unless a future metadata-specific tool exists.
- Draft import tools may read local paths only from configured `import_dirs`.
- Draft promotion tools default to `dry_run=true` for MCP clients. A client must
  explicitly pass `dry_run=false` to write into a local checkout or configured
  local source.
- MCP draft promotion rejects destinations inside managed `store/`,
  `rendered/`, `overlays/`, `drafts/`, `imports/`, or configured import
  directories.
- `sync_skills` returns a structured summary with `updated`,
  `updated_count`, `rendered`, `rendered_count`, and warnings when nothing was
  updated or rendered.
- Template overlay writes require Jinja template content in a `.j2` file. JSON
  variable-map overlays are rejected; variable-map template overlays are not in
  this version.

Safety:

- MCP clients can refresh base skills through `sync_skills`, but source updates
  happen from the lock file and configured sources, not from arbitrary MCP file
  writes.
- MCP clients can create and edit drafts, including brand-new draft skills, but
  cannot write synced base skills directly.
- MCP clients can import resource files into drafts from a shared import inbox
  or verified URLs. They cannot use import tools as arbitrary local file reads.
- MCP clients can promote drafts only into local checkout paths, never directly
  to a remote registry, Git host, or managed upskill directory.

## Historical Implementation Plan

The following stages captured the implementation plan used to build the current
CLI, draft, vault, and MCP surfaces. Treat them as historical context and use
the active agent plan for new documentation or implementation work.

### Stage 1: Config and Draft Store

- Add `DraftsDir`, `ImportsDir`, and `ImportDirs` to config loading.
- Add environment overrides for draft and import directories.
- Create a draft package that can create, read, list, remove, and validate
  draft metadata.
- Add path helpers for `draft:<skill-name>` identifiers.
- Add unit tests for metadata persistence, path validation, existing-draft
  handling, and config defaults.

### Stage 2: CLI Draft Workflow

- Implement `upskill workon`.
- Implement `upskill draft list/create/path/files/read/add-file/remove`.
- Implement new-skill draft skeleton creation.
- Implement version draft creation from pristine base skills.
- Keep draft commands independent from `sync`; drafts must not be published
  accidentally.
- Add CLI tests for new-skill drafts, version drafts, existing draft reuse, and
  error paths.

### Stage 3: Draft Import

- Implement local archive import for `.zip` and `.tar.gz`.
- Implement local single-file import.
- Implement archive inspection and import result manifests.
- Add URL import after local import is stable.
- Enforce archive limits, traversal checks, symlink rejection, and MCP import
  directory allowlisting.
- Add tests for valid archives, root detection, strip components, unsafe paths,
  duplicate/overwrite reporting, oversize rejection, and local import allowlist
  behavior.

### Stage 4: Draft Render, Diff, Rebase, and Promote

- Render draft bundles into `rendered/drafts/<skill-name>/`.
- Add optional overlay application for draft render.
- Implement `draft diff` against captured parent snapshots.
- Implement `draft rebase` with conflict reporting.
- Implement `draft promote --to PATH --dry-run` first, then real local checkout
  writes.
- Add tests for render markers, promotion dry runs, promotion writes, and
  rebase conflict cases.

### Stage 5: MCP Draft Tools

- Add draft tools to `tools/list` with complete schemas.
- Implement draft create/read/list/file write/import/render/diff/promote tools.
- Keep base skill writes impossible through MCP.
- Default MCP promotion to dry-run.
- Add protocol tests for schemas, successful draft workflows, write
  restrictions, and local import path allowlisting.

### Stage 6: Prompt and E2E QA

- Update `prompts/AGENTS.md` to tell agents to use `workon`/draft MCP tools for
  new skills and upstream revisions.
- Add E2E coverage for:
  - creating a new draft skill with resource files,
  - creating a version draft from an installed skill,
  - importing a bundle from the shared import inbox,
  - rendering a draft,
  - promoting a draft into a local skills repo checkout,
  - verifying MCP cannot mutate base skills.
- Re-run the previous real-repo overlay E2E and the Codex MCP multi-turn E2E.
- Keep `make`, `make test`, and `make coverage` passing with 100% coverage.

## Historical And Open Design Notes

- Whether `promote_skill_draft` should support branch creation directly or only
  write files into the current checkout. Default remains "write files only".
- Whether draft render should ever be included in ordinary `sync`. Default
  remains no; drafts require explicit render/promotion.
- Whether `--from-rendered` should be hidden from help initially to avoid
  accidental upstream contamination from local overlays.
- Whether MCP URL import should require a configured domain allowlist in
  addition to `sha256`.
- Future base-skill creation via MCP would require a separate explicit trust
  boundary and is out of scope.

## Copyable Agent Prompt

The repo ships `prompts/AGENTS.md`. Users can copy it into projects or agent
configuration. It must instruct agents to:

- Use the `upskill` MCP server for skill listing, searching, reading, and
  overlay edits.
- Treat base skills returned by `upskill` as read-only.
- Use draft tools for creating new skills or iterating on upstream revisions.
- Use local archive import from the configured import inbox for skill bundles
  with resource files.
- Use overlay tools for skill customization.
- Prefer agent-specific overlays when changing behavior for only one agent.
- Render or sync after overlay edits when they need final skill output.
- Avoid built-in skill servers for skill management when `upskill` tools are
  available.

## Safety Rules

- Never write into `store/` during rendering, MCP overlay writes, draft
  operations, or MCP draft writes.
- Treat `rendered/` as generated output.
- Treat `drafts/` as mutable user work, not synced upstream truth.
- Reject absolute paths in source subpaths, overlay operations, and draft file
  paths.
- Reject `..` path traversal.
- Do not delete overlays when removing a base skill unless explicitly requested.
- Do not delete drafts when removing a base skill unless an explicit draft
  removal command is used.
- Do not silently ignore render, clone, or update errors.
- Do not expose arbitrary template helper functions.
- Do not expose arbitrary shell execution through MCP.
- Do not let MCP local import tools read outside configured import directories.
- Do not let archive import write outside the draft directory after path
  cleaning.

## Required Tests

### CLI Commands

- `add` installs one or more local skills and writes lock entries.
- `add --skill` filters discovered skills.
- `list` and `find` show installed skills.
- `remove` deletes base skill and lock entry but preserves overlays by default.
- `update` refreshes a locked local skill and renders by default.
- `sync` updates locked skills and renders overlays.
- `init` creates a valid `SKILL.md`.
- `vault init` creates source, overlay, draft, and import directories and can
  configure a named local source.
- `vault status` reports source, installed skill, overlay, and draft state.
- `workon` opens an existing draft, creates a version draft for installed
  skills, and creates a new-skill draft for unknown names.
- `draft create/list/path/files/read/add-file/remove` cover the draft lifecycle.
- `draft import --archive` imports a local skill bundle with resource files.
- `draft render` renders a draft without publishing through `sync`.
- `draft diff` compares version drafts against captured parent snapshots.
- `draft promote --dry-run` reports local checkout writes without mutating.
- `draft promote --to-source` resolves a named local source and reports next
  steps.
- Help output presents `upskill` as a standalone command.

### Source Handling

- Local paths resolve to absolute paths.
- GitHub shorthand parses to a Git source.
- `@ref`, `#ref`, and `#ref@skill` parse correctly.
- Unsafe subpaths are rejected.
- Skill discovery finds root and nested `SKILL.md` files.
- Skill discovery ignores configured vault working directories.
- Skill hashes are deterministic.

### Overlay and Render

- Global overlays apply after base.
- Agent overlays apply after global overlays.
- `files/` refuses to overwrite existing files.
- `replace/` overwrites existing files.
- `append/` appends with deterministic separators.
- Root-level append shorthand maps correctly.
- JSON Patch applies only to `.json`.
- `.j2` templates render through gonja.
- Jinja `extends`, `block`, and `super()` work for base -> global -> agent.
- `.upskill-render.json` is written to rendered and copied outputs.
- Draft render writes draft marker fields and does not apply overlays unless
  requested.

### Draft Import

- Archive root detection works for root `SKILL.md` and one top-level directory.
- `--strip-components` imports the intended bundle root.
- Import fails when multiple candidate skill roots exist.
- Import rejects absolute paths, `..`, symlinks, device files, sockets, and
  oversized archives.
- Single-file import rejects unsafe destination paths.
- URL import verifies `sha256` in MCP mode.
- MCP local path import is allowed only under configured import directories.

### MCP

- `initialize`, `tools/list`, and `tools/call` work over stdio JSON-RPC.
- MCP read tools can read base and rendered skills.
- MCP file tools list and read support files.
- MCP overlay upsert writes only under `overlays/`.
- MCP overlay delete deletes only overlay files.
- MCP template overlay writes reject JSON variable maps.
- MCP draft upsert writes only under `drafts/`.
- MCP draft delete removes only draft bundles.
- MCP draft import cannot read outside configured import directories.
- MCP draft promotion defaults to dry-run.
- MCP draft promotion rejects managed upskill directories and supports
  configured local sources.
- MCP tools cannot overwrite base `SKILL.md` or files in `store/`.
- `sync_skills` updates from lock/config, renders final output, and returns a
  structured summary.

### Prompt

- `prompts/AGENTS.md` exists and tells agents to use `upskill` MCP tools for
  skill operations, drafts, imports, and overlays.
