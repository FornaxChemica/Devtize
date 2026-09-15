package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/safety"
)

type CommitOptions struct {
	Paths        []string
	Message      string
	Conventional bool
	DryRun       bool
	PlanJSON     bool
}

type CommitSelection struct {
	Mode     string   `json:"mode"`
	Paths    []string `json:"paths"`
	Warnings []string `json:"warnings,omitempty"`
}

type CommitResponse struct {
	SchemaVersion int                          `json:"schema_version"`
	Status        operation.Status             `json:"status"`
	Plan          operation.Plan               `json:"plan"`
	Selection     CommitSelection              `json:"selection"`
	Git           gitadapter.InspectRepoResult `json:"git"`
	Result        operation.ExecutionResult    `json:"result,omitempty"`
}

type CommitGitPort interface {
	InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error)
	InspectChanges(context.Context, string) (gitadapter.ChangeSet, error)
	InspectHeadMessage(context.Context, string) (string, error)
	Stage(context.Context, gitadapter.StageInput) error
	CreateCommit(context.Context, gitadapter.CommitInput) error
}

type CommitService struct {
	WorkingDir string
	Config     config.Config
	Runner     interface {
		Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
	}
	Git     CommitGitPort
	History history.Store
	Now     func() time.Time
	Output  io.Writer
}

var conventionalCommitPattern = regexp.MustCompile(`^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(\([A-Za-z0-9._/-]+\))?!?: [^[:space:]].*$`)

func ValidateCommitMessage(message string, conventional bool) error {
	if strings.TrimSpace(message) == "" || strings.ContainsAny(message, "\r\n") {
		return &Error{Code: CodePlanInvalid, Message: "commit message must be a non-empty single line"}
	}
	if conventional && !conventionalCommitPattern.MatchString(message) {
		return &Error{
			Code: CodePlanInvalid, Message: "commit message is not a valid Conventional Commit",
			Hint: "Use <type>[optional scope][!]: <description>, or pass --conventional=false.",
		}
	}
	return nil
}

func (s CommitService) Plan(ctx context.Context, options CommitOptions) (CommitResponse, error) {
	s = s.withDefaults()
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return CommitResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	if err := ValidateCommitMessage(options.Message, options.Conventional); err != nil {
		return CommitResponse{}, err
	}
	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: root})
	if err != nil {
		return CommitResponse{}, classifyProcess("git", "inspect repository", err)
	}
	if !state.IsRepository || !state.HasCommits {
		return CommitResponse{}, &Error{Code: CodeProjectNotFound, Message: "commit requires an existing Git repository with a commit"}
	}
	if state.IsDetachedHead || state.HeadBranch == "" {
		return CommitResponse{}, &Error{Code: CodePreconditionFailed, Message: "commit requires an attached branch"}
	}
	changes, err := s.Git.InspectChanges(ctx, root)
	if err != nil {
		return CommitResponse{}, classifyProcess("git", "inspect changed paths", err)
	}
	if len(changes.Staged) > 0 {
		return CommitResponse{}, &Error{Code: CodePreconditionFailed, Message: "commit requires a clean index", Hint: "Commit or unstage the existing staged paths before building a Devtize commit plan."}
	}
	selection, err := selectCommitPaths(root, options.Paths, changes)
	if err != nil {
		return CommitResponse{}, err
	}
	digests, err := fileDigests(root, selection.Paths)
	if err != nil {
		return CommitResponse{}, Wrap(CodePlanInvalid, "selected files could not be digested", err)
	}
	operations := []operation.Operation{
		{
			ID: "op_commit_stage", CapabilityID: "git.index.stage", ProviderID: "git", Summary: "Stage disclosed changed paths", Risk: safety.RiskLocalWrite,
			Effects: []operation.Effect{{Kind: "stage_paths", Target: root}},
			Inputs:  map[string]any{"project_root": root, "paths": selection.Paths, "content_digests": digests, "expected_head": state.HeadCommit, "branch": state.HeadBranch, "selection_mode": selection.Mode},
		},
		{
			ID: "op_commit_create", CapabilityID: "git.commit.create", ProviderID: "git", Summary: "Create commit", Risk: safety.RiskLocalWrite,
			Effects: []operation.Effect{{Kind: "create_commit", Target: state.HeadBranch}},
			Inputs:  map[string]any{"project_root": root, "message": options.Message, "expected_head": state.HeadCommit, "branch": state.HeadBranch},
		},
	}
	now := s.now()
	plan := operation.Plan{
		SchemaVersion: 1, ID: "plan_commit_" + now.Format("20060102150405"), Intent: "Create a Git commit from disclosed changed paths",
		CreatedAt: now, ProjectRoot: root, DryRun: options.DryRun || options.PlanJSON, Operations: operations,
	}
	plan, err = plan.WithDigest()
	if err != nil {
		return CommitResponse{}, Wrap(CodePlanInvalid, "commit plan could not be digested", err)
	}
	status := operation.StatusProposed
	if plan.DryRun {
		status = operation.StatusValidated
	}
	return CommitResponse{SchemaVersion: 1, Status: status, Plan: plan, Selection: selection, Git: state}, nil
}

func (s CommitService) ExecutePlanned(ctx context.Context, options CommitOptions, prompts io.Reader, response CommitResponse) (CommitResponse, error) {
	s = s.withDefaults()
	digest, err := operation.Digest(response.Plan)
	if err != nil || digest != response.Plan.Digest {
		return response, &Error{Code: CodePlanInvalid, Message: "authorized commit plan digest is invalid", Cause: err}
	}
	if response.Plan.DryRun || options.DryRun || options.PlanJSON {
		return response, nil
	}
	answers := newConfirmationReader(prompts, s.Output)
	if len(response.Selection.Warnings) > 0 {
		ok, confirmErr := answers.confirm("Potential secret warning", response.Plan.Digest, "yes")
		if confirmErr != nil || !ok {
			return cancelCommit(response, "secret warning was not confirmed")
		}
	}
	ok, confirmErr := answers.confirm("Create this commit", response.Plan.Digest, "commit")
	if confirmErr != nil || !ok {
		return cancelCommit(response, "commit confirmation was declined")
	}
	if err := s.validateCurrentPlan(ctx, response); err != nil {
		return response, err
	}
	result := operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusRunning}
	stageOp := response.Plan.Operations[0]
	stageStep := operation.StepResult{OperationID: stageOp.ID, CapabilityID: stageOp.CapabilityID, Summary: stageOp.Summary, Status: operation.StatusRunning, StartedAt: s.now()}
	err = s.Git.Stage(ctx, gitadapter.StageInput{ProjectRoot: response.Plan.ProjectRoot, Paths: response.Selection.Paths})
	stageStep.FinishedAt = s.now()
	if err != nil {
		stageStep.Status = operation.StatusFailed
		stageStep.ErrorCode = string(CodeProcessFailed)
		stageStep.ErrorMessage = "Stage disclosed changed paths failed"
		stageStep.RecoveryHint = "No commit was created. Fix the reported Git error and build a fresh plan."
		result.Status = operation.StatusFailed
		result.Steps = append(result.Steps, stageStep)
		result.RecoveryHints = []string{stageStep.RecoveryHint}
		response.Result = result
		_ = s.writeHistory(response, result, options)
		return response, &Error{Code: CodeProcessFailed, Message: stageStep.ErrorMessage, Cause: err, Hint: stageStep.RecoveryHint}
	}
	stageStep.Status = operation.StatusSucceeded
	result.Steps = append(result.Steps, stageStep)

	commitOp := response.Plan.Operations[1]
	commitStep := operation.StepResult{OperationID: commitOp.ID, CapabilityID: commitOp.CapabilityID, Summary: commitOp.Summary, Status: operation.StatusRunning, StartedAt: s.now()}
	err = s.Git.CreateCommit(ctx, gitadapter.CommitInput{ProjectRoot: response.Plan.ProjectRoot, Message: stringInput(commitOp, "message")})
	commitStep.FinishedAt = s.now()
	if err != nil {
		commitStep.Status = operation.StatusFailed
		commitStep.ErrorCode = string(CodeProcessFailed)
		commitStep.ErrorMessage = "Create commit failed"
		commitStep.RecoveryHint = "The disclosed paths may remain staged. Inspect the index, fix the Git error, and rerun the same dvz commit command."
		result.Status = operation.StatusPartiallyCompleted
		result.Steps = append(result.Steps, commitStep)
		result.RecoveryHints = []string{commitStep.RecoveryHint}
		response.Result = result
		_ = s.writeHistory(response, result, options)
		return response, &Error{Code: CodePartialExecution, Message: commitStep.ErrorMessage, Cause: err, Hint: commitStep.RecoveryHint}
	}
	commitStep.Status = operation.StatusSucceeded
	result.Steps = append(result.Steps, commitStep)

	state, inspectErr := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot})
	message, messageErr := s.Git.InspectHeadMessage(ctx, response.Plan.ProjectRoot)
	changes, changesErr := s.Git.InspectChanges(ctx, response.Plan.ProjectRoot)
	if inspectErr != nil || messageErr != nil || changesErr != nil || state.HeadCommit == response.Git.HeadCommit || state.CommitCount != response.Git.CommitCount+1 || message != stringInput(commitOp, "message") || intersects(response.Selection.Paths, allChangedPaths(changes)) {
		result.Status = operation.StatusPartiallyCompleted
		hint := "Inspect HEAD, the commit message, and the selected paths before retrying; the commit may already exist."
		result.RecoveryHints = []string{hint}
		response.Result = result
		_ = s.writeHistory(response, result, options)
		return response, &Error{Code: CodePostconditionFailed, Message: "commit postconditions were not satisfied", Cause: errors.Join(inspectErr, messageErr, changesErr), Hint: hint}
	}
	result.Status = operation.StatusSucceeded
	response.Status = operation.StatusSucceeded
	response.Git = state
	response.Result = result
	if err := s.writeHistory(response, result, options); err != nil {
		return response, &Error{Code: CodeHistoryWriteFailed, Message: "history could not be written", Cause: err}
	}
	return response, nil
}

func (s CommitService) validateCurrentPlan(ctx context.Context, response CommitResponse) error {
	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: response.Plan.ProjectRoot})
	if err != nil || state.HeadCommit != response.Git.HeadCommit || state.HeadBranch != response.Git.HeadBranch || state.IsDetachedHead {
		return &Error{Code: CodePreconditionFailed, Message: "repository HEAD or branch changed after planning", Cause: err, Hint: "No mutation was performed. Build and review a fresh commit plan."}
	}
	changes, err := s.Git.InspectChanges(ctx, response.Plan.ProjectRoot)
	if err != nil {
		return classifyProcess("git", "reinspect changed paths", err)
	}
	if len(changes.Staged) > 0 || !containsAll(allChangedPaths(changes), response.Selection.Paths) {
		return &Error{Code: CodePreconditionFailed, Message: "selected path state changed after planning", Hint: "No mutation was performed. Build and review a fresh commit plan."}
	}
	if response.Selection.Mode == "disclosed_all_changes" && !equalStringSlices(uniqueSorted(allChangedPaths(changes)), response.Selection.Paths) {
		return &Error{Code: CodePreconditionFailed, Message: "repository changes differ from the disclosed all-files plan", Hint: "No mutation was performed. Build and review a fresh commit plan."}
	}
	expected := stringMapInput(response.Plan.Operations[0], "content_digests")
	current, err := fileDigests(response.Plan.ProjectRoot, response.Selection.Paths)
	if err != nil || !equalStringMaps(expected, current) {
		return &Error{Code: CodePreconditionFailed, Message: "selected file content changed after planning", Cause: err, Hint: "No mutation was performed. Build and review a fresh commit plan."}
	}
	return nil
}

func selectCommitPaths(root string, requested []string, changes gitadapter.ChangeSet) (CommitSelection, error) {
	changed := uniqueSorted(allChangedPaths(changes))
	if len(changed) == 0 {
		return CommitSelection{}, &Error{Code: CodePreconditionFailed, Message: "no changed files are available to commit"}
	}
	mode := "explicit_paths"
	var selected []string
	if len(requested) == 0 {
		mode = "disclosed_all_changes"
		selected = changed
	} else {
		changedSet := stringSet(changed)
		ignoredSet := stringSet(changes.Ignored)
		for _, raw := range requested {
			path, err := normalizeCommitPath(root, raw)
			if err != nil {
				return CommitSelection{}, err
			}
			if ignoredSet[path] || isIgnoredPath(root, path) {
				return CommitSelection{}, &Error{Code: CodePreconditionFailed, Message: "selected path is ignored: " + path}
			}
			if !changedSet[path] {
				return CommitSelection{}, &Error{Code: CodePreconditionFailed, Message: "selected path is not currently changed: " + path}
			}
			selected = append(selected, path)
		}
		selected = uniqueSorted(selected)
	}
	selection := CommitSelection{Mode: mode, Paths: selected}
	for _, path := range selected {
		if warning := secretWarning(root, path); warning != "" {
			selection.Warnings = append(selection.Warnings, warning)
		}
	}
	return selection, nil
}

func normalizeCommitPath(root, raw string) (string, error) {
	clean := filepath.Clean(raw)
	if raw == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", &Error{Code: CodePlanInvalid, Message: "commit paths must be relative paths inside the project root"}
	}
	rel := filepath.ToSlash(clean)
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return "", &Error{Code: CodePlanInvalid, Message: "Git metadata cannot be selected for commit"}
	}
	full := filepath.Join(root, clean)
	resolved, err := filepath.Abs(full)
	if err != nil || (resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator))) {
		return "", &Error{Code: CodePlanInvalid, Message: "commit path escapes the project root", Cause: err}
	}
	return rel, nil
}

func allChangedPaths(changes gitadapter.ChangeSet) []string {
	paths := append([]string{}, changes.Unstaged...)
	paths = append(paths, changes.Untracked...)
	return uniqueSorted(paths)
}

func containsAll(values, wanted []string) bool {
	set := stringSet(values)
	for _, value := range wanted {
		if !set[value] {
			return false
		}
	}
	return true
}

func intersects(left, right []string) bool {
	set := stringSet(left)
	for _, value := range right {
		if set[value] {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func cancelCommit(response CommitResponse, hint string) (CommitResponse, error) {
	response.Status = operation.StatusCancelled
	response.Result = operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusCancelled, RecoveryHints: []string{hint}}
	return response, &Error{Code: CodeConfirmationDeclined, Message: "confirmation declined", Hint: hint}
}

func (s CommitService) writeHistory(response CommitResponse, result operation.ExecutionResult, options CommitOptions) error {
	if !s.Config.History.Enabled {
		return nil
	}
	return s.History.Append(history.Record{
		SchemaVersion: 1, ExecutionID: "exec_" + s.now().Format("20060102150405"), PlanID: response.Plan.ID,
		PlanDigest: response.Plan.Digest, StartedAt: response.Plan.CreatedAt, FinishedAt: s.now(),
		Invocation: map[string]any{"workflow": "commit", "selection_mode": response.Selection.Mode, "conventional": options.Conventional},
		Project:    map[string]string{"root": response.Plan.ProjectRoot}, Status: result.Status, Steps: result.Steps, RecoveryHints: result.RecoveryHints,
	})
}

func (s CommitService) withDefaults() CommitService {
	if s.Git == nil {
		s.Git = gitadapter.New(s.Runner)
	}
	return s
}

func (s CommitService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
