package registry_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
	"github.com/FornaxChemica/devtize/registry/builtin"
)

func TestCachePublishLoadAndCorruptFallback(t *testing.T) {
	store := registry.CacheStore{Root: t.TempDir()}
	snapshot := testSnapshot(t, time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC), "2.50.1", "status")
	published, err := store.Publish(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(published.Path) != "snapshot-"+strings.TrimPrefix(snapshot.Digest, "sha256:")+".json" {
		t.Fatalf("unexpected content-addressed path %q", published.Path)
	}
	info, err := os.Stat(published.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %o", info.Mode().Perm())
	}

	invalid := filepath.Join(filepath.Dir(published.Path), "snapshot-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json")
	if err := os.WriteFile(invalid, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("git")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != registry.CacheExact || loaded.Snapshot == nil || loaded.Snapshot.Digest != snapshot.Digest || len(loaded.Warnings) == 0 {
		t.Fatalf("load = %#v", loaded)
	}
}

func TestPhaseD1GitSnapshotFixtureKeepsCanonicalDigestAndBytes(t *testing.T) {
	fixture := filepath.Join("..", "..", "testdata", "registry", "phase-d1-git-snapshot.json")
	content, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:47bbb14849acf804bc30b54ec876e9effc5da89e1fdba5d543717a4f17c95f76"
	root := t.TempDir()
	directory := filepath.Join(root, "git")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "snapshot-"+strings.TrimPrefix(digest, "sha256:")+".json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := (registry.CacheStore{Root: root}).Load("git")
	if err != nil || loaded.Snapshot == nil || loaded.Snapshot.Digest != digest {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	canonical, err := json.Marshal(*loaded.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != strings.TrimSpace(string(content)) {
		t.Fatalf("legacy canonical bytes changed\n got %s\nwant %s", canonical, content)
	}
}

func TestCacheIgnoresTemporaryFilesAndReportsOnlyInvalidSnapshots(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "git")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".snapshot-half.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := (registry.CacheStore{Root: root}).Load("git")
	if err != nil || loaded.Status != registry.CacheNotSynced || loaded.Files != 0 {
		t.Fatalf("temporary load = %#v err=%v", loaded, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "snapshot-bad.json"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = (registry.CacheStore{Root: root}).Load("git")
	if err != nil || loaded.Status != registry.CacheInvalid {
		t.Fatalf("invalid load = %#v err=%v", loaded, err)
	}
}

func TestMergeCommandsKeepsReviewedBuiltinAuthority(t *testing.T) {
	builtins := builtin.Git()
	synced := testSnapshot(t, time.Now().UTC(), "2.50.1", "status", "worktree").Commands
	merged := registry.MergeCommands(builtins, synced)
	status := 0
	worktree := 0
	for _, command := range merged {
		switch command.Command() {
		case "git status":
			status++
			if command.Source.Kind != "builtin" || command.Risk != safety.RiskReadOnly {
				t.Fatalf("synced status displaced builtin: %#v", command)
			}
		case "git worktree":
			worktree++
		}
	}
	if status != 1 || worktree != 1 {
		t.Fatalf("status=%d worktree=%d", status, worktree)
	}
}

func TestCacheRejectsDiscoveryMetadataThatClaimsAuthority(t *testing.T) {
	snapshot := testSnapshot(t, time.Now().UTC(), "2.50.1", "status")
	snapshot.Commands[0].Effects = []string{"execute arbitrary command"}
	var err error
	snapshot, err = snapshot.WithDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err == nil {
		t.Fatal("cache accepted unreviewed authority metadata")
	}
}

func TestGitHubCacheValidatesIndependentlyAndRejectsProviderSwaps(t *testing.T) {
	store := registry.CacheStore{Root: t.TempDir()}
	when := time.Date(2026, 5, 27, 1, 2, 3, 0, time.UTC)
	gh := ghTestSnapshot(t, when, "2.93.0")
	if _, err := store.Publish(gh); err != nil {
		t.Fatal(err)
	}
	git := testSnapshot(t, when, "2.50.1", "status")
	if _, err := store.Publish(git); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(store.Root, "gh")
	if err := os.WriteFile(filepath.Join(directory, "snapshot-corrupt.json"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedGit, err := store.Load("git")
	if err != nil || loadedGit.Snapshot == nil || loadedGit.Snapshot.Digest != git.Digest {
		t.Fatalf("git=%#v err=%v", loadedGit, err)
	}
	loadedGH, err := store.Load("gh")
	if err != nil || loadedGH.Snapshot == nil || loadedGH.Snapshot.Digest != gh.Digest || len(loadedGH.Warnings) == 0 {
		t.Fatalf("gh=%#v err=%v", loadedGH, err)
	}
	swapped := gh
	swapped.ProviderID = "git"
	swapped, err = swapped.WithDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err := swapped.Validate(); err == nil {
		t.Fatal("provider/source swap was accepted")
	}
}

func TestCacheRetainsThreeValidSnapshots(t *testing.T) {
	store := registry.CacheStore{Root: t.TempDir()}
	for index, name := range []string{"alpha", "beta", "gamma", "delta"} {
		if _, err := store.Publish(testSnapshot(t, time.Date(2026, 9, 23, 1, index, 0, 0, time.UTC), "2.50.1", "status", name)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(store.Root, "git"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != registry.RetainedSnapshots {
		t.Fatalf("snapshot files = %d, want %d", len(entries), registry.RetainedSnapshots)
	}
}

func TestCacheUnchangedPublishAndCleanupWarning(t *testing.T) {
	root := t.TempDir()
	store := registry.CacheStore{Root: root}
	snapshot := testSnapshot(t, time.Now().UTC(), "2.50.1", "status")
	if _, err := store.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "git"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("unchanged publish entries=%d err=%v", len(entries), err)
	}

	store.Remove = func(string) error { return errors.New("remove denied") }
	var published registry.PublishResult
	for index, name := range []string{"alpha", "beta", "gamma"} {
		published, err = store.Publish(testSnapshot(t, time.Now().UTC().Add(time.Duration(index+1)*time.Second), "2.50.1", "status", name))
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(published.Warnings) == 0 {
		t.Fatal("cleanup failure was not reported after successful publication")
	}
	loaded, err := store.Load("git")
	if err != nil || loaded.Snapshot == nil || loaded.Snapshot.Digest == "" {
		t.Fatalf("published cache was lost after cleanup warning: %#v err=%v", loaded, err)
	}
}

func TestCacheStatusBoundedScanAndConcurrentReaders(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		store := registry.CacheStore{Root: t.TempDir()}
		snapshot := testSnapshot(t, time.Now().UTC(), "2.50.1", "status")
		snapshot.Installation.Version = "2.51.0"
		snapshot.Commands[0].VersionStatus = "stale"
		var err error
		snapshot, err = snapshot.WithDigest()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Publish(snapshot); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.Load("git")
		if err != nil || loaded.Status != registry.CacheStale || loaded.Snapshot.Commands[0].VersionStatus != "stale" {
			t.Fatalf("loaded=%#v err=%v", loaded, err)
		}
	})
	t.Run("bounded invalid files", func(t *testing.T) {
		root := t.TempDir()
		directory := filepath.Join(root, "git")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < registry.MaxSnapshotFiles+3; index++ {
			name := filepath.Join(directory, "snapshot-"+strings.Repeat(string(rune('a'+index)), 64)+".json")
			if err := os.WriteFile(name, []byte("invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		loaded, err := (registry.CacheStore{Root: root}).Load("git")
		if err != nil || loaded.Files != registry.MaxSnapshotFiles || loaded.Status != registry.CacheInvalid || len(loaded.Warnings) == 0 {
			t.Fatalf("loaded=%#v err=%v", loaded, err)
		}
	})
	t.Run("readers during publish", func(t *testing.T) {
		store := registry.CacheStore{Root: t.TempDir()}
		first := testSnapshot(t, time.Now().UTC(), "2.50.1", "status")
		if _, err := store.Publish(first); err != nil {
			t.Fatal(err)
		}
		second := testSnapshot(t, time.Now().UTC().Add(time.Second), "2.50.1", "status", "worktree")
		var wait sync.WaitGroup
		errorsSeen := make(chan error, 32)
		for index := 0; index < 32; index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				loaded, err := store.Load("git")
				if err != nil || loaded.Snapshot == nil {
					errorsSeen <- fmt.Errorf("load=%#v err=%v", loaded, err)
				}
			}()
		}
		if _, err := store.Publish(second); err != nil {
			t.Fatal(err)
		}
		wait.Wait()
		close(errorsSeen)
		for err := range errorsSeen {
			t.Error(err)
		}
		loaded, err := store.Load("git")
		if err != nil || loaded.Snapshot == nil || loaded.Snapshot.Digest != second.Digest {
			t.Fatalf("final load=%#v err=%v", loaded, err)
		}
	})
}

func FuzzSnapshotDecoder(f *testing.F) {
	f.Add([]byte(`{"schema_version":1}`))
	f.Fuzz(func(t *testing.T, content []byte) {
		root := t.TempDir()
		directory := filepath.Join(root, "git")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "snapshot-fuzz.json"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = (registry.CacheStore{Root: root}).Load("git")
	})
}

func testSnapshot(t *testing.T, when time.Time, version string, names ...string) registry.ProviderSnapshot {
	t.Helper()
	commands := make([]registry.CommandKnowledge, 0, len(names))
	sourceDigest := "sha256:source"
	for _, name := range names {
		commands = append(commands, registry.CommandKnowledge{
			ID: "sync.git." + name, ProviderID: "git", CommandPath: []string{"git", name}, Summary: "Synced " + name,
			VersionRange: "=" + version, Risk: safety.Risk("unclassified"), Effects: []string{"Discovery-only help metadata; effects are not reviewed."},
			Source:  registry.KnowledgeSource{Kind: "sync", Locator: "git help --all --no-external-commands --no-aliases --verbose", Digest: sourceDigest, ToolVersion: version, ParserVersion: "1", CapturedAt: when},
			Support: registry.SupportDiscoverable, VersionStatus: "exact",
		})
	}
	// The cache schema requires stable ID ordering.
	if len(commands) > 1 {
		for i := range commands {
			for j := i + 1; j < len(commands); j++ {
				if commands[j].ID < commands[i].ID {
					commands[i], commands[j] = commands[j], commands[i]
				}
			}
		}
	}
	snapshot := registry.ProviderSnapshot{
		SchemaVersion: registry.CacheSchemaVersion, ProviderID: "git",
		Installation: registry.InstallationSnapshot{Executable: "git", Path: "/usr/bin/git", Version: version, DetectedAt: when},
		Attempt:      registry.SyncAttempt{Status: "succeeded", AttemptedAt: when}, KnowledgeVersion: version,
		ParserID: "git-help-all", ParserVersion: "1",
		SourceArgv: []string{"git", "help", "--all", "--no-external-commands", "--no-aliases", "--verbose"}, SourceDigest: sourceDigest, CapturedAt: when,
		Limits: registry.SyncLimits{MaxDepth: 1, MaxCommands: 256, MaxOutputBytes: 1 << 20, TimeoutMillis: 10_000}, Commands: commands,
	}
	var err error
	snapshot, err = snapshot.WithDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		content, _ := json.Marshal(snapshot)
		t.Fatalf("invalid test snapshot: %v\n%s", err, content)
	}
	clone := snapshot
	content, _ := json.Marshal(snapshot)
	if err := json.Unmarshal(content, &clone); err != nil || !reflect.DeepEqual(snapshot, clone) {
		t.Fatalf("snapshot did not round trip: %v", err)
	}
	return snapshot
}

func ghTestSnapshot(t *testing.T, when time.Time, version string) registry.ProviderSnapshot {
	t.Helper()
	sourceDigest := "sha256:gh-source"
	paths := [][]string{{"gh", "api"}, {"gh", "auth"}, {"gh", "auth", "login"}, {"gh", "issue"}, {"gh", "issue", "create"}, {"gh", "pr"}, {"gh", "pr", "create"}, {"gh", "repo"}, {"gh", "repo", "create"}}
	commands := make([]registry.CommandKnowledge, 0, len(paths))
	for _, path := range paths {
		kind := registry.CommandKindCommand
		if len(path) == 2 && path[1] != "api" {
			kind = registry.CommandKindGroup
		}
		command := registry.CommandKnowledge{ID: "sync.gh." + strings.Join(path[1:], "."), ProviderID: "gh", CommandPath: path, Summary: "Synced " + strings.Join(path[1:], " "), Kind: kind, VersionRange: "=" + version, Risk: "unclassified", Effects: []string{"Discovery-only help metadata; effects are not reviewed."}, Source: registry.KnowledgeSource{Kind: "sync", Locator: "gh help reference", Digest: sourceDigest, ToolVersion: version, ParserVersion: "1", CapturedAt: when}, Support: registry.SupportDiscoverable, VersionStatus: "exact"}
		if command.Command() == "gh pr create" {
			command.Aliases = []string{"gh pr new"}
			command.Flags = []registry.FlagKnowledge{{LongName: "--web", ShortName: "-w", Summary: "Open the browser"}}
		}
		commands = append(commands, command)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].ID < commands[j].ID })
	snapshot := registry.ProviderSnapshot{SchemaVersion: 1, ProviderID: "gh", Installation: registry.InstallationSnapshot{Executable: "gh", Path: "/usr/bin/gh", Version: version, DetectedAt: when}, Attempt: registry.SyncAttempt{Status: "succeeded", AttemptedAt: when}, KnowledgeVersion: version, ParserID: "gh-help-reference", ParserVersion: "1", SourceArgv: []string{"gh", "help", "reference"}, SourceDigest: sourceDigest, CapturedAt: when, Limits: registry.SyncLimits{MaxDepth: 2, MaxCommands: 512, MaxOutputBytes: 2 << 20, TimeoutMillis: 15_000, MaxFlags: 4096, MaxAliases: 1024}, Commands: commands}
	var err error
	snapshot, err = snapshot.WithDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("invalid gh snapshot: %v", err)
	}
	return snapshot
}
