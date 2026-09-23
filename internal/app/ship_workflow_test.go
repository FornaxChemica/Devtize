package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golangadapter "github.com/FornaxChemica/devtize/internal/adapters/golang"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeWorkflowChecks struct {
	calls int
	fail  bool
}

func (f *fakeWorkflowChecks) Detect(context.Context, string) (golangadapter.Toolchain, error) {
	return golangadapter.Toolchain{GoPath: "/tools/go", GofmtPath: "/tools/gofmt", GoVersion: "go version go1.27.0"}, nil
}

func (f *fakeWorkflowChecks) Run(_ context.Context, input golangadapter.CheckInput) (golangadapter.CheckResult, error) {
	f.calls++
	result := golangadapter.CheckResult{CapabilityID: input.CapabilityID, Status: "succeeded"}
	if f.fail {
		result.Status = "failed"
		result.Diagnostic = "token=should-redact"
		return result, golangadapter.ErrCheckFailed
	}
	return result, nil
}

func TestComposedShipChecksCommitsThenBuildsExactPushPlan(t *testing.T) {
	repo, bare, base := workflowRepository(t)
	writeFile(t, repo, "README.md", "next\n")
	checks := &fakeWorkflowChecks{}
	service := testWorkflowService(repo, checks)
	options := ShipWorkflowOptions{
		Paths: []string{"README.md"}, Message: "docs: update readme", Conventional: true,
		Checks: []string{"go.test"}, Remote: "origin",
	}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "compose" || response.Remote.RemoteCommit != base || response.Push != nil || len(checkOperations(response.Local.Plan)) != 1 {
		t.Fatalf("plan = %#v", response)
	}
	if _, err := service.PlanPush(context.Background(), options, response); err == nil {
		t.Fatal("push plan was created before the local plan succeeded")
	}
	response, err = service.ExecuteLocal(context.Background(), options, strings.NewReader("commit\n"), response)
	if err != nil {
		t.Fatalf("execute local: %v", err)
	}
	if checks.calls != 1 || response.Local.Status != operation.StatusSucceeded {
		t.Fatalf("checks=%d response=%#v", checks.calls, response)
	}
	newHead := strings.TrimSpace(runShipGit(t, repo, "rev-parse", "HEAD"))
	if newHead == base || strings.TrimSpace(runShipGit(t, bare, "rev-parse", "refs/heads/main")) != base {
		t.Fatal("local phase did not stop before the remote boundary")
	}
	options.Remote = "attacker"
	response, err = service.PlanPush(context.Background(), options, response)
	if err != nil || response.Push == nil || response.Push.RemoteName != "origin" || len(response.Push.Commits) != 1 || response.Push.Commits[0].SHA != newHead {
		t.Fatalf("push plan=%#v err=%v", response.Push, err)
	}
	response, err = service.ExecutePush(context.Background(), options, strings.NewReader("push\n"), response)
	if err != nil || response.Status != operation.StatusSucceeded {
		t.Fatalf("execute push: response=%#v err=%v", response, err)
	}
	if remote := strings.TrimSpace(runShipGit(t, bare, "rev-parse", "refs/heads/main")); remote != newHead {
		t.Fatalf("remote=%s want=%s", remote, newHead)
	}
}

func TestComposedShipFailedCheckCausesNoGitMutation(t *testing.T) {
	repo, bare, base := workflowRepository(t)
	writeFile(t, repo, "README.md", "next\n")
	checks := &fakeWorkflowChecks{fail: true}
	service := testWorkflowService(repo, checks)
	service.Config.History.Enabled = true
	service.History = history.Store{Path: filepath.Join(t.TempDir(), "history.jsonl")}
	options := ShipWorkflowOptions{Message: "docs: update readme", Conventional: true, Checks: []string{"go.test"}, Remote: "origin"}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecuteLocal(context.Background(), options, strings.NewReader("commit\n"), response)
	if err == nil || response.Status != operation.StatusFailed || response.Checks[0].Diagnostic != "token=<redacted>" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if head := strings.TrimSpace(runShipGit(t, repo, "rev-parse", "HEAD")); head != base {
		t.Fatalf("failed check changed HEAD to %s", head)
	}
	if remote := strings.TrimSpace(runShipGit(t, bare, "rev-parse", "refs/heads/main")); remote != base {
		t.Fatalf("failed check changed remote to %s", remote)
	}
	if status := runShipGit(t, repo, "status", "--short"); status != " M README.md\n" {
		t.Fatalf("failed check changed working state: %q", status)
	}
	records, historyErr := service.History.List(history.ListOptions{ProjectRoot: repo, Limit: 10})
	if historyErr != nil || len(records.Records) != 1 || records.Records[0].WorkflowID != response.WorkflowID || records.Records[0].Status != operation.StatusFailed {
		t.Fatalf("history=%#v err=%v", records, historyErr)
	}
}

func TestComposedShipDryRunRunsNoChecksOrMutations(t *testing.T) {
	repo, bare, base := workflowRepository(t)
	writeFile(t, repo, "README.md", "next\n")
	checks := &fakeWorkflowChecks{}
	service := testWorkflowService(repo, checks)
	options := ShipWorkflowOptions{Message: "docs: update readme", Conventional: true, Checks: []string{"go.test"}, Remote: "origin", DryRun: true}
	response, err := service.Plan(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecuteLocal(context.Background(), options, strings.NewReader("commit\n"), response)
	if err != nil || checks.calls != 0 || response.Status != operation.StatusValidated {
		t.Fatalf("response=%#v calls=%d err=%v", response, checks.calls, err)
	}
	if head := strings.TrimSpace(runShipGit(t, repo, "rev-parse", "HEAD")); head != base {
		t.Fatalf("dry-run changed HEAD to %s", head)
	}
	if remote := strings.TrimSpace(runShipGit(t, bare, "rev-parse", "refs/heads/main")); remote != base {
		t.Fatalf("dry-run changed remote to %s", remote)
	}
}

func TestComposedShipRejectsUnknownCheckBeforeToolDetection(t *testing.T) {
	checks := &fakeWorkflowChecks{}
	service := testWorkflowService(t.TempDir(), checks)
	_, err := service.Plan(context.Background(), ShipWorkflowOptions{Message: "test: invalid", Conventional: true, Checks: []string{"shell.any"}})
	if err == nil || checks.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, checks.calls)
	}
}

func workflowRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	runShipGit(t, "", "init", "--bare", bare)
	runShipGit(t, repo, "init", "--initial-branch", "main")
	runShipGit(t, repo, "config", "user.name", "Devtize Test")
	runShipGit(t, repo, "config", "user.email", "devtize-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runShipGit(t, repo, "add", "--", "README.md")
	runShipGit(t, repo, "commit", "--message", "chore: initial fixture")
	runShipGit(t, repo, "remote", "add", "origin", bare)
	runShipGit(t, repo, "push", "--set-upstream", "origin", "main")
	return repo, bare, strings.TrimSpace(runShipGit(t, repo, "rev-parse", "HEAD"))
}

func testWorkflowService(repo string, checks ShipWorkflowCheckPort) ShipWorkflowService {
	cfg := config.Defaults()
	cfg.History.Enabled = false
	return ShipWorkflowService{
		WorkingDir: repo, Config: cfg, Runner: devprocess.NewRunner(), Checks: checks,
		Now: func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) }, Output: ioDiscard{},
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(content []byte) (int, error) { return len(content), nil }
