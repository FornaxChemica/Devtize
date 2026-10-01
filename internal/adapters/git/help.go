package git

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
)

const (
	HelpParserID      = "git-help-all"
	HelpParserVersion = "1"
	HelpMaxDepth      = 1
	HelpMaxCommands   = 256
	HelpMaxBytes      = 1 << 20
	HelpTimeout       = 10 * time.Second
)

var (
	helpArgs           = []string{"help", "--all", "--no-external-commands", "--no-aliases", "--verbose"}
	helpCommandPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	helpColumnGap      = regexp.MustCompile(`\s{2,}`)
)

type HelpInventory struct {
	Commands     []registry.CommandKnowledge
	SourceArgv   []string
	SourceDigest string
	CapturedAt   time.Time
}

func (a Adapter) InspectHelp(ctx context.Context, _ string, isolatedRoot, toolVersion string, capturedAt time.Time) (HelpInventory, error) {
	if a.Runner == nil {
		return HelpInventory{}, errors.New("git runner is required")
	}
	if strings.TrimSpace(toolVersion) == "" || capturedAt.IsZero() {
		return HelpInventory{}, errors.New("Git version and capture time are required")
	}
	executable := a.Executable
	if executable == "" {
		executable = "git"
	}
	result, err := a.Runner.Run(ctx, devprocess.CommandSpec{
		Executable: executable,
		Args:       append([]string(nil), helpArgs...),
		Dir:        isolatedRoot,
		Timeout:    HelpTimeout,
		Stdin:      devprocess.StdinDisabled,
		EnvAllowlist: []string{
			"PATH", "SYSTEMROOT", "WINDIR",
		},
		EnvOverlay: map[string]string{
			"HOME":                isolatedRoot,
			"XDG_CONFIG_HOME":     filepath.Join(isolatedRoot, "xdg-config"),
			"GIT_CONFIG_NOSYSTEM": "1",
			"GIT_TERMINAL_PROMPT": "0",
			"GIT_PAGER":           "cat",
			"PAGER":               "cat",
			"MANPAGER":            "cat",
			"LC_ALL":              "C",
		},
		CaptureLimit: HelpMaxBytes,
	})
	if err != nil {
		return HelpInventory{}, err
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return HelpInventory{}, errors.New("Git help output exceeded the capture limit")
	}
	commands, err := ParseHelp([]byte(result.Stdout), toolVersion, capturedAt)
	if err != nil {
		return HelpInventory{}, err
	}
	sum := sha256.Sum256([]byte(result.Stdout))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	for index := range commands {
		commands[index].Source.Digest = digest
	}
	return HelpInventory{
		Commands: commands, SourceArgv: append([]string{"git"}, helpArgs...),
		SourceDigest: digest, CapturedAt: capturedAt.UTC(),
	}, nil
}

func ParseHelp(content []byte, toolVersion string, capturedAt time.Time) ([]registry.CommandKnowledge, error) {
	if len(content) == 0 {
		return nil, errors.New("Git help inventory is empty")
	}
	if len(content) > HelpMaxBytes {
		return nil, fmt.Errorf("Git help inventory exceeds %d bytes", HelpMaxBytes)
	}
	if strings.TrimSpace(toolVersion) == "" || capturedAt.IsZero() {
		return nil, errors.New("Git help provenance is incomplete")
	}

	allowedSection := false
	mainSection := false
	seenMain := false
	statusInMain := false
	seen := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	scanner.Buffer(make([]byte, 64<<10), HelpMaxBytes)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			mainSection = line == "Main Porcelain Commands"
			allowedSection = mainSection || strings.HasPrefix(line, "Ancillary Commands") || line == "Interacting with Others" || strings.HasPrefix(line, "Low-level Commands")
			if mainSection {
				seenMain = true
			}
			continue
		}
		if !allowedSection {
			continue
		}
		name, description, ok := parseHelpRow(line)
		if !ok {
			return nil, fmt.Errorf("malformed command row in reviewed Git help section: %q", line)
		}
		if len(name) > 128 || len(description) > 1024 {
			return nil, fmt.Errorf("Git help command %q exceeds reviewed field limits", name)
		}
		if previous, duplicate := seen[name]; duplicate {
			if previous != description {
				return nil, fmt.Errorf("Git help command %q has conflicting descriptions", name)
			}
			return nil, fmt.Errorf("Git help command %q is duplicated", name)
		}
		seen[name] = description
		if mainSection && name == "status" {
			statusInMain = true
		}
		if len(seen) > HelpMaxCommands {
			return nil, fmt.Errorf("Git help inventory exceeds %d commands", HelpMaxCommands)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Git help inventory: %w", err)
	}
	if !seenMain {
		return nil, errors.New("Git help inventory is missing the main porcelain section")
	}
	if !statusInMain {
		return nil, errors.New("Git help inventory is missing anchor command git status")
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	commands := make([]registry.CommandKnowledge, 0, len(names))
	for _, name := range names {
		commands = append(commands, registry.CommandKnowledge{
			ID:           "sync.git." + name,
			ProviderID:   "git",
			CommandPath:  []string{"git", name},
			Summary:      seen[name],
			VersionRange: "=" + toolVersion,
			Risk:         safety.Risk("unclassified"),
			Effects:      []string{"Discovery-only help metadata; effects are not reviewed."},
			Source: registry.KnowledgeSource{
				Kind: "sync", Locator: "git help --all --no-external-commands --no-aliases --verbose",
				ToolVersion: toolVersion, ParserVersion: HelpParserVersion, CapturedAt: capturedAt.UTC(),
			},
			Support: registry.SupportDiscoverable, VersionStatus: "exact",
		})
	}
	return commands, nil
}

func parseHelpRow(line string) (string, string, bool) {
	if !strings.HasPrefix(line, "   ") {
		return "", "", false
	}
	trimmed := strings.TrimSpace(line)
	separator := helpColumnGap.FindStringIndex(trimmed)
	if separator == nil {
		return "", "", false
	}
	name := trimmed[:separator[0]]
	description := strings.TrimSpace(trimmed[separator[1]:])
	if !helpCommandPattern.MatchString(name) || description == "" || strings.ContainsAny(description, "\r\n") {
		return "", "", false
	}
	return name, description, true
}
