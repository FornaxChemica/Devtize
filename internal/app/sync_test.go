package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/history"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/registry"
)

const syncHelp = `Main Porcelain Commands
   status                  Show the working tree status
   commit                  Record changes to the repository

Ancillary Commands / Manipulators
   config                  Get and set repository or global options
`

type syncRunner struct {
	t          *testing.T
	versions   []string
	specs      []devprocess.CommandSpec
	helpErr    error
	truncated  bool
	helpOutput string
}

type versionOnlyRunner struct {
	result devprocess.CommandResult
	err    error
}

type controlledSyncCache struct {
	store             registry.CacheStore
	loadCalls         int
	failPublish       bool
	failPostcondition bool
}

func (c *controlledSyncCache) Load(provider string) (registry.CacheLoadResult, error) {
	c.loadCalls++
	if c.failPostcondition && c.loadCalls >= 3 {
		return registry.CacheLoadResult{}, errors.New("post-publication read failed")
	}
	return c.store.Load(provider)
}

func (c *controlledSyncCache) Publish(snapshot registry.ProviderSnapshot) (registry.PublishResult, error) {
	if c.failPublish {
		return registry.PublishResult{}, errors.New("publication failed")
	}
	return c.store.Publish(snapshot)
}

func (r versionOnlyRunner) Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error) {
	return r.result, r.err
}

func (r *syncRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	r.specs = append(r.specs, spec)
	if reflect.DeepEqual(spec.Args, []string{"--version"}) {
		version := "2.50.1"
		if len(r.versions) > 0 {
			version = r.versions[0]
			r.versions = r.versions[1:]
		}
		return devprocess.CommandResult{Executable: "/tools/git", Stdout: "git version " + version}, nil
	}
	if reflect.DeepEqual(spec.Args, []string{"help", "--all", "--no-external-commands", "--no-aliases", "--verbose"}) {
		output := r.helpOutput
		if output == "" {
			output = syncHelp
		}
		return devprocess.CommandResult{Executable: "/tools/git", Stdout: output, StdoutTruncated: r.truncated}, r.helpErr
	}
	r.t.Fatalf("unexpected subprocess: %s %#v", spec.Executable, spec.Args)
	return devprocess.CommandResult{}, nil
}

func TestSyncRejectsTimeoutTruncationAndPreservesPriorCache(t *testing.T) {
	for name, configure := range map[string]func(*syncRunner){
		"timeout": func(r *syncRunner) {
			r.helpErr = &devprocess.RunError{Kind: devprocess.ErrorTimeout, Message: "timed out"}
		},
		"truncated": func(r *syncRunner) { r.truncated = true },
	} {
		t.Run(name, func(t *testing.T) {
			service, runner, _, _ := newSyncService(t)
			initial, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
			if err != nil {
				t.Fatal(err)
			}
			initial, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), initial)
			if err != nil {
				t.Fatal(err)
			}
			before := initial.CacheAfter.Digest
			runner.specs = nil
			configure(runner)
			_, err = service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
			if !hasCode(err, app.CodeSyncFailed) {
				t.Fatalf("err=%v", err)
			}
			loaded, loadErr := service.Cache.Load("git")
			if loadErr != nil || loaded.Snapshot == nil || loaded.Snapshot.Digest != before {
				t.Fatalf("prior cache changed: load=%#v err=%v", loaded, loadErr)
			}
		})
	}
}

func TestSyncInvalidCacheFallsBackAndPublicationFailureIsLoud(t *testing.T) {
	service, _, cacheRoot, _ := newSyncService(t)
	directory := filepath.Join(cacheRoot, "git")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "snapshot-corrupt.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
	if err != nil {
		t.Fatal(err)
	}
	if response.CacheBefore.Status != registry.CacheInvalid || len(response.Warnings) == 0 {
		t.Fatalf("response=%#v", response)
	}
	if err := os.RemoveAll(cacheRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheRoot, []byte("blocks directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), response)
	if !hasCode(err, app.CodeSyncFailed) {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestSyncReportsPublicationAndPostconditionFailures(t *testing.T) {
	for name, fixture := range map[string]struct {
		configure func(*controlledSyncCache)
		code      app.ErrorCode
		status    string
	}{
		"publish": {
			configure: func(cache *controlledSyncCache) { cache.failPublish = true },
			code:      app.CodeSyncFailed,
			status:    "failed",
		},
		"postcondition": {
			configure: func(cache *controlledSyncCache) { cache.failPostcondition = true },
			code:      app.CodePostconditionFailed,
			status:    "partially_completed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, _, cacheRoot, _ := newSyncService(t)
			controlled := &controlledSyncCache{store: registry.CacheStore{Root: cacheRoot}}
			fixture.configure(controlled)
			service.Cache = controlled
			response, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
			if err != nil {
				t.Fatal(err)
			}
			response, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), response)
			if !hasCode(err, fixture.code) || string(response.Status) != fixture.status {
				t.Fatalf("response=%#v err=%v", response, err)
			}
		})
	}
}

func TestSyncRejectsChangedCacheAndResponseAfterPlanning(t *testing.T) {
	t.Run("cache changed", func(t *testing.T) {
		service, runner, _, _ := newSyncService(t)
		original, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
		if err != nil {
			t.Fatal(err)
		}
		runner.helpOutput = syncHelp + "   worktree                Manage multiple working trees\n"
		concurrent, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), concurrent); err != nil {
			t.Fatal(err)
		}
		_, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), original)
		if !hasCode(err, app.CodePreconditionFailed) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("response changed", func(t *testing.T) {
		service, _, _, _ := newSyncService(t)
		response, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
		if err != nil {
			t.Fatal(err)
		}
		response.Installation.Version = "9.9.9"
		_, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), response)
		if !hasCode(err, app.CodePlanInvalid) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestSyncDryRunQuarantinesKnowledgeWithoutWriting(t *testing.T) {
	service, runner, cacheRoot, historyPath := newSyncService(t)
	response, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git", DryRun: true}, strings.NewReader("sync\n"), response)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "validated" || response.Plan.Digest == "" || response.ParsedCount != 3 || len(runner.specs) != 2 {
		t.Fatalf("response=%#v specs=%d", response, len(runner.specs))
	}
	if _, err := os.Stat(cacheRoot); !os.IsNotExist(err) {
		t.Fatalf("dry-run cache mutation: %v", err)
	}
	if _, err := os.Stat(historyPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run history mutation: %v", err)
	}
}

func TestSyncMapsMissingAndUnsupportedGit(t *testing.T) {
	base := t.TempDir()
	for name, fixture := range map[string]struct {
		runner versionOnlyRunner
		code   app.ErrorCode
	}{
		"missing": {
			runner: versionOnlyRunner{err: &devprocess.RunError{Kind: devprocess.ErrorMissing, Message: "missing"}},
			code:   app.CodeToolNotFound,
		},
		"unsupported": {
			runner: versionOnlyRunner{result: devprocess.CommandResult{Executable: "/tools/git", Stdout: "git version 2.22.9"}},
			code:   app.CodeToolVersionUnsupported,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cacheRoot := filepath.Join(base, "cache")
			service := app.SyncService{WorkingDir: base, Cache: registry.CacheStore{Root: cacheRoot}, CacheRoot: cacheRoot, Runner: fixture.runner}
			_, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
			if !hasCode(err, fixture.code) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestSyncConfirmedPublishesAndRecordsSanitizedGlobalHistory(t *testing.T) {
	service, runner, _, historyPath := newSyncService(t)
	response, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
	if err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), response)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "succeeded" || response.CacheAfter.Status != registry.CacheExact || response.PublishedCount != 3 || len(runner.specs) != 3 {
		t.Fatalf("response=%#v specs=%d", response, len(runner.specs))
	}
	loaded, err := service.Cache.Load("git")
	if err != nil || loaded.Snapshot == nil || len(loaded.Snapshot.Commands) != 3 {
		t.Fatalf("load=%#v err=%v", loaded, err)
	}
	records, err := (history.Store{Path: historyPath}).List(history.ListOptions{Limit: 10})
	if err != nil || len(records.Records) != 1 {
		t.Fatalf("history=%#v err=%v", records, err)
	}
	record := records.Records[0]
	if record.Project != nil || record.Invocation["workflow"] != "registry.sync" || strings.Contains(string(mustRead(t, historyPath)), "Show the working tree") {
		t.Fatalf("history leaked project or descriptions: %#v", record)
	}
}

func TestSyncDeclineAndStalePlanDoNotPublish(t *testing.T) {
	for name, answer := range map[string]string{"declined": "no\n", "eof": ""} {
		t.Run(name, func(t *testing.T) {
			service, runner, cacheRoot, _ := newSyncService(t)
			response, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader(answer), response)
			if !hasCode(err, app.CodeConfirmationDeclined) || len(runner.specs) != 2 {
				t.Fatalf("err=%v specs=%d", err, len(runner.specs))
			}
			if _, err := os.Stat(cacheRoot); !os.IsNotExist(err) {
				t.Fatalf("decline mutated cache: %v", err)
			}
		})
	}
	t.Run("git changed", func(t *testing.T) {
		service, runner, cacheRoot, _ := newSyncService(t)
		runner.versions = []string{"2.50.1", "2.51.0"}
		response, err := service.Plan(context.Background(), app.SyncOptions{Provider: "git"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.ExecutePlanned(context.Background(), app.SyncOptions{Provider: "git"}, strings.NewReader("sync\n"), response)
		if !hasCode(err, app.CodePreconditionFailed) {
			t.Fatalf("err=%v", err)
		}
		if _, err := os.Stat(cacheRoot); !os.IsNotExist(err) {
			t.Fatalf("stale plan mutated cache: %v", err)
		}
	})
}

func newSyncService(t *testing.T) (app.SyncService, *syncRunner, string, string) {
	t.Helper()
	base := t.TempDir()
	cacheRoot := filepath.Join(base, "cache")
	historyPath := filepath.Join(base, "state", "history.jsonl")
	runner := &syncRunner{t: t}
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	service := app.SyncService{
		WorkingDir: base, Cache: registry.CacheStore{Root: cacheRoot}, CacheRoot: cacheRoot, Runner: runner,
		History: history.Store{Path: historyPath}, HistoryEnabled: true, Now: func() time.Time { return now },
		TempDir: func() (string, error) { return os.MkdirTemp(base, "isolated-") },
	}
	return service, runner, cacheRoot, historyPath
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func hasCode(err error, code app.ErrorCode) bool {
	var operational *app.Error
	return errors.As(err, &operational) && operational.Code == code
}
