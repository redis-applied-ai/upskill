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

## Install The Example Skill

From this repository root:

```bash
upskill add ./examples --skill hello-skill --agent codex
```

What this does:

- Discovers `examples/hello-skill/SKILL.md`.
- Copies the skill into `~/.skills/store/hello-skill`.
- Records the source in `~/.skills/upskill-lock.json`.
- Renders the effective skill into `~/.skills/rendered/codex/hello-skill`.
- Copies the rendered skill to the configured `codex` output directory.

Search and inspect it:

```bash
upskill find hello
upskill render hello-skill --print --agent codex
upskill status
```

## Use A Vault When Local Files Are The Source

A vault is a local editable skill workspace that `upskill` treats as a named
source. Use it when your upstream skills live in normal files, such as an
Obsidian vault, a synced folder, or a personal/team skills directory.

Create a vault:

```bash
upskill vault init ~/SkillVault --name personal
```

Put skill bundles under `~/SkillVault/source/`, then install from the named
source:

```bash
upskill add personal --skill hello-skill --agent codex
```

You do not need a vault when installing directly from Git repositories.

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

## Teach Agents To Use upskill

Copy or adapt `prompts/AGENTS.md` into the agent or project instructions for
clients that should use `upskill` as the skill server.

A first verification prompt:

```text
Use the upskill MCP server for skill work. List available skills, read
hello-skill rendered for Codex, read its referenced support files, and tell me
which files you used.
```

Expected MCP flow:

1. `find_skills`
2. `read_skill` with `skill_id: "hello-skill"`, `agent: "codex"`, and
   `rendered: true`
3. `list_skill_files`
4. `read_skill_file` for `references/checklist.md`

## Next Steps

- Use `docs/workflows.md` for overlays, update checks, draft creation, archive
  import, promotion, and Jinja template overlays.
- Use `upskill update --check` to preview source updates.
- Use `upskill sync --agent codex` to update locked skills and render final
  outputs.

## MCP Client References

- Codex MCP configuration: <https://developers.openai.com/codex/mcp>
- Codex config files: <https://developers.openai.com/codex/config-basic>
- Claude Desktop local MCP servers:
  <https://modelcontextprotocol.io/docs/develop/connect-local-servers>
- MCP stdio debugging:
  <https://modelcontextprotocol.io/docs/tools/debugging>
