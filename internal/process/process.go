package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type StdinPolicy string

const (
	StdinDisabled StdinPolicy = "disabled"
	StdinInherit  StdinPolicy = "inherit"
)

type CommandSpec struct {
	Executable   string
	Args         []string
	Dir          string
	Timeout      time.Duration
	Stdin        StdinPolicy
	EnvAllowlist []string
	EnvOverlay   map[string]string
	CaptureLimit int
	RedactValues []string
}

type CommandResult struct {
	Executable      string
	Stdout          string
	Stderr          string
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
}

type ErrorKind string

const (
	ErrorInvalid ErrorKind = "invalid_spec"
	ErrorMissing ErrorKind = "missing_executable"
	ErrorTimeout ErrorKind = "timeout"
	ErrorExit    ErrorKind = "nonzero_exit"
	ErrorStart   ErrorKind = "start_failed"
)

type RunError struct {
	Kind     ErrorKind
	ExitCode int
	Message  string
	Cause    error
}

func (e *RunError) Error() string { return e.Message }
func (e *RunError) Unwrap() error { return e.Cause }

type Runner struct {
	LookPath func(string) (string, error)
	Environ  func() []string
	Stdin    io.Reader
}

func NewRunner() *Runner {
	return &Runner{LookPath: exec.LookPath, Environ: os.Environ, Stdin: os.Stdin}
}

func (r *Runner) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	if spec.Executable == "" || spec.Dir == "" {
		return CommandResult{}, &RunError{Kind: ErrorInvalid, Message: "executable and working directory are required"}
	}
	if spec.Timeout <= 0 {
		return CommandResult{}, &RunError{Kind: ErrorInvalid, Message: "a positive process timeout is required"}
	}
	if spec.CaptureLimit <= 0 {
		return CommandResult{}, &RunError{Kind: ErrorInvalid, Message: "a positive capture limit is required"}
	}
	if spec.Stdin != StdinDisabled && spec.Stdin != StdinInherit {
		return CommandResult{}, &RunError{Kind: ErrorInvalid, Message: "unsupported stdin policy"}
	}

	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	executable, err := lookPath(spec.Executable)
	if err != nil {
		return CommandResult{}, &RunError{Kind: ErrorMissing, Message: fmt.Sprintf("executable %q was not found", spec.Executable), Cause: err}
	}

	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, executable, spec.Args...)
	command.Dir = spec.Dir
	command.Env = buildEnvironment(r.environment(), spec.EnvAllowlist, spec.EnvOverlay)
	if spec.Stdin == StdinInherit {
		command.Stdin = r.Stdin
	}
	stdout := newTailBuffer(spec.CaptureLimit)
	stderr := newTailBuffer(spec.CaptureLimit)
	command.Stdout = stdout
	command.Stderr = stderr

	err = command.Run()
	result := CommandResult{
		Executable: executable, Stdout: redact(stdout.String(), spec.RedactValues),
		Stderr: redact(stderr.String(), spec.RedactValues), ExitCode: 0,
		StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		return result, &RunError{Kind: ErrorTimeout, ExitCode: -1, Message: "process timed out", Cause: runCtx.Err()}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, &RunError{Kind: ErrorExit, ExitCode: result.ExitCode, Message: fmt.Sprintf("process exited with status %d", result.ExitCode), Cause: err}
	}
	result.ExitCode = -1
	return result, &RunError{Kind: ErrorStart, ExitCode: -1, Message: "process could not be started", Cause: err}
}

func (r *Runner) environment() []string {
	if r.Environ != nil {
		return r.Environ()
	}
	return os.Environ()
}

func buildEnvironment(base []string, allowlist []string, overlay map[string]string) []string {
	allowed := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		allowed[name] = struct{}{}
	}
	values := make(map[string]string, len(allowed)+len(overlay))
	for _, entry := range base {
		name, value, found := strings.Cut(entry, "=")
		if _, ok := allowed[name]; found && ok {
			values[name] = value
		}
	}
	for name, value := range overlay {
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result
}

func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return value
}

type tailBuffer struct {
	limit     int
	data      []byte
	truncated bool
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }

func (b *tailBuffer) Write(content []byte) (int, error) {
	original := len(content)
	if original >= b.limit {
		b.data = append(b.data[:0], content[original-b.limit:]...)
		b.truncated = true
		return original, nil
	}
	if len(b.data)+original > b.limit {
		drop := len(b.data) + original - b.limit
		b.data = append(b.data[drop:], content...)
		b.truncated = true
	} else {
		b.data = append(b.data, content...)
	}
	return original, nil
}

func (b *tailBuffer) String() string { return string(bytes.Clone(b.data)) }
