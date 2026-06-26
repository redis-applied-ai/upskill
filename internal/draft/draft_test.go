package draft

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"upskill/internal/config"
)

func TestDraftMetadataLifecycle(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	oldNow := nowUTC
	nowUTC = func() time.Time { return time.Date(2026, 6, 25, 1, 2, 3, 0, time.UTC) }
	defer func() { nowUTC = oldNow }()

	item, err := Save(cfg, Metadata{Name: "My Skill", Type: TypeNew, CreatedFrom: CreatedFromSkeleton})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "draft:my-skill" || item.StoreName != "my-skill" {
		t.Fatalf("draft identity = %#v", item)
	}
	if item.Metadata.CreatedAt != "2026-06-25T01:02:03Z" || item.Metadata.UpdatedAt != "2026-06-25T01:02:03Z" {
		t.Fatalf("timestamps = %#v", item.Metadata)
	}
	if !Exists(cfg, "draft:my-skill") {
		t.Fatal("expected draft to exist")
	}
	read, err := Read(cfg, "draft:my-skill")
	if err != nil {
		t.Fatal(err)
	}
	if read.Metadata.Name != "my-skill" {
		t.Fatalf("read = %#v", read)
	}
	listed, err := List(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "draft:my-skill" {
		t.Fatalf("listed = %#v", listed)
	}
	if err := Remove(cfg, "my-skill"); err != nil {
		t.Fatal(err)
	}
	if Exists(cfg, "my-skill") {
		t.Fatal("draft still exists")
	}
}

func TestVersionDraftMetadata(t *testing.T) {
	cfg := config.Config{DraftsDir: t.TempDir()}
	item, err := Save(cfg, Metadata{
		Name:           "redis",
		Type:           TypeVersion,
		ParentSkill:    "redis",
		ParentSource:   "owner/repo",
		ParentRevision: "main",
		ParentHash:     "sha256:abc",
		CreatedFrom:    CreatedFromBase,
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Metadata.ParentHash != "sha256:abc" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
}

func TestIDsAndPaths(t *testing.T) {
	cfg := config.Config{DraftsDir: "/tmp/drafts"}
	if !IsID("draft:redis") || IsID("redis") {
		t.Fatal("IsID failed")
	}
	if MakeID("Draft:Redis Skill") != "draft:redis-skill" {
		t.Fatalf("MakeID = %s", MakeID("Draft:Redis Skill"))
	}
	if NameFromID("draft:Redis Skill") != "redis-skill" {
		t.Fatalf("NameFromID failed")
	}
	if Path(cfg, "draft:redis") != "/tmp/drafts/redis" {
		t.Fatalf("Path = %s", Path(cfg, "draft:redis"))
	}
	if MetadataPath(cfg, "redis") != "/tmp/drafts/redis/.upskill-draft.json" {
		t.Fatalf("MetadataPath = %s", MetadataPath(cfg, "redis"))
	}
}

func TestValidationErrors(t *testing.T) {
	cfg := config.Config{DraftsDir: t.TempDir()}
	cases := []Metadata{
		{Name: "", Type: TypeNew, CreatedFrom: CreatedFromSkeleton},
		{Name: "redis", SchemaVersion: 9, Type: TypeNew, CreatedFrom: CreatedFromSkeleton},
		{Name: "redis", Type: "bad", CreatedFrom: CreatedFromSkeleton},
		{Name: "redis", Type: TypeNew},
		{Name: "redis", Type: TypeVersion, ParentHash: "sha256:abc", CreatedFrom: CreatedFromBase},
		{Name: "redis", Type: TypeVersion, ParentSkill: "redis", CreatedFrom: CreatedFromBase},
		{Name: "redis", Type: TypeVersion, ParentSkill: "redis", ParentHash: "sha256:abc"},
	}
	for _, tc := range cases {
		if _, err := Save(cfg, tc); err == nil {
			t.Fatalf("expected validation error for %#v", tc)
		}
	}
}

func TestReadListErrorBranches(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	if drafts, err := List(cfg); err != nil || drafts != nil {
		t.Fatalf("missing list = %#v %v", drafts, err)
	}
	if _, err := Read(cfg, ""); err == nil {
		t.Fatal("expected empty read error")
	}
	if err := os.MkdirAll(filepath.Join(cfg.DraftsDir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DraftsDir, "bad", MetadataFile), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(cfg, "bad"); err == nil {
		t.Fatal("expected read json error")
	}
	if drafts, err := List(cfg); err != nil || len(drafts) != 0 {
		t.Fatalf("list skips invalid = %#v %v", drafts, err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(config.Config{DraftsDir: filepath.Join(root, "file")}); err == nil {
		t.Fatal("expected list file error")
	}
	if err := Remove(cfg, ""); err == nil {
		t.Fatal("expected remove empty error")
	}
}

func TestDraftMetadataAdditionalBranches(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DraftsDir: filepath.Join(root, "drafts")}
	if _, err := Save(cfg, Metadata{Name: "zulu", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(cfg, Metadata{Name: "alpha", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DraftsDir, "not-a-dir"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	listed, err := List(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].StoreName != "alpha" || listed[1].StoreName != "zulu" {
		t.Fatalf("sorted list = %#v", listed)
	}

	bad := filepath.Join(cfg.DraftsDir, "mismatch")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, MetadataFile), []byte(`{"schema_version":1,"name":"other","type":"new","created_from":"skeleton"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(cfg, "mismatch"); err == nil {
		t.Fatal("expected metadata mismatch error")
	}

	parentFile := filepath.Join(root, "file-parent")
	if err := os.WriteFile(parentFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(config.Config{DraftsDir: filepath.Join(parentFile, "drafts")}, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err == nil {
		t.Fatal("expected mkdir failure")
	}
	writeAsDir := config.Config{DraftsDir: filepath.Join(root, "write-as-dir")}
	if err := os.MkdirAll(filepath.Join(writeAsDir.DraftsDir, "demo", MetadataFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(writeAsDir, Metadata{Name: "demo", Type: TypeNew, CreatedFrom: CreatedFromSkeleton}); err == nil {
		t.Fatal("expected metadata write failure")
	}
	if _, err := Save(cfg, Metadata{Name: "redis", Type: TypeNew}); err == nil {
		t.Fatal("expected new draft created_from validation error")
	}
	if _, err := Save(cfg, Metadata{Name: "other", Type: TypeNew, CreatedFrom: CreatedFromSkeleton, SchemaVersion: SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := validate("redis", Metadata{Name: "other", Type: TypeNew, CreatedFrom: CreatedFromSkeleton, SchemaVersion: SchemaVersion}); err == nil {
		t.Fatal("expected direct validate mismatch error")
	}
}
