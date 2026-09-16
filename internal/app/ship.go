package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/safety"
)

const maxShipCommits = 100

type ShipOptions struct {
	Remote   string
	DryRun   bool
	PlanJSON bool
}

type ShipResponse struct {
	SchemaVersion int                          `json:"schema_version"`
	Status        operation.Status             `json:"status"`
	Plan          operation.Plan               `json:"plan"`
	Git           gitadapter.InspectRepoResult `json:"git"`
	RemoteName    string                       `json:"remote_name"`
	RemoteURL     string                       `json:"remote_url"`
	RemoteCommit  string                       `json:"remote_commit"`
	Commits       []gitadapter.CommitSummary   `json:"commits,omitempty"`
	ExcludedPaths []string                     `json:"excluded_working_paths,omitempty"`
	Result        operation.ExecutionResult    `json:"result,omitempty"`
}

type ShipGitPort interface {
	InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error)
	InspectChanges(context.Context, string) (gitadapter.ChangeSet, error)
	InspectRemoteBranch(context.Context, gitadapter.InspectRemoteBranchInput) (string, error)
	IsAncestor(context.Context, string, string, string) (bool, error)
	InspectOutgoingCommits(context.Context, gitadapter.InspectOutgoingCommitsInput) ([]gitadapter.CommitSummary, error)
	PushExistingBranch(context.Context, gitadapter.PushExistingBranchInput) error
}

type ShipService struct {
	WorkingDir string
	Config     config.Config
	Runner     interface {
		Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
	}
	Git     ShipGitPort
	History history.Store
	Now     func() time.Time
	Output  io.Writer
}

func (s ShipService) Plan(ctx context.Context, options ShipOptions) (ShipResponse, error) {
	s = s.withDefaults()
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return ShipResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	remote := firstNonEmpty(options.Remote, "origin")
	if !safeName.MatchString(remote) || remote[0] == '-' {
		return ShipResponse{}, &Error{Code: CodePlanInvalid, Message: "remote name is invalid"}
	}
	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: root, RemoteName: remote})
	if err != nil {
		return ShipResponse{}, classifyProcess("git", "inspect repository", err)
	}
	if !state.IsRepository || !state.HasCommits {
		return ShipResponse{}, &Error{Code: CodeProjectNotFound, Message: "ship requires an existing Git repository with a commit"}
	}
	if state.IsDetachedHead || state.HeadBranch == "" {
		return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "ship requires an attached branch"}
	}
	if len(state.RemoteURLs) != 1 {
		return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "ship requires exactly one URL for remote " + remote}
	}
	expectedUpstream := remote + "/" + state.HeadBranch
	if state.Upstream != expectedUpstream || state.UpstreamCommit == "" {
		return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "ship requires the current branch to track " + expectedUpstream}
	}
	changes, err := s.Git.InspectChanges(ctx, root)
	if err != nil {
		return ShipResponse{}, classifyProcess("git", "inspect excluded working changes", err)
	}
	excluded := shipChangedPaths(changes)
	liveRemote, err := s.Git.InspectRemoteBranch(ctx, gitadapter.InspectRemoteBranchInput{ProjectRoot: root, RemoteName: remote, Branch: state.HeadBranch})
	if err != nil {
		return ShipResponse{}, classifyProcess("git", "inspect live remote branch", err)
	}
	var commits []gitadapter.CommitSummary
	var operations []operation.Operation
	if liveRemote == state.HeadCommit {
		if state.UpstreamCommit != state.HeadCommit {
			return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "live remote matches HEAD but the local remote-tracking ref is stale", Hint: "Refresh the remote-tracking state, then rerun dvz ship."}
		}
	} else {
		if liveRemote != state.UpstreamCommit {
			return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "live remote changed since the local remote-tracking ref was updated", Hint: "Refresh and inspect the remote before shipping; Devtize will not push from stale state."}
		}
		ancestor, ancestorErr := s.Git.IsAncestor(ctx, root, liveRemote, state.HeadCommit)
		if ancestorErr != nil {
			return ShipResponse{}, classifyProcess("git", "verify fast-forward ancestry", ancestorErr)
		}
		if !ancestor {
			return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "remote branch is not an ancestor of local HEAD", Hint: "Reconcile the branch without force, then build a fresh ship plan."}
		}
		commits, err = s.Git.InspectOutgoingCommits(ctx, gitadapter.InspectOutgoingCommitsInput{ProjectRoot: root, BaseCommit: liveRemote, HeadCommit: state.HeadCommit})
		if err != nil {
			return ShipResponse{}, classifyProcess("git", "inspect outgoing commits", err)
		}
		if len(commits) == 0 || commits[len(commits)-1].SHA != state.HeadCommit {
			return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "outgoing commit range does not end at local HEAD"}
		}
		if len(commits) > maxShipCommits {
			return ShipResponse{}, &Error{Code: CodePreconditionFailed, Message: "ship plan exceeds the 100-commit review limit", Hint: "Ship a smaller reviewed range."}
		}
		operations = append(operations, operation.Operation{
			ID: "op_ship_push", CapabilityID: "git.branch.push", ProviderID: "git", Summary: "Push reviewed commits without force", Risk: safety.RiskRemoteWrite,
			Effects: []operation.Effect{{Kind: "fast_forward_remote_branch", Target: remote + "/" + state.HeadBranch}},
			Inputs: map[string]any{
				"project_root": root, "remote_name": remote, "remote_url": state.RemoteURLs[0], "branch": state.HeadBranch,
				"expected_remote_commit": liveRemote, "head_commit": state.HeadCommit, "commits": commits, "excluded_working_paths": excluded,
			},
		})
	}
	now := s.now()
	plan := operation.Plan{
		SchemaVersion: 1, ID: "plan_ship_" + now.Format("20060102150405"), Intent: "Push reviewed local commits without force",
		CreatedAt: now, ProjectRoot: root, DryRun: options.DryRun || options.PlanJSON, Operations: operations,
	}
	plan, err = plan.WithDigest()
	if err != nil {
		return ShipResponse{}, Wrap(CodePlanInvalid, "ship plan could not be digested", err)
	}
	status := operation.StatusProposed
	if plan.DryRun {
		status = operation.StatusValidated
	}
	return ShipResponse{
		SchemaVersion: 1, Status: status, Plan: plan, Git: state, RemoteName: remote, RemoteURL: state.RemoteURLs[0],
		RemoteCommit: liveRemote, Commits: commits, ExcludedPaths: excluded,
	}, nil
}

func (s ShipService) ExecutePlanned(ctx context.Context, options ShipOptions, prompts io.Reader, response ShipResponse) (ShipResponse, error) {
	s = s.withDefaults()
	digest, err := operation.Digest(response.Plan)
	if err != nil || digest != response.Plan.Digest {
		return response, &Error{Code: CodePlanInvalid, Message: "authorized ship plan digest is invalid", Cause: err}
	}
	if response.Plan.DryRun || options.DryRun || options.PlanJSON {
		return response, nil
	}
	result := operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest}
	if len(response.Plan.Operations) == 0 {
		result.Status = operation.StatusSucceeded
		response.Status = operation.StatusSucceeded
		response.Result = result
		return response, nil
	}
	if len(response.Plan.Operations) != 1 || response.Plan.Operations[0].CapabilityID != "git.branch.push" {
		return response, &Error{Code: CodePlanInvalid, Message: "ship plan contains unsupported operations"}
	}
	op := response.Plan.Operations[0]
	if !shipResponseMatchesPlan(response, op) {
		return response, &Error{Code: CodePlanInvalid, Message: "rendered ship details do not match the authorized plan"}
	}
	if err := s.validateCurrentPlan(ctx, op, response); err != nil {
		return response, err
	}
	answers := newConfirmationReader(prompts, s.Output)
	ok, confirmErr := answers.confirm("Push reviewed commits to "+stringInput(op, "remote_name")+"/"+stringInput(op, "branch"), response.Plan.Digest, "push")
	if confirmErr != nil || !ok {
		response.Status = operation.StatusCancelled
		result.Status = operation.StatusCancelled
		result.RecoveryHints = []string{"No push was attempted. Rerun dvz ship to build a fresh plan."}
		response.Result = result
		return response, &Error{Code: CodeConfirmationDeclined, Message: "confirmation declined", Hint: result.RecoveryHints[0]}
	}
	if err := s.validateCurrentPlan(ctx, op, response); err != nil {
		return response, err
	}
	result.Status = operation.StatusRunning
	step := operation.StepResult{OperationID: op.ID, CapabilityID: op.CapabilityID, Summary: op.Summary, Status: operation.StatusRunning, StartedAt: s.now()}
	err = s.Git.PushExistingBranch(ctx, gitadapter.PushExistingBranchInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(op, "remote_name"), Branch: stringInput(op, "branch")})
	step.FinishedAt = s.now()
	if err != nil {
		step.Status = operation.StatusFailed
		step.ErrorCode = string(CodeProcessFailed)
		step.ErrorMessage = "Push reviewed commits failed"
		step.RecoveryHint = "The remote outcome may be uncertain. Rerun dvz ship; it will inspect the live branch and safely skip an already-completed push."
		result.Status = operation.StatusPartiallyCompleted
		result.Steps = append(result.Steps, step)
		result.RecoveryHints = []string{step.RecoveryHint}
		response.Result = result
		_ = s.writeHistory(response, result)
		return response, &Error{Code: CodePartialExecution, Message: step.ErrorMessage, Cause: err, Hint: step.RecoveryHint}
	}
	step.Status = operation.StatusSucceeded
	result.Steps = append(result.Steps, step)

	remoteCommit, remoteErr := s.Git.InspectRemoteBranch(ctx, gitadapter.InspectRemoteBranchInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(op, "remote_name"), Branch: stringInput(op, "branch")})
	state, stateErr := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(op, "remote_name")})
	if remoteErr != nil || stateErr != nil || remoteCommit != stringInput(op, "head_commit") || state.HeadCommit != stringInput(op, "head_commit") || state.Upstream != stringInput(op, "remote_name")+"/"+stringInput(op, "branch") || state.UpstreamCommit != state.HeadCommit {
		result.Status = operation.StatusPartiallyCompleted
		hint := "Inspect local HEAD, its upstream, and the live remote branch before retrying; Devtize will not force-push."
		result.RecoveryHints = []string{hint}
		response.Result = result
		_ = s.writeHistory(response, result)
		return response, &Error{Code: CodePostconditionFailed, Message: "ship postconditions were not satisfied", Cause: errors.Join(remoteErr, stateErr), Hint: hint}
	}
	result.Status = operation.StatusSucceeded
	response.Status = operation.StatusSucceeded
	response.Git = state
	response.RemoteCommit = remoteCommit
	response.Result = result
	if err := s.writeHistory(response, result); err != nil {
		return response, &Error{Code: CodeHistoryWriteFailed, Message: "history could not be written", Cause: err}
	}
	return response, nil
}

func shipResponseMatchesPlan(response ShipResponse, op operation.Operation) bool {
	return response.RemoteName == stringInput(op, "remote_name") &&
		response.RemoteURL == stringInput(op, "remote_url") &&
		response.RemoteCommit == stringInput(op, "expected_remote_commit") &&
		response.Git.HeadBranch == stringInput(op, "branch") &&
		response.Git.HeadCommit == stringInput(op, "head_commit") &&
		reflect.DeepEqual(response.Commits, commitSummariesInput(op, "commits")) &&
		equalStringSlices(response.ExcludedPaths, stringSliceInput(op, "excluded_working_paths"))
}

func (s ShipService) validateCurrentPlan(ctx context.Context, op operation.Operation, response ShipResponse) error {
	remote := stringInput(op, "remote_name")
	branch := stringInput(op, "branch")
	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: remote})
	if err != nil || state.IsDetachedHead || state.HeadBranch != branch || state.HeadCommit != stringInput(op, "head_commit") || state.Upstream != remote+"/"+branch || state.UpstreamCommit != stringInput(op, "expected_remote_commit") || len(state.RemoteURLs) != 1 || state.RemoteURLs[0] != stringInput(op, "remote_url") {
		return &Error{Code: CodePreconditionFailed, Message: "local branch, upstream, or remote changed after planning", Cause: err, Hint: "No push was attempted. Build and review a fresh ship plan."}
	}
	liveRemote, err := s.Git.InspectRemoteBranch(ctx, gitadapter.InspectRemoteBranchInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: remote, Branch: branch})
	if err != nil || liveRemote != stringInput(op, "expected_remote_commit") {
		return &Error{Code: CodePreconditionFailed, Message: "live remote branch changed after planning", Cause: err, Hint: "No push was attempted. Build and review a fresh ship plan."}
	}
	commits, err := s.Git.InspectOutgoingCommits(ctx, gitadapter.InspectOutgoingCommitsInput{ProjectRoot: response.Plan.ProjectRoot, BaseCommit: liveRemote, HeadCommit: state.HeadCommit})
	if err != nil || !reflect.DeepEqual(commits, commitSummariesInput(op, "commits")) {
		return &Error{Code: CodePreconditionFailed, Message: "outgoing commits changed after planning", Cause: err, Hint: "No push was attempted. Build and review a fresh ship plan."}
	}
	changes, err := s.Git.InspectChanges(ctx, response.Plan.ProjectRoot)
	if err != nil || !equalStringSlices(shipChangedPaths(changes), stringSliceInput(op, "excluded_working_paths")) {
		return &Error{Code: CodePreconditionFailed, Message: "excluded working changes changed after planning", Cause: err, Hint: "No push was attempted. Build and review a fresh ship plan."}
	}
	return nil
}

func shipChangedPaths(changes gitadapter.ChangeSet) []string {
	paths := append([]string{}, changes.Staged...)
	paths = append(paths, changes.Unstaged...)
	paths = append(paths, changes.Untracked...)
	return uniqueSorted(paths)
}

func commitSummariesInput(op operation.Operation, name string) []gitadapter.CommitSummary {
	switch values := op.Inputs[name].(type) {
	case []gitadapter.CommitSummary:
		return values
	case []any:
		result := make([]gitadapter.CommitSummary, 0, len(values))
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				sha, _ := item["sha"].(string)
				subject, _ := item["subject"].(string)
				result = append(result, gitadapter.CommitSummary{SHA: sha, Subject: subject})
			}
		}
		return result
	default:
		return nil
	}
}

func (s ShipService) writeHistory(response ShipResponse, result operation.ExecutionResult) error {
	if !s.Config.History.Enabled {
		return nil
	}
	return s.History.Append(history.Record{
		SchemaVersion: 1, ExecutionID: "exec_" + s.now().Format("20060102150405"), PlanID: response.Plan.ID,
		PlanDigest: response.Plan.Digest, StartedAt: response.Plan.CreatedAt, FinishedAt: s.now(),
		Invocation: map[string]any{"workflow": "ship", "remote": response.RemoteName}, Project: map[string]string{"root": response.Plan.ProjectRoot},
		Status: result.Status, Steps: result.Steps, RecoveryHints: result.RecoveryHints,
	})
}

func (s ShipService) withDefaults() ShipService {
	if s.Git == nil {
		s.Git = gitadapter.New(s.Runner)
	}
	return s
}

func (s ShipService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
