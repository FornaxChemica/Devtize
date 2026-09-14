package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	ghadapter "github.com/FornaxChemica/devtize/internal/adapters/github"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/safety"
)

type RepoOptions struct {
	Paths           []string
	Name            string
	Owner           string
	Visibility      string
	Branch          string
	Message         string
	GenerateMessage bool
	Remote          string
	Description     string
	Homepage        string
	DryRun          bool
	PlanJSON        bool
	Yes             bool
	RepairInitial   bool
}

type RedactInitialOptions struct {
	Path     string
	Remote   string
	DryRun   bool
	PlanJSON bool
}

type RepoResponse struct {
	SchemaVersion int                          `json:"schema_version"`
	Status        operation.Status             `json:"status"`
	Plan          operation.Plan               `json:"plan"`
	Selection     RepoSelection                `json:"selection"`
	Git           gitadapter.InspectRepoResult `json:"git"`
	GitHubAuth    ghadapter.AuthResult         `json:"github_auth"`
	GitHubRepo    ghadapter.RepoResult         `json:"github_repo"`
	Result        operation.ExecutionResult    `json:"result,omitempty"`
	Warnings      []string                     `json:"warnings,omitempty"`
}

type RepoSelection struct {
	Mode         string   `json:"mode"`
	Paths        []string `json:"paths"`
	IgnoredPaths []string `json:"ignored_paths,omitempty"`
	UntrackPaths []string `json:"untrack_paths,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

type RepoService struct {
	WorkingDir string
	Config     config.Config
	Runner     interface {
		Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
	}
	Git     GitPort
	GitHub  GitHubPort
	History history.Store
	Now     func() time.Time
	Output  io.Writer
}

type GitPort interface {
	InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error)
	InitRepo(context.Context, gitadapter.InitRepoInput) error
	Stage(context.Context, gitadapter.StageInput) error
	CreateCommit(context.Context, gitadapter.CommitInput) error
	Untrack(context.Context, gitadapter.UntrackInput) error
	AmendCommit(context.Context, gitadapter.CommitInput) error
	AmendCommitPreservingMessage(context.Context, string) error
	InspectRemote(context.Context, gitadapter.RemoteInput) ([]string, error)
	ConfigureRemote(context.Context, gitadapter.RemoteInput) error
	UpdateRemote(context.Context, gitadapter.RemoteInput) error
	PushBranch(context.Context, gitadapter.PushInput) error
	ForcePushWithLease(context.Context, gitadapter.ForcePushWithLeaseInput) error
	InspectRemoteBranch(context.Context, gitadapter.InspectRemoteBranchInput) (string, error)
}

type GitHubPort interface {
	InspectAuth(context.Context, ghadapter.AuthInput) (ghadapter.AuthResult, error)
	InspectRepo(context.Context, ghadapter.RepoInput) (ghadapter.RepoResult, error)
	CreateRepo(context.Context, ghadapter.RepoInput) (ghadapter.RepoResult, error)
}

func (s RepoService) Plan(ctx context.Context, options RepoOptions) (RepoResponse, error) {
	s = s.withDefaultAdapters()
	now := s.now()
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return RepoResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	if options.GenerateMessage {
		return RepoResponse{}, &Error{Code: CodeAIUnavailable, Message: "commit message generation is unavailable; pass --message"}
	}
	if strings.TrimSpace(options.Message) == "" {
		return RepoResponse{}, &Error{Code: CodePlanInvalid, Message: "an initial commit message is required", Hint: "Pass --message \"Initial commit\"."}
	}
	name := options.Name
	if name == "" {
		name = filepath.Base(root)
	}
	branch := firstNonEmpty(options.Branch, s.Config.Git.DefaultBranch, "main")
	remote := firstNonEmpty(options.Remote, "origin")
	visibility := firstNonEmpty(options.Visibility, s.Config.GitHub.Visibility, "private")
	if err := validateRepoInputs(root, name, options.Owner, branch, remote, visibility, options.Message); err != nil {
		return RepoResponse{}, err
	}
	gitState, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: root, RemoteName: remote})
	if err != nil {
		return RepoResponse{}, classifyProcess("git", "inspect repository", err)
	}
	if gitState.IsDetachedHead {
		return RepoResponse{}, &Error{Code: CodePreconditionFailed, Message: "repository is in detached HEAD state"}
	}
	selection, err := selectPaths(root, options.Paths)
	if err != nil {
		return RepoResponse{}, err
	}
	if len(selection.Paths) == 0 {
		return RepoResponse{}, &Error{Code: CodePreconditionFailed, Message: "no eligible files were selected for staging"}
	}
	auth, err := s.GitHub.InspectAuth(ctx, ghadapter.AuthInput{ProjectRoot: root})
	if err != nil {
		return RepoResponse{}, classifyProcess("gh", "inspect GitHub authentication", err)
	}
	owner := options.Owner
	if owner == "" {
		owner = auth.Owner
	}
	if owner == "" {
		return RepoResponse{}, &Error{Code: CodePlanInvalid, Message: "GitHub owner could not be determined", Hint: "Pass --owner <owner-or-org>."}
	}
	if err := validateRepoInputs(root, name, owner, branch, remote, visibility, options.Message); err != nil {
		return RepoResponse{}, err
	}
	if auth.Status != "" && auth.Status != "authenticated" {
		return RepoResponse{}, &Error{Code: CodeAuthRequired, Message: "GitHub authentication is required for remote writes", Hint: "Run gh auth login, then rerun the same dvz repo create command."}
	}
	ghRepo, err := s.GitHub.InspectRepo(ctx, ghadapter.RepoInput{ProjectRoot: root, Owner: owner, Name: name})
	if err != nil {
		return RepoResponse{}, classifyProcess("gh", "inspect GitHub repository", err)
	}
	if ghRepo.Exists && ghRepo.Visibility != "" && !strings.EqualFold(ghRepo.Visibility, visibility) {
		return RepoResponse{}, &Error{Code: CodePreconditionFailed, Message: "GitHub repository already exists with different visibility"}
	}
	if options.RepairInitial {
		if err := validateInitialRepair(gitState, ghRepo); err != nil {
			return RepoResponse{}, err
		}
		selection.UntrackPaths = ignoredTrackedPaths(root, gitState.TrackedPaths)
	} else if gitState.HasCommits && gitState.WorkingTreeStatus == "dirty" {
		return RepoResponse{}, &Error{
			Code:    CodePreconditionFailed,
			Message: "repository has an existing commit and uncommitted changes",
			Hint:    "Commit or discard the changes outside this workflow, or use --repair-unpushed-initial only for a verified unpushed first commit.",
		}
	}
	remoteURL := desiredRemoteURL(auth.GitProtocol, owner, name, ghRepo)
	if len(gitState.RemoteURLs) > 0 {
		if len(gitState.RemoteURLs) != 1 {
			return RepoResponse{}, &Error{Code: CodePreconditionFailed, Message: "remote " + remote + " has multiple URLs", Hint: "Resolve the remote configuration before rerunning."}
		}
		for _, existing := range gitState.RemoteURLs {
			if existing != remoteURL && !sameGitHubRepository(existing, remoteURL) {
				return RepoResponse{}, &Error{Code: CodePreconditionFailed, Message: "remote " + remote + " points to an unexpected URL", Hint: "Choose a different --remote or resolve the existing remote before rerunning."}
			}
		}
	}
	ops := repoOperations(root, gitState, ghRepo, selection, owner, name, visibility, branch, remote, remoteURL, options.Message, options.RepairInitial)
	plan := operation.Plan{
		SchemaVersion: 1, ID: "plan_repo_create_" + now.Format("20060102150405"),
		Intent: "Create or verify the local Git repository and GitHub remote", CreatedAt: now,
		ProjectRoot: root, DryRun: options.DryRun || options.PlanJSON, Operations: ops,
	}
	plan, err = plan.WithDigest()
	if err != nil {
		return RepoResponse{}, Wrap(CodePlanInvalid, "plan could not be digested", err)
	}
	status := operation.StatusProposed
	if options.DryRun || options.PlanJSON {
		status = operation.StatusValidated
	}
	return RepoResponse{
		SchemaVersion: 1, Status: status, Plan: plan, Selection: selection,
		Git: gitState, GitHubAuth: auth, GitHubRepo: ghRepo, Warnings: selection.Warnings,
	}, nil
}

func (s RepoService) PlanInitialRedaction(ctx context.Context, options RedactInitialOptions) (RepoResponse, error) {
	s = s.withDefaultAdapters()
	now := s.now()
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return RepoResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	path, err := validateRedactionPath(root, options.Path)
	if err != nil {
		return RepoResponse{}, err
	}
	remote := firstNonEmpty(options.Remote, "origin")
	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: root, RemoteName: remote})
	if err != nil {
		return RepoResponse{}, classifyProcess("git", "inspect repository", err)
	}
	if err := validateRedactionState(root, path, remote, state); err != nil {
		return RepoResponse{}, err
	}
	remoteCommit, err := s.Git.InspectRemoteBranch(ctx, gitadapter.InspectRemoteBranchInput{ProjectRoot: root, RemoteName: remote, Branch: state.HeadBranch})
	if err != nil {
		return RepoResponse{}, classifyProcess("git", "inspect live remote branch", err)
	}
	if remoteCommit != state.HeadCommit {
		return RepoResponse{}, &Error{Code: CodePreconditionFailed, Message: "live remote branch does not match local HEAD", Hint: "Refresh and inspect repository state; do not rewrite a branch that changed remotely."}
	}
	selection := redactionSelection(root, path, state)
	if len(selection.IgnoredPaths) > 0 {
		return RepoResponse{}, &Error{Code: CodePreconditionFailed, Message: "other changed ignored paths cannot be included in initial redaction", Hint: "Resolve the listed ignored changes before rerunning."}
	}
	operations := []operation.Operation{
		{ID: "op_git_redact_untrack", CapabilityID: "git.index.untrack", ProviderID: "git", Summary: "Remove the ignored file from the initial commit while preserving it locally", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "untrack_path", Target: path}}, Inputs: map[string]any{"project_root": root, "paths": []string{path}}},
	}
	if len(selection.Paths) > 0 {
		digests, digestErr := fileDigests(root, selection.Paths)
		if digestErr != nil {
			return RepoResponse{}, Wrap(CodePlanInvalid, "could not digest disclosed remediation files", digestErr)
		}
		operations = append(operations, operation.Operation{ID: "op_git_redact_stage", CapabilityID: "git.index.stage", ProviderID: "git", Summary: "Stage disclosed remediation files", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "stage_paths", Target: root}}, Inputs: map[string]any{"project_root": root, "paths": selection.Paths, "selection_mode": selection.Mode, "content_digests": digests}})
	}
	operations = append(operations,
		operation.Operation{ID: "op_git_redact_amend", CapabilityID: "git.commit.amend_initial_preserve_message", ProviderID: "git", Summary: "Amend the single initial commit while preserving its message", Risk: safety.RiskDestructive, Effects: []operation.Effect{{Kind: "rewrite_initial_commit", Target: state.HeadCommit}}, Inputs: map[string]any{"project_root": root}},
		operation.Operation{ID: "op_git_redact_push", CapabilityID: "git.branch.force_push_with_lease", ProviderID: "git", Summary: "Replace the published initial commit using an exact expected-SHA lease", Risk: safety.RiskDestructive, Effects: []operation.Effect{{Kind: "replace_remote_branch_with_lease", Target: state.Upstream}}, Inputs: map[string]any{"project_root": root, "remote_name": remote, "branch": state.HeadBranch, "expected_commit": state.UpstreamCommit}},
	)
	plan := operation.Plan{
		SchemaVersion: 1, ID: "plan_repo_redact_initial_" + now.Format("20060102150405"),
		Intent: "Remove one ignored local file from the synchronized pushed initial commit", CreatedAt: now,
		ProjectRoot: root, DryRun: options.DryRun || options.PlanJSON, Operations: operations,
	}
	plan, err = plan.WithDigest()
	if err != nil {
		return RepoResponse{}, Wrap(CodePlanInvalid, "redaction plan could not be digested", err)
	}
	status := operation.StatusProposed
	if plan.DryRun {
		status = operation.StatusValidated
	}
	return RepoResponse{SchemaVersion: 1, Status: status, Plan: plan, Selection: selection, Git: state, Warnings: selection.Warnings}, nil
}

func (s RepoService) ExecuteInitialRedactionPlanned(ctx context.Context, options RedactInitialOptions, prompts io.Reader, response RepoResponse) (RepoResponse, error) {
	s = s.withDefaultAdapters()
	digest, err := operation.Digest(response.Plan)
	if err != nil || digest != response.Plan.Digest {
		return response, &Error{Code: CodePlanInvalid, Message: "authorized redaction plan digest is invalid", Cause: err}
	}
	if options.DryRun || options.PlanJSON {
		return response, nil
	}
	answers := newConfirmationReader(prompts, s.Output)
	if len(response.Warnings) > 0 {
		ok, confirmErr := answers.confirm("Potential secret warning", response.Plan.Digest, "yes")
		if confirmErr != nil || !ok {
			return cancelled(response, "secret warning was not confirmed")
		}
	}
	ok, err := answers.confirm("Remove the disclosed file and rewrite the local initial commit", response.Plan.Digest, "redact")
	if err != nil || !ok {
		return cancelled(response, "initial commit redaction was not confirmed")
	}
	if err := validateRedactionFileDigests(response.Plan); err != nil {
		return response, err
	}
	remoteCommit, inspectRemoteErr := s.Git.InspectRemoteBranch(ctx, gitadapter.InspectRemoteBranchInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(response.Plan.Operations[len(response.Plan.Operations)-1], "remote_name"), Branch: stringInput(response.Plan.Operations[len(response.Plan.Operations)-1], "branch")})
	if inspectRemoteErr != nil || remoteCommit != response.Git.HeadCommit {
		return response, &Error{Code: CodePreconditionFailed, Message: "live remote branch changed after planning", Cause: inspectRemoteErr, Hint: "No mutation was performed. Build and review a fresh redaction plan."}
	}
	result := operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusRunning}
	for _, op := range response.Plan.Operations {
		if op.CapabilityID == "git.branch.force_push_with_lease" {
			break
		}
		step := s.runStep(ctx, op, response)
		result.Steps = append(result.Steps, step)
		if step.Status == operation.StatusFailed {
			return s.redactionFailure(response, result, options, step)
		}
	}
	path := stringSliceInput(response.Plan.Operations[0], "paths")[0]
	state, inspectErr := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: firstNonEmpty(options.Remote, "origin")})
	if inspectErr != nil || state.CommitCount != 1 || state.HeadCommit == response.Git.HeadCommit || state.WorkingTreeStatus != "clean" || containsString(state.TrackedPaths, path) {
		return s.redactionPostconditionFailure(response, result, options, "local redaction postconditions were not satisfied", inspectErr)
	}
	if _, statErr := os.Stat(filepath.Join(response.Plan.ProjectRoot, filepath.FromSlash(path))); statErr != nil {
		return s.redactionPostconditionFailure(response, result, options, "redacted local file was not preserved", statErr)
	}
	response.Git = state
	ok, err = answers.confirm("Replace the published initial commit using the exact expected-SHA lease", response.Plan.Digest, "force-update")
	if err != nil || !ok {
		result.Status = operation.StatusPartiallyCompleted
		result.RecoveryHints = append(result.RecoveryHints, "The local commit was rewritten but the remote was not. Re-run only after verifying the remote still points to the plan's expected commit.")
		response.Status = operation.StatusCancelled
		response.Result = result
		_ = s.writeHistoryInvocation(response, result, map[string]any{"redact_initial_path": path})
		return response, &Error{Code: CodeConfirmationDeclined, Message: "remote replacement confirmation declined", Hint: result.RecoveryHints[len(result.RecoveryHints)-1]}
	}
	forceOp := response.Plan.Operations[len(response.Plan.Operations)-1]
	step := s.runStep(ctx, forceOp, response)
	result.Steps = append(result.Steps, step)
	if step.Status == operation.StatusFailed {
		return s.redactionFailure(response, result, options, step)
	}
	state, inspectErr = s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: firstNonEmpty(options.Remote, "origin")})
	if inspectErr != nil || state.UpstreamCommit == "" || state.UpstreamCommit != state.HeadCommit {
		return s.redactionPostconditionFailure(response, result, options, "remote redaction postconditions were not satisfied", inspectErr)
	}
	result.Status = operation.StatusSucceeded
	response.Status = operation.StatusSucceeded
	response.Git = state
	response.Result = result
	if err := s.writeHistoryInvocation(response, result, map[string]any{"redact_initial_path": path}); err != nil {
		return response, &Error{Code: CodeHistoryWriteFailed, Message: "history could not be written", Cause: err}
	}
	return response, nil
}

func (s RepoService) Execute(ctx context.Context, options RepoOptions, prompts io.Reader) (RepoResponse, error) {
	s = s.withDefaultAdapters()
	response, err := s.Plan(ctx, options)
	if err != nil {
		return response, err
	}
	return s.ExecutePlanned(ctx, options, prompts, response)
}

func (s RepoService) ExecutePlanned(ctx context.Context, options RepoOptions, prompts io.Reader, response RepoResponse) (RepoResponse, error) {
	s = s.withDefaultAdapters()
	digest, err := operation.Digest(response.Plan)
	if err != nil || digest != response.Plan.Digest {
		return response, &Error{Code: CodePlanInvalid, Message: "authorized plan digest is invalid", Cause: err}
	}
	if options.DryRun || options.PlanJSON {
		return response, nil
	}
	if evaluation := safety.Evaluate(planRisks(response.Plan)); evaluation.Decision == safety.DecisionDeny && !isNarrowInitialRepair(response.Plan) {
		return response, &Error{Code: CodePolicyDenied, Message: evaluation.Reason}
	}
	answers := newConfirmationReader(prompts, s.Output)
	if len(response.Selection.Warnings) > 0 {
		ok, err := answers.confirm("Potential secret warning", response.Plan.Digest, "yes")
		if err != nil || !ok {
			return cancelled(response, "secret warning was not confirmed")
		}
	}
	if options.RepairInitial {
		ok, err := answers.confirm("Rewrite the verified unpushed initial commit", response.Plan.Digest, "repair")
		if err != nil || !ok {
			return cancelled(response, "initial commit repair was not confirmed")
		}
	}
	if !options.Yes || !yesAllowed(s.Config, safety.RiskLocalWrite) {
		ok, err := answers.confirm("Apply local repository changes", response.Plan.Digest, "yes")
		if err != nil || !ok {
			return cancelled(response, "local write confirmation was declined")
		}
	}
	result := operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusRunning}
	completedMutation := false
	for _, op := range response.Plan.Operations {
		if op.Risk == safety.RiskRemoteWrite {
			break
		}
		step := s.runStep(ctx, op, response)
		result.Steps = append(result.Steps, step)
		if step.Status == operation.StatusSucceeded {
			completedMutation = true
		}
		if step.Status == operation.StatusFailed {
			result.Status = partialStatus(completedMutation)
			result.RecoveryHints = append(result.RecoveryHints, step.RecoveryHint)
			response.Result = result
			_ = s.writeHistory(response, result, options)
			return response, &Error{Code: CodePartialExecution, Message: step.ErrorMessage, Hint: step.RecoveryHint}
		}
	}
	if options.RepairInitial {
		state, inspectErr := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: firstNonEmpty(options.Remote, "origin")})
		if inspectErr != nil || state.CommitCount != 1 || state.WorkingTreeStatus != "clean" || len(ignoredTrackedPaths(response.Plan.ProjectRoot, state.TrackedPaths)) > 0 {
			result.Status = operation.StatusPartiallyCompleted
			result.RecoveryHints = append(result.RecoveryHints, "Inspect the amended commit and index; do not push until exactly one clean commit remains and ignored artifacts are untracked.")
			response.Result = result
			_ = s.writeHistory(response, result, options)
			return response, &Error{Code: CodePostconditionFailed, Message: "initial commit repair postconditions were not satisfied", Cause: inspectErr, Hint: result.RecoveryHints[len(result.RecoveryHints)-1]}
		}
		response.Git = state
	}
	ok, err := answers.confirm("Create GitHub repository and push", response.Plan.Digest, "yes")
	if err != nil || !ok {
		response.Result = result
		return cancelled(response, "remote write confirmation was declined")
	}
	for _, op := range response.Plan.Operations {
		if op.Risk != safety.RiskRemoteWrite && op.CapabilityID != "git.remote.configure" {
			continue
		}
		if op.CapabilityID == "git.remote.configure" && alreadyRan(result.Steps, op.ID) {
			continue
		}
		step := s.runStep(ctx, op, response)
		result.Steps = append(result.Steps, step)
		if step.Status == operation.StatusSucceeded {
			completedMutation = true
		}
		if step.Status == operation.StatusFailed {
			result.Status = partialStatus(completedMutation)
			result.RecoveryHints = append(result.RecoveryHints, step.RecoveryHint)
			response.Result = result
			_ = s.writeHistory(response, result, options)
			return response, &Error{Code: CodePartialExecution, Message: step.ErrorMessage, Hint: step.RecoveryHint}
		}
	}
	if options.RepairInitial {
		state, inspectErr := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: firstNonEmpty(options.Remote, "origin")})
		expectedUpstream := firstNonEmpty(options.Remote, "origin") + "/" + firstNonEmpty(options.Branch, s.Config.Git.DefaultBranch, "main")
		if inspectErr != nil || state.Upstream != expectedUpstream || state.HeadBranch != firstNonEmpty(options.Branch, s.Config.Git.DefaultBranch, "main") {
			result.Status = operation.StatusPartiallyCompleted
			result.RecoveryHints = append(result.RecoveryHints, "Inspect the pushed branch and upstream, then rerun only if the remote branch is still safe to update without force.")
			response.Result = result
			_ = s.writeHistory(response, result, options)
			return response, &Error{Code: CodePostconditionFailed, Message: "push postconditions were not satisfied", Cause: inspectErr, Hint: result.RecoveryHints[len(result.RecoveryHints)-1]}
		}
		response.Git = state
	}
	result.Status = operation.StatusSucceeded
	response.Status = operation.StatusSucceeded
	response.Result = result
	if err := s.writeHistory(response, result, options); err != nil {
		return response, &Error{Code: CodeHistoryWriteFailed, Message: "history could not be written", Cause: err}
	}
	return response, nil
}

func (s RepoService) Status(ctx context.Context, remote string) (RepoResponse, error) {
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return RepoResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	s = s.withDefaultAdapters()
	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: root, RemoteName: firstNonEmpty(remote, "origin")})
	if err != nil {
		return RepoResponse{}, classifyProcess("git", "inspect repository", err)
	}
	return RepoResponse{SchemaVersion: 1, Status: operation.StatusSucceeded, Git: state}, nil
}

func (s RepoService) withDefaultAdapters() RepoService {
	if s.Git == nil {
		s.Git = gitadapter.New(s.Runner)
	}
	if s.GitHub == nil {
		s.GitHub = ghadapter.New(s.Runner)
	}
	return s
}

func (s RepoService) runStep(ctx context.Context, op operation.Operation, response RepoResponse) operation.StepResult {
	started := s.now()
	step := operation.StepResult{OperationID: op.ID, CapabilityID: op.CapabilityID, Status: operation.StatusSucceeded, Summary: op.Summary, StartedAt: started}
	defer func() { step.FinishedAt = s.now() }()
	var err error
	switch op.CapabilityID {
	case "git.repo.init":
		if response.Git.IsRepository {
			step.Status = operation.StatusSkipped
			return step
		}
		err = s.Git.InitRepo(ctx, gitadapter.InitRepoInput{ProjectRoot: response.Plan.ProjectRoot, InitialBranch: stringInput(op, "initial_branch")})
	case "git.index.stage":
		err = s.Git.Stage(ctx, gitadapter.StageInput{ProjectRoot: response.Plan.ProjectRoot, Paths: stringSliceInput(op, "paths")})
	case "git.commit.create":
		if response.Git.HasCommits {
			step.Status = operation.StatusSkipped
			return step
		}
		err = s.Git.CreateCommit(ctx, gitadapter.CommitInput{ProjectRoot: response.Plan.ProjectRoot, Message: stringInput(op, "message")})
	case "git.index.untrack":
		err = s.Git.Untrack(ctx, gitadapter.UntrackInput{ProjectRoot: response.Plan.ProjectRoot, Paths: stringSliceInput(op, "paths")})
	case "git.commit.amend_initial":
		err = s.Git.AmendCommit(ctx, gitadapter.CommitInput{ProjectRoot: response.Plan.ProjectRoot, Message: stringInput(op, "message")})
	case "git.commit.amend_initial_preserve_message":
		err = s.Git.AmendCommitPreservingMessage(ctx, response.Plan.ProjectRoot)
	case "github.repo.create":
		if response.GitHubRepo.Exists {
			step.Status = operation.StatusSkipped
			return step
		}
		_, err = s.GitHub.CreateRepo(ctx, ghadapter.RepoInput{
			ProjectRoot: response.Plan.ProjectRoot, Owner: stringInput(op, "owner"), Name: stringInput(op, "name"),
			Visibility: stringInput(op, "visibility"), Description: stringInput(op, "description"), Homepage: stringInput(op, "homepage"),
		})
	case "git.remote.configure":
		if len(response.Git.RemoteURLs) > 0 {
			step.Status = operation.StatusSkipped
			return step
		}
		err = s.Git.ConfigureRemote(ctx, gitadapter.RemoteInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(op, "remote_name"), URL: stringInput(op, "url")})
	case "git.remote.update":
		err = s.Git.UpdateRemote(ctx, gitadapter.RemoteInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(op, "remote_name"), URL: stringInput(op, "url")})
	case "git.branch.push":
		err = s.Git.PushBranch(ctx, gitadapter.PushInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(op, "remote_name"), Branch: stringInput(op, "branch")})
	case "git.branch.force_push_with_lease":
		err = s.Git.ForcePushWithLease(ctx, gitadapter.ForcePushWithLeaseInput{ProjectRoot: response.Plan.ProjectRoot, RemoteName: stringInput(op, "remote_name"), Branch: stringInput(op, "branch"), ExpectedCommit: stringInput(op, "expected_commit")})
	}
	if err != nil {
		step.Status = operation.StatusFailed
		step.ErrorCode = string(CodeProcessFailed)
		step.ErrorMessage = fmt.Sprintf("%s failed", op.Summary)
		step.RecoveryHint = recoveryHint(op.CapabilityID)
	}
	return step
}

func repoOperations(root string, gitState gitadapter.InspectRepoResult, ghRepo ghadapter.RepoResult, selection RepoSelection, owner, name, visibility, branch, remote, remoteURL, message string, repairInitial bool) []operation.Operation {
	var ops []operation.Operation
	if !gitState.IsRepository {
		ops = append(ops, operation.Operation{ID: "op_git_init", CapabilityID: "git.repo.init", ProviderID: "git", Summary: "Initialize Git repository", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "create_git_metadata", Target: root}}, Inputs: map[string]any{"project_root": root, "initial_branch": branch}})
	}
	if !gitState.HasCommits || repairInitial {
		if len(selection.UntrackPaths) > 0 {
			ops = append(ops, operation.Operation{ID: "op_git_untrack", CapabilityID: "git.index.untrack", ProviderID: "git", Summary: "Untrack ignored generated artifacts without deleting working files", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "untrack_disclosed_paths", Target: root}}, Inputs: map[string]any{"project_root": root, "paths": selection.UntrackPaths}})
		}
		ops = append(ops, operation.Operation{ID: "op_git_stage", CapabilityID: "git.index.stage", ProviderID: "git", Summary: "Stage selected files", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "stage_paths", Target: root}}, Inputs: map[string]any{"project_root": root, "paths": selection.Paths, "selection_mode": selection.Mode}})
	}
	if repairInitial {
		ops = append(ops, operation.Operation{ID: "op_git_amend_initial", CapabilityID: "git.commit.amend_initial", ProviderID: "git", Summary: "Amend the verified unpushed initial commit", Risk: safety.RiskDestructive, Effects: []operation.Effect{{Kind: "rewrite_unpushed_initial_commit", Target: gitState.HeadCommit}}, Inputs: map[string]any{"project_root": root, "message": message}})
	} else if !gitState.HasCommits {
		ops = append(ops, operation.Operation{ID: "op_git_commit", CapabilityID: "git.commit.create", ProviderID: "git", Summary: "Create initial commit", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "create_commit", Target: branch}}, Inputs: map[string]any{"project_root": root, "message": message, "allow_empty": false}})
	}
	if !ghRepo.Exists {
		ops = append(ops, operation.Operation{ID: "op_github_create", CapabilityID: "github.repo.create", ProviderID: "gh", Summary: "Create GitHub repository", Risk: safety.RiskRemoteWrite, Effects: []operation.Effect{{Kind: "create_remote_repository", Target: owner + "/" + name}}, Inputs: map[string]any{"owner": owner, "name": name, "visibility": visibility}})
	}
	if len(gitState.RemoteURLs) == 0 {
		ops = append(ops, operation.Operation{ID: "op_git_remote", CapabilityID: "git.remote.configure", ProviderID: "git", Summary: "Configure origin remote", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "configure_remote", Target: remote}}, Inputs: map[string]any{"project_root": root, "remote_name": remote, "url": remoteURL}})
	} else if gitState.RemoteURLs[0] != remoteURL {
		ops = append(ops, operation.Operation{ID: "op_git_remote_protocol", CapabilityID: "git.remote.update", ProviderID: "git", Summary: "Use the authenticated GitHub protocol for the verified repository remote", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "update_equivalent_remote_protocol", Target: remote}}, Inputs: map[string]any{"project_root": root, "remote_name": remote, "url": remoteURL}})
	}
	ops = append(ops, operation.Operation{ID: "op_git_push", CapabilityID: "git.branch.push", ProviderID: "git", Summary: "Push initial branch", Risk: safety.RiskRemoteWrite, Effects: []operation.Effect{{Kind: "push_branch", Target: remote + "/" + branch}}, Inputs: map[string]any{"project_root": root, "remote_name": remote, "branch": branch, "set_upstream": true}})
	return ops
}

func selectPaths(root string, requested []string) (RepoSelection, error) {
	mode := "explicit"
	if len(requested) == 0 {
		mode = "disclosed_all"
	}
	var candidates []string
	if len(requested) == 0 {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if rel == "." || rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				if entry.IsDir() && rel == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			candidates = append(candidates, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return RepoSelection{}, Wrap(CodeProjectNotFound, "could not enumerate project files", err)
		}
	} else {
		for _, raw := range requested {
			clean := filepath.Clean(raw)
			if filepath.IsAbs(clean) {
				return RepoSelection{}, &Error{Code: CodePlanInvalid, Message: "selected paths must be relative to the project root"}
			}
			full := filepath.Join(root, clean)
			rel, err := filepath.Rel(root, full)
			if err != nil || strings.HasPrefix(rel, "..") {
				return RepoSelection{}, &Error{Code: CodePlanInvalid, Message: "selected path escapes the project root"}
			}
			info, err := os.Stat(full)
			if err != nil {
				return RepoSelection{}, &Error{Code: CodePlanInvalid, Message: "selected path does not exist: " + raw, Cause: err}
			}
			if info.IsDir() {
				if err := filepath.WalkDir(full, func(path string, entry os.DirEntry, err error) error {
					if err == nil && !entry.IsDir() {
						rel, _ := filepath.Rel(root, path)
						candidates = append(candidates, filepath.ToSlash(rel))
					}
					return nil
				}); err != nil {
					return RepoSelection{}, Wrap(CodeProjectNotFound, "could not enumerate selected directory", err)
				}
			} else {
				candidates = append(candidates, filepath.ToSlash(rel))
			}
		}
	}
	var selected, ignoredPaths, warnings []string
	for _, path := range uniqueSorted(candidates) {
		if isIgnoredPath(root, path) {
			ignoredPaths = append(ignoredPaths, path)
			continue
		}
		selected = append(selected, path)
		if warning := secretWarning(root, path); warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return RepoSelection{Mode: mode, Paths: selected, IgnoredPaths: ignoredPaths, Warnings: warnings}, nil
}

func secretWarning(root, rel string) string {
	lower := strings.ToLower(filepath.Base(rel))
	if strings.Contains(lower, ".env") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "credential") || strings.Contains(lower, "id_rsa") || strings.HasSuffix(lower, ".pem") {
		return "Potential secret-like file selected: " + rel
	}
	file, err := os.Open(filepath.Join(root, rel))
	if err != nil {
		return ""
	}
	defer file.Close()
	buffer := make([]byte, 8192)
	n, _ := file.Read(buffer)
	content := strings.ToLower(string(buffer[:n]))
	if strings.Contains(content, "api_key=") || strings.Contains(content, "secret=") || strings.Contains(content, "token=") || strings.Contains(content, "private key") {
		return "Potential secret-like content selected: " + rel
	}
	return ""
}

func isIgnoredPath(root, rel string) bool {
	content, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	ignored := false
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negated := strings.HasPrefix(line, "!")
		line = strings.TrimPrefix(line, "!")
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		directory := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		matched := ignorePatternMatches(line, rel, anchored, directory)
		if matched {
			ignored = !negated
		}
	}
	return ignored
}

func ignorePatternMatches(pattern, rel string, anchored, directory bool) bool {
	if anchored || strings.Contains(pattern, "/") {
		if directory {
			return rel == pattern || strings.HasPrefix(rel, pattern+"/")
		}
		matched, _ := filepath.Match(pattern, rel)
		return matched
	}
	for _, part := range strings.Split(rel, "/") {
		matched, _ := filepath.Match(pattern, part)
		if matched {
			return true
		}
	}
	return false
}

func ignoredTrackedPaths(root string, tracked []string) []string {
	var ignored []string
	for _, path := range tracked {
		if isIgnoredPath(root, path) {
			ignored = append(ignored, path)
		}
	}
	return uniqueSorted(ignored)
}

func desiredRemoteURL(protocol, owner, name string, repo ghadapter.RepoResult) string {
	if protocol == "https" {
		return gitadapter.HTTPSRemoteURL(owner, name)
	}
	if repo.SSHURL != "" {
		return repo.SSHURL
	}
	return gitadapter.SafeRemoteURL(owner, name)
}

func sameGitHubRepository(left, right string) bool {
	leftTarget, leftOK := canonicalGitHubRepository(left)
	rightTarget, rightOK := canonicalGitHubRepository(right)
	return leftOK && rightOK && leftTarget == rightTarget
}

func canonicalGitHubRepository(value string) (string, bool) {
	value = strings.TrimSpace(value)
	var target string
	switch {
	case strings.HasPrefix(value, "git@github.com:"):
		target = strings.TrimPrefix(value, "git@github.com:")
	case strings.HasPrefix(value, "ssh://git@github.com/"):
		target = strings.TrimPrefix(value, "ssh://git@github.com/")
	case strings.HasPrefix(value, "https://github.com/"):
		target = strings.TrimPrefix(value, "https://github.com/")
	default:
		return "", false
	}
	target = strings.TrimSuffix(strings.TrimSuffix(target, "/"), ".git")
	parts := strings.Split(target, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return strings.ToLower(target), true
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func validateRepoInputs(root, name, owner, branch, remote, visibility, message string) error {
	for label, value := range map[string]string{"repository name": name, "branch": branch, "remote": remote} {
		if value == "" || strings.HasPrefix(value, "-") || !safeName.MatchString(value) {
			return &Error{Code: CodePlanInvalid, Message: label + " is invalid"}
		}
	}
	if owner != "" && !safeName.MatchString(owner) {
		return &Error{Code: CodePlanInvalid, Message: "GitHub owner is invalid"}
	}
	if visibility != "private" && visibility != "public" {
		return &Error{Code: CodePlanInvalid, Message: "visibility must be private or public"}
	}
	if strings.TrimSpace(message) == "" {
		return &Error{Code: CodePlanInvalid, Message: "commit message is required"}
	}
	if _, err := os.Stat(root); err != nil {
		return Wrap(CodeProjectNotFound, "project root does not exist", err)
	}
	return nil
}

func validateInitialRepair(gitState gitadapter.InspectRepoResult, ghRepo ghadapter.RepoResult) error {
	if !gitState.IsRepository || !gitState.HasCommits || gitState.CommitCount != 1 {
		return &Error{Code: CodePreconditionFailed, Message: "initial repair requires exactly one local commit"}
	}
	if gitState.Upstream != "" {
		return &Error{Code: CodePreconditionFailed, Message: "initial repair is unavailable after an upstream is configured"}
	}
	if !ghRepo.Exists {
		return &Error{Code: CodePreconditionFailed, Message: "initial repair requires the expected existing GitHub repository"}
	}
	if ghRepo.DefaultBranch != "" {
		return &Error{Code: CodePreconditionFailed, Message: "initial repair is unavailable after GitHub reports a published default branch"}
	}
	if gitState.WorkingTreeStatus != "dirty" {
		return &Error{Code: CodePreconditionFailed, Message: "initial repair requires disclosed local changes"}
	}
	return nil
}

func validateRedactionPath(root, raw string) (string, error) {
	clean := filepath.Clean(raw)
	if raw == "" || filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", &Error{Code: CodePlanInvalid, Message: "redaction path must be a relative file inside the project root"}
	}
	full := filepath.Join(root, clean)
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", &Error{Code: CodePreconditionFailed, Message: "redaction path must be an existing local file", Cause: err}
	}
	return filepath.ToSlash(clean), nil
}

func validateRedactionState(root, path, remote string, state gitadapter.InspectRepoResult) error {
	if !state.IsRepository || state.IsDetachedHead || state.CommitCount != 1 || state.HeadBranch == "" {
		return &Error{Code: CodePreconditionFailed, Message: "initial redaction requires exactly one commit on an attached branch"}
	}
	if state.Upstream != remote+"/"+state.HeadBranch || state.UpstreamCommit == "" || state.UpstreamCommit != state.HeadCommit {
		return &Error{Code: CodePreconditionFailed, Message: "local HEAD must exactly match its configured upstream before redaction", Hint: "Refresh and inspect repository state; do not rewrite a branch that changed remotely."}
	}
	if !containsString(state.TrackedPaths, path) {
		return &Error{Code: CodePreconditionFailed, Message: "redaction path is not tracked in the initial commit"}
	}
	if !isIgnoredPath(root, path) {
		return &Error{Code: CodePreconditionFailed, Message: "redaction path must be covered by .gitignore before mutation"}
	}
	return nil
}

func redactionSelection(root, target string, state gitadapter.InspectRepoResult) RepoSelection {
	selection := RepoSelection{Mode: "disclosed_changed_files", UntrackPaths: []string{target}}
	candidates := append(append([]string{}, state.TrackedChanges...), state.UntrackedPaths...)
	for _, path := range uniqueSorted(candidates) {
		if path == target {
			continue
		}
		if isIgnoredPath(root, path) {
			selection.IgnoredPaths = append(selection.IgnoredPaths, path)
			continue
		}
		selection.Paths = append(selection.Paths, path)
		if warning := secretWarning(root, path); warning != "" {
			selection.Warnings = append(selection.Warnings, warning)
		}
	}
	return selection
}

func fileDigests(root string, paths []string) (map[string]string, error) {
	digests := make(map[string]string, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			if os.IsNotExist(err) {
				digests[path] = "deleted"
				continue
			}
			return nil, err
		}
		sum := sha256.Sum256(content)
		digests[path] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return digests, nil
}

func validateRedactionFileDigests(plan operation.Plan) error {
	for _, op := range plan.Operations {
		if op.CapabilityID != "git.index.stage" {
			continue
		}
		expected := stringMapInput(op, "content_digests")
		current, err := fileDigests(plan.ProjectRoot, stringSliceInput(op, "paths"))
		if err != nil || !equalStringMaps(expected, current) {
			return &Error{Code: CodePreconditionFailed, Message: "a disclosed remediation file changed after planning", Cause: err, Hint: "No mutation was performed. Build and review a fresh redaction plan."}
		}
	}
	return nil
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type confirmationReader struct {
	scanner *bufio.Scanner
	writer  io.Writer
}

func newConfirmationReader(reader io.Reader, writer io.Writer) confirmationReader {
	if writer == nil {
		writer = os.Stdout
	}
	if reader == nil {
		return confirmationReader{writer: writer}
	}
	return confirmationReader{scanner: bufio.NewScanner(reader), writer: writer}
}

func (c confirmationReader) confirm(label, digest, expected string) (bool, error) {
	if c.scanner == nil {
		return false, nil
	}
	fmt.Fprintf(c.writer, "%s for plan %s? Type %s to continue: ", label, digest, expected)
	if !c.scanner.Scan() {
		return false, c.scanner.Err()
	}
	answer := strings.TrimSpace(strings.ToLower(c.scanner.Text()))
	return answer == expected, nil
}

func cancelled(response RepoResponse, hint string) (RepoResponse, error) {
	response.Status = operation.StatusCancelled
	response.Result = operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusCancelled, RecoveryHints: []string{hint}}
	return response, &Error{Code: CodeConfirmationDeclined, Message: "confirmation declined", Hint: hint}
}

func classifyProcess(provider, action string, err error) error {
	var runErr *devprocess.RunError
	if errors.As(err, &runErr) {
		switch runErr.Kind {
		case devprocess.ErrorMissing:
			return &Error{Code: CodeToolNotFound, Message: provider + " executable was not found", Provider: provider, Cause: err}
		case devprocess.ErrorTimeout:
			return &Error{Code: CodeProcessTimeout, Message: action + " timed out", Provider: provider, Cause: err, Retryable: true}
		}
	}
	return &Error{Code: CodeProcessFailed, Message: action + " failed", Provider: provider, Cause: err}
}

func (s RepoService) writeHistory(response RepoResponse, result operation.ExecutionResult, options RepoOptions) error {
	return s.writeHistoryInvocation(response, result, map[string]any{"dry_run": options.DryRun, "yes": options.Yes, "repair_unpushed_initial": options.RepairInitial})
}

func (s RepoService) writeHistoryInvocation(response RepoResponse, result operation.ExecutionResult, invocation map[string]any) error {
	if !s.Config.History.Enabled {
		return nil
	}
	return s.History.Append(history.Record{
		SchemaVersion: 1, ExecutionID: "exec_" + s.now().Format("20060102150405"), PlanID: response.Plan.ID,
		PlanDigest: response.Plan.Digest, StartedAt: response.Plan.CreatedAt, FinishedAt: s.now(),
		Invocation: invocation, Project: map[string]string{"root": response.Plan.ProjectRoot},
		Status: result.Status, Steps: result.Steps, RecoveryHints: result.RecoveryHints,
	})
}

func (s RepoService) redactionFailure(response RepoResponse, result operation.ExecutionResult, options RedactInitialOptions, step operation.StepResult) (RepoResponse, error) {
	result.Status = operation.StatusPartiallyCompleted
	result.RecoveryHints = append(result.RecoveryHints, step.RecoveryHint)
	response.Result = result
	_ = s.writeHistoryInvocation(response, result, map[string]any{"redact_initial_path": options.Path})
	return response, &Error{Code: CodePartialExecution, Message: step.ErrorMessage, Hint: step.RecoveryHint}
}

func (s RepoService) redactionPostconditionFailure(response RepoResponse, result operation.ExecutionResult, options RedactInitialOptions, message string, cause error) (RepoResponse, error) {
	result.Status = operation.StatusPartiallyCompleted
	hint := "Inspect the local commit, tracked files, and upstream before retrying; the exact lease prevents overwriting a concurrent remote update."
	result.RecoveryHints = append(result.RecoveryHints, hint)
	response.Result = result
	_ = s.writeHistoryInvocation(response, result, map[string]any{"redact_initial_path": options.Path})
	return response, &Error{Code: CodePostconditionFailed, Message: message, Cause: cause, Hint: hint}
}

func (s RepoService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func planRisks(plan operation.Plan) []safety.Risk {
	var risks []safety.Risk
	for _, op := range plan.Operations {
		risks = append(risks, op.Risk)
	}
	return risks
}

func isNarrowInitialRepair(plan operation.Plan) bool {
	found := false
	for _, op := range plan.Operations {
		if op.Risk != safety.RiskDestructive {
			continue
		}
		if op.CapabilityID != "git.commit.amend_initial" || found {
			return false
		}
		found = true
	}
	return found
}

func yesAllowed(cfg config.Config, risk safety.Risk) bool {
	for _, value := range strings.Split(cfg.Safety.AllowYesFor, ",") {
		if strings.TrimSpace(value) == string(risk) {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func stringInput(op operation.Operation, name string) string {
	if value, ok := op.Inputs[name].(string); ok {
		return value
	}
	return ""
}

func stringSliceInput(op operation.Operation, name string) []string {
	switch value := op.Inputs[name].(type) {
	case []string:
		return value
	case []any:
		var result []string
		for _, item := range value {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func stringMapInput(op operation.Operation, name string) map[string]string {
	switch value := op.Inputs[name].(type) {
	case map[string]string:
		return value
	case map[string]any:
		result := make(map[string]string, len(value))
		for key, item := range value {
			if text, ok := item.(string); ok {
				result[key] = text
			}
		}
		return result
	default:
		return nil
	}
}

func recoveryHint(capability string) string {
	switch capability {
	case "git.index.untrack", "git.commit.amend_initial":
		return "Inspect the local index and commit state, then rerun with --repair-unpushed-initial only if the branch remains unpublished."
	case "git.commit.amend_initial_preserve_message":
		return "Inspect the local initial commit and tracked file list before deciding whether the redaction can be retried."
	case "git.remote.update":
		return "Fix the authenticated Git transport. If the initial commit repair is already clean, rerun dvz repo create without --repair-unpushed-initial."
	case "github.repo.create":
		return "Fix GitHub authentication or repository name, then rerun the same dvz repo create command."
	case "git.remote.configure":
		return "Inspect the existing remote and rerun only after the target repository URL is the expected one."
	case "git.branch.push":
		return "Fix authentication or connectivity, then rerun without --repair-unpushed-initial if the repaired commit is already clean; Devtize will verify current state first."
	case "git.branch.force_push_with_lease":
		return "The remote was not replaced. Inspect its current branch SHA; retry only after confirming no concurrent update would be lost."
	default:
		return "Fix the reported issue, then rerun the same dvz repo create command."
	}
}

func partialStatus(completed bool) operation.Status {
	if completed {
		return operation.StatusPartiallyCompleted
	}
	return operation.StatusFailed
}

func alreadyRan(steps []operation.StepResult, id string) bool {
	for _, step := range steps {
		if step.OperationID == id {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
