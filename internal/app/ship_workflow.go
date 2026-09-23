package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	golangadapter "github.com/FornaxChemica/devtize/internal/adapters/golang"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/registry"
)

type ShipWorkflowOptions struct {
	Paths        []string
	Message      string
	Conventional bool
	Checks       []string
	Remote       string
	DryRun       bool
	PlanJSON     bool
}

type ShipRemoteBoundary struct {
	RemoteName   string `json:"remote_name"`
	RemoteURL    string `json:"remote_url"`
	Branch       string `json:"branch"`
	RemoteCommit string `json:"remote_commit"`
	PushDeferred bool   `json:"push_deferred"`
}

type ShipWorkflowResponse struct {
	SchemaVersion int                         `json:"schema_version"`
	WorkflowID    string                      `json:"workflow_id"`
	Mode          string                      `json:"mode"`
	Status        operation.Status            `json:"status"`
	Local         CommitResponse              `json:"local"`
	Checks        []golangadapter.CheckResult `json:"checks"`
	Remote        ShipRemoteBoundary          `json:"remote_boundary"`
	Push          *ShipResponse               `json:"push,omitempty"`
	RecoveryHints []string                    `json:"recovery_hints,omitempty"`
}

type ShipWorkflowGitPort interface {
	ListVisibleFiles(context.Context, string) ([]string, error)
}

type ShipWorkflowCheckPort interface {
	Detect(context.Context, string) (golangadapter.Toolchain, error)
	Run(context.Context, golangadapter.CheckInput) (golangadapter.CheckResult, error)
}

type ShipWorkflowProgress interface {
	CheckStarted(capabilityID string, index, total int)
	CheckFinished(golangadapter.CheckResult)
}

type ShipWorkflowService struct {
	WorkingDir string
	Config     config.Config
	Runner     interface {
		Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
	}
	Git      ShipWorkflowGitPort
	Checks   ShipWorkflowCheckPort
	History  history.Store
	Now      func() time.Time
	Output   io.Writer
	Progress ShipWorkflowProgress
}

func (s ShipWorkflowService) Plan(ctx context.Context, options ShipWorkflowOptions) (ShipWorkflowResponse, error) {
	s = s.withDefaults()
	if err := registry.ValidateCheckIDs(options.Checks); err != nil {
		return ShipWorkflowResponse{}, &Error{Code: CodeCapabilityNotFound, Message: err.Error()}
	}
	workflowID := "workflow_ship_" + s.now().Format("20060102150405")
	commitOptions := CommitOptions{
		Paths: options.Paths, Message: options.Message, Conventional: options.Conventional,
		DryRun: options.DryRun, PlanJSON: options.PlanJSON, WorkflowID: workflowID, Workflow: "ship.local",
	}
	commitResponse, err := s.commitService().Plan(ctx, commitOptions)
	if err != nil {
		return ShipWorkflowResponse{}, err
	}
	pushService := s.pushService()
	boundary, err := pushService.Plan(ctx, ShipOptions{Remote: options.Remote, DryRun: true, WorkflowID: workflowID})
	if err != nil {
		return ShipWorkflowResponse{}, err
	}

	var toolchain golangadapter.Toolchain
	var goFiles []string
	if len(options.Checks) > 0 {
		toolchain, err = s.Checks.Detect(ctx, commitResponse.Plan.ProjectRoot)
		if err != nil {
			return ShipWorkflowResponse{}, classifyCheckError("detect Go toolchain", err)
		}
	}
	if containsCheck(options.Checks, "go.format.check") {
		goFiles, err = s.visibleGoFiles(ctx, commitResponse.Plan.ProjectRoot)
		if err != nil {
			return ShipWorkflowResponse{}, err
		}
	}

	checkOperations := make([]operation.Operation, 0, len(options.Checks))
	for index, id := range options.Checks {
		capability, _ := registry.CheckByID(id)
		inputs := map[string]any{
			"project_root": commitResponse.Plan.ProjectRoot, "go_path": toolchain.GoPath,
			"gofmt_path": toolchain.GofmtPath, "go_version": toolchain.GoVersion,
		}
		if id == "go.format.check" {
			inputs["go_file_count"] = len(goFiles)
			inputs["go_files_digest"] = stringDigest(goFiles)
		}
		checkOperations = append(checkOperations, operation.Operation{
			ID: fmt.Sprintf("op_ship_check_%02d", index+1), CapabilityID: id, ProviderID: capability.ProviderID,
			Summary: capability.Summary, Risk: capability.Risk,
			Effects: []operation.Effect{{Kind: "project_check", Target: capability.Effect}}, Inputs: inputs,
		})
	}
	commitOp, ok := operationByCapability(commitResponse.Plan, "git.commit.create")
	if !ok {
		return ShipWorkflowResponse{}, &Error{Code: CodePlanInvalid, Message: "ship local plan is missing commit creation"}
	}
	for index := range commitResponse.Plan.Operations {
		if commitResponse.Plan.Operations[index].ID == commitOp.ID {
			commitResponse.Plan.Operations[index].Inputs["remote_name"] = boundary.RemoteName
			commitResponse.Plan.Operations[index].Inputs["remote_url"] = boundary.RemoteURL
			commitResponse.Plan.Operations[index].Inputs["expected_remote_commit"] = boundary.RemoteCommit
		}
	}
	commitResponse.Plan.ID = "plan_ship_local_" + s.now().Format("20060102150405")
	commitResponse.Plan.Intent = "Run reviewed checks and create a disclosed commit before a separately authorized push"
	commitResponse.Plan.Operations = append(checkOperations, commitResponse.Plan.Operations...)
	commitResponse.Plan, err = commitResponse.Plan.WithDigest()
	if err != nil {
		return ShipWorkflowResponse{}, Wrap(CodePlanInvalid, "ship local plan could not be digested", err)
	}
	status := operation.StatusProposed
	if commitResponse.Plan.DryRun {
		status = operation.StatusValidated
	}
	commitResponse.Status = status
	return ShipWorkflowResponse{
		SchemaVersion: 1, WorkflowID: workflowID, Mode: "compose", Status: status, Local: commitResponse,
		Checks: []golangadapter.CheckResult{},
		Remote: ShipRemoteBoundary{
			RemoteName: boundary.RemoteName, RemoteURL: boundary.RemoteURL, Branch: boundary.Git.HeadBranch,
			RemoteCommit: boundary.RemoteCommit, PushDeferred: true,
		},
	}, nil
}

func (s ShipWorkflowService) ExecuteLocal(ctx context.Context, options ShipWorkflowOptions, prompts io.Reader, response ShipWorkflowResponse) (ShipWorkflowResponse, error) {
	s = s.withDefaults()
	digest, err := operation.Digest(response.Local.Plan)
	if err != nil || digest != response.Local.Plan.Digest || !workflowBoundaryMatches(response) {
		return response, &Error{Code: CodePlanInvalid, Message: "authorized composed ship plan is invalid", Cause: err}
	}
	if response.Local.Plan.DryRun || options.DryRun || options.PlanJSON {
		return response, nil
	}
	answers := newConfirmationReader(prompts, s.Output)
	if len(response.Local.Selection.Warnings) > 0 {
		ok, confirmErr := answers.confirm("Potential secret warning", response.Local.Plan.Digest, "yes")
		if confirmErr != nil || !ok {
			response.Local, err = cancelCommit(response.Local, "secret warning was not confirmed")
			response.Status = operation.StatusCancelled
			return response, err
		}
	}
	ok, confirmErr := answers.confirm("Run reviewed checks and create this commit", response.Local.Plan.Digest, "commit")
	if confirmErr != nil || !ok {
		response.Local, err = cancelCommit(response.Local, "local ship confirmation was declined")
		response.Status = operation.StatusCancelled
		return response, err
	}

	goFiles, err := s.visibleGoFilesIfPlanned(ctx, response.Local.Plan)
	if err != nil {
		return response, err
	}
	toolchain := toolchainFromPlan(response.Local.Plan)
	plannedChecks := checkOperations(response.Local.Plan)
	steps := make([]operation.StepResult, 0, len(plannedChecks))
	for index, op := range plannedChecks {
		if s.Progress != nil {
			s.Progress.CheckStarted(op.CapabilityID, index+1, len(plannedChecks))
		}
		started := s.now()
		checkResult, checkErr := s.Checks.Run(ctx, golangadapter.CheckInput{
			CapabilityID: op.CapabilityID, ProjectRoot: response.Local.Plan.ProjectRoot, GoFiles: goFiles, Toolchain: toolchain,
		})
		checkResult.Diagnostic = config.Redact(checkResult.Diagnostic)
		response.Checks = append(response.Checks, checkResult)
		if s.Progress != nil {
			s.Progress.CheckFinished(checkResult)
		}
		step := operation.StepResult{OperationID: op.ID, CapabilityID: op.CapabilityID, Summary: op.Summary, StartedAt: started, FinishedAt: s.now()}
		if checkErr != nil {
			step.Status = operation.StatusFailed
			step.ErrorCode = string(CodeCheckFailed)
			step.ErrorMessage = "Project check failed"
			step.RecoveryHint = "No files were staged and no commit or push was attempted. Fix the check and rerun the same dvz ship command."
			steps = append(steps, step)
			result := operation.ExecutionResult{
				SchemaVersion: 1, PlanID: response.Local.Plan.ID, PlanDigest: response.Local.Plan.Digest,
				Status: operation.StatusFailed, Steps: steps, RecoveryHints: []string{step.RecoveryHint},
			}
			response.Status = operation.StatusFailed
			response.Local.Status = operation.StatusFailed
			response.Local.Result = result
			response.RecoveryHints = result.RecoveryHints
			commitOptions := workflowCommitOptions(options, response.WorkflowID)
			_ = s.commitService().writeHistory(response.Local, result, commitOptions)
			classified := classifyCheckError(op.CapabilityID, checkErr)
			classified.Hint = step.RecoveryHint
			return response, classified
		}
		step.Status = operation.StatusSucceeded
		steps = append(steps, step)
	}
	if err := s.verifyVisibleGoFiles(ctx, response.Local.Plan); err != nil {
		return response, err
	}
	commitService := s.commitService().withDefaults()
	if err := commitService.validateCurrentPlan(ctx, response.Local); err != nil {
		return response, err
	}
	response.Local, err = commitService.executeValidated(ctx, workflowCommitOptions(options, response.WorkflowID), response.Local, steps)
	response.Status = response.Local.Status
	return response, err
}

func (s ShipWorkflowService) PlanPush(ctx context.Context, _ ShipWorkflowOptions, response ShipWorkflowResponse) (ShipWorkflowResponse, error) {
	if response.Local.Status != operation.StatusSucceeded || response.Local.Result.Status != operation.StatusSucceeded || !workflowBoundaryMatches(response) {
		return response, &Error{Code: CodePlanInvalid, Message: "the local ship plan must succeed before an exact push plan can be created"}
	}
	push, err := s.pushService().Plan(ctx, ShipOptions{Remote: response.Remote.RemoteName, WorkflowID: response.WorkflowID})
	if err != nil {
		response.Status = operation.StatusPartiallyCompleted
		response.RecoveryHints = []string{"The local commit succeeded. Inspect the remote state, then run dvz ship to resume the push."}
		_ = s.recordRemotePlanFailure(response)
		return response, err
	}
	response.Push = &push
	response.Remote.PushDeferred = false
	return response, nil
}

func (s ShipWorkflowService) ExecutePush(ctx context.Context, _ ShipWorkflowOptions, prompts io.Reader, response ShipWorkflowResponse) (ShipWorkflowResponse, error) {
	if response.Push == nil {
		return response, &Error{Code: CodePlanInvalid, Message: "composed ship has no exact push plan"}
	}
	push, err := s.pushService().ExecutePlanned(ctx, ShipOptions{Remote: response.Push.RemoteName, WorkflowID: response.WorkflowID}, prompts, *response.Push)
	response.Push = &push
	if err != nil {
		response.Status = operation.StatusPartiallyCompleted
		response.RecoveryHints = []string{"The local commit succeeded. Run dvz ship to safely inspect and resume the push."}
		return response, err
	}
	response.Status = operation.StatusSucceeded
	return response, nil
}

func (s ShipWorkflowService) visibleGoFiles(ctx context.Context, root string) ([]string, error) {
	paths, err := s.Git.ListVisibleFiles(ctx, root)
	if err != nil {
		return nil, classifyProcess("git", "list files for Go format check", err)
	}
	files := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.EqualFold(filepath.Ext(path), ".go") {
			files = append(files, filepath.ToSlash(path))
		}
	}
	if len(files) > 10_000 {
		return nil, &Error{Code: CodePlanInvalid, Message: "Go format check exceeds the 10000-file review limit"}
	}
	sort.Strings(files)
	return files, nil
}

func (s ShipWorkflowService) visibleGoFilesIfPlanned(ctx context.Context, plan operation.Plan) ([]string, error) {
	if _, ok := operationByCapability(plan, "go.format.check"); !ok {
		return nil, nil
	}
	files, err := s.visibleGoFiles(ctx, plan.ProjectRoot)
	if err != nil {
		return nil, err
	}
	op, _ := operationByCapability(plan, "go.format.check")
	if stringInput(op, "go_files_digest") != stringDigest(files) {
		return nil, &Error{Code: CodePreconditionFailed, Message: "Go file inventory changed after planning", Hint: "No check or mutation was performed. Build and review a fresh ship plan."}
	}
	return files, nil
}

func (s ShipWorkflowService) verifyVisibleGoFiles(ctx context.Context, plan operation.Plan) error {
	_, err := s.visibleGoFilesIfPlanned(ctx, plan)
	if err != nil {
		return &Error{Code: CodePreconditionFailed, Message: "Go file inventory changed while checks were running", Cause: err, Hint: "No files were staged and no commit or push was attempted. Build a fresh ship plan."}
	}
	return nil
}

func workflowBoundaryMatches(response ShipWorkflowResponse) bool {
	commitOp, ok := operationByCapability(response.Local.Plan, "git.commit.create")
	return ok && response.Remote.RemoteName == stringInput(commitOp, "remote_name") &&
		response.Remote.RemoteURL == stringInput(commitOp, "remote_url") &&
		response.Remote.RemoteCommit == stringInput(commitOp, "expected_remote_commit") &&
		response.Remote.Branch == stringInput(commitOp, "branch")
}

func checkOperations(plan operation.Plan) []operation.Operation {
	var checks []operation.Operation
	for _, op := range plan.Operations {
		if _, ok := registry.CheckByID(op.CapabilityID); ok {
			checks = append(checks, op)
		}
	}
	return checks
}

func toolchainFromPlan(plan operation.Plan) golangadapter.Toolchain {
	checks := checkOperations(plan)
	if len(checks) == 0 {
		return golangadapter.Toolchain{}
	}
	return golangadapter.Toolchain{
		GoPath: stringInput(checks[0], "go_path"), GofmtPath: stringInput(checks[0], "gofmt_path"), GoVersion: stringInput(checks[0], "go_version"),
	}
}

func workflowCommitOptions(options ShipWorkflowOptions, workflowID string) CommitOptions {
	return CommitOptions{
		Paths: options.Paths, Message: options.Message, Conventional: options.Conventional,
		WorkflowID: workflowID, Workflow: "ship.local",
	}
}

func classifyCheckError(action string, err error) *Error {
	var runErr *devprocess.RunError
	switch {
	case errors.Is(err, golangadapter.ErrToolMissing), errors.As(err, &runErr) && runErr.Kind == devprocess.ErrorMissing:
		return &Error{Code: CodeToolNotFound, Message: action + " requires an installed Go toolchain", Cause: err, Provider: "go"}
	case errors.As(err, &runErr) && runErr.Kind == devprocess.ErrorTimeout:
		return &Error{Code: CodeProcessTimeout, Message: action + " timed out", Cause: err, Provider: "go", Retryable: true}
	case errors.Is(err, golangadapter.ErrCheckFailed):
		return &Error{Code: CodeCheckFailed, Message: action + " failed", Cause: err, Provider: "go"}
	default:
		return &Error{Code: CodeProcessFailed, Message: action + " could not run", Cause: err, Provider: "go"}
	}
}

func (s ShipWorkflowService) recordRemotePlanFailure(response ShipWorkflowResponse) error {
	if !s.Config.History.Enabled {
		return nil
	}
	now := s.now()
	return s.History.Append(history.Record{
		SchemaVersion: 1, WorkflowID: response.WorkflowID, ExecutionID: "exec_" + now.Format("20060102150405") + "_remote_plan",
		PlanID: response.Local.Plan.ID, PlanDigest: response.Local.Plan.Digest,
		StartedAt: now, FinishedAt: now, Invocation: map[string]any{"workflow": "ship.remote-plan"},
		Project: map[string]string{"root": response.Local.Plan.ProjectRoot}, Status: operation.StatusPartiallyCompleted,
		RecoveryHints: append([]string(nil), response.RecoveryHints...),
	})
}

func stringDigest(values []string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func containsCheck(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s ShipWorkflowService) commitService() CommitService {
	return CommitService{WorkingDir: s.WorkingDir, Config: s.Config, Runner: s.Runner, History: s.History, Now: s.Now, Output: s.Output}
}

func (s ShipWorkflowService) pushService() ShipService {
	return ShipService{WorkingDir: s.WorkingDir, Config: s.Config, Runner: s.Runner, History: s.History, Now: s.Now, Output: s.Output}
}

func (s ShipWorkflowService) withDefaults() ShipWorkflowService {
	if s.Git == nil {
		s.Git = gitadapter.New(s.Runner)
	}
	if s.Checks == nil {
		adapter := golangadapter.New(s.Runner)
		s.Checks = adapter
	}
	return s
}

func (s ShipWorkflowService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
