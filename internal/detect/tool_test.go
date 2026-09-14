package detect

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeRunner struct {
	result devprocess.CommandResult
	err    error
	specs  []devprocess.CommandSpec
}

func (f *fakeRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	f.specs = append(f.specs, spec)
	return f.result, f.err
}

func TestToolDetectorUsesExactVersionInvocation(t *testing.T) {
	runner := &fakeRunner{result: devprocess.CommandResult{Executable: "/usr/bin/git", Stdout: "git version 2.51.0\n"}}
	detector := ToolDetector{Runner: runner, Now: func() time.Time { return time.Unix(1, 0) }}
	installation := detector.Detect(context.Background(), ToolSpec{
		ProviderID: "git", Executable: "git", VersionArgs: []string{"--version"}, MinimumVersion: "2.23.0", WorkingDir: "/work",
	})
	if installation.Status != ToolInstalled || installation.Version != "2.51.0" || installation.Path != "/usr/bin/git" {
		t.Fatalf("unexpected installation: %#v", installation)
	}
	if len(runner.specs) != 1 || runner.specs[0].Executable != "git" || !reflect.DeepEqual(runner.specs[0].Args, []string{"--version"}) || runner.specs[0].Dir != "/work" {
		t.Fatalf("unexpected command spec: %#v", runner.specs)
	}
}

func TestToolDetectorStates(t *testing.T) {
	tests := []struct {
		name   string
		runner *fakeRunner
		want   ToolStatus
	}{
		{"missing", &fakeRunner{err: &devprocess.RunError{Kind: devprocess.ErrorMissing, Message: "missing"}}, ToolMissing},
		{"unparseable", &fakeRunner{result: devprocess.CommandResult{Executable: "/bin/git", Stdout: "git unknown"}}, ToolVersionUnparseable},
		{"unsupported", &fakeRunner{result: devprocess.CommandResult{Executable: "/bin/git", Stdout: "git version 2.22.9"}}, ToolVersionUnsupported},
		{"failure", &fakeRunner{err: errors.New("failed")}, ToolError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := (ToolDetector{Runner: test.runner}).Detect(context.Background(), ToolSpec{ProviderID: "git", Executable: "git", VersionArgs: []string{"--version"}, MinimumVersion: "2.23.0", WorkingDir: "/work"})
			if got.Status != test.want {
				t.Fatalf("status = %s, want %s (%#v)", got.Status, test.want, got)
			}
		})
	}
}

func TestGitHubAuthIsConservativelyUnknown(t *testing.T) {
	runner := &fakeRunner{result: devprocess.CommandResult{Executable: "/bin/gh", Stdout: "gh version 2.80.0"}}
	got := (ToolDetector{Runner: runner}).Detect(context.Background(), ToolSpec{ProviderID: "gh", Executable: "gh", VersionArgs: []string{"--version"}, MinimumVersion: "2.0.0", WorkingDir: "/work"})
	if got.AuthStatus != "auth_unknown" || len(runner.specs) != 1 {
		t.Fatalf("auth result = %#v, calls = %d", got, len(runner.specs))
	}
}
