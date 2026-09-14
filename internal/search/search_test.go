package search_test

import (
	"testing"

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
