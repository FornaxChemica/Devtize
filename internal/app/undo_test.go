package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
)

type fakeUndoHistory struct {
	record history.Record
	found  bool
	err    error
	seen   history.FindOptions
}

func (f *fakeUndoHistory) Find(options history.FindOptions) (history.Record, bool, error) {
	f.seen = options
	return f.record, f.found, f.err
}

type fakeUndoGit struct {
	state       gitadapter.InspectRepoResult
	changes     gitadapter.ChangeSet
	parents     []string
	upstream    gitadapter.UpstreamTarget
	remote      gitadapter.RemoteBranchState
	repoErr     error
	changesErr  error
	parentsErr  error
	upstreamErr error
	remoteErr   error
	calls       []string
}

func (f *fakeUndoGit) InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error) {
	f.calls = append(f.calls, "inspect_repo")
	return f.state, f.repoErr
}

func (f *fakeUndoGit) InspectChanges(context.Context, string) (gitadapter.ChangeSet, error) {
	f.calls = append(f.calls, "inspect_changes")
	return f.changes, f.changesErr
}

func (f *fakeUndoGit) InspectCommitParents(context.Context, string, string) ([]string, error) {
	f.calls = append(f.calls, "inspect_parents")
	return append([]string(nil), f.parents...), f.parentsErr
}

func (f *fakeUndoGit) InspectUpstreamTarget(context.Context, string, string) (gitadapter.UpstreamTarget, error) {
	f.calls = append(f.calls, "inspect_upstream")
	return f.upstream, f.upstreamErr
}

func (f *fakeUndoGit) InspectRemoteBranchState(context.Context, gitadapter.InspectRemoteBranchInput) (gitadapter.RemoteBranchState, error) {
	f.calls = append(f.calls, "inspect_remote")
	return f.remote, f.remoteErr
}

func undoFixture(root string) (history.Record, *fakeUndoGit) {
	before := strings.Repeat("a", 40)
	after := strings.Repeat("b", 40)
	record := history.Record{
		SchemaVersion: 1, ExecutionID: "exec_fixture", PlanID: "plan_commit_fixture", PlanDigest: "sha256:fixture",
		Invocation: map[string]any{"workflow": "commit"}, Project: map[string]string{"root": root}, Status: operation.StatusSucceeded,
		Steps: []operation.StepResult{{CapabilityID: "git.commit.create", Status: operation.StatusSucceeded}},
		ObservedChanges: []history.ObservedChange{{
			Kind: "git.commit.created", ProviderID: "git", CapabilityID: "git.commit.create",
			Branch: "main", BeforeCommit: before, AfterCommit: after,
		}},
	}
	git := &fakeUndoGit{
		state: gitadapter.InspectRepoResult{
			IsRepository: true, HasCommits: true, HeadBranch: "main", HeadCommit: after, WorkingTreeStatus: "clean",
		},
		parents: []string{before},
	}
	return record, git
}

func TestUndoBuildsPlannerOnlyCompensationFromLiveEvidence(t *testing.T) {
	root := t.TempDir()
	record, git := undoFixture(root)
	historyReader := &fakeUndoHistory{record: record, found: true}
	service := UndoService{
		WorkingDir: root, History: historyReader, Git: git,
		Now: func() time.Time { return time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC) },
	}
	response, err := service.Run(context.Background(), UndoOptions{ExecutionID: "exec_fixture", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if response.Eligibility != UndoAvailable || response.RollbackKind != "compensating" || response.MutationPerformed || response.Plan == nil {
		t.Fatalf("response=%#v", response)
	}
	if !response.Plan.DryRun || len(response.Plan.Operations) != 1 || response.Plan.Operations[0].CapabilityID != "git.commit.uncommit_preserve_changes" || response.Plan.Operations[0].Inputs["execution_supported"] != false {
		t.Fatalf("plan=%#v", response.Plan)
	}
	if response.Plan.Digest == "" || historyReader.seen.ProjectRoot != root || strings.Join(git.calls, ",") != "inspect_repo,inspect_changes,inspect_parents,inspect_upstream" {
		t.Fatalf("history=%#v calls=%#v plan=%#v", historyReader.seen, git.calls, response.Plan)
	}
}

func TestUndoAllowsUnpublishedConfiguredUpstreamStates(t *testing.T) {
	root := t.TempDir()
	for _, remote := range []gitadapter.RemoteBranchState{
		{Exists: false},
		{Exists: true, Commit: strings.Repeat("a", 40)},
	} {
		record, git := undoFixture(root)
		git.upstream = gitadapter.UpstreamTarget{Configured: true, Remote: "origin", Branch: "main"}
		git.remote = remote
		response, err := (UndoService{WorkingDir: root, History: &fakeUndoHistory{record: record, found: true}, Git: git}).Run(
			context.Background(), UndoOptions{ExecutionID: "exec_fixture", DryRun: true},
		)
		if err != nil || response.Eligibility != UndoAvailable || response.Plan == nil || response.Git.LiveRemote == nil {
			t.Fatalf("remote=%#v response=%#v err=%v", remote, response, err)
		}
	}
}

func TestUndoUnavailableReasonsDoNotBuildOperations(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name   string
		reason string
		mutate func(*history.Record, *fakeUndoGit)
	}{
		{"source failed", ReasonSourceNotSucceeded, func(record *history.Record, _ *fakeUndoGit) { record.Status = operation.StatusFailed }},
		{"unsupported workflow", ReasonSourceWorkflowUnsupported, func(record *history.Record, _ *fakeUndoGit) { record.Invocation["workflow"] = "ship" }},
		{"step failed", ReasonSourceNotSucceeded, func(record *history.Record, _ *fakeUndoGit) { record.Steps[0].Status = operation.StatusFailed }},
		{"legacy evidence", ReasonHistoryEvidenceMissing, func(record *history.Record, _ *fakeUndoGit) { record.ObservedChanges = nil }},
		{"branch changed", ReasonBranchChanged, func(_ *history.Record, git *fakeUndoGit) { git.state.HeadBranch = "other" }},
		{"head changed", ReasonSourceCommitNotHead, func(_ *history.Record, git *fakeUndoGit) { git.state.HeadCommit = strings.Repeat("c", 40) }},
		{"dirty tree", ReasonWorktreeNotClean, func(_ *history.Record, git *fakeUndoGit) { git.changes.Untracked = []string{"new.txt"} }},
		{"merge commit", ReasonSourceParentMismatch, func(_ *history.Record, git *fakeUndoGit) { git.parents = append(git.parents, strings.Repeat("c", 40)) }},
		{"published", ReasonCommitPublished, func(record *history.Record, git *fakeUndoGit) {
			git.upstream = gitadapter.UpstreamTarget{Configured: true, Remote: "origin", Branch: "main"}
			git.remote = gitadapter.RemoteBranchState{Exists: true, Commit: record.ObservedChanges[0].AfterCommit}
		}},
		{"remote moved", ReasonRemoteStateAmbiguous, func(_ *history.Record, git *fakeUndoGit) {
			git.upstream = gitadapter.UpstreamTarget{Configured: true, Remote: "origin", Branch: "main"}
			git.remote = gitadapter.RemoteBranchState{Exists: true, Commit: strings.Repeat("c", 40)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record, git := undoFixture(root)
			test.mutate(&record, git)
			response, err := (UndoService{WorkingDir: root, History: &fakeUndoHistory{record: record, found: true}, Git: git}).Run(
				context.Background(), UndoOptions{ExecutionID: "exec_fixture", DryRun: true},
			)
			if err != nil {
				t.Fatal(err)
			}
			if response.Eligibility != UndoUnavailable || response.Plan != nil || len(response.Reasons) != 1 || response.Reasons[0].Code != test.reason {
				t.Fatalf("response=%#v", response)
			}
		})
	}
}

func TestUndoRejectsMaliciousHistoryBeforeGitInspection(t *testing.T) {
	root := t.TempDir()
	record, git := undoFixture(root)
	record.ObservedChanges[0].AfterCommit = "HEAD; reset --hard"
	response, err := (UndoService{WorkingDir: root, History: &fakeUndoHistory{record: record, found: true}, Git: git}).Run(
		context.Background(), UndoOptions{ExecutionID: "exec_fixture", DryRun: true},
	)
	if err != nil || response.Reasons[0].Code != ReasonHistoryEvidenceMissing || len(git.calls) != 0 {
		t.Fatalf("response=%#v calls=%#v err=%v", response, git.calls, err)
	}
}

func TestUndoMapsLookupAndRemoteFailures(t *testing.T) {
	root := t.TempDir()
	_, err := (UndoService{WorkingDir: root, History: &fakeUndoHistory{}}).Run(context.Background(), UndoOptions{ExecutionID: "exec_missing", DryRun: true})
	var operational *Error
	if !errors.As(err, &operational) || operational.Code != CodeHistoryEntryNotFound {
		t.Fatalf("missing err=%#v", err)
	}

	record, git := undoFixture(root)
	git.upstream = gitadapter.UpstreamTarget{Configured: true, Remote: "origin", Branch: "main"}
	git.remoteErr = errors.New("network unavailable")
	_, err = (UndoService{WorkingDir: root, History: &fakeUndoHistory{record: record, found: true}, Git: git}).Run(context.Background(), UndoOptions{ExecutionID: "exec_fixture", DryRun: true})
	if !errors.As(err, &operational) || operational.Code != CodeProcessFailed {
		t.Fatalf("remote err=%#v", err)
	}
}

func TestUndoRequiresDryRunAndValidExecutionID(t *testing.T) {
	service := UndoService{WorkingDir: t.TempDir(), History: &fakeUndoHistory{}}
	for _, options := range []UndoOptions{{ExecutionID: "exec_valid"}, {ExecutionID: "../../history", DryRun: true}} {
		_, err := service.Run(context.Background(), options)
		var operational *Error
		if !errors.As(err, &operational) || operational.Code != CodeInvalidUsage {
			t.Fatalf("options=%#v err=%#v", options, err)
		}
	}
}
