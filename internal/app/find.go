package app

import (
	"strings"

	"github.com/FornaxChemica/devtize/internal/search"
)

type FindService struct {
	Search   *search.Engine
	Registry FindRegistry
}

type FindRegistry struct {
	Status    string                 `json:"status"`
	Warnings  []string               `json:"warnings,omitempty"`
	Providers []FindRegistryProvider `json:"providers,omitempty"`
}

type FindRegistryProvider struct {
	Provider         string   `json:"provider"`
	Status           string   `json:"status"`
	KnowledgeVersion string   `json:"knowledge_version,omitempty"`
	Entries          int      `json:"entries"`
	Warnings         []string `json:"warnings,omitempty"`
}

type FindResponse struct {
	SchemaVersion int             `json:"schema_version"`
	Query         string          `json:"query"`
	Provider      string          `json:"provider,omitempty"`
	Results       []search.Result `json:"results"`
	Registry      FindRegistry    `json:"registry"`
}

func (s FindService) Find(words []string) (FindResponse, error) {
	return s.FindWithProvider(words, "")
}

func (s FindService) FindWithProvider(words []string, provider string) (FindResponse, error) {
	if provider != "" && provider != "git" && provider != "gh" {
		return FindResponse{}, &Error{Code: CodeInvalidUsage, Message: "find provider is unsupported", Hint: "Use --provider git or --provider gh."}
	}
	query := strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		return FindResponse{}, &Error{Code: CodeCapabilityNotFound, Message: "search intent is required", Hint: "Use dvz find <intent>."}
	}
	results := s.Search.FindProvider(query, provider, 5)
	if len(results) == 0 {
		hint := "Try a more specific Git intent."
		if provider != "" {
			hint = "No matching " + provider + " knowledge is available. Run dvz sync " + provider + " or try a more specific intent."
		}
		return FindResponse{}, &Error{Code: CodeCapabilityNotFound, Message: "no reviewed command knowledge matched the intent", Provider: provider, Hint: hint}
	}
	return FindResponse{SchemaVersion: 1, Query: query, Provider: provider, Results: results, Registry: s.Registry}, nil
}
