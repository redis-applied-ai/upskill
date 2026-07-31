package updatecache

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestIdentityKeysAndFingerprintsAreDeterministic(t *testing.T) {
	identity := Identity("git", "https://example.com/team/private.git", "main")
	if identity != Identity("git", "https://example.com/team/private.git", "main") {
		t.Fatal("materialization identity is not deterministic")
	}
	key := SourceKey(identity)
	if !strings.HasPrefix(key, "sha256:") || strings.Contains(key, "example.com") {
		t.Fatalf("source key = %q", key)
	}

	entries := []FingerprintEntry{
		{StoreName: "two", SourceType: "git", SourceURL: "url", Ref: "main", SourceSubpath: "skills", SkillSubpath: "two", ResolvedRevision: "rev", Hash: "hash2", UpdatePolicy: "auto"},
		{StoreName: "one", SourceType: "git", SourceURL: "url", Ref: "main", SourceSubpath: "skills", SkillSubpath: "one", ResolvedRevision: "rev", Hash: "hash1", UpdatePolicy: "manual"},
	}
	first := Fingerprint(entries)
	second := Fingerprint([]FingerprintEntry{entries[1], entries[0]})
	if first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("fingerprints = %q, %q", first, second)
	}
	changed := append([]FingerprintEntry(nil), entries...)
	changed[0].SkillSubpath = "moved"
	if Fingerprint(changed) == first {
		t.Fatal("skill subpath did not affect fingerprint")
	}
}

func TestStoreReadWriteValidationAndPermissions(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	identity := Identity("git", "https://example.com/private.git", "main")
	record := Record{
		CheckedAt:        "2026-07-30T18:30:00Z",
		LastAttemptAt:    "2026-07-30T18:30:00Z",
		ResolvedRevision: "abc",
		LockFingerprint:  "sha256:fingerprint",
		Checks: map[string]SkillResult{
			"redis": {Name: "Redis", CurrentHash: "old", NewHash: "new", Changed: true},
		},
	}
	if err := store.Write(identity, record); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("cache directory mode = %o", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(store.Path(identity))
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode = %o", fileInfo.Mode().Perm())
	}
	got, ok, err := store.Read(identity)
	if err != nil || !ok {
		t.Fatalf("Read() = %#v, %t, %v", got, ok, err)
	}
	if got.SchemaVersion != SchemaVersion || !got.Checks["redis"].Changed {
		t.Fatalf("record = %#v", got)
	}

	if _, ok, err := store.Read(Identity("git", "missing", "")); err != nil || ok {
		t.Fatalf("missing Read() ok=%t err=%v", ok, err)
	}

	path := store.Path(identity)
	if err := os.WriteFile(path, []byte(`{"schema_version":99,"lock_fingerprint":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Read(identity); err == nil || ok {
		t.Fatalf("wrong schema Read() ok=%t err=%v", ok, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("invalid cache was deleted: %v", err)
	}

	if err := os.WriteFile(path, []byte(`{"schema_version":1,"checked_at":"bad","lock_fingerprint":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Read(identity); err == nil || ok {
		t.Fatalf("bad timestamp Read() ok=%t err=%v", ok, err)
	}

	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Read(identity); err == nil || ok {
		t.Fatalf("malformed Read() ok=%t err=%v", ok, err)
	}
}

func TestStoreDoesNotSerializeIdentityOrRawFailures(t *testing.T) {
	store := New(t.TempDir())
	identity := Identity("git", "https://token@example.com/private.git", "main")
	record := Record{
		LastAttemptAt:   "2026-07-30T18:30:00Z",
		RetryAfter:      "2026-07-30T18:35:00Z",
		LastFailure:     "remote_probe_failed",
		LockFingerprint: "sha256:fingerprint",
	}
	if err := store.Write(identity, record); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path(identity))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"token@", "example.com", "private.git", "git ls-remote", "fatal:"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("cache contains %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "remote_probe_failed") {
		t.Fatalf("cache = %s", text)
	}
	if strings.Contains(filepath.Base(store.Path(identity)), "private") {
		t.Fatalf("cache filename = %s", store.Path(identity))
	}
}

func TestConcurrentReadersNeverObservePartialJSON(t *testing.T) {
	store := New(t.TempDir())
	identity := Identity("git", "https://example.com/repo.git", "main")
	record := func(revision string) Record {
		return Record{
			CheckedAt:        "2026-07-30T18:30:00Z",
			LastAttemptAt:    "2026-07-30T18:30:00Z",
			ResolvedRevision: revision,
			LockFingerprint:  "sha256:fingerprint",
			Checks: map[string]SkillResult{
				"redis": {Name: "Redis", ResolvedRevision: revision},
			},
		}
	}
	if err := store.Write(identity, record("a")); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				got, ok, err := store.Read(identity)
				if err != nil {
					errs <- err
					return
				}
				if !ok || (got.ResolvedRevision != "a" && got.ResolvedRevision != "b") {
					errs <- os.ErrInvalid
					return
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		revision := "a"
		if i%2 == 0 {
			revision = "b"
		}
		if err := store.Write(identity, record(revision)); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
