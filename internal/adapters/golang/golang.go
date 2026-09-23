package golang

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

const (
	maxGoFiles  = 10_000
	formatBatch = 128
)

var (
	ErrCheckFailed = errors.New("project check failed")
	ErrToolMissing = errors.New("Go toolchain executable missing")
)

type Runner interface {
	Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
}

type Toolchain struct {
	GoPath    string `json:"go_path"`
	GofmtPath string `json:"gofmt_path"`
	GoVersion string `json:"go_version"`
}

type CheckInput struct {
	CapabilityID string
	ProjectRoot  string
	GoFiles      []string
	Toolchain    Toolchain
}

type CheckResult struct {
	CapabilityID string        `json:"capability_id"`
	Status       string        `json:"status"`
	Duration     time.Duration `json:"duration_ns,omitempty"`
	Diagnostic   string        `json:"diagnostic,omitempty"`
	Truncated    bool          `json:"truncated,omitempty"`
}

type Adapter struct {
	Runner   Runner
	LookPath func(string) (string, error)
	Now      func() time.Time
}

func New(runner Runner) Adapter {
	return Adapter{Runner: runner, LookPath: exec.LookPath, Now: time.Now}
}

func (a Adapter) Detect(ctx context.Context, projectRoot string) (Toolchain, error) {
	lookPath := a.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	goPath, err := lookPath("go")
	if err != nil {
		return Toolchain{}, fmt.Errorf("%w: resolve go: %v", ErrToolMissing, err)
	}
	gofmtPath, err := lookPath("gofmt")
	if err != nil {
		return Toolchain{}, fmt.Errorf("%w: resolve gofmt: %v", ErrToolMissing, err)
	}
	result, err := a.Runner.Run(ctx, command(goPath, []string{"version"}, projectRoot, 30*time.Second))
	if err != nil {
		return Toolchain{}, fmt.Errorf("detect go version: %w", err)
	}
	return Toolchain{GoPath: goPath, GofmtPath: gofmtPath, GoVersion: strings.TrimSpace(result.Stdout)}, nil
}

func (a Adapter) Run(ctx context.Context, input CheckInput) (CheckResult, error) {
	start := a.now()
	result := CheckResult{CapabilityID: input.CapabilityID, Status: "running"}
	var err error
	switch input.CapabilityID {
	case "go.format.check":
		formatCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result.Diagnostic, result.Truncated, err = a.runFormat(formatCtx, input)
		cancel()
	case "go.test":
		result.Diagnostic, result.Truncated, err = a.runGo(ctx, input, 10*time.Minute, "test", "./...")
	case "go.vet":
		result.Diagnostic, result.Truncated, err = a.runGo(ctx, input, 5*time.Minute, "vet", "./...")
	case "go.build":
		result.Diagnostic, result.Truncated, err = a.runGo(ctx, input, 5*time.Minute, "build", "./...")
	default:
		err = fmt.Errorf("unsupported check capability %q", input.CapabilityID)
	}
	result.Duration = a.now().Sub(start)
	if err != nil {
		result.Status = "failed"
		return result, err
	}
	result.Status = "succeeded"
	return result, nil
}

func (a Adapter) runFormat(ctx context.Context, input CheckInput) (string, bool, error) {
	if len(input.GoFiles) > maxGoFiles {
		return "", false, fmt.Errorf("%w: Go file inventory exceeds %d files", ErrCheckFailed, maxGoFiles)
	}
	var diagnostics []string
	truncated := false
	for start := 0; start < len(input.GoFiles); start += formatBatch {
		end := start + formatBatch
		if end > len(input.GoFiles) {
			end = len(input.GoFiles)
		}
		args := make([]string, 0, end-start+1)
		args = append(args, "-l")
		for _, path := range input.GoFiles[start:end] {
			args = append(args, "./"+filepath.ToSlash(path))
		}
		processResult, err := a.Runner.Run(ctx, command(input.Toolchain.GofmtPath, args, input.ProjectRoot, 30*time.Second))
		diagnostic := joinedDiagnostic(processResult)
		if diagnostic != "" {
			diagnostics = append(diagnostics, diagnostic)
		}
		truncated = truncated || processResult.StdoutTruncated || processResult.StderrTruncated
		if err != nil {
			return strings.Join(diagnostics, "\n"), truncated, err
		}
	}
	diagnostic := strings.TrimSpace(strings.Join(diagnostics, "\n"))
	if diagnostic != "" {
		return diagnostic, truncated, fmt.Errorf("%w: gofmt reported unformatted files", ErrCheckFailed)
	}
	return "", truncated, nil
}

func (a Adapter) runGo(ctx context.Context, input CheckInput, timeout time.Duration, args ...string) (string, bool, error) {
	result, err := a.Runner.Run(ctx, command(input.Toolchain.GoPath, args, input.ProjectRoot, timeout))
	diagnostic := joinedDiagnostic(result)
	if err != nil {
		return diagnostic, result.StdoutTruncated || result.StderrTruncated, fmt.Errorf("%w: %w", ErrCheckFailed, err)
	}
	return diagnostic, result.StdoutTruncated || result.StderrTruncated, nil
}

func command(executable string, args []string, dir string, timeout time.Duration) devprocess.CommandSpec {
	return devprocess.CommandSpec{
		Executable: executable, Args: append([]string(nil), args...), Dir: dir, Timeout: timeout,
		Stdin: devprocess.StdinDisabled, CaptureLimit: 1 << 20,
		EnvAllowlist: []string{
			"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR",
			"XDG_CACHE_HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOENV", "GOPROXY",
			"GONOPROXY", "GOPRIVATE", "GONOSUMDB", "GOSUMDB", "CGO_ENABLED", "CC", "CXX",
		},
	}
}

func joinedDiagnostic(result devprocess.CommandResult) string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(result.Stdout); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(result.Stderr); value != "" {
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

func (a Adapter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
