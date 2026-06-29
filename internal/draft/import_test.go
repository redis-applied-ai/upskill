package draft

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"upskill/internal/config"
)

func TestImportZipArchive(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(root, "demo.zip")
	writeZip(t, zipPath, map[string]string{
		"demo/SKILL.md":          "# Demo\n",
		"demo/references/api.md": "API\n",
	})
	writeFile(t, filepath.Join(root, "drafts", "demo", "references", "api.md"), "old\n")

	inspect, err := Import(cfg, "demo", ImportOptions{ArchivePath: zipPath, Inspect: true})
	if err != nil {
		t.Fatal(err)
	}
	if inspect.Imported || len(inspect.Warnings) != 1 || !strings.Contains(inspect.Warnings[0], "references/api.md") {
		t.Fatalf("inspect = %#v", inspect)
	}
	if got := readFile(t, filepath.Join(root, "drafts", "demo", "references", "api.md")); got != "old\n" {
		t.Fatalf("inspect wrote file: %q", got)
	}

	result, err := Import(cfg, "demo", ImportOptions{ArchivePath: zipPath})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Imported || len(result.Files) != 2 {
		t.Fatalf("result = %#v", result)
	}
	if got := readFile(t, filepath.Join(root, "drafts", "demo", "references", "api.md")); got != "API\n" {
		t.Fatalf("imported file = %q", got)
	}
}

func TestImportTarGZArchiveWithStrip(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "demo.tar.gz")
	writeTarGZ(t, archivePath, map[string]string{
		"bundle/skill/SKILL.md":       "# Demo\n",
		"bundle/skill/examples/a.txt": "A\n",
	})
	result, err := Import(cfg, "demo", ImportOptions{ArchivePath: archivePath, StripComponents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("files = %#v", result.Files)
	}
	if got := readFile(t, filepath.Join(root, "drafts", "demo", "examples", "a.txt")); got != "A\n" {
		t.Fatalf("tar import = %q", got)
	}
}

func TestImportArchiveSkipsMacOSMetadata(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	for _, tc := range []struct {
		name  string
		setup func(string)
	}{
		{
			name: "zip",
			setup: func(path string) {
				writeZip(t, path, map[string]string{
					"bundle/SKILL.md":                   "# Demo\n",
					"bundle/._SKILL.md":                 "resource fork",
					"bundle/references/api.md":          "API\n",
					"bundle/references/._api.md":        "resource fork",
					"__MACOSX/bundle/._SKILL.md":        "metadata",
					"__MACOSX/bundle/references/api.md": "metadata",
				})
			},
		},
		{
			name: "tar.gz",
			setup: func(path string) {
				writeTarGZ(t, path, map[string]string{
					"bundle/SKILL.md":                   "# Demo\n",
					"bundle/._SKILL.md":                 "resource fork",
					"bundle/references/api.md":          "API\n",
					"bundle/references/._api.md":        "resource fork",
					"__MACOSX/bundle/._SKILL.md":        "metadata",
					"__MACOSX/bundle/references/api.md": "metadata",
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draftName := "demo-" + strings.ReplaceAll(tc.name, ".", "-")
			if _, err := Save(cfg, Metadata{Name: draftName, Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
				t.Fatal(err)
			}
			archivePath := filepath.Join(root, draftName+"."+tc.name)
			tc.setup(archivePath)
			result, err := Import(cfg, draftName, ImportOptions{ArchivePath: archivePath})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Files) != 2 {
				t.Fatalf("files = %#v", result.Files)
			}
			draftDir := filepath.Join(root, "drafts", draftName)
			if got := readFile(t, filepath.Join(draftDir, "references", "api.md")); got != "API\n" {
				t.Fatalf("api file = %q", got)
			}
			for _, path := range []string{
				filepath.Join(draftDir, "._SKILL.md"),
				filepath.Join(draftDir, "references", "._api.md"),
				filepath.Join(draftDir, "__MACOSX"),
			} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("metadata path exists: %s", path)
				}
			}
		})
	}
}

func TestImportSingleFileAndURL(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "api.md")
	writeFile(t, source, "API\n")
	result, err := Import(cfg, "demo", ImportOptions{FilePath: source, DestPath: "references/api.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Imported || result.Files[0].Path != "references/api.md" {
		t.Fatalf("file result = %#v", result)
	}

	body := []byte("Remote\n")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	result, err = Import(cfg, "demo", ImportOptions{FileURL: server.URL + "/remote.md", DestPath: "references/remote.md", SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source == "" || readFile(t, filepath.Join(root, "drafts", "demo", "references", "remote.md")) != "Remote\n" {
		t.Fatalf("url result = %#v", result)
	}
	if _, err := Import(cfg, "demo", ImportOptions{FileURL: server.URL + "/remote.md", DestPath: "bad.md", SHA256: "sha256:bad"}); err == nil {
		t.Fatal("expected checksum error")
	}
}

func TestImportErrorsAndAllowlist(t *testing.T) {
	root := t.TempDir()
	imports := filepath.Join(root, "imports")
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts"), ImportsDir: imports, ImportDirs: []string{imports}}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(imports, "demo.zip")
	writeZip(t, allowed, map[string]string{"SKILL.md": "# Demo\n"})
	if _, err := Import(cfg, "demo", ImportOptions{ArchivePath: allowed, EnforceImportDirs: true}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.zip")
	writeZip(t, outside, map[string]string{"SKILL.md": "# Demo\n"})
	if _, err := Import(cfg, "demo", ImportOptions{ArchivePath: outside, EnforceImportDirs: true}); err == nil {
		t.Fatal("expected import dir rejection")
	}
	outsideFile := filepath.Join(root, "outside.txt")
	writeFile(t, outsideFile, "outside")
	if _, err := Import(cfg, "demo", ImportOptions{FilePath: outsideFile, DestPath: "outside.txt", EnforceImportDirs: true}); err == nil {
		t.Fatal("expected file import dir rejection")
	}
	if _, err := Import(cfg, "demo", ImportOptions{}); err == nil {
		t.Fatal("expected missing source error")
	}
	if _, err := Import(cfg, "demo", ImportOptions{ArchivePath: allowed, FilePath: allowed}); err == nil {
		t.Fatal("expected multiple source error")
	}
	if _, err := Import(cfg, "missing", ImportOptions{ArchivePath: allowed}); err == nil {
		t.Fatal("expected missing draft error")
	}
	if _, err := Import(cfg, "demo", ImportOptions{FilePath: allowed}); err == nil {
		t.Fatal("expected missing file dest error")
	}
}

func TestArchiveValidationErrors(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	if _, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]string{
		"missing-skill.zip": {"README.md": "x"},
		"multi.zip":         {"one/SKILL.md": "x", "two/SKILL.md": "x"},
		"unsafe.zip":        {"../SKILL.md": "x"},
	}
	for name, files := range cases {
		path := filepath.Join(root, name)
		writeZip(t, path, files)
		if _, err := Import(cfg, "demo", ImportOptions{ArchivePath: path}); err == nil {
			t.Fatalf("expected error for %s", name)
		}
	}
	unsupported := filepath.Join(root, "bundle.rar")
	writeFile(t, unsupported, "x")
	if _, err := Import(cfg, "demo", ImportOptions{ArchivePath: unsupported}); err == nil {
		t.Fatal("expected unsupported archive error")
	}
	if _, err := Import(cfg, "demo", ImportOptions{ArchivePath: filepath.Join(root, "missing.zip")}); err == nil {
		t.Fatal("expected missing archive error")
	}
	zipPath := filepath.Join(root, "demo.zip")
	writeZip(t, zipPath, map[string]string{"SKILL.md": "x"})
	if _, err := Import(cfg, "demo", ImportOptions{ArchivePath: zipPath, StripComponents: -1}); err == nil {
		t.Fatal("expected negative strip error")
	}
	if _, err := Import(cfg, "demo", ImportOptions{FilePath: zipPath, DestPath: "../x"}); err == nil {
		t.Fatal("expected unsafe single file dest error")
	}
}

func TestRejectsZipSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "link.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "SKILL.md"}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("target"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(path); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestDownloadErrors(t *testing.T) {
	noopCleanup()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	if _, cleanup, err := download("", ""); err == nil {
		cleanup()
		t.Fatal("expected empty url error")
	}
	if _, cleanup, err := download(server.URL+"/missing", ""); err == nil {
		cleanup()
		t.Fatal("expected status error")
	}
}

func TestImportAdditionalBranches(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	item, err := Save(cfg, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton})
	if err != nil {
		t.Fatal(err)
	}

	archiveBytes := zipBytesForDraft(t, map[string]string{"SKILL.md": "# Remote\n", "references/r.md": "R\n"})
	archiveSum := sha256.Sum256(archiveBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bundle.zip":
			_, _ = w.Write(archiveBytes)
		case "/large":
			_, _ = w.Write(bytes.Repeat([]byte("x"), maxSingleFile+2))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := Import(cfg, "demo", ImportOptions{ArchiveURL: server.URL + "/bundle.zip", SHA256: hex.EncodeToString(archiveSum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Imported || readFile(t, filepath.Join(item.Path, "references", "r.md")) != "R\n" {
		t.Fatalf("archive url result = %#v", result)
	}
	if _, err := Import(cfg, "demo", ImportOptions{ArchiveURL: server.URL + "/missing"}); err == nil {
		t.Fatal("expected archive url status error")
	}
	if _, cleanup, err := download(server.URL+"/large", ""); err == nil {
		cleanup()
		t.Fatal("expected large download error")
	}
	oldHTTPGet := httpGet
	httpGet = func(string) (*http.Response, error) { return nil, errors.New("network failed") }
	t.Cleanup(func() { httpGet = oldHTTPGet })
	if _, cleanup, err := download(server.URL+"/bundle.zip", ""); err == nil {
		cleanup()
		t.Fatal("expected http get error")
	}
	httpGet = oldHTTPGet
	oldCreateTemp := osCreateTemp
	osCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("temp failed") }
	t.Cleanup(func() { osCreateTemp = oldCreateTemp })
	if _, cleanup, err := download(server.URL+"/bundle.zip", ""); err == nil {
		cleanup()
		t.Fatal("expected temp creation error")
	}
	osCreateTemp = oldCreateTemp
	httpGet = func(string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: failingBody{}}, nil
	}
	if _, cleanup, err := download(server.URL+"/bundle.zip", ""); err == nil {
		cleanup()
		t.Fatal("expected copy error")
	}
	httpGet = oldHTTPGet
	tarURL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tar"))
	}))
	defer tarURL.Close()
	path, cleanup, err := download(tarURL.URL+"/bundle.tar.gz", "")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if !strings.HasSuffix(path, ".tar.gz") {
		t.Fatalf("download extension = %s", path)
	}

	source := filepath.Join(root, "source.txt")
	writeFile(t, source, "Source\n")
	inspect, err := importSingleFile(item, source, ImportOptions{DestPath: "references/source.txt", Inspect: true})
	if err != nil {
		t.Fatal(err)
	}
	if inspect.Imported || len(inspect.Files) != 1 {
		t.Fatalf("inspect = %#v", inspect)
	}
	if _, err := importSingleFile(item, filepath.Join(root, "missing.txt"), ImportOptions{DestPath: "x.txt"}); err == nil {
		t.Fatal("expected single-file read error")
	}
	if _, err := importSingleFile(item, source, ImportOptions{DestPath: "bad.txt", SHA256: "sha256:bad"}); err == nil {
		t.Fatal("expected single-file checksum error")
	}
	large := filepath.Join(root, "large.txt")
	if err := os.WriteFile(large, bytes.Repeat([]byte("x"), maxSingleFile+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := importSingleFile(item, large, ImportOptions{DestPath: "large.txt"}); err == nil {
		t.Fatal("expected large single-file error")
	}
	blockedParent := filepath.Join(item.Path, "blocked")
	writeFile(t, blockedParent, "file")
	if _, err := importSingleFile(item, source, ImportOptions{DestPath: "blocked/file.txt"}); err == nil {
		t.Fatal("expected single-file mkdir error")
	}
	if err := os.MkdirAll(filepath.Join(item.Path, "as-dir.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := importSingleFile(item, source, ImportOptions{DestPath: "as-dir.txt"}); err == nil {
		t.Fatal("expected single-file write error")
	}
	if _, err := importArchivePath(item, filepath.Join(root, "missing.zip"), ImportOptions{SHA256: "sha256:bad"}); err == nil {
		t.Fatal("expected archive checksum read error")
	}
	blockedArchive := filepath.Join(root, "blocked.zip")
	writeZip(t, blockedArchive, map[string]string{"bundle/SKILL.md": "# Demo\n", "bundle/blocked/file.md": "x"})
	if _, err := importArchivePath(item, blockedArchive, ImportOptions{}); err == nil {
		t.Fatal("expected archive mkdir error")
	}
	writeArchive := filepath.Join(root, "write-error.zip")
	writeZip(t, writeArchive, map[string]string{"bundle/SKILL.md": "# Demo\n", "bundle/as-dir.txt": "x"})
	if _, err := importArchivePath(item, writeArchive, ImportOptions{}); err == nil {
		t.Fatal("expected archive write error")
	}
}

func TestArchiveReaderAdditionalBranches(t *testing.T) {
	root := t.TempDir()
	tooLarge := filepath.Join(root, "too-large.zip")
	file, err := os.Create(tooLarge)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxArchiveBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := readArchive(tooLarge); err == nil {
		t.Fatal("expected archive size error")
	}
	badZip := filepath.Join(root, "bad.zip")
	writeFile(t, badZip, "not zip")
	if _, err := readArchive(badZip); err == nil {
		t.Fatal("expected bad zip error")
	}
	tooMany := filepath.Join(root, "too-many.zip")
	writeManyZip(t, tooMany, maxImportFiles+1)
	if _, err := readZip(tooMany); err == nil {
		t.Fatal("expected too many zip entries")
	}
	tooExpanded := filepath.Join(root, "too-expanded.zip")
	writeExpandedZip(t, tooExpanded)
	if _, err := readZip(tooExpanded); err == nil {
		t.Fatal("expected expanded zip size error")
	}
	bigZip := filepath.Join(root, "big.zip")
	writeZipBytes(t, bigZip, map[string][]byte{"SKILL.md": bytes.Repeat([]byte("x"), maxSingleFile+1)})
	if _, err := readZip(bigZip); err == nil {
		t.Fatal("expected big zip file error")
	}
	unsupportedZip := filepath.Join(root, "unsupported.zip")
	writeUnsupportedMethodZip(t, unsupportedZip)
	if _, err := readZip(unsupportedZip); err == nil {
		t.Fatal("expected unsupported zip method error")
	}
	dirZip := filepath.Join(root, "dir.zip")
	writeZipWithDir(t, dirZip)
	entries, err := readZip(dirZip)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].Dir {
		t.Fatalf("dir entries = %#v", entries)
	}

	if _, err := readTarGZ(filepath.Join(root, "missing.tar.gz")); err == nil {
		t.Fatal("expected missing tar error")
	}
	badTar := filepath.Join(root, "bad.tar.gz")
	writeFile(t, badTar, "not gzip")
	if _, err := readTarGZ(badTar); err == nil {
		t.Fatal("expected bad gzip error")
	}
	badTarBody := filepath.Join(root, "bad-tar-body.tar.gz")
	writeGzipBody(t, badTarBody, []byte("not a tar stream"))
	if _, err := readTarGZ(badTarBody); err == nil {
		t.Fatal("expected tar read error")
	}
	tooManyTar := filepath.Join(root, "too-many.tar.gz")
	writeManyTarGZ(t, tooManyTar, maxImportFiles+1)
	if _, err := readTarGZ(tooManyTar); err == nil {
		t.Fatal("expected too many tar entries")
	}
	unsafeTar := filepath.Join(root, "unsafe.tar.gz")
	writeTarGZHeader(t, unsafeTar, &tar.Header{Name: "../SKILL.md", Typeflag: tar.TypeReg, Mode: 0o644})
	if _, err := readTarGZ(unsafeTar); err == nil {
		t.Fatal("expected unsafe tar path")
	}
	bigTar := filepath.Join(root, "big.tar.gz")
	writePartialTarGZHeader(t, bigTar, &tar.Header{Name: "SKILL.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: maxSingleFile + 1})
	if _, err := readTarGZ(bigTar); err == nil {
		t.Fatal("expected big tar file error")
	}
	truncatedTar := filepath.Join(root, "truncated.tar.gz")
	writeTruncatedTarGZ(t, truncatedTar)
	if _, err := readTarGZ(truncatedTar); err == nil {
		t.Fatal("expected tar readall error")
	}
	tooExpandedTar := filepath.Join(root, "too-expanded.tar.gz")
	writeExpandedTarGZ(t, tooExpandedTar)
	if _, err := readTarGZ(tooExpandedTar); err == nil {
		t.Fatal("expected expanded tar size error")
	}
	symlinkTar := filepath.Join(root, "symlink.tar.gz")
	writeTarGZHeader(t, symlinkTar, &tar.Header{Name: "SKILL.md", Typeflag: tar.TypeSymlink, Linkname: "x"})
	if _, err := readTarGZ(symlinkTar); err == nil {
		t.Fatal("expected unsupported tar entry")
	}
	dirTar := filepath.Join(root, "dir.tar.gz")
	writeTarGZDirAndFile(t, dirTar)
	tarEntries, err := readTarGZ(dirTar)
	if err != nil {
		t.Fatal(err)
	}
	if len(tarEntries) != 2 || !tarEntries[0].Dir {
		t.Fatalf("tar entries = %#v", tarEntries)
	}
}

func TestArchivePlanningAdditionalBranches(t *testing.T) {
	root := t.TempDir()
	entries := []archiveEntry{
		{Path: "skip/", Dir: true},
		{Path: "bundle/SKILL.md", Data: []byte("skill")},
		{Path: "other/file.md", Data: []byte("ignored")},
	}
	files, _, err := planArchiveImport(root, entries, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatalf("files = %#v", files)
	}
	if _, _, err := planArchiveImport(root, []archiveEntry{{Path: "a/SKILL.md"}, {Path: "b/SKILL.md"}}, 0); err == nil {
		t.Fatal("expected multiple roots")
	}
	if _, _, err := planArchiveImport(root, []archiveEntry{{Path: "bundle/SKILL.md"}, {Path: "bundle/../escape.md"}}, 0); err == nil {
		t.Fatal("expected unsafe planned output")
	}
	if _, _, err := planArchiveImport(root, []archiveEntry{{Path: "a/SKILL.md"}}, 3); err == nil {
		t.Fatal("expected no candidates after strip")
	}
	if path, ok := archiveOutputPath("a/b/c.md", 4, []ImportedFile{{Path: "c.md"}}); ok || path != "" {
		t.Fatalf("unexpected output path = %q %v", path, ok)
	}
	if path, ok := archiveOutputPath("a/b/c.md", 0, []ImportedFile{{Path: "other.md"}}); ok || path != "" {
		t.Fatalf("unexpected no-match output path = %q %v", path, ok)
	}
	if path, ok := stripPath("a/b", 3); ok || path != "" {
		t.Fatalf("unexpected stripped path = %q %v", path, ok)
	}
	if err := verifyFile(filepath.Join(root, "missing"), "sha256:abc"); err == nil {
		t.Fatal("expected verify read error")
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte(body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func zipBytesForDraft(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeZipBytes(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeManyZip(t *testing.T, path string, count int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for i := 0; i < count; i++ {
		entry, err := writer.Create("f" + strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZipWithDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	if _, err := writer.Create("references/"); err != nil {
		t.Fatal(err)
	}
	entry, err := writer.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("# Demo\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTarGZ(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for name, body := range files {
		data := []byte(body)
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}

func writeTarGZHeader(t *testing.T, path string, header *tar.Header) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}

func writeTarGZDirAndFile(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: "references", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	data := []byte("# Demo\n")
	if err := writer.WriteHeader(&tar.Header{Name: "SKILL.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}

func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("body failed") }
func (failingBody) Close() error             { return nil }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func writeUnsupportedMethodZip(t *testing.T, path string) {
	t.Helper()
	writeZip(t, path, map[string]string{"SKILL.md": "x"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patchZipMethod(t, data, 99)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZipWithPatchedSizes(t *testing.T, path string, count int, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for i := 0; i < count; i++ {
		entry, err := writer.Create("f" + strconv.Itoa(i) + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patchZipUncompressedSizes(t, data, uint32(size))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeExpandedZip(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for i := 0; i < 11; i++ {
		entry, err := writer.Create("f" + strconv.Itoa(i) + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.CopyN(entry, zeroReader{}, maxSingleFile); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func patchZipMethod(t *testing.T, data []byte, method uint16) {
	t.Helper()
	for i := 0; i+12 < len(data); i++ {
		switch string(data[i : i+4]) {
		case "PK\x03\x04":
			binary.LittleEndian.PutUint16(data[i+8:i+10], method)
		case "PK\x01\x02":
			binary.LittleEndian.PutUint16(data[i+10:i+12], method)
		}
	}
}

func patchZipUncompressedSizes(t *testing.T, data []byte, size uint32) {
	t.Helper()
	for i := 0; i+28 < len(data); i++ {
		switch string(data[i : i+4]) {
		case "PK\x03\x04":
			binary.LittleEndian.PutUint32(data[i+22:i+26], size)
		case "PK\x01\x02":
			binary.LittleEndian.PutUint32(data[i+24:i+28], size)
		}
	}
}

func writeGzipBody(t *testing.T, path string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}

func writeManyTarGZ(t *testing.T, path string, count int) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for i := 0; i < count; i++ {
		if err := writer.WriteHeader(&tar.Header{Name: "f" + strconv.Itoa(i) + ".md", Typeflag: tar.TypeReg, Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}

func writeTruncatedTarGZ(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: "SKILL.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}

func writePartialTarGZHeader(t *testing.T, path string, header *tar.Header) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}

func writeExpandedTarGZ(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for i := 0; i < 11; i++ {
		if err := writer.WriteHeader(&tar.Header{Name: "f" + strconv.Itoa(i) + ".md", Typeflag: tar.TypeReg, Mode: 0o644, Size: maxSingleFile}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.CopyN(writer, zeroReader{}, maxSingleFile); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buf.String())
}
