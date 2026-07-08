# upskill Workflows

This guide shows the common workflows for using `upskill` as a local skill
manager and MCP skill server. The examples use GitHub-style sources, but every
source command also accepts local paths.

Build the binary first when working from this repo:

```bash
make
```

The primary examples use the local `./examples` directory so they work from a
fresh checkout without a remote skills repository.

## Configure Outputs

`upskill` stores synced base skills under `~/.skills/store`, local
customizations under `~/.skills/overlays`, drafts under `~/.skills/drafts`, and
generated output under `~/.skills/rendered`.

Configure agent output directories in `~/.skills/config.json`:

```json
{
  "agents": {
    "codex": "~/.codex/skills",
    "claude-code": "~/.claude/skills"
  },
  "imports_dir": "~/.skills/imports",
  "import_dirs": ["~/.skills/imports"]
}
```

When a command prints a rendered path, use that path as the source of truth.
If agents are configured, render output is agent-specific, such as
`~/.skills/rendered/codex/<skill-name>`.

## Migrate Existing Skills

Use `migrate` when skill bundles already exist outside an Upskill source and
you want them managed from a normal local source directory.

The default migration reads unmanaged skills from configured agent output
directories and writes them to a `local-skills` source:

```bash
upskill migrate
upskill migrate --yes
```

The first command is a dry run. The second command copies the bundles into
`~/Documents/Upskill/local-skills`, records that source if needed, installs the
copied skills into `store/`, and renders managed output. Duplicate names are
kept with numeric suffixes such as `redis`, `redis-2`, and `redis-3`.

Limit migration to one agent or skill when reviewing a noisy system:

```bash
upskill migrate --from unmanaged --agent codex
upskill migrate --from unmanaged --skill redis
```

You can also copy from one configured local source to another without deleting
from the original source:

```bash
upskill migrate --from personal --to-source local-skills --skill redis --yes
```

## Use An Obsidian Or Filesystem Directory

Use a local source when skills already live in normal editable files and
`upskill` should keep installed base skills as synced copies.

```bash
upskill source add ~/Documents/Notes/Memory/Skills --name personal
upskill source skills personal
upskill add personal --skill hello-skill --agent codex
```

The local directory is the upstream skill source. `~/.skills/store` is the
installed copy and should not be edited directly. Overlays, drafts, imports, and
generated output stay in the managed `upskill` home unless configured otherwise.

You can also install directly from a raw local path:

```bash
upskill add ~/Documents/Notes/Memory/Skills --skill hello-skill --agent codex
```

Raw sources are recorded automatically and displayed by path in
`upskill source list` / `upskill source ls`.

Local source inspection reads the directory directly every time:

```bash
upskill source skills personal
upskill source find personal redis
```

If you edit a skill in that directory, the next source command shows the current
source content. Installed copies in `~/.skills/store` do not change until a
manual update or automatic update policy applies.

## Use An Existing Memory-Server Skills Directory

If an existing memory server stores skill bundles as ordinary directories, point
`upskill` at that directory:

```bash
upskill source add ~/Obsidian/Skills --name personal
upskill source skills personal
upskill add personal --agent codex
```

After setup:

- The configured directory is the local upstream skill source.
- `~/.skills/store` is the installed copy and should not be edited directly.
- Local customizations belong in overlays.
- Rapid skill creation and upstream revisions belong in drafts.
- Agents should use the `upskill` MCP draft and overlay tools instead of
  writing base skills directly.

Use `upskill list` to see source ownership for installed skills. Removing a
source also removes skills installed from that source and managed generated
copies; when skills would be removed, `upskill` prints the affected skill list
and requires `--yes`. Use `upskill source remove personal --keep-installed` only
when you want to detach the source record and keep the installed copies.

For current limits such as local-only search, promotion boundaries, sync
discovery behavior, and Jinja template customization rules, see `README.md`.

## Add One Skill

Install one skill from a repo or local checkout with `--skill`:

```bash
upskill add ./examples --skill hello-skill --agent codex
```

What happens:

- `upskill` discovers skill directories containing `SKILL.md`.
- Only the selected skill is copied into the read-only base store.
- The lock file records the source and hash.
- The effective skill is rendered and copied to the configured `codex` output.

Use `--no-render` when you only want to populate the base store:

```bash
upskill add ./examples --skill hello-skill --no-render
```

## Add All Skills From A Repo

Omit `--skill` to install every discovered skill:

```bash
upskill add ./examples --agent codex
```

For a repo that keeps skills under a subdirectory:

```bash
upskill add owner/repo --subpath skills --agent codex
```

You can also save a source shortcut:

```bash
upskill source add ./examples --name local-examples
upskill add local-examples --agent codex
```

Git sources behave the same way from the user's point of view:

```bash
upskill source add owner/repo --name team-skills --subpath skills
upskill source skills team-skills
upskill source find team-skills redis
upskill add team-skills --skill redis --agent codex
```

`source skills` and `source find` inspect the current upstream ref. They do not
answer from the installed store and they do not install newly added upstream
skills.

## Customize An Installed Skill With An Agent

Use an overlay, which is a local customization layer, for behavior changes after
a run. This is the right target when the base skill should stay
upstream-managed.

Example user instruction to an agent connected to the `upskill` MCP server:

```text
The last run of hello-skill was too verbose. Use upskill to find hello-skill,
read the rendered skill, add a Codex-specific local customization that tells it
to answer concisely, build generated output, then read the rendered skill again
to verify the customization is present.
```

The agent should use MCP tools in this shape:

```json
{"name": "find_skills", "arguments": {"query": "hello", "limit": 5}}
```

```json
{
  "name": "read_skill",
  "arguments": {
    "skill_id": "hello-skill",
    "agent": "codex",
    "rendered": true,
    "include_files": true
  }
}
```

```json
{
  "name": "upsert_skill_overlay_file",
  "arguments": {
    "skill_id": "hello-skill",
    "agent": "codex",
    "operation": "root_append",
    "path": "SKILL.md",
    "content": "\nFor Codex, prefer concise answers and include only the commands needed.\n"
  }
}
```

```json
{
  "name": "render_skill",
  "arguments": {
    "skill_id": "hello-skill",
    "agent": "codex",
    "copy": true
  }
}
```

Then the agent should call `read_skill` again with `rendered: true` and confirm
the customization text appears. MCP writes are overlay-only; agents cannot
overwrite base skills through the MCP server.

The equivalent CLI inspection command is:

```bash
upskill show hello-skill --agent codex
```

To author the same kinds of local customizations without an MCP client:

```bash
upskill overlay edit hello-skill
upskill overlay edit hello-skill --operation replace --file references/checklist.md
upskill overlay edit hello-skill --operation patch --file metadata.patch.json
upskill render hello-skill --agent codex
upskill show hello-skill --agent codex
```

Those operations demonstrate root append, file replacement, and JSON Patch
against `metadata.json`.

## Check For Updates

Use `update --check` to compare locked skills against their recorded sources
without writing to `store/`, `rendered/`, or agent outputs:

```bash
upskill update --check
upskill update hello-skill --check
```

Example output:

```text
hello-skill   changed sha256:old -> sha256:new
```

If everything is current, `upskill` prints the unchanged rows and then:

```text
no updates available
```

MCP clients can run the same read-only check:

```json
{"name": "check_skill_updates", "arguments": {"skill_id": "hello-skill", "include_diff": true}}
```

The result includes changed, unchanged, and failed checks. A failed source read
is reported on the individual check instead of being treated as current.

## Preview Diffs And Apply Updates

Use `--diff` to preview incoming file changes. It is also read-only:

```bash
upskill update hello-skill --diff
```

The diff output is simple and file-oriented:

```text
hello-skill   changed sha256:old -> sha256:new
diff SKILL.md
-old file body
+new file body
```

Apply updates with `update`:

```bash
upskill update hello-skill --agent codex
```

Update every locked skill and render/copy final outputs with:

```bash
upskill sync --agent codex
```

`sync` always applies overlays. There is no `--without-overlays` mode because
sync publishes the effective local skill state. To inspect the base skill
without local customizations:

```bash
upskill render hello-skill --print --base-only
```

`sync` is about installed skills. It refreshes locked installed copies and
renders output, but it does not install a new skill that appeared in a source.
Use `upskill source skills <source>` to discover new upstream skills and
`upskill add <source> --skill <name>` to install one.

## Use Automatic Update Policy

Manual update policy is the default. With manual policy, normal commands print a
notice when an installed skill has changed upstream:

```text
notice: updates available for hello-skill; run upskill sync or upskill update --diff hello-skill
```

Set `auto` when an installed skill should refresh before normal Upskill
commands. To change an installed skill's policy later, reinstall it from the
same source with the new `--update-policy` value:

```bash
upskill add ./examples --skill hello-skill --update-policy auto --agent codex
upskill add ./examples --skill hello-skill --update-policy manual --agent codex
```

Set a source default for future installs:

```bash
upskill source add ./examples --name local-examples --default-update-policy auto
upskill add local-examples --skill hello-skill
```

Automatic updates refresh the installed base skill, update the lock entry, and
render/copy configured agent outputs. If the command has `--agent`, preflight
rendering is limited to that agent.

Read-only commands report pending automatic updates without applying them:

```bash
upskill update --check
upskill update hello-skill --diff
upskill source skills local-examples
upskill source find local-examples hello
```

Skip all preflight checks and notices for one command with:

```bash
upskill list --no-check
UPSKILL_NO_CHECK=1 upskill list
```

## Search For Skills

Installed-skill search is local to Upskill's store. Source search inspects one
requested source live. `upskill` does not yet provide broad remote registry
search or search across common public sources.

Search installed skills:

```bash
upskill find hello
upskill search hello
upskill find hello --json
```

Search through MCP:

```json
{"name": "find_skills", "arguments": {"query": "hello", "limit": 10}}
```

If you want to inspect a known source without installing it:

```bash
upskill source find ./examples hello
upskill show ./examples --skill hello-skill
```

MCP source inspection uses `find_source_skills`:

```json
{"name": "find_source_skills", "arguments": {"source": "./examples", "query": "hello", "limit": 10}}
```

If you want to make a common source easy to reuse, add it as a named source:

```bash
upskill source add ./examples --name local-examples
upskill source search local-examples hello
upskill add local-examples --skill hello-skill
```

MCP clients can inspect configured sources and check installed skill updates:

```json
{"name": "list_sources", "arguments": {}}
```

```json
{"name": "check_skill_updates", "arguments": {"skill_id": "hello-skill"}}
```

## Instruct An LLM To Use Skills

Run the server:

```bash
upskill mcp
```

Add it to your MCP client as a stdio server using the command path for your
installed binary.

For Codex, edit `~/.codex/config.toml` or a trusted project
`.codex/config.toml`:

```toml
[mcp_servers.upskill]
command = "/absolute/path/to/upskill"
args = ["mcp"]

[mcp_servers.upskill.env]
UPSKILL_HOME = "/Users/you/.skills"
```

You can also add it with the Codex CLI:

```bash
codex mcp add upskill --env UPSKILL_HOME="$HOME/.skills" -- "$(command -v upskill)" mcp
```

For Claude Desktop, open Settings > Developer > Edit Config and add an entry to
`claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "upskill": {
      "command": "/absolute/path/to/upskill",
      "args": ["mcp"],
      "env": {
        "UPSKILL_HOME": "/Users/you/.skills"
      }
    }
  }
}
```

Restart Claude Desktop after saving the config. Use absolute paths; MCP clients
may start stdio servers with an undefined working directory.

Copy `prompts/AGENTS.md` into the agent or project instructions. The important
rules are:

- Use `find_skills`, `read_skill`, `list_skill_files`, and `read_skill_file`
  for skill retrieval.
- Treat rendered agent-local skill files as generated output, not edit targets.
- Treat synced base skills as read-only.
- Use overlay tools for local or agent-specific customization.
- Use draft tools for new skills and upstream base-skill revisions.

Example prompt:

```text
Use the skill instructions in AGENTS.md. Find hello-skill, read the rendered
hello-skill body and support files for Codex, then follow that skill's
instructions for this task. If a skill needs to change, do not edit rendered
skill files; use an overlay or draft.
```

Expected MCP sequence:

```json
{"name": "find_skills", "arguments": {"query": "hello"}}
```

```json
{"name": "read_skill", "arguments": {"skill_id": "hello-skill", "agent": "codex", "rendered": true}}
```

The agent then uses the returned `SKILL.md` body and any listed resource files
as its working instructions.

## Draft A New Skill With An LLM Over MCP

Use drafts when the skill itself is new or when you want a full editable bundle
with support files.

Example prompt:

```text
Create a new skill named gamma-tool using upskill. Include a SKILL.md, a
scripts/gamma.sh helper, and a references/gamma.md reference file. Render the
draft so I can inspect it.
```

Expected MCP calls:

```json
{"name": "create_skill_draft", "arguments": {"skill_name": "gamma-tool", "allow_existing": true}}
```

```json
{
  "name": "upsert_skill_draft_file",
  "arguments": {
    "draft_id": "draft:gamma-tool",
    "path": "SKILL.md",
    "content": "---\nname: Gamma Tool\ndescription: gamma workflow helper\n---\n\n# Gamma Tool\n\nUse `scripts/gamma.sh` for checks.\n"
  }
}
```

```json
{
  "name": "upsert_skill_draft_file",
  "arguments": {
    "draft_id": "draft:gamma-tool",
    "path": "scripts/gamma.sh",
    "content": "#!/bin/sh\necho gamma\n"
  }
}
```

```json
{
  "name": "upsert_skill_draft_file",
  "arguments": {
    "draft_id": "draft:gamma-tool",
    "path": "references/gamma.md",
    "content": "# Gamma Reference\n\nExtra guidance.\n"
  }
}
```

Verify:

```json
{"name": "list_skill_draft_files", "arguments": {"draft_id": "draft:gamma-tool", "recursive": true}}
```

```json
{"name": "render_skill_draft", "arguments": {"draft_id": "draft:gamma-tool", "agent": "codex", "copy": false}}
```

For larger bundles, have the agent write a `.zip` or `.tar.gz` into an allowed
import directory, then import it by path:

```bash
mkdir -p ~/.skills/imports
tar -czf ~/.skills/imports/gamma-tool.tar.gz -C /path/to gamma-tool
shasum -a 256 ~/.skills/imports/gamma-tool.tar.gz
upskill draft create gamma-tool
upskill draft import gamma-tool \
  --archive ~/.skills/imports/gamma-tool.tar.gz \
  --sha256 <hex-sha256> \
  --inspect
upskill draft import gamma-tool \
  --archive ~/.skills/imports/gamma-tool.tar.gz \
  --sha256 <hex-sha256>
```

For interactive CLI use, `draft create`, `draft edit`, `draft add-file`, and
non-inspection `draft import` open `$VISUAL` or `$EDITOR` on the file that needs
attention. Add `--no-edit` when running those commands from scripts.

`--sha256` may be the hex digest or the same value with a `sha256:` prefix.
Archive import skips common macOS metadata entries such as `__MACOSX/` and
`._*` files.

The MCP equivalent is:

```json
{
  "name": "import_skill_draft_archive",
  "arguments": {
    "draft_id": "draft:gamma-tool",
    "archive_path": "/Users/you/.skills/imports/gamma-tool.zip",
    "sha256": "..."
  }
}
```

Local MCP imports must be inside `import_dirs`.

## Turn A Draft Into A PR

`upskill` has a helper for copying a finished draft into a local upstream
checkout. It does not create branches, push, or open pull requests directly.
Use `git` and `gh` for that part.

Start a branch in the upstream checkout:

```bash
cd ~/src/skills
git checkout -b add-gamma-tool
```

Preview the promotion:

```bash
upskill draft promote gamma-tool --to ~/src/skills/skills --dry-run
```

Apply it:

```bash
upskill draft promote gamma-tool --to ~/src/skills/skills
```

Commit and open the PR:

```bash
cd ~/src/skills
git status --short
git add skills/gamma-tool
git commit -m "Add gamma tool skill"
git push -u origin add-gamma-tool
gh pr create --fill
```

Through MCP, use `promote_skill_draft` first with `dry_run: true`, then with
`dry_run: false` after reviewing the planned writes.

If the destination is a named local source, prefer `--to-source`:

```bash
upskill draft promote gamma-tool --to-source personal --dry-run
upskill draft promote gamma-tool --to-source personal
upskill add personal --skill gamma-tool
```

Through MCP, pass `to_source: "personal"`. Promotion writes to the source but
does not install the skill. Run `add` for a new skill or `update` for an
already locked skill after the source change is ready.

## New Skill Lifecycle

The lifecycle for a new skill is:

1. Create a local draft with `create_skill_draft` or `upskill draft create`.
2. Add or import `SKILL.md` and support files.
3. Render the draft and iterate until the agent can use it successfully.
4. Promote the draft into an upstream checkout or named local source.
5. Open and merge a PR in that skill repo.
6. Install the merged skill with `upskill add <source> --skill <name>`.
7. After the skill is locked locally, future changes arrive through
   `upskill update`, `upskill update --check`, and `upskill sync`.

Important detail: `sync` updates locked skills. It does not discover brand-new
skills that were not already installed. After a new skill PR merges, run
`upskill add <source> --skill <new-skill>` once.

## Advanced Jinja Template Customizations

Jinja rendering is enabled only for files ending in `.j2`. Non-`.j2` files are
literal files.

### Author A Skill With Blocks

Skill discovery accepts `SKILL.md.j2` directly. For a templated skill, make
`SKILL.md.j2` the source entrypoint; do not maintain a parallel placeholder
`SKILL.md` just for discovery:

```text
examples/template-skill/
  SKILL.md.j2
```

Example `SKILL.md.j2`:

```jinja
---
name: Template Skill
description: Example skill with a Jinja-rendered entrypoint
---

# Template Skill

{% block guidance %}
Base guidance for the template skill.
{% endblock %}

{% block examples %}
Base examples.
{% endblock %}
```

The rendered output strips `.j2`, so `SKILL.md.j2` renders to the final
`SKILL.md` that agents load.

### Customize Template Blocks With `super()`

Create a Jinja template customization:

```bash
upskill overlay edit template-skill --operation template --file SKILL.md.j2
```

Use `parent/<path>` to extend the previous layer:

```jinja
{% extends "parent/SKILL.md.j2" %}

{% block guidance %}
{{ super() }}

Local guidance: prefer concise answers and include exact commands.
{% endblock %}
```

Render and inspect:

```bash
upskill render template-skill --copy=false
upskill render template-skill --print
```

For agent-specific template customizations:

```bash
upskill overlay edit template-skill --agent codex --operation template --file SKILL.md.j2
```

### Full Replacement When Blocks Are Not Enough

Use `replace` for a complete file replacement:

```bash
upskill overlay edit hello-skill --operation replace --file SKILL.md
```

Use the default root append operation for additive text:

```bash
upskill overlay edit hello-skill
```

Use JSON Patch only for JSON files:

```bash
upskill overlay edit hello-skill --operation patch --file metadata.patch.json
```

```json
{
  "target": "metadata.json",
  "operations": [
    {"op": "add", "path": "/tags/-", "value": "codex"}
  ]
}
```
