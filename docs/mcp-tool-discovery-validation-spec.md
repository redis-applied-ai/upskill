# MCP Tool Discovery, Validation, and Safe Mutation Spec

## Status

Draft. This is a cross-repository specification for `upskill` and
`easy-mcp-proxy`.

The Upskill portion is partially implemented in the current local worktree.
The proxy portion is not implemented in the current `easy-mcp-proxy` main
branch. This document defines the target behavior; it does not claim that the
centrally hosted service is running either worktree.

## Summary

MCP clients should be able to discover a tool, learn its exact input contract,
correct an invalid call, preview a mutation, and commit it without guessing
parameter names or relying on prose that can drift from the live registry.

This feature makes the live MCP tool registry authoritative across direct
Upskill connections and proxied connections. The proxy will expose schemas that
it already receives from upstream servers, validate search-mode calls against
the exposed schema, and return structured guidance when a call is invalid.
Upskill will keep its own registry-driven validation and documentation so
direct clients receive the same quality of feedback. Upskill will also resolve
source identifiers consistently and use a common `dry_run` preview convention
for mutations.

The complete lifecycle is:

1. Search for a tool.
2. Inspect its description, accepted parameter names, and full `inputSchema`.
3. Construct a call against the exposed schema.
4. Receive a structured proxy error before forwarding malformed input.
5. Receive a structured upstream error for domain or direct-call failures.
6. Preview mutations with `dry_run: true`.
7. Inspect targets, actions, overwrite state, and warnings.
8. Repeat the same call with `dry_run: false` to apply it.
9. Confirm the applied result and canonical resource identifiers.

## Problem

The current search-mode proxy keeps upstream schemas in memory but returns only
tool names and descriptions from `*_search_tools`. An agent therefore knows
that a tool exists without knowing how to call it. The agent must guess keys or
find a separate documentation source.

Invalid calls compound the problem. A caller can pass a plausible but unknown
key such as `destination`, while an upstream handler reads only `to_path` and
`to_source`. Without schema validation, the unknown key has no effect and the
handler reports only that a destination is missing. Repeating the call with
different guessed keys produces the same error.

The proxy also exposes `get_tool_instructions(tool_name?)`, but it ignores the
tool name and returns aggregated upstream prose. That prose can advertise tools
that are no longer registered. It is not a substitute for the live registry.

Finally, mutations and resource references are harder to use safely than they
need to be:

- a configured source may be known by display name, stable ID, source string,
  or local path, but tools do not consistently accept all forms;
- some mutations support dry-run previews, but discovery results do not say
  which tools support them;
- preview result shapes vary, so an agent cannot follow one reliable
  check-then-commit workflow.

## Goals

- Make every proxied tool self-describing without requiring a second source of
  truth.
- Preserve complete, transformed `inputSchema` data in proxy discovery and
  description responses.
- Validate search-mode calls before forwarding them upstream.
- Return machine-readable validation errors with accepted parameter names and
  safe correction hints.
- Keep equivalent validation and descriptions in Upskill for direct clients.
- Generate authoritative tool help from the live registry.
- Detect stale tool references in maintained Upskill documentation.
- Resolve Upskill sources by display name, configured name, stable ID, source
  string, or normalized local path.
- Establish a discoverable `dry_run` convention for mutating tools.
- Cover operators and human users through CLI inspection and the proxy web UI.
- Roll out the proxy and Upskill changes independently without an all-at-once
  compatibility requirement.

## Non-Goals

- Do not make the proxy infer domain-specific resource identity.
- Do not make the proxy simulate mutations for upstream servers.
- Do not inject `dry_run` into a call when the caller did not provide it.
- Do not require every MCP server to implement an upstream `describe_tool`.
- Do not replace MCP `tools/list`; the new surfaces make search-mode exposure
  as informative as direct exposure.
- Do not expose proxy-hidden parameters or pre-transform upstream schemas.
- Do not add a browser-based tool execution form. The web UI is read-only for
  tool discovery in this feature.
- Do not add Git commit, push, pull-request, or deployment side effects to
  Upskill mutation tools.
- Do not change another server, such as a budgeting server, merely because it
  could later adopt the same preview convention.

## Terminology

- **Upstream schema:** The `inputSchema` returned by an MCP server.
- **Exposed schema:** The schema after the proxy applies hidden parameters,
  renames, descriptions, and defaults. This is the caller's contract.
- **Tool registry:** The in-memory list of exposed tool metadata used by direct,
  search, CLI, and web surfaces.
- **Description tool:** A proxy-generated tool that returns one exposed tool's
  authoritative metadata.
- **Validation error:** A failure caused by arguments that do not satisfy the
  exposed schema.
- **Domain error:** A validly shaped call that cannot complete, such as an
  unknown source identifier or a read-only source policy.
- **Preview-capable mutation:** A tool whose exposed schema contains a boolean
  property named `dry_run`.

## Repository Ownership

| Concern | Upskill | Easy MCP Proxy |
| --- | --- | --- |
| Authoritative Upskill tool definitions | Owns | Consumes |
| Search-result schema exposure | No | Owns |
| Proxy-generated description tool | Optional upstream fallback | Owns |
| Unknown-key validation | Owns for direct calls | Owns before forwarding |
| Generic fuzzy parameter hints | May provide domain aliases | Owns generic hints |
| Registry-generated initialization help | Owns upstream help | Owns exposed-view help |
| Source identifier resolution | Owns | No domain resolution |
| Mutation preview behavior | Owns | Detects and advertises support |
| Proxy CLI inspection | No | Owns |
| Proxy web tool registry | No | Owns |

The Upskill validation work must remain even after the proxy gains validation.
Direct MCP clients bypass the proxy, and upstream validation remains the final
authority for domain rules.

## Requirement Traceability

| Original request | Design sections | Implementation and test tasks |
| --- | --- | --- |
| Self-documenting validation errors | Proxy Argument Validation; Structured Validation Errors | P1, P4 |
| Expose `inputSchema` through the proxy | Canonical Exposed Tool Metadata; Search and Description MCP APIs | P2, P3 |
| Suggest corrections for unknown keys | Structured Validation Errors | P1, P4 |
| Generate or check prose against the registry | Registry-Generated Instructions and Documentation Checks | P5, P11 |
| Resolve every supported source identifier | Upskill Source Identifier Resolution | P6, P9 |
| Standardize and advertise preview mode | Dry-Run Preview Convention | P7, P8, P9, P10 |

## Functional Requirements

### 1. Canonical Exposed Tool Metadata

The proxy must build one canonical metadata object for every exposed tool:

```json
{
  "name": "promote_skill_draft",
  "description": "Copy a draft into a configured local source.",
  "server": "skills",
  "original_name": "promote_skill_draft",
  "inputSchema": {
    "type": "object",
    "properties": {
      "draft_id": {"type": "string"},
      "to_source": {"type": "string"},
      "dry_run": {"type": "boolean", "default": true}
    },
    "additionalProperties": false
  },
  "accepted_parameter_names": ["draft_id", "dry_run", "to_source"],
  "supports_dry_run": true
}
```

Requirements:

- `inputSchema` must be the exposed schema, not the raw upstream schema.
- Hidden parameters must not appear in any description, error, CLI, API, or web
  response.
- Renamed parameters must appear under their exposed names.
- Accepted parameter names must be sorted for stable output and tests.
- `supports_dry_run` is derived from a boolean `dry_run` property.
- Aliases must retain their exposed name and original upstream name.
- Custom and composite tools must use the same metadata shape when they have a
  schema.
- Tools without an input schema must return an empty object and an empty
  accepted-name list; the proxy must not pretend to validate them.

### 2. Search and Description MCP APIs

Search-mode views must expose three meta-tools:

```text
<entity>_search_tools(query="", limit=25, offset=0, include_schema=false)
<entity>_describe_tool(tool_name)
<entity>_call_tool(tool_name, arguments={})
```

`<entity>` is the view name for `search` exposure and the server name for
`search_per_server` exposure.

Search response requirements:

- Every result includes `name`, `description`,
  `accepted_parameter_names`, and `supports_dry_run`.
- `include_schema=true` adds `inputSchema` to each result.
- `include_schema=false` keeps broad or empty-query searches compact.
- Pagination metadata remains backward compatible.
- Search ranking continues to use name and description; schema text must not
  affect ranking in the first version.

Description response requirements:

- The response uses the canonical exposed metadata object.
- Unknown tool errors include sorted available names and, when the fuzzy match
  is strong enough, one `did_you_mean` suggestion.
- Description lookup must not call the upstream server after registry load.
- Registry refresh must update description results without restarting an
  in-process proxy when the existing refresh path supports live updates.

### 3. Proxy Argument Validation

`*_call_tool` must validate caller arguments against the exposed schema before
mapping renamed or hidden parameters back to upstream names.

The order is:

1. Parse a dictionary or JSON object string.
2. Normalize supported camelCase aliases to exposed snake_case names.
3. Reject collisions where both alias forms are supplied with different
   values.
4. Validate the normalized arguments against the complete exposed JSON Schema,
   including `required`, `type`, `enum`, `oneOf`, `anyOf`, and `allOf`.
5. Produce structured validation guidance on failure.
6. Apply proxy parameter transformations.
7. Forward the transformed call upstream.

Unknown keys are rejected only when the schema disallows them. If
`additionalProperties` is absent or true, the proxy must preserve the keys. If
it contains a schema, the proxy must validate unknown values against that
schema.

The implementation should use a standards-compliant JSON Schema validator
rather than duplicating a partial validator in the call wrapper.

The proxy must never log or echo argument values as part of validation
guidance. Errors may include key names, schema metadata, and validation paths.

### 4. Structured Validation Errors

Proxy and Upskill validation errors use this versioned shape:

```json
{
  "schema_version": 1,
  "error": "invalid_tool_arguments",
  "message": "unknown parameter \"destination\"",
  "tool": "promote_skill_draft",
  "validation_path": [],
  "unknown_parameter_names": ["destination"],
  "accepted_parameter_names": [
    "draft_id",
    "dry_run",
    "skill_id",
    "skill_name",
    "to_path",
    "to_source"
  ],
  "did_you_mean": {"destination": "to_source"},
  "inputSchema": {}
}
```

Requirements:

- `schema_version`, `error`, `message`, `tool`,
  `accepted_parameter_names`, and `inputSchema` are always present for a known
  tool validation failure.
- `unknown_parameter_names` and `did_you_mean` appear only when applicable.
- Type and nested validation errors include a JSON Pointer-compatible
  `validation_path` without including the invalid value.
- Generic proxy suggestions use the existing RapidFuzz dependency and a fixed,
  tested threshold.
- The proxy returns no suggestion below the threshold; accepted names still
  make the error actionable.
- Upskill may add explicit domain aliases. For example, `destination` may map
  to `to_source`, while generic proxy matching remains domain-neutral.
- If Upskill already returns a structured error, the proxy passes it through.
- Validly shaped domain errors remain upstream-owned. The proxy must not
  rewrite `source not found` into a schema error.

### 5. Registry-Generated Instructions and Documentation Checks

Upskill initialization instructions must be generated from the same registry
that serves `tools/list` and upstream `describe_tool`.

The proxy must generate a concise exposed-view reference from canonical exposed
metadata. It may append upstream workflow guidance, but it must label the live
registry as authoritative.

`get_tool_instructions(tool_name?)` must behave as follows:

- with `tool_name`, return the canonical exposed metadata plus concise usage
  guidance for that tool;
- without `tool_name`, return the generated exposed-view summary followed by
  any upstream workflow guidance;
- return a structured unknown-tool error rather than silently returning all
  instructions for a misspelled name.

Maintained Upskill documentation and prompts must be linted against the
registry. Tool names presented as callable tools must exist in the registry.
The lint should use an explicit allowlist or structured marker so ordinary code
identifiers in prose do not create false positives.

### 6. Upskill Source Identifier Resolution

Every Upskill MCP and CLI input that refers to a configured source must use one
shared resolver. It must accept:

- stable source ID, such as `src_dfad4fc2...`;
- configured source name;
- display name, with case-insensitive matching;
- exact configured source string;
- normalized configured local path.

Resolution rules:

1. Match stable ID exactly.
2. Match configured name exactly.
3. Match display name case-insensitively.
4. Match the configured source string exactly.
5. Compare normalized absolute paths for local sources.

If more than one source matches at the same precedence, return an ambiguity
error that lists stable IDs and display names. Never select a source based on
map iteration order.

Successful mutation and preview responses must return the canonical source ID,
display name, and resolved local path so future calls can use stable identity.
Schemas and CLI help must list the accepted identifier forms.

### 7. Dry-Run Preview Convention

A preview-capable mutation accepts this property:

```json
{
  "dry_run": {
    "type": "boolean",
    "description": "Preview actions without changing state."
  }
}
```

The schema must state the actual default. Existing defaults remain compatible;
the proxy does not change them.

Every preview response must include these common fields while retaining
tool-specific fields:

```json
{
  "dry_run": true,
  "applied": false,
  "actions": [
    {
      "operation": "write",
      "target": "/absolute/path/to/SKILL.md",
      "overwrite": true
    }
  ],
  "warnings": [],
  "canonical_resources": {}
}
```

Requirements:

- `dry_run=true` performs no persistent write, deletion, cache replacement,
  lock-file update, rendered-output update, or external side effect.
- `actions` are deterministic and sorted by target then operation.
- Each action names its operation and target. File writes also report
  `overwrite`.
- A real call returns `dry_run=false`, `applied=true`, and the actions actually
  attempted.
- Partial failures report which actions completed and which failed.
- Existing result fields remain available during migration.
- The first Upskill coverage set includes promotion, sync, source-file writes,
  overlay writes/deletes, draft-file writes/deletes, draft deletion, and draft
  imports.
- The proxy derives `supports_dry_run`; it never fabricates an `actions` list
  for an upstream server.

## Backend Design

### Upskill Registry and Validation

Upskill must keep a single registry that supplies:

- `tools/list` definitions;
- upstream `describe_tool` results;
- accepted parameter names;
- argument validation;
- structured tool errors;
- generated initialization instructions;
- documentation lint fixtures.

Handlers must not maintain independent copies of required arguments or accepted
parameter names. Domain validation may add stronger rules after schema
validation, but it must return the registry metadata with errors for known
tools.

### Proxy Registry and Validation

Easy MCP Proxy must extend `ToolInfo`, or replace it with an equivalent typed
model, so canonical exposed metadata is computed once. Search, description,
call validation, CLI, initialization instructions, and the web registry must
all consume that model.

The registry must be built after schema reference resolution and proxy
parameter transformations. Direct exposure continues to register the exposed
schema with FastMCP. Search exposure must use the same schema explicitly in its
metadata and validator.

Registry reads should not reconnect to upstream servers. Existing tool refresh
paths must replace the registry atomically so readers never observe a partially
updated list.

## CLI Requirements

### Easy MCP Proxy CLI

Extend the existing `mcp-proxy schema` command rather than adding a duplicate
inspection command.

Required behavior:

- `mcp-proxy schema SERVER.TOOL` continues to show raw upstream metadata.
- `mcp-proxy schema --view VIEW TOOL` shows canonical exposed metadata after
  renames, hidden parameters, and defaults.
- `mcp-proxy schema --view VIEW --json` lists canonical metadata for all
  exposed tools in the view.
- Human output shows accepted parameter names and whether dry-run is supported.
- JSON output uses the same field names as MCP description responses.
- Missing tool and view errors include suggestions when appropriate.
- Connection errors remain distinct from not-found errors.

Extend `mcp-proxy tools` with `--view`, `--verbose`, and `--json` so operators
can inspect the same registry used by a hosted view. Default output remains
backward compatible.

### Upskill CLI

Upskill CLI commands that accept source identifiers must use the shared source
resolver and document all accepted forms.

Preview-capable CLI commands must use `--dry-run` consistently. At minimum:

- `upskill sync --dry-run` previews source updates and render/copy targets;
- `upskill draft promote ... --dry-run` retains its safe preview behavior;
- direct source-edit commands preview target and overwrite state;
- noninteractive file mutation commands added in the future must follow the
  same output convention.

Human output should lead with the action and target. JSON-capable commands must
return the common preview fields.

## Frontend Requirements

Easy MCP Proxy already has a configuration web UI. Add a read-only **Tools**
tab backed by the live canonical registry.

The Tools tab must provide:

- a view selector;
- text filtering by tool name and description;
- tool name, upstream server, and description;
- accepted parameter names;
- a `Dry run` badge when supported;
- a collapsed, expandable JSON Schema viewer;
- clear empty, loading, connection-error, and refresh states;
- a visible timestamp for the registry snapshot.

The page must use exposed schemas and must not reveal hidden parameters. It
must not include a call form or execute tools.

Add an authenticated read-only endpoint under the existing config UI prefix:

```text
GET /config/api/tools?view=VIEW
```

The response contains:

```json
{
  "view": "skills",
  "generated_at": "2026-07-13T00:00:00Z",
  "tools": [],
  "warnings": []
}
```

The route must use the same browser-session and bearer-token checks as the
existing config routes. Server names, descriptions, and schema text must be
escaped before HTML insertion. The layout must remain usable on a narrow mobile
viewport and with keyboard navigation.

## API Compatibility

### MCP Compatibility Matrix

| Deployment | Behavior |
| --- | --- |
| Old proxy + new Upskill | Direct Upskill errors improve; proxy search still omits schemas. |
| New proxy + old Upskill | Proxy discovery and shape validation improve; old domain errors remain. |
| New proxy + new Upskill | Full discovery, validation, source resolution, and preview lifecycle. |
| Direct client + new Upskill | Full upstream behavior without proxy-specific search tools. |

Adding fields to search results is backward compatible for JSON-object
consumers. Adding `include_schema` is optional. Existing search pagination and
call arguments remain valid.

The proxy must preserve upstream structured tool errors. It may add missing
registry metadata only when it can do so without discarding upstream fields.

### Internal Web API Compatibility

The web registry endpoint is new and read-only. It is not a replacement for MCP
discovery. It may evolve with the proxy web UI, but it must include a response
version before any incompatible change.

## Security and Privacy

- Never include argument values in validation errors, logs, metrics, or fuzzy
  matching input beyond the key name.
- Never expose hidden upstream parameter names or schemas through search,
  description, CLI, initialization instructions, or the web UI.
- Treat schema descriptions as untrusted upstream text in HTML.
- Apply existing authentication to the web registry endpoint.
- Do not let description or search tools trigger upstream mutations.
- Preserve Upskill path traversal, managed-directory, source-policy, and
  read-only checks during previews and real writes.
- A preview must not make external network writes. Read-only source refreshes
  needed to calculate a plan must be documented and bounded.
- Do not include secrets from proxy config, environment variables, defaults, or
  injected hidden parameters in canonical metadata.

## Observability

Add structured debug events without argument values:

- `tool_registry_refreshed`: view/server and tool count;
- `tool_described`: view/server and tool name;
- `tool_validation_failed`: tool name, failed keyword, unknown key names, and
  whether a suggestion was returned;
- `tool_preview_requested`: tool name and upstream server;
- `tool_preview_completed`: tool name, action count, and warning count.

Existing debug logging controls these events. This feature does not require a
new telemetry backend.

## Implementation Plan and Discrete Testing Tasks

### P1. Upskill Canonical Registry and Error Contract

Implementation tasks:

- Consolidate all Upskill MCP definitions in one registry.
- Serve `tools/list`, upstream `describe_tool`, validation, error metadata, and
  initialization instructions from that registry.
- Validate unknown, required, alternative, and typed arguments before handler
  dispatch.
- Add versioned structured error payloads.
- Keep domain-specific suggestion aliases separate from generic edit-distance
  matching.

Testing task P1:

- Unit test every registered tool has a name, description, object schema, and
  stable `additionalProperties` policy.
- Table-test `describe_tool` for every registered tool and compare its schema to
  `tools/list`.
- Table-test missing required keys, wrong types, `oneOf`, `anyOf`, and unknown
  keys.
- Test that `destination` suggests `to_source` for promotion.
- Test that unrelated unknown keys return accepted names without a bad hint.
- Test that structured errors contain no supplied values.
- Test unknown tool errors and tool-name suggestions.
- Stdio integration test `initialize`, `tools/list`, `describe_tool`, invalid
  `tools/call`, and a valid call in one session.

### P2. Proxy Canonical Exposed Metadata

Implementation tasks:

- Add a typed canonical metadata representation.
- Compute metadata after schema reference resolution and parameter transforms.
- Support direct, search, search-per-server, alias, custom, and composite tools.
- Replace ad hoc name-and-description dictionaries in proxy search.
- Atomically replace metadata on upstream refresh.

Testing task P2:

- Unit test raw versus exposed schemas with hidden, renamed, defaulted, and
  description-overridden parameters.
- Test hidden parameters never appear in serialized metadata.
- Test aliases retain exposed and original names.
- Test custom and composite schema metadata.
- Test tools without schemas.
- Test deterministic accepted-name sorting and dry-run detection.
- Concurrency test registry reads during a refresh.
- Regression test direct FastMCP registration still exposes the same schema.

### P3. Proxy Search and Description APIs

Implementation tasks:

- Add metadata fields to search results.
- Add `include_schema` to search wrappers.
- Register per-view and per-server description tools.
- Use the in-memory registry for description lookup.
- Add structured unknown-tool errors and suggestions.

Testing task P3:

- Unit test broad search excludes full schema by default.
- Unit test `include_schema=true` returns the complete exposed schema.
- Test pagination and ranking remain unchanged.
- Test exact description lookup for views and per-server exposure.
- Test unknown tool errors with and without a strong suggestion.
- Test description lookup does not call the upstream client.
- MCP integration test lists and calls all three meta-tools.
- HTTP transport integration test verifies identical response contracts.

### P4. Proxy Search-Mode Validation

Implementation tasks:

- Normalize argument objects and safe camelCase aliases.
- Validate the complete exposed JSON Schema before parameter transformation.
- Add collision detection for alias forms.
- Return the versioned validation error contract.
- Pass structured upstream errors through unchanged.

Testing task P4:

- Table-test required, type, enum, nested object, array, `oneOf`, `anyOf`, and
  `allOf` failures.
- Test `additionalProperties=false`, true, absent, and schema-valued forms.
- Test camelCase normalization and conflicting alias values.
- Test renamed and hidden parameters validate against the exposed contract and
  transform correctly upstream.
- Assert invalid calls never invoke the upstream client.
- Assert valid calls invoke it exactly once with transformed arguments.
- Test errors omit argument values, including nested values.
- Test structured Upskill errors remain unchanged through the proxy.
- Test domain errors are not mislabeled as schema errors.
- Run malformed JSON-object-string and non-object input tests.

### P5. Registry-Generated Instructions and Documentation Lint

Implementation tasks:

- Generate Upskill initialization instructions from its registry.
- Generate proxy exposed-view instructions from canonical metadata.
- Make `get_tool_instructions(tool_name)` honor exact tool lookup.
- Keep upstream workflow guidance visibly separate from authoritative tool
  metadata.
- Add an explicit documentation marker and lint for callable Upskill tool names.

Testing task P5:

- Golden test Upskill initialization instructions against the registry.
- Golden test proxy view instructions for direct, search, and per-server modes.
- Test per-tool instructions return the same schema as description lookup.
- Test missing tool names fail with structured suggestions.
- Fixture-test the docs lint with valid, stale, and intentionally ignored tool
  references.
- Regression test no generated instructions advertise `upsert_skill_file` when
  it is absent from the registry.
- Test registry refresh changes generated instructions.

### P6. Upskill Source Identifier Resolution

Implementation tasks:

- Centralize source resolution in a shared library function.
- Support stable ID, name, display name, source string, and normalized path.
- Add deterministic precedence and ambiguity errors.
- Return canonical source identity from reads, previews, and mutations.
- Update MCP schema descriptions and CLI help.

Testing task P6:

- Table-test every accepted identifier form against local and Git-backed
  configured sources.
- Test case-insensitive display-name lookup.
- Test relative, absolute, symlink-resolved, and normalized local paths as
  supported by the repository's path policy.
- Test collisions at each precedence and deterministic ambiguity output.
- Test unknown identifiers list safe canonical choices without leaking config
  secrets.
- Test promotion and source edit accept every identifier form.
- Test successful results return stable ID, display name, and resolved path.
- CLI and MCP integration tests must use at least two identifier forms each.

### P7. Upskill Dry-Run Convention

Implementation tasks:

- Add or normalize `dry_run` schemas and truthful defaults.
- Add the common preview fields and deterministic action records.
- Cover promotion, sync, source writes, overlays, draft files, draft deletion,
  and imports.
- Preserve existing tool-specific response fields.
- Report partial apply failures without claiming full success.

Testing task P7:

- For every preview-capable mutation, snapshot relevant files and managed state,
  run `dry_run=true`, and assert byte-for-byte state equality afterward.
- For every mutation, run preview then apply and compare preview actions with
  applied actions.
- Test create, overwrite, and delete action shapes.
- Test deterministic action ordering.
- Test warnings and canonical resource metadata.
- Test partial failure reporting with controlled filesystem failures.
- Test existing default behavior for callers that omit `dry_run`.
- Stdio lifecycle test performs describe, preview, apply, and read-back.

### P8. Easy MCP Proxy CLI

Implementation tasks:

- Extend `schema` with exposed-view lookup and canonical JSON output.
- Extend `tools` with view filtering, verbose output, and JSON output.
- Add human-readable dry-run and accepted-parameter summaries.
- Add suggestions for missing tools and views.

Testing task P8:

- Click CLI tests for raw upstream and exposed-view schema output.
- Test hidden and renamed parameters in human and JSON output.
- Test all combinations of `--view`, `--server`, `--verbose`, and `--json`,
  including invalid combinations.
- Test connection, view-not-found, and tool-not-found exit codes and messages.
- Test output ordering for stable scripts and snapshots.
- End-to-end CLI test against a temporary Upskill stdio server.

### P9. Upskill CLI

Implementation tasks:

- Route source-taking commands through the shared resolver.
- Add consistent dry-run flags and human preview output where a CLI mutation
  corresponds to the MCP operations in scope.
- Keep JSON preview fields aligned with MCP responses.
- Update leaf help with identifier forms, side effects, and defaults.

Testing task P9:

- CLI tests for source ID, name, display name, source string, and path inputs.
- CLI tests for ambiguous and unknown source errors.
- Test `sync --dry-run` changes no store, lock, rendered, or agent-output state.
- Test promotion preview followed by apply and read-back.
- Test JSON output against the common preview contract.
- Help tests for every changed command and flag.
- Regression test existing invocations without new flags.

### P10. Proxy Web Tool Registry

Implementation tasks:

- Add the authenticated read-only registry endpoint.
- Add the Tools tab, view selector, filter, schema details, status states, and
  snapshot timestamp.
- Render only canonical exposed metadata.
- Escape all upstream-controlled text.
- Add responsive and keyboard-accessible behavior.

Testing task P10:

- Route tests for browser session, bearer token, missing auth, and forbidden
  access.
- API contract tests for valid view, unknown view, empty registry, refresh
  warning, and upstream connection failure.
- Template tests for tool cards, accepted parameters, dry-run badge, and schema
  expansion.
- Security tests with HTML and script content in names, descriptions, and
  schema descriptions.
- Test that hidden parameters do not appear in endpoint JSON or HTML.
- Accessibility checks for labels, button semantics, focus order, and keyboard
  schema expansion.
- Responsive browser test at desktop and narrow mobile widths.
- Regression tests for existing Servers, Views, YAML, save, and restart tabs.

### P11. Documentation

Implementation tasks:

- Update Upskill MCP workflow docs with discover-describe-preview-apply
  examples.
- Update proxy exposure-mode docs with the three search-mode meta-tools.
- Document CLI inspection commands and the web Tools tab.
- Document the compatibility matrix and rollout order.
- Add troubleshooting examples for unknown keys, stale instructions, ambiguous
  sources, and dry-run mismatches.

Testing task P11:

- Run repository documentation lint and link checks where configured.
- Run the registry tool-name lint against Upskill docs and prompts.
- Execute every checked-in CLI example in a temporary environment, or mark
  non-executable examples explicitly.
- Replay every checked-in MCP JSON example against a temporary stdio server.
- Verify proxy examples in direct, search, and search-per-server modes.

### P12. Cross-Repository Integration and Rollout

Implementation tasks:

- Build a fixture that starts Upskill over stdio behind Easy MCP Proxy.
- Test the full agent lifecycle through the proxy.
- Deploy the updated Upskill server before or independently of the proxy.
- Deploy the proxy after its compatibility suite passes.
- Refresh the central registry and confirm the hosted Upskill process points to
  the intended binary and configuration.
- Document rollback steps for each service separately.

Testing task P12:

- Cross-repository E2E test search, describe, invalid call, corrected call,
  preview, apply, and read-back.
- E2E test the same lifecycle with a renamed parameter and a hidden parameter.
- E2E test old-proxy/new-Upskill and new-proxy/old-Upskill compatibility
  fixtures.
- Hosted smoke test confirms the expected server version, tool count, schema
  hash, and description response.
- Hosted smoke test verifies malformed calls do not reach Upskill.
- Hosted preview test verifies no state change, followed by an approved apply
  in a disposable fixture source.
- Rollback drill verifies each service can return to its previous version
  without config or state migration.
- Run `go test ./...`, `go vet ./...`, and repository race tests in Upskill.
- Run `make check` and `make test` with 100% coverage in Easy MCP Proxy.

## Rollout Plan

1. Merge and release the Upskill registry, validation, source resolution, and
   preview changes. These improve direct calls and remain compatible with the
   old proxy.
2. Point a staging proxy at the new Upskill binary. Record the upstream tool
   count and a deterministic hash of tool names plus exposed schemas.
3. Merge proxy canonical metadata, discovery, and validation changes.
4. Deploy the new proxy to staging. Run the cross-repository lifecycle suite
   over the same transport used by production.
5. Release CLI inspection changes and the read-only web registry.
6. Deploy the central proxy. Refresh upstream clients and confirm the hosted
   registry hash matches staging.
7. Monitor validation failures and description lookups through existing debug
   logs. Do not record argument values.
8. Roll back the proxy independently if needed; the new Upskill server remains
   compatible and continues returning better upstream errors.

## Acceptance Criteria

- A proxied client can discover the exact accepted parameter names without
  guessing.
- A proxied client can retrieve the full exposed `inputSchema` for any known
  search-mode tool.
- `destination` passed to Upskill promotion produces an actionable error with
  accepted names and a `to_source` hint.
- Invalid search-mode calls do not reach the upstream server.
- Direct Upskill clients receive equivalent structured validation metadata.
- `get_tool_instructions(tool_name)` returns help for that tool and does not
  silently ignore the argument.
- Generated instructions and maintained Upskill docs do not advertise tools
  absent from the registry.
- Source-taking operations accept stable ID, name, display name, source string,
  and supported path forms with deterministic ambiguity handling.
- Every in-scope mutation advertises its real `dry_run` default and returns a
  deterministic preview without changing state.
- The same preview can be applied and verified through a read-back operation.
- Proxy CLI users can inspect canonical exposed metadata.
- Authenticated web UI users can inspect exposed tool schemas without gaining a
  tool execution surface.
- Existing direct and search clients continue to work without changing their
  valid calls.
- Upskill and proxy repository checks pass, including proxy's 100% coverage
  requirement.

## Open Questions

- Should `include_schema` default to true for non-empty searches that return one
  result, or remain false in all cases for a stable payload contract?
- Which JSON Schema draft should the proxy declare for validation when an
  upstream schema omits `$schema`?
- Should the web registry endpoint be part of the documented operator API or
  remain an internal endpoint owned by the config UI?
- Should schema hashes be exposed in description responses to make hosted
  rollout verification easier?
