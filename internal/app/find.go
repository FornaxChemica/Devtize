package app

import (
	"strings"

	"github.com/FornaxChemica/devtize/internal/search"
)

type FindService struct {
	Search *search.Engine
}

type FindResponse struct {
	SchemaVersion int             `json:"schema_version"`
	Query         string          `json:"query"`
	Results       []search.Result `json:"results"`
}

func (s FindService) Find(words []string) (FindResponse, error) {
	query := strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		return FindResponse{}, &Error{Code: CodeCapabilityNotFound, Message: "search intent is required", Hint: "Use dvz find <intent>."}
	}
	results := s.Search.Find(query, 5)
	if len(results) == 0 {
		return FindResponse{}, &Error{Code: CodeCapabilityNotFound, Message: "no reviewed command knowledge matched the intent", Hint: "Try a more specific Git intent."}
	}
	return FindResponse{SchemaVersion: 1, Query: query, Results: results}, nil
}
