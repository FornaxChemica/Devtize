package github_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/adapters/github"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/registry"
)

type referenceRunner struct {
	spec   devprocess.CommandSpec
	result devprocess.CommandResult
	err    error
}

func (r *referenceRunner) Run(_ context.Context, spec devprocess.CommandSpec) (devprocess.CommandResult, error) {
	r.spec = spec
	return r.result, r.err
}

func TestParseOfficialReferenceFixtures(t *testing.T) {
	when := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	fixtures := []struct {
		name, version string
		commands      int
		flags         int
		aliases       int
	}{
		{name: "reference-2.93.0.txt", version: "2.93.0", commands: 207, flags: 916, aliases: 45},
		{name: "reference-2.80.0.txt", version: "2.80.0", commands: 196, flags: 865, aliases: 39},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.version, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("testdata", fixture.name))
			if err != nil {
				t.Fatal(err)
			}
			commands, err := github.ParseReference(content, fixture.version, when)
			if err != nil {
				t.Fatal(err)
			}
			flags, aliases := inventoryCounts(commands)
			if len(commands) != fixture.commands || flags != fixture.flags || aliases != fixture.aliases {
				t.Fatalf("inventory=%d/%d/%d want=%d/%d/%d", len(commands), flags, aliases, fixture.commands, fixture.flags, fixture.aliases)
			}
			assertCommand(t, commands, "gh api", registry.CommandKindCommand)
			assertCommand(t, commands, "gh auth", registry.CommandKindGroup)
			assertCommand(t, commands, "gh auth login", registry.CommandKindCommand)
			assertCommand(t, commands, "gh issue create", registry.CommandKindCommand)
			assertCommand(t, commands, "gh pr create", registry.CommandKindCommand)
			assertCommand(t, commands, "gh repo create", registry.CommandKindCommand)
		})
	}
}

func TestReferenceAdapterUsesExactIsolatedProcessSpec(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "reference-2.93.0.txt"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &referenceRunner{result: devprocess.CommandResult{Executable: "/tools/gh", Stdout: string(content)}}
	when := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	inventory, err := (github.Adapter{Runner: runner, Executable: "/tools/gh"}).InspectReference(context.Background(), "/isolated", "2.93.0", when)
	if err != nil {
		t.Fatal(err)
	}
	if runner.spec.Executable != "/tools/gh" || !reflect.DeepEqual(runner.spec.Args, []string{"help", "reference"}) || runner.spec.Dir != "/isolated" || runner.spec.Timeout != github.ReferenceTimeout || runner.spec.Stdin != devprocess.StdinDisabled || runner.spec.CaptureLimit != github.ReferenceMaxBytes {
		t.Fatalf("unexpected process spec: %#v", runner.spec)
	}
	if !reflect.DeepEqual(runner.spec.EnvAllowlist, []string{"PATH", "SYSTEMROOT", "WINDIR"}) || !reflect.DeepEqual(runner.spec.EnvOverlay, github.ReferenceEnvironment("/isolated")) {
		t.Fatalf("unexpected isolated environment: %#v %#v", runner.spec.EnvAllowlist, runner.spec.EnvOverlay)
	}
	if len(inventory.Commands) == 0 || inventory.FlagCount == 0 || inventory.AliasCount == 0 || inventory.SourceDigest == "" {
		t.Fatalf("incomplete inventory: %#v", inventory)
	}
}

func TestReferenceAdapterRejectsProcessFailures(t *testing.T) {
	for name, runner := range map[string]*referenceRunner{
		"truncated stdout":  {result: devprocess.CommandResult{StdoutTruncated: true}},
		"truncated stderr":  {result: devprocess.CommandResult{StderrTruncated: true}},
		"unexpected stderr": {result: devprocess.CommandResult{Stderr: "warning"}},
		"timeout":           {err: &devprocess.RunError{Kind: devprocess.ErrorTimeout, Message: "timeout"}},
		"missing":           {err: &devprocess.RunError{Kind: devprocess.ErrorMissing, Message: "missing"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (github.Adapter{Runner: runner, Executable: "/tools/gh"}).InspectReference(context.Background(), t.TempDir(), "2.93.0", time.Now())
			if err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestParseReferenceRejectsHostileOrAmbiguousInput(t *testing.T) {
	valid := minimalReference()
	fixtures := map[string]string{
		"missing anchor":     strings.Replace(valid, "# gh reference", "# reference", 1),
		"ansi":               strings.Replace(valid, "Create a pull request", "Create \x1b[31ma pull request", 1),
		"duplicate path":     valid + "\n## gh api\nDuplicate API command\n",
		"wrong parent":       strings.Replace(valid, "### gh auth login", "### gh repo login", 1),
		"missing summary":    strings.Replace(valid, "Log in to a GitHub account\n", "", 1),
		"bad alias":          strings.Replace(valid, "gh pr new", "gh pr new;rm", 1),
		"conflicting flag":   strings.Replace(valid, "## gh repo", "  -w, --web   Other meaning\n\n## gh repo", 1),
		"unexpected heading": strings.Replace(valid, "## gh repo", "## Notes\n\n## gh repo", 1),
	}
	for name, content := range fixtures {
		t.Run(name, func(t *testing.T) {
			if _, err := github.ParseReference([]byte(content), "2.93.0", time.Now()); err == nil {
				t.Fatal("hostile reference was accepted")
			}
		})
	}
	if _, err := github.ParseReference([]byte(valid+strings.Repeat("x", github.ReferenceMaxBytes)), "2.93.0", time.Now()); err == nil {
		t.Fatal("oversized reference was accepted")
	}
}

func FuzzParseReference(f *testing.F) {
	f.Add([]byte(minimalReference()), "2.93.0", int64(1))
	f.Add([]byte("# gh reference\n"), "2.0.0", int64(2))
	f.Fuzz(func(t *testing.T, content []byte, version string, unix int64) {
		_, _ = github.ParseReference(content, version, time.Unix(unix, 0).UTC())
	})
}

func inventoryCounts(commands []registry.CommandKnowledge) (int, int) {
	flags, aliases := 0, 0
	for _, command := range commands {
		flags += len(command.Flags)
		aliases += len(command.Aliases)
	}
	return flags, aliases
}

func assertCommand(t *testing.T, commands []registry.CommandKnowledge, path string, kind registry.CommandKind) {
	t.Helper()
	for _, command := range commands {
		if command.Command() == path {
			if command.Kind != kind || command.Risk != "unclassified" || command.Support != registry.SupportDiscoverable {
				t.Fatalf("unexpected command metadata: %#v", command)
			}
			return
		}
	}
	t.Fatalf("missing command %s", path)
}

func minimalReference() string {
	return `# gh reference

## gh api <endpoint> [flags]

Make an authenticated GitHub API request

  -X, --method string   The HTTP method

## gh auth <command>

Authenticate gh and git with GitHub

### gh auth login [flags]

Log in to a GitHub account

## gh issue <command>

Work with GitHub issues

### gh issue create [flags]

Create a new issue

## gh pr <command>

Manage pull requests

### gh pr create [flags]

Create a pull request

Aliases

gh pr new

  -w, --web   Open the browser

## gh repo <command>

Manage repositories

### gh repo create [<name>] [flags]

Create a new repository
`
}
