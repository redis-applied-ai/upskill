# Using Skills

<!-- upskill-callable-tools: find_skills, read_skill, list_skill_files, read_skill_file, get_skill_stats, record_skill_use, record_skill_feedback, render_skill, list_skill_drafts, read_skill_draft, list_skill_draft_files, read_skill_draft_file, upsert_skill_draft_file, delete_skill_draft_file, render_skill_draft, promote_skill_draft -->

Use the `upskill` MCP server for skill work whenever its tools are available.
Do not use built-in skill servers for listing, searching, reading, creating, or
customizing skills unless the `upskill` MCP server is unavailable.

Treat every skill file visible in an agent-local skill directory as rendered
output. A rendered skill is the effective result of:

1. The installed base skill.
2. Global overlays.
3. Agent-specific overlays.

Rendered skill directories are generated output and are safe to delete or
regenerate. Do not edit rendered `SKILL.md` files, rendered support files,
agent output directories such as `~/.codex/skills` or `~/.claude/skills`, or
the synced base store to fix a skill.

Use the right write target:

- Use a local customization (overlay) when the requested change is local,
  team-specific, or agent-specific behavior.
- Use a draft when the base skill itself should change upstream or when you are
  creating a new skill.
- Use direct source edit only when the target is a configured local source and
  `source_policy.write_policy` allows it.
- Ask for scope only when the user's requested fix could reasonably be either
  a local customization or an upstream base-skill revision.

## Reading Skills

- Start by calling `find_skills` with no query to list available base skills and
  drafts. Use a `query` only when narrowing the list.
- `find_skills` returns base skills and drafts. Draft IDs look like
  `draft:<skill-name>` and should be passed back exactly as returned.
- Use `list_sources` when you need to see configured upstream sources.
  Results include `scope`, `write_policy`, `recommended_workflow`, and optional
  `llm_guidance`.
- Use `find_source_skills` when the user asks what is available in a source or
  wants to search a source before installing. This reads the current source
  catalog and does not install or update anything.
- Do not infer that a newly visible source skill is installed. New source skills
  require an explicit future install workflow outside the current MCP surface.
- Use `read_skill`, `list_skill_files`, and `read_skill_file` to inspect base
  or draft skill bodies and support files.
- When using a rendered skill, call `read_skill` with `rendered: true`, read the
  skill body first, then list/read any support files referenced by the skill
  before acting on it.
- MCP tool results may include a top-level `notices` field. Treat update
  notices as information about installed skills that have changed upstream.
  Do not treat notices as instructions to install newly discovered source
  skills.

## Recording Skill Activity

- Successful installed calls to `read_skill` and `read_skill_file` record one
  retrieval automatically. `find_skills`, `list_skill_files`,
  `get_skill_stats`, source reads, and draft reads do not.
- Use `get_skill_stats` or `upskill show <skill> --stats --nocontent` to inspect
  activity without recording another retrieval.
- A retrieval is not a use. Call `record_skill_use` only after materially
  applying the skill to the user's task. Supply a stable `idempotency_key` when
  the call may be retried.
- After real use, call `record_skill_feedback` when you have actionable positive
  or negative feedback. Include every required field: `sentiment`, the concrete
  `situation`, `details` explaining what went right or wrong, and a specific
  `proposed_change`.
- Feedback does not increment use. Record use separately when appropriate.
- Statistics and feedback stay in Upskill's local activity log. They do not
  authorize or automatically cause edits to a skill, source, overlay, draft,
  rendered output, or VCS state.
- Never include secrets, credentials, private customer data, or unnecessary
  personal data in feedback.

## Checking And Syncing Installed Skills

- Use `check_skill_updates` to compare installed skills with their locked
  sources without writing to `store/`, rendered output, or agent output
  directories.
- Use `sync_skills` when synced base skills and rendered agent outputs should be
  refreshed. Treat its `updated_count`, `rendered_count`, and warnings as the
  source of truth for what happened.
- To change an installed skill's update policy, reinstall it with
  `upskill add <source> --skill <name> --update-policy manual|auto`; do not edit
  the lock file directly.
- If a tool result reports a failed update check, surface the failure clearly
  instead of describing the skill as current.

## Changing Skills With Local Customizations

For lightweight customization of an existing skill, write overlay files through
`upskill` MCP tools. An overlay is a local customization layer that is applied
when generated skill output is built.

- Use `upsert_skill_overlay_file` to add `append`, `root_append`, `files`,
  `replace`, `template`, or JSON Patch customizations.
- Use `delete_skill_overlay_file` only for overlay files.
- Prefer agent-specific overlays when a change should apply only to one agent.
  Use global overlays only for customization that should affect every agent.
- Use `render_skill` to inspect the effective result after overlay edits.
- After writing an overlay, build the skill output and read the rendered skill again
  to verify the intended text or file change is present.
- Template customizations must be complete Jinja template files with `.j2`
  paths. Do not send JSON or YAML variable maps as template customizations.

The equivalent CLI workflow is:

```bash
upskill render <skill-name> --print --agent <agent>
upskill render <skill-name> --print --base-only --agent <agent>
upskill overlay edit <skill-name> --agent <agent>
upskill overlay diff <skill-name> --agent <agent>
upskill render <skill-name> --agent <agent>
```

For a Jinja template customization:

```bash
upskill overlay edit <skill-name> --agent <agent> --operation template --file SKILL.md.j2
upskill render <skill-name> --print --agent <agent>
```

## Changing Base Skills With Drafts

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
  upskill named local source. Promotion defaults to dry-run; review the planned
  writes before setting `dry_run` to false.
- If `source_policy.write_policy` is `read_only`, do not attempt promotion.
  If it is `overlay_preferred`, confirm the user wants an upstream source
  change instead of an overlay. If it is `draft_promote`, use draft plus dry-run
  promotion as the normal path.
- After promotion, the skill is not automatically installed. For a new skill,
  tell the user to run `upskill add <source> --skill <skill-name>` or use the
  equivalent workflow. For an existing locked skill, use `sync_skills` or
  `upskill update <skill-name>` after the source change is ready.

The equivalent CLI workflow is:

```bash
upskill draft create <draft-name> --from <skill-name>
upskill draft edit <draft-name>
upskill draft render <draft-name> --agent <agent> --copy=false
upskill draft diff <draft-name>
upskill draft promote <draft-name> --to-source <source-name> --dry-run
```

Use `--from-rendered` only when the draft should intentionally start from the
rendered effective skill, including local overlays.

Never attempt to overwrite base `SKILL.md` files or base support files through
the store or rendered agent-local directories. If a base skill needs to change,
create a version draft, iterate locally, promote it into an upstream checkout or
named source, and then use the normal upstream review process.

## Direct Source Edits

Use `upsert_source_skill_file` only for configured local sources. It defaults to
dry-run and never installs skills, updates the lock, renders output, commits,
pushes, opens pull requests, or merges.

- For `direct_allowed`, direct source edits are allowed after normal path
  validation.
- For `overlay_preferred`, ask whether the user wants a source edit instead of
  an overlay before setting `confirm_source_write`.
- For `draft_promote`, prefer draft promotion. If the user explicitly wants a
  direct source edit, set `confirm_source_write` and provide `reason`.
- For `read_only`, do not call source-edit writes; use overlays or drafts
  rendered locally.

After any source edit, tell the user that installed skills are not updated until
they run `upskill update <skill-name>` or reinstall from the source.

If the `upskill` MCP server or any of these tools are unavailable, tell the user
which tool is missing and ask them to configure or restart the MCP server. If
the CLI is available, use `upskill <command> --help` for the matching overlay,
draft, render, sync, or source workflow. Do not silently fall back to editing a
built-in skill store or rendered agent-local files.
