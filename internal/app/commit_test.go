package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeCommitGit struct {
	state       gitadapter.InspectRepoResult
	changes     gitadapter.ChangeSet
	message     string
	stageCalls  int
	commitCalls int
	failStage   bool
	failCommit  bool
}

func (g *fakeCommitGit) InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error) {
	return g.state, nil
}

func (g *fakeCommitGit) InspectChanges(context.Context, string) (gitadapter.ChangeSet, error) {
	return g.changes, nil
}

func (g *fakeCommitGit) InspectHeadMessage(context.Context, string) (string, error) {
	return g.message, nil
}

func (g *fakeCommitGit) Stage(_ context.Context, input gitadapter.StageInput) error {
	g.stageCalls++
	if g.failStage {
		return errors.New("stage failed")
	}
	g.changes.Staged = append([]string{}, input.Paths...)
	g.changes.Unstaged = removePaths(g.changes.Unstaged, input.Paths)
	g.changes.Untracked = removePaths(g.changes.Untracked, input.Paths)
	return nil
}

func (g *fakeCommitGit) CreateCommit(_ context.Context, input gitadapter.CommitInput) error {
	g.commitCalls++
	if g.failCommit {
		return errors.New("commit failed")
	}
	g.message = input.Message
	g.state.HeadCommit = "next456"
	g.state.CommitCount++
	g.changes.Staged = nil
	return nil
}

func removePaths(values, remove []string) []string {
	set := stringSet(remove)
	var result []string
	for _, value := range values {
		if !set[value] {
			result = append(result, value)
		}
	}
	return result
}

func TestValidateCommitMessage(t *testing.T) {
	for _, valid := range []string{"feat: add commit workflow", "fix(cli): preserve literal paths", "feat!: change schema", "chore(build)!: update pipeline"} {
		if err := ValidateCommitMessage(valid, true); err != nil {
			t.Fatalf("valid message %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"Add workflow", "feat:", "unknown: change", "feat: first\nsecond"} {
		if err := ValidateCommitMessage(invalid, true); err == nil {
			t.Fatalf("invalid message accepted: %q", invalid)
		}
	}
	if err := ValidateCommitMessage("Plain explicit message", false); err != nil {
		t.Fatalf("non-conventional opt-out rejected: %v", err)
	}
}

func TestCommitPlanDisclosesOnlySelectedChangedPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "changed")
	writeFile(t, dir, "docs/plans/phase-c.md", "planning")
	git := baseCommitGit()
	git.changes = gitadapter.ChangeSet{Unstaged: []string{"README.md", "docs/plans/phase-c.md"}}
	service := testCommitService(dir, git)
	response, err := service.Plan(context.Background(), CommitOptions{Paths: []string{"README.md"}, Message: "docs: update project status", Conventional: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !reflect.DeepEqual(response.Selection.Paths, []string{"README.md"}) || response.Selection.Mode != "explicit_paths" {
		t.Fatalf("selection = %#v", response.Selection)
	}
	if got := stringSliceInput(response.Plan.Operations[0], "paths"); !reflect.DeepEqual(got, []string{"README.md"}) {
		t.Fatalf("planned paths = %#v", got)
	}
	if response.Plan.Digest == "" {
		t.Fatal("plan digest is empty")
	}
}

func TestCommitPlanRejectsStagedIgnoredAndEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/ignored.txt\n")
	writeFile(t, dir, "ignored.txt", "ignored")
	git := baseCommitGit()
	git.changes = gitadapter.ChangeSet{Staged: []string{"README.md"}}
	service := testCommitService(dir, git)
	_, err := service.Plan(context.Background(), CommitOptions{Message: "test: staged", Conventional: true})
	if err == nil || !strings.Contains(err.Error(), "clean index") {
		t.Fatalf("staged index accepted: %v", err)
	}

	git.changes = gitadapter.ChangeSet{Untracked: []string{"ignored.txt"}, Ignored: []string{"ignored.txt"}}
	_, err = service.Plan(context.Background(), CommitOptions{Paths: []string{"ignored.txt"}, Message: "test: ignored", Conventional: true})
	if err == nil || !strings.Contains(err.Error(), "ignored") {
		t.Fatalf("ignored path accepted: %v", err)
	}
	_, err = service.Plan(context.Background(), CommitOptions{Paths: []string{"../outside"}, Message: "test: escape", Conventional: true})
	if err == nil || !strings.Contains(err.Error(), "relative paths") {
		t.Fatalf("escaping path accepted: %v", err)
	}
}

func TestCommitDryRunAndDeclineDoNotMutate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "changed")
	git := baseCommitGit()
	git.changes = gitadapter.ChangeSet{Unstaged: []string{"README.md"}}
	service := testCommitService(dir, git)
	options := CommitOptions{Message: "docs: update readme", Conventional: true, DryRun: true}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("commit\n"), response); err != nil {
		t.Fatal(err)
	}
	if git.stageCalls+git.commitCalls != 0 {
		t.Fatalf("dry-run mutated: %#v", git)
	}

	options.DryRun = false
	response, err = service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("no\n"), response)
	if err == nil || git.stageCalls+git.commitCalls != 0 {
		t.Fatalf("declined commit mutated: err=%v git=%#v", err, git)
	}
}

func TestCommitRejectsChangedPlanAndStaleContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "before")
	git := baseCommitGit()
	git.changes = gitadapter.ChangeSet{Unstaged: []string{"README.md"}}
	service := testCommitService(dir, git)
	options := CommitOptions{Message: "docs: update readme", Conventional: true}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	response.Plan.Operations[1].Inputs["message"] = "feat: tampered"
	_, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("commit\n"), response)
	if err == nil || git.stageCalls != 0 {
		t.Fatalf("changed plan mutated: err=%v", err)
	}

	response, err = service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "README.md", "after planning")
	_, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("commit\n"), response)
	if err == nil || !strings.Contains(err.Error(), "content changed") || git.stageCalls != 0 {
		t.Fatalf("stale content mutated: err=%v", err)
	}
}

func TestCommitRecordsPartialExecutionWhenCommitFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "changed")
	git := baseCommitGit()
	git.changes = gitadapter.ChangeSet{Unstaged: []string{"README.md"}}
	git.failCommit = true
	service := testCommitService(dir, git)
	options := CommitOptions{Message: "docs: update readme", Conventional: true}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("commit\n"), response)
	if err == nil || response.Result.Status != operation.StatusPartiallyCompleted || git.stageCalls != 1 || git.commitCalls != 1 {
		t.Fatalf("partial result=%#v err=%v git=%#v", response.Result, err, git)
	}
}

func TestCommitSucceedsInTemporaryRepositoryWithExplicitPath(t *testing.T) {
	dir := t.TempDir()
	runCommitGit(t, dir, "init", "--initial-branch", "main")
	runCommitGit(t, dir, "config", "user.name", "Devtize Test")
	runCommitGit(t, dir, "config", "user.email", "devtize-test@example.invalid")
	writeFile(t, dir, "README.md", "before\n")
	runCommitGit(t, dir, "add", "--", "README.md")
	runCommitGit(t, dir, "commit", "--message", "chore: initial fixture")
	writeFile(t, dir, "README.md", "after\n")
	writeFile(t, dir, "planning.md", "leave uncommitted\n")

	cfg := config.Defaults()
	cfg.History.Enabled = false
	service := CommitService{WorkingDir: dir, Config: cfg, Runner: devprocess.NewRunner(), Now: func() time.Time { return time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC) }}
	options := CommitOptions{Paths: []string{"README.md"}, Message: "docs: update readme", Conventional: true}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	response, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("commit\n"), response)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.Status != operation.StatusSucceeded {
		t.Fatalf("status = %s", response.Status)
	}
	if got := strings.TrimSpace(runCommitGit(t, dir, "log", "-1", "--format=%s")); got != options.Message {
		t.Fatalf("message = %q", got)
	}
	if got := strings.TrimSpace(runCommitGit(t, dir, "status", "--short")); got != "?? planning.md" {
		t.Fatalf("unexpected remaining status: %q", got)
	}
}

func baseCommitGit() *fakeCommitGit {
	return &fakeCommitGit{state: gitadapter.InspectRepoResult{
		IsRepository: true, HasCommits: true, CommitCount: 1, HeadBranch: "main", HeadCommit: "base123", WorkingTreeStatus: "dirty",
	}}
}

func testCommitService(dir string, git *fakeCommitGit) CommitService {
	cfg := config.Defaults()
	cfg.History.Enabled = false
	return CommitService{WorkingDir: dir, Config: cfg, Git: git, Now: func() time.Time { return time.Date(2026, 9, 15, 2, 0, 0, 0, time.UTC) }, Output: &strings.Builder{}}
}

func runCommitGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
