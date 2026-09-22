package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
)

type fakeStatusGit struct {
	state         gitadapter.InspectRepoResult
	changes       gitadapter.ChangeSet
	relation      gitadapter.TrackingRelation
	live          gitadapter.RemoteBranchState
	liveErr       error
	liveCalls     int
	relationCalls int
}

func (f *fakeStatusGit) InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error) {
	return f.state, nil
}

func (f *fakeStatusGit) InspectChanges(context.Context, string) (gitadapter.ChangeSet, error) {
	return f.changes, nil
}

func (f *fakeStatusGit) InspectTrackingRelation(context.Context, gitadapter.InspectTrackingRelationInput) (gitadapter.TrackingRelation, error) {
	f.relationCalls++
	return f.relation, nil
}

func (f *fakeStatusGit) InspectRemoteBranchState(context.Context, gitadapter.InspectRemoteBranchInput) (gitadapter.RemoteBranchState, error) {
	f.liveCalls++
	return f.live, f.liveErr
}

func TestStatusDefaultIsLocalOnlyAndSortsPaths(t *testing.T) {
	head := strings.Repeat("b", 40)
	upstream := strings.Repeat("a", 40)
	git := &fakeStatusGit{
		state: gitadapter.InspectRepoResult{
			IsRepository: true, HasCommits: true, HeadBranch: "main", HeadCommit: head,
			WorkingTreeStatus: "dirty", Upstream: "origin/main", UpstreamCommit: upstream,
			RemoteURLs: []string{"https://example.invalid/repo.git"},
		},
		changes:  gitadapter.ChangeSet{Staged: []string{"z.go", "a.go"}, Untracked: []string{"new.txt"}, Ignored: []string{"dvz"}},
		relation: gitadapter.TrackingRelation{Ahead: 1},
	}
	response, err := (StatusService{WorkingDir: t.TempDir(), Git: git}).Run(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if git.liveCalls != 0 {
		t.Fatalf("default status made %d live calls", git.liveCalls)
	}
	if response.Upstream.Relation != "ahead" || !reflect.DeepEqual(response.Changes.Staged, []string{"a.go", "z.go"}) {
		t.Fatalf("response = %#v", response)
	}
	if len(response.RecommendedActions) != 3 || !strings.Contains(response.RecommendedActions[2], "dvz ship") {
		t.Fatalf("actions = %#v", response.RecommendedActions)
	}
}

func TestStatusClassifiesLocalRelations(t *testing.T) {
	for name, relation := range map[string]gitadapter.TrackingRelation{
		"synchronized": {},
		"ahead":        {Ahead: 2},
		"behind":       {Behind: 3},
		"diverged":     {Ahead: 1, Behind: 1},
	} {
		t.Run(name, func(t *testing.T) {
			git := &fakeStatusGit{
				state:    gitadapter.InspectRepoResult{IsRepository: true, HasCommits: true, HeadBranch: "main", HeadCommit: strings.Repeat("b", 40), Upstream: "origin/main", UpstreamCommit: strings.Repeat("a", 40)},
				relation: relation,
			}
			response, err := (StatusService{WorkingDir: t.TempDir(), Git: git}).Run(context.Background(), StatusOptions{})
			if err != nil || response.Upstream.Relation != name {
				t.Fatalf("relation=%q err=%v", response.Upstream.Relation, err)
			}
		})
	}
}

func TestStatusClassifiesLiveRemoteWithoutFetch(t *testing.T) {
	upstream := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	for name, test := range map[string]struct {
		live     gitadapter.RemoteBranchState
		relation gitadapter.TrackingRelation
		want     string
	}{
		"unpublished":    {live: gitadapter.RemoteBranchState{}, relation: gitadapter.TrackingRelation{Ahead: 1}, want: "unpublished"},
		"synchronized":   {live: gitadapter.RemoteBranchState{Exists: true, Commit: head}, want: "synchronized"},
		"local ahead":    {live: gitadapter.RemoteBranchState{Exists: true, Commit: upstream}, relation: gitadapter.TrackingRelation{Ahead: 1}, want: "local_ahead"},
		"tracking stale": {live: gitadapter.RemoteBranchState{Exists: true, Commit: strings.Repeat("c", 40)}, relation: gitadapter.TrackingRelation{Ahead: 1}, want: "tracking_stale"},
	} {
		t.Run(name, func(t *testing.T) {
			git := &fakeStatusGit{
				state:    gitadapter.InspectRepoResult{IsRepository: true, HasCommits: true, HeadBranch: "main", HeadCommit: head, Upstream: "origin/main", UpstreamCommit: upstream, RemoteURLs: []string{"remote"}},
				relation: test.relation, live: test.live,
			}
			response, err := (StatusService{WorkingDir: t.TempDir(), Git: git}).Run(context.Background(), StatusOptions{Remote: true})
			if err != nil || response.LiveRemote.Relation != test.want || git.liveCalls != 1 {
				t.Fatalf("response=%#v calls=%d err=%v", response.LiveRemote, git.liveCalls, err)
			}
		})
	}
}

func TestStatusLiveFailureIsActionable(t *testing.T) {
	git := &fakeStatusGit{
		state:   gitadapter.InspectRepoResult{IsRepository: true, HasCommits: true, HeadBranch: "main", HeadCommit: strings.Repeat("a", 40), RemoteURLs: []string{"remote"}},
		liveErr: errors.New("offline"),
	}
	_, err := (StatusService{WorkingDir: t.TempDir(), Git: git}).Run(context.Background(), StatusOptions{Remote: true})
	var operational *Error
	if !errors.As(err, &operational) || operational.Code != CodeProcessFailed || !strings.Contains(operational.Hint, "--remote") {
		t.Fatalf("err=%#v", err)
	}
}

func TestStatusReportsNonRepositoryWithoutFurtherGitCalls(t *testing.T) {
	git := &fakeStatusGit{state: gitadapter.InspectRepoResult{WorkingTreeStatus: "unknown"}}
	response, err := (StatusService{WorkingDir: t.TempDir(), Git: git}).Run(context.Background(), StatusOptions{Remote: true})
	if err != nil || response.Repository.IsRepository || git.liveCalls != 0 || git.relationCalls != 0 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestStatusReportsSpecialLocalStatesWithoutFailure(t *testing.T) {
	for name, state := range map[string]gitadapter.InspectRepoResult{
		"unborn":      {IsRepository: true, WorkingTreeStatus: "clean", HeadBranch: "main"},
		"detached":    {IsRepository: true, HasCommits: true, IsDetachedHead: true, HeadCommit: strings.Repeat("a", 40), WorkingTreeStatus: "clean"},
		"no_upstream": {IsRepository: true, HasCommits: true, HeadBranch: "main", HeadCommit: strings.Repeat("a", 40), WorkingTreeStatus: "clean"},
	} {
		t.Run(name, func(t *testing.T) {
			git := &fakeStatusGit{state: state}
			response, err := (StatusService{WorkingDir: t.TempDir(), Git: git}).Run(context.Background(), StatusOptions{})
			if err != nil || response.Upstream.Relation != name || git.relationCalls != 0 {
				t.Fatalf("response=%#v err=%v", response, err)
			}
		})
	}
}

func TestStatusRemoteOptionReportsMissingRemoteWithoutNetworkCall(t *testing.T) {
	git := &fakeStatusGit{state: gitadapter.InspectRepoResult{IsRepository: true, HasCommits: true, HeadBranch: "main", HeadCommit: strings.Repeat("a", 40)}}
	response, err := (StatusService{WorkingDir: t.TempDir(), Git: git}).Run(context.Background(), StatusOptions{Remote: true})
	if err != nil || git.liveCalls != 0 || len(response.Warnings) == 0 || !strings.Contains(response.Warnings[len(response.Warnings)-1], "not configured") {
		t.Fatalf("response=%#v calls=%d err=%v", response, git.liveCalls, err)
	}
}
