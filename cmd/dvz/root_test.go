package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/detect"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type spyRunner struct {
	calls int
}

type repoDeclineRunner struct {
	t *testing.T
}

type descriptionDeclineRunner struct {
	t *testing.T
}

func (r *repoDeclineRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	joined := spec.Executable + " " + strings.Join(spec.Args, " ")
	switch joined {
	case "git rev-parse --is-inside-work-tree":
		return devprocess.CommandResult{}, &devprocess.RunError{Kind: devprocess.ErrorExit, ExitCode: 128, Message: "not a repository"}
	case "gh auth status":
		return devprocess.CommandResult{Stdout: "Logged in to github.com as OWNER\nGit operations protocol: https\n"}, nil
	case "gh repo view OWNER/repo --json nameWithOwner,visibility,description,url,sshUrl,defaultBranchRef":
		return devprocess.CommandResult{}, &devprocess.RunError{Kind: devprocess.ErrorExit, ExitCode: 1, Message: "not found"}
	default:
		r.t.Fatalf("mutation or unexpected command ran before declined confirmation: %s", joined)
		return devprocess.CommandResult{}, nil
	}
}

func (r *descriptionDeclineRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	joined := spec.Executable + " " + strings.Join(spec.Args, " ")
	switch joined {
	case "gh auth status":
		return devprocess.CommandResult{Stdout: "Logged in to github.com as OWNER\nGit operations protocol: https\n"}, nil
	case "gh repo view OWNER/Devtize --json nameWithOwner,visibility,description,url,sshUrl,defaultBranchRef":
		return devprocess.CommandResult{Stdout: `{"nameWithOwner":"OWNER/Devtize","visibility":"PUBLIC","description":"old"}`}, nil
	default:
		r.t.Fatalf("mutation or unexpected command ran before declined confirmation: %s", joined)
		return devprocess.CommandResult{}, nil
	}
}

func (s *spyRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	s.calls++
	version := spec.Executable + " version 2.51.0"
	if spec.Executable == "gh" {
		version = "gh version 2.80.0"
	}
	return devprocess.CommandResult{Executable: "/tools/" + spec.Executable, Stdout: version}, nil
}

func testDependencies(t *testing.T, runner detect.Runner) dependencies {
	t.Helper()
	return dependencies{
		workingDir: t.TempDir(), environment: map[string]string{}, runner: runner,
		userConfigDir: func() (string, error) { return t.TempDir(), nil },
	}
}

func execute(t *testing.T, deps dependencies, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command, _, err := newRootCommand(deps, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs(args)
	err = command.Execute()
	return stdout.String(), stderr.String(), err
}

func TestHelpAndVersion(t *testing.T) {
	runner := &spyRunner{}
	stdout, _, err := execute(t, testDependencies(t, runner), "--help")
	if err != nil || !strings.Contains(stdout, "Available Commands:") || !strings.Contains(stdout, "find") || strings.Contains(stdout, "raw") {
		t.Fatalf("help = %q, err = %v", stdout, err)
	}
	stdout, _, err = execute(t, testDependencies(t, runner), "version")
	if err != nil || stdout != "Devtize devel (dvz, commit unknown, built unknown)\n" {
		t.Fatalf("version = %q, err = %v", stdout, err)
	}
}

func TestFindReturnsGitInitAndNeverRunsAProcess(t *testing.T) {
	runner := &spyRunner{}
	stdout, _, err := execute(t, testDependencies(t, runner), "find", "initialize", "git", "repository")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "git init\n") || runner.calls != 0 || strings.Contains(strings.ToLower(stdout), "execute") {
		t.Fatalf("output = %q, process calls = %d", stdout, runner.calls)
	}
}

func TestFindJSONIsVersionedAndHasRequiredMetadata(t *testing.T) {
	runner := &spyRunner{}
	stdout, _, err := execute(t, testDependencies(t, runner), "--json", "find", "initialize", "git", "repository")
	if err != nil {
		t.Fatal(err)
	}
	var response app.FindResponse
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatal(err)
	}
	first := response.Results[0]
	if response.SchemaVersion != 1 || first.Command != "git init" || first.Source.Kind != "builtin" || first.Risk == "" || first.MatchReason == "" || first.Confidence == "" || first.VersionRange == "" || first.VersionStatus != "not_checked" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if runner.calls != 0 || strings.Contains(stdout, "\x1b[") {
		t.Fatalf("process calls = %d; JSON contains ANSI = %t", runner.calls, strings.Contains(stdout, "\x1b["))
	}
}

func TestFindTreatsMetacharactersAsData(t *testing.T) {
	runner := &spyRunner{}
	_, _, err := execute(t, testDependencies(t, runner), "find", "show", "$HOME", ">", "output", "|", "next", ";")
	if err == nil {
		t.Fatal("unmatched metacharacter intent unexpectedly succeeded")
	}
	if runner.calls != 0 {
		t.Fatalf("metacharacter input invoked process runner %d times", runner.calls)
	}
}

func TestDoctorReportsStableStates(t *testing.T) {
	runner := &spyRunner{}
	stdout, _, err := execute(t, testDependencies(t, runner), "--json", "doctor")
	if err != nil {
		t.Fatal(err)
	}
	var response app.DoctorResponse
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != 1 || response.Config.Status != "valid" || response.Project.Status != "not_found" || response.Registry.Entries != 12 || len(response.Tools) != 2 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.Tools[0].ProviderID != "gh" || response.Tools[0].AuthStatus != "auth_unknown" {
		t.Fatalf("unexpected gh result: %#v", response.Tools[0])
	}
}

func TestDoctorReportsInvalidConfigWithoutUsingItsValues(t *testing.T) {
	runner := &spyRunner{}
	deps := testDependencies(t, runner)
	if err := os.WriteFile(filepath.Join(deps.workingDir, ".dvz.yaml"), []byte("version: 2\ntoken: exposed-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := execute(t, deps, "--json", "doctor")
	if err == nil {
		t.Fatal("doctor accepted invalid config")
	}
	if !strings.Contains(stdout, `"status": "invalid"`) || strings.Contains(stdout, "exposed-value") {
		t.Fatalf("unsafe doctor output: %s", stdout)
	}
}

func TestInvalidUseAndJSONErrorMapping(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command, options, err := newRootCommand(testDependencies(t, &spyRunner{}), &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	options.jsonOutput = true
	command.SetArgs([]string{"find"})
	err = command.Execute()
	if err == nil {
		t.Fatal("missing intent succeeded")
	}
	wrapped := app.Wrap(app.CodeInvalidUsage, err.Error(), err)
	renderError(&stderr, true, wrapped)
	if !strings.Contains(stderr.String(), `"schema_version": 1`) || !strings.Contains(stderr.String(), `"code": "INVALID_USAGE"`) {
		t.Fatalf("JSON error = %q", stderr.String())
	}
	if exitCode(wrapped) != exitInvalid {
		t.Fatalf("exit code = %d", exitCode(wrapped))
	}
}

func TestRepoCreateRendersExactPlanBeforeConfirmation(t *testing.T) {
	deps := testDependencies(t, &repoDeclineRunner{t: t})
	if err := os.WriteFile(filepath.Join(deps.workingDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command, _, err := newRootCommand(deps, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	command.SetIn(strings.NewReader("no\n"))
	command.SetArgs([]string{"repo", "create", "--owner", "OWNER", "--name", "repo", "--message", "chore: initial"})
	err = command.Execute()
	if err == nil {
		t.Fatal("declined confirmation unexpectedly succeeded")
	}
	output := stdout.String()
	planAt := strings.Index(output, "Plan ")
	operationsAt := strings.Index(output, "operations:")
	promptAt := strings.Index(output, "Apply local repository changes")
	if planAt < 0 || operationsAt < planAt || promptAt < operationsAt {
		t.Fatalf("plan was not fully rendered before confirmation:\n%s", output)
	}
}

func TestRepoSetDescriptionRendersExactPlanBeforeRemoteConfirmation(t *testing.T) {
	deps := testDependencies(t, &descriptionDeclineRunner{t: t})
	var stdout, stderr bytes.Buffer
	command, _, err := newRootCommand(deps, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	command.SetIn(strings.NewReader("no\n"))
	command.SetArgs([]string{"repo", "set-description", "--owner", "OWNER", "--name", "Devtize", "--description", "One universal command layer"})
	err = command.Execute()
	if err == nil {
		t.Fatal("declined confirmation unexpectedly succeeded")
	}
	output := stdout.String()
	planAt := strings.Index(output, "Plan ")
	descriptionAt := strings.Index(output, "new description: One universal command layer")
	operationsAt := strings.Index(output, "operations:")
	promptAt := strings.Index(output, "Update the GitHub repository description")
	if planAt < 0 || descriptionAt < planAt || operationsAt < descriptionAt || promptAt < operationsAt {
		t.Fatalf("description plan was not fully rendered before confirmation:\n%s", output)
	}
}

func TestRenderRepoPlanCompactsLargePathInventory(t *testing.T) {
	paths := make([]string, 0, 3301)
	for i := 0; i < 3300; i++ {
		paths = append(paths, fmt.Sprintf(".cache/go-build/%04d", i))
	}
	paths = append(paths, "dvz")
	response := app.RepoResponse{
		Plan:      appPlanForRenderTest(),
		Selection: app.RepoSelection{Mode: "disclosed_all", UntrackPaths: paths},
	}
	var output bytes.Buffer
	renderRepoPlan(&output, response)
	text := output.String()
	if !strings.Contains(text, ".cache/go-build/** (3300 paths)") || !strings.Contains(text, "  - dvz\n") {
		t.Fatalf("large path inventory was not meaningfully summarized:\n%s", text)
	}
	if strings.Count(text, "\n") > 25 {
		t.Fatalf("large path inventory rendered %d lines", strings.Count(text, "\n"))
	}
}

func appPlanForRenderTest() operation.Plan {
	return operation.Plan{ID: "plan_test", Digest: "sha256:test", ProjectRoot: "/tmp/project"}
}

func TestGoldenCLIOutput(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"help", []string{"--help"}},
		{"version", []string{"version"}},
		{"find", []string{"find", "initialize", "git", "repository"}},
		{"find-json", []string{"--json", "find", "initialize", "git", "repository"}},
		{"doctor", []string{"doctor"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, _, err := execute(t, testDependencies(t, &spyRunner{}), test.args...)
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, test.name+".golden", stdout)
		})
	}

	var output bytes.Buffer
	renderError(&output, true, &app.Error{Code: app.CodeCapabilityNotFound, Message: "no reviewed command knowledge matched the intent", Hint: "Try a more specific Git intent."})
	assertGolden(t, "error-json.golden", output.String())

	output.Reset()
	renderStatus(&output, app.StatusResponse{
		SchemaVersion: 1, ProjectRoot: "/work/project",
		Repository:         app.RepositoryStatus{IsRepository: true, HasCommits: true, Branch: "main", HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WorkingTree: "dirty"},
		Changes:            app.ChangeStatus{Staged: []string{}, Unstaged: []string{"README.md"}, Untracked: []string{"notes.txt"}, IgnoredCount: 2},
		Upstream:           app.UpstreamStatus{Name: "origin/main", Commit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Ahead: 1, Relation: "ahead"},
		LiveRemote:         app.LiveRemoteStatus{Requested: true, Name: "origin", URLs: []string{"https://example.invalid/project.git"}, Branch: "main", Exists: true, Commit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Relation: "local_ahead", Detail: "The live branch matches local tracking state and local HEAD is ahead."},
		RecommendedActions: []string{"Use dvz commit with explicit paths and a reviewed message when these changes are ready.", "Run dvz ship to review and push the outgoing commits."},
	})
	assertGolden(t, "status.golden", output.String())

	output.Reset()
	renderHistory(&output, app.HistoryResponse{
		SchemaVersion: 1, Scope: "project", ProjectRoot: "/work/project", Limit: 1, RecordingEnabled: true, HasMore: true,
		Records: []app.HistoryEntry{{
			SchemaVersion: 1, ExecutionID: "exec_1", Workflow: "commit", PlanID: "plan_commit_1", PlanDigest: "sha256:test",
			FinishedAt: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC), Status: operation.StatusSucceeded,
			Steps: []operation.StepResult{{CapabilityID: "git.commit.create", Status: operation.StatusSucceeded}},
		}},
	})
	assertGolden(t, "history.golden", output.String())
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("output differs from %s\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}
