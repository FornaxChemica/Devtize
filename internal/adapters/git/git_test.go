package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeRunner struct {
	specs []devprocess.CommandSpec
	out   devprocess.CommandResult
	err   error
	errs  []error
}

func (r *fakeRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	r.specs = append(r.specs, spec)
	if len(r.errs) > 0 {
		err := r.errs[0]
		r.errs = r.errs[1:]
		return r.out, err
	}
	if r.out.Executable == "" {
		r.out.Executable = spec.Executable
	}
	return r.out, r.err
}

func TestStageUsesLiteralPathArgumentsAfterSeparator(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	err := adapter.Stage(context.Background(), StageInput{ProjectRoot: "/tmp/project", Paths: []string{"a b.txt", "--not-a-flag", "semi;colon"}})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	want := []string{"add", "--", "a b.txt", "--not-a-flag", "semi;colon"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
	if runner.specs[0].Dir != "/tmp/project" {
		t.Fatalf("dir = %q", runner.specs[0].Dir)
	}
	if runner.specs[0].CaptureLimit != 4<<20 {
		t.Fatalf("capture limit = %d, want enough for bounded repository inventories", runner.specs[0].CaptureLimit)
	}
}

func TestPushNeverUsesForce(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	if err := adapter.PushBranch(context.Background(), PushInput{ProjectRoot: "/tmp/project", RemoteName: "origin", Branch: "main"}); err != nil {
		t.Fatalf("push: %v", err)
	}
	for _, arg := range runner.specs[0].Args {
		if arg == "--force" || arg == "--force-with-lease" {
			t.Fatalf("push used force arg: %#v", runner.specs[0].Args)
		}
	}
	want := []string{"push", "--set-upstream", "origin", "main"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestPushExistingBranchUsesFullRefspecAndNeverForce(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	if err := adapter.PushExistingBranch(context.Background(), PushExistingBranchInput{ProjectRoot: "/tmp/project", RemoteName: "origin", Branch: "feature/safe"}); err != nil {
		t.Fatalf("push existing branch: %v", err)
	}
	want := []string{"push", "origin", "refs/heads/feature/safe:refs/heads/feature/safe"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
	for _, arg := range runner.specs[0].Args {
		if strings.Contains(arg, "force") {
			t.Fatalf("push used force: %#v", runner.specs[0].Args)
		}
	}
}

func TestPushExistingBranchRejectsOptionLikeNamesBeforeRunner(t *testing.T) {
	for _, branch := range []string{"--force", "main\nmalicious"} {
		runner := &fakeRunner{}
		adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
		err := adapter.PushExistingBranch(context.Background(), PushExistingBranchInput{ProjectRoot: "/tmp/project", RemoteName: "origin", Branch: branch})
		if err == nil || len(runner.specs) != 0 {
			t.Fatalf("unsafe branch %q reached runner: err=%v specs=%#v", branch, err, runner.specs)
		}
	}
}

func TestUntrackPreservesWorkingFilesAndUsesLiteralPaths(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	paths := []string{".cache/go-build/a file", "dvz"}
	if err := adapter.Untrack(context.Background(), UntrackInput{ProjectRoot: "/tmp/project", Paths: paths}); err != nil {
		t.Fatalf("untrack: %v", err)
	}
	want := []string{"rm", "--cached", "--ignore-unmatch", "--", ".cache/go-build/a file", "dvz"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestAmendInitialCommitUsesExactMessageArgument(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	if err := adapter.AmendCommit(context.Background(), CommitInput{ProjectRoot: "/tmp/project", Message: "chore: self-host Devtize with Devtize"}); err != nil {
		t.Fatalf("amend: %v", err)
	}
	want := []string{"commit", "--amend", "--message", "chore: self-host Devtize with Devtize"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestUpdateRemoteUsesExactArgumentArray(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	if err := adapter.UpdateRemote(context.Background(), RemoteInput{ProjectRoot: "/tmp/project", RemoteName: "origin", URL: "https://github.com/OWNER/repo.git"}); err != nil {
		t.Fatalf("update remote: %v", err)
	}
	want := []string{"remote", "set-url", "origin", "https://github.com/OWNER/repo.git"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestAmendCommitPreservingMessageUsesNoEdit(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	if err := adapter.AmendCommitPreservingMessage(context.Background(), "/tmp/project"); err != nil {
		t.Fatalf("amend preserving message: %v", err)
	}
	want := []string{"commit", "--amend", "--no-edit"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestForcePushUsesExactExpectedLeaseAndNeverPlainForce(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	input := ForcePushWithLeaseInput{ProjectRoot: "/tmp/project", RemoteName: "origin", Branch: "main", ExpectedCommit: "abc123"}
	if err := adapter.ForcePushWithLease(context.Background(), input); err != nil {
		t.Fatalf("force push with lease: %v", err)
	}
	want := []string{"push", "--force-with-lease=refs/heads/main:abc123", "origin", "main"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
	for _, arg := range runner.specs[0].Args {
		if arg == "--force" {
			t.Fatalf("plain force was used: %#v", runner.specs[0].Args)
		}
	}
}

func TestForcePushRequiresExpectedCommit(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	err := adapter.ForcePushWithLease(context.Background(), ForcePushWithLeaseInput{ProjectRoot: "/tmp/project", RemoteName: "origin", Branch: "main"})
	if err == nil || len(runner.specs) != 0 {
		t.Fatalf("missing expected commit reached runner: err=%v specs=%#v", err, runner.specs)
	}
}

func TestInspectRemoteBranchUsesExactRef(t *testing.T) {
	commitID := strings.Repeat("a", 40)
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: commitID + "\trefs/heads/main\n"}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	commit, err := adapter.InspectRemoteBranch(context.Background(), InspectRemoteBranchInput{ProjectRoot: "/tmp/project", RemoteName: "origin", Branch: "main"})
	if err != nil || commit != commitID {
		t.Fatalf("inspect remote branch: commit=%q err=%v", commit, err)
	}
	want := []string{"ls-remote", "--heads", "origin", "refs/heads/main"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestInspectRemoteBranchStateTreatsMissingBranchAsState(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: ""}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	state, err := adapter.InspectRemoteBranchState(context.Background(), InspectRemoteBranchInput{ProjectRoot: "/tmp/project", RemoteName: "origin", Branch: "main"})
	if err != nil || state.Exists || state.Commit != "" {
		t.Fatalf("state = %#v, err=%v", state, err)
	}
}

func TestInspectTrackingRelationUsesFullCommitRange(t *testing.T) {
	upstream := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: "2\t3\n"}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	relation, err := adapter.InspectTrackingRelation(context.Background(), InspectTrackingRelationInput{ProjectRoot: "/tmp/project", UpstreamCommit: upstream, HeadCommit: head})
	if err != nil || relation.Ahead != 3 || relation.Behind != 2 {
		t.Fatalf("relation = %#v, err=%v", relation, err)
	}
	want := []string{"rev-list", "--left-right", "--count", upstream + "..." + head}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestInspectOutgoingCommitsUsesCommitIDsAndParsesNULFields(t *testing.T) {
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: strings.Repeat("c", 40) + "\x00feat: one\x00\n" + head + "\x00fix: two\x00\n"}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	commits, err := adapter.InspectOutgoingCommits(context.Background(), InspectOutgoingCommitsInput{ProjectRoot: "/tmp/project", BaseCommit: base, HeadCommit: head})
	if err != nil {
		t.Fatalf("inspect outgoing: %v", err)
	}
	wantArgs := []string{"log", "--reverse", "--format=%H%x00%s%x00", base + ".." + head, "--"}
	if !reflect.DeepEqual(runner.specs[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, wantArgs)
	}
	if len(commits) != 2 || commits[1].SHA != head || commits[1].Subject != "fix: two" {
		t.Fatalf("commits = %#v", commits)
	}
}

func TestIsAncestorMapsOnlyExitOneToFalse(t *testing.T) {
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	runner := &fakeRunner{err: &devprocess.RunError{Kind: devprocess.ErrorExit, ExitCode: 1}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	ok, err := adapter.IsAncestor(context.Background(), "/tmp/project", base, head)
	if err != nil || ok {
		t.Fatalf("is ancestor = %t, err=%v", ok, err)
	}
	want := []string{"merge-base", "--is-ancestor", base, head}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestInspectChangesUsesNULTerminatedReadCommands(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: "a b.txt\x00semi;colon\x00"}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	changes, err := adapter.InspectChanges(context.Background(), "/tmp/project")
	if err != nil {
		t.Fatalf("inspect changes: %v", err)
	}
	wantArgs := [][]string{
		{"diff", "--cached", "--name-only", "-z", "--"},
		{"diff", "--name-only", "-z", "--"},
		{"ls-files", "--others", "--exclude-standard", "-z", "--"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "-z", "--"},
	}
	for i, want := range wantArgs {
		if !reflect.DeepEqual(runner.specs[i].Args, want) {
			t.Fatalf("call %d args = %#v, want %#v", i, runner.specs[i].Args, want)
		}
	}
	if !reflect.DeepEqual(changes.Staged, []string{"a b.txt", "semi;colon"}) || !reflect.DeepEqual(changes.Ignored, changes.Staged) {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestInspectHeadMessageUsesExactFormat(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: "feat(cli): add commit workflow\n"}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	message, err := adapter.InspectHeadMessage(context.Background(), "/tmp/project")
	if err != nil || message != "feat(cli): add commit workflow" {
		t.Fatalf("message=%q err=%v", message, err)
	}
	want := []string{"log", "-1", "--format=%B"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestInitUsesInitialBranchArgumentArray(t *testing.T) {
	runner := &fakeRunner{}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	if err := adapter.InitRepo(context.Background(), InitRepoInput{ProjectRoot: "/tmp/project", InitialBranch: "main"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	want := []string{"init", "--initial-branch", "main"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestInitFallsBackWhenInitialBranchFlagFails(t *testing.T) {
	runner := &fakeRunner{errs: []error{errors.New("unsupported initial branch"), nil, nil}}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	if err := adapter.InitRepo(context.Background(), InitRepoInput{ProjectRoot: "/tmp/project", InitialBranch: "main"}); err != nil {
		t.Fatalf("init fallback: %v", err)
	}
	want := [][]string{
		{"init", "--initial-branch", "main"},
		{"init"},
		{"symbolic-ref", "HEAD", "refs/heads/main"},
	}
	for i := range want {
		if !reflect.DeepEqual(runner.specs[i].Args, want[i]) {
			t.Fatalf("call %d args = %#v, want %#v", i, runner.specs[i].Args, want[i])
		}
	}
}

func TestAdapterInitAndStageInTemporaryRepository(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := devprocess.NewRunner()
	adapter := New(runner)
	if err := adapter.InitRepo(context.Background(), InitRepoInput{ProjectRoot: dir, InitialBranch: "main"}); err != nil {
		t.Skipf("git init unavailable in test environment: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("temp repo was not initialized: %v", err)
	}
	if err := adapter.Stage(context.Background(), StageInput{ProjectRoot: dir, Paths: []string{"README.md"}}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	state, err := adapter.InspectRepo(context.Background(), InspectRepoInput{ProjectRoot: dir, RemoteName: "origin"})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !state.IsRepository {
		t.Fatal("expected temp directory to be a repository")
	}
}

func TestInspectRepoPreservesMissingExecutableFailure(t *testing.T) {
	runErr := &devprocess.RunError{Kind: devprocess.ErrorMissing, Message: "missing"}
	runner := &fakeRunner{err: runErr}
	adapter := Adapter{Runner: runner, Executable: "git", Timeout: time.Second}
	_, err := adapter.InspectRepo(context.Background(), InspectRepoInput{ProjectRoot: "/tmp/project"})
	if !errors.Is(err, runErr) {
		t.Fatalf("err=%v", err)
	}
}
