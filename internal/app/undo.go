package app

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/registry"
)

const (
	UndoAvailable   = "available"
	UndoUnavailable = "unavailable"

	ReasonHistoryEvidenceMissing    = "HISTORY_EVIDENCE_MISSING"
	ReasonSourceNotSucceeded        = "SOURCE_NOT_SUCCEEDED"
	ReasonSourceWorkflowUnsupported = "SOURCE_WORKFLOW_UNSUPPORTED"
	ReasonSourceCommitNotHead       = "SOURCE_COMMIT_NOT_HEAD"
	ReasonSourceParentMismatch      = "SOURCE_PARENT_MISMATCH"
	ReasonBranchChanged             = "BRANCH_CHANGED"
	ReasonWorktreeNotClean          = "WORKTREE_NOT_CLEAN"
	ReasonCommitPublished           = "COMMIT_PUBLISHED"
	ReasonRemoteStateAmbiguous      = "REMOTE_STATE_AMBIGUOUS"
)

var executionIDPattern = regexp.MustCompile(`^exec_[A-Za-z0-9._-]{1,128}$`)
var commitIDPattern = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

type UndoOptions struct {
	ExecutionID string
	DryRun      bool
}

type UndoReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type UndoSource struct {
	ExecutionID    string                  `json:"execution_id"`
	Workflow       string                  `json:"workflow"`
	Status         operation.Status        `json:"status"`
	PlanID         string                  `json:"plan_id"`
	PlanDigest     string                  `json:"plan_digest"`
	ObservedChange *history.ObservedChange `json:"observed_change,omitempty"`
}

type UndoGitState struct {
	ProjectRoot string                        `json:"project_root"`
	Branch      string                        `json:"branch,omitempty"`
	HeadCommit  string                        `json:"head_commit,omitempty"`
	WorkingTree string                        `json:"working_tree,omitempty"`
	Upstream    gitadapter.UpstreamTarget     `json:"upstream"`
	LiveRemote  *gitadapter.RemoteBranchState `json:"live_remote,omitempty"`
}

type UndoResponse struct {
	SchemaVersion     int              `json:"schema_version"`
	Status            operation.Status `json:"status"`
	Eligibility       string           `json:"eligibility"`
	RollbackKind      string           `json:"rollback_kind"`
	MutationPerformed bool             `json:"mutation_performed"`
	Source            UndoSource       `json:"source"`
	Git               UndoGitState     `json:"git"`
	Reasons           []UndoReason     `json:"reasons"`
	Limitations       []string         `json:"limitations"`
	Plan              *operation.Plan  `json:"plan,omitempty"`
}

type UndoHistoryPort interface {
	Find(history.FindOptions) (history.Record, bool, error)
}

type UndoGitPort interface {
	InspectRepo(context.Context, gitadapter.InspectRepoInput) (gitadapter.InspectRepoResult, error)
	InspectChanges(context.Context, string) (gitadapter.ChangeSet, error)
	InspectCommitParents(context.Context, string, string) ([]string, error)
	InspectUpstreamTarget(context.Context, string, string) (gitadapter.UpstreamTarget, error)
	InspectRemoteBranchState(context.Context, gitadapter.InspectRemoteBranchInput) (gitadapter.RemoteBranchState, error)
}

type UndoService struct {
	WorkingDir string
	Runner     interface {
		Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
	}
	History UndoHistoryPort
	Git     UndoGitPort
	Now     func() time.Time
}

func (s UndoService) Run(ctx context.Context, options UndoOptions) (UndoResponse, error) {
	if !options.DryRun {
		return UndoResponse{}, &Error{Code: CodeInvalidUsage, Message: "undo planning requires --dry-run", Hint: "Executable undo is not implemented."}
	}
	if !executionIDPattern.MatchString(options.ExecutionID) {
		return UndoResponse{}, &Error{Code: CodeInvalidUsage, Message: "undo requires a valid execution ID"}
	}
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return UndoResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	s = s.withDefaults()
	if s.History == nil {
		return UndoResponse{}, &Error{Code: CodeHistoryReadFailed, Message: "history reader is unavailable"}
	}
	record, found, err := s.History.Find(history.FindOptions{ProjectRoot: root, ExecutionID: options.ExecutionID})
	if err != nil {
		if errors.Is(err, history.ErrInvalid) {
			return UndoResponse{}, &Error{Code: CodeHistoryInvalid, Message: "history contains an invalid or ambiguous execution record", Cause: err}
		}
		return UndoResponse{}, &Error{Code: CodeHistoryReadFailed, Message: "history could not be read", Cause: err}
	}
	if !found {
		return UndoResponse{}, &Error{Code: CodeHistoryEntryNotFound, Message: "execution was not found in this project's history"}
	}

	workflow := historyWorkflow(record)
	response := UndoResponse{
		SchemaVersion: 1, Status: operation.StatusValidated, Eligibility: UndoUnavailable, RollbackKind: UndoUnavailable,
		MutationPerformed: false, Reasons: []UndoReason{},
		Limitations: []string{"Plan only; execution is not implemented in this milestone."},
		Source:      UndoSource{ExecutionID: record.ExecutionID, Workflow: workflow, Status: record.Status, PlanID: record.PlanID, PlanDigest: record.PlanDigest},
		Git:         UndoGitState{ProjectRoot: root},
	}
	if record.Status != operation.StatusSucceeded {
		return unavailable(response, ReasonSourceNotSucceeded, "Only a successfully completed source execution can have a compensation plan."), nil
	}
	if workflow != "commit" && workflow != "ship.local" {
		return unavailable(response, ReasonSourceWorkflowUnsupported, "This workflow has no registered compensation in the planner."), nil
	}
	if !succeededStep(record, "git.commit.create") {
		return unavailable(response, ReasonSourceNotSucceeded, "The source record does not contain a successful commit creation step."), nil
	}
	change, ok := commitObservation(record)
	if !ok {
		return unavailable(response, ReasonHistoryEvidenceMissing, "The source record lacks one valid, trusted commit transition."), nil
	}
	response.Source.ObservedChange = &change

	state, err := s.Git.InspectRepo(ctx, gitadapter.InspectRepoInput{ProjectRoot: root})
	if err != nil {
		return UndoResponse{}, classifyProcess("git", "inspect repository for undo planning", err)
	}
	if !state.IsRepository || !state.HasCommits {
		return UndoResponse{}, &Error{Code: CodeProjectNotFound, Message: "undo planning requires an existing Git repository with a commit"}
	}
	response.Git.Branch = state.HeadBranch
	response.Git.HeadCommit = state.HeadCommit
	response.Git.WorkingTree = state.WorkingTreeStatus
	if state.IsDetachedHead || state.HeadBranch != change.Branch {
		return unavailable(response, ReasonBranchChanged, "The current attached branch no longer matches the recorded commit transition."), nil
	}
	if state.HeadCommit != change.AfterCommit {
		return unavailable(response, ReasonSourceCommitNotHead, "The recorded commit is no longer the current HEAD."), nil
	}
	changes, err := s.Git.InspectChanges(ctx, root)
	if err != nil {
		return UndoResponse{}, classifyProcess("git", "inspect working tree for undo planning", err)
	}
	if state.WorkingTreeStatus != "clean" || len(changes.Staged)+len(changes.Unstaged)+len(changes.Untracked) != 0 {
		return unavailable(response, ReasonWorktreeNotClean, "The index and working tree must be clean before compensation can be planned."), nil
	}
	parents, err := s.Git.InspectCommitParents(ctx, root, change.AfterCommit)
	if err != nil {
		return UndoResponse{}, classifyProcess("git", "inspect commit parents for undo planning", err)
	}
	if len(parents) != 1 || parents[0] != change.BeforeCommit {
		return unavailable(response, ReasonSourceParentMismatch, "The current commit is not the recorded single-parent transition."), nil
	}
	upstream, err := s.Git.InspectUpstreamTarget(ctx, root, state.HeadBranch)
	if err != nil {
		return UndoResponse{}, classifyProcess("git", "inspect upstream for undo planning", err)
	}
	response.Git.Upstream = upstream
	if upstream.Configured {
		remote, err := s.Git.InspectRemoteBranchState(ctx, gitadapter.InspectRemoteBranchInput{ProjectRoot: root, RemoteName: upstream.Remote, Branch: upstream.Branch})
		if err != nil {
			return UndoResponse{}, classifyProcess("git", "inspect live upstream for undo planning", err)
		}
		response.Git.LiveRemote = &remote
		switch {
		case !remote.Exists, remote.Commit == change.BeforeCommit:
		case remote.Commit == change.AfterCommit:
			return unavailable(response, ReasonCommitPublished, "The recorded commit is already the live upstream commit."), nil
		default:
			return unavailable(response, ReasonRemoteStateAmbiguous, "The live upstream is neither the recorded parent nor the recorded commit."), nil
		}
	} else {
		response.Limitations = append(response.Limitations, "No configured upstream exists; publication outside current Git configuration cannot be ruled out.")
	}

	capability, exists := registry.CompensationByID("git.commit.uncommit_preserve_changes")
	if !exists || capability.Support != registry.SupportPlanned {
		return UndoResponse{}, &Error{Code: CodePlanInvalid, Message: "reviewed compensation capability is unavailable"}
	}
	now := s.now()
	plan := operation.Plan{
		SchemaVersion: 1, ID: "plan_undo_" + now.Format("20060102150405"),
		Intent: "Plan local compensation for a verified Devtize-created commit", CreatedAt: now,
		ProjectRoot: root, DryRun: true,
		Operations: []operation.Operation{{
			ID: "op_undo_uncommit", CapabilityID: capability.ID, ProviderID: capability.ProviderID,
			Summary: capability.Summary, Risk: capability.Risk,
			Effects: []operation.Effect{{Kind: "move_local_branch", Target: change.Branch}, {Kind: "preserve_working_tree_changes", Target: root}},
			Inputs: map[string]any{
				"source_execution_id": record.ExecutionID, "expected_branch": change.Branch,
				"expected_head": change.AfterCommit, "verified_parent": change.BeforeCommit,
				"required_worktree_state": "clean", "execution_supported": false,
			},
		}},
	}
	plan, err = plan.WithDigest()
	if err != nil {
		return UndoResponse{}, Wrap(CodePlanInvalid, "undo plan could not be digested", err)
	}
	response.Eligibility = UndoAvailable
	response.RollbackKind = "compensating"
	response.Plan = &plan
	response.Limitations = append(response.Limitations, "Compensation would preserve content but may not reproduce hook side effects or index metadata exactly.")
	return response, nil
}

func unavailable(response UndoResponse, code, message string) UndoResponse {
	response.Reasons = append(response.Reasons, UndoReason{Code: code, Message: message})
	return response
}

func succeededStep(record history.Record, capabilityID string) bool {
	for _, step := range record.Steps {
		if step.CapabilityID == capabilityID && step.Status == operation.StatusSucceeded {
			return true
		}
	}
	return false
}

func commitObservation(record history.Record) (history.ObservedChange, bool) {
	var match history.ObservedChange
	count := 0
	for _, observed := range record.ObservedChanges {
		if observed.Kind != "git.commit.created" || observed.ProviderID != "git" || observed.CapabilityID != "git.commit.create" {
			continue
		}
		if strings.TrimSpace(observed.Branch) == "" || !commitIDPattern.MatchString(observed.BeforeCommit) || !commitIDPattern.MatchString(observed.AfterCommit) || observed.BeforeCommit == observed.AfterCommit {
			return history.ObservedChange{}, false
		}
		match = observed
		count++
	}
	return match, count == 1
}

func (s UndoService) withDefaults() UndoService {
	if s.Git == nil {
		s.Git = gitadapter.New(s.Runner)
	}
	return s
}

func (s UndoService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
