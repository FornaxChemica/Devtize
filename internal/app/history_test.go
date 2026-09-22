package app

import (
	"errors"
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
)

type fakeHistoryReader struct {
	options history.ListOptions
	result  history.ListResult
	err     error
}

func (f *fakeHistoryReader) List(options history.ListOptions) (history.ListResult, error) {
	f.options = options
	return f.result, f.err
}

func TestHistoryDefaultsToCurrentProjectAndDerivesLegacyWorkflow(t *testing.T) {
	reader := &fakeHistoryReader{result: history.ListResult{Records: []history.Record{{
		SchemaVersion: 1, ExecutionID: "exec", PlanID: "plan_repo_create_1", PlanDigest: "sha256:test",
		Project: map[string]string{"root": "/project"}, FinishedAt: time.Unix(1, 0), Status: operation.StatusSucceeded,
	}}}}
	service := HistoryService{WorkingDir: "/project", Config: config.Defaults(), History: reader}
	response, err := service.Run(HistoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Scope != "project" || response.Limit != 20 || reader.options.ProjectRoot != "/project" || response.Records[0].Workflow != "repo.create" {
		t.Fatalf("response=%#v options=%#v", response, reader.options)
	}
}

func TestHistoryAllProjectsRemovesFilterAndPreservesDisabledState(t *testing.T) {
	reader := &fakeHistoryReader{}
	response, err := (HistoryService{WorkingDir: t.TempDir(), Config: config.Config{}, History: reader}).Run(HistoryOptions{All: true, Limit: 3})
	if err != nil || response.Scope != "all" || response.ProjectRoot != "" || reader.options.ProjectRoot != "" || response.RecordingEnabled {
		t.Fatalf("response=%#v options=%#v err=%v", response, reader.options, err)
	}
}

func TestHistoryMapsInvalidAndReadFailures(t *testing.T) {
	for name, test := range map[string]struct {
		err  error
		code ErrorCode
	}{
		"invalid": {err: history.ErrInvalid, code: CodeHistoryInvalid},
		"read":    {err: errors.New("denied"), code: CodeHistoryReadFailed},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (HistoryService{WorkingDir: t.TempDir(), Config: config.Defaults(), History: &fakeHistoryReader{err: test.err}}).Run(HistoryOptions{})
			var operational *Error
			if !errors.As(err, &operational) || operational.Code != test.code {
				t.Fatalf("err=%#v", err)
			}
		})
	}
}

func TestHistoryRejectsOutOfRangeLimitBeforeReading(t *testing.T) {
	reader := &fakeHistoryReader{}
	_, err := (HistoryService{WorkingDir: t.TempDir(), Config: config.Defaults(), History: reader}).Run(HistoryOptions{Limit: 201})
	var operational *Error
	if !errors.As(err, &operational) || operational.Code != CodeInvalidUsage {
		t.Fatalf("err=%#v", err)
	}
}
