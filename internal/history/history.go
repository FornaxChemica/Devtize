package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FornaxChemica/devtize/internal/operation"
)

type Record struct {
	SchemaVersion int                    `json:"schema_version"`
	ExecutionID   string                 `json:"execution_id"`
	PlanID        string                 `json:"plan_id"`
	PlanDigest    string                 `json:"plan_digest"`
	StartedAt     time.Time              `json:"started_at"`
	FinishedAt    time.Time              `json:"finished_at"`
	Invocation    map[string]any         `json:"invocation,omitempty"`
	Project       map[string]string      `json:"project,omitempty"`
	Status        operation.Status       `json:"status"`
	Steps         []operation.StepResult `json:"steps,omitempty"`
	RecoveryHints []string               `json:"recovery_hints,omitempty"`
}

type Store struct {
	Path string
	Now  func() time.Time
}

func (s Store) Append(record Record) error {
	if s.Path == "" {
		return nil
	}
	if record.SchemaVersion == 0 {
		record.SchemaVersion = 1
	}
	record = redactRecord(record)
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create history directory: %w", err)
	}
	file, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

func redactRecord(record Record) Record {
	for i := range record.Steps {
		record.Steps[i].ErrorMessage = Redact(record.Steps[i].ErrorMessage)
		record.Steps[i].RecoveryHint = Redact(record.Steps[i].RecoveryHint)
	}
	for i := range record.RecoveryHints {
		record.RecoveryHints[i] = Redact(record.RecoveryHints[i])
	}
	return record
}

func Redact(value string) string {
	words := []string{"token", "password", "secret", "authorization", "bearer"}
	fields := strings.Fields(value)
	for i, field := range fields {
		lower := strings.ToLower(field)
		for _, word := range words {
			if strings.Contains(lower, word) {
				fields[i] = "<redacted>"
				break
			}
		}
	}
	return strings.Join(fields, " ")
}
