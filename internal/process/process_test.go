package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("DVZ_PROCESS_HELPER") != "1" {
		return
	}
	mode := os.Getenv("DVZ_PROCESS_MODE")
	switch mode {
	case "args":
		marker := 0
		for index, arg := range os.Args {
			if arg == "--" {
				marker = index + 1
				break
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(os.Args[marker:])
	case "cwd":
		cwd, _ := os.Getwd()
		fmt.Fprint(os.Stdout, cwd)
	case "env":
		fmt.Fprintf(os.Stdout, "%s|%s", os.Getenv("DVZ_ALLOWED"), os.Getenv("DVZ_BLOCKED"))
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 100)+"tail")
	case "secret":
		fmt.Fprint(os.Stdout, "token-value")
	case "sleep":
		time.Sleep(2 * time.Second)
	case "exit":
		os.Exit(7)
	}
	os.Exit(0)
}

func TestRunnerPreservesLiteralArgumentsWithoutShell(t *testing.T) {
	arguments := []string{"space value", `quote"value`, "*", "$HOME", ">", "|", ";", "-leading"}
	result, err := helperRun(t, "args", arguments, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal([]byte(result.Stdout), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, arguments) {
		t.Fatalf("argv = %#v, want %#v", got, arguments)
	}
}

func TestRunnerSetsWorkingDirectoryAndEnvironmentPolicy(t *testing.T) {
	working := t.TempDir()
	runner := NewRunner()
	result, err := runner.Run(context.Background(), CommandSpec{
		Executable: os.Args[0], Args: []string{"-test.run=TestProcessHelper"}, Dir: working,
		Timeout: 5 * time.Second, Stdin: StdinDisabled, CaptureLimit: 1024,
		EnvOverlay: map[string]string{"DVZ_PROCESS_HELPER": "1", "DVZ_PROCESS_MODE": "cwd"},
	})
	canonicalWorking, canonicalErr := filepath.EvalSymlinks(working)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if err != nil || result.Stdout != canonicalWorking {
		t.Fatalf("cwd result = %#v, %v; want %q", result, err, canonicalWorking)
	}

	runner.Environ = func() []string { return []string{"DVZ_ALLOWED=base", "DVZ_BLOCKED=secret"} }
	result, err = runner.Run(context.Background(), CommandSpec{
		Executable: os.Args[0], Args: []string{"-test.run=TestProcessHelper"}, Dir: working,
		Timeout: 5 * time.Second, Stdin: StdinDisabled, CaptureLimit: 1024,
		EnvAllowlist: []string{"DVZ_ALLOWED"},
		EnvOverlay:   map[string]string{"DVZ_PROCESS_HELPER": "1", "DVZ_PROCESS_MODE": "env"},
	})
	if err != nil || result.Stdout != "base|" {
		t.Fatalf("environment output = %q, %v", result.Stdout, err)
	}
}

func TestRunnerBoundsAndRedactsOutput(t *testing.T) {
	result, err := helperRunSpecWithLimit(t, "large", nil, nil, 5*time.Second, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !result.StdoutTruncated || len(result.Stdout) != 64 || !strings.HasSuffix(result.Stdout, "tail") {
		t.Fatalf("bounded output = %#v", result)
	}
	result, err = helperRun(t, "secret", nil, []string{"token-value"})
	if err != nil || result.Stdout != "<redacted>" {
		t.Fatalf("redacted output = %q, %v", result.Stdout, err)
	}
}

func TestRunnerClassifiesTimeoutMissingAndExit(t *testing.T) {
	runner := NewRunner()
	_, err := runner.Run(context.Background(), CommandSpec{Executable: "definitely-not-a-dvz-tool", Dir: t.TempDir(), Timeout: time.Second, Stdin: StdinDisabled, CaptureLimit: 64})
	assertKind(t, err, ErrorMissing)
	_, err = helperRunWithTimeout(t, "sleep", 20*time.Millisecond)
	assertKind(t, err, ErrorTimeout)
	_, err = helperRun(t, "exit", nil, nil)
	assertKind(t, err, ErrorExit)
}

func helperRun(t *testing.T, mode string, args []string, redactions []string) (CommandResult, error) {
	t.Helper()
	return helperRunSpec(t, mode, args, redactions, 5*time.Second)
}

func helperRunWithTimeout(t *testing.T, mode string, timeout time.Duration) (CommandResult, error) {
	t.Helper()
	return helperRunSpec(t, mode, nil, nil, timeout)
}

func helperRunSpec(t *testing.T, mode string, args []string, redactions []string, timeout time.Duration) (CommandResult, error) {
	return helperRunSpecWithLimit(t, mode, args, redactions, timeout, 4096)
}

func helperRunSpecWithLimit(t *testing.T, mode string, args []string, redactions []string, timeout time.Duration, captureLimit int) (CommandResult, error) {
	t.Helper()
	commandArgs := []string{"-test.run=TestProcessHelper", "--"}
	commandArgs = append(commandArgs, args...)
	return NewRunner().Run(context.Background(), CommandSpec{
		Executable: os.Args[0], Args: commandArgs, Dir: filepath.Dir(os.Args[0]), Timeout: timeout,
		Stdin: StdinDisabled, CaptureLimit: captureLimit, RedactValues: redactions,
		EnvOverlay: map[string]string{"DVZ_PROCESS_HELPER": "1", "DVZ_PROCESS_MODE": mode},
	})
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	var runErr *RunError
	if !errors.As(err, &runErr) || runErr.Kind != want {
		t.Fatalf("error = %#v, want kind %s", err, want)
	}
}
