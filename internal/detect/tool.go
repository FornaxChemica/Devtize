package detect

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

type Runner interface {
	Run(context.Context, devprocess.CommandSpec) (devprocess.CommandResult, error)
}

type ToolDetector struct {
	Runner Runner
	Now    func() time.Time
}

type ToolSpec struct {
	ProviderID     string
	Executable     string
	VersionArgs    []string
	MinimumVersion string
	WorkingDir     string
	EnvOverlay     map[string]string
}

func (d ToolDetector) Detect(ctx context.Context, spec ToolSpec) ToolInstallation {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	installation := ToolInstallation{
		ProviderID: spec.ProviderID, Executable: spec.Executable, Status: ToolError,
		Readiness: "unavailable", DetectedAt: now().UTC(),
	}
	result, err := d.Runner.Run(ctx, devprocess.CommandSpec{
		Executable: spec.Executable, Args: append([]string(nil), spec.VersionArgs...), Dir: spec.WorkingDir,
		Timeout: 3 * time.Second, Stdin: devprocess.StdinDisabled, CaptureLimit: 32 << 10,
		EnvAllowlist: []string{"PATH", "SYSTEMROOT", "WINDIR"}, EnvOverlay: spec.EnvOverlay,
	})
	if err != nil {
		var runErr *devprocess.RunError
		if errors.As(err, &runErr) && runErr.Kind == devprocess.ErrorMissing {
			installation.Status = ToolMissing
			installation.Readiness = "missing"
			installation.Detail = "executable not found"
			return installation
		}
		installation.Detail = "version check failed"
		return installation
	}
	installation.Path = result.Executable
	version := parseVersion(result.Stdout + "\n" + result.Stderr)
	if version == "" {
		installation.Status = ToolVersionUnparseable
		installation.Readiness = "detected"
		installation.Detail = "installed version could not be parsed"
		return installation
	}
	installation.Version = version
	if spec.MinimumVersion != "" && compareVersion(version, spec.MinimumVersion) < 0 {
		installation.Status = ToolVersionUnsupported
		installation.Readiness = "detected"
		installation.Detail = "installed version is below the supported minimum " + spec.MinimumVersion
		return installation
	}
	installation.Status = ToolInstalled
	installation.Readiness = "detected"
	if spec.ProviderID == "gh" {
		installation.AuthStatus = "auth_unknown"
		installation.Detail = "authentication is not probed in Phase A"
	}
	return installation
}

var versionPattern = regexp.MustCompile(`\b(\d+\.\d+(?:\.\d+)?)\b`)

func parseVersion(value string) string {
	match := versionPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func compareVersion(left, right string) int {
	l := versionParts(left)
	r := versionParts(right)
	for i := 0; i < 3; i++ {
		if l[i] < r[i] {
			return -1
		}
		if l[i] > r[i] {
			return 1
		}
	}
	return 0
}

func versionParts(value string) [3]int {
	var result [3]int
	for index, part := range strings.Split(value, ".") {
		if index == len(result) {
			break
		}
		result[index], _ = strconv.Atoi(part)
	}
	return result
}
