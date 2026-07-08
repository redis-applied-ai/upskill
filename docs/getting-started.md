# Getting Started

This guide takes a new checkout from build to a rendered skill and optional MCP
setup. It uses the local example skill in this repository so you can verify the
tool without depending on any remote skill source.

## Build Or Install

From the repository root:

```bash
make
```

The binary is written to `bin/upskill`. Use it directly:

```bash
bin/upskill --help
```

Or install it somewhere on your `PATH`:

```bash
mkdir -p ~/.local/bin
install -m 0755 bin/upskill ~/.local/bin/upskill
upskill --help
```

The examples below assume `upskill` is on your `PATH`. If it is not, replace
`upskill` with `bin/upskill`.

## Initialize Local State

`upskill` defaults to `~/.skills` for installed base skills, overlays, drafts,
imports, rendered output, config, and the lock file.

Create the default directories:

```bash
upskill doctor
```

`doctor` creates these directories when needed:

```text
~/.skills/store
~/.skills/overlays
~/.skills/drafts
~/.skills/imports
~/.skills/rendered
```

It does not create `config.json`. Create one when you want rendered skills copied
into agent-specific skill directories:

```bash
cat > ~/.skills/config.json <<'JSON'
{
  "agents": {
    "codex": "~/.codex/skills",
    "claude-code": "~/.claude/skills"
  },
  "imports_dir": "~/.skills/imports",
  "import_dirs": ["~/.skills/imports"]
}
JSON
```

Use only the agents you need. If no agents are configured, `upskill` still
renders under `~/.skills/rendered`, but it copies to agent directories only when
they are configured or auto-detected.

## Migrate Existing Agent Skills

If you already have skill folders in agent output directories such as
`~/.codex/skills` or `~/.claude/skills`, `doctor` and `status` warn when those
skills are not installed from a configured Upskill source.

Inspect the migration plan:

```bash
upskill migrate
```

By default, this is a dry run. It reads unmanaged agent-output skills and plans
to copy them into a `local-skills` source at
`~/Documents/Upskill/local-skills`. Duplicate skill names are preserved with
numeric suffixes such as `redis`, `redis-2`, and `redis-3`.

Apply the migration only after reviewing the plan:

```bash
upskill migrate --yes
```

## Install The Example Skill

From this repository root:

```bash
upskill add ./examples --skill hello-skill --agent codex
```

What this does:

- Discovers `examples/hello-skill/SKILL.md`.
- Copies the skill into `~/.skills/store/hello-skill`.
- Records the source in `~/.skills/upskill-lock.json`.
- Builds the effective skill into `~/.skills/rendered/codex/hello-skill`.
- Copies the generated skill to the configured `codex` output directory.

Search and inspect it:

```bash
upskill find hello
upskill list
upskill show hello-skill --agent codex
upskill status
```

## Use A Local Directory As A Source

Use a local source when upstream skills already live in normal files, such as an
Obsidian skills directory, a synced folder, or a personal/team skills directory.
You do not need to move those files into a special layout.

Add the existing directory as a named source and inspect what skills it contains:

```bash
upskill source add ~/Documents/Notes/Memory/Skills --name Obsidian
upskill source skills Obsidian
upskill source find Obsidian hello
```

Install from the named source:

```bash
upskill add Obsidian --skill hello-skill --agent codex
```

You can also install from a raw path. `upskill` records that source
automatically and displays it by path in `upskill source list` / `upskill source ls`:

```bash
upskill add ~/Documents/Notes/Memory/Skills --skill hello-skill --agent codex
```

Use `upskill list` to see which source owns each installed skill. Removing a
source removes skills installed from that source and managed generated copies;
when skills would be removed, `upskill` prints the affected skill list and
requires `--yes`. Add `--keep-installed` only when you want to remove the source
record while leaving installed copies in place.

Source inspection reads the current source every time. If another skill is added
to the local directory or upstream Git repo, `upskill source skills` and
`upskill source find` show it without requiring a separate source sync. New
source skills are not installed automatically; install them explicitly with
`upskill add <source> --skill <name>`.

Installed skills are synced copies. Most commands check whether installed skills
have changed upstream and print notices to stderr when updates are available.
Preview and apply manual updates with:

```bash
upskill update --check
upskill update hello-skill --diff
upskill sync --agent codex
```

Use automatic updates when a skill should refresh whenever Upskill runs a normal
command:

```bash
upskill add Obsidian --skill hello-skill --update-policy auto --agent codex
```

To change that policy later, reinstall the skill from the same source:

```bash
upskill add Obsidian --skill hello-skill --update-policy manual --agent codex
```

You can also set a source default for future installs:

```bash
upskill source add ~/Documents/Notes/Memory/Skills --name Obsidian --default-update-policy auto
```

Read-only commands such as `upskill update --check`, `upskill update --diff`,
`upskill source skills`, and `upskill source find` report pending automatic
updates without applying them. Add `--no-check`, or set `UPSKILL_NO_CHECK=1`,
when you need to skip preflight checks and notices for one command.

## Configure MCP

Run the MCP server manually once to verify the command starts:

```bash
upskill mcp
```

Stop it with `Ctrl-C`. MCP clients start the server themselves after you add it
to their config.

### Codex

Codex stores MCP settings in `~/.codex/config.toml`, or in a project-scoped
`.codex/config.toml` for trusted projects. Add:

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

In the Codex TUI, use `/mcp` to verify the server is active.

### Claude Desktop

Claude Desktop can connect to local stdio MCP servers through
`claude_desktop_config.json`. Open Settings > Developer > Edit Config and add:

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

Save the file and restart Claude Desktop. Use absolute paths for the command and
any configured directories.

Claude Desktop also supports desktop extensions for local MCP servers. `upskill`
currently documents direct stdio configuration; packaging a `.mcpb` extension is
separate release work.

## Teach Agents To Use Skills

Copy or adapt `prompts/AGENTS.md` into the agent or project instructions for
clients that should use `upskill` as the skill server. The copied instructions
must tell agents that local skill files are generated output. When asked to fix a
skill, agents should write a local customization overlay for local or
agent-specific behavior, or create a draft for an upstream base-skill change.

A first verification prompt:

```text
Use the skill instructions in AGENTS.md. List available skills, read
hello-skill rendered for Codex, read its referenced support files, and tell me
which files you used. Do not edit rendered skill files.
```

Expected MCP flow:

1. `find_skills`
2. `read_skill` with `skill_id: "hello-skill"`, `agent: "codex"`, and
   `rendered: true`
3. `list_skill_files`
4. `read_skill_file` for `references/checklist.md`

## Next Steps

- Use `docs/workflows.md` for local customizations, update checks, draft
  creation, archive import, promotion, and Jinja template customizations.
- Use `upskill source skills <source>` or the MCP `find_source_skills` tool to
  inspect current source catalogs without installing anything.
- Use `upskill update --check` or the MCP `check_skill_updates` tool to preview
  installed source updates.
- Use `upskill sync --agent codex` to update locked skills and build final
  outputs.

## MCP Client References

- Codex MCP configuration: <https://developers.openai.com/codex/mcp>
- Codex config files: <https://developers.openai.com/codex/config-basic>
- Claude Desktop local MCP servers:
  <https://modelcontextprotocol.io/docs/develop/connect-local-servers>
- MCP stdio debugging:
  <https://modelcontextprotocol.io/docs/tools/debugging>
