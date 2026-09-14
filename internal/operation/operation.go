package operation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/FornaxChemica/devtize/internal/safety"
)

type Status string

const (
	StatusProposed           Status = "proposed"
	StatusValidated          Status = "validated"
	StatusPolicyEvaluated    Status = "policy_evaluated"
	StatusAuthorized         Status = "authorized"
	StatusRunning            Status = "running"
	StatusSucceeded          Status = "succeeded"
	StatusFailed             Status = "failed"
	StatusCancelled          Status = "cancelled"
	StatusPartiallyCompleted Status = "partially_completed"
	StatusSkipped            Status = "skipped"
)

type Effect struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

type Operation struct {
	ID           string         `json:"id"`
	CapabilityID string         `json:"capability_id"`
	ProviderID   string         `json:"provider_id"`
	Summary      string         `json:"summary"`
	Risk         safety.Risk    `json:"risk"`
	Effects      []Effect       `json:"effects,omitempty"`
	Inputs       map[string]any `json:"inputs"`
}

type Plan struct {
	SchemaVersion int         `json:"schema_version"`
	ID            string      `json:"id"`
	Intent        string      `json:"intent"`
	CreatedAt     time.Time   `json:"created_at"`
	ProjectRoot   string      `json:"project_root"`
	DryRun        bool        `json:"dry_run"`
	Digest        string      `json:"digest"`
	Operations    []Operation `json:"operations"`
}

func (p Plan) WithDigest() (Plan, error) {
	digest, err := Digest(p)
	if err != nil {
		return p, err
	}
	p.Digest = digest
	return p, nil
}

func Digest(plan Plan) (string, error) {
	plan.Digest = ""
	content, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("canonicalize plan: %w", err)
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type StepResult struct {
	OperationID  string    `json:"operation_id"`
	CapabilityID string    `json:"capability_id"`
	Status       Status    `json:"status"`
	Summary      string    `json:"summary"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	RecoveryHint string    `json:"recovery_hint,omitempty"`
}

type ExecutionResult struct {
	SchemaVersion int          `json:"schema_version"`
	PlanID        string       `json:"plan_id"`
	PlanDigest    string       `json:"plan_digest"`
	Status        Status       `json:"status"`
	Steps         []StepResult `json:"steps,omitempty"`
	RecoveryHints []string     `json:"recovery_hints,omitempty"`
}
