package github

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type Runner interface {
	Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
}

type Adapter struct {
	Runner     Runner
	Executable string
	Timeout    time.Duration
}

func New(runner Runner) Adapter {
	return Adapter{Runner: runner, Executable: "gh", Timeout: 10 * time.Second}
}

type AuthInput struct {
	ProjectRoot string
}

type AuthResult struct {
	Status      string `json:"status"`
	Owner       string `json:"owner,omitempty"`
	GitProtocol string `json:"git_protocol,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type RepoInput struct {
	ProjectRoot string
	Owner       string
	Name        string
	Visibility  string
	Description string
	Homepage    string
}

type RepoResult struct {
	Exists        bool   `json:"exists"`
	NameWithOwner string `json:"name_with_owner,omitempty"`
	Visibility    string `json:"visibility,omitempty"`
	Description   string `json:"description,omitempty"`
	URL           string `json:"url,omitempty"`
	SSHURL        string `json:"ssh_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

func (a Adapter) InspectAuth(ctx context.Context, input AuthInput) (AuthResult, error) {
	result, err := a.run(ctx, input.ProjectRoot, "auth", "status")
	if err != nil {
		var runErr *devprocess.RunError
		if errors.As(err, &runErr) && runErr.Kind == devprocess.ErrorExit {
			return AuthResult{Status: "unauthenticated", Detail: redact(result.Stderr + result.Stdout)}, nil
		}
		return AuthResult{Status: "auth_unknown", Detail: "authentication could not be determined"}, err
	}
	detail := result.Stdout + "\n" + result.Stderr
	owner := parseAccount(detail)
	return AuthResult{Status: "authenticated", Owner: owner, GitProtocol: parseGitProtocol(detail)}, nil
}

func (a Adapter) InspectRepo(ctx context.Context, input RepoInput) (RepoResult, error) {
	result, err := a.run(ctx, input.ProjectRoot, "repo", "view", input.Owner+"/"+input.Name, "--json", "nameWithOwner,visibility,description,url,sshUrl,defaultBranchRef")
	if err != nil {
		var runErr *devprocess.RunError
		if errors.As(err, &runErr) && runErr.Kind == devprocess.ErrorExit {
			return RepoResult{Exists: false}, nil
		}
		return RepoResult{}, err
	}
	var payload struct {
		NameWithOwner string `json:"nameWithOwner"`
		Visibility    string `json:"visibility"`
		Description   string `json:"description"`
		URL           string `json:"url"`
		SSHURL        string `json:"sshUrl"`
		DefaultBranch *struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return RepoResult{}, err
	}
	repo := RepoResult{
		Exists: true, NameWithOwner: payload.NameWithOwner, Visibility: strings.ToLower(payload.Visibility),
		Description: payload.Description, URL: payload.URL, SSHURL: payload.SSHURL,
	}
	if payload.DefaultBranch != nil {
		repo.DefaultBranch = payload.DefaultBranch.Name
	}
	return repo, nil
}

func (a Adapter) UpdateRepoDescription(ctx context.Context, input RepoInput) (RepoResult, error) {
	_, err := a.run(ctx, input.ProjectRoot, "repo", "edit", input.Owner+"/"+input.Name, "--description", input.Description)
	if err != nil {
		return RepoResult{}, err
	}
	return a.InspectRepo(ctx, input)
}

func (a Adapter) CreateRepo(ctx context.Context, input RepoInput) (RepoResult, error) {
	args := []string{"repo", "create", input.Owner + "/" + input.Name}
	if input.Visibility == "public" {
		args = append(args, "--public")
	} else {
		args = append(args, "--private")
	}
	if input.Description != "" {
		args = append(args, "--description", input.Description)
	}
	if input.Homepage != "" {
		args = append(args, "--homepage", input.Homepage)
	}
	_, err := a.run(ctx, input.ProjectRoot, args...)
	if err != nil {
		return RepoResult{}, err
	}
	return a.InspectRepo(ctx, input)
}

func (a Adapter) run(ctx context.Context, dir string, args ...string) (devprocess.CommandResult, error) {
	if a.Runner == nil {
		return devprocess.CommandResult{}, errors.New("github runner is required")
	}
	executable := a.Executable
	if executable == "" {
		executable = "gh"
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return a.Runner.Run(ctx, devprocess.CommandSpec{
		Executable: executable, Args: args, Dir: dir, Timeout: timeout, Stdin: devprocess.StdinDisabled,
		EnvAllowlist: []string{"PATH", "SYSTEMROOT", "WINDIR", "HOME", "GH_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME"},
		CaptureLimit: 64 << 10,
	})
}

func parseAccount(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Logged in to") && strings.Contains(line, " as ") {
			_, after, _ := strings.Cut(line, " as ")
			return strings.Fields(after)[0]
		}
	}
	return ""
}

func parseGitProtocol(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Git operations protocol:") {
			_, protocol, _ := strings.Cut(line, ":")
			protocol = strings.ToLower(strings.TrimSpace(protocol))
			if protocol == "https" || protocol == "ssh" {
				return protocol
			}
		}
	}
	return ""
}

func redact(value string) string {
	for _, word := range []string{"token", "password", "secret", "oauth_token"} {
		value = strings.ReplaceAll(value, word, "<redacted>")
	}
	return value
}
