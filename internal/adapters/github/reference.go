package github

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
	"unicode/utf8"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
)

const (
	ReferenceParserID      = "gh-help-reference"
	ReferenceParserVersion = "1"
	ReferenceMaxDepth      = 2
	ReferenceMaxCommands   = 512
	ReferenceMaxFlags      = 4096
	ReferenceMaxAliases    = 1024
	ReferenceMaxBytes      = 2 << 20
	ReferenceTimeout       = 15 * time.Second
	maxAliasesPerCommand   = 32
	maxPathNameBytes       = 128
	maxDisplayBytes        = 1024
)

var (
	referenceArgs       = []string{"help", "reference"}
	referencePathToken  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	referenceColumnGap  = regexp.MustCompile(`\s{2,}`)
	referenceLongFlag   = regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)
	referenceShortFlag  = regexp.MustCompile(`^-[A-Za-z0-9]$`)
	referenceAnchorPath = map[string]struct{}{
		"gh api": {}, "gh auth login": {}, "gh issue create": {}, "gh pr create": {}, "gh repo create": {},
	}
)

type ReferenceInventory struct {
	Commands     []registry.CommandKnowledge
	SourceArgv   []string
	SourceDigest string
	CapturedAt   time.Time
	FlagCount    int
	AliasCount   int
}

func (a Adapter) InspectReference(ctx context.Context, isolatedRoot, toolVersion string, capturedAt time.Time) (ReferenceInventory, error) {
	if a.Runner == nil {
		return ReferenceInventory{}, errors.New("GitHub CLI runner is required")
	}
	if strings.TrimSpace(toolVersion) == "" || capturedAt.IsZero() {
		return ReferenceInventory{}, errors.New("GitHub CLI version and capture time are required")
	}
	executable := a.Executable
	if executable == "" {
		executable = "gh"
	}
	result, err := a.Runner.Run(ctx, devprocess.CommandSpec{
		Executable: executable,
		Args:       append([]string(nil), referenceArgs...),
		Dir:        isolatedRoot,
		Timeout:    ReferenceTimeout,
		Stdin:      devprocess.StdinDisabled,
		EnvAllowlist: []string{
			"PATH", "SYSTEMROOT", "WINDIR",
		},
		EnvOverlay:   ReferenceEnvironment(isolatedRoot),
		CaptureLimit: ReferenceMaxBytes,
	})
	if err != nil {
		return ReferenceInventory{}, err
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return ReferenceInventory{}, errors.New("GitHub CLI reference output exceeded the capture limit")
	}
	if strings.TrimSpace(result.Stderr) != "" {
		return ReferenceInventory{}, errors.New("GitHub CLI reference wrote unexpected diagnostic output")
	}
	commands, err := ParseReference([]byte(result.Stdout), toolVersion, capturedAt)
	if err != nil {
		return ReferenceInventory{}, err
	}
	sum := sha256.Sum256([]byte(result.Stdout))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	flags, aliases := 0, 0
	for index := range commands {
		commands[index].Source.Digest = digest
		flags += len(commands[index].Flags)
		aliases += len(commands[index].Aliases)
	}
	return ReferenceInventory{
		Commands: commands, SourceArgv: append([]string{"gh"}, referenceArgs...),
		SourceDigest: digest, CapturedAt: capturedAt.UTC(), FlagCount: flags, AliasCount: aliases,
	}, nil
}

func ReferenceEnvironment(isolatedRoot string) map[string]string {
	return map[string]string{
		"HOME":                  isolatedRoot,
		"GH_CONFIG_DIR":         filepath.Join(isolatedRoot, "gh-config"),
		"XDG_CONFIG_HOME":       filepath.Join(isolatedRoot, "xdg-config"),
		"XDG_STATE_HOME":        filepath.Join(isolatedRoot, "xdg-state"),
		"GH_PROMPT_DISABLED":    "1",
		"GH_NO_UPDATE_NOTIFIER": "1",
		"GH_PAGER":              "cat",
		"PAGER":                 "cat",
		"NO_COLOR":              "1",
		"CLICOLOR":              "0",
		"TERM":                  "dumb",
		"LC_ALL":                "C",
	}
}

type referenceCommand struct {
	level         int
	path          []string
	usage         string
	summary       string
	aliases       []string
	flags         []registry.FlagKnowledge
	wantSummary   bool
	wantAliases   bool
	summaryClosed bool
}

func ParseReference(content []byte, toolVersion string, capturedAt time.Time) ([]registry.CommandKnowledge, error) {
	if len(content) == 0 {
		return nil, errors.New("GitHub CLI reference is empty")
	}
	if len(content) > ReferenceMaxBytes {
		return nil, fmt.Errorf("GitHub CLI reference exceeds %d bytes", ReferenceMaxBytes)
	}
	if strings.TrimSpace(toolVersion) == "" || capturedAt.IsZero() {
		return nil, errors.New("GitHub CLI reference provenance is incomplete")
	}
	if !utf8.Valid(content) {
		return nil, errors.New("GitHub CLI reference is not valid UTF-8")
	}
	for _, value := range content {
		if value == 0x1b || value < 0x20 && value != '\n' && value != '\r' && value != '\t' {
			return nil, errors.New("GitHub CLI reference contains control bytes")
		}
	}

	var commands []*referenceCommand
	var current *referenceCommand
	var parent []string
	ignoredDepth := 0
	anchorSeen := false
	seenPaths := make(map[string]struct{})
	aliasOwners := make(map[string]string)
	totalAliases := 0
	totalFlags := 0

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	scanner.Buffer(make([]byte, 64<<10), ReferenceMaxBytes)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.HasPrefix(line, "#") {
			level := headingLevel(line)
			if level == 1 {
				if line != "# gh reference" || anchorSeen || len(commands) != 0 {
					return nil, fmt.Errorf("unexpected GitHub CLI reference document heading %q", line)
				}
				anchorSeen = true
				current = nil
				ignoredDepth = 0
				continue
			}
			if !anchorSeen {
				return nil, errors.New("GitHub CLI reference is missing exact anchor # gh reference")
			}
			if level < 2 || !strings.HasPrefix(line, strings.Repeat("#", level)+" gh ") {
				return nil, fmt.Errorf("unexpected Markdown heading in GitHub CLI reference: %q", line)
			}
			path, usage, err := parseReferenceHeading(line, level)
			if err != nil {
				return nil, err
			}
			if level > 3 {
				if current == nil || current.level != 3 || len(path)-1 != level-1 || len(path) <= len(current.path) || !samePath(path[:len(current.path)], current.path) {
					return nil, fmt.Errorf("over-depth GitHub CLI heading has ambiguous ownership: %q", line)
				}
				ignoredDepth = level
				continue
			}
			ignoredDepth = 0
			if len(path)-1 != level-1 {
				return nil, fmt.Errorf("GitHub CLI heading depth does not match its command path: %q", line)
			}
			if level == 2 {
				parent = append([]string(nil), path...)
			} else if len(parent) != 2 || !samePath(path[:2], parent) {
				return nil, fmt.Errorf("GitHub CLI subcommand does not extend its current parent: %q", line)
			}
			key := strings.Join(path, " ")
			if _, duplicate := seenPaths[key]; duplicate {
				return nil, fmt.Errorf("duplicate GitHub CLI command path %q", key)
			}
			seenPaths[key] = struct{}{}
			current = &referenceCommand{level: level, path: path, usage: usage, wantSummary: true}
			commands = append(commands, current)
			if len(commands) > ReferenceMaxCommands {
				return nil, fmt.Errorf("GitHub CLI reference exceeds %d command paths", ReferenceMaxCommands)
			}
			continue
		}
		if ignoredDepth > 0 {
			continue
		}
		if current == nil {
			continue
		}
		if strings.TrimSpace(line) == "" {
			if current.summary != "" {
				current.summaryClosed = true
			}
			continue
		}
		if current.wantSummary {
			if strings.HasPrefix(line, " ") || line == "Aliases" || strings.ContainsAny(line, "\r\n") || len(line) > maxDisplayBytes {
				return nil, fmt.Errorf("GitHub CLI command %q has no unambiguous summary", strings.Join(current.path, " "))
			}
			current.summary = line
			current.wantSummary = false
			continue
		}
		if current.wantAliases {
			aliases, err := parseReferenceAliases(line)
			if err != nil {
				return nil, fmt.Errorf("GitHub CLI command %q: %w", strings.Join(current.path, " "), err)
			}
			for _, alias := range aliases {
				if owner, duplicate := aliasOwners[alias]; duplicate {
					return nil, fmt.Errorf("GitHub CLI alias %q belongs to both %q and %q", alias, owner, strings.Join(current.path, " "))
				}
				aliasOwners[alias] = strings.Join(current.path, " ")
			}
			current.aliases = aliases
			current.wantAliases = false
			totalAliases += len(aliases)
			if len(aliases) > maxAliasesPerCommand || totalAliases > ReferenceMaxAliases {
				return nil, errors.New("GitHub CLI reference exceeds reviewed alias limits")
			}
			continue
		}
		if line == "Aliases" {
			if len(current.aliases) != 0 {
				return nil, fmt.Errorf("GitHub CLI command %q has duplicate alias blocks", strings.Join(current.path, " "))
			}
			current.wantAliases = true
			continue
		}
		if !strings.HasPrefix(line, " ") && !current.summaryClosed {
			return nil, fmt.Errorf("GitHub CLI command %q has an ambiguous multiline summary", strings.Join(current.path, " "))
		}
		if strings.HasPrefix(line, " ") {
			flag, recognized, err := parseReferenceFlag(line)
			if err != nil {
				return nil, fmt.Errorf("GitHub CLI command %q: %w", strings.Join(current.path, " "), err)
			}
			if !recognized {
				continue
			}
			duplicate := false
			for _, existing := range current.flags {
				if existing.LongName == flag.LongName {
					if existing != flag {
						return nil, fmt.Errorf("conflicting duplicate GitHub CLI flag %s", flag.LongName)
					}
					duplicate = true
					break
				}
			}
			if !duplicate {
				current.flags = append(current.flags, flag)
				totalFlags++
				if totalFlags > ReferenceMaxFlags {
					return nil, fmt.Errorf("GitHub CLI reference exceeds %d flags", ReferenceMaxFlags)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan GitHub CLI reference: %w", err)
	}
	if !anchorSeen {
		return nil, errors.New("GitHub CLI reference is missing exact anchor # gh reference")
	}
	if len(commands) == 0 {
		return nil, errors.New("GitHub CLI reference contains no bounded command paths")
	}
	for _, command := range commands {
		if command.wantSummary || command.wantAliases || command.summary == "" {
			return nil, fmt.Errorf("GitHub CLI command %q has incomplete reviewed metadata", strings.Join(command.path, " "))
		}
	}
	for anchor := range referenceAnchorPath {
		if _, ok := seenPaths[anchor]; !ok {
			return nil, fmt.Errorf("GitHub CLI reference is missing anchor command %s", anchor)
		}
	}

	children := make(map[string]bool)
	for _, command := range commands {
		if len(command.path) == 3 {
			children[strings.Join(command.path[:2], " ")] = true
		}
	}
	result := make([]registry.CommandKnowledge, 0, len(commands))
	for _, command := range commands {
		sort.Strings(command.aliases)
		sort.Slice(command.flags, func(i, j int) bool {
			if command.flags[i].LongName != command.flags[j].LongName {
				return command.flags[i].LongName < command.flags[j].LongName
			}
			return command.flags[i].ShortName < command.flags[j].ShortName
		})
		kind := registry.CommandKindCommand
		if children[strings.Join(command.path, " ")] {
			kind = registry.CommandKindGroup
		}
		result = append(result, registry.CommandKnowledge{
			ID: "sync.gh." + strings.Join(command.path[1:], "."), ProviderID: "gh",
			CommandPath: append([]string(nil), command.path...), Summary: command.summary,
			Kind: kind, Usage: command.usage, Flags: append([]registry.FlagKnowledge(nil), command.flags...),
			Aliases: append([]string(nil), command.aliases...), VersionRange: "=" + toolVersion,
			Risk: safety.Risk("unclassified"), Effects: []string{"Discovery-only help metadata; effects are not reviewed."},
			Source: registry.KnowledgeSource{
				Kind: "sync", Locator: "gh help reference", ToolVersion: toolVersion,
				ParserVersion: ReferenceParserVersion, CapturedAt: capturedAt.UTC(),
			},
			Support: registry.SupportDiscoverable, VersionStatus: "exact",
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func headingLevel(line string) int {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0
	}
	return level
}

func parseReferenceHeading(line string, level int) ([]string, string, error) {
	prefix := strings.Repeat("#", level) + " gh "
	if !strings.HasPrefix(line, prefix) {
		return nil, "", fmt.Errorf("invalid GitHub CLI command heading %q", line)
	}
	body := strings.TrimSpace(strings.TrimPrefix(line, strings.Repeat("#", level)+" "))
	if body == "" || strings.Contains(body, "  ") {
		return nil, "", fmt.Errorf("ambiguous GitHub CLI command heading %q", line)
	}
	tokens := strings.Fields(body)
	if len(tokens) < 2 || tokens[0] != "gh" {
		return nil, "", fmt.Errorf("invalid GitHub CLI command heading %q", line)
	}
	path := []string{"gh"}
	usageStart := -1
	offset := len("gh")
	for _, token := range tokens[1:] {
		for offset < len(body) && body[offset] == ' ' {
			offset++
		}
		if strings.ContainsRune("-[<{", rune(token[0])) {
			usageStart = offset
			break
		}
		if len(token) > maxPathNameBytes || !referencePathToken.MatchString(token) {
			return nil, "", fmt.Errorf("invalid GitHub CLI path token %q", token)
		}
		path = append(path, token)
		offset += len(token)
	}
	usage := ""
	if usageStart >= 0 {
		usage = strings.TrimSpace(body[usageStart:])
		if len(usage) > maxDisplayBytes {
			return nil, "", errors.New("GitHub CLI usage suffix exceeds reviewed limits")
		}
	}
	return path, usage, nil
}

func parseReferenceAliases(line string) ([]string, error) {
	if strings.HasPrefix(line, " ") || len(line) > maxDisplayBytes {
		return nil, errors.New("alias block is malformed")
	}
	parts := strings.Split(line, ",")
	aliases := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		alias := strings.TrimSpace(part)
		fields := strings.Fields(alias)
		if len(fields) < 2 || len(fields) > 3 || fields[0] != "gh" || strings.Join(fields, " ") != alias {
			return nil, fmt.Errorf("invalid alias %q", alias)
		}
		for _, token := range fields[1:] {
			if len(token) > maxPathNameBytes || !referencePathToken.MatchString(token) {
				return nil, fmt.Errorf("invalid alias %q", alias)
			}
		}
		if _, duplicate := seen[alias]; duplicate {
			return nil, fmt.Errorf("duplicate alias %q", alias)
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	if len(aliases) == 0 {
		return nil, errors.New("alias block is empty")
	}
	return aliases, nil
}

func parseReferenceFlag(line string) (registry.FlagKnowledge, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "-") {
		return registry.FlagKnowledge{}, false, nil
	}
	separator := referenceColumnGap.FindStringIndex(trimmed)
	if separator == nil {
		return registry.FlagKnowledge{}, false, errors.New("flag row has no fixed-column description")
	}
	syntax := strings.TrimSpace(trimmed[:separator[0]])
	summary := strings.TrimSpace(trimmed[separator[1]:])
	if summary == "" || len(summary) > maxDisplayBytes || strings.ContainsAny(summary, "\r\n") {
		return registry.FlagKnowledge{}, false, errors.New("flag row has an invalid summary")
	}
	shortName := ""
	if strings.Contains(syntax, ",") {
		short, remainder, found := strings.Cut(syntax, ",")
		if !found || !referenceShortFlag.MatchString(strings.TrimSpace(short)) {
			return registry.FlagKnowledge{}, false, fmt.Errorf("flag row has invalid short name %q", short)
		}
		shortName = strings.TrimSpace(short)
		syntax = strings.TrimSpace(remainder)
	}
	parts := strings.Fields(syntax)
	if len(parts) == 0 || !referenceLongFlag.MatchString(parts[0]) || len(parts[0]) > maxPathNameBytes {
		return registry.FlagKnowledge{}, false, fmt.Errorf("flag row has invalid long name %q", syntax)
	}
	valueHint := strings.TrimSpace(strings.TrimPrefix(syntax, parts[0]))
	if len(valueHint) > maxDisplayBytes || strings.ContainsAny(valueHint, "\r\n") {
		return registry.FlagKnowledge{}, false, fmt.Errorf("flag %s has invalid value syntax", parts[0])
	}
	flag := registry.FlagKnowledge{LongName: parts[0], ShortName: shortName, ValueHint: valueHint, Summary: summary}
	if err := flag.Validate(); err != nil {
		return registry.FlagKnowledge{}, false, err
	}
	return flag, true, nil
}

func samePath(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
