package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FornaxChemica/devtize/internal/operation"
)

const (
	MaxRecordBytes = 1 << 20
	MaxScanBytes   = 32 << 20
)

var ErrInvalid = errors.New("invalid history")

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

type ListOptions struct {
	ProjectRoot string
	Limit       int
}

type ListResult struct {
	Records []Record
	HasMore bool
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

func (s Store) List(options ListOptions) (ListResult, error) {
	if options.Limit < 1 {
		return ListResult{}, fmt.Errorf("%w: limit must be positive", ErrInvalid)
	}
	if s.Path == "" {
		return ListResult{}, nil
	}
	file, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return ListResult{}, nil
	}
	if err != nil {
		return ListResult{}, fmt.Errorf("open history: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ListResult{}, fmt.Errorf("inspect history: %w", err)
	}
	if info.Size() > MaxScanBytes {
		return ListResult{}, fmt.Errorf("%w: history exceeds the %d byte read limit", ErrInvalid, MaxScanBytes)
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), MaxRecordBytes)
	records := make([]Record, 0, options.Limit+1)
	line := 0
	for scanner.Scan() {
		line++
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return ListResult{}, fmt.Errorf("%w: malformed record at line %d", ErrInvalid, line)
		}
		if record.SchemaVersion != 1 {
			return ListResult{}, fmt.Errorf("%w: unsupported schema version %d at line %d", ErrInvalid, record.SchemaVersion, line)
		}
		if options.ProjectRoot != "" && filepath.Clean(record.Project["root"]) != filepath.Clean(options.ProjectRoot) {
			continue
		}
		records = append(records, redactRecord(record))
		if len(records) > options.Limit+1 {
			records = records[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return ListResult{}, fmt.Errorf("%w: record exceeds the %d byte read limit", ErrInvalid, MaxRecordBytes)
	}
	hasMore := len(records) > options.Limit
	if hasMore {
		records = records[1:]
	}
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
	return ListResult{Records: records, HasMore: hasMore}, nil
}

func redactRecord(record Record) Record {
	record.Invocation = redactMap(record.Invocation)
	if record.Project != nil {
		project := make(map[string]string, len(record.Project))
		for key, value := range record.Project {
			if sensitiveName(key) {
				project[key] = "<redacted>"
			} else {
				project[key] = Redact(value)
			}
		}
		record.Project = project
	}
	record.Steps = append([]operation.StepResult{}, record.Steps...)
	for i := range record.Steps {
		record.Steps[i].ErrorMessage = Redact(record.Steps[i].ErrorMessage)
		record.Steps[i].RecoveryHint = Redact(record.Steps[i].RecoveryHint)
	}
	record.RecoveryHints = append([]string{}, record.RecoveryHints...)
	for i := range record.RecoveryHints {
		record.RecoveryHints[i] = Redact(record.RecoveryHints[i])
	}
	return record
}

func redactMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if sensitiveName(key) {
			result[key] = "<redacted>"
			continue
		}
		result[key] = redactValue(value)
	}
	return result
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case string:
		return Redact(typed)
	case map[string]any:
		return redactMap(typed)
	case map[string]string:
		result := make(map[string]string, len(typed))
		for key, item := range typed {
			if sensitiveName(key) {
				result[key] = "<redacted>"
			} else {
				result[key] = Redact(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = redactValue(typed[i])
		}
		return result
	case []string:
		result := make([]string, len(typed))
		for i := range typed {
			result[i] = Redact(typed[i])
		}
		return result
	default:
		return value
	}
}

func sensitiveName(value string) bool {
	lower := strings.ToLower(value)
	for _, word := range []string{"token", "password", "secret", "authorization", "bearer"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func Redact(value string) string {
	fields := strings.Fields(value)
	redactNext := false
	for i, field := range fields {
		if redactNext {
			fields[i] = "<redacted>"
			redactNext = false
		}
		if sensitiveName(field) {
			fields[i] = "<redacted>"
			redactNext = true
		}
	}
	return strings.Join(fields, " ")
}
