package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var syncedCommandName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const (
	CacheSchemaVersion = 1
	MaxSyncCommands    = 256 // Phase D.1 Git compatibility alias.
	MaxGHSyncCommands  = 512
	MaxGHSyncFlags     = 4096
	MaxGHSyncAliases   = 1024
	MaxSnapshotFiles   = 8
	MaxSnapshotBytes   = 2 << 20
	MaxCacheScanBytes  = 8 << 20
	RetainedSnapshots  = 3
)

type CacheStatus string

const (
	CacheExact     CacheStatus = "exact"
	CacheStale     CacheStatus = "stale"
	CacheNotSynced CacheStatus = "not_synced"
	CacheInvalid   CacheStatus = "invalid"
)

type InstallationSnapshot struct {
	Executable string    `json:"executable"`
	Path       string    `json:"path"`
	Version    string    `json:"version"`
	DetectedAt time.Time `json:"detected_at"`
}

type SyncAttempt struct {
	Status       string    `json:"status"`
	AttemptedAt  time.Time `json:"attempted_at"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

type SyncLimits struct {
	MaxDepth       int `json:"max_depth"`
	MaxCommands    int `json:"max_commands"`
	MaxOutputBytes int `json:"max_output_bytes"`
	TimeoutMillis  int `json:"timeout_millis"`
	MaxFlags       int `json:"max_flags,omitempty"`
	MaxAliases     int `json:"max_aliases,omitempty"`
}

type ProviderSnapshot struct {
	SchemaVersion    int                  `json:"schema_version"`
	ProviderID       string               `json:"provider_id"`
	Digest           string               `json:"digest"`
	Installation     InstallationSnapshot `json:"installation"`
	Attempt          SyncAttempt          `json:"attempt"`
	KnowledgeVersion string               `json:"knowledge_version"`
	ParserID         string               `json:"parser_id"`
	ParserVersion    string               `json:"parser_version"`
	SourceArgv       []string             `json:"source_argv"`
	SourceDigest     string               `json:"source_digest"`
	CapturedAt       time.Time            `json:"captured_at"`
	Limits           SyncLimits           `json:"limits"`
	Commands         []CommandKnowledge   `json:"commands"`
}

type CacheLoadResult struct {
	Snapshot *ProviderSnapshot `json:"snapshot,omitempty"`
	Status   CacheStatus       `json:"status"`
	Warnings []string          `json:"warnings,omitempty"`
	Files    int               `json:"files_scanned"`
}

type PublishResult struct {
	Path     string   `json:"path"`
	Warnings []string `json:"warnings,omitempty"`
}

type CacheStore struct {
	Root   string
	Remove func(string) error
}

func (s ProviderSnapshot) WithDigest() (ProviderSnapshot, error) {
	digest, err := snapshotDigest(s)
	if err != nil {
		return s, err
	}
	s.Digest = digest
	return s, nil
}

func (s ProviderSnapshot) Validate() error {
	if err := s.validateEnvelope(); err != nil {
		return err
	}
	switch s.ProviderID {
	case "git":
		return s.validateGit()
	case "gh":
		return s.validateGitHub()
	default:
		return fmt.Errorf("unsupported registry cache provider %q", s.ProviderID)
	}
}

func (s ProviderSnapshot) validateEnvelope() error {
	if s.SchemaVersion != CacheSchemaVersion {
		return fmt.Errorf("unsupported registry cache schema version %d", s.SchemaVersion)
	}
	if s.ProviderID == "" {
		return errors.New("registry cache provider is required")
	}
	if s.Digest == "" {
		return errors.New("registry cache digest is required")
	}
	expected, err := snapshotDigest(s)
	if err != nil {
		return err
	}
	if expected != s.Digest {
		return errors.New("registry cache digest does not match its content")
	}
	if strings.TrimSpace(s.Installation.Executable) == "" || strings.TrimSpace(s.Installation.Path) == "" || strings.TrimSpace(s.Installation.Version) == "" || s.Installation.DetectedAt.IsZero() {
		return errors.New("registry cache installation evidence is incomplete")
	}
	if s.Attempt.Status != "succeeded" || s.Attempt.AttemptedAt.IsZero() || s.Attempt.ErrorCode != "" || s.Attempt.ErrorMessage != "" {
		return errors.New("registry cache sync attempt is invalid")
	}
	if s.KnowledgeVersion == "" || s.ParserID == "" || s.ParserVersion == "" || s.SourceDigest == "" || s.CapturedAt.IsZero() {
		return errors.New("registry cache provenance is incomplete")
	}
	if len(s.Commands) == 0 || len(s.Commands) > s.Limits.MaxCommands {
		return errors.New("registry cache command count is invalid")
	}
	return nil
}

func (s ProviderSnapshot) validateGit() error {
	if s.ParserID != "git-help-all" || s.ParserVersion != "1" {
		return errors.New("Git registry cache parser is not reviewed")
	}
	if s.Limits.MaxDepth != 1 || s.Limits.MaxCommands < 1 || s.Limits.MaxCommands > MaxSyncCommands || s.Limits.MaxOutputBytes < 1 || s.Limits.MaxOutputBytes > 1<<20 || s.Limits.TimeoutMillis < 1 || s.Limits.TimeoutMillis > 10_000 || s.Limits.MaxFlags != 0 || s.Limits.MaxAliases != 0 {
		return errors.New("registry cache limits are outside reviewed bounds")
	}
	if len(s.SourceArgv) != 6 || s.SourceArgv[0] != "git" || s.SourceArgv[1] != "help" || s.SourceArgv[2] != "--all" || s.SourceArgv[3] != "--no-external-commands" || s.SourceArgv[4] != "--no-aliases" || s.SourceArgv[5] != "--verbose" {
		return errors.New("registry cache source argv is not reviewed")
	}
	previousID := ""
	seenPaths := make(map[string]struct{}, len(s.Commands))
	for _, command := range s.Commands {
		if err := command.Validate(); err != nil {
			return fmt.Errorf("validate synced command: %w", err)
		}
		if command.ProviderID != s.ProviderID || command.Source.Kind != "sync" || command.Source.ToolVersion != s.KnowledgeVersion || command.Source.ParserVersion != s.ParserVersion || !command.Source.CapturedAt.Equal(s.CapturedAt) || command.Source.Digest != s.SourceDigest {
			return fmt.Errorf("%s: command provenance does not match snapshot", command.ID)
		}
		path := normalizedPath(command.CommandPath)
		name := command.CommandPath[1]
		if len(command.CommandPath) != 2 || !syncedCommandName.MatchString(name) || command.ID != "sync.git."+name {
			return fmt.Errorf("%s: synced command identity is invalid", command.ID)
		}
		if len(name) > 128 || len(command.Summary) > 1024 || strings.ContainsAny(command.Summary, "\r\n") {
			return fmt.Errorf("%s: synced command fields exceed reviewed limits", command.ID)
		}
		if command.Kind != "" || command.Usage != "" || len(command.Flags) != 0 || len(command.Aliases) != 0 || len(command.IntentPhrases) != 0 || len(command.Examples) != 0 || command.VersionRange != "="+s.KnowledgeVersion || command.Risk != "unclassified" || len(command.Effects) != 1 || command.Effects[0] != "Discovery-only help metadata; effects are not reviewed." {
			return fmt.Errorf("%s: synced command contains unreviewed authority metadata", command.ID)
		}
		if command.Source.Locator != "git help --all --no-external-commands --no-aliases --verbose" {
			return fmt.Errorf("%s: synced command source is not reviewed", command.ID)
		}
		if previousID != "" && command.ID <= previousID {
			return errors.New("registry cache commands are not sorted by stable ID")
		}
		if _, duplicate := seenPaths[path]; duplicate {
			return fmt.Errorf("duplicate registry command path %q", command.Command())
		}
		seenPaths[path] = struct{}{}
		previousID = command.ID
	}
	return nil
}

func (s ProviderSnapshot) validateGitHub() error {
	if s.ParserID != "gh-help-reference" || s.ParserVersion != "1" {
		return errors.New("GitHub CLI registry cache parser is not reviewed")
	}
	if s.Limits.MaxDepth != 2 || s.Limits.MaxCommands < 1 || s.Limits.MaxCommands > MaxGHSyncCommands || s.Limits.MaxOutputBytes < 1 || s.Limits.MaxOutputBytes > 2<<20 || s.Limits.TimeoutMillis < 1 || s.Limits.TimeoutMillis > 15_000 || s.Limits.MaxFlags < 1 || s.Limits.MaxFlags > MaxGHSyncFlags || s.Limits.MaxAliases < 1 || s.Limits.MaxAliases > MaxGHSyncAliases {
		return errors.New("GitHub CLI registry cache limits are outside reviewed bounds")
	}
	if len(s.SourceArgv) != 3 || s.SourceArgv[0] != "gh" || s.SourceArgv[1] != "help" || s.SourceArgv[2] != "reference" {
		return errors.New("GitHub CLI registry cache source argv is not reviewed")
	}
	previousID := ""
	seenPaths := make(map[string]struct{}, len(s.Commands))
	seenAliases := make(map[string]string)
	paths := make(map[string]struct{}, len(s.Commands))
	totalFlags := 0
	totalAliases := 0
	anchors := map[string]bool{
		"gh api": false, "gh auth login": false, "gh issue create": false,
		"gh pr create": false, "gh repo create": false,
	}
	for _, command := range s.Commands {
		if err := command.Validate(); err != nil {
			return fmt.Errorf("validate synced command: %w", err)
		}
		if command.ProviderID != "gh" || command.Source.Kind != "sync" || command.Source.ToolVersion != s.KnowledgeVersion || command.Source.ParserVersion != s.ParserVersion || !command.Source.CapturedAt.Equal(s.CapturedAt) || command.Source.Digest != s.SourceDigest {
			return fmt.Errorf("%s: command provenance does not match snapshot", command.ID)
		}
		if len(command.CommandPath) < 2 || len(command.CommandPath) > 3 {
			return fmt.Errorf("%s: synced command depth is invalid", command.ID)
		}
		for _, segment := range command.CommandPath[1:] {
			if len(segment) > 128 || !syncedCommandName.MatchString(segment) {
				return fmt.Errorf("%s: synced command path is invalid", command.ID)
			}
		}
		expectedID := "sync.gh." + strings.Join(command.CommandPath[1:], ".")
		if command.ID != expectedID || command.Kind == "" {
			return fmt.Errorf("%s: synced command identity is invalid", command.ID)
		}
		if len(command.IntentPhrases) != 0 || len(command.Examples) != 0 || command.VersionRange != "="+s.KnowledgeVersion || command.Risk != "unclassified" || len(command.Effects) != 1 || command.Effects[0] != "Discovery-only help metadata; effects are not reviewed." {
			return fmt.Errorf("%s: synced command contains unreviewed authority metadata", command.ID)
		}
		if command.Source.Locator != "gh help reference" {
			return fmt.Errorf("%s: synced command source is not reviewed", command.ID)
		}
		if previousID != "" && command.ID <= previousID {
			return errors.New("registry cache commands are not sorted by stable ID")
		}
		path := normalizedPath(command.CommandPath)
		if _, duplicate := seenPaths[path]; duplicate {
			return fmt.Errorf("duplicate registry command path %q", command.Command())
		}
		seenPaths[path] = struct{}{}
		paths[path] = struct{}{}
		if _, required := anchors[command.Command()]; required {
			anchors[command.Command()] = true
		}
		previousAlias := ""
		for _, alias := range command.Aliases {
			if err := validateGitHubAlias(alias); err != nil {
				return fmt.Errorf("%s: %w", command.ID, err)
			}
			if previousAlias != "" && alias <= previousAlias {
				return fmt.Errorf("%s: aliases are not sorted", command.ID)
			}
			if owner, duplicate := seenAliases[alias]; duplicate {
				return fmt.Errorf("GitHub CLI alias %q has multiple owners %q and %q", alias, owner, command.ID)
			}
			seenAliases[alias] = command.ID
			previousAlias = alias
		}
		previousFlag := registryFlagKey(FlagKnowledge{})
		for index, flag := range command.Flags {
			key := registryFlagKey(flag)
			if index > 0 && key <= previousFlag {
				return fmt.Errorf("%s: flags are not sorted", command.ID)
			}
			previousFlag = key
		}
		totalAliases += len(command.Aliases)
		totalFlags += len(command.Flags)
		if len(command.Aliases) > 32 || totalAliases > s.Limits.MaxAliases || totalFlags > s.Limits.MaxFlags {
			return fmt.Errorf("%s: synced discovery metadata exceeds reviewed counts", command.ID)
		}
	}
	for anchor, present := range anchors {
		if !present {
			return fmt.Errorf("GitHub CLI registry cache is missing anchor command %s", anchor)
		}
	}
	for _, command := range s.Commands {
		hasChild := false
		if len(command.CommandPath) == 2 {
			prefix := normalizedPath(command.CommandPath) + "\x00"
			for path := range paths {
				if strings.HasPrefix(path, prefix) {
					hasChild = true
					break
				}
			}
		}
		expected := CommandKindCommand
		if hasChild {
			expected = CommandKindGroup
		}
		if command.Kind != expected {
			return fmt.Errorf("%s: command kind does not match parsed children", command.ID)
		}
	}
	return nil
}

func validateGitHubAlias(alias string) error {
	if len(alias) > 1024 || strings.ContainsAny(alias, "\r\n") {
		return fmt.Errorf("GitHub CLI alias %q exceeds reviewed limits", alias)
	}
	parts := strings.Fields(alias)
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "gh" || strings.Join(parts, " ") != alias {
		return fmt.Errorf("GitHub CLI alias %q is invalid", alias)
	}
	for _, segment := range parts[1:] {
		if len(segment) > 128 || !syncedCommandName.MatchString(segment) {
			return fmt.Errorf("GitHub CLI alias %q is invalid", alias)
		}
	}
	return nil
}

func registryFlagKey(flag FlagKnowledge) string {
	return flag.LongName + "\x00" + flag.ShortName
}

func (s CacheStore) Load(provider string) (CacheLoadResult, error) {
	result := CacheLoadResult{Status: CacheNotSynced}
	directory := filepath.Join(s.Root, provider)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read registry cache: %w", err)
	}
	type cacheFile struct {
		name    string
		modTime time.Time
	}
	var files []cacheFile
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "snapshot-") && strings.HasSuffix(entry.Name(), ".json") {
			info, infoErr := entry.Info()
			if infoErr != nil {
				result.Warnings = append(result.Warnings, "registry cache snapshot could not be inspected: "+entry.Name())
				continue
			}
			files = append(files, cacheFile{name: entry.Name(), modTime: info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.After(files[j].modTime)
		}
		return files[i].name > files[j].name
	})
	if len(files) > MaxSnapshotFiles {
		result.Warnings = append(result.Warnings, fmt.Sprintf("registry cache contains more than %d snapshots; only a bounded set was inspected", MaxSnapshotFiles))
		files = files[:MaxSnapshotFiles]
	}
	var valid []ProviderSnapshot
	total := int64(0)
	for _, file := range files {
		name := file.name
		result.Files++
		path := filepath.Join(directory, name)
		info, statErr := os.Stat(path)
		if statErr != nil {
			result.Warnings = append(result.Warnings, "registry cache snapshot could not be inspected: "+name)
			continue
		}
		if info.Size() > MaxSnapshotBytes || total+info.Size() > MaxCacheScanBytes {
			result.Warnings = append(result.Warnings, "registry cache snapshot exceeded bounded read limits: "+name)
			continue
		}
		total += info.Size()
		snapshot, readErr := readSnapshot(path)
		if readErr != nil || snapshot.ProviderID != provider || name != snapshotFilename(snapshot.Digest) {
			result.Warnings = append(result.Warnings, "ignored invalid registry cache snapshot: "+name)
			continue
		}
		valid = append(valid, snapshot)
	}
	if len(valid) == 0 {
		if len(files) > 0 {
			result.Status = CacheInvalid
		}
		return result, nil
	}
	sort.Slice(valid, func(i, j int) bool {
		if !valid[i].Attempt.AttemptedAt.Equal(valid[j].Attempt.AttemptedAt) {
			return valid[i].Attempt.AttemptedAt.After(valid[j].Attempt.AttemptedAt)
		}
		return valid[i].Digest > valid[j].Digest
	})
	selected := valid[0]
	result.Snapshot = &selected
	result.Status = CacheExact
	if selected.Installation.Version != selected.KnowledgeVersion {
		result.Status = CacheStale
	}
	setVersionStatus(result.Snapshot.Commands, result.Status)
	return result, nil
}

func (s CacheStore) Publish(snapshot ProviderSnapshot) (PublishResult, error) {
	if err := snapshot.Validate(); err != nil {
		return PublishResult{}, err
	}
	directory := filepath.Join(s.Root, snapshot.ProviderID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return PublishResult{}, fmt.Errorf("create registry cache directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return PublishResult{}, fmt.Errorf("restrict registry cache directory: %w", err)
	}
	target := filepath.Join(directory, snapshotFilename(snapshot.Digest))
	if _, err := os.Stat(target); err == nil {
		return PublishResult{Path: target, Warnings: s.retain(directory, target)}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return PublishResult{}, fmt.Errorf("inspect registry cache target: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".snapshot-*.tmp")
	if err != nil {
		return PublishResult{}, fmt.Errorf("create registry cache temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return PublishResult{}, fmt.Errorf("restrict registry cache temporary file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(snapshot); err != nil {
		_ = temporary.Close()
		return PublishResult{}, fmt.Errorf("encode registry cache snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return PublishResult{}, fmt.Errorf("flush registry cache snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return PublishResult{}, fmt.Errorf("close registry cache snapshot: %w", err)
	}
	if _, err := readSnapshot(temporaryName); err != nil {
		return PublishResult{}, fmt.Errorf("verify registry cache snapshot: %w", err)
	}
	if _, err := os.Stat(target); err == nil {
		return PublishResult{Path: target, Warnings: s.retain(directory, target)}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return PublishResult{}, fmt.Errorf("inspect registry cache target: %w", err)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return PublishResult{}, fmt.Errorf("publish registry cache snapshot: %w", err)
	}
	removeTemporary = false
	return PublishResult{Path: target, Warnings: s.retain(directory, target)}, nil
}

func (s CacheStore) retain(directory, current string) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return []string{"published registry cache, but old snapshots could not be inspected for retention"}
	}
	type candidate struct {
		path string
		when time.Time
	}
	var valid []candidate
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), "snapshot-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		snapshot, readErr := readSnapshot(path)
		if readErr == nil && entry.Name() == snapshotFilename(snapshot.Digest) {
			valid = append(valid, candidate{path: path, when: snapshot.Attempt.AttemptedAt})
		}
	}
	sort.Slice(valid, func(i, j int) bool {
		if !valid[i].when.Equal(valid[j].when) {
			return valid[i].when.After(valid[j].when)
		}
		return valid[i].path > valid[j].path
	})
	var warnings []string
	if len(valid) <= RetainedSnapshots {
		return nil
	}
	keep := map[string]struct{}{current: {}}
	for _, item := range valid {
		if len(keep) >= RetainedSnapshots {
			break
		}
		keep[item.path] = struct{}{}
	}
	for _, item := range valid {
		if _, retained := keep[item.path]; retained {
			continue
		}
		remove := s.Remove
		if remove == nil {
			remove = os.Remove
		}
		if err := remove(item.path); err != nil {
			warnings = append(warnings, "published registry cache, but an old snapshot could not be removed")
		}
	}
	return warnings
}

func MergeCommands(builtins, synced []CommandKnowledge) []CommandKnowledge {
	merged := append([]CommandKnowledge(nil), builtins...)
	seen := make(map[string]struct{}, len(builtins))
	for _, command := range builtins {
		seen[normalizedProviderPath(command)] = struct{}{}
	}
	for _, command := range synced {
		key := normalizedProviderPath(command)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, command)
	}
	return merged
}

func normalizedProviderPath(command CommandKnowledge) string {
	return strings.ToLower(command.ProviderID) + "\x00" + normalizedPath(command.CommandPath)
}

func readSnapshot(path string) (ProviderSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return ProviderSnapshot{}, err
	}
	defer file.Close()
	limited := io.LimitReader(file, MaxSnapshotBytes+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return ProviderSnapshot{}, err
	}
	if len(content) > MaxSnapshotBytes {
		return ProviderSnapshot{}, errors.New("registry cache snapshot exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var snapshot ProviderSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return ProviderSnapshot{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ProviderSnapshot{}, errors.New("registry cache snapshot contains trailing data")
	}
	if err := snapshot.Validate(); err != nil {
		return ProviderSnapshot{}, err
	}
	return snapshot, nil
}

func snapshotDigest(snapshot ProviderSnapshot) (string, error) {
	snapshot.Digest = ""
	content, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("canonicalize registry cache snapshot: %w", err)
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func snapshotFilename(digest string) string {
	return "snapshot-" + strings.TrimPrefix(digest, "sha256:") + ".json"
}

func normalizedPath(parts []string) string {
	return strings.ToLower(strings.Join(parts, "\x00"))
}

func setVersionStatus(commands []CommandKnowledge, status CacheStatus) {
	value := "exact"
	if status == CacheStale {
		value = "stale"
	}
	for index := range commands {
		commands[index].VersionStatus = value
	}
}
