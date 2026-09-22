package app

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type StatusOptions struct {
	Remote     bool
	RemoteName string
}

type RepositoryStatus struct {
	IsRepository bool   `json:"is_repository"`
	HasCommits   bool   `json:"has_commits"`
	Branch       string `json:"branch,omitempty"`
	HeadCommit   string `json:"head_commit,omitempty"`
	Detached     bool   `json:"detached"`
	WorkingTree  string `json:"working_tree"`
}

type ChangeStatus struct {
	Staged       []string `json:"staged"`
	Unstaged     []string `json:"unstaged"`
	Untracked    []string `json:"untracked"`
	IgnoredCount int      `json:"ignored_count"`
}

type UpstreamStatus struct {
	Name     string `json:"name,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Relation string `json:"relation"`
}

type LiveRemoteStatus struct {
	Requested bool     `json:"requested"`
	Name      string   `json:"name"`
	URLs      []string `json:"urls"`
	Branch    string   `json:"branch,omitempty"`
	Exists    bool     `json:"exists"`
	Commit    string   `json:"commit,omitempty"`
	Relation  string   `json:"relation,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

type StatusResponse struct {
	SchemaVersion      int              `json:"schema_version"`
	ProjectRoot        string           `json:"project_root"`
	Repository         RepositoryStatus `json:"repository"`
	Changes            ChangeStatus     `json:"changes"`
	Upstream           UpstreamStatus   `json:"upstream"`
	LiveRemote         LiveRemoteStatus `json:"live_remote"`
	Warnings           []string         `json:"warnings"`
	RecommendedActions []string         `json:"recommended_actions"`
}

type StatusGitPort interface {
	InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error)
	InspectChanges(context.Context, string) (gitadapter.ChangeSet, error)
	InspectTrackingRelation(context.Context, gitadapter.InspectTrackingRelationInput) (gitadapter.TrackingRelation, error)
	InspectRemoteBranchState(context.Context, gitadapter.InspectRemoteBranchInput) (gitadapter.RemoteBranchState, error)
}

type StatusService struct {
	WorkingDir string
	Runner     interface {
		Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
	}
	Git StatusGitPort
}

func (s StatusService) Run(ctx context.Context, options StatusOptions) (StatusResponse, error) {
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return StatusResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	remote := firstNonEmpty(options.RemoteName, "origin")
	if !safeName.MatchString(remote) || strings.HasPrefix(remote, "-") {
		return StatusResponse{}, &Error{Code: CodeInvalidUsage, Message: "remote name is invalid"}
	}
	if s.Git == nil {
		s.Git = gitadapter.New(s.Runner)
	}
	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: root, RemoteName: remote})
	if err != nil {
		return StatusResponse{}, classifyProcess("git", "inspect repository status", err)
	}
	response := StatusResponse{
		SchemaVersion: 1,
		ProjectRoot:   root,
		Repository: RepositoryStatus{
			IsRepository: state.IsRepository, HasCommits: state.HasCommits, Branch: state.HeadBranch,
			HeadCommit: state.HeadCommit, Detached: state.IsDetachedHead, WorkingTree: state.WorkingTreeStatus,
		},
		Changes:    ChangeStatus{Staged: []string{}, Unstaged: []string{}, Untracked: []string{}},
		Upstream:   UpstreamStatus{Name: state.Upstream, Commit: state.UpstreamCommit},
		LiveRemote: LiveRemoteStatus{Requested: options.Remote, Name: remote, URLs: sortedCopy(state.RemoteURLs), Branch: state.HeadBranch},
		Warnings:   []string{}, RecommendedActions: []string{},
	}
	if !state.IsRepository {
		response.Upstream.Relation = "no_upstream"
		response.Warnings = append(response.Warnings, "Current folder is not a Git repository.")
		response.RecommendedActions = append(response.RecommendedActions, "Run dvz repo create to review repository initialization.")
		return response, nil
	}
	changes, err := s.Git.InspectChanges(ctx, root)
	if err != nil {
		return response, classifyProcess("git", "inspect working tree changes", err)
	}
	response.Changes = ChangeStatus{
		Staged: sortedCopy(changes.Staged), Unstaged: sortedCopy(changes.Unstaged),
		Untracked: sortedCopy(changes.Untracked), IgnoredCount: len(changes.Ignored),
	}

	switch {
	case !state.HasCommits:
		response.Upstream.Relation = "unborn"
		response.Warnings = append(response.Warnings, "The repository does not have an initial commit.")
	case state.IsDetachedHead:
		response.Upstream.Relation = "detached"
		response.Warnings = append(response.Warnings, "HEAD is detached; daily commit and ship workflows require an attached branch.")
	case state.Upstream == "" || state.UpstreamCommit == "":
		response.Upstream.Relation = "no_upstream"
		response.Warnings = append(response.Warnings, "The current branch has no configured upstream.")
	default:
		relation, relationErr := s.Git.InspectTrackingRelation(ctx, gitadapter.InspectTrackingRelationInput{
			ProjectRoot: root, UpstreamCommit: state.UpstreamCommit, HeadCommit: state.HeadCommit,
		})
		if relationErr != nil {
			return response, classifyProcess("git", "inspect upstream relation", relationErr)
		}
		response.Upstream.Ahead = relation.Ahead
		response.Upstream.Behind = relation.Behind
		response.Upstream.Relation = relationName(relation)
	}

	response.addWorkingTreeGuidance()
	response.addUpstreamGuidance()
	if options.Remote {
		if len(response.LiveRemote.URLs) == 0 {
			response.Warnings = append(response.Warnings, "Remote "+remote+" is not configured; live verification was not attempted.")
		} else if state.HeadBranch == "" {
			response.Warnings = append(response.Warnings, "Live verification requires an attached branch.")
		} else {
			live, liveErr := s.Git.InspectRemoteBranchState(ctx, gitadapter.InspectRemoteBranchInput{ProjectRoot: root, RemoteName: remote, Branch: state.HeadBranch})
			if liveErr != nil {
				classified := classifyProcess("git", "inspect live remote", liveErr)
				operational, _ := classified.(*Error)
				return response, &Error{Code: operational.Code, Message: "live remote verification failed", Provider: "git", Cause: liveErr, Retryable: operational.Retryable, Hint: "Check network access and the configured remote, then rerun dvz status --remote."}
			}
			response.LiveRemote.Exists = live.Exists
			response.LiveRemote.Commit = live.Commit
			response.classifyLiveRemote(state)
		}
	}
	return response, nil
}

func relationName(relation gitadapter.TrackingRelation) string {
	switch {
	case relation.Ahead == 0 && relation.Behind == 0:
		return "synchronized"
	case relation.Ahead > 0 && relation.Behind == 0:
		return "ahead"
	case relation.Ahead == 0 && relation.Behind > 0:
		return "behind"
	default:
		return "diverged"
	}
}

func (r *StatusResponse) classifyLiveRemote(state gitadapter.InspectRepoResult) {
	if !r.LiveRemote.Exists {
		r.LiveRemote.Relation = "unpublished"
		r.LiveRemote.Detail = "The branch does not exist on the live remote."
		return
	}
	if r.LiveRemote.Commit == state.HeadCommit {
		r.LiveRemote.Relation = "synchronized"
		r.LiveRemote.Detail = "The live branch matches local HEAD."
		return
	}
	if state.UpstreamCommit != "" && r.LiveRemote.Commit == state.UpstreamCommit {
		if r.Upstream.Relation == "ahead" {
			r.LiveRemote.Relation = "local_ahead"
			r.LiveRemote.Detail = "The live branch matches local tracking state and local HEAD is ahead."
			return
		}
		r.LiveRemote.Relation = "synchronized"
		r.LiveRemote.Detail = "The live branch matches local tracking state; see the local upstream relation."
		return
	}
	r.LiveRemote.Relation = "tracking_stale"
	if state.UpstreamCommit == "" {
		r.LiveRemote.Detail = "No synchronized local tracking state is available; Devtize did not fetch and cannot safely infer a stronger relation."
	} else {
		r.LiveRemote.Detail = "The live branch differs from local tracking state; Devtize did not fetch and cannot safely infer a stronger relation."
	}
	r.Warnings = append(r.Warnings, r.LiveRemote.Detail)
}

func (r *StatusResponse) addWorkingTreeGuidance() {
	if len(r.Changes.Staged) > 0 {
		r.Warnings = append(r.Warnings, "The index contains staged changes.")
		r.RecommendedActions = append(r.RecommendedActions, "Review staged changes before creating another commit.")
	}
	if len(r.Changes.Unstaged) > 0 || len(r.Changes.Untracked) > 0 {
		r.RecommendedActions = append(r.RecommendedActions, "Use dvz commit with explicit paths and a reviewed message when these changes are ready.")
	}
}

func (r *StatusResponse) addUpstreamGuidance() {
	switch r.Upstream.Relation {
	case "ahead":
		r.RecommendedActions = append(r.RecommendedActions, "Run dvz ship to review and push the outgoing commits.")
	case "behind", "diverged":
		r.Warnings = append(r.Warnings, "The local branch is "+r.Upstream.Relation+" relative to its tracking branch.")
		r.RecommendedActions = append(r.RecommendedActions, "Reconcile the branch with reviewed Git operations before committing or shipping; Devtize will not pull, rebase, reset, or force-push automatically.")
	case "no_upstream":
		r.RecommendedActions = append(r.RecommendedActions, "Configure and verify an upstream before shipping this branch.")
	}
}

func sortedCopy(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}
