package search_test

import (
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
	"github.com/FornaxChemica/devtize/internal/search"
	"github.com/FornaxChemica/devtize/registry/builtin"
)

func engine(t *testing.T) *search.Engine {
	t.Helper()
	catalog, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	return search.New(catalog.Commands())
}

func TestFindInitializationIntent(t *testing.T) {
	results := engine(t).Find("initialize git repository", 5)
	if len(results) == 0 || results[0].Command != "git init" {
		t.Fatalf("first result = %#v, want git init", results)
	}
	if results[0].Confidence != search.ConfidenceHigh {
		t.Fatalf("confidence = %s, want high", results[0].Confidence)
	}
}

func TestExactCommandOutranksReviewedPhrase(t *testing.T) {
	results := engine(t).Find("git status", 5)
	if len(results) == 0 || results[0].ID != "git.status" || results[0].MatchReason != "exact command path" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestSearchIsStableAndConservative(t *testing.T) {
	first := engine(t).Find("show git", 20)
	second := engine(t).Find("show git", 20)
	if len(first) != len(second) {
		t.Fatalf("result counts differ: %d and %d", len(first), len(second))
	}
	for index := range first {
		if first[index].ID != second[index].ID {
			t.Fatalf("unstable tie at %d: %s and %s", index, first[index].ID, second[index].ID)
		}
	}
	if got := engine(t).Find("xyzzy", 5); len(got) != 0 {
		t.Fatalf("unrelated query returned %#v", got)
	}
}

func TestFuzzyMatchIsLowConfidence(t *testing.T) {
	results := engine(t).Find("intialize repositry", 5)
	if len(results) == 0 || results[0].Command != "git init" || results[0].Confidence != search.ConfidenceLow {
		t.Fatalf("unexpected fuzzy results: %#v", results)
	}
}

func TestEmptyQueryReturnsNoResults(t *testing.T) {
	if results := engine(t).Find("  ", 5); len(results) != 0 {
		t.Fatalf("empty query returned %#v", results)
	}
}

func TestReviewedBuiltinsWinTiesAndSyncedVersionsAreVisible(t *testing.T) {
	builtins := builtin.Git()
	synced := registry.CommandKnowledge{
		ID: "sync.git.status", ProviderID: "git", CommandPath: []string{"git", "status"}, Summary: "Synced status",
		VersionRange: "=2.50.1", VersionStatus: "stale", Risk: safety.Risk("unclassified"), Effects: []string{"Discovery-only help metadata; effects are not reviewed."},
		Source:  registry.KnowledgeSource{Kind: "sync", Locator: "git help --all --no-external-commands --no-aliases --verbose", Digest: "sha256:test", ToolVersion: "2.50.1", ParserVersion: "1", CapturedAt: time.Unix(1, 0).UTC()},
		Support: registry.SupportDiscoverable,
	}
	results := search.New(append(builtins, synced)).Find("git status", 20)
	if len(results) < 2 || results[0].ID != "git.status" || results[0].VersionStatus != "not_checked" || results[1].ID != "sync.git.status" || results[1].VersionStatus != "stale" {
		t.Fatalf("unexpected source ordering: %#v", results)
	}
}

func TestGitHubSearchSupportsProviderAliasesFlagsAndGroups(t *testing.T) {
	commands := []registry.CommandKnowledge{
		{ID: "sync.gh.pr", ProviderID: "gh", CommandPath: []string{"gh", "pr"}, Summary: "Manage pull requests", Kind: registry.CommandKindGroup},
		{ID: "sync.gh.pr.create", ProviderID: "gh", CommandPath: []string{"gh", "pr", "create"}, Summary: "Create a pull request", Kind: registry.CommandKindCommand, Usage: "[flags]", Aliases: []string{"gh pr new"}, Flags: []registry.FlagKnowledge{{LongName: "--web", ShortName: "-w", Summary: "Open a browser"}}},
	}
	for index := range commands {
		commands[index].Risk = "unclassified"
		commands[index].Effects = []string{"Discovery-only help metadata; effects are not reviewed."}
		commands[index].VersionRange = "=2.93.0"
		commands[index].VersionStatus = "exact"
		commands[index].Support = registry.SupportDiscoverable
		commands[index].Source = registry.KnowledgeSource{Kind: "sync", Locator: "gh help reference", Digest: "sha256:test", ToolVersion: "2.93.0", ParserVersion: "1", CapturedAt: time.Unix(1, 0).UTC()}
	}
	engine := search.New(commands)
	if got := engine.FindProvider("gh pr create", "gh", 5); len(got) == 0 || got[0].ID != "sync.gh.pr.create" || got[0].Kind != registry.CommandKindCommand {
		t.Fatalf("exact path results=%#v", got)
	}
	if got := engine.FindProvider("gh pr new", "gh", 5); len(got) == 0 || got[0].MatchReason != "exact alias" || got[0].MatchedField != "alias" {
		t.Fatalf("alias results=%#v", got)
	}
	if got := engine.FindProvider("--web", "gh", 5); len(got) == 0 || got[0].MatchedField != "flag:--web" {
		t.Fatalf("flag results=%#v", got)
	}
	if got := engine.FindProvider("gh pr create", "git", 5); len(got) != 0 {
		t.Fatalf("provider filter leaked results=%#v", got)
	}
}

func FuzzSearchIsDeterministic(f *testing.F) {
	f.Add("initialize git repository")
	f.Add("show $HOME > output | next")
	f.Fuzz(func(t *testing.T, query string) {
		first := engine(t).Find(query, 5)
		second := engine(t).Find(query, 5)
		if len(first) != len(second) {
			t.Fatalf("result counts differ for %q", query)
		}
		for index := range first {
			if first[index].ID != second[index].ID || first[index].Score != second[index].Score {
				t.Fatalf("unstable result for %q", query)
			}
		}
	})
}
