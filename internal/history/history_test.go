package history

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/operation"
)

func TestListReturnsNewestMatchingRecordsAndRedactsAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store := Store{Path: path}
	for _, record := range []Record{
		{ExecutionID: "one", PlanID: "plan_commit_1", Project: map[string]string{"root": "/one"}, Status: operation.StatusSucceeded},
		{ExecutionID: "two", PlanID: "plan_ship_2", Project: map[string]string{"root": "/two"}, Status: operation.StatusSucceeded},
		{ExecutionID: "three", PlanID: "plan_commit_3", Project: map[string]string{"root": "/one"}, Invocation: map[string]any{"nested": map[string]any{"access_token": "raw-value"}}, RecoveryHints: []string{"authorization bearer raw-value"}, Status: operation.StatusFailed},
	} {
		if err := store.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.List(ListOptions{ProjectRoot: "/one", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasMore || len(result.Records) != 1 || result.Records[0].ExecutionID != "three" {
		t.Fatalf("result = %#v", result)
	}
	nested := result.Records[0].Invocation["nested"].(map[string]any)
	if nested["access_token"] != "<redacted>" || strings.Contains(strings.Join(result.Records[0].RecoveryHints, " "), "raw-value") {
		t.Fatalf("record was not redacted: %#v", result.Records[0])
	}
}

func TestListMissingFileIsEmpty(t *testing.T) {
	result, err := (Store{Path: filepath.Join(t.TempDir(), "missing.jsonl")}).List(ListOptions{Limit: 20})
	if err != nil || len(result.Records) != 0 || result.HasMore {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestListRejectsMalformedAndUnsupportedRecords(t *testing.T) {
	for name, content := range map[string]string{
		"malformed":   "not-json\n",
		"unsupported": `{"schema_version":2}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.jsonl")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := (Store{Path: path}).List(ListOptions{Limit: 20})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestListRejectsOversizedRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	content := `{"schema_version":1,"execution_id":"` + strings.Repeat("x", MaxRecordBytes) + `"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (Store{Path: path}).List(ListOptions{Limit: 20})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestListRejectsOversizedFileBeforeScanning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, MaxScanBytes+1); err != nil {
		t.Fatal(err)
	}
	_, err := (Store{Path: path}).List(ListOptions{Limit: 20})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestAppendDoesNotMutateCallerMaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	record := Record{StartedAt: time.Now(), Invocation: map[string]any{"token": "value"}}
	if err := (Store{Path: path}).Append(record); err != nil {
		t.Fatal(err)
	}
	if record.Invocation["token"] != "value" {
		t.Fatalf("caller record was mutated: %#v", record)
	}
}

func TestFindReturnsOneProjectScopedExecutionAndRejectsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store := Store{Path: path}
	for _, record := range []Record{
		{ExecutionID: "exec_target", Project: map[string]string{"root": "/one"}, Status: operation.StatusSucceeded},
		{ExecutionID: "exec_target", Project: map[string]string{"root": "/two"}, Status: operation.StatusSucceeded},
	} {
		if err := store.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	record, found, err := store.Find(FindOptions{ProjectRoot: "/one", ExecutionID: "exec_target"})
	if err != nil || !found || record.Project["root"] != "/one" {
		t.Fatalf("record=%#v found=%t err=%v", record, found, err)
	}
	if _, _, err := store.Find(FindOptions{ExecutionID: "exec_target"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate lookup err=%v", err)
	}
}

func TestFindPreservesTypedEvidenceAndRedactsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store := Store{Path: path}
	record := Record{
		ExecutionID: "exec_evidence", Project: map[string]string{"root": "/project"},
		ObservedChanges: []ObservedChange{{
			Kind: "git.commit.created", ProviderID: "git", CapabilityID: "git.commit.create",
			Branch: "secret token raw-value", BeforeCommit: strings.Repeat("a", 40), AfterCommit: strings.Repeat("b", 40),
		}},
	}
	if err := store.Append(record); err != nil {
		t.Fatal(err)
	}
	found, ok, err := store.Find(FindOptions{ProjectRoot: "/project", ExecutionID: "exec_evidence"})
	if err != nil || !ok || len(found.ObservedChanges) != 1 {
		t.Fatalf("record=%#v ok=%t err=%v", found, ok, err)
	}
	if strings.Contains(found.ObservedChanges[0].Branch, "raw-value") {
		t.Fatalf("evidence was not redacted: %#v", found.ObservedChanges[0])
	}
}

func TestExecutionIDUsesDigestAndDiscriminator(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	first := ExecutionID(now, "sha256:one", "commit")
	second := ExecutionID(now, "sha256:two", "commit")
	third := ExecutionID(now, "sha256:one", "ship")
	if first == second || first == third || !strings.HasPrefix(first, "exec_20260923010203_") {
		t.Fatalf("IDs are not collision resistant: %q %q %q", first, second, third)
	}
}
