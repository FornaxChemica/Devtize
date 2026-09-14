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
