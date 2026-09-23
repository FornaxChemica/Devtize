package app

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 200
)

type HistoryOptions struct {
	Limit int
	All   bool
}

type HistoryEntry struct {
	SchemaVersion   int                      `json:"schema_version"`
	WorkflowID      string                   `json:"workflow_id,omitempty"`
	ExecutionID     string                   `json:"execution_id"`
	Workflow        string                   `json:"workflow"`
	ProjectRoot     string                   `json:"project_root,omitempty"`
	PlanID          string                   `json:"plan_id"`
	PlanDigest      string                   `json:"plan_digest"`
	StartedAt       time.Time                `json:"started_at"`
	FinishedAt      time.Time                `json:"finished_at"`
	Status          operation.Status         `json:"status"`
	Steps           []operation.StepResult   `json:"steps"`
	RecoveryHints   []string                 `json:"recovery_hints"`
	ObservedChanges []history.ObservedChange `json:"observed_changes,omitempty"`
}

type HistoryResponse struct {
	SchemaVersion    int            `json:"schema_version"`
	Scope            string         `json:"scope"`
	ProjectRoot      string         `json:"project_root,omitempty"`
	Limit            int            `json:"limit"`
	RecordingEnabled bool           `json:"recording_enabled"`
	HasMore          bool           `json:"has_more"`
	Records          []HistoryEntry `json:"records"`
}

type HistoryReader interface {
	List(history.ListOptions) (history.ListResult, error)
}

type HistoryService struct {
	WorkingDir string
	Config     config.Config
	History    HistoryReader
}

func (s HistoryService) Run(options HistoryOptions) (HistoryResponse, error) {
	root, err := filepath.Abs(s.WorkingDir)
	if err != nil {
		return HistoryResponse{}, Wrap(CodeProjectNotFound, "project root could not be resolved", err)
	}
	limit := options.Limit
	if limit == 0 {
		limit = defaultHistoryLimit
	}
	if limit < 1 || limit > maxHistoryLimit {
		return HistoryResponse{}, &Error{Code: CodeInvalidUsage, Message: "history limit must be between 1 and 200"}
	}
	scope := "project"
	projectFilter := filepath.Clean(root)
	projectRoot := projectFilter
	if options.All {
		scope = "all"
		projectFilter = ""
		projectRoot = ""
	}
	response := HistoryResponse{
		SchemaVersion: 1, Scope: scope, ProjectRoot: projectRoot, Limit: limit,
		RecordingEnabled: s.Config.History.Enabled, Records: []HistoryEntry{},
	}
	if s.History == nil {
		return response, nil
	}
	result, err := s.History.List(history.ListOptions{ProjectRoot: projectFilter, Limit: limit})
	if err != nil {
		if errors.Is(err, history.ErrInvalid) {
			return response, &Error{Code: CodeHistoryInvalid, Message: "history contains an invalid or unsupported record", Cause: err, Hint: "Inspect or archive the local history file; Devtize did not modify it."}
		}
		return response, &Error{Code: CodeHistoryReadFailed, Message: "history could not be read", Cause: err, Hint: "Check that the local history file is readable."}
	}
	response.HasMore = result.HasMore
	for _, record := range result.Records {
		response.Records = append(response.Records, historyEntry(record))
	}
	return response, nil
}

func historyEntry(record history.Record) HistoryEntry {
	steps := append([]operation.StepResult{}, record.Steps...)
	hints := append([]string{}, record.RecoveryHints...)
	return HistoryEntry{
		SchemaVersion: record.SchemaVersion, WorkflowID: record.WorkflowID, ExecutionID: record.ExecutionID, Workflow: historyWorkflow(record),
		ProjectRoot: record.Project["root"], PlanID: record.PlanID, PlanDigest: record.PlanDigest,
		StartedAt: record.StartedAt, FinishedAt: record.FinishedAt, Status: record.Status,
		Steps: steps, RecoveryHints: hints, ObservedChanges: append([]history.ObservedChange(nil), record.ObservedChanges...),
	}
}

func historyWorkflow(record history.Record) string {
	if workflow, ok := record.Invocation["workflow"].(string); ok && strings.TrimSpace(workflow) != "" {
		return workflow
	}
	for prefix, workflow := range map[string]string{
		"plan_repo_set_description_": "repo.set-description",
		"plan_repo_redact_initial_":  "repo.redact-initial",
		"plan_repo_create_":          "repo.create",
		"plan_commit_":               "commit",
		"plan_ship_":                 "ship",
	} {
		if strings.HasPrefix(record.PlanID, prefix) {
			return workflow
		}
	}
	return "unknown"
}
