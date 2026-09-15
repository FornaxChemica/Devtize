package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
