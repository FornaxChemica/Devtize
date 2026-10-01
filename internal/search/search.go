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
	Kind          registry.CommandKind     `json:"kind,omitempty"`
	Usage         string                   `json:"usage,omitempty"`
	Flags         []registry.FlagKnowledge `json:"flags,omitempty"`
	MatchedField  string                   `json:"matched_field,omitempty"`
	Score         int                      `json:"-"`
}

type Engine struct {
	commands []registry.CommandKnowledge
}

func New(commands []registry.CommandKnowledge) *Engine {
	return &Engine{commands: append([]registry.CommandKnowledge(nil), commands...)}
}

func (e *Engine) Find(query string, limit int) []Result {
	return e.FindProvider(query, "", limit)
}

func (e *Engine) FindProvider(query, provider string, limit int) []Result {
	query = normalize(query)
	if query == "" {
		return nil
	}
	if limit <= 0 {
		limit = 5
	}

	var results []Result
	for _, command := range e.commands {
		if provider != "" && command.ProviderID != provider {
			continue
		}
		score, confidence, reason, matchedField := score(command, query)
		if score == 0 {
			continue
		}
		results = append(results, Result{
			ID: command.ID, Command: command.Command(), Summary: command.Summary,
			Provider: command.ProviderID, Source: command.Source, Risk: command.Risk,
			Effects: append([]string(nil), command.Effects...), VersionRange: command.VersionRange,
			VersionStatus: versionStatus(command), Confidence: confidence, MatchReason: reason, Score: score,
			Kind: command.Kind, Usage: command.Usage, Flags: append([]registry.FlagKnowledge(nil), command.Flags...), MatchedField: matchedField,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if sourcePriority(results[i].Source.Kind) != sourcePriority(results[j].Source.Kind) {
			return sourcePriority(results[i].Source.Kind) < sourcePriority(results[j].Source.Kind)
		}
		if results[i].Provider != results[j].Provider {
			return results[i].Provider < results[j].Provider
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func versionStatus(command registry.CommandKnowledge) string {
	if command.Source.Kind == "sync" && command.VersionStatus != "" {
		return command.VersionStatus
	}
	return "not_checked"
}

func sourcePriority(kind string) int {
	if kind == "builtin" {
		return 0
	}
	return 1
}

func score(command registry.CommandKnowledge, query string) (int, Confidence, string, string) {
	commandText := normalize(command.Command())
	if query == commandText {
		return 1000, ConfidenceHigh, "exact command path", evidence(command, "command_path")
	}
	for _, alias := range command.Aliases {
		if query == normalize(alias) {
			if command.Kind != "" {
				return 950, ConfidenceHigh, "exact alias", "alias"
			}
			return 900, ConfidenceHigh, "exact reviewed phrase", ""
		}
	}
	for _, phrase := range command.IntentPhrases {
		if query == normalize(phrase) {
			return 900, ConfidenceHigh, "exact reviewed phrase", ""
		}
	}
	for _, flag := range command.Flags {
		if query == normalize(flag.LongName) || flag.ShortName != "" && query == normalize(flag.ShortName) {
			return 850, ConfidenceHigh, "exact flag name", "flag:" + flag.LongName
		}
	}

	queryTokens := strings.Fields(query)
	coreFields := []string{command.Command(), command.Summary}
	coreFields = append(coreFields, command.Aliases...)
	coreFields = append(coreFields, command.IntentPhrases...)
	coreTokens := strings.Fields(normalize(strings.Join(coreFields, " ")))
	all, prefix := tokenMatch(queryTokens, coreTokens)
	if all && !prefix {
		score := 700 + len(queryTokens)
		if command.Kind == registry.CommandKindGroup {
			score -= 20
		}
		return score, ConfidenceMedium, "all query tokens matched", evidence(command, "command_or_summary")
	}
	if all {
		score := 500 + len(queryTokens)
		if command.Kind == registry.CommandKindGroup {
			score -= 20
		}
		return score, ConfidenceMedium, "prefix token match", evidence(command, "command_or_summary")
	}
	var flagFields []string
	for _, flag := range command.Flags {
		flagFields = append(flagFields, flag.LongName, flag.ShortName, flag.ValueHint, flag.Summary)
	}
	flagTokens := strings.Fields(normalize(strings.Join(flagFields, " ")))
	all, prefix = tokenMatch(queryTokens, append(append([]string(nil), coreTokens...), flagTokens...))
	if all && !prefix {
		return 650 + len(queryTokens), ConfidenceMedium, "flag metadata token match", matchedFlagEvidence(command, queryTokens)
	}
	if all {
		return 450 + len(queryTokens), ConfidenceMedium, "flag metadata prefix match", matchedFlagEvidence(command, queryTokens)
	}

	fuzzy := true
	for _, wanted := range queryTokens {
		if len(wanted) < 4 {
			fuzzy = false
			break
		}
		matched := false
		for _, candidate := range coreTokens {
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
		score := 300
		if command.Kind == registry.CommandKindGroup {
			score -= 20
		}
		return score, ConfidenceLow, "conservative fuzzy match", matchedFlagEvidence(command, queryTokens)
	}
	return 0, "", "", ""
}

func tokenMatch(wantedTokens, candidateTokens []string) (bool, bool) {
	prefix := false
	for _, wanted := range wantedTokens {
		matched := false
		for _, candidate := range candidateTokens {
			if candidate == wanted {
				matched = true
				break
			}
			if strings.HasPrefix(candidate, wanted) || strings.HasPrefix(wanted, candidate) {
				matched = true
				prefix = true
				break
			}
		}
		if !matched {
			return false, false
		}
	}
	return true, prefix
}

func evidence(command registry.CommandKnowledge, value string) string {
	if command.Kind != "" {
		return value
	}
	return ""
}

func matchedFlagEvidence(command registry.CommandKnowledge, query []string) string {
	for _, flag := range command.Flags {
		text := normalize(flag.LongName + " " + flag.ShortName + " " + flag.Summary)
		for _, token := range query {
			if strings.Contains(" "+text+" ", " "+token+" ") {
				return "flag:" + flag.LongName
			}
		}
	}
	return evidence(command, "command_or_summary")
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
