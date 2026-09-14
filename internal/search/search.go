package search

import (
	"sort"
	"strings"
	"unicode"

	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

type Result struct {
	ID            string                   `json:"id"`
	Command       string                   `json:"command"`
	Summary       string                   `json:"summary"`
	Provider      string                   `json:"provider"`
	Source        registry.KnowledgeSource `json:"source"`
	Risk          safety.Risk              `json:"risk"`
	Effects       []string                 `json:"effects"`
	VersionRange  string                   `json:"version_range"`
	VersionStatus string                   `json:"version_status"`
	Confidence    Confidence               `json:"confidence"`
	MatchReason   string                   `json:"match_reason"`
	Score         int                      `json:"-"`
}

type Engine struct {
	commands []registry.CommandKnowledge
}

func New(commands []registry.CommandKnowledge) *Engine {
	return &Engine{commands: append([]registry.CommandKnowledge(nil), commands...)}
}

func (e *Engine) Find(query string, limit int) []Result {
	query = normalize(query)
	if query == "" {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}

	var results []Result
	for _, command := range e.commands {
		score, confidence, reason := score(command, query)
		if score == 0 {
			continue
		}
		results = append(results, Result{
			ID: command.ID, Command: command.Command(), Summary: command.Summary,
			Provider: command.ProviderID, Source: command.Source, Risk: command.Risk,
			Effects: append([]string(nil), command.Effects...), VersionRange: command.VersionRange,
			VersionStatus: "not_checked", Confidence: confidence, MatchReason: reason, Score: score,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func score(command registry.CommandKnowledge, query string) (int, Confidence, string) {
	commandText := normalize(command.Command())
	if query == commandText {
		return 1000, ConfidenceHigh, "exact command path"
	}
	for _, phrase := range append(append([]string{}, command.Aliases...), command.IntentPhrases...) {
		if query == normalize(phrase) {
			return 900, ConfidenceHigh, "exact reviewed phrase"
		}
	}

	queryTokens := strings.Fields(query)
	fields := []string{command.Command(), command.Summary}
	fields = append(fields, command.Aliases...)
	fields = append(fields, command.IntentPhrases...)
	searchText := normalize(strings.Join(fields, " "))
	searchTokens := strings.Fields(searchText)
	all, prefix := true, false
	for _, wanted := range queryTokens {
		matched := false
		for _, candidate := range searchTokens {
			if candidate == wanted {
				matched = true
				break
			}
			if strings.HasPrefix(candidate, wanted) || strings.HasPrefix(wanted, candidate) {
				matched, prefix = true, true
				break
			}
		}
		if !matched {
			all = false
			break
		}
	}
	if all && !prefix {
		return 700 + len(queryTokens), ConfidenceMedium, "all query tokens matched"
	}
	if all {
		return 500 + len(queryTokens), ConfidenceMedium, "prefix token match"
	}

	fuzzy := true
	for _, wanted := range queryTokens {
		if len(wanted) < 4 {
			fuzzy = false
			break
		}
		matched := false
		for _, candidate := range searchTokens {
			max := 1
			if len(wanted) > 8 {
				max = 2
			}
			if levenshteinWithin(wanted, candidate, max) {
				matched = true
				break
			}
		}
		if !matched {
			fuzzy = false
			break
		}
	}
	if fuzzy {
		return 300, ConfidenceLow, "conservative fuzzy match"
	}
	return 0, "", ""
}

func normalize(value string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func levenshteinWithin(a, b string, max int) bool {
	if abs(len(a)-len(b)) > max {
		return false
	}
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		rowMin := current[0]
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			rowMin = min(rowMin, current[j])
		}
		if rowMin > max {
			return false
		}
		previous = current
	}
	return previous[len(b)] <= max
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
