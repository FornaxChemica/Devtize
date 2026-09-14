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
	ghadapter "github.com/FornaxChemica/devtize/internal/adapters/github"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeGit struct {
	state          gitadapter.InspectRepoResult
	initCalls      int
	stageCalls     int
	commitCalls    int
	untrackCalls   int
	amendCalls     int
	amendKeepCalls int
	forceCalls     int
	untrackedPaths []string
	liveRemote     string
	remoteCalls    int
	updateCalls    int
	pushCalls      int
	failCapability string
}

func (g *fakeGit) InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error) {
	return g.state, nil
}
func (g *fakeGit) InitRepo(context.Context, gitadapter.InitRepoInput) error {
	g.initCalls++
	return g.fail("git.repo.init")
}
func (g *fakeGit) Stage(context.Context, gitadapter.StageInput) error {
	g.stageCalls++
	return g.fail("git.index.stage")
}
func (g *fakeGit) CreateCommit(context.Context, gitadapter.CommitInput) error {
	g.commitCalls++
	return g.fail("git.commit.create")
}
func (g *fakeGit) Untrack(_ context.Context, input gitadapter.UntrackInput) error {
	g.untrackCalls++
	g.untrackedPaths = append(g.untrackedPaths, input.Paths...)
	remaining := g.state.TrackedPaths[:0]
	for _, tracked := range g.state.TrackedPaths {
		remove := false
		for _, path := range input.Paths {
			if tracked == path {
				remove = true
				break
			}
		}
		if !remove {
			remaining = append(remaining, tracked)
		}
	}
	g.state.TrackedPaths = remaining
	return g.fail("git.index.untrack")
}
func (g *fakeGit) AmendCommit(context.Context, gitadapter.CommitInput) error {
	g.amendCalls++
	if err := g.fail("git.commit.amend_initial"); err != nil {
		return err
	}
	g.state.WorkingTreeStatus = "clean"
	g.state.TrackedChanges = nil
	return nil
}
func (g *fakeGit) AmendCommitPreservingMessage(context.Context, string) error {
	g.amendKeepCalls++
	if err := g.fail("git.commit.amend_initial_preserve_message"); err != nil {
		return err
	}
	g.state.HeadCommit = "amended123"
	g.state.WorkingTreeStatus = "clean"
	g.state.TrackedChanges = nil
	g.state.UntrackedPaths = nil
	return nil
}
func (g *fakeGit) InspectRemote(context.Context, gitadapter.RemoteInput) ([]string, error) {
	return nil, nil
}
func (g *fakeGit) ConfigureRemote(context.Context, gitadapter.RemoteInput) error {
	g.remoteCalls++
	return g.fail("git.remote.configure")
}
func (g *fakeGit) UpdateRemote(_ context.Context, input gitadapter.RemoteInput) error {
	g.updateCalls++
	g.state.RemoteURLs = []string{input.URL}
	return g.fail("git.remote.update")
}
func (g *fakeGit) PushBranch(context.Context, gitadapter.PushInput) error {
	g.pushCalls++
	if err := g.fail("git.branch.push"); err != nil {
		return err
	}
	g.state.Upstream = "origin/main"
	return nil
}
func (g *fakeGit) ForcePushWithLease(context.Context, gitadapter.ForcePushWithLeaseInput) error {
	g.forceCalls++
	if err := g.fail("git.branch.force_push_with_lease"); err != nil {
		return err
	}
	g.state.UpstreamCommit = g.state.HeadCommit
	return nil
}
func (g *fakeGit) InspectRemoteBranch(context.Context, gitadapter.InspectRemoteBranchInput) (string, error) {
	if g.liveRemote != "" {
		return g.liveRemote, nil
	}
	return g.state.UpstreamCommit, nil
}
func (g *fakeGit) fail(capability string) error {
	if g.failCapability == capability {
		return errors.New("boom")
	}
	return nil
}

type fakeGH struct {
	auth        ghadapter.AuthResult
	repo        ghadapter.RepoResult
	createCalls int
}

func (g *fakeGH) InspectAuth(context.Context, ghadapter.AuthInput) (ghadapter.AuthResult, error) {
	return g.auth, nil
}
func (g *fakeGH) InspectRepo(context.Context, ghadapter.RepoInput) (ghadapter.RepoResult, error) {
	return g.repo, nil
}
func (g *fakeGH) CreateRepo(context.Context, ghadapter.RepoInput) (ghadapter.RepoResult, error) {
	g.createCalls++
	g.repo.Exists = true
	return g.repo, nil
}

func TestRepoPlanDryRunDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	git := &fakeGit{}
	gh := &fakeGH{auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"}}
	service := testRepoService(dir, git, gh)
	response, err := service.Plan(context.Background(), RepoOptions{Name: "repo", Message: "Initial commit", DryRun: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if response.Plan.Digest == "" || len(response.Plan.Operations) == 0 {
		t.Fatalf("plan was incomplete: %#v", response.Plan)
	}
	if git.initCalls+git.stageCalls+git.commitCalls+git.remoteCalls+git.updateCalls+git.pushCalls+gh.createCalls != 0 {
		t.Fatalf("dry-run plan mutated: git=%#v gh=%#v", git, gh)
	}
}

func TestRepoExecutePlannedRejectsChangedPlanDigest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	git := &fakeGit{}
	gh := &fakeGH{auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"}}
	service := testRepoService(dir, git, gh)
	response, err := service.Plan(context.Background(), RepoOptions{Name: "repo", Message: "chore: initial"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	response.Plan.Operations[0].Summary = "changed after display"
	_, err = service.ExecutePlanned(context.Background(), RepoOptions{Name: "repo", Message: "chore: initial"}, strings.NewReader("yes\n"), response)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected changed plan digest refusal, got %v", err)
	}
	if git.initCalls+git.stageCalls+git.commitCalls+git.pushCalls != 0 {
		t.Fatal("changed displayed plan reached mutation adapters")
	}
}

func TestRepoCreateDecliningLocalConfirmationMutatesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	git := &fakeGit{}
	gh := &fakeGH{auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"}}
	service := testRepoService(dir, git, gh)
	_, err := service.Execute(context.Background(), RepoOptions{Name: "repo", Message: "Initial commit"}, strings.NewReader("no\n"))
	if err == nil {
		t.Fatal("expected declined confirmation")
	}
	if git.initCalls+git.stageCalls+git.commitCalls+git.remoteCalls+git.updateCalls+git.pushCalls+gh.createCalls != 0 {
		t.Fatalf("declined confirmation mutated: git=%#v gh=%#v", git, gh)
	}
}

func TestRepoCreateRequiresSecretConfirmationEvenWithYes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "TOKEN=abc")
	git := &fakeGit{}
	gh := &fakeGH{auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"}}
	service := testRepoService(dir, git, gh)
	_, err := service.Execute(context.Background(), RepoOptions{Name: "repo", Message: "Initial commit", Yes: true}, strings.NewReader("no\n"))
	if err == nil {
		t.Fatal("expected secret confirmation decline")
	}
	if git.initCalls+git.stageCalls+git.commitCalls+git.remoteCalls+git.updateCalls+git.pushCalls+gh.createCalls != 0 {
		t.Fatalf("--yes bypassed secret confirmation: git=%#v gh=%#v", git, gh)
	}
}

func TestRepoCreateRecordsPartialFailureRecovery(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	git := &fakeGit{failCapability: "git.commit.create"}
	gh := &fakeGH{auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"}}
	service := testRepoService(dir, git, gh)
	response, err := service.Execute(context.Background(), RepoOptions{Name: "repo", Message: "Initial commit"}, strings.NewReader("yes\n"))
	if err == nil {
		t.Fatal("expected partial failure")
	}
	if response.Result.Status != operation.StatusPartiallyCompleted {
		t.Fatalf("status = %s", response.Result.Status)
	}
	if len(response.Result.RecoveryHints) == 0 {
		t.Fatal("expected recovery hint")
	}
	if gh.createCalls != 0 {
		t.Fatal("remote write ran after local failure")
	}
}

func TestRepoPlanBlocksUnexpectedOrigin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	git := &fakeGit{state: gitadapter.InspectRepoResult{IsRepository: true, RemoteURLs: []string{"git@github.com:OTHER/repo.git"}}}
	gh := &fakeGH{auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"}}
	service := testRepoService(dir, git, gh)
	_, err := service.Plan(context.Background(), RepoOptions{Name: "repo", Message: "Initial commit", Owner: "OWNER"})
	if err == nil {
		t.Fatal("expected unexpected origin error")
	}
}

func TestRepoPlanUpdatesOnlyEquivalentRemoteToAuthenticatedProtocol(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	git := &fakeGit{state: gitadapter.InspectRepoResult{
		IsRepository: true, HasCommits: true, CommitCount: 1, HeadBranch: "main", HeadCommit: "abc", WorkingTreeStatus: "clean",
		RemoteURLs: []string{"git@github.com:OWNER/repo.git"},
	}}
	gh := repairableGH()
	gh.auth.GitProtocol = "https"
	service := testRepoService(dir, git, gh)
	response, err := service.Plan(context.Background(), RepoOptions{Name: "repo", Owner: "OWNER", Message: "chore: initial"})
	if err != nil {
		t.Fatalf("plan equivalent protocol update: %v", err)
	}
	if !planHasCapability(response.Plan, "git.remote.update") {
		t.Fatal("plan did not disclose equivalent remote protocol update")
	}
	for _, op := range response.Plan.Operations {
		if op.CapabilityID == "git.remote.update" && stringInput(op, "url") != "https://github.com/OWNER/repo.git" {
			t.Fatalf("remote update URL = %q", stringInput(op, "url"))
		}
	}
}

func TestEquivalentRemoteComparisonRejectsDifferentRepository(t *testing.T) {
	if !sameGitHubRepository("git@github.com:OWNER/repo.git", "https://github.com/owner/repo.git") {
		t.Fatal("equivalent GitHub URLs were not recognized")
	}
	if sameGitHubRepository("git@github.com:OWNER/other.git", "https://github.com/owner/repo.git") {
		t.Fatal("different GitHub repositories were treated as equivalent")
	}
}

func TestRepoPlanBlocksDirtyExistingCommitWithoutExplicitRepair(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "changed")
	git := &fakeGit{state: repairableGitState()}
	gh := repairableGH()
	service := testRepoService(dir, git, gh)
	_, err := service.Plan(context.Background(), RepoOptions{Name: "repo", Owner: "OWNER", Message: "chore: initial"})
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("expected dirty repository refusal, got %v", err)
	}
}

func TestRepoRepairDryRunPlansAmendWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/.cache/\n/dvz\n")
	writeFile(t, dir, "README.md", "changed")
	writeFile(t, dir, "dvz", "binary")
	git := &fakeGit{state: repairableGitState()}
	git.state.TrackedPaths = []string{".cache/go-build/item", "README.md", "dvz"}
	gh := repairableGH()
	service := testRepoService(dir, git, gh)
	response, err := service.Plan(context.Background(), RepoOptions{Name: "repo", Owner: "OWNER", Message: "chore: self-host Devtize with Devtize", RepairInitial: true, DryRun: true})
	if err != nil {
		t.Fatalf("plan repair: %v", err)
	}
	if !reflect.DeepEqual(response.Selection.UntrackPaths, []string{".cache/go-build/item", "dvz"}) {
		t.Fatalf("untrack paths = %#v", response.Selection.UntrackPaths)
	}
	if !planHasCapability(response.Plan, "git.commit.amend_initial") {
		t.Fatal("repair plan did not disclose initial commit amendment")
	}
	if git.untrackCalls+git.stageCalls+git.amendCalls+git.pushCalls != 0 {
		t.Fatal("repair dry-run mutated repository")
	}
}

func TestRepoRepairRequiresTypedConfirmationEvenWithYes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "changed")
	git := &fakeGit{state: repairableGitState()}
	gh := repairableGH()
	service := testRepoService(dir, git, gh)
	_, err := service.Execute(context.Background(), RepoOptions{Name: "repo", Owner: "OWNER", Message: "chore: initial", RepairInitial: true, Yes: true}, strings.NewReader("yes\n"))
	if err == nil {
		t.Fatal("expected typed repair confirmation refusal")
	}
	if git.stageCalls+git.amendCalls+git.pushCalls != 0 {
		t.Fatal("repair confirmation was bypassed")
	}
}

func TestRepoRepairRejectsPublishedBranch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "changed")
	git := &fakeGit{state: repairableGitState()}
	gh := repairableGH()
	gh.repo.DefaultBranch = "main"
	service := testRepoService(dir, git, gh)
	_, err := service.Plan(context.Background(), RepoOptions{Name: "repo", Owner: "OWNER", Message: "chore: initial", RepairInitial: true})
	if err == nil || !strings.Contains(err.Error(), "published default branch") {
		t.Fatalf("expected published branch refusal, got %v", err)
	}
}

func TestInitialRepairPreconditionsRejectUnsafeStates(t *testing.T) {
	baseGit := repairableGitState()
	baseRepo := repairableGH().repo
	tests := []struct {
		name string
		git  gitadapter.InspectRepoResult
		repo ghadapter.RepoResult
		want string
	}{
		{name: "multiple commits", git: func() gitadapter.InspectRepoResult { state := baseGit; state.CommitCount = 2; return state }(), repo: baseRepo, want: "exactly one"},
		{name: "configured upstream", git: func() gitadapter.InspectRepoResult { state := baseGit; state.Upstream = "origin/main"; return state }(), repo: baseRepo, want: "upstream"},
		{name: "missing GitHub repository", git: baseGit, repo: ghadapter.RepoResult{}, want: "existing GitHub repository"},
		{name: "clean tree", git: func() gitadapter.InspectRepoResult { state := baseGit; state.WorkingTreeStatus = "clean"; return state }(), repo: baseRepo, want: "local changes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateInitialRepair(test.git, test.repo)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestSelectPathsHonorsRootedFileAndDirectoryIgnores(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/.cache/\n/dvz\n")
	writeFile(t, dir, ".cache/go-build/item", "generated")
	writeFile(t, dir, "dvz", "binary")
	writeFile(t, dir, "cmd/dvz/main.go", "package main")
	selection, err := selectPaths(dir, nil)
	if err != nil {
		t.Fatalf("select paths: %v", err)
	}
	if contains(selection.Paths, ".cache/go-build/item") || contains(selection.Paths, "dvz") {
		t.Fatalf("ignored generated artifacts were selected: %#v", selection.Paths)
	}
	if !contains(selection.Paths, ".gitignore") || !contains(selection.Paths, "cmd/dvz/main.go") {
		t.Fatalf("source paths were not selected: %#v", selection.Paths)
	}
}

func TestRepoRepairConsumesDestructiveAndRemoteConfirmations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "changed")
	git := &fakeGit{state: repairableGitState()}
	gh := repairableGH()
	service := testRepoService(dir, git, gh)
	response, err := service.Execute(context.Background(), RepoOptions{Name: "repo", Owner: "OWNER", Message: "chore: initial", RepairInitial: true, Yes: true}, strings.NewReader("repair\nyes\n"))
	if err != nil {
		t.Fatalf("execute repair: %v", err)
	}
	if response.Status != operation.StatusSucceeded || git.stageCalls != 1 || git.amendCalls != 1 || git.pushCalls != 1 {
		t.Fatalf("unexpected repair calls/status: status=%s git=%#v", response.Status, git)
	}
	if git.remoteCalls != 0 || git.updateCalls != 0 {
		t.Fatal("existing matching origin was configured again")
	}
}

func TestRepoRepairRewritesOnlyUnpushedInitialCommitInTemporaryRepository(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", bare)
	runGit(t, root, "init", "--initial-branch", "main")
	runGit(t, root, "config", "user.name", "Devtize Test")
	runGit(t, root, "config", "user.email", "devtize-test@example.invalid")
	writeFile(t, root, "README.md", "before\n")
	writeFile(t, root, ".cache/go-build/item", "generated\n")
	writeFile(t, root, "dvz", "binary\n")
	runGit(t, root, "add", "--", "README.md", ".cache/go-build/item", "dvz")
	runGit(t, root, "commit", "--message", "chore: self-host Devtize with Devtize")
	runGit(t, root, "remote", "add", "origin", bare)

	writeFile(t, root, ".gitignore", "/.cache/\n/dvz\n")
	writeFile(t, root, "README.md", "after\n")
	if err := os.RemoveAll(filepath.Join(root, ".cache")); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.History.Enabled = false
	service := RepoService{
		WorkingDir: root, Config: cfg, Runner: devprocess.NewRunner(), Git: gitadapter.New(devprocess.NewRunner()),
		GitHub: &fakeGH{
			auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"},
			repo: ghadapter.RepoResult{Exists: true, NameWithOwner: "OWNER/repo", Visibility: "private", SSHURL: bare},
		},
		Now: func() time.Time { return time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC) },
	}
	response, err := service.Execute(context.Background(), RepoOptions{
		Name: "repo", Owner: "OWNER", Message: "chore: self-host Devtize with Devtize", RepairInitial: true, Yes: true,
	}, strings.NewReader("repair\nyes\n"))
	if err != nil {
		t.Fatalf("repair temporary repository: %v", err)
	}
	if response.Status != operation.StatusSucceeded {
		t.Fatalf("status = %s", response.Status)
	}
	if got := strings.TrimSpace(runGit(t, root, "rev-list", "--count", "HEAD")); got != "1" {
		t.Fatalf("commit count = %s, want 1", got)
	}
	if got := strings.TrimSpace(runGit(t, root, "ls-files", ".cache", "dvz")); got != "" {
		t.Fatalf("generated artifacts remain tracked: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "dvz")); err != nil {
		t.Fatalf("working binary was deleted: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")); got != "origin/main" {
		t.Fatalf("upstream = %q", got)
	}
	if got := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main")); got == "" {
		t.Fatal("local bare remote did not receive main")
	}
}

func TestInitialRedactionRequiresTrackedIgnoredSynchronizedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/MASTER_IDE_PROMPT.md\n")
	writeFile(t, dir, "MASTER_IDE_PROMPT.md", "local only")
	state := redactableGitState()
	service := testRepoService(dir, &fakeGit{state: state}, &fakeGH{})
	response, err := service.PlanInitialRedaction(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"})
	if err != nil {
		t.Fatalf("plan redaction: %v", err)
	}
	if !planHasCapability(response.Plan, "git.branch.force_push_with_lease") || !reflect.DeepEqual(response.Selection.UntrackPaths, []string{"MASTER_IDE_PROMPT.md"}) {
		t.Fatalf("incomplete redaction plan: %#v", response)
	}

	state.UpstreamCommit = "different"
	_, err = testRepoService(dir, &fakeGit{state: state}, &fakeGH{}).PlanInitialRedaction(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"})
	if err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("stale upstream was accepted: %v", err)
	}
}

func TestInitialRedactionConfirmationsCannotBeBypassed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/MASTER_IDE_PROMPT.md\n")
	writeFile(t, dir, "MASTER_IDE_PROMPT.md", "local only")
	git := &fakeGit{state: redactableGitState()}
	service := testRepoService(dir, git, &fakeGH{})
	response, err := service.PlanInitialRedaction(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExecuteInitialRedactionPlanned(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"}, strings.NewReader("yes\n"), response)
	if err == nil || git.untrackCalls+git.amendKeepCalls+git.forceCalls != 0 {
		t.Fatalf("redact confirmation was bypassed: err=%v git=%#v", err, git)
	}
}

func TestInitialRedactionRecordsPartialStateWhenRemoteDeclined(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/MASTER_IDE_PROMPT.md\n")
	writeFile(t, dir, "MASTER_IDE_PROMPT.md", "local only")
	git := &fakeGit{state: redactableGitState()}
	service := testRepoService(dir, git, &fakeGH{})
	response, err := service.PlanInitialRedaction(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"})
	if err != nil {
		t.Fatal(err)
	}
	response, err = service.ExecuteInitialRedactionPlanned(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"}, strings.NewReader("redact\nno\n"), response)
	if err == nil || response.Result.Status != operation.StatusPartiallyCompleted || git.forceCalls != 0 {
		t.Fatalf("remote decline state = %#v, err=%v", response.Result, err)
	}
}

func TestInitialRedactionRefusesConcurrentRemoteUpdateBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/MASTER_IDE_PROMPT.md\n")
	writeFile(t, dir, "MASTER_IDE_PROMPT.md", "local only")
	git := &fakeGit{state: redactableGitState()}
	service := testRepoService(dir, git, &fakeGH{})
	response, err := service.PlanInitialRedaction(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"})
	if err != nil {
		t.Fatal(err)
	}
	git.liveRemote = "concurrent456"
	_, err = service.ExecuteInitialRedactionPlanned(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"}, strings.NewReader("redact\n"), response)
	if err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("concurrent remote update was not refused: %v", err)
	}
	if git.untrackCalls+git.stageCalls+git.amendKeepCalls+git.forceCalls != 0 {
		t.Fatalf("mutation occurred after remote changed: %#v", git)
	}
}

func TestInitialRedactionRefusesFileChangedAfterPlan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "/MASTER_IDE_PROMPT.md\n")
	writeFile(t, dir, "MASTER_IDE_PROMPT.md", "local only")
	git := &fakeGit{state: redactableGitState()}
	service := testRepoService(dir, git, &fakeGH{})
	response, err := service.PlanInitialRedaction(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".gitignore", "/MASTER_IDE_PROMPT.md\n/new-entry\n")
	_, err = service.ExecuteInitialRedactionPlanned(context.Background(), RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"}, strings.NewReader("redact\n"), response)
	if err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("changed remediation file was accepted: %v", err)
	}
	if git.untrackCalls+git.stageCalls+git.amendKeepCalls+git.forceCalls != 0 {
		t.Fatalf("mutation occurred after file changed: %#v", git)
	}
}

func TestInitialRedactionPreservesLocalFileAndReplacesRemoteCommitInTemporaryRepository(t *testing.T) {
	root, bare := setupPublishedInitialRepository(t)
	writeFile(t, root, ".gitignore", "/MASTER_IDE_PROMPT.md\n")
	writeFile(t, root, "README.md", "updated by remediation\n")

	cfg := config.Defaults()
	cfg.History.Enabled = false
	service := RepoService{WorkingDir: root, Config: cfg, Runner: devprocess.NewRunner(), Git: gitadapter.New(devprocess.NewRunner())}
	options := RedactInitialOptions{Path: "MASTER_IDE_PROMPT.md"}
	response, err := service.PlanInitialRedaction(context.Background(), options)
	if err != nil {
		t.Fatalf("plan initial redaction: %v", err)
	}
	response, err = service.ExecuteInitialRedactionPlanned(context.Background(), options, strings.NewReader("redact\nforce-update\n"), response)
	if err != nil {
		t.Fatalf("execute initial redaction: %v", err)
	}
	if response.Status != operation.StatusSucceeded {
		t.Fatalf("status = %s", response.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "MASTER_IDE_PROMPT.md")); err != nil {
		t.Fatalf("local file was not preserved: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, root, "ls-files", "MASTER_IDE_PROMPT.md")); got != "" {
		t.Fatalf("redacted file remains tracked: %q", got)
	}
	if got := strings.TrimSpace(runGit(t, root, "rev-list", "--count", "HEAD")); got != "1" {
		t.Fatalf("commit count = %s", got)
	}
	local := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	remote := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main"))
	if local != remote {
		t.Fatalf("local %s != remote %s", local, remote)
	}
	if got := strings.TrimSpace(runGit(t, root, "log", "-1", "--format=%s")); got != "chore: self-host Devtize with Devtize" {
		t.Fatalf("commit message changed: %q", got)
	}
}

func TestRepoExecuteInitializesDefaultAdaptersBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")
	runner := scriptedRunner{t: t}
	service := RepoService{
		WorkingDir: dir, Config: config.Defaults(), Runner: &runner,
		Now: func() time.Time { return time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC) },
	}
	_, err := service.Execute(context.Background(), RepoOptions{Name: "repo", Owner: "OWNER", Message: "Initial commit"}, strings.NewReader("yes\nyes\n"))
	if err == nil {
		t.Fatal("expected fake push to fail, not a panic or success")
	}
	if runner.count("git", "init") == 0 {
		t.Fatalf("default git adapter was not used; calls: %#v", runner.calls)
	}
}

func testRepoService(dir string, git *fakeGit, gh *fakeGH) RepoService {
	return RepoService{
		WorkingDir: dir, Config: config.Defaults(), Git: git, GitHub: gh,
		Now: func() time.Time { return time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC) },
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repairableGitState() gitadapter.InspectRepoResult {
	return gitadapter.InspectRepoResult{
		IsRepository: true, HasCommits: true, CommitCount: 1, HeadBranch: "main", HeadCommit: "abc123",
		WorkingTreeStatus: "dirty", TrackedChanges: []string{"README.md"}, RemoteURLs: []string{"git@github.com:OWNER/repo.git"},
	}
}

func redactableGitState() gitadapter.InspectRepoResult {
	return gitadapter.InspectRepoResult{
		IsRepository: true, HasCommits: true, CommitCount: 1, HeadBranch: "main", HeadCommit: "published123",
		WorkingTreeStatus: "dirty", TrackedPaths: []string{".gitignore", "MASTER_IDE_PROMPT.md"},
		TrackedChanges: []string{".gitignore"}, RemoteURLs: []string{"https://github.com/OWNER/repo.git"},
		Upstream: "origin/main", UpstreamCommit: "published123",
	}
}

func setupPublishedInitialRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", bare)
	runGit(t, root, "init", "--initial-branch", "main")
	runGit(t, root, "config", "user.name", "Devtize Test")
	runGit(t, root, "config", "user.email", "devtize-test@example.invalid")
	writeFile(t, root, ".gitignore", "/dvz\n")
	writeFile(t, root, "README.md", "initial\n")
	writeFile(t, root, "MASTER_IDE_PROMPT.md", "keep locally\n")
	runGit(t, root, "add", "--", ".gitignore", "README.md", "MASTER_IDE_PROMPT.md")
	runGit(t, root, "commit", "--message", "chore: self-host Devtize with Devtize")
	runGit(t, root, "remote", "add", "origin", bare)
	runGit(t, root, "push", "--set-upstream", "origin", "main")
	return root, bare
}

func repairableGH() *fakeGH {
	return &fakeGH{
		auth: ghadapter.AuthResult{Status: "authenticated", Owner: "OWNER"},
		repo: ghadapter.RepoResult{Exists: true, NameWithOwner: "OWNER/repo", Visibility: "private", SSHURL: "git@github.com:OWNER/repo.git"},
	}
}

func planHasCapability(plan operation.Plan, capability string) bool {
	for _, op := range plan.Operations {
		if op.CapabilityID == capability {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	if dir != "" {
		command.Dir = dir
	}
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			t.Skip("git is unavailable")
		}
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

type scriptedRunner struct {
	t     *testing.T
	calls []devprocess.CommandSpec
}

func (r *scriptedRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	r.calls = append(r.calls, spec)
	key := append([]string{spec.Executable}, spec.Args...)
	joined := strings.Join(key, " ")
	switch {
	case joined == "git rev-parse --is-inside-work-tree":
		return devprocess.CommandResult{}, &devprocess.RunError{Kind: devprocess.ErrorExit, ExitCode: 128, Message: "not a repository"}
	case joined == "gh auth status":
		return devprocess.CommandResult{Stdout: "Logged in to github.com as OWNER\n"}, nil
	case joined == "gh repo view OWNER/repo --json nameWithOwner,visibility,url,sshUrl,defaultBranchRef":
		return devprocess.CommandResult{}, &devprocess.RunError{Kind: devprocess.ErrorExit, ExitCode: 1, Message: "not found"}
	case joined == "git init --initial-branch main":
		return devprocess.CommandResult{}, nil
	case joined == "git add -- README.md":
		return devprocess.CommandResult{}, nil
	case joined == "git commit --message Initial commit":
		return devprocess.CommandResult{}, nil
	case joined == "gh repo create OWNER/repo --private":
		return devprocess.CommandResult{}, nil
	case joined == "git remote add origin git@github.com:OWNER/repo.git":
		return devprocess.CommandResult{}, nil
	case joined == "git push --set-upstream origin main":
		return devprocess.CommandResult{}, &devprocess.RunError{Kind: devprocess.ErrorExit, ExitCode: 1, Message: "fake push failed"}
	default:
		r.t.Fatalf("unexpected command: %s", joined)
		return devprocess.CommandResult{}, nil
	}
}

func (r *scriptedRunner) count(executable, firstArg string) int {
	var count int
	for _, call := range r.calls {
		if call.Executable == executable && len(call.Args) > 0 && call.Args[0] == firstArg {
			count++
		}
	}
	return count
}
