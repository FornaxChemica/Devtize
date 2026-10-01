package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	"github.com/FornaxChemica/devtize/internal/detect"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
)

type SyncOptions struct {
	Provider string
	DryRun   bool
}

type SyncCacheState struct {
	Status           registry.CacheStatus `json:"status"`
	Digest           string               `json:"digest,omitempty"`
	KnowledgeVersion string               `json:"knowledge_version,omitempty"`
	Commands         int                  `json:"commands"`
	FilesScanned     int                  `json:"files_scanned"`
}

type SyncParser struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type SyncResponse struct {
	SchemaVersion    int                       `json:"schema_version"`
	Status           operation.Status          `json:"status"`
	Provider         string                    `json:"provider"`
	Installation     detect.ToolInstallation   `json:"installation"`
	Parser           SyncParser                `json:"parser"`
	SourceArgv       []string                  `json:"source_argv"`
	Limits           registry.SyncLimits       `json:"limits"`
	CacheBefore      SyncCacheState            `json:"cache_before"`
	CacheAfter       SyncCacheState            `json:"cache_after"`
	ParsedCount      int                       `json:"parsed_count"`
	PublishedCount   int                       `json:"published_count"`
	Warnings         []string                  `json:"warnings,omitempty"`
	PlanWarningCount int                       `json:"-"`
	Plan             operation.Plan            `json:"plan"`
	Result           operation.ExecutionResult `json:"result,omitempty"`
	snapshot         registry.ProviderSnapshot
}

type SyncCache interface {
	Load(string) (registry.CacheLoadResult, error)
	Publish(registry.ProviderSnapshot) (registry.PublishResult, error)
}

type SyncService struct {
	WorkingDir     string
	Cache          SyncCache
	CacheRoot      string
	Runner         detect.Runner
	History        history.Store
	HistoryEnabled bool
	Now            func() time.Time
	TempDir        func() (string, error)
	Output         io.Writer
}

func (s SyncService) Plan(ctx context.Context, options SyncOptions) (SyncResponse, error) {
	s = s.withDefaults()
	if options.Provider != "git" {
		return SyncResponse{}, &Error{Code: CodeInvalidUsage, Message: "sync requires the supported provider git", Hint: "Use dvz sync git."}
	}
	if s.Cache == nil || s.CacheRoot == "" || !filepath.IsAbs(s.CacheRoot) {
		return SyncResponse{}, &Error{Code: CodeSyncFailed, Message: "registry cache root must be an absolute platform cache path", Hint: "Resolve the platform user cache directory and retry."}
	}
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return SyncResponse{}, Wrap(CodeSyncFailed, "sync working directory could not be resolved", err)
	}
	loaded, err := s.Cache.Load("git")
	if err != nil {
		return SyncResponse{}, syncFailure("registry cache could not be inspected", err)
	}
	response := SyncResponse{
		SchemaVersion: 1, Provider: "git", Parser: SyncParser{ID: gitadapter.HelpParserID, Version: gitadapter.HelpParserVersion},
		Limits:      registry.SyncLimits{MaxDepth: gitadapter.HelpMaxDepth, MaxCommands: gitadapter.HelpMaxCommands, MaxOutputBytes: gitadapter.HelpMaxBytes, TimeoutMillis: int(gitadapter.HelpTimeout / time.Millisecond)},
		CacheBefore: cacheState(loaded), CacheAfter: cacheState(loaded), Warnings: append([]string(nil), loaded.Warnings...),
	}
	response.PlanWarningCount = len(response.Warnings)
	installation := detect.ToolDetector{Runner: s.Runner, Now: s.Now}.Detect(ctx, detect.ToolSpec{
		ProviderID: "git", Executable: "git", VersionArgs: []string{"--version"}, MinimumVersion: "2.23.0", WorkingDir: root,
	})
	response.Installation = installation
	if err := syncInstallationError(installation); err != nil {
		return response, err
	}
	isolation, err := s.TempDir()
	if err != nil {
		return response, syncFailure("isolated Git introspection directory could not be created", err)
	}
	defer os.RemoveAll(isolation)
	capturedAt := s.now()
	adapter := gitadapter.Adapter{Runner: s.Runner, Executable: installation.Path, Timeout: gitadapter.HelpTimeout}
	inventory, err := adapter.InspectHelp(ctx, root, isolation, installation.Version, capturedAt)
	if err != nil {
		return response, syncFailure("Git help inventory could not be synchronized", err)
	}
	response.SourceArgv = append([]string(nil), inventory.SourceArgv...)
	response.ParsedCount = len(inventory.Commands)
	snapshot := registry.ProviderSnapshot{
		SchemaVersion: registry.CacheSchemaVersion, ProviderID: "git",
		Installation: registry.InstallationSnapshot{
			Executable: installation.Executable, Path: installation.Path, Version: installation.Version, DetectedAt: installation.DetectedAt,
		},
		Attempt:          registry.SyncAttempt{Status: "succeeded", AttemptedAt: capturedAt},
		KnowledgeVersion: installation.Version, ParserID: gitadapter.HelpParserID, ParserVersion: gitadapter.HelpParserVersion,
		SourceArgv: inventory.SourceArgv, SourceDigest: inventory.SourceDigest, CapturedAt: inventory.CapturedAt,
		Limits: response.Limits, Commands: inventory.Commands,
	}
	snapshot, err = snapshot.WithDigest()
	if err != nil {
		return response, syncFailure("Git knowledge snapshot could not be digested", err)
	}
	if err := snapshot.Validate(); err != nil {
		return response, syncFailure("Git knowledge snapshot did not pass validation", err)
	}
	oldDigest := response.CacheBefore.Digest
	now := s.now()
	plan := operation.Plan{
		SchemaVersion: 1, ID: "plan_sync_git_" + now.Format("20060102150405"),
		Intent: "Publish validated Git discovery knowledge to the local cache", CreatedAt: now, DryRun: options.DryRun,
		Operations: []operation.Operation{{
			ID: "op_registry_knowledge_publish", CapabilityID: "registry.knowledge.publish", ProviderID: "git",
			Summary: "Publish validated Git command knowledge", Risk: safety.RiskLocalWrite,
			Effects: []operation.Effect{{Kind: "write_registry_cache", Target: filepath.Join(s.CacheRoot, "git")}},
			Inputs: map[string]any{
				"provider": "git", "cache_path": filepath.Join(s.CacheRoot, "git"), "old_digest": oldDigest,
				"new_digest": snapshot.Digest, "tool_path": installation.Path, "tool_version": installation.Version,
				"parser_id": snapshot.ParserID, "parser_version": snapshot.ParserVersion,
				"source_argv": append([]string(nil), snapshot.SourceArgv...), "command_count": len(snapshot.Commands),
				"max_depth": snapshot.Limits.MaxDepth, "max_commands": snapshot.Limits.MaxCommands,
				"max_output_bytes": snapshot.Limits.MaxOutputBytes, "timeout_millis": snapshot.Limits.TimeoutMillis,
			},
		}},
	}
	plan, err = plan.WithDigest()
	if err != nil {
		return response, Wrap(CodePlanInvalid, "sync plan could not be digested", err)
	}
	response.Plan = plan
	response.snapshot = snapshot
	response.Status = operation.StatusProposed
	if options.DryRun {
		response.Status = operation.StatusValidated
	}
	return response, nil
}

func (s SyncService) ExecutePlanned(ctx context.Context, options SyncOptions, prompts io.Reader, response SyncResponse) (SyncResponse, error) {
	s = s.withDefaults()
	digest, err := operation.Digest(response.Plan)
	if err != nil || digest != response.Plan.Digest {
		return response, &Error{Code: CodePlanInvalid, Message: "authorized sync plan digest is invalid", Cause: err}
	}
	if response.Plan.DryRun || options.DryRun {
		return response, nil
	}
	if len(response.Plan.Operations) != 1 || response.Plan.Operations[0].CapabilityID != "registry.knowledge.publish" || response.snapshot.Digest == "" {
		return response, &Error{Code: CodePlanInvalid, Message: "sync plan is missing its reviewed cache publication"}
	}
	op := response.Plan.Operations[0]
	if stringInput(op, "new_digest") != response.snapshot.Digest || stringInput(op, "old_digest") != response.CacheBefore.Digest || stringInput(op, "tool_path") != response.Installation.Path || stringInput(op, "tool_version") != response.Installation.Version {
		return response, &Error{Code: CodePlanInvalid, Message: "sync response does not match its immutable publication plan"}
	}
	answers := newConfirmationReader(prompts, s.Output)
	ok, confirmErr := answers.confirm("Publish this Git knowledge snapshot", response.Plan.Digest, "sync")
	if confirmErr != nil || !ok {
		response.Status = operation.StatusCancelled
		response.Result = operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusCancelled}
		return response, &Error{Code: CodeConfirmationDeclined, Message: "sync confirmation declined", Hint: "No cache or history changes were made.", Cause: confirmErr}
	}
	current := detect.ToolDetector{Runner: s.Runner, Now: s.Now}.Detect(ctx, detect.ToolSpec{
		ProviderID: "git", Executable: "git", VersionArgs: []string{"--version"}, MinimumVersion: "2.23.0", WorkingDir: s.WorkingDir,
	})
	if current.Status != detect.ToolInstalled || current.Path != response.Installation.Path || current.Version != response.Installation.Version {
		return response, &Error{Code: CodePreconditionFailed, Message: "Git executable or version changed after sync planning", Hint: "No cache was published. Build and review a fresh sync plan."}
	}
	currentCache, err := s.Cache.Load("git")
	if err != nil {
		return response, syncFailure("registry cache could not be revalidated", err)
	}
	if cacheState(currentCache).Digest != response.CacheBefore.Digest {
		return response, &Error{Code: CodePreconditionFailed, Message: "registry cache changed after sync planning", Hint: "No cache was published. Build and review a fresh sync plan."}
	}
	operationStep := op
	step := operation.StepResult{OperationID: operationStep.ID, CapabilityID: operationStep.CapabilityID, Summary: operationStep.Summary, Status: operation.StatusRunning, StartedAt: s.now()}
	result := operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusRunning}
	published, err := s.Cache.Publish(response.snapshot)
	step.FinishedAt = s.now()
	if err != nil {
		step.Status = operation.StatusFailed
		step.ErrorCode = string(CodeSyncFailed)
		step.ErrorMessage = "validated registry cache snapshot could not be published"
		result.Status = operation.StatusFailed
		result.Steps = []operation.StepResult{step}
		response.Status = operation.StatusFailed
		response.Result = result
		return response, syncFailure(step.ErrorMessage, err)
	}
	response.Warnings = append(response.Warnings, published.Warnings...)
	loaded, err := s.Cache.Load("git")
	if err != nil || loaded.Snapshot == nil || loaded.Snapshot.Digest != response.snapshot.Digest || len(loaded.Snapshot.Commands) != len(response.snapshot.Commands) {
		step.Status = operation.StatusFailed
		step.ErrorCode = string(CodePostconditionFailed)
		step.ErrorMessage = "published registry cache snapshot could not be verified"
		step.RecoveryHint = "The previous valid cache remains immutable. Inspect the cache directory and rerun dvz sync git."
		result.Status = operation.StatusPartiallyCompleted
		result.Steps = []operation.StepResult{step}
		result.RecoveryHints = []string{step.RecoveryHint}
		response.Status = operation.StatusPartiallyCompleted
		response.Result = result
		return response, &Error{Code: CodePostconditionFailed, Message: step.ErrorMessage, Hint: step.RecoveryHint, Cause: err}
	}
	step.Status = operation.StatusSucceeded
	result.Status = operation.StatusSucceeded
	result.Steps = []operation.StepResult{step}
	response.Status = operation.StatusSucceeded
	response.Result = result
	response.CacheAfter = cacheState(loaded)
	response.PublishedCount = len(loaded.Snapshot.Commands)
	response.Warnings = append(response.Warnings, loaded.Warnings...)
	if s.HistoryEnabled {
		if err := s.writeHistory(response); err != nil {
			response.Status = operation.StatusPartiallyCompleted
			response.Result.Status = operation.StatusPartiallyCompleted
			response.Result.RecoveryHints = []string{"The cache was published successfully; check local history permissions before the next sync."}
			return response, &Error{Code: CodeHistoryWriteFailed, Message: "sync history could not be written", Cause: err, Hint: response.Result.RecoveryHints[0]}
		}
	}
	return response, nil
}

func (s SyncService) writeHistory(response SyncResponse) error {
	return s.History.Append(history.Record{
		SchemaVersion: 1,
		ExecutionID:   history.ExecutionID(s.now(), response.Plan.Digest, "registry.sync"),
		PlanID:        response.Plan.ID,
		PlanDigest:    response.Plan.Digest,
		StartedAt:     response.Plan.CreatedAt,
		FinishedAt:    s.now(),
		Invocation: map[string]any{
			"workflow": "registry.sync", "provider": response.Provider,
			"tool_version": response.Installation.Version, "old_digest": response.CacheBefore.Digest,
			"new_digest": response.CacheAfter.Digest, "commands": response.PublishedCount,
			"max_depth": response.Limits.MaxDepth, "max_commands": response.Limits.MaxCommands,
			"max_output_bytes": response.Limits.MaxOutputBytes, "timeout_millis": response.Limits.TimeoutMillis,
			"approved": true,
		},
		Status: response.Result.Status, Steps: response.Result.Steps, RecoveryHints: response.Result.RecoveryHints,
	})
}

func (s SyncService) withDefaults() SyncService {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.TempDir == nil {
		s.TempDir = func() (string, error) { return os.MkdirTemp("", "devtize-sync-git-") }
	}
	return s
}

func (s SyncService) now() time.Time { return s.Now().UTC() }

func cacheState(loaded registry.CacheLoadResult) SyncCacheState {
	state := SyncCacheState{Status: loaded.Status, FilesScanned: loaded.Files}
	if loaded.Snapshot != nil {
		state.Digest = loaded.Snapshot.Digest
		state.KnowledgeVersion = loaded.Snapshot.KnowledgeVersion
		state.Commands = len(loaded.Snapshot.Commands)
	}
	return state
}

func syncInstallationError(installation detect.ToolInstallation) error {
	switch installation.Status {
	case detect.ToolInstalled:
		return nil
	case detect.ToolMissing:
		return &Error{Code: CodeToolNotFound, Message: "git executable was not found", Provider: "git", Hint: "Install Git and rerun dvz sync git."}
	case detect.ToolVersionUnsupported:
		return &Error{Code: CodeToolVersionUnsupported, Message: "installed Git version is unsupported", Provider: "git", Hint: "Install Git 2.23.0 or newer."}
	default:
		return &Error{Code: CodeSyncFailed, Message: "installed Git version could not be validated", Provider: "git", Hint: "Run git --version and resolve the reported local tool error."}
	}
}

func syncFailure(message string, cause error) *Error {
	var runErr *devprocess.RunError
	if errors.As(cause, &runErr) && runErr.Kind == devprocess.ErrorTimeout {
		return &Error{Code: CodeSyncFailed, Message: message, Provider: "git", Retryable: true, Cause: cause, Hint: "Git help introspection timed out; resolve the local Git issue and retry."}
	}
	return &Error{Code: CodeSyncFailed, Message: message, Provider: "git", Cause: cause, Hint: "The last valid cache was retained. Resolve the local Git or cache error and retry."}
}
