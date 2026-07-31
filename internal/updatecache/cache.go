package updatecache

import (
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
)

const SchemaVersion = 1

type SkillResult struct {
	Name             string `json:"name"`
	CurrentRevision  string `json:"current_revision,omitempty"`
	ResolvedRevision string `json:"resolved_revision,omitempty"`
	CurrentHash      string `json:"current_hash,omitempty"`
	NewHash          string `json:"new_hash,omitempty"`
	Changed          bool   `json:"changed"`
}

type Record struct {
	SchemaVersion    int                    `json:"schema_version"`
	CheckedAt        string                 `json:"checked_at,omitempty"`
	LastAttemptAt    string                 `json:"last_attempt_at,omitempty"`
	RetryAfter       string                 `json:"retry_after,omitempty"`
	LastFailure      string                 `json:"last_failure,omitempty"`
	ResolvedRevision string                 `json:"resolved_revision,omitempty"`
	LockFingerprint  string                 `json:"lock_fingerprint"`
	Checks           map[string]SkillResult `json:"checks,omitempty"`
}

type FingerprintEntry struct {
	StoreName        string
	SourceType       string
	SourceURL        string
	Ref              string
	SourceSubpath    string
	SkillSubpath     string
	ResolvedRevision string
	Hash             string
	UpdatePolicy     string
}

type Store struct {
	Dir string
}

func New(home string) Store {
	if strings.TrimSpace(home) == "" {
		return Store{}
	}
	return Store{Dir: filepath.Join(home, "cache", "update-checks")}
}

func Identity(sourceType string, sourceURL string, ref string) string {
	return strings.Join([]string{sourceType, sourceURL, ref}, "\x00")
}

func SourceKey(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func FileKey(identity string) string {
	return strings.TrimPrefix(SourceKey(identity), "sha256:")
}

func Fingerprint(entries []FingerprintEntry) string {
	ordered := append([]FingerprintEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		return fingerprintLine(ordered[i]) < fingerprintLine(ordered[j])
	})
	var lines []string
	for _, entry := range ordered {
		lines = append(lines, fingerprintLine(entry))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\x1e")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fingerprintLine(entry FingerprintEntry) string {
	return strings.Join([]string{
		entry.StoreName,
		entry.SourceType,
		entry.SourceURL,
		entry.Ref,
		entry.SourceSubpath,
		entry.SkillSubpath,
		entry.ResolvedRevision,
		entry.Hash,
		entry.UpdatePolicy,
	}, "\x00")
}

func (store Store) Path(identity string) string {
	return filepath.Join(store.Dir, FileKey(identity)+".json")
}

func (store Store) Read(identity string) (Record, bool, error) {
	if strings.TrimSpace(store.Dir) == "" {
		return Record{}, false, nil
	}
	data, err := os.ReadFile(store.Path(identity))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, false, fmt.Errorf("parse update-check cache: %w", err)
	}
	if err := Validate(record); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

func Validate(record Record) error {
	if record.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported update-check cache schema %d", record.SchemaVersion)
	}
	if record.LockFingerprint == "" {
		return fmt.Errorf("update-check cache lock fingerprint is missing")
	}
	for name, value := range map[string]string{
		"checked_at":      record.CheckedAt,
		"last_attempt_at": record.LastAttemptAt,
		"retry_after":     record.RetryAfter,
	} {
		if value == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("invalid update-check cache %s: %w", name, err)
		}
	}
	for key, check := range record.Checks {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(check.Name) == "" {
			return fmt.Errorf("malformed update-check cache result")
		}
	}
	return nil
}

func (store Store) Write(identity string, record Record) error {
	if strings.TrimSpace(store.Dir) == "" {
		return fmt.Errorf("update-check cache directory is required")
	}
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(store.Dir, 0o700); err != nil {
		return err
	}
	record.SchemaVersion = SchemaVersion
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(store.Dir, ".update-check-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	path := store.Path(identity)
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
