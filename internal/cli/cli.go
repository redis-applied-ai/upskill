package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"upskill/internal/config"
	"upskill/internal/draft"
	"upskill/internal/fsutil"
	"upskill/internal/library"
	"upskill/internal/mcpserver"
	"upskill/internal/overlay"
	"upskill/internal/pathutil"
	"upskill/internal/render"
)

var (
	cliCopyDir      = fsutil.CopyDir
	cliFileManifest = library.FileManifest
	cliOsRemoveAll  = os.RemoveAll
	cliOsStat       = os.Stat
	cliOsWriteFile  = os.WriteFile
	cliCanEdit      = canOpenEditor
	cliEditor       = editorCommand
	cliRunEditor    = runEditorCommand
)

var version = "dev"

const (
	helpAdd = `usage: upskill add <source> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--no-render]

summary:
  Install one or more skills from a configured source, local directory, GitHub
  shorthand, or Git URL.

arguments:
  <source>          Source name, local path, GitHub shorthand, or Git URL.

options:
  --skill NAME      Install one matching skill only. Matches the skill ID or
                    SKILL.md name.
  --ref REF         Git branch, tag, or commit to read.
  --subpath PATH    Relative directory inside the source to scan.
  --agent AGENT     Render output for one configured agent after installing.
  --no-render       Install to Upskill's store and lock only; skip rendering.

notes:
  Without --skill, every discovered SKILL.md directory is installed.
  A raw source is recorded automatically as an unnamed configured source.
  Installed skill IDs use SKILL.md name when present, otherwise the directory
  name, normalized for filesystem use.

examples:
  upskill add Obsidian --skill redis
  upskill add ~/Documents/Notes/Memory/Skills --skill redis
  upskill add owner/repo --subpath skills --skill redis
  upskill add owner/repo --ref v1.2.0 --skill redis --no-render`

	helpUse = `usage: upskill use <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH]

summary:
  Print an installed skill or preview a skill directly from a source.

arguments:
  <source-or-skill> Installed skill ID, configured source name, local path,
                    GitHub shorthand, or Git URL.

options:
  --skill NAME      Select one source skill when reading from a source.
  --ref REF         Git branch, tag, or commit to read.
  --subpath PATH    Relative directory inside the source to scan.
  --agent AGENT     Include the agent-specific overlay chain when reading an
                    installed skill.

notes:
  Reading from a source does not install, update the lock, or render output.
  Reading an installed skill applies overlays for the selected agent.

examples:
  upskill use redis
  upskill use redis --agent codex
  upskill use Obsidian --skill redis
  upskill use owner/repo --subpath skills --skill redis`

	helpList = `usage: upskill list|ls [--json] [--agent AGENT]

summary:
  List installed skills in Upskill's local store.

options:
  --json            Print machine-readable JSON.
  --agent AGENT     Show overlay counts for one agent's overlay chain.

notes:
  This lists installed skills, not every skill available in configured sources.
  Use upskill source skills to inspect a source without installing from it.

examples:
  upskill list
  upskill ls --agent codex
  upskill list --json`

	helpFind = `usage: upskill find|search [query] [--json] [--limit N]

summary:
  Search installed skills in Upskill's local store.

arguments:
  [query]           Optional search text. Omit it to list installed skills.

options:
  --json            Print machine-readable JSON.
  --limit N         Maximum results to return. Default: 10.

notes:
  This searches only installed skills by ID, display name, description, and
  SKILL.md body text. Use upskill source find to search skills that are
  available in one source but not necessarily installed.
  When --limit truncates text output, upskill prints showing N/M skills.

examples:
  upskill find redis
  upskill search redis
  upskill find writing --limit 5
  upskill find --json`

	helpRemove = `usage: upskill remove|rm <skill-name>... [--delete-overlays]

summary:
  Remove installed skills from Upskill's store and lock file.

arguments:
  <skill-name>      One or more installed skill IDs to remove.

options:
  --delete-overlays Also delete local overlays for the removed skills.

notes:
  Removing an installed skill does not remove configured sources or drafts.
  Overlays are preserved by default.

examples:
  upskill rm redis
  upskill remove redis docs --delete-overlays`

	helpUpdate = `usage: upskill update [skill-name...] [--agent AGENT] [--no-render] [--check] [--diff]

summary:
  Refresh installed skills from the source metadata recorded in the lock file.

arguments:
  [skill-name...]   Optional installed skill IDs. Omit to update all locked
                    skills.

options:
  --agent AGENT     Render updated skills for one configured agent.
  --no-render       Update store and lock only; skip rendering.
  --check           Preview which locked skills would change.
  --diff            Preview incoming file changes. Implies --check.

notes:
  update uses locked source metadata; it does not inspect arbitrary configured
  sources unless a skill was installed from them.

examples:
  upskill update
  upskill update redis --agent codex
  upskill update --check
  upskill update redis --diff`

	helpSync = `usage: upskill sync [--agent AGENT]

summary:
  Update all locked skills and render the effective skill output.

options:
  --agent AGENT     Render only for one configured agent.

notes:
  sync is equivalent to updating locked skills, then rendering with overlays.
  It writes installed store updates, lock metadata, rendered output, and agent
  output copies when configured.

examples:
  upskill sync
  upskill sync --agent codex`

	helpInit = `usage: upskill init [name] [--dir DIR]

summary:
  Create a new skill bundle skeleton containing SKILL.md.

arguments:
  [name]            Skill name or ID. Defaults to the target directory name.

options:
  --dir DIR         Directory where the skill bundle should be created.

notes:
  init creates source files only. It does not install the skill into Upskill's
  store or render agent output.

examples:
  upskill init redis-helper
  upskill init --dir ~/Documents/Notes/Memory/Skills/redis-helper`

	helpWorkon = `usage: upskill workon <skill-name> [--from SKILL] [--template NAME] [--no-edit]

summary:
  Create or open a mutable draft for skill work.

arguments:
  <skill-name>      Draft skill ID to create or open.

options:
  --from SKILL      Create a version draft from an installed skill.
  --template NAME   Use a named skeleton template for a new-skill draft.
  --no-edit         Create or open the draft without launching an editor.

notes:
  Existing installed skills default to version drafts. Unknown names create
  new-skill drafts. Drafts are not published until promoted or rendered.

examples:
  upskill workon redis
  upskill workon new-skill --no-edit
  upskill workon redis-v2 --from redis`

	helpSourceList = `usage: upskill source list|ls [--json]

summary:
  List configured sources.

options:
  --json            Print machine-readable JSON including canonical_id, ref,
                    and subpath.

notes:
  This command does not inspect upstream contents. Use source skills for that.
  Unnamed sources are displayed by canonical source string.

examples:
  upskill source list
  upskill source ls --json`

	helpSourceAdd = `usage: upskill source add <source> [--name NAME] [--ref REF] [--subpath PATH]

summary:
  Add, rename, or update a configured source without installing skills.

arguments:
  <source>          Local path, GitHub shorthand, Git URL, or GitHub tree URL.

options:
  --name NAME       Shortcut name for repeated commands.
  --ref REF         Git branch, tag, or commit to read.
  --subpath PATH    Relative directory inside the source to scan.

notes:
  Source identity is source location plus ref plus subpath. Re-adding the same
  identity updates the existing record. Adding the same identity with a new
  --name renames the source.

examples:
  upskill source add ~/Documents/Notes/Memory/Skills --name Obsidian
  upskill source add owner/repo --subpath skills --name team
  upskill source add owner/repo --ref main --subpath skills`

	helpSourceSkills = `usage: upskill source skills <source-or-name> [--json] [--ref REF] [--subpath PATH]

summary:
  Inspect skills currently available in a source without installing them.

arguments:
  <source-or-name>  Configured source name, local path, GitHub shorthand, or Git
                    URL.

options:
  --json            Print machine-readable JSON.
  --ref REF         Git branch, tag, or commit to inspect.
  --subpath PATH    Relative directory inside the source to scan.

notes:
  Output columns are SKILL, DESCRIPTION, and PATH. The SKILL column is the
  installable skill ID; display names are shown only when they differ.
  The command does not write the store, lock, source list, or rendered output.

examples:
  upskill source skills Obsidian
  upskill source skills ~/Documents/Notes/Memory/Skills
  upskill source skills owner/repo --subpath skills`

	helpSourceFind = `usage: upskill source find|search <source-or-name> [query] [--json] [--limit N] [--ref REF] [--subpath PATH]

summary:
  Search skills currently available in one source without installing them.

arguments:
  <source-or-name>  Configured source name, local path, GitHub shorthand, or Git
                    URL.
  [query]           Optional search text. Omit it to list matching source skills
                    up to --limit.

options:
  --json            Print machine-readable JSON.
  --limit N         Maximum results to return. Default: 10.
  --ref REF         Git branch, tag, or commit to inspect.
  --subpath PATH    Relative directory inside the source to scan.

notes:
  This searches the requested live source by skill ID, display name,
  description, SKILL.md body text, and path. It does not write the store, lock,
  source list, or rendered output.
  When --limit truncates text output, upskill prints showing N/M skills.

examples:
  upskill source find Obsidian redis
  upskill source search ~/Documents/Notes/Memory/Skills writing --limit 5
  upskill source find owner/repo cache --subpath skills --json`

	helpSourceRemove = `usage: upskill source remove|rm <source-or-name>

summary:
  Remove a configured source record.

arguments:
  <source-or-name>  Source name or raw source string matching a configured
                    source identity.

notes:
  Removing a source does not remove installed skills, lock entries, overlays,
  drafts, or rendered output.

examples:
  upskill source rm Obsidian
  upskill source remove ~/Documents/Notes/Memory/Skills`

	helpDraftList = `usage: upskill draft list|ls [--json]

summary:
  List mutable draft bundles.

options:
  --json            Print machine-readable JSON.

notes:
  Drafts are local work in progress. Installed skills are listed with upskill
  list.

examples:
  upskill draft list
  upskill draft ls --json`

	helpDraftCreate = `usage: upskill draft create <skill-name> [--from SKILL] [--from-rendered] [--template NAME] [--no-edit]

summary:
  Create a draft bundle for new skill work or for editing an existing skill.

arguments:
  <skill-name>      Draft skill ID to create.

options:
  --from SKILL      Create from an installed base skill.
  --from-rendered   Create from the rendered effective skill.
  --template NAME   Use a named skeleton template for new skill drafts.
  --no-edit         Do not launch an editor after creating the draft.

examples:
  upskill draft create redis-helper
  upskill draft create redis-v2 --from redis
  upskill draft create redis-effective --from redis --from-rendered`

	helpDraftEdit = `usage: upskill draft edit <skill-name> [--file PATH] [--no-edit]

summary:
  Open or prepare a file inside an existing draft.

arguments:
  <skill-name>      Draft skill ID.

options:
  --file PATH       Relative draft file to edit. Default: SKILL.md.
  --no-edit         Create or verify the file without launching an editor.

examples:
  upskill draft edit redis
  upskill draft edit redis --file references/checklist.md`

	helpDraftPath = `usage: upskill draft path <skill-name>

summary:
  Print the local filesystem path for a draft.

arguments:
  <skill-name>      Draft skill ID.

examples:
  upskill draft path redis`

	helpDraftFiles = `usage: upskill draft files <skill-name> [--json]

summary:
  List files in a draft bundle.

arguments:
  <skill-name>      Draft skill ID.

options:
  --json            Print machine-readable JSON.

examples:
  upskill draft files redis
  upskill draft files redis --json`

	helpDraftRead = `usage: upskill draft read <skill-name> [--file PATH]

summary:
  Print a file from a draft bundle.

arguments:
  <skill-name>      Draft skill ID.

options:
  --file PATH       Relative draft file to read. Default: SKILL.md.

examples:
  upskill draft read redis
  upskill draft read redis --file references/checklist.md`

	helpDraftAddFile = `usage: upskill draft add-file <skill-name> <local-path> --path PATH [--no-edit]

summary:
  Copy a local file into a draft bundle.

arguments:
  <skill-name>      Draft skill ID.
  <local-path>      Local file to copy into the draft.

options:
  --path PATH       Required relative destination path inside the draft.
  --no-edit         Copy the file without launching an editor.

examples:
  upskill draft add-file redis ./notes.md --path references/notes.md
  upskill draft add-file redis ./api.md --path references/api.md --no-edit`

	helpDraftImport = `usage:
  upskill draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect] [--no-edit]
  upskill draft import <skill-name> --archive-url URL --sha256 SHA256 [--strip-components N] [--inspect] [--no-edit]
  upskill draft import <skill-name> --file PATH --path DEST [--sha256 SHA256] [--no-edit]
  upskill draft import <skill-name> --file-url URL --path DEST --sha256 SHA256 [--no-edit]

summary:
  Import files into a draft from a local file, remote file, local archive, or
  remote archive.

arguments:
  <skill-name>      Draft skill ID. The draft is created if it does not exist.

options:
  --archive PATH          Local zip or tar archive to import.
  --archive-url URL       Remote archive to download and import.
  --file PATH             Local single file to import.
  --file-url URL          Remote single file to download and import.
  --path DEST             Required destination path for single-file imports.
  --sha256 SHA256         Expected SHA-256 checksum.
  --strip-components N    Remove leading path components from archive entries.
  --inspect               Show what would import without writing files.
  --no-edit               Do not launch an editor after importing.

notes:
  Remote imports require --sha256. Existing files are not overwritten during
  inspection.

examples:
  upskill draft import redis --file ./api.md --path references/api.md
  upskill draft import redis --archive ./bundle.zip --strip-components 1
  upskill draft import redis --file-url https://example.com/api.md --path references/api.md --sha256 HASH`

	helpDraftRender = `usage: upskill draft render <skill-name> [--agent AGENT] [--copy=false] [--apply-overlays]

summary:
  Render a draft into effective skill output.

arguments:
  <skill-name>      Draft skill ID.

options:
  --agent AGENT       Render for one configured agent.
  --copy=false        Render to Upskill's rendered directory without copying to
                      agent output directories.
  --apply-overlays    Apply existing overlays while rendering the draft.

examples:
  upskill draft render redis
  upskill draft render redis --agent codex --copy=false
  upskill draft render redis --apply-overlays`

	helpDraftDiff = `usage: upskill draft diff <skill-name>

summary:
  Show changes between a draft and its parent or skeleton baseline.

arguments:
  <skill-name>      Draft skill ID.

examples:
  upskill draft diff redis`

	helpDraftRebase = `usage: upskill draft rebase <skill-name>

summary:
  Refresh a version draft's parent snapshot from the current installed skill.

arguments:
  <skill-name>      Draft skill ID.

notes:
  Rebase is for version drafts. It does not promote draft changes upstream.

examples:
  upskill draft rebase redis`

	helpDraftPromote = `usage: upskill draft promote <skill-name> (--to PATH | --to-source NAME) [--dry-run]

summary:
  Copy a draft bundle into an upstream local source checkout.

arguments:
  <skill-name>      Draft skill ID.

options:
  --to PATH         Destination directory in a local source checkout.
  --to-source NAME  Configured local source name to promote into.
  --dry-run         Show planned writes without changing files.

notes:
  Promotion writes source files. It does not install or render the promoted
  skill; run upskill add or sync afterward when needed.

examples:
  upskill draft promote redis --to-source Obsidian --dry-run
  upskill draft promote redis --to ~/Documents/Notes/Memory/Skills/redis`

	helpDraftRemove = `usage: upskill draft remove|rm <skill-name>

summary:
  Delete a local draft bundle.

arguments:
  <skill-name>      Draft skill ID.

notes:
  Removing a draft does not remove installed skills, sources, overlays, or
  rendered output.

examples:
  upskill draft rm redis`

	helpOverlayList = `usage: upskill overlay list|ls [--skill NAME] [--agent AGENT] [--json]

summary:
  List local overlay files.

options:
  --skill NAME      Show overlays for one installed skill.
  --agent AGENT     Show overlays for one agent.
  --json            Print machine-readable JSON.

examples:
  upskill overlay list
  upskill overlay ls --skill redis
  upskill overlay list --agent codex --json`

	helpOverlayAdd = `usage: upskill overlay add <skill-name> [--agent AGENT] [--path] [--no-edit]

summary:
  Create an overlay file for an installed skill.

arguments:
  <skill-name>      Installed skill ID.

options:
  --agent AGENT     Create an agent-specific overlay.
  --path            Print the overlay path after creating it.
  --no-edit         Create the overlay without launching an editor.

examples:
  upskill overlay add redis
  upskill overlay add redis --agent codex --path`

	helpOverlayEdit = `usage: upskill overlay edit <skill-name> [--agent AGENT] [--file PATH] [--operation root-append|append|files|replace|template|patch] [--no-edit]

summary:
  Edit an overlay operation file for an installed skill.

arguments:
  <skill-name>      Installed skill ID.

options:
  --agent AGENT       Edit an agent-specific overlay.
  --file PATH         Relative file path for file-scoped operations.
  --operation OP      Overlay operation. Default: root-append.
  --no-edit           Create or verify the overlay file without launching an
                      editor.

notes:
  append adds to a base file, files adds new files, replace replaces files,
  template adds template overrides, patch stores patch files, and root-append
  appends to SKILL.md.

examples:
  upskill overlay edit redis
  upskill overlay edit redis --agent codex
  upskill overlay edit redis --operation files --file references/checklist.md`

	helpOverlayDiff = `usage: upskill overlay diff <skill-name> [--agent AGENT]

summary:
  Show the rendered changes produced by overlays.

arguments:
  <skill-name>      Installed skill ID.

options:
  --agent AGENT     Include overlays for one agent.

examples:
  upskill overlay diff redis
  upskill overlay diff redis --agent codex`

	helpRender = `usage: upskill render [skill-name] [--agent AGENT] [--copy=false] [--print] [--base-only]

summary:
  Render installed skills with overlays into effective skill output.

arguments:
  [skill-name]      Optional installed skill ID. Omit to render all installed
                    skills.

options:
  --agent AGENT     Render for one configured agent.
  --copy=false      Render without copying to agent output directories.
  --print           Print one rendered skill file to stdout.
  --base-only       With --print, print the installed base file without overlays.

notes:
  Rendering writes Upskill's rendered directory. Copying writes configured agent
  output directories.
  If the base skill, overlays, mode, and agent are unchanged, render reuses the
  existing rendered directory.

examples:
  upskill render redis --agent codex
  upskill render --copy=false
  upskill render redis --print --base-only`

	helpMCP = `usage: upskill mcp

summary:
  Run the Upskill MCP server over stdin and stdout.

notes:
  MCP mode is intended for agent integrations. It reads JSON-RPC messages from
  stdin and writes responses to stdout.

examples:
  upskill mcp`

	helpVersion = `usage: upskill version

summary:
  Print the current Upskill version.

notes:
  Version comes from release build metadata when available. Local builds fall
  back to VCS metadata or dev.

examples:
  upskill version`

	helpStatus = `usage: upskill status

summary:
  Show local Upskill state paths and counts.

notes:
  status reads configured store, overlay, and draft state. It does not create
  missing directories; use doctor for that.

examples:
  upskill status`

	helpDoctor = `usage: upskill doctor

summary:
  Check and create Upskill's managed local directories.

notes:
  doctor creates the home, store, overlays, drafts, imports, and rendered
  directories when needed. It does not install or update skills.

examples:
  upskill doctor`
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if printRequestedHelp(args, stdout) {
		return 0
	}

	if args[0] == "version" {
		return runVersion(args[1:], stdout, stderr)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}

	switch args[0] {
	case "add":
		return runAdd(cfg, args[1:], stdout, stderr)
	case "use":
		return runUse(cfg, args[1:], stdout, stderr)
	case "list", "ls":
		return runList(cfg, args[1:], stdout, stderr)
	case "find", "search":
		return runFind(cfg, args[1:], stdout, stderr)
	case "remove", "rm":
		return runRemove(cfg, args[1:], stdout, stderr)
	case "update":
		return runUpdate(cfg, args[1:], stdout, stderr)
	case "sync":
		return runSync(cfg, args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "workon":
		return runWorkon(cfg, args[1:], stdout, stderr)
	case "source":
		return runSource(cfg, args[1:], stdout, stderr)
	case "draft":
		return runDraft(cfg, args[1:], stdout, stderr)
	case "overlay":
		return runOverlay(cfg, args[1:], stdout, stderr)
	case "render":
		return runRender(cfg, args[1:], stdout, stderr)
	case "mcp":
		return runMCP(cfg, stdout, stderr)
	case "status":
		return runStatus(cfg, stdout, stderr)
	case "doctor":
		return runDoctor(cfg, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "upskill: unknown command: %s\n", args[0])
		return 2
	}
}

func runVersion(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "upskill: unexpected argument: %s\n", args[0])
		return 2
	}
	fmt.Fprintf(stdout, "upskill %s\n", currentVersion())
	return 0
}

func currentVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	if revision := buildSetting(info, "vcs.revision"); revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		if buildSetting(info, "vcs.modified") == "true" {
			return revision + "-dirty"
		}
		return revision
	}
	return version
}

func buildSetting(info *debug.BuildInfo, key string) string {
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

func runAdd(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printUsage(stdout, helpAdd)
		return 0
	}
	source := args[0]
	agent, skill, ref, subpath := "", "", "", ""
	noRender := false
	if err := parseFlags(args[1:], map[string]func(string) error{
		"--agent":     func(v string) error { agent = v; return nil },
		"--skill":     func(v string) error { skill = v; return nil },
		"--ref":       func(v string) error { ref = v; return nil },
		"--subpath":   func(v string) error { subpath = v; return nil },
		"--no-render": func(_ string) error { noRender = true; return nil },
	}); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	if _, ok := library.ResolveNamedSource(cfg, source); !ok {
		canonical, key, err := library.CanonicalSource(source, "", ref, subpath)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if cfg.Sources == nil {
			cfg.Sources = map[string]config.Source{}
		}
		canonical, key, _ = upsertSourceRecord(cfg.Sources, canonical)
		cfg.Sources[key] = canonical
		if err := config.SaveSources(cfg, cfg.Sources); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
	}
	installed, err := library.InstallSource(cfg, source, library.InstallOptions{SkillFilter: skill, Ref: ref, Subpath: subpath})
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	printInstalled(stdout, installed, "installed")
	if noRender {
		return 0
	}
	return renderInstalled(cfg, installed, agent, stdout, stderr)
}

func runUse(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printUsage(stdout, helpUse)
		return 0
	}
	input := args[0]
	agent, skill, ref, subpath := "", "", "", ""
	if err := parseFlags(args[1:], map[string]func(string) error{
		"--agent":   func(v string) error { agent = v; return nil },
		"--skill":   func(v string) error { skill = v; return nil },
		"--ref":     func(v string) error { ref = v; return nil },
		"--subpath": func(v string) error { subpath = v; return nil },
	}); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	storeName := library.NormalizeName(input)
	if _, err := os.Stat(filepath.Join(cfg.StoreDir, storeName)); err == nil {
		text, err := (render.Renderer{Config: cfg}).Print(storeName, agent, "SKILL.md", false)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, text)
		return 0
	}
	sourceSkill, err := library.ReadSourceSkill(cfg, input, library.InstallOptions{SkillFilter: skill, Ref: ref, Subpath: subpath})
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, sourceSkill.EntrypointText())
	return 0
}

func runList(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	jsonOut := false
	agent := ""
	if err := parseFlags(args, map[string]func(string) error{
		"--json":  func(_ string) error { jsonOut = true; return nil },
		"--agent": func(v string) error { agent = v; return nil },
	}); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	skills, err := library.ListInstalled(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	if jsonOut {
		data, _ := json.MarshalIndent(skills, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	if len(skills) == 0 {
		fmt.Fprintln(stdout, "no skills installed")
		return 0
	}
	rows := make([][]string, 0, len(skills))
	for _, skill := range skills {
		overlays := overlay.Chain(cfg, skill.StoreName, agent)
		rows = append(rows, []string{skill.StoreName, skill.Description, strconv.Itoa(len(overlays))})
	}
	printTable(stdout, []tableColumn{
		{Header: "SKILL", MaxWidth: 36},
		{Header: "DESCRIPTION", MaxWidth: 64},
		{Header: "OVERLAYS", MaxWidth: 8},
	}, rows)
	return 0
}

func runFind(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	jsonOut := false
	limit := 10
	positionals := []string{}
	if err := parseFlags(args, map[string]func(string) error{
		"--json":  func(_ string) error { jsonOut = true; return nil },
		"--limit": func(v string) error { n, err := strconv.Atoi(v); limit = n; return err },
	}, &positionals); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	query := strings.Join(positionals, " ")
	skills, total, err := library.FindInstalled(cfg, query, limit)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	if jsonOut {
		data, _ := json.MarshalIndent(skills, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	if len(skills) == 0 {
		fmt.Fprintln(stdout, "no matching skills")
		return 0
	}
	printSearchPageSummary(stdout, len(skills), total)
	rows := make([][]string, 0, len(skills))
	for _, skill := range skills {
		rows = append(rows, []string{skill.StoreName, skill.Description})
	}
	printTable(stdout, []tableColumn{
		{Header: "SKILL", MaxWidth: 36},
		{Header: "DESCRIPTION", MaxWidth: 72},
	}, rows)
	return 0
}

func runRemove(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	deleteOverlays := false
	positionals := []string{}
	if err := parseFlags(args, map[string]func(string) error{
		"--delete-overlays": func(_ string) error { deleteOverlays = true; return nil },
	}, &positionals); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	if len(positionals) == 0 {
		fmt.Fprintln(stderr, "upskill: remove requires at least one skill name")
		return 2
	}
	removed, err := library.RemoveSkills(cfg, positionals, deleteOverlays)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	for _, name := range removed {
		fmt.Fprintf(stdout, "removed %s\n", name)
	}
	return 0
}

func runUpdate(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	agent := ""
	noRender := false
	check := false
	diff := false
	positionals := []string{}
	if err := parseFlags(args, map[string]func(string) error{
		"--agent":     func(v string) error { agent = v; return nil },
		"--no-render": func(_ string) error { noRender = true; return nil },
		"--check":     func(_ string) error { check = true; return nil },
		"--diff": func(_ string) error {
			check = true
			diff = true
			return nil
		},
	}, &positionals); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	if check {
		previews, err := library.PreviewUpdates(cfg, positionals, diff)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		printUpdatePreviews(stdout, previews, diff)
		return 0
	}
	updated, err := library.UpdateSkills(cfg, positionals)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	printInstalled(stdout, updated, "updated")
	if noRender {
		return 0
	}
	return renderInstalled(cfg, updated, agent, stdout, stderr)
}

func runSync(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	agent := ""
	if err := parseFlags(args, map[string]func(string) error{
		"--agent": func(v string) error { agent = v; return nil },
		"--with-overlays": func(_ string) error {
			return fmt.Errorf("--with-overlays is not supported; sync always renders overlays")
		},
		"--without-overlays": func(_ string) error {
			return fmt.Errorf("--without-overlays is not supported; sync always renders overlays")
		},
	}); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	updated, err := library.UpdateSkills(cfg, nil)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	printInstalled(stdout, updated, "updated")
	results, err := (render.Renderer{Config: cfg}).Render(render.Options{Agent: agent, Copy: true})
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	printResults(stdout, results)
	return 0
}

func runInit(args []string, stdout io.Writer, stderr io.Writer) int {
	name := ""
	dir := ""
	positionals := []string{}
	if err := parseFlags(args, map[string]func(string) error{
		"--dir": func(v string) error { dir = v; return nil },
	}, &positionals); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	if len(positionals) > 0 {
		name = positionals[0]
	}
	path, err := library.InitSkill(dir, name)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created %s\n", path)
	return 0
}

func runSource(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printSourceHelp(stdout)
		return 0
	}
	switch args[0] {
	case "list", "ls":
		jsonOut := false
		if err := parseFlags(args[1:], map[string]func(string) error{
			"--json": func(_ string) error { jsonOut = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		if jsonOut {
			data, _ := json.MarshalIndent(sourceRows(cfg.Sources), "", "  ")
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		sources := config.SourceRecords(cfg.Sources)
		if len(sources) == 0 {
			fmt.Fprintln(stdout, "no sources")
			return 0
		}
		hasRef := false
		hasSubpath := false
		for _, src := range sources {
			if src.Ref != "" {
				hasRef = true
			}
			if src.Subpath != "" {
				hasSubpath = true
			}
		}
		columns := []tableColumn{
			{Header: "SOURCE", MaxWidth: 48},
			{Header: "LOCATION", MaxWidth: 72},
		}
		if hasRef {
			columns = append(columns, tableColumn{Header: "REF", MaxWidth: 20})
		}
		if hasSubpath {
			columns = append(columns, tableColumn{Header: "SUBPATH", MaxWidth: 36})
		}
		rows := make([][]string, 0, len(sources))
		for _, src := range sources {
			location := src.Source
			if src.Name == "" && config.SourceDisplayName(src) == src.Source {
				location = ""
			}
			row := []string{config.SourceDisplayName(src), location}
			if hasRef {
				row = append(row, src.Ref)
			}
			if hasSubpath {
				row = append(row, src.Subpath)
			}
			rows = append(rows, row)
		}
		printTable(stdout, columns, rows)
		return 0
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: source add requires <source>")
			return 2
		}
		source := args[1]
		name, ref, subpath := "", "", ""
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--name":    func(v string) error { name = v; return nil },
			"--ref":     func(v string) error { ref = v; return nil },
			"--subpath": func(v string) error { subpath = v; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		canonical, _, err := library.CanonicalSource(source, name, ref, subpath)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if cfg.Sources == nil {
			cfg.Sources = map[string]config.Source{}
		}
		canonical, key, updated := upsertSourceRecord(cfg.Sources, canonical)
		cfg.Sources[key] = canonical
		if err := config.SaveSources(cfg, cfg.Sources); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		verb := "added"
		if updated {
			verb = "updated"
		}
		fmt.Fprintf(stdout, "%s source %s\n", verb, config.SourceDisplayName(canonical))
		return 0
	case "skills":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: source skills requires <source-or-name>")
			return 2
		}
		source := args[1]
		jsonOut := false
		ref, subpath := "", ""
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--json":    func(_ string) error { jsonOut = true; return nil },
			"--ref":     func(v string) error { ref = v; return nil },
			"--subpath": func(v string) error { subpath = v; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		skills, err := library.InspectSource(cfg, source, library.InstallOptions{Ref: ref, Subpath: subpath})
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if jsonOut {
			data, _ := json.MarshalIndent(skills, "", "  ")
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		rows := make([][]string, 0, len(skills))
		for _, skill := range skills {
			rows = append(rows, []string{sourceSkillDisplay(skill), skill.Description, skill.SkillPath})
		}
		printTable(stdout, []tableColumn{
			{Header: "SKILL", MaxWidth: 40},
			{Header: "DESCRIPTION", MaxWidth: 58},
			{Header: "PATH", MaxWidth: 44},
		}, rows)
		return 0
	case "find", "search":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "upskill: source %s requires <source-or-name>\n", args[0])
			return 2
		}
		source := args[1]
		jsonOut := false
		limit := 10
		ref, subpath := "", ""
		positionals := []string{}
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--json":    func(_ string) error { jsonOut = true; return nil },
			"--limit":   func(v string) error { n, err := strconv.Atoi(v); limit = n; return err },
			"--ref":     func(v string) error { ref = v; return nil },
			"--subpath": func(v string) error { subpath = v; return nil },
		}, &positionals); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		query := strings.Join(positionals, " ")
		skills, total, err := library.SearchSource(cfg, source, query, library.InstallOptions{Ref: ref, Subpath: subpath}, limit)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if jsonOut {
			data, _ := json.MarshalIndent(skills, "", "  ")
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		if len(skills) == 0 {
			fmt.Fprintln(stdout, "no matching skills")
			return 0
		}
		printSearchPageSummary(stdout, len(skills), total)
		rows := make([][]string, 0, len(skills))
		for _, skill := range skills {
			rows = append(rows, []string{sourceSkillDisplay(skill), skill.Description, skill.SkillPath})
		}
		printTable(stdout, []tableColumn{
			{Header: "SKILL", MaxWidth: 40},
			{Header: "DESCRIPTION", MaxWidth: 58},
			{Header: "PATH", MaxWidth: 44},
		}, rows)
		return 0
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: source remove requires <source-or-name>")
			return 2
		}
		key := args[1]
		if _, ok := cfg.Sources[key]; !ok {
			if canonical, canonicalKey, err := library.CanonicalSource(args[1], "", "", ""); err == nil {
				key = canonicalKey
				if _, ok := cfg.Sources[key]; !ok {
					if existingKey, ok := findSourceRecordByIdentity(cfg.Sources, canonical); ok {
						key = existingKey
					}
				}
			}
		}
		if _, ok := cfg.Sources[key]; !ok {
			fmt.Fprintf(stderr, "upskill: source not found: %s\n", args[1])
			return 1
		}
		delete(cfg.Sources, key)
		if err := config.SaveSources(cfg, cfg.Sources); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "removed source %s\n", args[1])
		return 0
	default:
		fmt.Fprintf(stderr, "upskill: unknown source command: %s\n", args[0])
		return 2
	}
}

func runWorkon(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printUsage(stdout, helpWorkon)
		return 0
	}
	name := args[0]
	from, template := "", ""
	noEdit := false
	if err := parseFlags(args[1:], map[string]func(string) error{
		"--from":     func(v string) error { from = v; return nil },
		"--template": func(v string) error { template = v; return nil },
		"--no-edit":  func(_ string) error { noEdit = true; return nil },
	}); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	item, err := workonDraft(cfg, name, from, false, template)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	printDraft(stdout, item)
	if !noEdit {
		if code := editDraftFile(item, "SKILL.md", stdout, stderr); code != 0 {
			return code
		}
	}
	return 0
}

func runDraft(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printDraftHelp(stdout)
		return 0
	}
	switch args[0] {
	case "list", "ls":
		jsonOut := false
		if err := parseFlags(args[1:], map[string]func(string) error{
			"--json": func(_ string) error { jsonOut = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		items, err := draft.List(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if jsonOut {
			data, _ := json.MarshalIndent(items, "", "  ")
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		if len(items) == 0 {
			fmt.Fprintln(stdout, "no drafts")
			return 0
		}
		rows := make([][]string, 0, len(items))
		for _, item := range items {
			rows = append(rows, []string{item.ID, item.Metadata.Type, item.Path})
		}
		printTable(stdout, []tableColumn{
			{Header: "DRAFT", MaxWidth: 36},
			{Header: "TYPE", MaxWidth: 16},
			{Header: "PATH", MaxWidth: 72},
		}, rows)
		return 0
	case "create":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft create requires <skill-name>")
			return 2
		}
		from, template := "", ""
		fromRendered := false
		noEdit := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--from":          func(v string) error { from = v; return nil },
			"--from-rendered": func(_ string) error { fromRendered = true; return nil },
			"--template":      func(v string) error { template = v; return nil },
			"--no-edit":       func(_ string) error { noEdit = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		item, err := createDraft(cfg, args[1], from, fromRendered, template, true)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		printDraft(stdout, item)
		if !noEdit {
			if code := editDraftFile(item, "SKILL.md", stdout, stderr); code != 0 {
				return code
			}
		}
		return 0
	case "edit":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft edit requires <skill-name>")
			return 2
		}
		file := "SKILL.md"
		noEdit := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--file":    func(v string) error { file = v; return nil },
			"--no-edit": func(_ string) error { noEdit = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		item, err := draft.Read(cfg, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if noEdit {
			if _, err := ensureDraftFile(item, file); err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "ready %s\n", filepath.ToSlash(filepath.Clean(file)))
			return 0
		}
		return editDraftFile(item, file, stdout, stderr)
	case "path":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft path requires <skill-name>")
			return 2
		}
		item, err := draft.Read(cfg, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, item.Path)
		return 0
	case "files":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft files requires <skill-name>")
			return 2
		}
		jsonOut := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--json": func(_ string) error { jsonOut = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		item, err := draft.Read(cfg, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		files, err := cliFileManifest(item.Path, "", true, false, 500)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if jsonOut {
			data, _ := json.MarshalIndent(files, "", "  ")
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		for _, file := range files {
			fmt.Fprintf(stdout, "%s\t%s\n", file["kind"], file["path"])
		}
		return 0
	case "read":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft read requires <skill-name>")
			return 2
		}
		file := "SKILL.md"
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--file": func(v string) error { file = v; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		item, err := draft.Read(cfg, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		path, err := pathutil.SafeJoin(item.Path, file)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, string(data))
		return 0
	case "add-file":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "upskill: draft add-file requires <skill-name> <local-path> --path PATH")
			return 2
		}
		dest := ""
		noEdit := false
		if err := parseFlagsWithValues(args[3:], map[string]func(string) error{
			"--path":    func(v string) error { dest = v; return nil },
			"--no-edit": func(_ string) error { noEdit = true; return nil },
		}, map[string]bool{"--path": true}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		if dest == "" {
			fmt.Fprintln(stderr, "upskill: draft add-file requires --path")
			return 2
		}
		item, err := draft.Read(cfg, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		target, err := pathutil.SafeJoin(item.Path, dest)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if err := fsutil.CopyFile(args[2], target); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "added %s\n", dest)
		if !noEdit {
			if code := editDraftFile(item, dest, stdout, stderr); code != 0 {
				return code
			}
		}
		return 0
	case "import":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft import requires <skill-name>")
			return 2
		}
		opts := draft.ImportOptions{}
		strip := ""
		noEdit := false
		if err := parseFlagsWithValues(args[2:], map[string]func(string) error{
			"--archive":          func(v string) error { opts.ArchivePath = v; return nil },
			"--archive-url":      func(v string) error { opts.ArchiveURL = v; return nil },
			"--file":             func(v string) error { opts.FilePath = v; return nil },
			"--file-url":         func(v string) error { opts.FileURL = v; return nil },
			"--path":             func(v string) error { opts.DestPath = v; return nil },
			"--sha256":           func(v string) error { opts.SHA256 = v; return nil },
			"--strip-components": func(v string) error { strip = v; return nil },
			"--inspect":          func(_ string) error { opts.Inspect = true; return nil },
			"--no-edit":          func(_ string) error { noEdit = true; return nil },
		}, map[string]bool{"--path": true}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		if strip != "" {
			n, err := strconv.Atoi(strip)
			if err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 2
			}
			opts.StripComponents = n
		}
		if !draft.Exists(cfg, args[1]) {
			if _, err := createNewDraft(cfg, library.NormalizeName(args[1]), ""); err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 1
			}
		}
		result, err := draft.Import(cfg, args[1], opts)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(data))
		if !noEdit && result.Imported && canStartEditor(stdout, stderr) {
			item, err := draft.Read(cfg, args[1])
			if err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 1
			}
			if file := importedEditTarget(result); file != "" {
				if code := editDraftFile(item, file, stdout, stderr); code != 0 {
					return code
				}
			}
		}
		return 0
	case "render":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft render requires <skill-name>")
			return 2
		}
		agent := ""
		copyOutput := true
		applyOverlays := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--agent":          func(v string) error { agent = v; return nil },
			"--copy":           func(v string) error { copyOutput = parseBoolDefault(v, true); return nil },
			"--copy=true":      func(_ string) error { copyOutput = true; return nil },
			"--copy=false":     func(_ string) error { copyOutput = false; return nil },
			"--no-copy":        func(_ string) error { copyOutput = false; return nil },
			"--apply-overlays": func(_ string) error { applyOverlays = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		results, err := draft.Render(cfg, args[1], draft.RenderOptions{Agent: agent, Copy: copyOutput, ApplyOverlays: applyOverlays})
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return 0
	case "diff":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft diff requires <skill-name>")
			return 2
		}
		result, err := draft.Diff(cfg, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if len(result.Lines) == 0 {
			fmt.Fprintln(stdout, "no changes")
			return 0
		}
		fmt.Fprintln(stdout, strings.Join(result.Lines, "\n"))
		return 0
	case "rebase":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft rebase requires <skill-name>")
			return 2
		}
		result, err := draft.Rebase(cfg, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return 0
	case "promote":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft promote requires <skill-name>")
			return 2
		}
		toPath := ""
		toSource := ""
		dryRun := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--to":        func(v string) error { toPath = v; return nil },
			"--to-source": func(v string) error { toSource = v; return nil },
			"--dry-run":   func(_ string) error { dryRun = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		if toSource != "" {
			dest, err := library.ResolveLocalSourceDestination(cfg, toSource)
			if err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 1
			}
			toPath = dest
		}
		result, err := draft.Promote(cfg, args[1], draft.PromoteOptions{ToPath: toPath, ToSource: toSource, DryRun: dryRun})
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return 0
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft remove requires <skill-name>")
			return 2
		}
		if err := draft.Remove(cfg, args[1]); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "removed %s\n", draft.MakeID(args[1]))
		return 0
	default:
		fmt.Fprintf(stderr, "upskill: unknown draft command: %s\n", args[0])
		return 2
	}
}

func workonDraft(cfg config.Config, name string, from string, fromRendered bool, template string) (draft.Draft, error) {
	if draft.Exists(cfg, name) {
		return draft.Read(cfg, name)
	}
	return createDraft(cfg, name, from, fromRendered, template, true)
}

func createDraft(cfg config.Config, name string, from string, fromRendered bool, template string, allowImplicitSource bool) (draft.Draft, error) {
	storeName := library.NormalizeName(name)
	if storeName == "" {
		return draft.Draft{}, fmt.Errorf("draft name is required")
	}
	if draft.Exists(cfg, storeName) {
		return draft.Draft{}, fmt.Errorf("draft already exists: %s", draft.MakeID(storeName))
	}
	sourceName := library.NormalizeName(from)
	if sourceName == "" && allowImplicitSource && storeSkillExists(cfg, storeName) {
		sourceName = storeName
	}
	if sourceName != "" || fromRendered {
		if sourceName == "" {
			sourceName = storeName
		}
		return createVersionDraft(cfg, storeName, sourceName, fromRendered)
	}
	return createNewDraft(cfg, storeName, template)
}

func createNewDraft(cfg config.Config, storeName string, template string) (draft.Draft, error) {
	item, err := draft.Save(cfg, draft.Metadata{Name: storeName, Type: draft.TypeNew, CreatedFrom: draft.CreatedFromSkeleton})
	if err != nil {
		return draft.Draft{}, err
	}
	skillPath := filepath.Join(item.Path, "SKILL.md")
	if _, err := cliOsStat(skillPath); err == nil {
		return item, nil
	} else if !os.IsNotExist(err) {
		return draft.Draft{}, err
	}
	displayName := storeName
	if template != "" {
		displayName = fmt.Sprintf("%s (%s)", storeName, template)
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s skill instructions\n---\n\n# %s\n\nAdd skill instructions here.\n", storeName, storeName, displayName)
	if err := cliOsWriteFile(skillPath, []byte(body), 0o644); err != nil {
		return draft.Draft{}, err
	}
	return item, nil
}

func createVersionDraft(cfg config.Config, storeName string, sourceName string, fromRendered bool) (draft.Draft, error) {
	baseDir := filepath.Join(cfg.StoreDir, sourceName)
	info, err := cliOsStat(baseDir)
	if err != nil {
		return draft.Draft{}, err
	}
	if !info.IsDir() {
		return draft.Draft{}, fmt.Errorf("%s is not a directory", baseDir)
	}
	sourceDir := baseDir
	createdFrom := draft.CreatedFromBase
	if fromRendered {
		results, err := (render.Renderer{Config: cfg}).Render(render.Options{Skill: sourceName, Copy: false})
		if err != nil {
			return draft.Draft{}, err
		}
		sourceDir = results[0].RenderedPath
		createdFrom = draft.CreatedFromRendered
	}
	dst := draft.Path(cfg, storeName)
	if err := cliOsRemoveAll(dst); err != nil {
		return draft.Draft{}, err
	}
	if err := cliCopyDir(sourceDir, dst, func(rel string, _ os.DirEntry) bool {
		return rel == render.MarkerFile
	}); err != nil {
		return draft.Draft{}, err
	}
	parentSource, parentRevision, parentHash, err := parentMetadata(cfg, sourceName, baseDir)
	if err != nil {
		return draft.Draft{}, err
	}
	return draft.Save(cfg, draft.Metadata{
		Name:           storeName,
		Type:           draft.TypeVersion,
		ParentSkill:    sourceName,
		ParentSource:   parentSource,
		ParentRevision: parentRevision,
		ParentHash:     parentHash,
		CreatedFrom:    createdFrom,
	})
}

func parentMetadata(cfg config.Config, sourceName string, baseDir string) (string, string, string, error) {
	lock, err := library.ReadLock(cfg)
	if err != nil {
		return "", "", "", err
	}
	if entry, ok := lock.Skills[sourceName]; ok {
		return entry.Source, entry.Ref, entry.Hash, nil
	}
	hash, err := library.HashDir(baseDir)
	if err != nil {
		return "", "", "", err
	}
	return "", "", hash, nil
}

func storeSkillExists(cfg config.Config, name string) bool {
	info, err := cliOsStat(filepath.Join(cfg.StoreDir, library.NormalizeName(name)))
	return err == nil && info.IsDir()
}

func printDraft(w io.Writer, item draft.Draft) {
	fmt.Fprintf(w, "%s\t%s\n", item.ID, item.Metadata.Type)
	if item.Metadata.CreatedFrom == draft.CreatedFromRendered {
		fmt.Fprintln(w, "warning: draft was created from rendered output and may include local overlays")
	}
}

func editDraftFile(item draft.Draft, file string, stdout io.Writer, stderr io.Writer) int {
	path, err := ensureDraftFile(item, file)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	edited, err := maybeEdit(path, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	if edited {
		fmt.Fprintf(stdout, "edited %s\n", filepath.ToSlash(filepath.Clean(file)))
	} else {
		fmt.Fprintf(stdout, "ready %s\n", filepath.ToSlash(filepath.Clean(file)))
	}
	return 0
}

func ensureDraftFile(item draft.Draft, file string) (string, error) {
	path, err := pathutil.SafeJoin(item.Path, file)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory", file)
		}
		return path, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(""), 0o644)
}

func importedEditTarget(result draft.ImportResult) string {
	if len(result.Files) == 0 {
		return ""
	}
	for _, file := range result.Files {
		if file.Path == "SKILL.md" {
			return file.Path
		}
	}
	if len(result.Files) == 1 {
		return result.Files[0].Path
	}
	return "SKILL.md"
}

func editOverlayFile(cfg config.Config, skill string, agent string, operation string, file string, stdout io.Writer, stderr io.Writer) int {
	path, err := ensureOverlayFile(cfg, skill, agent, operation, file)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	edited, err := maybeEdit(path, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	label := overlayLabel(skill, agent)
	if edited {
		fmt.Fprintf(stdout, "edited overlay %s %s\n", label, filepath.Base(path))
	} else {
		fmt.Fprintf(stdout, "ready overlay %s %s\n", label, filepath.Base(path))
	}
	return 0
}

func ensureOverlayFile(cfg config.Config, skill string, agent string, operation string, file string) (string, error) {
	return overlay.EnsureFile(cfg, overlay.WriteOptions{
		Skill:     skill,
		Agent:     agent,
		Operation: operation,
		Path:      file,
	}, "")
}

func overlayLabel(skill string, agent string) string {
	if agent == "" {
		return skill
	}
	return fmt.Sprintf("%s[%s]", skill, agent)
}

func normalizeOverlayOperation(operation string) string {
	return strings.ReplaceAll(operation, "-", "_")
}

func maybeEdit(path string, stdout io.Writer, stderr io.Writer) (bool, error) {
	editor := cliEditor()
	if editor == "" || !cliCanEdit(stdout, stderr) {
		return false, nil
	}
	if err := cliRunEditor(editor, path); err != nil {
		return true, fmt.Errorf("editor failed: %w", err)
	}
	return true, nil
}

func canStartEditor(stdout io.Writer, stderr io.Writer) bool {
	return cliEditor() != "" && cliCanEdit(stdout, stderr)
}

func editorCommand() string {
	for _, name := range []string{"UPSKILL_EDITOR", "VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func canOpenEditor(stdout io.Writer, stderr io.Writer) bool {
	out, ok := stdout.(*os.File)
	if !ok || !isCharDevice(out) || !isCharDevice(os.Stdin) {
		return false
	}
	errOut, ok := stderr.(*os.File)
	return ok && isCharDevice(errOut)
}

func isCharDevice(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runEditorCommand(editor string, path string) error {
	command := editor + " " + quoteEditorPath(path)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("/bin/sh", "-c", command)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func quoteEditorPath(path string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(path, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}

func runOverlay(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printOverlayHelp(stdout)
		return 0
	}
	switch args[0] {
	case "list", "ls":
		var skill, agent string
		jsonOut := false
		if err := parseFlags(args[1:], map[string]func(string) error{
			"--skill": func(v string) error { skill = v; return nil },
			"--agent": func(v string) error { agent = v; return nil },
			"--json":  func(_ string) error { jsonOut = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		rows, err := overlay.List(cfg, skill, agent)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if jsonOut {
			data := overlay.MarshalSummaries(rows)
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		if len(rows) == 0 {
			fmt.Fprintln(stdout, "no overlays")
			return 0
		}
		tableRows := make([][]string, 0, len(rows))
		for _, row := range rows {
			agentName := row.Agent
			if agentName == "" {
				agentName = "all"
			}
			tableRows = append(tableRows, []string{row.Skill, agentName, strconv.Itoa(row.Files), row.Path})
		}
		printTable(stdout, []tableColumn{
			{Header: "SKILL", MaxWidth: 36},
			{Header: "AGENT", MaxWidth: 20},
			{Header: "FILES", MaxWidth: 8},
			{Header: "PATH", MaxWidth: 72},
		}, tableRows)
		return 0
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: overlay add requires <skill-name>")
			return 2
		}
		skill := args[1]
		agent := ""
		printPath := false
		noEdit := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--agent":   func(v string) error { agent = v; return nil },
			"--path":    func(_ string) error { printPath = true; return nil },
			"--no-edit": func(_ string) error { noEdit = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		storeName := library.NormalizeName(skill)
		path, err := overlay.Add(cfg, storeName, agent)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if printPath {
			fmt.Fprintln(stdout, path)
		} else {
			fmt.Fprintf(stdout, "created overlay %s\n", overlayLabel(storeName, agent))
			if !noEdit {
				if code := editOverlayFile(cfg, storeName, agent, "root_append", "SKILL.md", stdout, stderr); code != 0 {
					return code
				}
			}
		}
		return 0
	case "edit":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: overlay edit requires <skill-name>")
			return 2
		}
		skill := library.NormalizeName(args[1])
		agent := ""
		file := "SKILL.md"
		operation := "root_append"
		noEdit := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--agent":     func(v string) error { agent = v; return nil },
			"--file":      func(v string) error { file = v; return nil },
			"--operation": func(v string) error { operation = normalizeOverlayOperation(v); return nil },
			"--no-edit":   func(_ string) error { noEdit = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		if noEdit {
			path, err := ensureOverlayFile(cfg, skill, agent, operation, file)
			if err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "ready overlay %s %s\n", overlayLabel(skill, agent), filepath.Base(path))
			return 0
		}
		return editOverlayFile(cfg, skill, agent, operation, file, stdout, stderr)
	case "diff":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: overlay diff requires <skill-name>")
			return 2
		}
		skill := library.NormalizeName(args[1])
		agent := ""
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--agent": func(v string) error { agent = v; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		text, err := (render.Renderer{Config: cfg}).Diff(skill, agent)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, text)
		return 0
	default:
		fmt.Fprintf(stderr, "upskill: unknown overlay command: %s\n", args[0])
		return 2
	}
}

func runRender(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	var skill, agent, file string
	copyOutput := true
	baseOnly := false
	printFile := false
	file = "SKILL.md"

	positionals := []string{}
	if err := parseFlags(args, map[string]func(string) error{
		"--agent":     func(v string) error { agent = v; return nil },
		"--file":      func(v string) error { file = v; return nil },
		"--copy":      func(v string) error { copyOutput = parseBoolDefault(v, true); return nil },
		"--copy=true": func(_ string) error { copyOutput = true; return nil },
		"--copy=false": func(_ string) error {
			copyOutput = false
			return nil
		},
		"--no-copy": func(_ string) error { copyOutput = false; return nil },
		"--base-only": func(_ string) error {
			baseOnly = true
			return nil
		},
		"--print": func(_ string) error {
			printFile = true
			return nil
		},
	}, &positionals); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
	}
	if len(positionals) > 0 {
		skill = library.NormalizeName(positionals[0])
	}

	renderer := render.Renderer{Config: cfg}
	if printFile {
		if skill == "" {
			fmt.Fprintln(stderr, "upskill: render --print requires <skill-name>")
			return 2
		}
		text, err := renderer.Print(skill, agent, file, baseOnly)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, text)
		return 0
	}

	results, err := renderer.Render(render.Options{Skill: skill, Agent: agent, Copy: copyOutput, BaseOnly: baseOnly})
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	printResults(stdout, results)
	return 0
}

func runMCP(cfg config.Config, stdout io.Writer, stderr io.Writer) int {
	if err := mcpserver.Serve(cfg, os.Stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	return 0
}

func runStatus(cfg config.Config, stdout io.Writer, stderr io.Writer) int {
	skills, err := library.ListInstalled(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	overlays, err := overlay.List(cfg, "", "")
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	drafts, err := draft.List(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "home: %s\nstore: %s\ninstalled skills: %d\noverlays: %d\ndrafts: %d\n", cfg.Home, cfg.StoreDir, len(skills), len(overlays), len(drafts))
	return 0
}

func runDoctor(cfg config.Config, stdout io.Writer, stderr io.Writer) int {
	for _, dir := range []string{cfg.Home, cfg.StoreDir, cfg.OverlaysDir, cfg.DraftsDir, cfg.ImportsDir, cfg.RenderedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

func renderInstalled(cfg config.Config, installed []library.InstalledSkill, agent string, stdout io.Writer, stderr io.Writer) int {
	for _, item := range installed {
		results, err := (render.Renderer{Config: cfg}).Render(render.Options{Skill: item.StoreName, Agent: agent, Copy: true})
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		printResults(stdout, results)
	}
	return 0
}

func printInstalled(w io.Writer, installed []library.InstalledSkill, verb string) {
	if len(installed) == 0 {
		fmt.Fprintf(w, "no skills %s\n", verb)
		return
	}
	for _, item := range installed {
		fmt.Fprintf(w, "%s %s (%s)\n", verb, item.StoreName, item.Hash)
	}
}

func printUpdatePreviews(w io.Writer, previews []library.UpdatePreview, includeDiff bool) {
	if len(previews) == 0 {
		fmt.Fprintln(w, "no locked skills")
		return
	}
	changed := false
	for _, preview := range previews {
		status := "unchanged"
		if preview.Changed {
			status = "changed"
			changed = true
		}
		fmt.Fprintf(w, "%s\t%s\t%s -> %s\n", preview.StoreName, status, preview.CurrentHash, preview.NewHash)
		if includeDiff {
			for _, line := range preview.Diff {
				fmt.Fprintln(w, line)
			}
		}
	}
	if !changed {
		fmt.Fprintln(w, "no updates available")
	}
}

func printResults(w io.Writer, results []render.Result) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Skill != results[j].Skill {
			return results[i].Skill < results[j].Skill
		}
		return results[i].Agent < results[j].Agent
	})
	for _, result := range results {
		agent := result.Agent
		if agent == "" {
			agent = "generic"
		}
		if result.Skipped && len(result.CopiedTo) == 0 {
			continue
		}
		copied := "not copied"
		if len(result.CopiedTo) > 0 {
			copied = strings.Join(result.CopiedTo, ", ")
		}
		if result.Skipped {
			copied = "unchanged; " + copied
		}
		fmt.Fprintf(w, "%s [%s] -> %s (%s)\n", result.Skill, agent, result.RenderedPath, copied)
	}
	unchanged := 0
	for _, result := range results {
		if result.Skipped && len(result.CopiedTo) == 0 {
			unchanged++
		}
	}
	if unchanged == len(results) && unchanged > 0 {
		fmt.Fprintln(w, "all rendered skills unchanged")
	} else if unchanged > 0 {
		fmt.Fprintf(w, "%d rendered skills unchanged\n", unchanged)
	}
}

type tableColumn struct {
	Header   string
	MaxWidth int
}

func printTable(w io.Writer, columns []tableColumn, rows [][]string) {
	if len(columns) == 0 || len(rows) == 0 {
		return
	}
	widths := tableWidths(columns, rows)
	printTableLine(w, columnHeaders(columns), widths)
	printTableLine(w, tableRules(widths), widths)
	for _, row := range rows {
		wrapped := wrapTableRow(row, widths)
		for line := 0; line < tableRowHeight(wrapped); line++ {
			cells := make([]string, len(columns))
			for col := range columns {
				if col < len(wrapped) && line < len(wrapped[col]) {
					cells[col] = wrapped[col][line]
				}
			}
			printTableLine(w, cells, widths)
		}
	}
}

func printSearchPageSummary(w io.Writer, shown int, total int) {
	if total > shown {
		fmt.Fprintf(w, "showing %d/%d skills\n", shown, total)
	}
}

func tableWidths(columns []tableColumn, rows [][]string) []int {
	widths := make([]int, len(columns))
	for i, column := range columns {
		widths[i] = len(column.Header)
	}
	for _, row := range rows {
		for i := range columns {
			if i >= len(row) {
				continue
			}
			for _, line := range strings.Split(row[i], "\n") {
				if width := len(line); width > widths[i] {
					widths[i] = width
				}
			}
		}
	}
	for i, column := range columns {
		if column.MaxWidth > 0 && widths[i] > column.MaxWidth {
			widths[i] = column.MaxWidth
		}
		if widths[i] < len(column.Header) {
			widths[i] = len(column.Header)
		}
	}
	return widths
}

func columnHeaders(columns []tableColumn) []string {
	headers := make([]string, len(columns))
	for i, column := range columns {
		headers[i] = column.Header
	}
	return headers
}

func tableRules(widths []int) []string {
	rules := make([]string, len(widths))
	for i, width := range widths {
		rules[i] = strings.Repeat("-", width)
	}
	return rules
}

func printTableLine(w io.Writer, cells []string, widths []int) {
	for i, width := range widths {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		fmt.Fprint(w, padRight(cell, width))
	}
	fmt.Fprintln(w)
}

func wrapTableRow(row []string, widths []int) [][]string {
	wrapped := make([][]string, len(widths))
	for i, width := range widths {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		wrapped[i] = wrapCell(cell, width)
	}
	return wrapped
}

func tableRowHeight(wrapped [][]string) int {
	height := 1
	for _, lines := range wrapped {
		if len(lines) > height {
			height = len(lines)
		}
	}
	return height
}

func wrapCell(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	var out []string
	for _, rawLine := range strings.Split(text, "\n") {
		out = append(out, wrapLine(rawLine, width)...)
	}
	return out
}

func wrapLine(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	line := ""
	for _, word := range words {
		for len(word) > width {
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, word[:width])
			word = word[width:]
		}
		if line == "" {
			line = word
			continue
		}
		if len(line)+1+len(word) <= width {
			line += " " + word
			continue
		}
		lines = append(lines, line)
		line = word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func padRight(text string, width int) string {
	if len(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len(text))
}

func sourceSkillDisplay(skill library.SourceSkill) string {
	if skill.Name == "" || library.NormalizeName(skill.Name) == skill.StoreName {
		return skill.StoreName
	}
	return fmt.Sprintf("%s (%s)", skill.StoreName, skill.Name)
}

type sourceRow struct {
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name"`
	Source      string `json:"source"`
	Ref         string `json:"ref,omitempty"`
	Subpath     string `json:"subpath,omitempty"`
	CanonicalID string `json:"canonical_id"`
}

func sourceRows(sources map[string]config.Source) []sourceRow {
	records := config.SourceRecords(sources)
	rows := make([]sourceRow, 0, len(records))
	for _, source := range records {
		rows = append(rows, sourceRow{
			Name:        source.Name,
			DisplayName: config.SourceDisplayName(source),
			Source:      source.Source,
			Ref:         source.Ref,
			Subpath:     source.Subpath,
			CanonicalID: config.SourceKey(source),
		})
	}
	return rows
}

func upsertSourceRecord(sources map[string]config.Source, source config.Source) (config.Source, string, bool) {
	if existingKey, ok := findSourceRecordByIdentity(sources, source); ok {
		existing := sources[existingKey]
		if source.Name == "" && existing.Name != "" {
			source.Name = existing.Name
		}
		key := config.SourceKey(source)
		if existingKey != key {
			delete(sources, existingKey)
		}
		return source, key, true
	}
	return source, config.SourceKey(source), false
}

func findSourceRecordByIdentity(sources map[string]config.Source, source config.Source) (string, bool) {
	keys := make([]string, 0, len(sources))
	for key := range sources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if sameSourceIdentity(sources[key], source) {
			return key, true
		}
	}
	return "", false
}

func sameSourceIdentity(left config.Source, right config.Source) bool {
	return left.Source == right.Source &&
		left.Ref == right.Ref &&
		filepath.ToSlash(left.Subpath) == filepath.ToSlash(right.Subpath)
}

func parseFlags(args []string, handlers map[string]func(string) error, positionals ...*[]string) error {
	return parseFlagsWithValues(args, handlers, nil, positionals...)
}

func parseFlagsWithValues(args []string, handlers map[string]func(string) error, valueFlags map[string]bool, positionals ...*[]string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		handler, ok := handlers[arg]
		if !ok {
			if strings.HasPrefix(arg, "--") {
				return fmt.Errorf("unknown flag: %s", arg)
			}
			if len(positionals) > 0 {
				*positionals[0] = append(*positionals[0], arg)
				continue
			}
			return fmt.Errorf("unexpected argument: %s", arg)
		}
		if flagTakesValue(arg) || valueFlags[arg] {
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			if err := handler(args[i+1]); err != nil {
				return err
			}
			i++
		} else {
			if err := handler(""); err != nil {
				return err
			}
		}
	}
	return nil
}

func flagTakesValue(flag string) bool {
	switch flag {
	case "--agent", "--skill", "--file", "--copy", "--limit", "--ref", "--subpath", "--name", "--dir", "--from", "--template", "--archive", "--archive-url", "--file-url", "--sha256", "--strip-components", "--to", "--to-source", "--operation":
		return true
	default:
		return false
	}
}

func parseBoolDefault(value string, fallback bool) bool {
	if value == "" {
		return fallback
	}
	return value == "true" || value == "1" || value == "yes"
}

func isHelp(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}

func printRequestedHelp(args []string, w io.Writer) bool {
	if len(args) == 0 || isHelp(args[0]) {
		printHelp(w)
		return true
	}
	switch args[0] {
	case "add":
		return printDirectHelp(args, w, helpAdd)
	case "use":
		return printDirectHelp(args, w, helpUse)
	case "list", "ls":
		return printDirectHelp(args, w, helpList)
	case "find", "search":
		return printDirectHelp(args, w, helpFind)
	case "remove", "rm":
		return printDirectHelp(args, w, helpRemove)
	case "update":
		return printDirectHelp(args, w, helpUpdate)
	case "sync":
		return printDirectHelp(args, w, helpSync)
	case "init":
		return printDirectHelp(args, w, helpInit)
	case "workon":
		return printDirectHelp(args, w, helpWorkon)
	case "source":
		return printNestedHelp(args[1:], w, printSourceHelp, map[string]string{
			"list":   helpSourceList,
			"add":    helpSourceAdd,
			"skills": helpSourceSkills,
			"find":   helpSourceFind,
			"search": helpSourceFind,
			"remove": helpSourceRemove,
		})
	case "draft":
		return printNestedHelp(args[1:], w, printDraftHelp, map[string]string{
			"list":     helpDraftList,
			"create":   helpDraftCreate,
			"edit":     helpDraftEdit,
			"path":     helpDraftPath,
			"files":    helpDraftFiles,
			"read":     helpDraftRead,
			"add-file": helpDraftAddFile,
			"import":   helpDraftImport,
			"render":   helpDraftRender,
			"diff":     helpDraftDiff,
			"rebase":   helpDraftRebase,
			"promote":  helpDraftPromote,
			"remove":   helpDraftRemove,
		})
	case "overlay":
		return printNestedHelp(args[1:], w, printOverlayHelp, map[string]string{
			"list": helpOverlayList,
			"add":  helpOverlayAdd,
			"edit": helpOverlayEdit,
			"diff": helpOverlayDiff,
		})
	case "render":
		return printDirectHelp(args, w, helpRender)
	case "mcp":
		return printDirectHelp(args, w, helpMCP)
	case "version":
		return printDirectHelp(args, w, helpVersion)
	case "status":
		return printDirectHelp(args, w, helpStatus)
	case "doctor":
		return printDirectHelp(args, w, helpDoctor)
	default:
		return false
	}
}

func printDirectHelp(args []string, w io.Writer, usage string) bool {
	if len(args) > 1 && isHelp(args[1]) {
		printUsage(w, usage)
		return true
	}
	return false
}

func printNestedHelp(args []string, w io.Writer, parent func(io.Writer), usages map[string]string) bool {
	if len(args) == 0 {
		return false
	}
	if isHelp(args[0]) {
		parent(w)
		return true
	}
	if len(args) > 1 && isHelp(args[1]) {
		usage, ok := nestedUsage(usages, args[0])
		if !ok {
			return false
		}
		printUsage(w, usage)
		return true
	}
	return false
}

func nestedUsage(usages map[string]string, command string) (string, bool) {
	if usage, ok := usages[command]; ok {
		return usage, true
	}
	switch command {
	case "ls":
		usage, ok := usages["list"]
		return usage, ok
	case "rm":
		usage, ok := usages["remove"]
		return usage, ok
	default:
		return "", false
	}
}

func printUsage(w io.Writer, usage string) {
	fmt.Fprintln(w, strings.TrimRight(usage, "\n"))
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  add      Install skills from a source")
	fmt.Fprintln(w, "  use      Print a source or installed skill")
	fmt.Fprintln(w, "  list     List installed skills")
	fmt.Fprintln(w, "  find     Search installed skills")
	fmt.Fprintln(w, "  remove   Remove installed skills")
	fmt.Fprintln(w, "  update   Update installed skills from their locked sources")
	fmt.Fprintln(w, "  sync     Update skills and render overlays")
	fmt.Fprintln(w, "  init     Create a new skill bundle")
	fmt.Fprintln(w, "  workon   Create or open a mutable draft")
	fmt.Fprintln(w, "  source   Manage skill sources")
	fmt.Fprintln(w, "  draft    Manage mutable skill drafts")
	fmt.Fprintln(w, "  overlay  Manage skill overlays")
	fmt.Fprintln(w, "  render   Render skills with overlays")
	fmt.Fprintln(w, "  mcp      Run the MCP server")
	fmt.Fprintln(w, "  version  Print the current version")
	fmt.Fprintln(w, "  status   Show local upskill state")
	fmt.Fprintln(w, "  doctor   Check and create local directories")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "upskill <command> --help" for command help.`)
}

func printOverlayHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill overlay <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  list  List overlays")
	fmt.Fprintln(w, "  add   Create and edit an overlay")
	fmt.Fprintln(w, "  edit  Edit an overlay file")
	fmt.Fprintln(w, "  diff  Show rendered overlay changes")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "upskill overlay <command> --help" for command help.`)
}

func printSourceHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill source <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  list    List configured sources")
	fmt.Fprintln(w, "  add     Add, rename, or update a source")
	fmt.Fprintln(w, "  skills  List skills available in a source")
	fmt.Fprintln(w, "  find    Search skills in a source")
	fmt.Fprintln(w, "  remove  Remove a configured source")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "upskill source <command> --help" for command help.`)
}

func printDraftHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill draft <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  list      List drafts")
	fmt.Fprintln(w, "  create    Create and edit a draft")
	fmt.Fprintln(w, "  edit      Edit a draft file")
	fmt.Fprintln(w, "  path      Print a draft path")
	fmt.Fprintln(w, "  files     List draft files")
	fmt.Fprintln(w, "  read      Print a draft file")
	fmt.Fprintln(w, "  add-file  Add a local file to a draft")
	fmt.Fprintln(w, "  import    Import files into a draft")
	fmt.Fprintln(w, "  render    Render a draft")
	fmt.Fprintln(w, "  diff      Show draft changes")
	fmt.Fprintln(w, "  rebase    Rebase a version draft")
	fmt.Fprintln(w, "  promote   Copy a draft into an upstream checkout")
	fmt.Fprintln(w, "  remove    Remove a draft")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "upskill draft <command> --help" for command help.`)
}
