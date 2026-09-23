package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/config"
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
