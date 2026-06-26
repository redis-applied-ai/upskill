package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printHelp(stdout)
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
		fmt.Fprintln(stdout, "usage: upskill add <source> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--no-render]")
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
		fmt.Fprintln(stdout, "usage: upskill use <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH]")
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
	positionals := []string{}
	if err := parseFlags(args, map[string]func(string) error{
		"--agent":     func(v string) error { agent = v; return nil },
		"--no-render": func(_ string) error { noRender = true; return nil },
	}, &positionals); err != nil {
		fmt.Fprintf(stderr, "upskill: %v\n", err)
		return 2
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

func runWorkon(cfg config.Config, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(stdout, "usage: upskill workon <skill-name> [--from SKILL] [--template NAME]")
		return 0
	}
	name := args[0]
	from, template := "", ""
	if err := parseFlags(args[1:], map[string]func(string) error{
		"--from":     func(v string) error { from = v; return nil },
		"--template": func(v string) error { template = v; return nil },
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
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--from":          func(v string) error { from = v; return nil },
			"--from-rendered": func(_ string) error { fromRendered = true; return nil },
			"--template":      func(v string) error { template = v; return nil },
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
		return 0
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
		if err := parseFlagsWithValues(args[3:], map[string]func(string) error{
			"--path": func(v string) error { dest = v; return nil },
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
		return 0
	case "import":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "upskill: draft import requires <skill-name>")
			return 2
		}
		opts := draft.ImportOptions{}
		strip := ""
		if err := parseFlagsWithValues(args[2:], map[string]func(string) error{
			"--archive":          func(v string) error { opts.ArchivePath = v; return nil },
			"--archive-url":      func(v string) error { opts.ArchiveURL = v; return nil },
			"--file":             func(v string) error { opts.FilePath = v; return nil },
			"--file-url":         func(v string) error { opts.FileURL = v; return nil },
			"--path":             func(v string) error { opts.DestPath = v; return nil },
			"--sha256":           func(v string) error { opts.SHA256 = v; return nil },
			"--strip-components": func(v string) error { strip = v; return nil },
			"--inspect":          func(_ string) error { opts.Inspect = true; return nil },
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
		dryRun := false
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--to":      func(v string) error { toPath = v; return nil },
			"--dry-run": func(_ string) error { dryRun = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		result, err := draft.Promote(cfg, args[1], draft.PromoteOptions{ToPath: toPath, DryRun: dryRun})
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
	fmt.Fprintf(w, "%s\t%s\t%s\n", item.ID, item.Metadata.Type, item.Path)
	if item.Metadata.CreatedFrom == draft.CreatedFromRendered {
		fmt.Fprintln(w, "warning: draft was created from rendered output and may include local overlays")
	}
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
		if err := parseFlags(args[2:], map[string]func(string) error{
			"--agent": func(v string) error { agent = v; return nil },
			"--path":  func(_ string) error { printPath = true; return nil },
		}); err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 2
		}
		path, err := overlay.Add(cfg, library.NormalizeName(skill), agent)
		if err != nil {
			fmt.Fprintf(stderr, "upskill: %v\n", err)
			return 1
		}
		if printPath {
			fmt.Fprintln(stdout, path)
		} else {
			fmt.Fprintf(stdout, "created %s\n", path)
		}
		return 0
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
	case "--agent", "--skill", "--file", "--copy", "--limit", "--ref", "--subpath", "--name", "--dir", "--from", "--template", "--archive", "--archive-url", "--file-url", "--sha256", "--strip-components", "--to":
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

func printHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "library:")
	fmt.Fprintln(w, "  add <source> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH] [--no-render]")
	fmt.Fprintln(w, "  use <source-or-skill> [--agent AGENT] [--skill NAME] [--ref REF] [--subpath PATH]")
	fmt.Fprintln(w, "  list|ls [--json] [--agent AGENT]")
	fmt.Fprintln(w, "  find [query] [--json] [--limit N]")
	fmt.Fprintln(w, "  remove|rm <skill-name>... [--delete-overlays]")
	fmt.Fprintln(w, "  update [skill-name...] [--agent AGENT] [--no-render]")
	fmt.Fprintln(w, "  sync [--agent AGENT]")
	fmt.Fprintln(w, "  init [name] [--dir DIR]")
	fmt.Fprintln(w, "  workon <skill-name> [--from SKILL] [--template NAME]")
	fmt.Fprintln(w, "  status")
	fmt.Fprintln(w, "  doctor")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "sources:")
	fmt.Fprintln(w, "  source list [--json]")
	fmt.Fprintln(w, "  source add <source> [--name NAME] [--ref REF] [--subpath PATH]")
	fmt.Fprintln(w, "  source remove <name>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "drafts:")
	fmt.Fprintln(w, "  draft list [--json]")
	fmt.Fprintln(w, "  draft create <skill-name> [--from SKILL] [--from-rendered] [--template NAME]")
	fmt.Fprintln(w, "  draft path <skill-name>")
	fmt.Fprintln(w, "  draft files <skill-name> [--json]")
	fmt.Fprintln(w, "  draft read <skill-name> [--file PATH]")
	fmt.Fprintln(w, "  draft add-file <skill-name> <local-path> --path PATH")
	fmt.Fprintln(w, "  draft import <skill-name> --archive PATH [--sha256 SHA256] [--strip-components N] [--inspect]")
	fmt.Fprintln(w, "  draft import <skill-name> --archive-url URL --sha256 SHA256 [--strip-components N] [--inspect]")
	fmt.Fprintln(w, "  draft import <skill-name> --file PATH --path DEST [--sha256 SHA256]")
	fmt.Fprintln(w, "  draft import <skill-name> --file-url URL --path DEST --sha256 SHA256")
	fmt.Fprintln(w, "  draft render <skill-name> [--agent AGENT] [--copy=false] [--apply-overlays]")
	fmt.Fprintln(w, "  draft diff <skill-name>")
	fmt.Fprintln(w, "  draft rebase <skill-name>")
	fmt.Fprintln(w, "  draft promote <skill-name> --to PATH [--dry-run]")
	fmt.Fprintln(w, "  draft remove <skill-name>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "overlays:")
	fmt.Fprintln(w, "  overlay list [--skill NAME] [--agent AGENT] [--json]")
	fmt.Fprintln(w, "  overlay add <skill-name> [--agent AGENT] [--path]")
	fmt.Fprintln(w, "  overlay diff <skill-name> [--agent AGENT]")
	fmt.Fprintln(w, "  render [skill-name] [--agent AGENT] [--copy=false] [--print] [--base-only]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "server:")
	fmt.Fprintln(w, "  mcp")
}

func printOverlayHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill overlay <list|add|diff> [args]")
}

func printSourceHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill source <list|add|remove> [args]")
}

func printDraftHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: upskill draft <list|create|path|files|read|add-file|import|render|diff|rebase|promote|remove> [args]")
}
