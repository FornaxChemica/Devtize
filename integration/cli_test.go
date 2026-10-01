package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuiltBinaryGitKnowledgeSyncAndOfflineFind(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}

	fakeDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "git-calls.log")
	controlPath := filepath.Join(t.TempDir(), "fail-help")
	buildFakeGit(t, fakeDir, logPath, controlPath)
	ghLogPath := filepath.Join(t.TempDir(), "gh-calls.log")
	buildFakeGH(t, fakeDir, ghLogPath)
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	cacheHome := filepath.Join(home, "cache")
	project := t.TempDir()
	before, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(),
		"PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
		"XDG_CONFIG_HOME="+configHome,
		"XDG_CACHE_HOME="+cacheHome,
		"NO_COLOR=1",
	)
	syncCommand := exec.Command(binary, "--json", "sync", "git")
	syncCommand.Dir = project
	syncCommand.Env = environment
	syncCommand.Stdin = strings.NewReader("sync\n")
	output, err := syncCommand.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"status": "succeeded"`) || strings.Contains(string(output), "\x1b[") {
		t.Fatalf("sync: %v\n%s", err, output)
	}

	find := exec.Command(binary, "--json", "find", "git", "config")
	find.Dir = project
	find.Env = environment
	output, err = find.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"command": "git config"`) || !strings.Contains(string(output), `"kind": "sync"`) || !strings.Contains(string(output), `"version_status": "exact"`) {
		t.Fatalf("offline find: %v\n%s", err, output)
	}
	if err := os.WriteFile(controlPath, []byte("fail"), 0o600); err != nil {
		t.Fatal(err)
	}
	failedSync := exec.Command(binary, "--json", "sync", "git", "--dry-run")
	failedSync.Dir = project
	failedSync.Env = environment
	output, err = failedSync.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 6 || !strings.Contains(string(output), `"code": "SYNC_FAILED"`) {
		t.Fatalf("failed sync did not retain cache: %v\n%s", err, output)
	}
	find = exec.Command(binary, "--json", "find", "git", "config")
	find.Dir = project
	find.Env = environment
	output, err = find.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"command": "git config"`) || !strings.Contains(string(output), `"version_status": "exact"`) {
		t.Fatalf("fallback find: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := "--version\nhelp --all --no-external-commands --no-aliases --verbose\n--version\n--version\nhelp --all --no-external-commands --no-aliases --verbose\n"
	if string(calls) != wantCalls {
		t.Fatalf("Git calls = %q, want %q", calls, wantCalls)
	}
	ghSync := exec.Command(binary, "--json", "sync", "gh")
	ghSync.Dir = project
	ghSync.Env = environment
	ghSync.Stdin = strings.NewReader("sync\n")
	output, err = ghSync.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"published_alias_count": 1`) || !strings.Contains(string(output), `"published_flag_count": 1`) {
		t.Fatalf("gh sync: %v\n%s", err, output)
	}
	ghFind := exec.Command(binary, "--json", "find", "--provider", "gh", "gh", "pr", "new")
	ghFind.Dir = project
	ghFind.Env = environment
	output, err = ghFind.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"command": "gh pr create"`) || !strings.Contains(string(output), `"matched_field": "alias"`) {
		t.Fatalf("gh find: %v\n%s", err, output)
	}
	ghCalls, err := os.ReadFile(ghLogPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(ghCalls) != "--version\nhelp reference\n--version\n" {
		t.Fatalf("gh calls=%q", ghCalls)
	}
	after, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("sync changed project directory: before=%d after=%d", len(before), len(after))
	}
	historyPath := filepath.Join(configHome, "devtize", "history.jsonl")
	historyContent, err := os.ReadFile(historyPath)
	if err != nil || !strings.Contains(string(historyContent), `"workflow":"registry.sync"`) || strings.Contains(string(historyContent), "Show the working tree") {
		t.Fatalf("global sync history: %v\n%s", err, historyContent)
	}
}

func buildFakeGH(t *testing.T, directory, logPath string) {
	t.Helper()
	source := fmt.Sprintf(`package main
import("fmt";"os";"strings")
func main(){args:=strings.Join(os.Args[1:]," ");f,err:=os.OpenFile(%q,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{panic(err)};fmt.Fprintln(f,args);f.Close();switch args{case "--version":fmt.Println("gh version 2.93.0");case "help reference":fmt.Print(%q);default:os.Exit(2)}}
`, logPath, `# gh reference

## gh api

Make an authenticated GitHub API request

## gh auth <command>

Authenticate GitHub CLI

### gh auth login

Log in to GitHub

## gh issue <command>

Work with issues

### gh issue create

Create an issue

## gh pr <command>

Work with pull requests

### gh pr create [flags]

Create a pull request

Aliases

gh pr new

  -w, --web   Open a browser

## gh repo <command>

Work with repositories

### gh repo create

Create a repository
`)
	sourcePath := filepath.Join(directory, "fake_gh.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "gh")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.Command("go", "build", "-o", executable, sourcePath)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake gh: %v\n%s", err, output)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
}

func buildFakeGit(t *testing.T, directory, logPath, controlPath string) {
	t.Helper()
	source := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := strings.Join(os.Args[1:], " ")
	file, err := os.OpenFile(%q, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil { panic(err) }
	fmt.Fprintln(file, args)
	file.Close()
	switch args {
	case "--version":
		fmt.Println("git version 2.50.1")
	case "help --all --no-external-commands --no-aliases --verbose":
		if _, err := os.Stat(%q); err == nil {
			fmt.Print("Main Porcelain Commands\n   commit                  Missing required anchor\n")
			return
		}
		fmt.Print("Main Porcelain Commands\n   status                  Show the working tree status\n   config                  Get and set repository or global options\n")
	default:
		os.Exit(2)
	}
}
`, logPath, controlPath)
	sourcePath := filepath.Join(directory, "fake_git.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "git")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.Command("go", "build", "-o", executable, sourcePath)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake git: %v\n%s", err, output)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltBinaryStatusAndHistoryAreReadOnly(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}

	repo := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", bare)
	runGit(t, repo, "init", "--initial-branch", "main")
	runGit(t, repo, "config", "user.name", "Devtize Test")
	runGit(t, repo, "config", "user.email", "devtize-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", "README.md")
	runGit(t, repo, "commit", "--message", "chore: initial fixture")
	runGit(t, repo, "remote", "add", "origin", bare)
	runGit(t, repo, "push", "--set-upstream", "origin", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", "README.md")
	runGit(t, repo, "commit", "--message", "feat: local fixture")
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("working\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBefore := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	worktreeBefore := runGit(t, repo, "status", "--porcelain=v1")

	configHome := t.TempDir()
	environment := append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "NO_COLOR=1")
	status := exec.Command(binary, "--json", "status")
	status.Dir = repo
	status.Env = environment
	output, err := status.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"relation": "ahead"`) || !strings.Contains(string(output), `"untracked.txt"`) || !strings.Contains(string(output), `"requested": false`) {
		t.Fatalf("status: %v\n%s", err, output)
	}
	live := exec.Command(binary, "--json", "status", "--remote")
	live.Dir = repo
	live.Env = environment
	output, err = live.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"relation": "local_ahead"`) {
		t.Fatalf("live status: %v\n%s", err, output)
	}
	if headAfter := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")); headAfter != headBefore {
		t.Fatalf("status changed HEAD from %s to %s", headBefore, headAfter)
	}
	if worktreeAfter := runGit(t, repo, "status", "--porcelain=v1"); worktreeAfter != worktreeBefore {
		t.Fatalf("status changed working tree from %q to %q", worktreeBefore, worktreeAfter)
	}

	historyDir := filepath.Join(configHome, "devtize")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"schema_version": 1, "execution_id": "exec_fixture", "plan_id": "plan_commit_fixture",
		"plan_digest": "sha256:fixture", "project": map[string]string{"root": canonicalRepo},
		"invocation": map[string]any{"workflow": "commit", "access_token": "must-not-appear"}, "status": "succeeded",
	}
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "history.jsonl"), append(content, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	historyCommand := exec.Command(binary, "--json", "history")
	historyCommand.Dir = repo
	historyCommand.Env = environment
	output, err = historyCommand.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"execution_id": "exec_fixture"`) || strings.Contains(string(output), "must-not-appear") || strings.Contains(string(output), "access_token") {
		t.Fatalf("history: %v\n%s", err, output)
	}
}

func TestBuiltBinaryPhaseASmoke(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}

	configHome := t.TempDir()
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "Available Commands:"},
		{[]string{"version"}, "Devtize devel"},
		{[]string{"doctor"}, "Devtize doctor:"},
		{[]string{"find", "initialize", "git", "repository"}, "git init"},
	} {
		command := exec.Command(binary, test.args...)
		command.Dir = t.TempDir()
		command.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configHome, "NO_COLOR=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("dvz %v: %v\n%s", test.args, err, output)
		}
		if !strings.Contains(string(output), test.want) {
			t.Fatalf("dvz %v output %q does not contain %q", test.args, output, test.want)
		}
	}
}

func TestBuiltBinaryReturnsStableInvalidUseExit(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}
	command := exec.Command(binary, "--json", "find")
	command.Dir = t.TempDir()
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("invalid invocation succeeded")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("exit = %v, want 2; output: %s", err, output)
	}
	if !strings.Contains(string(output), `"code": "INVALID_USAGE"`) {
		t.Fatalf("error output = %s", output)
	}

	missing := exec.Command(binary, "--json", "undo", "exec_missing", "--dry-run")
	missing.Dir = t.TempDir()
	missing.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	output, err = missing.CombinedOutput()
	exitErr, ok = err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 || !strings.Contains(string(output), `"code": "HISTORY_ENTRY_NOT_FOUND"`) {
		t.Fatalf("missing history exit=%v output=%s", err, output)
	}
}

func TestBuiltBinaryCommitDryRunAndExecutionInTemporaryRepository(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "--initial-branch", "main")
	runGit(t, repo, "config", "user.name", "Devtize Test")
	runGit(t, repo, "config", "user.email", "devtize-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", "README.md")
	runGit(t, repo, "commit", "--message", "chore: initial fixture")
	before := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configHome := t.TempDir()
	dryRun := exec.Command(binary, "commit", "README.md", "--message", "docs: update readme", "--dry-run")
	dryRun.Dir = repo
	dryRun.Env = append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "NO_COLOR=1")
	output, err := dryRun.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "git.commit.create [local_write]") {
		t.Fatalf("dry-run: %v\n%s", err, output)
	}
	if after := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")); after != before {
		t.Fatalf("dry-run changed HEAD from %s to %s", before, after)
	}

	commit := exec.Command(binary, "commit", "README.md", "--message", "docs: update readme")
	commit.Dir = repo
	commit.Env = append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "NO_COLOR=1")
	commit.Stdin = strings.NewReader("commit\n")
	output, err = commit.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "result: succeeded") {
		t.Fatalf("commit: %v\n%s", err, output)
	}
	if message := strings.TrimSpace(runGit(t, repo, "log", "-1", "--format=%s")); message != "docs: update readme" {
		t.Fatalf("message = %q", message)
	}
}

func TestBuiltBinaryShipDryRunAndPushToLocalBareRemote(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}

	repo := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", bare)
	runGit(t, repo, "init", "--initial-branch", "main")
	runGit(t, repo, "config", "user.name", "Devtize Test")
	runGit(t, repo, "config", "user.email", "devtize-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", "README.md")
	runGit(t, repo, "commit", "--message", "chore: initial fixture")
	runGit(t, repo, "remote", "add", "origin", bare)
	runGit(t, repo, "push", "--set-upstream", "origin", "main")
	remoteBefore := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main"))
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", "README.md")
	runGit(t, repo, "commit", "--message", "feat: next fixture")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if err := os.MkdirAll(filepath.Join(repo, "docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "plans", "phase-c.md"), []byte("excluded\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configHome := t.TempDir()
	dryRun := exec.Command(binary, "ship", "--dry-run")
	dryRun.Dir = repo
	dryRun.Env = append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "NO_COLOR=1")
	output, err := dryRun.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "git.branch.push [remote_write]") || !strings.Contains(string(output), "docs/plans/phase-c.md") {
		t.Fatalf("ship dry-run: %v\n%s", err, output)
	}
	if remote := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main")); remote != remoteBefore {
		t.Fatalf("dry-run changed remote from %s to %s", remoteBefore, remote)
	}

	ship := exec.Command(binary, "ship")
	ship.Dir = repo
	ship.Env = append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "NO_COLOR=1")
	ship.Stdin = strings.NewReader("push\n")
	output, err = ship.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "result: succeeded") {
		t.Fatalf("ship: %v\n%s", err, output)
	}
	if remote := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main")); remote != head {
		t.Fatalf("remote = %s, want %s", remote, head)
	}
	if _, err := os.Stat(filepath.Join(repo, "docs", "plans", "phase-c.md")); err != nil {
		t.Fatalf("excluded dirty path was lost: %v", err)
	}
}

func TestBuiltBinaryComposedShipRunsChecksCommitsAndPushes(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}

	repo := t.TempDir()
	bare := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", bare)
	runGit(t, repo, "init", "--initial-branch", "main")
	runGit(t, repo, "config", "user.name", "Devtize Test")
	runGit(t, repo, "config", "user.email", "devtize-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", "README.md")
	runGit(t, repo, "commit", "--message", "chore: initial fixture")
	runGit(t, repo, "remote", "add", "origin", bare)
	runGit(t, repo, "push", "--set-upstream", "origin", "main")
	remoteBefore := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main"))

	files := map[string]string{
		"go.mod":       "module example.invalid/fixture\n\ngo 1.27.0\n",
		"main.go":      "package fixture\n\nfunc Sum(a, b int) int { return a + b }\n",
		"main_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) {\n\tif Sum(2, 3) != 5 {\n\t\tt.Fatal(\"sum\")\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	configHome := t.TempDir()
	dryArgs := []string{
		"--json", "ship", "go.mod", "main.go", "main_test.go", "--message", "feat: add checked fixture", "--dry-run",
		"--check", "go.format.check", "--check", "go.test", "--check", "go.vet", "--check", "go.build",
	}
	dryRun := exec.Command(binary, dryArgs...)
	dryRun.Dir = repo
	dryRun.Env = append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "GOCACHE="+filepath.Join(configHome, "go-cache"), "NO_COLOR=1")
	dryOutput, err := dryRun.CombinedOutput()
	if err != nil {
		t.Fatalf("composed ship dry-run: %v\n%s", err, dryOutput)
	}
	var dryResponse struct {
		SchemaVersion int    `json:"schema_version"`
		Mode          string `json:"mode"`
		Push          any    `json:"push"`
	}
	if err := json.Unmarshal(dryOutput, &dryResponse); err != nil || dryResponse.SchemaVersion != 1 || dryResponse.Mode != "compose" || dryResponse.Push != nil || strings.Contains(string(dryOutput), "\x1b[") {
		t.Fatalf("dry response=%#v err=%v output=%s", dryResponse, err, dryOutput)
	}
	if strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")) != remoteBefore || strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main")) != remoteBefore {
		t.Fatal("composed dry-run mutated local or remote HEAD")
	}

	command := exec.Command(binary,
		"ship", "go.mod", "main.go", "main_test.go", "--message", "feat: add checked fixture",
		"--check", "go.format.check", "--check", "go.test", "--check", "go.vet", "--check", "go.build",
	)
	command.Dir = repo
	command.Env = append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "GOCACHE="+filepath.Join(configHome, "go-cache"), "NO_COLOR=1")
	command.Stdin = strings.NewReader("commit\npush\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("composed ship: %v\n%s", err, output)
	}
	text := string(output)
	for _, expected := range []string{"go.format.check", "go.test", "go.vet", "go.build", "git.commit.create", "git.branch.push"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output missing %q:\n%s", expected, text)
		}
	}
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if head == remoteBefore || strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main")) != head {
		t.Fatalf("composed ship did not push exact created commit; before=%s head=%s", remoteBefore, head)
	}
	if message := strings.TrimSpace(runGit(t, repo, "log", "-1", "--format=%s")); message != "feat: add checked fixture" {
		t.Fatalf("message = %q", message)
	}
}

func TestBuiltBinaryUndoPlansUnpublishedCommitAndRefusesPublishedCommit(t *testing.T) {
	root := filepath.Clean("..")
	binary := filepath.Join(t.TempDir(), "dvz")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/dvz")
	build.Dir = root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dvz: %v\n%s", err, output)
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "--initial-branch", "main")
	runGit(t, repo, "config", "user.name", "Devtize Test")
	runGit(t, repo, "config", "user.email", "devtize-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", "README.md")
	runGit(t, repo, "commit", "--message", "chore: initial fixture")
	parent := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configHome := t.TempDir()
	environment := append(os.Environ(), "HOME="+configHome, "XDG_CONFIG_HOME="+configHome, "NO_COLOR=1")
	commit := exec.Command(binary, "commit", "README.md", "--message", "docs: update fixture")
	commit.Dir = repo
	commit.Env = environment
	commit.Stdin = strings.NewReader("commit\n")
	if output, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, output)
	}
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if head == parent {
		t.Fatal("fixture commit was not created")
	}

	historyCommand := exec.Command(binary, "--json", "history", "--limit", "1")
	historyCommand.Dir = repo
	historyCommand.Env = environment
	historyOutput, err := historyCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("history: %v\n%s", err, historyOutput)
	}
	var historyResponse struct {
		Records []struct {
			ExecutionID     string `json:"execution_id"`
			ObservedChanges []struct {
				BeforeCommit string `json:"before_commit"`
				AfterCommit  string `json:"after_commit"`
			} `json:"observed_changes"`
		} `json:"records"`
	}
	if err := json.Unmarshal(historyOutput, &historyResponse); err != nil || len(historyResponse.Records) != 1 || len(historyResponse.Records[0].ObservedChanges) != 1 {
		t.Fatalf("history response=%#v err=%v output=%s", historyResponse, err, historyOutput)
	}
	record := historyResponse.Records[0]
	if record.ObservedChanges[0].BeforeCommit != parent || record.ObservedChanges[0].AfterCommit != head {
		t.Fatalf("observed transition=%#v, want %s -> %s", record.ObservedChanges[0], parent, head)
	}
	historyPath := filepath.Join(configHome, "devtize", "history.jsonl")
	historyBefore, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	statusBefore := runGit(t, repo, "status", "--porcelain=v1")

	undo := exec.Command(binary, "--json", "undo", record.ExecutionID, "--dry-run")
	undo.Dir = repo
	undo.Env = environment
	undoOutput, err := undo.CombinedOutput()
	if err != nil || !strings.Contains(string(undoOutput), `"eligibility": "available"`) || !strings.Contains(string(undoOutput), `"git.commit.uncommit_preserve_changes"`) || strings.Contains(string(undoOutput), "\x1b[") {
		t.Fatalf("undo available: %v\n%s", err, undoOutput)
	}
	if strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")) != head || runGit(t, repo, "status", "--porcelain=v1") != statusBefore {
		t.Fatal("undo planning changed local repository state")
	}
	if historyAfter, err := os.ReadFile(historyPath); err != nil || string(historyAfter) != string(historyBefore) {
		t.Fatalf("undo planning changed history: err=%v", err)
	}

	bare := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", bare)
	runGit(t, repo, "remote", "add", "origin", bare)
	runGit(t, repo, "push", "--set-upstream", "origin", "main")
	remoteBefore := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main"))
	undo = exec.Command(binary, "--json", "undo", record.ExecutionID, "--dry-run")
	undo.Dir = repo
	undo.Env = environment
	undoOutput, err = undo.CombinedOutput()
	var unavailable struct {
		Eligibility string `json:"eligibility"`
		Reasons     []struct {
			Code string `json:"code"`
		} `json:"reasons"`
		Plan any `json:"plan"`
	}
	jsonErr := json.Unmarshal(undoOutput, &unavailable)
	if err != nil || jsonErr != nil || unavailable.Eligibility != "unavailable" || len(unavailable.Reasons) != 1 || unavailable.Reasons[0].Code != "COMMIT_PUBLISHED" || unavailable.Plan != nil {
		t.Fatalf("undo published: %v\n%s", err, undoOutput)
	}
	if strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")) != head || strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main")) != remoteBefore || runGit(t, repo, "status", "--porcelain=v1") != statusBefore {
		t.Fatal("published undo inspection changed local or remote state")
	}
	if historyAfter, err := os.ReadFile(historyPath); err != nil || string(historyAfter) != string(historyBefore) {
		t.Fatalf("published undo inspection changed history: err=%v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
