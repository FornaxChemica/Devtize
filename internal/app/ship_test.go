package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeShipGit struct {
	state       gitadapter.InspectRepoResult
	changes     gitadapter.ChangeSet
	liveRemote  string
	commits     []gitadapter.CommitSummary
	ancestor    bool
	pushCalls   int
	failPush    bool
	remoteAfter string
}

func (g *fakeShipGit) InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error) {
	return g.state, nil
}

func (g *fakeShipGit) InspectChanges(context.Context, string) (gitadapter.ChangeSet, error) {
	return g.changes, nil
}

func (g *fakeShipGit) InspectRemoteBranch(context.Context, gitadapter.InspectRemoteBranchInput) (string, error) {
	return g.liveRemote, nil
}

func (g *fakeShipGit) IsAncestor(context.Context, string, string, string) (bool, error) {
	return g.ancestor, nil
}

func (g *fakeShipGit) InspectOutgoingCommits(context.Context, gitadapter.InspectOutgoingCommitsInput) ([]gitadapter.CommitSummary, error) {
	return append([]gitadapter.CommitSummary{}, g.commits...), nil
}

func (g *fakeShipGit) PushExistingBranch(context.Context, gitadapter.PushExistingBranchInput) error {
	g.pushCalls++
	if g.failPush {
		return errors.New("push failed")
	}
	g.liveRemote = g.state.HeadCommit
	g.state.UpstreamCommit = g.state.HeadCommit
	return nil
}

func TestShipPlanDisclosesCommitsRemoteAndExcludedChanges(t *testing.T) {
	dir := t.TempDir()
	git := baseShipGit()
	git.changes = gitadapter.ChangeSet{Unstaged: []string{"docs/plans/phase-b.md"}, Untracked: []string{"docs/plans/phase-c.md"}}
	service := testShipService(dir, git)
	response, err := service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if response.Plan.Digest == "" || len(response.Plan.Operations) != 1 || response.Plan.Operations[0].Risk != "remote_write" {
		t.Fatalf("plan = %#v", response.Plan)
	}
	wantExcluded := []string{"docs/plans/phase-b.md", "docs/plans/phase-c.md"}
	if !reflect.DeepEqual(response.ExcludedPaths, wantExcluded) || !reflect.DeepEqual(stringSliceInput(response.Plan.Operations[0], "excluded_working_paths"), wantExcluded) {
		t.Fatalf("excluded paths = %#v", response.ExcludedPaths)
	}
	if response.RemoteCommit != shipBaseSHA || response.Commits[0].SHA != shipHeadSHA || response.RemoteURL != "https://github.com/OWNER/repo.git" {
		t.Fatalf("response = %#v", response)
	}
}

func TestShipDryRunAndDeclineDoNotPush(t *testing.T) {
	dir := t.TempDir()
	git := baseShipGit()
	service := testShipService(dir, git)
	options := ShipOptions{DryRun: true}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("push\n"), response); err != nil {
		t.Fatal(err)
	}
	if git.pushCalls != 0 {
		t.Fatal("dry-run pushed")
	}

	options.DryRun = false
	response, err = service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExecutePlanned(context.Background(), options, strings.NewReader("no\n"), response)
	if err == nil || git.pushCalls != 0 {
		t.Fatalf("declined ship pushed: err=%v calls=%d", err, git.pushCalls)
	}
}

func TestShipRejectsChangedPlanRemoteAndExcludedChanges(t *testing.T) {
	dir := t.TempDir()
	git := baseShipGit()
	service := testShipService(dir, git)
	response, err := service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response.Plan.Operations[0].Inputs["branch"] = "other"
	_, err = service.ExecutePlanned(context.Background(), ShipOptions{}, strings.NewReader("push\n"), response)
	if err == nil || git.pushCalls != 0 {
		t.Fatalf("changed plan pushed: err=%v", err)
	}

	response, err = service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response.RemoteURL = "https://github.com/ATTACKER/repo.git"
	_, err = service.ExecutePlanned(context.Background(), ShipOptions{}, strings.NewReader("push\n"), response)
	if err == nil || !strings.Contains(err.Error(), "rendered ship details") || git.pushCalls != 0 {
		t.Fatalf("mismatched rendered target pushed: err=%v", err)
	}

	response, err = service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git.liveRemote = strings.Repeat("d", 40)
	_, err = service.ExecutePlanned(context.Background(), ShipOptions{}, strings.NewReader("push\n"), response)
	if err == nil || !strings.Contains(err.Error(), "live remote") || git.pushCalls != 0 {
		t.Fatalf("stale remote pushed: err=%v", err)
	}

	git = baseShipGit()
	service = testShipService(dir, git)
	response, err = service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git.changes.Untracked = []string{"new.txt"}
	_, err = service.ExecutePlanned(context.Background(), ShipOptions{}, strings.NewReader("push\n"), response)
	if err == nil || !strings.Contains(err.Error(), "excluded working changes") || git.pushCalls != 0 {
		t.Fatalf("changed exclusions pushed: err=%v", err)
	}
}

func TestShipRejectsDivergenceAndStaleTracking(t *testing.T) {
	dir := t.TempDir()
	git := baseShipGit()
	git.ancestor = false
	_, err := testShipService(dir, git).Plan(context.Background(), ShipOptions{})
	if err == nil || !strings.Contains(err.Error(), "not an ancestor") {
		t.Fatalf("divergence accepted: %v", err)
	}

	git = baseShipGit()
	git.liveRemote = strings.Repeat("d", 40)
	_, err = testShipService(dir, git).Plan(context.Background(), ShipOptions{})
	if err == nil || !strings.Contains(err.Error(), "live remote changed") {
		t.Fatalf("stale tracking accepted: %v", err)
	}
}

func TestShipFailureRecordsUncertainRemoteRecovery(t *testing.T) {
	dir := t.TempDir()
	git := baseShipGit()
	git.failPush = true
	service := testShipService(dir, git)
	response, err := service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecutePlanned(context.Background(), ShipOptions{}, strings.NewReader("push\n"), response)
	if err == nil || response.Result.Status != operation.StatusPartiallyCompleted || git.pushCalls != 1 || !strings.Contains(response.Result.RecoveryHints[0], "uncertain") {
		t.Fatalf("result=%#v err=%v calls=%d", response.Result, err, git.pushCalls)
	}
}

func TestShipIsIdempotentWhenLiveRemoteAlreadyMatches(t *testing.T) {
	dir := t.TempDir()
	git := baseShipGit()
	git.liveRemote = shipHeadSHA
	git.state.UpstreamCommit = shipHeadSHA
	git.commits = nil
	service := testShipService(dir, git)
	response, err := service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Plan.Operations) != 0 {
		t.Fatalf("idempotent plan has operations: %#v", response.Plan.Operations)
	}
	response, err = service.ExecutePlanned(context.Background(), ShipOptions{}, nil, response)
	if err != nil || response.Status != operation.StatusSucceeded || git.pushCalls != 0 {
		t.Fatalf("response=%#v err=%v calls=%d", response, err, git.pushCalls)
	}
}

func TestShipPushesToLocalBareRemoteAndPreservesDirtyFiles(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	runShipGit(t, "", "init", "--bare", bare)
	runShipGit(t, root, "init", "--initial-branch", "main")
	runShipGit(t, root, "config", "user.name", "Devtize Test")
	runShipGit(t, root, "config", "user.email", "devtize-test@example.invalid")
	writeFile(t, root, "README.md", "initial\n")
	runShipGit(t, root, "add", "--", "README.md")
	runShipGit(t, root, "commit", "--message", "chore: initial fixture")
	runShipGit(t, root, "remote", "add", "origin", bare)
	runShipGit(t, root, "push", "--set-upstream", "origin", "main")
	base := strings.TrimSpace(runShipGit(t, root, "rev-parse", "HEAD"))
	writeFile(t, root, "README.md", "second\n")
	runShipGit(t, root, "add", "--", "README.md")
	runShipGit(t, root, "commit", "--message", "feat: second commit")
	head := strings.TrimSpace(runShipGit(t, root, "rev-parse", "HEAD"))
	writeFile(t, root, "docs/plans/phase-c.md", "leave dirty\n")

	cfg := config.Defaults()
	cfg.History.Enabled = false
	service := ShipService{WorkingDir: root, Config: cfg, Runner: devprocess.NewRunner(), Now: func() time.Time { return time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC) }}
	response, err := service.Plan(context.Background(), ShipOptions{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if response.RemoteCommit != base || len(response.Commits) != 1 || response.Commits[0].SHA != head || !reflect.DeepEqual(response.ExcludedPaths, []string{"docs/plans/phase-c.md"}) {
		t.Fatalf("response = %#v", response)
	}
	response, err = service.ExecutePlanned(context.Background(), ShipOptions{}, strings.NewReader("push\n"), response)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.Status != operation.StatusSucceeded || strings.TrimSpace(runShipGit(t, bare, "rev-parse", "refs/heads/main")) != head {
		t.Fatalf("ship did not update remote: %#v", response)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/plans/phase-c.md")); err != nil {
		t.Fatalf("dirty plan file was not preserved: %v", err)
	}

	response, err = service.Plan(context.Background(), ShipOptions{})
	if err != nil || len(response.Plan.Operations) != 0 {
		t.Fatalf("rerun was not idempotent: response=%#v err=%v", response, err)
	}
}

const (
	shipBaseSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shipHeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func baseShipGit() *fakeShipGit {
	return &fakeShipGit{
		state: gitadapter.InspectRepoResult{
			IsRepository: true, HasCommits: true, CommitCount: 2, HeadBranch: "main", HeadCommit: shipHeadSHA,
			WorkingTreeStatus: "dirty", RemoteURLs: []string{"https://github.com/OWNER/repo.git"}, Upstream: "origin/main", UpstreamCommit: shipBaseSHA,
		},
		liveRemote: shipBaseSHA, ancestor: true,
		commits: []gitadapter.CommitSummary{{SHA: shipHeadSHA, Subject: "feat: ship safely"}},
	}
}

func testShipService(dir string, git *fakeShipGit) ShipService {
	cfg := config.Defaults()
	cfg.History.Enabled = false
	return ShipService{WorkingDir: dir, Config: cfg, Git: git, Now: func() time.Time { return time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC) }, Output: &strings.Builder{}}
}

func runShipGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	if dir != "" {
		command.Dir = dir
	}
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
