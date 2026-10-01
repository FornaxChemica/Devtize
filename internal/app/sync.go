package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	gitadapter "github.com/FornaxChemica/devtize/internal/adapters/git"
	githubadapter "github.com/FornaxChemica/devtize/internal/adapters/github"
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
	Aliases          int                  `json:"aliases,omitempty"`
	Flags            int                  `json:"flags,omitempty"`
	FilesScanned     int                  `json:"files_scanned"`
}
type SyncParser struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type SyncResponse struct {
	SchemaVersion       int                       `json:"schema_version"`
	Status              operation.Status          `json:"status"`
	Provider            string                    `json:"provider"`
	Installation        detect.ToolInstallation   `json:"installation"`
	Parser              SyncParser                `json:"parser"`
	SourceArgv          []string                  `json:"source_argv"`
	Limits              registry.SyncLimits       `json:"limits"`
	CacheBefore         SyncCacheState            `json:"cache_before"`
	CacheAfter          SyncCacheState            `json:"cache_after"`
	ParsedCount         int                       `json:"parsed_count"`
	ParsedAliasCount    int                       `json:"parsed_alias_count,omitempty"`
	ParsedFlagCount     int                       `json:"parsed_flag_count,omitempty"`
	PublishedCount      int                       `json:"published_count"`
	PublishedAliasCount int                       `json:"published_alias_count,omitempty"`
	PublishedFlagCount  int                       `json:"published_flag_count,omitempty"`
	AnchorSample        []string                  `json:"anchor_sample,omitempty"`
	Warnings            []string                  `json:"warnings,omitempty"`
	PlanWarningCount    int                       `json:"-"`
	Plan                operation.Plan            `json:"plan"`
	Result              operation.ExecutionResult `json:"result,omitempty"`
	snapshot            registry.ProviderSnapshot
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

type syncInventory struct {
	commands       []registry.CommandKnowledge
	sourceArgv     []string
	sourceDigest   string
	capturedAt     time.Time
	aliases, flags int
}
type syncProvider struct {
	id, display, executable, minimum, parserID, parserVersion string
	limits                                                    registry.SyncLimits
}

func selectSyncProvider(id string) (syncProvider, error) {
	switch id {
	case "git":
		return syncProvider{id: "git", display: "Git", executable: "git", minimum: "2.23.0", parserID: gitadapter.HelpParserID, parserVersion: gitadapter.HelpParserVersion, limits: registry.SyncLimits{MaxDepth: gitadapter.HelpMaxDepth, MaxCommands: gitadapter.HelpMaxCommands, MaxOutputBytes: gitadapter.HelpMaxBytes, TimeoutMillis: int(gitadapter.HelpTimeout / time.Millisecond)}}, nil
	case "gh":
		return syncProvider{id: "gh", display: "GitHub CLI", executable: "gh", minimum: "2.0.0", parserID: githubadapter.ReferenceParserID, parserVersion: githubadapter.ReferenceParserVersion, limits: registry.SyncLimits{MaxDepth: githubadapter.ReferenceMaxDepth, MaxCommands: githubadapter.ReferenceMaxCommands, MaxOutputBytes: githubadapter.ReferenceMaxBytes, TimeoutMillis: int(githubadapter.ReferenceTimeout / time.Millisecond), MaxFlags: githubadapter.ReferenceMaxFlags, MaxAliases: githubadapter.ReferenceMaxAliases}}, nil
	default:
		return syncProvider{}, &Error{Code: CodeInvalidUsage, Message: "sync requires exactly one supported provider", Hint: "Use dvz sync git or dvz sync gh."}
	}
}

func (s SyncService) Plan(ctx context.Context, options SyncOptions) (SyncResponse, error) {
	s = s.withDefaults()
	provider, err := selectSyncProvider(options.Provider)
	if err != nil {
		return SyncResponse{}, err
	}
	if s.Cache == nil || s.CacheRoot == "" || !filepath.IsAbs(s.CacheRoot) {
		return SyncResponse{}, &Error{Code: CodeSyncFailed, Message: "registry cache root must be an absolute platform cache path", Provider: provider.id, Hint: "Resolve the platform user cache directory and retry."}
	}
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return SyncResponse{}, Wrap(CodeSyncFailed, "sync working directory could not be resolved", err)
	}
	loaded, err := s.Cache.Load(provider.id)
	if err != nil {
		return SyncResponse{}, syncFailure(provider, "registry cache could not be inspected", err)
	}
	response := SyncResponse{SchemaVersion: 1, Provider: provider.id, Parser: SyncParser{ID: provider.parserID, Version: provider.parserVersion}, Limits: provider.limits, CacheBefore: cacheState(loaded), CacheAfter: cacheState(loaded), Warnings: append([]string(nil), loaded.Warnings...)}
	response.PlanWarningCount = len(response.Warnings)
	isolation, err := s.TempDir()
	if err != nil {
		return response, syncFailure(provider, "isolated "+provider.display+" introspection directory could not be created", err)
	}
	defer os.RemoveAll(isolation)
	installation := detect.ToolDetector{Runner: s.Runner, Now: s.Now}.Detect(ctx, detectionSpec(provider, root, isolation))
	response.Installation = installation
	if err := syncInstallationError(provider, installation); err != nil {
		return response, err
	}
	capturedAt := s.now()
	inventory, err := s.inspect(ctx, provider, isolation, installation, capturedAt)
	if err != nil {
		return response, syncFailure(provider, provider.display+" help inventory could not be synchronized", err)
	}
	response.SourceArgv = append([]string(nil), inventory.sourceArgv...)
	response.ParsedCount = len(inventory.commands)
	response.ParsedAliasCount = inventory.aliases
	response.ParsedFlagCount = inventory.flags
	if provider.id == "gh" {
		response.AnchorSample = []string{"gh api", "gh auth login", "gh issue create", "gh pr create", "gh repo create"}
	}
	snapshot := registry.ProviderSnapshot{SchemaVersion: registry.CacheSchemaVersion, ProviderID: provider.id, Installation: registry.InstallationSnapshot{Executable: installation.Executable, Path: installation.Path, Version: installation.Version, DetectedAt: installation.DetectedAt}, Attempt: registry.SyncAttempt{Status: "succeeded", AttemptedAt: capturedAt}, KnowledgeVersion: installation.Version, ParserID: provider.parserID, ParserVersion: provider.parserVersion, SourceArgv: inventory.sourceArgv, SourceDigest: inventory.sourceDigest, CapturedAt: inventory.capturedAt, Limits: response.Limits, Commands: inventory.commands}
	snapshot, err = snapshot.WithDigest()
	if err != nil {
		return response, syncFailure(provider, provider.display+" knowledge snapshot could not be digested", err)
	}
	if err := snapshot.Validate(); err != nil {
		return response, syncFailure(provider, provider.display+" knowledge snapshot did not pass validation", err)
	}
	now := s.now()
	inputs := map[string]any{"provider": provider.id, "cache_path": filepath.Join(s.CacheRoot, provider.id), "old_digest": response.CacheBefore.Digest, "new_digest": snapshot.Digest, "tool_path": installation.Path, "tool_version": installation.Version, "parser_id": snapshot.ParserID, "parser_version": snapshot.ParserVersion, "source_argv": append([]string(nil), snapshot.SourceArgv...), "command_count": len(snapshot.Commands), "max_depth": snapshot.Limits.MaxDepth, "max_commands": snapshot.Limits.MaxCommands, "max_output_bytes": snapshot.Limits.MaxOutputBytes, "timeout_millis": snapshot.Limits.TimeoutMillis}
	if provider.id == "gh" {
		inputs["alias_count"] = inventory.aliases
		inputs["flag_count"] = inventory.flags
		inputs["max_aliases"] = snapshot.Limits.MaxAliases
		inputs["max_flags"] = snapshot.Limits.MaxFlags
	}
	plan := operation.Plan{SchemaVersion: 1, ID: "plan_sync_" + provider.id + "_" + now.Format("20060102150405"), Intent: "Publish validated " + provider.display + " discovery knowledge to the local cache", CreatedAt: now, DryRun: options.DryRun, Operations: []operation.Operation{{ID: "op_registry_knowledge_publish", CapabilityID: "registry.knowledge.publish", ProviderID: provider.id, Summary: "Publish validated " + provider.display + " command knowledge", Risk: safety.RiskLocalWrite, Effects: []operation.Effect{{Kind: "write_registry_cache", Target: filepath.Join(s.CacheRoot, provider.id)}}, Inputs: inputs}}}
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

func (s SyncService) inspect(ctx context.Context, p syncProvider, isolation string, i detect.ToolInstallation, at time.Time) (syncInventory, error) {
	if p.id == "git" {
		v, e := (gitadapter.Adapter{Runner: s.Runner, Executable: i.Path, Timeout: gitadapter.HelpTimeout}).InspectHelp(ctx, s.WorkingDir, isolation, i.Version, at)
		return syncInventory{commands: v.Commands, sourceArgv: v.SourceArgv, sourceDigest: v.SourceDigest, capturedAt: v.CapturedAt}, e
	}
	v, e := (githubadapter.Adapter{Runner: s.Runner, Executable: i.Path}).InspectReference(ctx, isolation, i.Version, at)
	return syncInventory{commands: v.Commands, sourceArgv: v.SourceArgv, sourceDigest: v.SourceDigest, capturedAt: v.CapturedAt, aliases: v.AliasCount, flags: v.FlagCount}, e
}
func detectionSpec(p syncProvider, root, isolation string) detect.ToolSpec {
	v := detect.ToolSpec{ProviderID: p.id, Executable: p.executable, VersionArgs: []string{"--version"}, MinimumVersion: p.minimum, WorkingDir: root}
	if p.id == "gh" {
		v.WorkingDir = isolation
		v.EnvOverlay = githubadapter.ReferenceEnvironment(isolation)
	}
	return v
}

func (s SyncService) ExecutePlanned(ctx context.Context, options SyncOptions, prompts io.Reader, response SyncResponse) (SyncResponse, error) {
	s = s.withDefaults()
	provider, err := selectSyncProvider(response.Provider)
	if err != nil || options.Provider != response.Provider {
		return response, &Error{Code: CodePlanInvalid, Message: "sync response provider does not match the requested provider", Cause: err}
	}
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
	if op.ProviderID != provider.id || stringInput(op, "provider") != provider.id || stringInput(op, "new_digest") != response.snapshot.Digest || stringInput(op, "old_digest") != response.CacheBefore.Digest || stringInput(op, "tool_path") != response.Installation.Path || stringInput(op, "tool_version") != response.Installation.Version {
		return response, &Error{Code: CodePlanInvalid, Message: "sync response does not match its immutable publication plan"}
	}
	ok, confirmErr := newConfirmationReader(prompts, s.Output).confirm("Publish this "+provider.display+" knowledge snapshot", response.Plan.Digest, "sync")
	if confirmErr != nil || !ok {
		response.Status = operation.StatusCancelled
		response.Result = operation.ExecutionResult{SchemaVersion: 1, PlanID: response.Plan.ID, PlanDigest: response.Plan.Digest, Status: operation.StatusCancelled}
		return response, &Error{Code: CodeConfirmationDeclined, Message: "sync confirmation declined", Hint: "No cache or history changes were made.", Cause: confirmErr}
	}
	isolation, err := s.TempDir()
	if err != nil {
		return response, syncFailure(provider, "isolated "+provider.display+" revalidation directory could not be created", err)
	}
	defer os.RemoveAll(isolation)
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return response, Wrap(CodeSyncFailed, "sync working directory could not be resolved", err)
	}
	current := detect.ToolDetector{Runner: s.Runner, Now: s.Now}.Detect(ctx, detectionSpec(provider, root, isolation))
	if current.Status != detect.ToolInstalled || current.Path != response.Installation.Path || current.Version != response.Installation.Version {
		return response, &Error{Code: CodePreconditionFailed, Message: provider.display + " executable or version changed after sync planning", Provider: provider.id, Hint: "No cache was published. Build and review a fresh sync plan."}
	}
	currentCache, err := s.Cache.Load(provider.id)
	if err != nil {
		return response, syncFailure(provider, "registry cache could not be revalidated", err)
	}
	if cacheState(currentCache).Digest != response.CacheBefore.Digest {
		return response, &Error{Code: CodePreconditionFailed, Message: "registry cache changed after sync planning", Provider: provider.id, Hint: "No cache was published. Build and review a fresh sync plan."}
	}
	step := operation.StepResult{OperationID: op.ID, CapabilityID: op.CapabilityID, Summary: op.Summary, Status: operation.StatusRunning, StartedAt: s.now()}
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
		return response, syncFailure(provider, step.ErrorMessage, err)
	}
	response.Warnings = append(response.Warnings, published.Warnings...)
	loaded, err := s.Cache.Load(provider.id)
	state := cacheState(loaded)
	if err != nil || loaded.Snapshot == nil || loaded.Snapshot.Digest != response.snapshot.Digest || state.Commands != len(response.snapshot.Commands) || state.Aliases != response.ParsedAliasCount || state.Flags != response.ParsedFlagCount {
		step.Status = operation.StatusFailed
		step.ErrorCode = string(CodePostconditionFailed)
		step.ErrorMessage = "published registry cache snapshot could not be verified"
		step.RecoveryHint = "The previous valid cache remains immutable. Inspect the cache directory and rerun dvz sync " + provider.id + "."
		result.Status = operation.StatusPartiallyCompleted
		result.Steps = []operation.StepResult{step}
		result.RecoveryHints = []string{step.RecoveryHint}
		response.Status = operation.StatusPartiallyCompleted
		response.Result = result
		return response, &Error{Code: CodePostconditionFailed, Message: step.ErrorMessage, Provider: provider.id, Hint: step.RecoveryHint, Cause: err}
	}
	step.Status = operation.StatusSucceeded
	result.Status = operation.StatusSucceeded
	result.Steps = []operation.StepResult{step}
	response.Status = operation.StatusSucceeded
	response.Result = result
	response.CacheAfter = state
	response.PublishedCount = state.Commands
	response.PublishedAliasCount = state.Aliases
	response.PublishedFlagCount = state.Flags
	response.Warnings = append(response.Warnings, loaded.Warnings...)
	if s.HistoryEnabled {
		if err := s.writeHistory(response); err != nil {
			response.Status = operation.StatusPartiallyCompleted
			response.Result.Status = operation.StatusPartiallyCompleted
			response.Result.RecoveryHints = []string{"The cache was published successfully; check local history permissions before the next sync."}
			return response, &Error{Code: CodeHistoryWriteFailed, Message: "sync history could not be written", Provider: provider.id, Cause: err, Hint: response.Result.RecoveryHints[0]}
		}
	}
	return response, nil
}

func (s SyncService) writeHistory(r SyncResponse) error {
	v := map[string]any{"workflow": "registry.sync", "provider": r.Provider, "tool_version": r.Installation.Version, "old_digest": r.CacheBefore.Digest, "new_digest": r.CacheAfter.Digest, "commands": r.PublishedCount, "max_depth": r.Limits.MaxDepth, "max_commands": r.Limits.MaxCommands, "max_output_bytes": r.Limits.MaxOutputBytes, "timeout_millis": r.Limits.TimeoutMillis, "approved": true}
	if r.Provider == "gh" {
		v["aliases"] = r.PublishedAliasCount
		v["flags"] = r.PublishedFlagCount
		v["max_aliases"] = r.Limits.MaxAliases
		v["max_flags"] = r.Limits.MaxFlags
	}
	return s.History.Append(history.Record{SchemaVersion: 1, ExecutionID: history.ExecutionID(s.now(), r.Plan.Digest, "registry.sync"), PlanID: r.Plan.ID, PlanDigest: r.Plan.Digest, StartedAt: r.Plan.CreatedAt, FinishedAt: s.now(), Invocation: v, Status: r.Result.Status, Steps: r.Result.Steps, RecoveryHints: r.Result.RecoveryHints})
}
func (s SyncService) withDefaults() SyncService {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.TempDir == nil {
		s.TempDir = func() (string, error) { return os.MkdirTemp("", "devtize-sync-") }
	}
	return s
}
func (s SyncService) now() time.Time { return s.Now().UTC() }
func cacheState(v registry.CacheLoadResult) SyncCacheState {
	r := SyncCacheState{Status: v.Status, FilesScanned: v.Files}
	if v.Snapshot != nil {
		r.Digest = v.Snapshot.Digest
		r.KnowledgeVersion = v.Snapshot.KnowledgeVersion
		r.Commands = len(v.Snapshot.Commands)
		for _, c := range v.Snapshot.Commands {
			r.Aliases += len(c.Aliases)
			r.Flags += len(c.Flags)
		}
	}
	return r
}
func syncInstallationError(p syncProvider, i detect.ToolInstallation) error {
	switch i.Status {
	case detect.ToolInstalled:
		return nil
	case detect.ToolMissing:
		return &Error{Code: CodeToolNotFound, Message: p.executable + " executable was not found", Provider: p.id, Hint: "Install " + p.display + " and rerun dvz sync " + p.id + "."}
	case detect.ToolVersionUnsupported:
		return &Error{Code: CodeToolVersionUnsupported, Message: "installed " + p.display + " version is unsupported", Provider: p.id, Hint: "Install " + p.display + " " + p.minimum + " or newer."}
	default:
		return &Error{Code: CodeSyncFailed, Message: "installed " + p.display + " version could not be validated", Provider: p.id, Hint: "Run " + p.executable + " --version and resolve the reported local tool error."}
	}
}
func syncFailure(p syncProvider, message string, cause error) *Error {
	var e *devprocess.RunError
	if errors.As(cause, &e) && e.Kind == devprocess.ErrorTimeout {
		return &Error{Code: CodeSyncFailed, Message: message, Provider: p.id, Retryable: true, Cause: cause, Hint: p.display + " help introspection timed out; resolve the local tool issue and retry."}
	}
	return &Error{Code: CodeSyncFailed, Message: message, Provider: p.id, Cause: cause, Hint: fmt.Sprintf("The last valid %s cache was retained. Resolve the local tool or cache error and retry.", p.id)}
}
