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
- Show the current skill catalog for a configured source when users inspect or
  search that source, even when an internal Git cache or clone is stale.
- Support local filesystem sources, including Obsidian directories, without
  requiring users to create an Upskill-managed source layout.
- Automatically create Upskill's managed config, store, overlay, draft, import,
  rendered, and cache directories when commands need them.
- Track installed skill source metadata in a deterministic lock file.
- Update installed skills from their recorded sources.
- Notify users when installed skills have upstream updates.
- Support per-skill automatic updates for users who want installed skills kept
  current whenever they use Upskill.
- Render base skills plus global and agent-specific overlays into effective
  skill directories.
- Apply overlays by default whenever syncing, updating, or copying skills to
  agents.
- Support agent-specific overrides for well-known agents such as `claude-code`
  and `codex`.
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
- Ship a copyable `AGENTS.md` prompt with a `Using Skills` section that teaches
  agents to use the `upskill` MCP server for skill work instead of built-in
  skill servers.

## Non-Goals

- Provide remote registry search in the first standalone version.
- Install every new skill that appears in a configured source automatically.
- Print generic notifications for every new upstream skill that is not already
  installed.
- Run a background daemon, scheduler, or watcher for source updates.
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
upskill add <source> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--update-policy manual|auto] [--no-render]
upskill show <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH]
upskill list|ls [--json] [--agent AGENT]
upskill find|search [query] [--json] [--limit N]
upskill remove|rm <skill-name>... [--delete-overlays]
upskill update [skill-name...] [--agent AGENT] [--no-render] [--check] [--diff]
upskill sync [--agent AGENT]
upskill migrate [--from unmanaged|SOURCE] [--agent AGENT] [--skill NAME] [--to-source NAME] [--to-source-path DIR] [--yes]
upskill init [name] [--dir DIR]
upskill workon <skill-name> [--from SKILL] [--template NAME] [--no-edit]

upskill source list|ls [--json]
upskill source add <source> [--name NAME] [--ref REF] [--subpath PATH] [--default-update-policy manual|auto]
upskill source skills <source-or-name> [--json] [--ref REF] [--subpath PATH]
upskill source find|search <source-or-name> [query] [--json] [--limit N] [--ref REF] [--subpath PATH]
upskill source remove|rm <source-or-name> [--yes] [--keep-installed]

upskill draft list|ls [--json]
upskill draft create <skill-name> [--from SKILL] [--from-rendered] [--template NAME] [--no-edit]
upskill draft edit <skill-name> [--file PATH] [--no-edit]
upskill draft path <skill-name>
upskill draft files <skill-name> [--json]
upskill draft read <skill-name> [--file PATH]
upskill draft add-file <skill-name> <local-path> --path PATH [--no-edit]
upskill draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect] [--no-edit]
upskill draft import <skill-name> --archive-url URL --sha256 SHA256 [--strip-components N] [--inspect] [--no-edit]
upskill draft import <skill-name> --file PATH --path DEST [--sha256 SHA256] [--no-edit]
upskill draft import <skill-name> --file-url URL --path DEST --sha256 SHA256 [--no-edit]
upskill draft render <skill-name> [--agent AGENT] [--copy=true|false] [--apply-overlays]
upskill draft diff <skill-name>
upskill draft rebase <skill-name>
upskill draft promote <skill-name> (--to PATH | --to-source NAME) [--dry-run]
upskill draft remove|rm <skill-name>

upskill overlay list|ls [--skill NAME] [--agent AGENT] [--json]
upskill overlay add <skill-name> [--agent AGENT] [--path] [--no-edit]
upskill overlay edit <skill-name> [--agent AGENT] [--file PATH] [--operation root-append|append|files|replace|template|patch] [--no-edit]
upskill overlay diff <skill-name> [--agent AGENT]

upskill render [skill-name] [--agent AGENT] [--copy=true|false]
upskill render <skill-name> --print [--file SKILL.md] [--base-only] [--agent AGENT]

upskill mcp
upskill status
upskill doctor
```

Global options:

```text
--no-check
```

`--no-check` disables source/update preflight for one command. It is equivalent
to setting `UPSKILL_NO_CHECK=1` for that invocation.

## CLI Help Contract

Top-level and parent command help stay concise and route users to leaf command
help. Parent `commands:` listings print canonical command names only; aliases
are shown in the leaf command `usage:` text. Every leaf command help output must
include:

- `usage:` with the complete command shape and aliases.
- `summary:` explaining what the command does.
- `arguments:` and `options:` when the command accepts them.
- `notes:` for side effects, defaults, identity rules, naming behavior, or
  safety constraints that users need before running the command.
- `examples:` with practical commands that can be copied and adapted.

Help text must explain non-obvious flags in user language. For example, `--ref`
means a Git branch, tag, or commit; `--subpath` means a relative directory
inside a source; and `--no-render` means updating Upskill's store and lock
without writing rendered agent output.

Aliases:

```text
ls -> list
rm -> remove
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
- If a `SKILL.md` has no frontmatter block, Upskill infers default `name` and
  `description` metadata from the directory name and first prose paragraph for
  search/list output and rendered agent copies.
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
UPSKILL_NO_CHECK
```

## Configuration

Upskill must work without an explicit initialization command. Any command that
needs managed state must create the default config directory and the specific
files or directories it needs before continuing.

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

- Missing `config.json` is treated as an empty config with defaults.
- Commands create `~/.skills/` before writing config, lock, store, rendered,
  overlay, draft, import, or cache state.
- Commands create managed directories lazily: `add` creates `store/` and the
  lock file, rendering creates `rendered/` and configured agent outputs, overlay
  commands create `overlays/`, draft commands create `drafts/`, and import
  commands create `imports/`.
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

### Source Records

`sources` stores known upstream locations. A source may be named or unnamed.
Names are optional conveniences for repeated commands; the source location
itself remains the canonical identity for unnamed sources.

Preferred write format:

```json
{
  "sources": [
    {
      "id": "src_01h...",
      "name": "Obsidian",
      "source": "~/Documents/Notes/Memory/Skills",
      "default_update_policy": "manual"
    },
    {
      "id": "src_01j...",
      "source": "example/skills",
      "ref": "main",
      "subpath": "skills",
      "default_update_policy": "auto"
    }
  ]
}
```

Read compatibility:

- Existing object/map `sources` config remains valid.
- When reading map config, keys are treated as source names unless the record has
  no `name` and the key is identical to the canonical source string.
- New writes should preserve existing source names but may migrate to the list
  form.

Rules:

- `source add <source> --name NAME` records a named source.
- `source add <source>` records an unnamed source.
- `source add --default-update-policy auto` makes future installs from that
  source auto-updating by default. The policy is copied into the installed skill
  lock entry and can be changed later per skill.
- Source records have a stable `id`. The display name may change without
  changing the identity used by lock entries, cache state, or update checks.
- `add <source> ...` also records the source if the argument is not already a
  configured source name. This supports one-step installs such as
  `upskill add ~/Documents/Notes/Memory/Skills --skill redis`.
- `--skill` is an install or inspection filter and is not part of source
  identity.
- Local source paths are expanded to absolute paths before storage.
- GitHub shorthand and Git URLs are stored in canonical form when possible.
- `ref` and `subpath` are part of source identity because they change the visible
  skill set.
- Re-adding the same canonical source updates the existing source record instead
  of creating a duplicate.
- Re-adding a source with an existing name updates that named record.
- `source list|ls` prints named sources by name. For unnamed sources, it prints the
  canonical source string where a name would otherwise appear.
- `source list|ls --json` includes `name`, `display_name`, `source`, `ref`,
  `subpath`, `id`, `canonical_id`, and `default_update_policy` for each source.
  `name` is omitted or empty for unnamed sources.

### Live Source Inspection

Users can inspect available upstream skills without installing them:

```bash
upskill source skills Obsidian
upskill source skills ~/Documents/Notes/Memory/Skills
upskill source skills example/skills --subpath skills
upskill source find Obsidian redis
upskill source search ~/Documents/Notes/Memory/Skills writing --limit 5
```

Rules:

- `source skills` resolves either a configured source name or a raw source
  string.
- `source find|search` resolves the same source inputs and filters skills by
  normalized store name, display name, description, `SKILL.md` body, and source
  path.
- The command inspects the current upstream source every time it runs.
- Local directory inspection reads the current directory contents directly.
- Git source inspection may use a temporary clone, persistent cache, or provider
  API internally, but output must reflect the requested source/ref/subpath rather
  than the installed store.
- The command never installs skills, updates the lock file, writes `store/`, or
  renders agent outputs.
- Output includes normalized store name, display name, description, source
  subpath, and `SKILL.md` path.
- `source find|search` uses the same output shape as `source skills`; when no
  skills match, text output prints `no matching skills` and exits successfully.
- `--ref` and `--subpath` override configured or parsed source values for that
  inspection only.
- Errors clearly distinguish source resolution failures, clone/fetch failures,
  and "no skills found".

### Source Freshness and Update Checks

Upskill has two different freshness questions:

1. Is the source catalog current?
2. Is an installed skill behind its source?

These questions must stay separate.

Source catalog freshness is about what a source contains now. `source skills`
and `source find|search` must inspect the current source every time. They must
not answer from `store/`, `rendered/`, the lock file, or a stale local clone.
New upstream skills appear in these command results as normal source data, not
as generic notifications.

Installed skill freshness is about locked local copies. Upskill compares each
installed skill's lock entry to the current source version for that same skill.
When the source version differs, Upskill reports an update notice. It does not
install new skills from that source unless the user runs `add`.

Source-specific rules:

- Local sources are inspected from the filesystem path recorded in the source
  record or lock entry. A local source update is detected by hashing the current
  source skill directory and comparing it to the installed lock hash.
- Git sources are inspected from the upstream remote, ref, and subpath recorded
  in the source record or lock entry. A persistent cache is allowed only as an
  implementation detail. Before returning source catalog data or update status,
  the cache must fetch the remote and check out the requested resolved revision.
- If a Git ref is omitted, Upskill uses the remote default branch.
- If a Git ref names a mutable branch, update checks compare the installed lock
  entry with the latest resolved commit for that branch.
- If a Git ref names a tag, Upskill resolves the tag for each check. Moving tags
  are treated as source changes.
- If a Git ref is a full commit SHA, Upskill treats it as pinned. Catalog reads
  still inspect that commit, but update checks should report no newer revision
  unless the skill files at that commit differ from the lock hash.
- Clone, fetch, checkout, and source-read failures must be surfaced clearly.
  Failed checks must not be silently treated as "up to date."

Check timing:

- Every command that loads Upskill config and works with skills or sources runs
  a preflight check before its main behavior.
- `help`, `version`, and pure parse errors do not run checks.
- Commands with explicit read-only contracts, such as `update --check`,
  `update --diff`, and source inspection commands, run the check but do not
  apply automatic updates. They report pending automatic updates instead.
- Normal commands apply automatic updates for skills whose lock entry has
  `update_policy: "auto"` before running their main behavior.
- Users may disable preflight checks with `--no-check` or `UPSKILL_NO_CHECK=1`.
  Commands that skip checks should not print update notices.

Notice behavior:

- Text CLI notices go to stderr so stdout remains stable for command output.
- JSON CLI commands include a top-level `notices` array when the command's
  normal JSON shape is an object. Commands that return arrays keep stdout as the
  requested array and print notices to stderr.
- MCP tool results include a `notices` field when checks find updates, checks
  fail, or automatic updates run.
- Notices are only for installed skills, failed checks, or automatic updates.
  Upskill must not print "new skill available" notices for uninstalled upstream
  skills.
- A typical update notice is:

  ```text
  notice: updates available for redis, docs-helper; run upskill sync or upskill update --diff redis
  ```

Automatic update behavior:

- Automatic updates refresh the installed base skill from its recorded source,
  update the lock entry, and render/copy outputs for configured agents.
- When the command has an `--agent` flag or MCP `agent` argument, automatic
  update rendering is limited to that agent. Otherwise it renders and copies for
  all configured agent outputs.
- Automatic updates are per installed skill. A source can provide a default
  policy for future installs, but source records do not force existing skills to
  change policy.
- `--no-render` suppresses rendering for the command's primary operation only.
  It does not suppress preflight automatic update rendering. Use `--no-check`
  when the user wants to skip all preflight work.
- Automatic updates must never install brand-new upstream skills.
- Automatic updates must use the same safe copy and atomic replacement behavior
  as `update`.
- If an automatic update fails, the command exits with an error before running
  behavior that would depend on stale generated output. Read-only commands
  report the failure without mutating state.

`sync` remains about installed skills. It updates locked installed skills and
renders final output. It does not refresh a source as a user-visible operation
and does not install newly discovered upstream skills.

If no output directories are configured, `upskill` may auto-detect well-known
directories that already exist:

```text
claude-code -> ${CLAUDE_CONFIG_DIR:-~/.claude}/skills
codex       -> ${CODEX_HOME:-~/.codex}/skills
```

Auto-detection must not create directories. Explicitly configured output
directories may be created during rendering.

## Lock File

`upskill-lock.json` is deterministic JSON:

```json
{
  "schema_version": 2,
  "skills": {
    "redis": {
      "name": "Redis",
      "source": "vercel-labs/agent-skills",
      "source_id": "src_01j...",
      "source_type": "git",
      "source_url": "https://github.com/vercel-labs/agent-skills.git",
      "ref": "main",
      "source_subpath": "skills",
      "skill_subpath": "redis",
      "skill_path": "skills/redis/SKILL.md",
      "resolved_revision": "abc123...",
      "update_policy": "manual",
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
- `source_id` links an installed skill to a configured source when one exists.
  Lock entries installed from raw or removed sources continue to work from their
  recorded source metadata.
- `source_subpath` is the configured source scan root. `skill_subpath` is the
  selected skill directory inside that scan root. Existing schema 1 `subpath`
  entries are read as `skill_subpath` until migrated.
- `hash` is computed from all files in the installed skill directory.
- `resolved_revision` stores the Git commit used for the installed copy when
  the source type is Git. Local sources omit it.
- `update_policy` is `manual` by default and may be `auto`.
- `sync` and `update` use this file to refresh base skills.

## Command Behavior

### `add`

`upskill add <source>` discovers skills from a local or Git source, copies the
selected skill directories into `store/`, updates the lock file, and renders by
default.

Rules:

- `<source>` may be a configured source name or any supported source string.
- If `<source>` is a raw source string, `add` records it as a configured source
  before installation. The auto-created source is unnamed and is displayed by
  canonical source string.
- Multiple discovered skills are installed unless `--skill` filters the result.
- Existing store skills are replaced atomically.
- Existing overlays are preserved.
- `--update-policy` writes the installed skill policy. If omitted, Upskill uses
  the source record's `default_update_policy`, then `manual`.
- To change an installed skill's update policy, reinstall it with `add --skill`
  and the desired `--update-policy` value.
- `--no-render` installs only into the base store.

### `show`

`upskill show <source-or-skill>` shows a selected skill body for inspection. If
the argument names an installed skill, it shows the rendered `SKILL.md`. If the
argument is a source string, it resolves the source in a temporary directory and
shows the selected `SKILL.md` without installing.

### `list` / `ls`

Lists installed base skills with source and overlay status. With `--agent`, the
overlay status reflects that agent's effective overlay chain. Text output marks
lock-backed source metadata as unconfigured when no configured source record
matches it. JSON output includes source metadata, source configuration status,
and overlay count.

### `find` / `search`

Searches installed skills by normalized name, display name, description, and
`SKILL.md` body. It is local to Upskill's installed store. Use
`source find|search` to search a source before installing from it.

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

Rendering skips unchanged rendered directories when the base skill hash, overlay
hash, mode, and agent match the existing render marker.

If overlays exist, they are always applied. There is no `--with-overlays` flag
because overlays are the default. There is no `--without-overlays` flag because
sync publishes the effective final state. Use `render --print --base-only` to
inspect the base state.

### Changing Update Policy

Installed skill update policy is changed by reinstalling from the skill source:

```bash
upskill add <source> --skill <skill-name> --update-policy manual|auto
```

Upskill does not provide a lock-only `update-policy` command. Reinstalling
refreshes the synced base skill, rewrites the lock entry, and preserves local
overlays.

### `migrate`

`upskill migrate` copies skill bundles into a local source and installs them
from that source. It is a dry run unless `--yes` is passed.

- Default source mode: `--from unmanaged`, which reads unmanaged skill folders
  from configured agent output directories.
- Default target source name: `local-skills`.
- Default target source path: `~/Documents/Upskill/local-skills`.
- `--from SOURCE` can copy from another configured local source.
- `--agent AGENT` limits unmanaged migration to one configured agent output.
- `--skill NAME` limits migration to one normalized skill name.
- With `--yes`, the command copies matching skill bundles into the target local
  source, records that source if needed, installs those skills into `store/` and
  `upskill-lock.json`, then renders managed output back to the selected agent
  output or to all configured agent outputs when `--agent` is omitted.
- The command does not delete from the source.
- Duplicate skill names are preserved with numeric suffixes such as `redis`,
  `redis-2`, and `redis-3`.

### `init`

Creates a new local skill skeleton with `SKILL.md` and frontmatter.

### `source`

`source` commands manage configured upstream sources in `config.json`.

`source add <source> [--name NAME] [--ref REF] [--subpath PATH]
[--default-update-policy manual|auto]` records a source without installing
skills. `--name` creates a shortcut; omitting `--name` stores an unnamed source
that is displayed by canonical source string.

`source list|ls` prints configured sources. It does not print upstream skill
catalog contents. Preflight may still check installed-skill freshness and print
notices unless disabled.

`source skills <source-or-name>` performs live source inspection and prints the
skills available in that source. It accepts named sources and raw source strings.
It does not install, update, render, or mutate the lock file.

`source find|search <source-or-name> [query]` performs live source inspection and
filters available skills by metadata, body, and path. It accepts `--json`,
`--limit`, `--ref`, and `--subpath`. It does not install, update, render, record
the source, or mutate the lock file.

`source remove|rm <source-or-name>` removes a configured source record and, by
default, removes installed skills whose locked source matches that source. It
also removes managed rendered output and marked agent-output copies for those
skills. If installed skills would be removed and `--yes` is not provided, the
command prints the affected skill list and exits without writing changes. It
does not delete upstream source files, overlays, or drafts.

`--keep-installed` removes only the configured source record and leaves
installed store copies, lock entries, overlays, drafts, and rendered outputs.

### `workon`

`upskill workon <skill-name>` is the recommended low-friction entry point for
users and agents that want to create or revise a skill.

Behavior:

- If a draft already exists for `<skill-name>`, report the draft identifier and
  metadata, then open `SKILL.md` in `$VISUAL` or `$EDITOR` when running
  interactively.
- If an installed base skill named `<skill-name>` exists, create a version
  draft from the pristine base skill and open `SKILL.md`.
- If no installed base skill exists, create a new-skill draft skeleton.
- `--from SKILL` creates a version draft from a different installed base skill.
- `--template NAME` selects a built-in draft skeleton for new-skill drafts.
- `--no-edit` creates or reports the draft without opening an editor.
- The command never mutates `store/`, `overlays/`, or an upstream repository.

### `draft`

Draft commands manage mutable local skill bundles under `drafts/`.

Rules:

- Drafts are full skill bundles with `SKILL.md` and any resource files.
- Drafts are intended for rapid iteration. The CLI creates requested files and
  opens them in `$VISUAL` or `$EDITOR` when the workflow depends on edits.
  Users and agents edit normal files, not overlay patch syntax.
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

- `upskill draft create <skill-name>` writes a minimal `SKILL.md` skeleton and
  opens it in the editor unless `--no-edit` is set.
- `upskill draft edit <skill-name> --file PATH` creates the file if needed and
  opens it in the editor.
- `upskill draft import` may replace or merge the skeleton with an imported
  bundle containing `SKILL.md`, then opens the imported `SKILL.md` or single
  imported file unless `--inspect` or `--no-edit` is set.
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
  local source and promotes into that source.
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

CLI overlay creation is editor-first:

- `upskill overlay add <skill-name>` creates the default root append overlay
  and opens it in `$VISUAL` or `$EDITOR` unless `--path` or `--no-edit` is set.
- `upskill overlay edit <skill-name>` creates the requested overlay operation
  file if needed and opens it in the editor.
- `--path` remains available for scripts that need the overlay directory.

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
7. If the final `SKILL.md` has no frontmatter block, prepend inferred default
   `name` and `description` frontmatter without changing the stored source.
8. Write `.upskill-render.json`.
9. Replace the final rendered cache directory atomically when possible.
10. If copy is enabled, replace only the output skill directory.

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
  "schema_version": 3,
  "skill": "redis",
  "agent": "codex",
  "mode": "with-overlays",
  "base_path": "/Users/example/.skills/store/redis",
  "base_hash": "sha256:...",
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
list_sources()
find_source_skills(source, query?, limit?, ref?, subpath?)
check_skill_updates(skill_id?, include_diff=false)
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

- Every MCP tool call runs the same preflight check rules as the CLI.
- MCP tools that are explicitly read-only return pending auto-update notices
  but do not apply automatic updates.
- `find_skills` returns installed base skills and drafts. It does not search
  every configured source.
- `list_sources` returns configured sources and their default update policy.
- `find_source_skills` performs live source inspection and returns current
  source catalog entries. It does not install skills, update the lock file, or
  render output.
- `check_skill_updates` returns installed-skill update status from recorded
  sources. It is read-only.
- `rendered=true` reads the effective rendered skill and applies overlays first.
- `rendered=false` reads the synced base skill from `store/`.
- `read_skill` returns the `SKILL.md` body plus a file manifest.
- `skill_id` may be the normalized store name or frontmatter name.
- `skill_id` may be a draft identifier such as `draft:redis`; draft reads use
  draft semantics and do not apply overlays unless the tool explicitly requests
  draft rendering with overlays.

Write semantics:

- MCP does not expose a lock-only update policy write. Change update policy by
  reinstalling a skill through the CLI `add --update-policy` path.
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
  updated or rendered. It also includes `notices` for preflight checks and
  automatic updates.
- Template overlay writes require Jinja template content in a `.j2` file. JSON
  variable-map overlays are rejected; variable-map template overlays are not in
  this version.

Safety:

- MCP clients can refresh base skills through `sync_skills`, but source updates
  happen from the lock file and configured sources, not from arbitrary MCP file
  writes.
- MCP clients can inspect configured sources live through source tools, but they
  cannot install newly discovered source skills unless a future explicit install
  tool is added.
- MCP clients can create and edit drafts, including brand-new draft skills, but
  cannot write synced base skills directly.
- MCP clients can import resource files into drafts from a shared import inbox
  or verified URLs. They cannot use import tools as arbitrary local file reads.
- MCP clients can promote drafts only into local checkout paths, never directly
  to a remote registry, Git host, or managed upskill directory.

## Source Freshness Implementation Plan

This plan describes the work needed to add source freshness, update notices,
and automatic installed-skill updates.

### Stage 1: Metadata and Compatibility

- Add stable source IDs and `default_update_policy` to source records.
- Add lock schema 2 fields: `source_id`, `source_subpath`, `skill_subpath`,
  `resolved_revision`, and `update_policy`.
- Preserve read compatibility for schema 1 lock files and existing source
  config shapes.
- Keep writes deterministic.
- Add focused tests for schema migration and stable source identity after source
  rename.

### Stage 2: Source Materialization and Revision Resolution

- Replace direct `materialize` calls with a source resolver that returns root,
  cleanup, source type, resolved revision, and source identity.
- For Git sources, support temp clones or persistent cache, but always fetch
  before live source inspection or update checks.
- For local sources, resolve and read the current local directory directly.
- Add tests with a local bare Git remote where a new skill appears after the
  first inspection and `source skills` sees it without reinstalling.

### Stage 3: Installed Update Checks and Notices

- Add a read-only update-check API shared by CLI and MCP.
- Detect changed installed skills by comparing current source hashes with lock
  hashes.
- Include source failures as explicit check results.
- Print text notices to stderr and expose structured MCP notices.
- Keep uninstalled upstream skills out of generic notices.

### Stage 4: Automatic Update Policy

- Add `--update-policy manual|auto` to `add`.
- Add `source add --default-update-policy manual|auto`.
- Change an installed skill's policy by reinstalling it with `add --skill` and
  the desired `--update-policy` value.
- Run preflight auto-updates before normal mutable commands.
- Keep explicit read-only commands read-only by reporting pending auto updates
  instead of applying them.
- Add `--no-check` and `UPSKILL_NO_CHECK=1`.

### Stage 5: MCP Source and Check Tools

- Add `list_sources`, `find_source_skills`, and `check_skill_updates`.
- Ensure every tool result can include notices without breaking existing fields.
- Keep MCP unable to install new source skills unless a future explicit install
  tool adds that capability.

### Stage 6: Documentation and End-to-End QA

- Update `README.md`, `docs/getting-started.md`, and `docs/workflows.md` after
  implementation lands.
- Document the distinction between source catalog freshness, installed skill
  freshness, `sync`, and auto update policy.
- Add an end-to-end scenario that installs a skill from a Git source, changes
  the upstream repo, observes a notice, runs `sync`, and verifies rendered agent
  output.

## Historical Implementation Plan

The following stages captured the implementation plan used to build the current
CLI, draft, and MCP surfaces. Treat them as historical context and use the
active agent plan for new documentation or implementation work.

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
- Implement `upskill draft list|ls/create/path/files/read/add-file/remove|rm`.
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
- Treat rendered agent-local skill files as generated output, not as the source
  to edit when a user asks to fix a skill.
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
- `list` shows installed skills, and `find|search` searches installed skills.
- `remove` deletes base skill and lock entry but preserves overlays by default.
- `update` refreshes a locked local skill and renders by default.
- `sync` updates locked skills and renders overlays.
- `init` creates a valid `SKILL.md`.
- `source add` records named and unnamed local/Git sources.
- `source add --default-update-policy auto` records the source default policy.
- `source list|ls` displays unnamed sources by canonical source string.
- `source skills` inspects named and raw sources live without installing.
- `source find|search` searches one named or raw source live without installing.
- `source skills` for a Git source fetches remote changes before listing skills.
- `source find|search` sees a newly added upstream Git skill even when an
  internal clone or cache already existed.
- `add <raw-source>` records an unnamed source before installing from it.
- `add --update-policy auto` records an auto-updating lock entry.
- Reinstalling with `add --skill` and `--update-policy manual|auto` changes an
  installed skill's update policy.
- Commands with preflight checks print installed-skill update notices without
  printing generic uninstalled-skill notices.
- `--no-check` and `UPSKILL_NO_CHECK=1` suppress preflight update checks.
- Top-level help documents `--no-check` as a global option.
- `workon` opens an existing draft in the editor, creates a version draft for
  installed skills, and creates a new-skill draft for unknown names.
- `draft create/edit/list/path/files/read/add-file/remove` cover the draft
  lifecycle.
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
- Git branch refs resolve to the latest remote commit during live source
  inspection.
- Git commit refs are treated as pinned.
- Git tag refs are resolved during each check.
- Skill discovery finds root and nested `SKILL.md` files.
- Skill discovery ignores configured working directories such as `overlays`,
  `drafts`, `imports`, and `rendered`.
- Skill hashes are deterministic.
- Local source update checks compare current source skill hashes to lock hashes.
- Git source update checks compare current fetched source skill hashes to lock
  hashes and include the resolved revision.
- Failed fetch, checkout, local source read, and hash operations are reported as
  failed checks.

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
- MCP tool calls run preflight checks and return notices.
- MCP `find_source_skills` performs live source inspection without installing.
- MCP `check_skill_updates` is read-only and reports changed, unchanged, and
  failed installed-skill checks.

### Prompt

- `prompts/AGENTS.md` exists, has a `Using Skills` heading, and tells agents to
  use `upskill` MCP tools for skill operations, drafts, imports, and overlays.
