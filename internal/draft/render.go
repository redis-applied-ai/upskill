package draft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"upskill/internal/config"
	"upskill/internal/fsutil"
	"upskill/internal/library"
	"upskill/internal/render"
)

var (
	fsCopyDir   = fsutil.CopyDir
	fsWalkFiles = fsutil.WalkFiles
	filepathAbs = filepath.Abs
)

type RenderOptions struct {
	Agent         string
	Copy          bool
	ApplyOverlays bool
}

type RenderResult struct {
	Skill        string   `json:"skill"`
	SkillID      string   `json:"skill_id"`
	Agent        string   `json:"agent,omitempty"`
	Mode         string   `json:"mode"`
	RenderedPath string   `json:"rendered_path"`
	CopiedTo     []string `json:"copied_to,omitempty"`
}

type DiffResult struct {
	DraftID string   `json:"draft_id"`
	Lines   []string `json:"lines"`
}

type RebaseResult struct {
	DraftID   string   `json:"draft_id"`
	Rebased   bool     `json:"rebased"`
	Conflicts []string `json:"conflicts,omitempty"`
}

type PromoteOptions struct {
	ToPath   string
	ToSource string
	DryRun   bool
}

type PromoteResult struct {
	DraftID     string         `json:"draft_id"`
	Destination string         `json:"destination"`
	Source      string         `json:"source,omitempty"`
	DryRun      bool           `json:"dry_run"`
	Files       []ImportedFile `json:"files"`
	Overwrite   bool           `json:"overwrite"`
	BaseMutated bool           `json:"base_mutated"`
	NextSteps   []string       `json:"next_steps,omitempty"`
}

type draftMarker struct {
	SchemaVersion int    `json:"schema_version"`
	Skill         string `json:"skill"`
	SkillID       string `json:"skill_id"`
	Kind          string `json:"kind"`
	DraftType     string `json:"draft_type"`
	Agent         string `json:"agent,omitempty"`
	Mode          string `json:"mode"`
	DraftPath     string `json:"draft_path"`
	ParentSkill   string `json:"parent_skill,omitempty"`
	ParentHash    string `json:"parent_hash,omitempty"`
	RenderedAt    string `json:"rendered_at"`
}

func Render(cfg config.Config, name string, opts RenderOptions) ([]RenderResult, error) {
	item, err := Read(cfg, name)
	if err != nil {
		return nil, err
	}
	tmpStore, err := osMkdirTemp("", "upskill-draft-store-*")
	if err != nil {
		return nil, err
	}
	defer osRemoveAll(tmpStore)
	tmpSkill := filepath.Join(tmpStore, item.StoreName)
	if err := fsCopyDir(item.Path, tmpSkill, func(rel string, _ os.DirEntry) bool {
		return rel == MetadataFile
	}); err != nil {
		return nil, err
	}
	renderCfg := cfg
	renderCfg.StoreDir = tmpStore
	renderCfg.RenderedDir = filepath.Join(cfg.RenderedDir, "drafts")
	results, err := (render.Renderer{Config: renderCfg}).Render(render.Options{
		Skill:    item.StoreName,
		Agent:    opts.Agent,
		Copy:     false,
		BaseOnly: !opts.ApplyOverlays,
	})
	if err != nil {
		return nil, err
	}
	mode := "draft"
	if opts.ApplyOverlays {
		mode = "draft-with-overlays"
	}
	out := make([]RenderResult, 0, len(results))
	for _, result := range results {
		if err := writeDraftMarker(result.RenderedPath, item, result.Agent, mode); err != nil {
			return nil, err
		}
		copied := []string{}
		if opts.Copy {
			copied, err = copyRenderedToAgents(cfg, item.StoreName, result.Agent, result.RenderedPath)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, RenderResult{
			Skill:        item.StoreName,
			SkillID:      item.ID,
			Agent:        result.Agent,
			Mode:         mode,
			RenderedPath: result.RenderedPath,
			CopiedTo:     copied,
		})
	}
	return out, nil
}

func Diff(cfg config.Config, name string) (DiffResult, error) {
	item, err := Read(cfg, name)
	if err != nil {
		return DiffResult{}, err
	}
	base := ""
	if item.Metadata.Type == TypeVersion && item.Metadata.ParentSkill != "" {
		base = filepath.Join(cfg.StoreDir, item.Metadata.ParentSkill)
	}
	lines, err := diffDirs(base, item.Path)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{DraftID: item.ID, Lines: lines}, nil
}

func Rebase(cfg config.Config, name string) (RebaseResult, error) {
	item, err := Read(cfg, name)
	if err != nil {
		return RebaseResult{}, err
	}
	if item.Metadata.Type != TypeVersion {
		return RebaseResult{}, fmt.Errorf("only version drafts can be rebased")
	}
	baseDir := filepath.Join(cfg.StoreDir, item.Metadata.ParentSkill)
	hash, err := library.HashDir(baseDir)
	if err != nil {
		return RebaseResult{}, err
	}
	if hash == item.Metadata.ParentHash {
		return RebaseResult{DraftID: item.ID, Rebased: false}, nil
	}
	return RebaseResult{DraftID: item.ID, Rebased: false, Conflicts: []string{"parent base changed; manual rebase required"}}, nil
}

func Promote(cfg config.Config, name string, opts PromoteOptions) (PromoteResult, error) {
	item, err := Read(cfg, name)
	if err != nil {
		return PromoteResult{}, err
	}
	if opts.ToPath == "" {
		return PromoteResult{}, fmt.Errorf("promotion destination is required")
	}
	dest, err := promotionDestination(opts.ToPath, item.StoreName)
	if err != nil {
		return PromoteResult{}, err
	}
	files, err := plannedCopy(item.Path, dest)
	if err != nil {
		return PromoteResult{}, err
	}
	_, statErr := osStat(dest)
	overwrite := statErr == nil
	result := PromoteResult{
		DraftID:     item.ID,
		Destination: dest,
		Source:      opts.ToSource,
		DryRun:      opts.DryRun,
		Files:       files,
		Overwrite:   overwrite,
		BaseMutated: false,
		NextSteps:   promotionNextSteps(item.StoreName, opts.ToSource),
	}
	if opts.DryRun {
		return result, nil
	}
	if err := osRemoveAll(dest); err != nil {
		return PromoteResult{}, err
	}
	if err := fsCopyDir(item.Path, dest, func(rel string, _ os.DirEntry) bool {
		return rel == MetadataFile
	}); err != nil {
		return PromoteResult{}, err
	}
	return result, nil
}

func promotionNextSteps(skill string, source string) []string {
	if source == "" {
		return []string{
			"review the promoted files in the destination checkout",
			"commit, push, and open a PR in that upstream repository",
			fmt.Sprintf("after merge, run upskill update %s or upskill add <source> --skill %s", skill, skill),
		}
	}
	return []string{
		fmt.Sprintf("review the promoted files in source %s", source),
		"commit, push, and open a PR if that source is version-controlled",
		fmt.Sprintf("run upskill add %s --skill %s to install a new skill, or upskill update %s for an already locked skill", source, skill, skill),
	}
}

func writeDraftMarker(root string, item Draft, agent string, mode string) error {
	data, _ := json.MarshalIndent(draftMarker{
		SchemaVersion: SchemaVersion,
		Skill:         item.StoreName,
		SkillID:       item.ID,
		Kind:          "draft",
		DraftType:     item.Metadata.Type,
		Agent:         agent,
		Mode:          mode,
		DraftPath:     item.Path,
		ParentSkill:   item.Metadata.ParentSkill,
		ParentHash:    item.Metadata.ParentHash,
		RenderedAt:    time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	data = append(data, '\n')
	return osWriteFile(filepath.Join(root, render.MarkerFile), data, 0o644)
}

func copyRenderedToAgents(cfg config.Config, skill string, agent string, renderedPath string) ([]string, error) {
	var copied []string
	for _, output := range cfg.AgentOutputs {
		if agent != "" && output.Name != agent {
			continue
		}
		dest := filepath.Join(output.Path, skill)
		if err := osRemoveAll(dest); err != nil {
			return nil, err
		}
		if err := fsCopyDir(renderedPath, dest, nil); err != nil {
			return nil, err
		}
		copied = append(copied, dest)
	}
	sort.Strings(copied)
	return copied, nil
}

func diffDirs(base string, draftPath string) ([]string, error) {
	baseFiles, err := readFiles(base)
	if err != nil {
		return nil, err
	}
	draftFiles, err := readFiles(draftPath)
	if err != nil {
		return nil, err
	}
	keys := map[string]struct{}{}
	for key := range baseFiles {
		keys[key] = struct{}{}
	}
	for key := range draftFiles {
		keys[key] = struct{}{}
	}
	var sorted []string
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	var lines []string
	for _, key := range sorted {
		before, hadBefore := baseFiles[key]
		after, hasAfter := draftFiles[key]
		if hadBefore && hasAfter && before == after {
			continue
		}
		lines = append(lines, "diff "+key)
		if hadBefore {
			lines = append(lines, "-"+before)
		}
		if hasAfter {
			lines = append(lines, "+"+after)
		}
	}
	return lines, nil
}

func readFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	if root == "" {
		return files, nil
	}
	if _, err := osStat(root); os.IsNotExist(err) {
		return files, nil
	} else if err != nil {
		return nil, err
	}
	paths, err := fsWalkFiles(root)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == MetadataFile || rel == render.MarkerFile {
			continue
		}
		data, err := osReadFile(path)
		if err != nil {
			return nil, err
		}
		files[rel] = string(data)
	}
	return files, nil
}

func promotionDestination(path string, skill string) (string, error) {
	abs, err := filepathAbs(path)
	if err != nil {
		return "", err
	}
	if filepath.Base(abs) == skill {
		return abs, nil
	}
	if _, err := osStat(filepath.Join(abs, "SKILL.md")); err == nil {
		return abs, nil
	}
	return filepath.Join(abs, skill), nil
}

func plannedCopy(src string, dest string) ([]ImportedFile, error) {
	files := []ImportedFile{}
	paths, err := fsWalkFiles(src)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		rel, _ := filepath.Rel(src, path)
		rel = filepath.ToSlash(rel)
		if rel == MetadataFile {
			continue
		}
		info, err := osStat(path)
		if err != nil {
			return nil, err
		}
		_, statErr := osStat(filepath.Join(dest, filepath.FromSlash(rel)))
		files = append(files, ImportedFile{Path: rel, Size: info.Size(), Overwrite: statErr == nil})
	}
	sort.Slice(files, func(i, j int) bool { return strings.Compare(files[i].Path, files[j].Path) < 0 })
	return files, nil
}
