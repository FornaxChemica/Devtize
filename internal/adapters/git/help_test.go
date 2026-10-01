package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	devprocess "github.com/FornaxChemica/devtize/internal/process"
)

const sampleHelp = `See 'git help <command>' to read about a specific subcommand

Main Porcelain Commands
   status                  Show the working tree status
   commit                  Record changes to the repository

Ancillary Commands / Manipulators
   config                  Get and set repository or global options

Low-level Commands / Interrogators
   rev-parse               Pick out and massage parameters

User-facing repository, command and file interfaces
   hooks                   Hooks used by Git
`

func TestParseHelpFiltersDocumentationAndSortsCommands(t *testing.T) {
	captured := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	commands, err := ParseHelp([]byte(sampleHelp), "2.50.1", captured)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, command := range commands {
		paths = append(paths, command.Command())
	}
	want := []string{"git commit", "git config", "git rev-parse", "git status"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	for _, command := range commands {
		if command.Risk != "unclassified" || command.VersionStatus != "exact" || command.Source.ToolVersion != "2.50.1" {
			t.Fatalf("unsafe or incomplete synced command: %#v", command)
		}
	}
}

func TestParseHelpPlatformFixtures(t *testing.T) {
	for _, platform := range []string{"apple", "upstream"} {
		t.Run(platform, func(t *testing.T) {
			input, err := os.ReadFile(filepath.Join("testdata", "help-"+platform+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			commands, err := ParseHelp(input, "2.50.1", time.Unix(1, 0).UTC())
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			for _, command := range commands {
				fmt.Fprintf(&output, "%s\t%s\n", command.Command(), command.Summary)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "help-"+platform+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if output.String() != string(want) {
				t.Fatalf("parsed output differs\n--- got ---\n%s--- want ---\n%s", output.String(), want)
			}
		})
	}
}

func TestParseHelpRejectsMissingAnchorDuplicateAndMalformedRows(t *testing.T) {
	now := time.Now().UTC()
	for name, content := range map[string]string{
		"empty":          "",
		"missing anchor": "Main Porcelain Commands\n   commit                  Record changes\n",
		"duplicate":      "Main Porcelain Commands\n   status                  First\n   status                  Second\n",
		"malformed":      "Main Porcelain Commands\n   status one column\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseHelp([]byte(content), "2.50.1", now); err == nil {
				t.Fatal("invalid help was accepted")
			}
		})
	}
}

func TestParseHelpEnforcesCommandCountAndFieldLimits(t *testing.T) {
	now := time.Now().UTC()
	var tooMany strings.Builder
	tooMany.WriteString("Main Porcelain Commands\n   status                  Status\n")
	for index := 0; index < HelpMaxCommands; index++ {
		fmt.Fprintf(&tooMany, "   command-%03d             Description\n", index)
	}
	if _, err := ParseHelp([]byte(tooMany.String()), "2.50.1", now); err == nil {
		t.Fatal("oversized command inventory was accepted")
	}
	longDescription := "Main Porcelain Commands\n   status                  " + strings.Repeat("x", 1025) + "\n"
	if _, err := ParseHelp([]byte(longDescription), "2.50.1", now); err == nil {
		t.Fatal("oversized description was accepted")
	}
}

func TestInspectHelpUsesOnlyReviewedInvocation(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: sampleHelp, Executable: "/usr/bin/git"}}
	adapter := Adapter{Runner: runner, Executable: "/usr/bin/git"}
	captured := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	inventory, err := adapter.InspectHelp(context.Background(), "/project", "/isolated", "2.50.1", captured)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.specs))
	}
	spec := runner.specs[0]
	wantArgs := []string{"help", "--all", "--no-external-commands", "--no-aliases", "--verbose"}
	if spec.Executable != "/usr/bin/git" || !reflect.DeepEqual(spec.Args, wantArgs) || spec.Dir != "/isolated" || spec.Timeout != HelpTimeout || spec.Stdin != devprocess.StdinDisabled || spec.CaptureLimit != HelpMaxBytes {
		t.Fatalf("unexpected help spec: %#v", spec)
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT", "GIT_PAGER", "PAGER", "MANPAGER", "LC_ALL"} {
		if spec.EnvOverlay[key] == "" {
			t.Fatalf("missing isolated environment %s", key)
		}
	}
	if strings.Join(inventory.SourceArgv, " ") != "git help --all --no-external-commands --no-aliases --verbose" {
		t.Fatalf("source argv = %#v", inventory.SourceArgv)
	}
}

func TestInspectHelpRejectsTruncatedOutput(t *testing.T) {
	runner := &fakeRunner{out: devprocess.CommandResult{Stdout: sampleHelp, StdoutTruncated: true}}
	adapter := Adapter{Runner: runner}
	if _, err := adapter.InspectHelp(context.Background(), "/project", "/isolated", "2.50.1", time.Now().UTC()); err == nil {
		t.Fatal("truncated help was accepted")
	}
}

func FuzzParseHelp(f *testing.F) {
	f.Add([]byte(sampleHelp))
	f.Fuzz(func(t *testing.T, content []byte) {
		_, _ = ParseHelp(content, "2.50.1", time.Unix(1, 0).UTC())
	})
}
