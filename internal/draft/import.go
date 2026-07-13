package draft

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"upskill/internal/config"
	"upskill/internal/pathutil"
)

const (
	maxArchiveBytes   = 50 * 1024 * 1024
	maxExtractedBytes = 200 * 1024 * 1024
	maxSingleFile     = 20 * 1024 * 1024
	maxImportFiles    = 2000
)

var httpGet = http.Get

func noopCleanup() {
	return
}

type ImportOptions struct {
	ArchivePath       string
	ArchiveURL        string
	FilePath          string
	FileURL           string
	DestPath          string
	SHA256            string
	StripComponents   int
	Inspect           bool
	EnforceImportDirs bool
}

type ImportedFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type ImportResult struct {
	DraftID  string         `json:"draft_id"`
	DryRun   bool           `json:"dry_run"`
	Imported bool           `json:"imported"`
	Source   string         `json:"source"`
	Files    []ImportedFile `json:"files"`
	Warnings []string       `json:"warnings"`
}

type archiveEntry struct {
	Path string
	Data []byte
	Dir  bool
}

func Import(cfg config.Config, name string, opts ImportOptions) (ImportResult, error) {
	item, err := Read(cfg, name)
	if err != nil {
		return ImportResult{}, err
	}
	selected := 0
	for _, value := range []string{opts.ArchivePath, opts.ArchiveURL, opts.FilePath, opts.FileURL} {
		if value != "" {
			selected++
		}
	}
	if selected != 1 {
		return ImportResult{}, fmt.Errorf("exactly one import source is required")
	}
	switch {
	case opts.ArchivePath != "":
		path, err := localImportPath(cfg, opts.ArchivePath, opts.EnforceImportDirs)
		if err != nil {
			return ImportResult{}, err
		}
		return importArchivePath(item, path, opts)
	case opts.ArchiveURL != "":
		path, cleanup, err := download(opts.ArchiveURL, opts.SHA256)
		if err != nil {
			return ImportResult{}, err
		}
		defer cleanup()
		return importArchivePath(item, path, opts)
	case opts.FilePath != "":
		path, err := localImportPath(cfg, opts.FilePath, opts.EnforceImportDirs)
		if err != nil {
			return ImportResult{}, err
		}
		return importSingleFile(item, path, opts)
	default:
		path, cleanup, err := download(opts.FileURL, opts.SHA256)
		if err != nil {
			return ImportResult{}, err
		}
		defer cleanup()
		return importSingleFile(item, path, opts)
	}
}

func importArchivePath(item Draft, path string, opts ImportOptions) (ImportResult, error) {
	if err := verifyFile(path, opts.SHA256); err != nil {
		return ImportResult{}, err
	}
	entries, err := readArchive(path)
	if err != nil {
		return ImportResult{}, err
	}
	files, warnings, err := planArchiveImport(item.Path, entries, opts.StripComponents)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{DraftID: item.ID, DryRun: opts.Inspect, Imported: !opts.Inspect, Source: path, Files: files, Warnings: warnings}
	if opts.Inspect {
		return result, nil
	}
	byPath := map[string]archiveEntry{}
	for _, entry := range entries {
		rel, ok := archiveOutputPath(entry.Path, opts.StripComponents, files)
		if ok && !entry.Dir {
			byPath[rel] = entry
		}
	}
	for _, file := range files {
		entry := byPath[file.Path]
		target := filepath.Join(item.Path, filepath.FromSlash(file.Path))
		if err := osMkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ImportResult{}, err
		}
		if err := osWriteFile(target, entry.Data, 0o644); err != nil {
			return ImportResult{}, err
		}
	}
	return result, nil
}

func importSingleFile(item Draft, path string, opts ImportOptions) (ImportResult, error) {
	if opts.DestPath == "" {
		return ImportResult{}, fmt.Errorf("--path is required for file imports")
	}
	if err := verifyFile(path, opts.SHA256); err != nil {
		return ImportResult{}, err
	}
	data, err := osReadFile(path)
	if err != nil {
		return ImportResult{}, err
	}
	if len(data) > maxSingleFile {
		return ImportResult{}, fmt.Errorf("file is too large: %s", path)
	}
	target, err := pathutil.SafeJoin(item.Path, opts.DestPath)
	if err != nil {
		return ImportResult{}, err
	}
	_, statErr := os.Stat(target)
	file := ImportedFile{Path: filepath.ToSlash(filepath.Clean(opts.DestPath)), Size: int64(len(data)), Overwrite: statErr == nil}
	result := ImportResult{DraftID: item.ID, DryRun: opts.Inspect, Imported: !opts.Inspect, Source: path, Files: []ImportedFile{file}}
	if opts.Inspect {
		return result, nil
	}
	if err := osMkdirAll(filepath.Dir(target), 0o755); err != nil {
		return ImportResult{}, err
	}
	if err := osWriteFile(target, data, 0o644); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func readArchive(path string) ([]archiveEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxArchiveBytes {
		return nil, fmt.Errorf("archive is too large: %s", path)
	}
	switch {
	case strings.HasSuffix(path, ".zip"):
		return readZip(path)
	case strings.HasSuffix(path, ".tar.gz"), strings.HasSuffix(path, ".tgz"):
		return readTarGZ(path)
	default:
		return nil, fmt.Errorf("unsupported archive type: %s", path)
	}
}

func readZip(path string) ([]archiveEntry, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var entries []archiveEntry
	var total int64
	for _, file := range reader.File {
		if len(entries) >= maxImportFiles {
			return nil, fmt.Errorf("archive has too many files")
		}
		if err := validateArchivePath(file.Name); err != nil {
			return nil, err
		}
		if isIgnoredArchiveMetadata(file.Name) {
			continue
		}
		mode := file.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeType != 0 && !mode.IsDir() {
			return nil, fmt.Errorf("unsupported archive entry: %s", file.Name)
		}
		entry := archiveEntry{Path: filepath.ToSlash(file.Name), Dir: file.FileInfo().IsDir()}
		if !entry.Dir {
			if file.UncompressedSize64 > maxSingleFile {
				return nil, fmt.Errorf("archive file is too large: %s", file.Name)
			}
			total += int64(file.UncompressedSize64)
			if total > maxExtractedBytes {
				return nil, fmt.Errorf("archive expands too large")
			}
			data, err := readZipFile(file)
			if err != nil {
				return nil, err
			}
			entry.Data = data
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, maxSingleFile+1))
}

func readTarGZ(path string) ([]archiveEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var entries []archiveEntry
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(entries) >= maxImportFiles {
			return nil, fmt.Errorf("archive has too many files")
		}
		if err := validateArchivePath(header.Name); err != nil {
			return nil, err
		}
		if isIgnoredArchiveMetadata(header.Name) {
			continue
		}
		entry := archiveEntry{Path: filepath.ToSlash(header.Name), Dir: header.Typeflag == tar.TypeDir}
		switch header.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg, tar.TypeRegA:
			if header.Size > maxSingleFile {
				return nil, fmt.Errorf("archive file is too large: %s", header.Name)
			}
			total += header.Size
			if total > maxExtractedBytes {
				return nil, fmt.Errorf("archive expands too large")
			}
			data, err := io.ReadAll(io.LimitReader(reader, maxSingleFile+1))
			if err != nil {
				return nil, err
			}
			entry.Data = data
		default:
			return nil, fmt.Errorf("unsupported archive entry: %s", header.Name)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func isIgnoredArchiveMetadata(path string) bool {
	path = filepath.ToSlash(path)
	if path == "__MACOSX" || strings.HasPrefix(path, "__MACOSX/") {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}

func planArchiveImport(root string, entries []archiveEntry, strip int) ([]ImportedFile, []string, error) {
	if strip < 0 {
		return nil, nil, fmt.Errorf("strip components must be non-negative")
	}
	candidates := map[string]struct{}{}
	for _, entry := range entries {
		if entry.Dir {
			continue
		}
		rel, ok := stripPath(entry.Path, strip)
		if !ok {
			continue
		}
		if rel == "SKILL.md" || strings.HasSuffix(rel, "/SKILL.md") {
			candidates[strings.TrimSuffix(rel, "SKILL.md")] = struct{}{}
		}
	}
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("imported archive must contain SKILL.md")
	}
	if len(candidates) > 1 {
		return nil, nil, fmt.Errorf("archive contains multiple skill roots")
	}
	rootPrefix := ""
	for candidate := range candidates {
		rootPrefix = candidate
	}
	var files []ImportedFile
	var warnings []string
	for _, entry := range entries {
		if entry.Dir {
			continue
		}
		rel, ok := stripPath(entry.Path, strip)
		if !ok || !strings.HasPrefix(rel, rootPrefix) {
			continue
		}
		out := strings.TrimPrefix(rel, rootPrefix)
		if _, err := pathutil.SafeJoin(root, out); err != nil {
			return nil, nil, err
		}
		target := filepath.Join(root, filepath.FromSlash(out))
		_, statErr := os.Stat(target)
		file := ImportedFile{Path: out, Size: int64(len(entry.Data)), Overwrite: statErr == nil}
		if file.Overwrite {
			warnings = append(warnings, "will overwrite "+out)
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(warnings)
	return files, warnings, nil
}

func archiveOutputPath(path string, strip int, files []ImportedFile) (string, bool) {
	rel, ok := stripPath(path, strip)
	if !ok {
		return "", false
	}
	for _, file := range files {
		if strings.HasSuffix(rel, file.Path) {
			return file.Path, true
		}
	}
	return "", false
}

func stripPath(path string, strip int) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if strip > len(parts) {
		return "", false
	}
	return strings.Join(parts[strip:], "/"), true
}

func validateArchivePath(path string) error {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) || path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe archive path: %s", path)
	}
	return nil
}

func verifyFile(path string, expected string) error {
	if expected == "" {
		return nil
	}
	data, err := osReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	expected = strings.TrimPrefix(strings.ToLower(expected), "sha256:")
	if got != expected {
		return fmt.Errorf("sha256 mismatch for %s: got %s", path, got)
	}
	return nil
}

func localImportPath(cfg config.Config, path string, enforce bool) (string, error) {
	if enforce {
		return config.ImportPathAllowed(cfg, path)
	}
	return filepath.Abs(path)
}

func download(rawURL string, expected string) (string, func(), error) {
	if rawURL == "" {
		return "", noopCleanup, fmt.Errorf("url is required")
	}
	resp, err := httpGet(rawURL)
	if err != nil {
		return "", noopCleanup, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", noopCleanup, fmt.Errorf("download failed with status %s", resp.Status)
	}
	ext := filepath.Ext(rawURL)
	if strings.HasSuffix(rawURL, ".tar.gz") {
		ext = ".tar.gz"
	}
	tmp, err := osCreateTemp("", "upskill-import-*"+ext)
	if err != nil {
		return "", noopCleanup, err
	}
	defer tmp.Close()
	limit := int64(maxArchiveBytes)
	if ext != ".zip" && ext != ".tar.gz" && ext != ".tgz" {
		limit = maxSingleFile
	}
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		_ = osRemove(tmp.Name())
		return "", noopCleanup, err
	}
	if written > limit {
		_ = osRemove(tmp.Name())
		return "", noopCleanup, fmt.Errorf("download is too large: %s", strconv.FormatInt(written, 10))
	}
	if err := verifyFile(tmp.Name(), expected); err != nil {
		_ = osRemove(tmp.Name())
		return "", noopCleanup, err
	}
	return tmp.Name(), func() { _ = osRemove(tmp.Name()) }, nil
}
