package github

import (
	"context"
	"reflect"
	"testing"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type fakeRunner struct {
	specs []devprocess.CommandSpec
	out   devprocess.CommandResult
	err   error
}

func (r *fakeRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	r.specs = append(r.specs, spec)
	return r.out, r.err
}

func TestCreateRepoUsesExactGhArgvWithoutSourceSideEffect(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: `{"nameWithOwner":"OWNER/repo","visibility":"PRIVATE","url":"https://github.com/OWNER/repo","sshUrl":"git@github.com:OWNER/repo.git"}`}}
	adapter := Adapter{Runner: runner, Executable: "gh", Timeout: time.Second}
	_, err := adapter.CreateRepo(context.Background(), RepoInput{ProjectRoot: "/tmp/project", Owner: "OWNER", Name: "repo", Visibility: "private"})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	want := []string{"repo", "create", "OWNER/repo", "--private"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
	for _, arg := range runner.specs[0].Args {
		if arg == "--source" {
			t.Fatalf("repo create configured local source as a side effect: %#v", runner.specs[0].Args)
		}
	}
}

func TestInspectRepoParsesJSON(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: `{"nameWithOwner":"OWNER/repo","visibility":"PUBLIC","url":"https://github.com/OWNER/repo","sshUrl":"git@github.com:OWNER/repo.git","defaultBranchRef":{"name":"main"}}`}}
	adapter := Adapter{Runner: runner, Executable: "gh", Timeout: time.Second}
	result, err := adapter.InspectRepo(context.Background(), RepoInput{ProjectRoot: "/tmp/project", Owner: "OWNER", Name: "repo"})
	if err != nil {
		t.Fatalf("inspect repo: %v", err)
	}
	if !result.Exists || result.Visibility != "public" || result.SSHURL == "" || result.DefaultBranch != "main" {
		t.Fatalf("unexpected result: %#v", result)
	}
	want := []string{"repo", "view", "OWNER/repo", "--json", "nameWithOwner,visibility,description,url,sshUrl,defaultBranchRef"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
}

func TestUpdateRepoDescriptionUsesExactLiteralArgv(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: `{"nameWithOwner":"OWNER/repo","visibility":"PUBLIC","description":"new; $(unsafe)","url":"https://github.com/OWNER/repo"}`}}
	adapter := Adapter{Runner: runner, Executable: "gh", Timeout: time.Second}
	result, err := adapter.UpdateRepoDescription(context.Background(), RepoInput{ProjectRoot: "/tmp/project", Owner: "OWNER", Name: "repo", Description: "new; $(unsafe)"})
	if err != nil {
		t.Fatalf("update description: %v", err)
	}
	want := []string{"repo", "edit", "OWNER/repo", "--description", "new; $(unsafe)"}
	if !reflect.DeepEqual(runner.specs[0].Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.specs[0].Args, want)
	}
	if result.Description != "new; $(unsafe)" {
		t.Fatalf("description = %q", result.Description)
	}
}

func TestInspectAuthParsesGitProtocol(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: "Logged in to github.com as OWNER\nGit operations protocol: https\n"}}
	adapter := Adapter{Runner: runner, Executable: "gh", Timeout: time.Second}
	result, err := adapter.InspectAuth(context.Background(), AuthInput{ProjectRoot: "/tmp/project"})
	if err != nil {
		t.Fatalf("inspect auth: %v", err)
	}
	if result.Owner != "OWNER" || result.GitProtocol != "https" {
		t.Fatalf("auth result = %#v", result)
	}
}
