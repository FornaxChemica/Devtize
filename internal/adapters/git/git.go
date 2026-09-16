package git

import (
	"context"
	"errors"
	"fmt"
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
	return Adapter{Runner: runner, Executable: "git", Timeout: 10 * time.Second}
}

type InspectRepoInput struct {
	ProjectRoot string
	RemoteName  string
}

type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type InspectRepoResult struct {
	IsRepository      bool     `json:"is_repository"`
	HasCommits        bool     `json:"has_commits"`
	CommitCount       int      `json:"commit_count"`
	HeadBranch        string   `json:"head_branch,omitempty"`
	HeadCommit        string   `json:"head_commit,omitempty"`
	IsDetachedHead    bool     `json:"is_detached_head"`
	WorkingTreeStatus string   `json:"working_tree_status"`
	TrackedPaths      []string `json:"tracked_paths,omitempty"`
	TrackedChanges    []string `json:"tracked_changes,omitempty"`
	UntrackedPaths    []string `json:"untracked_paths,omitempty"`
	IgnoredPaths      []string `json:"ignored_paths,omitempty"`
	RemoteURLs        []string `json:"remote_urls,omitempty"`
	Upstream          string   `json:"upstream,omitempty"`
	UpstreamCommit    string   `json:"upstream_commit,omitempty"`
}

type InitRepoInput struct {
	ProjectRoot   string
	InitialBranch string
}

type StageInput struct {
	ProjectRoot string
	Paths       []string
}

type CommitInput struct {
	ProjectRoot string
	Message     string
}

type UntrackInput struct {
	ProjectRoot string
	Paths       []string
}

type RemoteInput struct {
	ProjectRoot string
	RemoteName  string
	URL         string
}

type PushInput struct {
	ProjectRoot string
	RemoteName  string
	Branch      string
}

type ForcePushWithLeaseInput struct {
	ProjectRoot    string
	RemoteName     string
	Branch         string
	ExpectedCommit string
}

type InspectRemoteBranchInput struct {
	ProjectRoot string
	RemoteName  string
	Branch      string
}

type PushExistingBranchInput struct {
	ProjectRoot string
	RemoteName  string
	Branch      string
}

type InspectOutgoingCommitsInput struct {
	ProjectRoot string
	BaseCommit  string
	HeadCommit  string
}

type CommitSummary struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

type ChangeSet struct {
	Staged    []string `json:"staged,omitempty"`
	Unstaged  []string `json:"unstaged,omitempty"`
	Untracked []string `json:"untracked,omitempty"`
	Ignored   []string `json:"ignored,omitempty"`
}

func (a Adapter) InspectRepo(ctx context.Context, input InspectRepoInput) (InspectRepoResult, error) {
	result := InspectRepoResult{WorkingTreeStatus: "unknown"}
	if _, err := a.run(ctx, input.ProjectRoot, "rev-parse", "--is-inside-work-tree"); err != nil {
		return result, nil
	}
	result.IsRepository = true
	if head, err := a.run(ctx, input.ProjectRoot, "rev-parse", "--verify", "HEAD"); err == nil {
		result.HasCommits = true
		result.HeadCommit = strings.TrimSpace(head.Stdout)
		if count, countErr := a.run(ctx, input.ProjectRoot, "rev-list", "--count", "HEAD"); countErr == nil {
			_, _ = fmt.Sscanf(strings.TrimSpace(count.Stdout), "%d", &result.CommitCount)
		}
	}
	if tracked, err := a.run(ctx, input.ProjectRoot, "ls-files", "-z"); err == nil {
		result.TrackedPaths = nulFields(tracked.Stdout)
	}
	if branch, err := a.run(ctx, input.ProjectRoot, "branch", "--show-current"); err == nil {
		result.HeadBranch = strings.TrimSpace(branch.Stdout)
	}
	if result.HasCommits && result.HeadBranch == "" {
		result.IsDetachedHead = true
	}
	if status, err := a.run(ctx, input.ProjectRoot, "status", "--porcelain=v1", "--untracked-files=all"); err == nil {
		result.TrackedChanges, result.UntrackedPaths = parseStatus(status.Stdout)
		if len(result.TrackedChanges) == 0 && len(result.UntrackedPaths) == 0 {
			result.WorkingTreeStatus = "clean"
		} else {
			result.WorkingTreeStatus = "dirty"
		}
	}
	if ignored, err := a.run(ctx, input.ProjectRoot, "status", "--porcelain=v1", "--ignored", "--untracked-files=all"); err == nil {
		result.IgnoredPaths = parseIgnored(statusLines(ignored.Stdout))
	}
	if input.RemoteName != "" {
		if remote, err := a.InspectRemote(ctx, RemoteInput{ProjectRoot: input.ProjectRoot, RemoteName: input.RemoteName}); err == nil {
			result.RemoteURLs = remote
		}
	}
	if upstream, err := a.run(ctx, input.ProjectRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		result.Upstream = strings.TrimSpace(upstream.Stdout)
		if commit, commitErr := a.run(ctx, input.ProjectRoot, "rev-parse", "@{u}"); commitErr == nil {
			result.UpstreamCommit = strings.TrimSpace(commit.Stdout)
		}
	}
	return result, nil
}

func (a Adapter) InitRepo(ctx context.Context, input InitRepoInput) error {
	_, err := a.run(ctx, input.ProjectRoot, "init", "--initial-branch", input.InitialBranch)
	if err != nil {
		if _, fallbackErr := a.run(ctx, input.ProjectRoot, "init"); fallbackErr != nil {
			return err
		}
		_, err = a.run(ctx, input.ProjectRoot, "symbolic-ref", "HEAD", "refs/heads/"+input.InitialBranch)
	}
	return err
}

func (a Adapter) Stage(ctx context.Context, input StageInput) error {
	args := append([]string{"add", "--"}, input.Paths...)
	_, err := a.run(ctx, input.ProjectRoot, args...)
	return err
}

func (a Adapter) CreateCommit(ctx context.Context, input CommitInput) error {
	_, err := a.run(ctx, input.ProjectRoot, "commit", "--message", input.Message)
	return err
}

func (a Adapter) Untrack(ctx context.Context, input UntrackInput) error {
	const batchSize = 100
	for start := 0; start < len(input.Paths); start += batchSize {
		end := start + batchSize
		if end > len(input.Paths) {
			end = len(input.Paths)
		}
		args := append([]string{"rm", "--cached", "--ignore-unmatch", "--"}, input.Paths[start:end]...)
		if _, err := a.run(ctx, input.ProjectRoot, args...); err != nil {
			return err
		}
	}
	return nil
}

func (a Adapter) AmendCommit(ctx context.Context, input CommitInput) error {
	_, err := a.run(ctx, input.ProjectRoot, "commit", "--amend", "--message", input.Message)
	return err
}

func (a Adapter) AmendCommitPreservingMessage(ctx context.Context, projectRoot string) error {
	_, err := a.run(ctx, projectRoot, "commit", "--amend", "--no-edit")
	return err
}

func (a Adapter) InspectRemote(ctx context.Context, input RemoteInput) ([]string, error) {
	result, err := a.run(ctx, input.ProjectRoot, "remote", "get-url", "--all", input.RemoteName)
	if err != nil {
		return nil, err
	}
	lines := strings.Fields(result.Stdout)
	return lines, nil
}

func (a Adapter) ConfigureRemote(ctx context.Context, input RemoteInput) error {
	_, err := a.run(ctx, input.ProjectRoot, "remote", "add", input.RemoteName, input.URL)
	return err
}

func (a Adapter) UpdateRemote(ctx context.Context, input RemoteInput) error {
	_, err := a.run(ctx, input.ProjectRoot, "remote", "set-url", input.RemoteName, input.URL)
	return err
}

func (a Adapter) PushBranch(ctx context.Context, input PushInput) error {
	_, err := a.run(ctx, input.ProjectRoot, "push", "--set-upstream", input.RemoteName, input.Branch)
	return err
}

func (a Adapter) PushExistingBranch(ctx context.Context, input PushExistingBranchInput) error {
	if !validRemoteOrBranch(input.RemoteName) || !validRemoteOrBranch(input.Branch) {
		return errors.New("remote and branch must be safe Git names")
	}
	refspec := "refs/heads/" + input.Branch + ":refs/heads/" + input.Branch
	_, err := a.run(ctx, input.ProjectRoot, "push", input.RemoteName, refspec)
	return err
}

func (a Adapter) InspectRemoteBranch(ctx context.Context, input InspectRemoteBranchInput) (string, error) {
	if !validRemoteOrBranch(input.RemoteName) || !validRemoteOrBranch(input.Branch) {
		return "", errors.New("remote and branch must be safe Git names")
	}
	result, err := a.run(ctx, input.ProjectRoot, "ls-remote", "--heads", input.RemoteName, "refs/heads/"+input.Branch)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) < 2 || fields[1] != "refs/heads/"+input.Branch {
		return "", errors.New("remote branch was not found")
	}
	return fields[0], nil
}

func (a Adapter) IsAncestor(ctx context.Context, projectRoot, ancestor, descendant string) (bool, error) {
	if !validCommitID(ancestor) || !validCommitID(descendant) {
		return false, errors.New("ancestor checks require full commit IDs")
	}
	_, err := a.run(ctx, projectRoot, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var runErr *devprocess.RunError
	if errors.As(err, &runErr) && runErr.Kind == devprocess.ErrorExit && runErr.ExitCode == 1 {
		return false, nil
	}
	return false, err
}

func (a Adapter) InspectOutgoingCommits(ctx context.Context, input InspectOutgoingCommitsInput) ([]CommitSummary, error) {
	if !validCommitID(input.BaseCommit) || !validCommitID(input.HeadCommit) {
		return nil, errors.New("outgoing commit inspection requires full commit IDs")
	}
	commitRange := input.BaseCommit + ".." + input.HeadCommit
	result, err := a.run(ctx, input.ProjectRoot, "log", "--reverse", "--format=%H%x00%s%x00", commitRange, "--")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(result.Stdout, "\x00")
	var commits []CommitSummary
	for i := 0; i+1 < len(fields); i += 2 {
		sha := strings.TrimSpace(fields[i])
		subject := strings.TrimRight(fields[i+1], "\r\n")
		if sha == "" {
			continue
		}
		if !validCommitID(sha) {
			return nil, errors.New("git returned an invalid outgoing commit ID")
		}
		commits = append(commits, CommitSummary{SHA: sha, Subject: subject})
	}
	return commits, nil
}

func (a Adapter) ForcePushWithLease(ctx context.Context, input ForcePushWithLeaseInput) error {
	if strings.TrimSpace(input.ExpectedCommit) == "" {
		return errors.New("expected remote commit is required for force-with-lease")
	}
	lease := fmt.Sprintf("--force-with-lease=refs/heads/%s:%s", input.Branch, input.ExpectedCommit)
	_, err := a.run(ctx, input.ProjectRoot, "push", lease, input.RemoteName, input.Branch)
	return err
}

func (a Adapter) InspectChanges(ctx context.Context, projectRoot string) (ChangeSet, error) {
	commands := []struct {
		args   []string
		assign func([]string)
	}{
		{args: []string{"diff", "--cached", "--name-only", "-z", "--"}},
		{args: []string{"diff", "--name-only", "-z", "--"}},
		{args: []string{"ls-files", "--others", "--exclude-standard", "-z", "--"}},
		{args: []string{"ls-files", "--others", "--ignored", "--exclude-standard", "-z", "--"}},
	}
	var changes ChangeSet
	commands[0].assign = func(paths []string) { changes.Staged = paths }
	commands[1].assign = func(paths []string) { changes.Unstaged = paths }
	commands[2].assign = func(paths []string) { changes.Untracked = paths }
	commands[3].assign = func(paths []string) { changes.Ignored = paths }
	for _, command := range commands {
		result, err := a.run(ctx, projectRoot, command.args...)
		if err != nil {
			return ChangeSet{}, err
		}
		command.assign(nulFields(result.Stdout))
	}
	return changes, nil
}

func (a Adapter) InspectHeadMessage(ctx context.Context, projectRoot string) (string, error) {
	result, err := a.run(ctx, projectRoot, "log", "-1", "--format=%B")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(result.Stdout, "\r\n"), nil
}

func (a Adapter) run(ctx context.Context, dir string, args ...string) (devprocess.CommandResult, error) {
	if a.Runner == nil {
		return devprocess.CommandResult{}, errors.New("git runner is required")
	}
	executable := a.Executable
	if executable == "" {
		executable = "git"
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return a.Runner.Run(ctx, devprocess.CommandSpec{
		Executable: executable, Args: args, Dir: dir, Timeout: timeout, Stdin: devprocess.StdinDisabled,
		EnvAllowlist: []string{"PATH", "SYSTEMROOT", "WINDIR", "HOME", "XDG_CONFIG_HOME"},
		CaptureLimit: 4 << 20,
	})
}

func parseStatus(output string) ([]string, []string) {
	var tracked, untracked []string
	for _, line := range statusLines(output) {
		if strings.HasPrefix(line, "?? ") {
			untracked = append(untracked, strings.TrimSpace(strings.TrimPrefix(line, "?? ")))
			continue
		}
		if len(line) > 3 && !strings.HasPrefix(line, "!! ") {
			tracked = append(tracked, strings.TrimSpace(line[3:]))
		}
	}
	return tracked, untracked
}

func parseIgnored(lines []string) []string {
	var ignored []string
	for _, line := range lines {
		if strings.HasPrefix(line, "!! ") {
			ignored = append(ignored, strings.TrimSpace(strings.TrimPrefix(line, "!! ")))
		}
	}
	return ignored
}

func statusLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func nulFields(output string) []string {
	var fields []string
	for _, value := range strings.Split(output, "\x00") {
		if value != "" {
			fields = append(fields, value)
		}
	}
	return fields
}

func validCommitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func validRemoteOrBranch(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, ".lock") || strings.Contains(value, "//") {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func IsMissingRemote(err error) bool {
	var runErr *devprocess.RunError
	return errors.As(err, &runErr) && runErr.Kind == devprocess.ErrorExit
}

func SafeRemoteURL(owner, name string) string {
	return fmt.Sprintf("git@github.com:%s/%s.git", owner, name)
}

func HTTPSRemoteURL(owner, name string) string {
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, name)
}
