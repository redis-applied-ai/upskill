package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"upskill/internal/config"
	"upskill/internal/library"
)

const (
	MetadataFile  = ".upskill-draft.json"
	SchemaVersion = 1
	IDPrefix      = "draft:"

	TypeNew     = "new"
	TypeVersion = "version"

	CreatedFromBase     = "base"
	CreatedFromRendered = "rendered"
	CreatedFromSkeleton = "skeleton"
)

var (
	nowUTC       = func() time.Time { return time.Now().UTC() }
	osCreateTemp = os.CreateTemp
	osMkdirAll   = os.MkdirAll
	osMkdirTemp  = os.MkdirTemp
	osReadFile   = os.ReadFile
	osRemove     = os.Remove
	osRemoveAll  = os.RemoveAll
	osStat       = os.Stat
	osWriteFile  = os.WriteFile
)

type Metadata struct {
	SchemaVersion  int    `json:"schema_version"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	ParentSkill    string `json:"parent_skill,omitempty"`
	ParentSource   string `json:"parent_source,omitempty"`
	ParentRevision string `json:"parent_revision,omitempty"`
	ParentHash     string `json:"parent_hash,omitempty"`
	CreatedFrom    string `json:"created_from"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type Draft struct {
	ID        string   `json:"id"`
	StoreName string   `json:"store_name"`
	Path      string   `json:"path"`
	Metadata  Metadata `json:"metadata"`
}

func MakeID(name string) string {
	return IDPrefix + NameFromID(name)
}

func IsID(value string) bool {
	return strings.HasPrefix(value, IDPrefix)
}

func NameFromID(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), IDPrefix) {
		value = value[len(IDPrefix):]
	}
	return library.NormalizeName(value)
}

func Path(cfg config.Config, name string) string {
	return filepath.Join(cfg.DraftsDir, NameFromID(name))
}

func MetadataPath(cfg config.Config, name string) string {
	return filepath.Join(Path(cfg, name), MetadataFile)
}

func Exists(cfg config.Config, name string) bool {
	info, err := os.Stat(MetadataPath(cfg, name))
	return err == nil && !info.IsDir()
}

func Read(cfg config.Config, name string) (Draft, error) {
	storeName := NameFromID(name)
	if storeName == "" {
		return Draft{}, fmt.Errorf("draft name is required")
	}
	data, err := os.ReadFile(MetadataPath(cfg, storeName))
	if err != nil {
		return Draft{}, err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Draft{}, err
	}
	if err := validate(storeName, meta); err != nil {
		return Draft{}, err
	}
	return Draft{ID: MakeID(storeName), StoreName: storeName, Path: Path(cfg, storeName), Metadata: meta}, nil
}

func List(cfg config.Config) ([]Draft, error) {
	entries, err := os.ReadDir(cfg.DraftsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var drafts []Draft
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		item, err := Read(cfg, entry.Name())
		if err != nil {
			continue
		}
		drafts = append(drafts, item)
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].StoreName < drafts[j].StoreName })
	return drafts, nil
}

func Save(cfg config.Config, meta Metadata) (Draft, error) {
	storeName := NameFromID(meta.Name)
	if storeName == "" {
		return Draft{}, fmt.Errorf("draft name is required")
	}
	meta.Name = storeName
	if meta.SchemaVersion == 0 {
		meta.SchemaVersion = SchemaVersion
	}
	if meta.CreatedAt == "" {
		meta.CreatedAt = timestamp()
	}
	meta.UpdatedAt = timestamp()
	if err := validate(storeName, meta); err != nil {
		return Draft{}, err
	}
	root := Path(cfg, storeName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Draft{}, err
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, MetadataFile), data, 0o644); err != nil {
		return Draft{}, err
	}
	return Draft{ID: MakeID(storeName), StoreName: storeName, Path: root, Metadata: meta}, nil
}

func Remove(cfg config.Config, name string) error {
	storeName := NameFromID(name)
	if storeName == "" {
		return fmt.Errorf("draft name is required")
	}
	return os.RemoveAll(Path(cfg, storeName))
}

func validate(storeName string, meta Metadata) error {
	if meta.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported draft schema version: %d", meta.SchemaVersion)
	}
	if meta.Name == "" || library.NormalizeName(meta.Name) != storeName {
		return fmt.Errorf("draft metadata name mismatch: %s", meta.Name)
	}
	switch meta.Type {
	case TypeNew:
		if meta.CreatedFrom == "" {
			return fmt.Errorf("draft created_from is required")
		}
	case TypeVersion:
		if meta.ParentSkill == "" {
			return fmt.Errorf("version draft parent_skill is required")
		}
		if meta.ParentHash == "" {
			return fmt.Errorf("version draft parent_hash is required")
		}
		if meta.CreatedFrom == "" {
			return fmt.Errorf("draft created_from is required")
		}
	default:
		return fmt.Errorf("unsupported draft type: %s", meta.Type)
	}
	return nil
}

func timestamp() string {
	return nowUTC().Format(time.RFC3339)
}
