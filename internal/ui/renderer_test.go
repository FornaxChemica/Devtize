package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/detect"
	"github.com/FornaxChemica/devtize/internal/operation"
	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
)

func TestColorAndUnicodeCapabilitiesAreExplicitAndIndependent(t *testing.T) {
	response := sampleStatus()
	var styled bytes.Buffer
	New(&styled, Options{
		Color: config.ColorAlways, Environment: map[string]string{},
		Capabilities: &Capabilities{TTY: true, Width: 80, Unicode: true},
	}).Status(response)
	if !strings.Contains(styled.String(), "\x1b[") || !strings.Contains(styled.String(), "•") {
		t.Fatalf("styled output = %q", styled.String())
	}

	var plain bytes.Buffer
	New(&plain, Options{
		Color: config.ColorAuto, Environment: map[string]string{"NO_COLOR": "1"},
		Capabilities: &Capabilities{TTY: false, Width: 80, Unicode: false},
	}).Status(response)
	if strings.Contains(plain.String(), "\x1b[") || !strings.HasPrefix(plain.String(), "! Repository status") {
		t.Fatalf("plain output = %q", plain.String())
	}
}

func TestDetailedLayoutWrapsAtConfiguredWidths(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		var output bytes.Buffer
		New(&output, Options{
			Color: config.ColorNever, Environment: map[string]string{},
			Capabilities: &Capabilities{Width: width},
		}).Status(sampleStatus())
		for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
			if len([]rune(line)) > width {
				t.Fatalf("width %d line has %d runes: %q", width, len([]rune(line)), line)
			}
		}
		golden := filepath.Join("testdata", fmt.Sprintf("status-%d.golden", width))
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("read %s: %v\n--- output ---\n%s", golden, err, output.String())
		}
		if output.String() != string(want) {
			t.Fatalf("width %d output differs\n--- got ---\n%s--- want ---\n%s", width, output.String(), want)
		}
	}
}

func TestUndoLayoutFitsNarrowAndWideRedirectedOutput(t *testing.T) {
	response := app.UndoResponse{
		Eligibility: app.UndoUnavailable, RollbackKind: app.UndoUnavailable,
		Source:      app.UndoSource{ExecutionID: "exec_fixture", Workflow: "commit", Status: "succeeded", PlanID: "plan_commit_fixture", PlanDigest: "sha256:fixture"},
		Git:         app.UndoGitState{ProjectRoot: "/workspace/Devtize", Branch: "main", HeadCommit: strings.Repeat("a", 40), WorkingTree: "clean"},
		Reasons:     []app.UndoReason{{Code: app.ReasonCommitPublished, Message: "The recorded commit is already the live upstream commit."}},
		Limitations: []string{"Plan only; execution is not implemented in this milestone."},
	}
	for _, width := range []int{60, 80, 120} {
		var output bytes.Buffer
		New(&output, Options{Color: config.ColorAuto, Environment: map[string]string{}, Capabilities: &Capabilities{Width: width}}).Undo(response)
		if strings.Contains(output.String(), "\x1b[") || !strings.HasSuffix(output.String(), "No changes were made.\n") {
			t.Fatalf("width %d output=%q", width, output.String())
		}
		for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
			if len([]rune(line)) > width {
				t.Fatalf("width %d line has %d runes: %q", width, len([]rune(line)), line)
			}
		}
	}
}

func TestSyncPlanResultWarningAndErrorGoldens(t *testing.T) {
	response := sampleSync()
	for _, width := range []int{60, 80, 120} {
		var output bytes.Buffer
		renderer := New(&output, Options{Color: config.ColorNever, Environment: map[string]string{}, Capabilities: &Capabilities{Width: width}})
		renderer.SyncPlan(response)
		result := response
		result.Status = operation.StatusSucceeded
		result.CacheAfter = app.SyncCacheState{Status: registry.CacheExact, Digest: "sha256:new", KnowledgeVersion: "2.50.1", Commands: 149, FilesScanned: 1}
		result.PublishedCount = 149
		result.Result = operation.ExecutionResult{Status: operation.StatusSucceeded, Steps: []operation.StepResult{{CapabilityID: "registry.knowledge.publish", Status: operation.StatusSucceeded}}}
		renderer.SyncResult(result)
		renderer.Error(&app.Error{Code: app.CodeSyncFailed, Message: "Git help inventory could not be synchronized", Hint: "The last valid cache was retained."})
		for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
			if len([]rune(line)) > width {
				t.Fatalf("width %d line has %d runes: %q", width, len([]rune(line)), line)
			}
		}
		golden := filepath.Join("testdata", fmt.Sprintf("sync-%d.golden", width))
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("read %s: %v\n--- output ---\n%s", golden, err, output.String())
		}
		if output.String() != string(want) {
			t.Fatalf("width %d output differs\n--- got ---\n%s--- want ---\n%s", width, output.String(), want)
		}
	}
}

func TestGitHubSyncOutputShowsSafeCountsAtSupportedWidths(t *testing.T) {
	response := sampleSync()
	response.Provider = "gh"
	response.Installation = detect.ToolInstallation{Version: "2.93.0", Path: "/tools/gh"}
	response.Parser = app.SyncParser{ID: "gh-help-reference", Version: "1"}
	response.SourceArgv = []string{"gh", "help", "reference"}
	response.Limits = registry.SyncLimits{MaxDepth: 2, MaxCommands: 512, MaxOutputBytes: 2 << 20, TimeoutMillis: 15_000, MaxFlags: 4096, MaxAliases: 1024}
	response.ParsedCount = 207
	response.ParsedAliasCount = 45
	response.ParsedFlagCount = 916
	for _, width := range []int{60, 80, 120} {
		var output bytes.Buffer
		renderer := New(&output, Options{Color: config.ColorNever, Environment: map[string]string{}, Capabilities: &Capabilities{Width: width}})
		renderer.SyncStarted("gh")
		renderer.SyncPlan(response)
		result := response
		result.Status = operation.StatusSucceeded
		result.PublishedCount = 207
		result.PublishedAliasCount = 45
		result.PublishedFlagCount = 916
		result.CacheAfter = app.SyncCacheState{Status: registry.CacheExact, Commands: 207, Aliases: 45, Flags: 916}
		result.Result = operation.ExecutionResult{Status: operation.StatusSucceeded}
		renderer.SyncResult(result)
		if !strings.Contains(output.String(), "parsed aliases: 45") || !strings.Contains(output.String(), "flags: 916") || strings.Contains(output.String(), "gh auth token") {
			t.Fatalf("width %d output=%s", width, output.String())
		}
		for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
			if len([]rune(line)) > width {
				t.Fatalf("width %d line=%q", width, line)
			}
		}
	}
}

func sampleStatus() app.StatusResponse {
	return app.StatusResponse{
		ProjectRoot:        "/workspace/Devtize",
		Repository:         app.RepositoryStatus{IsRepository: true, Branch: "main", HeadCommit: strings.Repeat("a", 40), WorkingTree: "dirty"},
		Changes:            app.ChangeStatus{Unstaged: []string{"internal/app/ship_workflow.go"}, Untracked: []string{"internal/ui/renderer.go"}, IgnoredCount: 1},
		Upstream:           app.UpstreamStatus{Name: "origin/main", Ahead: 2, Relation: "ahead"},
		LiveRemote:         app.LiveRemoteStatus{Name: "origin", URLs: []string{"https://github.com/FornaxChemica/Devtize.git"}},
		Warnings:           []string{"The index is unchanged, but the working tree contains reviewed implementation work."},
		RecommendedActions: []string{"Run dvz ship with explicit paths and a reviewed Conventional Commit message when these changes are ready."},
	}
}

func sampleSync() app.SyncResponse {
	plan := operation.Plan{
		SchemaVersion: 1, ID: "plan_sync_git_fixture", Intent: "Publish validated Git discovery knowledge to the local cache",
		CreatedAt: time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC), DryRun: true, Digest: "sha256:fixture",
		Operations: []operation.Operation{{
			CapabilityID: "registry.knowledge.publish", Risk: safety.RiskLocalWrite, Summary: "Publish validated Git command knowledge",
			Effects: []operation.Effect{{Kind: "write_registry_cache", Target: "/home/dev/.cache/devtize/registry/v1/git"}},
		}},
	}
	return app.SyncResponse{
		Status: operation.StatusValidated, Installation: detect.ToolInstallation{Version: "2.50.1", Path: "/usr/bin/git"},
		Parser: app.SyncParser{ID: "git-help-all", Version: "1"}, SourceArgv: []string{"git", "help", "--all", "--no-external-commands", "--no-aliases", "--verbose"},
		Limits:      registry.SyncLimits{MaxDepth: 1, MaxCommands: 256, MaxOutputBytes: 1 << 20, TimeoutMillis: 10_000},
		CacheBefore: app.SyncCacheState{Status: registry.CacheInvalid}, ParsedCount: 149, Plan: plan,
		Warnings: []string{"ignored invalid registry cache snapshot: snapshot-invalid.json"}, PlanWarningCount: 1,
	}
}
