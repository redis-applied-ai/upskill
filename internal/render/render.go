package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
	gonja "github.com/nikolalohinski/gonja/v2"
	gonjaexec "github.com/nikolalohinski/gonja/v2/exec"
	"github.com/nikolalohinski/gonja/v2/loaders"

	"upskill/internal/config"
	"upskill/internal/fsutil"
	"upskill/internal/overlay"
	"upskill/internal/pathutil"
)

const MarkerFile = ".upskill-render.json"
const markerSchemaVersion = 2

const appendSeparator = "\n\n---\n\n<!-- upskill overlay -->\n\n"

var (
	mkdirAll   = os.MkdirAll
	removeAll  = os.RemoveAll
	readDir    = os.ReadDir
	renamePath = os.Rename
)

type Renderer struct {
	Config config.Config
}

type Options struct {
	Skill    string
	Agent    string
	Copy     bool
	BaseOnly bool
}

type Result struct {
	Skill        string
	Agent        string
	RenderedPath string
	CopiedTo     []string
	Overlays     []string
	Mode         string
	Skipped      bool
}

type marker struct {
	SchemaVersion int      `json:"schema_version"`
	Skill         string   `json:"skill"`
	Agent         string   `json:"agent,omitempty"`
	Mode          string   `json:"mode"`
	BasePath      string   `json:"base_path"`
	BaseHash      string   `json:"base_hash"`
	Overlays      []string `json:"overlays"`
	OverlayHash   string   `json:"overlay_hash"`
	RenderedAt    string   `json:"rendered_at"`
}

func (r Renderer) Render(opts Options) ([]Result, error) {
	if opts.BaseOnly {
		opts.Copy = false
	}
	skills, err := r.skills(opts.Skill)
	if err != nil {
		return nil, err
	}
	outputs := r.agentOutputs(opts.Agent)
	var results []Result
	for _, skill := range skills {
		if len(outputs) == 0 {
			result, err := r.renderOne(skill, opts.Agent, opts.BaseOnly)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
			continue
		}
		for _, output := range outputs {
			result, err := r.renderOne(skill, output.Name, opts.BaseOnly)
			if err != nil {
				return nil, err
			}
			if opts.Copy {
				copied, changed, err := copyToAgentOutput(skill, result.RenderedPath, output)
				if err != nil {
					return nil, err
				}
				if changed {
					result.CopiedTo = append(result.CopiedTo, copied)
				}
			}
			results = append(results, result)
		}
	}
	return results, nil
}

func (r Renderer) Print(skill string, agent string, file string, baseOnly bool) (string, error) {
	result, err := r.renderOne(skill, agent, baseOnly)
	if err != nil {
		return "", err
	}
	target, err := pathutil.SafeJoin(result.RenderedPath, file)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r Renderer) Diff(skill string, agent string) (string, error) {
	baseDir := filepath.Join(r.Config.StoreDir, skill)
	result, err := r.renderOne(skill, agent, false)
	if err != nil {
		return "", err
	}
	return diffDirs(baseDir, result.RenderedPath)
}

func (r Renderer) renderOne(skill string, agent string, baseOnly bool) (Result, error) {
	baseDir := filepath.Join(r.Config.StoreDir, skill)
	info, err := os.Stat(baseDir)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%s is not a directory", baseDir)
	}

	agentKey := agent
	if agentKey == "" {
		agentKey = "generic"
	}
	finalDir := filepath.Join(r.Config.RenderedDir, agentKey, skill)
	overlays := []string{}
	if !baseOnly {
		overlays = overlay.Chain(r.Config, skill, agent)
	}
	mode := "with-overlays"
	if baseOnly {
		mode = "base-only"
	}
	baseHash, err := treeHash(baseDir)
	if err != nil {
		return Result{}, err
	}
	overlayDigest := overlayHash(overlays)
	if renderCurrent(finalDir, skill, agent, mode, baseDir, baseHash, overlays, overlayDigest) {
		return Result{
			Skill:        skill,
			Agent:        agent,
			RenderedPath: finalDir,
			Overlays:     overlays,
			Mode:         mode,
			Skipped:      true,
		}, nil
	}

	tmpDir := finalDir + ".tmp"
	if err := removeAll(tmpDir); err != nil {
		return Result{}, err
	}
	if err := fsutil.CopyDir(baseDir, tmpDir, func(rel string, _ os.DirEntry) bool {
		return fsutil.IsJ2(rel)
	}); err != nil {
		return Result{}, err
	}

	state, err := newTemplateState(baseDir, tmpDir, skill, agent)
	if err != nil {
		return Result{}, err
	}
	if err := state.renderBaseTemplates(); err != nil {
		return Result{}, err
	}

	if !baseOnly {
		for _, dir := range overlays {
			if err := state.applyOverlay(dir); err != nil {
				return Result{}, err
			}
		}
	}

	if err := writeMarker(tmpDir, skill, agent, mode, baseDir, baseHash, overlays, overlayDigest); err != nil {
		return Result{}, err
	}
	if err := removeAll(finalDir); err != nil {
		return Result{}, err
	}
	if err := mkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return Result{}, err
	}
	if err := renamePath(tmpDir, finalDir); err != nil {
		return Result{}, err
	}

	return Result{
		Skill:        skill,
		Agent:        agent,
		RenderedPath: finalDir,
		Overlays:     overlays,
		Mode:         mode,
	}, nil
}

func (r Renderer) skills(skill string) ([]string, error) {
	if skill != "" {
		return []string{skill}, nil
	}
	return config.DiscoverSkills(r.Config.StoreDir)
}

func (r Renderer) agentOutputs(agent string) []config.AgentOutput {
	var outputs []config.AgentOutput
	for _, output := range r.Config.AgentOutputs {
		if agent == "" || output.Name == agent {
			outputs = append(outputs, output)
		}
	}
	sort.Slice(outputs, func(i, j int) bool {
		return outputs[i].Name < outputs[j].Name
	})
	return outputs
}

type templateState struct {
	baseDir       string
	workDir       string
	skill         string
	agent         string
	layer         int
	contents      map[string]string
	currentByPath map[string]string
}

func newTemplateState(baseDir string, workDir string, skill string, agent string) (*templateState, error) {
	state := &templateState{
		baseDir:       baseDir,
		workDir:       workDir,
		skill:         skill,
		agent:         agent,
		contents:      map[string]string{},
		currentByPath: map[string]string{},
	}
	files, err := fsutil.WalkFiles(baseDir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		rel, _ := filepath.Rel(baseDir, file)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		text := string(data)
		state.contents[templateKey("base/"+rel)] = text
		if strings.HasSuffix(rel, ".j2") {
			name := templateKey("layer0/" + rel)
			state.contents[name] = text
			state.currentByPath[rel] = name
		} else {
			state.contents[templateKey(rel)] = text
		}
	}
	return state, nil
}

func (s *templateState) renderBaseTemplates() error {
	paths := sortedKeys(s.currentByPath)
	for _, rel := range paths {
		if err := s.renderTemplate(s.currentByPath[rel], rel); err != nil {
			return err
		}
	}
	return nil
}

func (s *templateState) applyOverlay(dir string) error {
	if err := s.copyNewFiles(filepath.Join(dir, "files")); err != nil {
		return err
	}
	if err := s.replaceFiles(filepath.Join(dir, "replace")); err != nil {
		return err
	}
	if err := s.applyPatches(filepath.Join(dir, "patches")); err != nil {
		return err
	}
	if err := s.applyTemplates(filepath.Join(dir, "templates")); err != nil {
		return err
	}
	if err := s.appendFiles(dir); err != nil {
		return err
	}
	return nil
}

func (s *templateState) copyNewFiles(root string) error {
	return walkExistingFiles(root, func(path string, rel string) error {
		dst := filepath.Join(s.workDir, rel)
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("%s would overwrite %s; use replace/ for overrides", path, dst)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return fsutil.CopyFile(path, dst)
	})
}

func (s *templateState) replaceFiles(root string) error {
	return walkExistingFiles(root, func(path string, rel string) error {
		dst := filepath.Join(s.workDir, rel)
		return fsutil.CopyFile(path, dst)
	})
}

func (s *templateState) appendFiles(dir string) error {
	if err := walkExistingFiles(filepath.Join(dir, "append"), func(path string, rel string) error {
		return s.appendTo(rel, path)
	}); err != nil {
		return err
	}
	entries, err := readDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.Contains(entry.Name(), ".append.") {
			continue
		}
		target := pathutil.AppendTargetName(entry.Name())
		if err := s.appendTo(target, filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *templateState) appendTo(rel string, source string) error {
	dst, err := pathutil.SafeJoin(s.workDir, rel)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(dst)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	separator := ""
	if strings.TrimSpace(string(existing)) != "" {
		separator = appendSeparator
	}
	content := strings.TrimRight(string(existing), "\n") + separator + strings.TrimSpace(string(data)) + "\n"
	if err := mkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, []byte(content), 0o644)
}

func (s *templateState) applyPatches(root string) error {
	return walkExistingFiles(root, func(path string, _ string) error {
		var spec struct {
			Target     string          `json:"target"`
			Operations json.RawMessage `json:"operations"`
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &spec); err != nil {
			return err
		}
		if spec.Target == "" || !strings.HasSuffix(spec.Target, ".json") {
			return fmt.Errorf("JSON Patch target must be a .json file: %s", spec.Target)
		}
		target, err := pathutil.SafeJoin(s.workDir, spec.Target)
		if err != nil {
			return err
		}
		original := []byte("{}")
		if data, err := os.ReadFile(target); err == nil {
			original = data
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		patch, err := jsonpatch.DecodePatch(spec.Operations)
		if err != nil {
			return err
		}
		patched, err := patch.Apply(original)
		if err != nil {
			return err
		}
		if err := mkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, patched, "", "  "); err == nil {
			pretty.WriteByte('\n')
			patched = pretty.Bytes()
		}
		return os.WriteFile(target, patched, 0o644)
	})
}

func (s *templateState) applyTemplates(root string) error {
	return walkExistingFiles(root, func(path string, rel string) error {
		if !strings.HasSuffix(rel, ".j2") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if looksLikeJSON(raw) {
			return fmt.Errorf("template overlay %s must contain Jinja template content, not a JSON variable map", path)
		}
		rel = filepath.ToSlash(rel)
		parentName := s.currentByPath[rel]
		content := string(raw)
		if parentName != "" {
			content = replaceParentReference(content, rel, parentName)
		}
		s.layer++
		name := templateKey(fmt.Sprintf("layer%d/%s", s.layer, rel))
		s.contents[name] = content
		s.contents[templateKey("overlay/"+rel)] = string(raw)
		s.currentByPath[rel] = name
		return s.renderTemplate(name, rel)
	})
}

func (s *templateState) renderTemplate(name string, rel string) error {
	loader := loaders.MustNewMemoryLoader(s.contents)
	tmpl, err := gonjaexec.NewTemplate(name, gonja.DefaultConfig, loader, gonja.DefaultEnvironment)
	if err != nil {
		return err
	}
	ctx := gonjaexec.NewContext(map[string]any{
		"skill":        s.skill,
		"agent":        s.agent,
		"base_file":    s.baseFile,
		"current_file": s.currentFile,
	})
	out, err := tmpl.ExecuteToString(ctx)
	if err != nil {
		return err
	}
	targetRel := pathutil.StripJ2(filepath.FromSlash(rel))
	target, err := pathutil.SafeJoin(s.workDir, targetRel)
	if err != nil {
		return err
	}
	if err := mkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(out), 0o644)
}

func (s *templateState) baseFile(path string) (string, error) {
	target, err := pathutil.SafeJoin(s.baseDir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *templateState) currentFile(path string) (string, error) {
	target, err := pathutil.SafeJoin(s.workDir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func walkExistingFiles(root string, fn func(path string, rel string) error) error {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	files, err := fsutil.WalkFiles(root)
	if err != nil {
		return err
	}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		if err := fn(path, rel); err != nil {
			return err
		}
	}
	return nil
}

func replaceParentReference(content string, rel string, parentName string) string {
	content = strings.ReplaceAll(content, "\"parent/"+rel+"\"", "\""+parentName+"\"")
	content = strings.ReplaceAll(content, "'parent/"+rel+"'", "'"+parentName+"'")
	return content
}

func templateKey(path string) string {
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

func looksLikeJSON(content []byte) bool {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return false
	}
	return json.Valid([]byte(trimmed)) && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "["))
}

func copyToAgentOutput(skill string, rendered string, output config.AgentOutput) (string, bool, error) {
	dst := filepath.Join(output.Path, skill)
	if copiedOutputCurrent(rendered, dst) {
		return dst, false, nil
	}
	if info, err := os.Stat(dst); err == nil {
		if !info.IsDir() {
			return "", false, fmt.Errorf("%s exists and is not a directory", dst)
		}
		if err := removeAll(dst); err != nil {
			return "", false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if err := mkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", false, err
	}
	if err := fsutil.CopyDir(rendered, dst, nil); err != nil {
		return "", false, err
	}
	return dst, true, nil
}

func copiedOutputCurrent(rendered string, dst string) bool {
	renderedMarker, err := readMarker(filepath.Join(rendered, MarkerFile))
	if err != nil {
		return false
	}
	dstMarker, err := readMarker(filepath.Join(dst, MarkerFile))
	if err != nil {
		return false
	}
	return renderedMarker.SchemaVersion == dstMarker.SchemaVersion &&
		renderedMarker.Skill == dstMarker.Skill &&
		renderedMarker.Agent == dstMarker.Agent &&
		renderedMarker.Mode == dstMarker.Mode &&
		renderedMarker.BasePath == dstMarker.BasePath &&
		renderedMarker.BaseHash == dstMarker.BaseHash &&
		equalStrings(renderedMarker.Overlays, dstMarker.Overlays) &&
		renderedMarker.OverlayHash == dstMarker.OverlayHash
}

func writeMarker(dir string, skill string, agent string, mode string, baseDir string, baseHash string, overlays []string, overlayDigest string) error {
	m := marker{
		SchemaVersion: markerSchemaVersion,
		Skill:         skill,
		Agent:         agent,
		Mode:          mode,
		BasePath:      baseDir,
		BaseHash:      "sha256:" + baseHash,
		Overlays:      overlays,
		OverlayHash:   "sha256:" + overlayDigest,
		RenderedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, MarkerFile), data, 0o644)
}

func readMarker(path string) (marker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return marker{}, err
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil {
		return marker{}, err
	}
	return m, nil
}

func renderCurrent(dir string, skill string, agent string, mode string, baseDir string, baseHash string, overlays []string, overlayDigest string) bool {
	m, err := readMarker(filepath.Join(dir, MarkerFile))
	if err != nil {
		return false
	}
	return m.SchemaVersion == markerSchemaVersion &&
		m.Skill == skill &&
		m.Agent == agent &&
		m.Mode == mode &&
		m.BasePath == baseDir &&
		m.BaseHash == "sha256:"+baseHash &&
		equalStrings(m.Overlays, overlays) &&
		m.OverlayHash == "sha256:"+overlayDigest
}

func treeHash(root string) (string, error) {
	hash := sha256.New()
	files, err := fsutil.WalkFiles(root)
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, file := range files {
		rel, _ := filepath.Rel(root, file)
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func overlayHash(paths []string) string {
	hash := sha256.New()
	for _, root := range paths {
		hash.Write([]byte(root))
		files, err := fsutil.WalkFiles(root)
		if err != nil {
			continue
		}
		sort.Strings(files)
		for _, file := range files {
			rel, _ := filepath.Rel(root, file)
			hash.Write([]byte(filepath.ToSlash(rel)))
			hash.Write([]byte{0})
			if data, err := os.ReadFile(file); err == nil {
				hash.Write(data)
				hash.Write([]byte{0})
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func diffDirs(base string, rendered string) (string, error) {
	relSet := map[string]struct{}{}
	for _, root := range []string{base, rendered} {
		files, err := fsutil.WalkFiles(root)
		if err != nil {
			return "", err
		}
		for _, file := range files {
			if filepath.Base(file) == MarkerFile {
				continue
			}
			rel, _ := filepath.Rel(root, file)
			relSet[rel] = struct{}{}
		}
	}
	var rels []string
	for rel := range relSet {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	var out strings.Builder
	for _, rel := range rels {
		before, _ := os.ReadFile(filepath.Join(base, rel))
		after, _ := os.ReadFile(filepath.Join(rendered, rel))
		if bytes.Equal(before, after) {
			continue
		}
		out.WriteString("--- " + filepath.Join(base, rel) + "\n")
		out.WriteString("+++ " + filepath.Join(rendered, rel) + "\n")
		out.WriteString("@@\n")
		for _, line := range strings.SplitAfter(string(before), "\n") {
			if line != "" {
				out.WriteString("-" + line)
			}
		}
		for _, line := range strings.SplitAfter(string(after), "\n") {
			if line != "" {
				out.WriteString("+" + line)
			}
		}
	}
	return out.String(), nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
