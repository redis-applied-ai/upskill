package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

const (
	helpAdd          = "usage: upskill add <source> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--no-render]"
	helpUse          = "usage: upskill use <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH]"
	helpList         = "usage: upskill list|ls [--json] [--agent AGENT]"
	helpFind         = "usage: upskill find [query] [--json] [--limit N]"
	helpRemove       = "usage: upskill remove|rm <skill-name>... [--delete-overlays]"
	helpUpdate       = "usage: upskill update [skill-name...] [--agent AGENT] [--no-render] [--check] [--diff]"
	helpSync         = "usage: upskill sync [--agent AGENT]"
	helpInit         = "usage: upskill init [name] [--dir DIR]"
	helpWorkon       = "usage: upskill workon <skill-name> [--from SKILL] [--template NAME] [--no-edit]"
	helpSourceList   = "usage: upskill source list [--json]"
	helpSourceAdd    = "usage: upskill source add <source> [--name NAME] [--ref REF] [--subpath PATH]"
	helpSourceRemove = "usage: upskill source remove <name>"
	helpVaultInit    = "usage: upskill vault init <path> [--name NAME] [--source-dir DIR] [--no-config]"
	helpVaultStatus  = "usage: upskill vault status [--name NAME] [--json]"
	helpDraftList    = "usage: upskill draft list [--json]"
	helpDraftCreate  = "usage: upskill draft create <skill-name> [--from SKILL] [--from-rendered] [--template NAME] [--no-edit]"
	helpDraftEdit    = "usage: upskill draft edit <skill-name> [--file PATH] [--no-edit]"
	helpDraftPath    = "usage: upskill draft path <skill-name>"
	helpDraftFiles   = "usage: upskill draft files <skill-name> [--json]"
	helpDraftRead    = "usage: upskill draft read <skill-name> [--file PATH]"
	helpDraftAddFile = "usage: upskill draft add-file <skill-name> <local-path> --path PATH [--no-edit]"
	helpDraftRender  = "usage: upskill draft render <skill-name> [--agent AGENT] [--copy=false] [--apply-overlays]"
	helpDraftDiff    = "usage: upskill draft diff <skill-name>"
	helpDraftRebase  = "usage: upskill draft rebase <skill-name>"
	helpDraftPromote = "usage: upskill draft promote <skill-name> (--to PATH | --to-source NAME) [--dry-run]"
	helpDraftRemove  = "usage: upskill draft remove <skill-name>"
	helpOverlayList  = "usage: upskill overlay list [--skill NAME] [--agent AGENT] [--json]"
	helpOverlayAdd   = "usage: upskill overlay add <skill-name> [--agent AGENT] [--path] [--no-edit]"
	helpOverlayEdit  = "usage: upskill overlay edit <skill-name> [--agent AGENT] [--file PATH] [--operation root-append|append|files|replace|template|patch] [--no-edit]"
	helpOverlayDiff  = "usage: upskill overlay diff <skill-name> [--agent AGENT]"
	helpRender       = "usage: upskill render [skill-name] [--agent AGENT] [--copy=false] [--print] [--base-only]"
	helpMCP          = "usage: upskill mcp"
	helpStatus       = "usage: upskill status"
	helpDoctor       = "usage: upskill doctor"
	helpDraftImport  = `usage:
  upskill draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect] [--no-edit]
  upskill draft import <skill-name> --archive-url URL --sha256 SHA256 [--strip-components N] [--inspect] [--no-edit]
  upskill draft import <skill-name> --file PATH --path DEST [--sha256 SHA256] [--no-edit]
  upskill draft import <skill-name> --file-url URL --path DEST --sha256 SHA256 [--no-edit]`
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if printRequestedHelp(args, stdout) {
		return 0
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
	case "find":
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
	case "vault":
		return runVault(cfg, args[1:], stdout, stderr)
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
	fmt.Fprint(stdout, sourceSkill.Body)
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
	for _, skill := range skills {
		overlays := overlay.Chain(cfg, skill.StoreName, agent)
		fmt.Fprintf(stdout, "%s\t%s\toverlays:%d\n", skill.StoreName, skill.Description, len(overlays))
	}
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
	skills, err := library.FindInstalled(cfg, query, limit)
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
	for _, skill := range skills {
		fmt.Fprintf(stdout, "%s\t%s\n", skill.StoreName, skill.Description)
	}
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
	case "list":
		jsonOut := false
		if err := parseFlags(args[1:], map[string]func(string) error{
			"--json": func(_ string) error { jsonOut = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		if jsonOut {
			data, _ := json.MarshalIndent(cfg.Sources, "", "  ")
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		names := sortedSourceNames(cfg.Sources)
		if len(names) == 0 {
			fmt.Fprintln(stdout, "no sources")
			return 0
		}
		for _, name := range names {
			src := cfg.Sources[name]
			fmt.Fprintf(stdout, "%s\t%s\n", name, src.Source)
		}
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
		if name == "" {
			name = defaultSourceName(source)
		}
		cfg.Sources[name] = config.Source{Name: name, Source: source, Ref: ref, Subpath: subpath}
		if err := config.SaveSources(cfg, cfg.Sources); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "added source %s\n", name)
		return 0
	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: source remove requires <name>")
			return 2
		}
		delete(cfg.Sources, args[1])
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

type vaultStatus struct {
	Name            string `json:"name"`
	Root            string `json:"root"`
	SourceDir       string `json:"source_dir"`
	OverlaysDir     string `json:"overlays_dir"`
	DraftsDir       string `json:"drafts_dir"`
	ImportsDir      string `json:"imports_dir"`
	InstalledSkills int    `json:"installed_skills"`
	SourceSkills    int    `json:"source_skills"`
	Overlays        int    `json:"overlays"`
	Drafts          int    `json:"drafts"`
}

func runVault(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printVaultHelp(stdout)
		return 0
	}
	switch args[0] {
	case "init":
		name := ""
		sourceDirName := "source"
		noConfig := false
		positionals := []string{}
		if err := parseFlags(args[1:], map[string]func(string) error{
			"--name":       func(v string) error { name = v; return nil },
			"--source-dir": func(v string) error { sourceDirName = v; return nil },
			"--no-config":  func(_ string) error { noConfig = true; return nil },
		}, &positionals); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		if len(positionals) == 0 {
			fmt.Fprintln(stderr, "upskill: vault init requires <path>")
			return 2
		}
		root, _ := filepath.Abs(positionals[0])
		sourceDir, err := pathutil.SafeJoin(root, sourceDirName)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		layout := map[string]string{
			"source":   sourceDir,
			"overlays": filepath.Join(root, "overlays"),
			"drafts":   filepath.Join(root, "drafts"),
			"imports":  filepath.Join(root, "imports"),
		}
		for _, dir := range layout {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 1
			}
		}
		if name == "" {
			name = defaultSourceName(root)
		}
		if !noConfig {
			cfg.OverlaysDir = layout["overlays"]
			cfg.DraftsDir = layout["drafts"]
			cfg.ImportsDir = layout["imports"]
			cfg.ImportDirs = appendUnique(cfg.ImportDirs, layout["imports"])
			cfg.SourceIgnore = appendUnique(config.DefaultSourceIgnore, cfg.SourceIgnore...)
			if cfg.Sources == nil {
				cfg.Sources = map[string]config.Source{}
			}
			cfg.Sources[name] = config.Source{Name: name, Source: sourceDir}
			if err := config.SaveConfig(cfg); err != nil {
				fmt.Fprintf(stderr, "upskill: %v\n", err)
				return 1
			}
		}
		fmt.Fprintf(stdout, "initialized vault %s\n", root)
		fmt.Fprintf(stdout, "source %s -> %s\n", name, sourceDir)
		return 0
	case "status":
		name := ""
		jsonOut := false
		if err := parseFlags(args[1:], map[string]func(string) error{
			"--name": func(v string) error { name = v; return nil },
			"--json": func(_ string) error {
				jsonOut = true
				return nil
			},
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		status, err := buildVaultStatus(cfg, name)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if jsonOut {
			data, _ := json.MarshalIndent(status, "", "  ")
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		fmt.Fprintf(stdout, "name: %s\nroot: %s\nsource: %s\ninstalled skills: %d\nsource skills: %d\noverlays: %d\ndrafts: %d\n", status.Name, status.Root, status.SourceDir, status.InstalledSkills, status.SourceSkills, status.Overlays, status.Drafts)
		return 0
	default:
		fmt.Fprintf(stderr, "upskill: unknown vault command: %s\n", args[0])
		return 2
	}
}

func buildVaultStatus(cfg config.Config, name string) (vaultStatus, error) {
	sourceName := name
	if sourceName == "" {
		names := sortedSourceNames(cfg.Sources)
		if len(names) == 0 {
			return vaultStatus{}, fmt.Errorf("no sources configured")
		}
		sourceName = names[0]
	}
	sourceDir, err := library.ResolveLocalSourceDestination(cfg, sourceName)
	if err != nil {
		return vaultStatus{}, err
	}
	root := filepath.Dir(sourceDir)
	installed, err := library.ListInstalled(cfg)
	if err != nil {
		return vaultStatus{}, err
	}
	sourceSkills, err := library.DiscoverSkillsWithIgnore(sourceDir, "", cfg.SourceIgnore)
	if err != nil {
		return vaultStatus{}, err
	}
	overlays, err := overlay.List(cfg, "", "")
	if err != nil {
		return vaultStatus{}, err
	}
	drafts, err := draft.List(cfg)
	if err != nil {
		return vaultStatus{}, err
	}
	return vaultStatus{
		Name:            sourceName,
		Root:            root,
		SourceDir:       sourceDir,
		OverlaysDir:     cfg.OverlaysDir,
		DraftsDir:       cfg.DraftsDir,
		ImportsDir:      cfg.ImportsDir,
		InstalledSkills: len(installed),
		SourceSkills:    len(sourceSkills),
		Overlays:        len(overlays),
		Drafts:          len(drafts),
	}, nil
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
	case "list":
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
		for _, item := range items {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.ID, item.Metadata.Type, item.Path)
		}
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
	case "remove":
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
	case "list":
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
		for _, row := range rows {
			agentName := row.Agent
			if agentName == "" {
				agentName = "all"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%d\t%s\n", row.Skill, agentName, row.Files, row.Path)
		}
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
		copied := "not copied"
		if len(result.CopiedTo) > 0 {
			copied = strings.Join(result.CopiedTo, ", ")
		}
		fmt.Fprintf(w, "%s [%s] -> %s (%s)\n", result.Skill, agent, result.RenderedPath, copied)
	}
}

func sortedSourceNames(sources map[string]config.Source) []string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func appendUnique(values []string, extra ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values)+len(extra))
	for _, value := range append(append([]string{}, values...), extra...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func defaultSourceName(source string) string {
	source = strings.TrimSuffix(source, ".git")
	source = strings.Trim(source, "/")
	if source == "" {
		return "source"
	}
	parts := strings.FieldsFunc(source, func(r rune) bool {
		return r == '/' || r == ':' || r == '\\'
	})
	return library.NormalizeName(parts[len(parts)-1])
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
	case "--agent", "--skill", "--file", "--copy", "--limit", "--ref", "--subpath", "--name", "--dir", "--from", "--template", "--archive", "--archive-url", "--file-url", "--sha256", "--strip-components", "--to", "--to-source", "--source-dir", "--operation":
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
	case "find":
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
			"remove": helpSourceRemove,
		})
	case "vault":
		return printNestedHelp(args[1:], w, printVaultHelp, map[string]string{
			"init":   helpVaultInit,
			"status": helpVaultStatus,
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
		usage, ok := usages[args[0]]
		if !ok {
			return false
		}
		printUsage(w, usage)
		return true
	}
	return false
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
	fmt.Fprintln(w, "  source   Manage named skill sources")
	fmt.Fprintln(w, "  vault    Manage filesystem skill vaults")
	fmt.Fprintln(w, "  draft    Manage mutable skill drafts")
	fmt.Fprintln(w, "  overlay  Manage skill overlays")
	fmt.Fprintln(w, "  render   Render skills with overlays")
	fmt.Fprintln(w, "  mcp      Run the MCP server")
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
	fmt.Fprintln(w, "  list    List named sources")
	fmt.Fprintln(w, "  add     Add or update a named source")
	fmt.Fprintln(w, "  remove  Remove a named source")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "upskill source <command> --help" for command help.`)
}

func printVaultHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill vault <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  init    Create a vault layout and configure it as a local source")
	fmt.Fprintln(w, "  status  Show vault source, overlay, draft, and import state")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "upskill vault <command> --help" for command help.`)
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
