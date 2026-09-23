package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
