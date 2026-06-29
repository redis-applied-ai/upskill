# Skill Management

Use the `upskill` MCP server for skill work whenever its tools are available.
Do not use built-in skill servers for listing, searching, reading, creating, or
customizing skills unless the `upskill` MCP server is unavailable.

Base skills from `upskill` are read-only. Treat synced base skill files as
vendor-managed content.

- Start by calling `find_skills` with no query to list available base skills and
  drafts. Use a `query` only when narrowing the list.
- `find_skills` returns base skills and drafts. Draft IDs look like
  `draft:<skill-name>` and should be passed back exactly as returned.
- Use `read_skill`, `list_skill_files`, and `read_skill_file` to inspect base
  or draft skill bodies and support files.
- When using a rendered skill, read the skill body first, then list/read any
  support files referenced by the skill before acting on it.

For lightweight customization of an existing skill, write overlays through
`upskill` MCP tools:

- Use `upsert_skill_overlay_file` to add append, replace, file, template, or
  JSON Patch overlays.
- Use `delete_skill_overlay_file` only for overlay files.
- Use `render_skill` to inspect the effective result after overlay edits.
- After writing an overlay, render the skill and read the rendered skill again
  to verify the intended text or file change is present.
- Use `sync_skills` when synced base skills and rendered agent outputs should be
  refreshed. Treat its `updated_count`, `rendered_count`, and warnings as the
  source of truth for what happened.
- Template overlays must be full Jinja template files with `.j2` paths. Do not
  send JSON variable maps as template overlays.

Prefer agent-specific overlays when a change should apply only to one agent.
Use global overlays only for customization that should affect every agent.

For creating a new skill or drafting a new upstream version, use draft tools:

- Use `create_skill_draft` for both new skills and version drafts. If the named
  skill already exists as a base skill, this creates a version draft by default.
  If it does not exist, this creates a new draft skill skeleton.
- If a same-named draft already exists but is the wrong kind, use
  `delete_skill_draft` or create with `replace_existing: true` only after the
  user is comfortable replacing that local draft.
- Use `list_skill_drafts`, `read_skill_draft`, `list_skill_draft_files`, and
  `read_skill_draft_file` to inspect mutable draft bundles.
- Use `upsert_skill_draft_file` and `delete_skill_draft_file` to edit draft
  files. These tools write only under the draft directory and cannot change the
  synced base skill.
- Use `delete_skill_draft` only to remove a mutable draft bundle, never for
  base skills.
- Use `import_skill_draft_archive` for skill bundle zip or tar.gz files and
  `import_skill_draft_file` for individual resource files.
- For local imports through MCP, the path must be in an `upskill` import
  directory. If you need to transfer a bundle, place it in that shared import
  directory and pass the path to the import tool. Do not try to send large
  resource bundles as one MCP string argument.
- Use `render_skill_draft` to inspect draft output. Draft renders do not apply
  overlays unless `apply_overlays` is explicitly true.
- Use `diff_skill_draft` and `rebase_skill_draft` when iterating on a version
  draft of an existing base skill.
- Use `promote_skill_draft` only to copy a finished draft into a local upstream
  checkout or configured local source. Prefer `to_source` when the user has an
  upskill vault or named local source. Promotion defaults to dry-run; review
  the planned writes before setting `dry_run` to false.
- After promotion, the skill is not automatically installed. For a new skill,
  tell the user to run `upskill add <source> --skill <skill-name>` or use the
  equivalent workflow. For an existing locked skill, use `sync_skills` or
  `upskill update <skill-name>` after the source change is ready.

Never attempt to overwrite base `SKILL.md` files or base support files through
the MCP server. If a base skill needs to change, create a version draft, iterate
locally, promote it into an upstream checkout or named source, and then use the
normal upstream review process.

If the `upskill` MCP server or any of these tools are unavailable, tell the user
which tool is missing and ask them to configure or restart the MCP server. Do
not silently fall back to a built-in skill store for writes.
