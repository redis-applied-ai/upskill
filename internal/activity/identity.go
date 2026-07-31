package activity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"upskill/internal/config"
	"upskill/internal/library"
)

func skillKeyFromLockEntry(entry library.LockEntry, storeName string) (string, error) {
	storeName = library.NormalizeName(storeName)
	if storeName == "" {
		return "", fmt.Errorf("installed skill name is empty")
	}

	values := []string{"source_id", entry.SourceID}
	if entry.SourceID == "" {
		sourceType := strings.TrimSpace(entry.SourceType)
		sourceURL := strings.TrimSpace(entry.SourceURL)
		if sourceType == "" || sourceURL == "" {
			parsed, err := library.ParseSource(entry.Source)
			if err != nil {
				return "", fmt.Errorf("derive canonical source identity for %s: %w", storeName, err)
			}
			sourceType = parsed.Type
			sourceURL = parsed.URL
		}
		values = []string{"source", sourceType, sourceURL}
	}
	values = append(values,
		normalizeIdentityPath(entry.EffectiveSourceSubpath()),
		normalizeIdentityPath(entry.EffectiveSkillSubpath()),
		storeName,
	)
	return hashSkillTuple(values...), nil
}

func skillKeyForUnlocked(cfg config.Config, storeName string) (string, string, error) {
	storeName = library.NormalizeName(storeName)
	if storeName == "" {
		return "", "", fmt.Errorf("installed skill name is empty")
	}
	storePath, err := filepath.Abs(filepath.Join(cfg.StoreDir, storeName))
	if err != nil {
		return "", "", fmt.Errorf("resolve installed skill path: %w", err)
	}
	storePath = filepath.Clean(storePath)
	if resolved, err := filepath.EvalSymlinks(storePath); err == nil {
		storePath = filepath.Clean(resolved)
	}
	return hashSkillTuple("unlocked", storeName, storePath), storePath, nil
}

func identityFromInstalledSkill(cfg config.Config, skill library.Skill, lock library.Lock) (SkillIdentity, error) {
	storeName := library.NormalizeName(skill.StoreName)
	if storeName == "" {
		return SkillIdentity{}, fmt.Errorf("installed skill has an empty normalized name")
	}
	identity := SkillIdentity{
		SkillID:   storeName,
		SkillName: skill.Name,
		Path:      skill.Path,
	}
	if entry, ok := lock.Skills[storeName]; ok {
		key, err := skillKeyFromLockEntry(entry, storeName)
		if err != nil {
			return SkillIdentity{}, err
		}
		identity.SkillKey = key
		identity.SkillHash = entry.Hash
		identity.SourceID = entry.SourceID
		if identity.SkillName == "" {
			identity.SkillName = entry.Name
		}
		return identity, nil
	}

	key, canonicalPath, err := skillKeyForUnlocked(cfg, storeName)
	if err != nil {
		return SkillIdentity{}, err
	}
	identity.SkillKey = key
	identity.Path = canonicalPath
	identity.SkillHash, err = library.HashDir(canonicalPath)
	if err != nil {
		return SkillIdentity{}, fmt.Errorf("hash unlocked installed skill %s: %w", storeName, err)
	}
	return identity, nil
}

// ResolveSkill resolves one installed base skill and its current activity identity.
func ResolveSkill(cfg config.Config, skillID string) (SkillIdentity, error) {
	rawID, err := activityTargetID(skillID)
	if err != nil {
		return SkillIdentity{}, err
	}
	skill, err := library.ReadInstalled(cfg, rawID)
	if err != nil {
		return SkillIdentity{}, fmt.Errorf("activity requires an installed skill: %w", err)
	}
	lock, err := library.ReadLock(cfg)
	if err != nil {
		return SkillIdentity{}, fmt.Errorf("read installed skill lock: %w", err)
	}
	return identityFromInstalledSkill(cfg, skill, lock)
}

func resolveSkillMetadata(cfg config.Config, skillID string) (SkillIdentity, error) {
	rawID, err := activityTargetID(skillID)
	if err != nil {
		return SkillIdentity{}, err
	}
	storeName := library.NormalizeName(rawID)
	root := filepath.Join(cfg.StoreDir, storeName)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return SkillIdentity{}, fmt.Errorf("activity requires an installed skill: installed skill not found: %s", storeName)
	}
	entrypointFound := false
	for _, name := range []string{"SKILL.md.j2", "SKILL.md"} {
		if info, statErr := os.Stat(filepath.Join(root, name)); statErr == nil && !info.IsDir() {
			entrypointFound = true
			break
		}
	}
	if !entrypointFound {
		return SkillIdentity{}, fmt.Errorf("activity requires an installed skill: installed skill is malformed: %s", storeName)
	}
	lock, err := library.ReadLock(cfg)
	if err != nil {
		return SkillIdentity{}, fmt.Errorf("read installed skill lock: %w", err)
	}
	name := storeName
	if entry, ok := lock.Skills[storeName]; ok && entry.Name != "" {
		name = entry.Name
	}
	return identityFromInstalledSkill(cfg, library.Skill{StoreName: storeName, Name: name, Path: root}, lock)
}

func activityTargetID(skillID string) (string, error) {
	rawID := strings.TrimSpace(skillID)
	if rawID == "" {
		return "", fmt.Errorf("activity requires an installed skill id")
	}
	if strings.HasPrefix(strings.ToLower(rawID), "draft:") {
		return "", fmt.Errorf("activity requires an installed base skill; drafts are not eligible")
	}
	return rawID, nil
}

func hashSkillTuple(values ...string) string {
	hash := sha256.New()
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func normalizeIdentityPath(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.Trim(value, "/")
	if value == "." {
		return ""
	}
	return value
}
