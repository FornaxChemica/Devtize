package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/FornaxChemica/devtize/internal/safety"
)

type SupportLevel string

const (
	SupportPlanned      SupportLevel = "planned"
	SupportDetected     SupportLevel = "detected"
	SupportDiscoverable SupportLevel = "discoverable"
)

type KnowledgeSource struct {
	Kind          string    `json:"kind" yaml:"kind"`
	Locator       string    `json:"locator" yaml:"locator"`
	Digest        string    `json:"digest" yaml:"digest"`
	ToolVersion   string    `json:"tool_version,omitempty" yaml:"tool_version,omitempty"`
	ParserVersion string    `json:"parser_version,omitempty" yaml:"parser_version,omitempty"`
	CapturedAt    time.Time `json:"captured_at,omitempty" yaml:"captured_at,omitempty"`
}

func (s KnowledgeSource) MarshalJSON() ([]byte, error) {
	type wire struct {
		Kind          string     `json:"kind"`
		Locator       string     `json:"locator"`
		Digest        string     `json:"digest"`
		ToolVersion   string     `json:"tool_version,omitempty"`
		ParserVersion string     `json:"parser_version,omitempty"`
		CapturedAt    *time.Time `json:"captured_at,omitempty"`
	}
	value := wire{Kind: s.Kind, Locator: s.Locator, Digest: s.Digest, ToolVersion: s.ToolVersion, ParserVersion: s.ParserVersion}
	if !s.CapturedAt.IsZero() {
		captured := s.CapturedAt
		value.CapturedAt = &captured
	}
	return json.Marshal(value)
}

type CommandKnowledge struct {
	ID            string          `json:"id" yaml:"id"`
	ProviderID    string          `json:"provider" yaml:"provider"`
	CommandPath   []string        `json:"command_path" yaml:"command_path"`
	Summary       string          `json:"summary" yaml:"summary"`
	Aliases       []string        `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	IntentPhrases []string        `json:"intent_phrases,omitempty" yaml:"intent_phrases,omitempty"`
	Examples      []string        `json:"examples,omitempty" yaml:"examples,omitempty"`
	VersionRange  string          `json:"version_range,omitempty" yaml:"version_range,omitempty"`
	Risk          safety.Risk     `json:"risk" yaml:"risk"`
	Effects       []string        `json:"effects" yaml:"effects"`
	Source        KnowledgeSource `json:"source" yaml:"source"`
	Support       SupportLevel    `json:"support" yaml:"support"`
	VersionStatus string          `json:"version_status,omitempty" yaml:"version_status,omitempty"`
}

func (k CommandKnowledge) Command() string {
	return strings.Join(k.CommandPath, " ")
}

func (k CommandKnowledge) Validate() error {
	if strings.TrimSpace(k.ID) == "" {
		return errors.New("command knowledge ID is required")
	}
	if strings.TrimSpace(k.ProviderID) == "" {
		return fmt.Errorf("%s: provider is required", k.ID)
	}
	if len(k.CommandPath) < 2 || k.CommandPath[0] != k.ProviderID {
		return fmt.Errorf("%s: command path must start with provider and include a command", k.ID)
	}
	for _, part := range k.CommandPath {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("%s: command path contains an empty segment", k.ID)
		}
	}
	if strings.TrimSpace(k.Summary) == "" {
		return fmt.Errorf("%s: summary is required", k.ID)
	}
	if !k.Risk.Valid() && !(k.Source.Kind == "sync" && k.Risk == safety.Risk("unclassified")) {
		return fmt.Errorf("%s: invalid risk %q", k.ID, k.Risk)
	}
	if len(k.Effects) == 0 {
		return fmt.Errorf("%s: at least one effect is required", k.ID)
	}
	if k.Source.Locator == "" || k.Source.Digest == "" {
		return fmt.Errorf("%s: complete provenance is required", k.ID)
	}
	switch k.Source.Kind {
	case "builtin":
		if k.Source.ToolVersion != "" || k.Source.ParserVersion != "" || !k.Source.CapturedAt.IsZero() {
			return fmt.Errorf("%s: builtin provenance cannot contain sync metadata", k.ID)
		}
	case "sync":
		if k.Source.ToolVersion == "" || k.Source.ParserVersion == "" || k.Source.CapturedAt.IsZero() {
			return fmt.Errorf("%s: complete sync provenance is required", k.ID)
		}
		if k.VersionStatus != "exact" && k.VersionStatus != "stale" {
			return fmt.Errorf("%s: invalid sync version status %q", k.ID, k.VersionStatus)
		}
	default:
		return fmt.Errorf("%s: unsupported provenance kind %q", k.ID, k.Source.Kind)
	}
	if k.Support != SupportDiscoverable {
		return fmt.Errorf("%s: command knowledge must be discoverable only", k.ID)
	}
	return nil
}

type Catalog struct {
	commands []CommandKnowledge
}

// CheckCapability is reviewed executable behavior, not discovery knowledge.
// Only adapters owned by Devtize may implement these stable IDs.
type CheckCapability struct {
	ID         string      `json:"id" yaml:"id"`
	ProviderID string      `json:"provider_id" yaml:"provider_id"`
	Summary    string      `json:"summary" yaml:"summary"`
	Risk       safety.Risk `json:"risk" yaml:"risk"`
	Effect     string      `json:"effect" yaml:"effect"`
}

// CompensationCapability describes reviewed recovery behavior. Planned support
// is metadata only and cannot be dispatched to an adapter.
type CompensationCapability struct {
	ID                 string       `json:"id" yaml:"id"`
	ProviderID         string       `json:"provider_id" yaml:"provider_id"`
	SourceCapabilityID string       `json:"source_capability_id" yaml:"source_capability_id"`
	Summary            string       `json:"summary" yaml:"summary"`
	Risk               safety.Risk  `json:"risk" yaml:"risk"`
	Effect             string       `json:"effect" yaml:"effect"`
	Support            SupportLevel `json:"support" yaml:"support"`
}

var builtinChecks = []CheckCapability{
	{ID: "go.format.check", ProviderID: "go", Summary: "Verify Go source formatting", Risk: safety.RiskReadOnly, Effect: "reads tracked and nonignored Go source files"},
	{ID: "go.test", ProviderID: "go", Summary: "Run Go tests", Risk: safety.RiskLocalWrite, Effect: "executes project test code and may write local caches"},
	{ID: "go.vet", ProviderID: "go", Summary: "Run Go static analysis", Risk: safety.RiskLocalWrite, Effect: "loads project packages and may write local caches"},
	{ID: "go.build", ProviderID: "go", Summary: "Build Go packages", Risk: safety.RiskLocalWrite, Effect: "compiles project packages and may write local caches"},
}

var builtinCompensations = []CompensationCapability{
	{
		ID: "git.commit.uncommit_preserve_changes", ProviderID: "git", SourceCapabilityID: "git.commit.create",
		Summary: "Move the local branch to the verified parent while preserving working-tree content",
		Risk:    safety.RiskLocalWrite, Effect: "moves the local branch and reconstructs the removed commit as working-tree changes",
		Support: SupportPlanned,
	},
}

func BuiltinChecks() []CheckCapability {
	return append([]CheckCapability(nil), builtinChecks...)
}

func CheckByID(id string) (CheckCapability, bool) {
	for _, capability := range builtinChecks {
		if capability.ID == id {
			return capability, true
		}
	}
	return CheckCapability{}, false
}

func ValidateCheckIDs(ids []string) error {
	if len(ids) > 16 {
		return fmt.Errorf("checks.ship supports at most 16 capability IDs")
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := CheckByID(id); !ok {
			return fmt.Errorf("unknown ship check capability %q", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate ship check capability %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func CompensationByID(id string) (CompensationCapability, bool) {
	for _, capability := range builtinCompensations {
		if capability.ID == id {
			return capability, true
		}
	}
	return CompensationCapability{}, false
}

func NewCatalog(commands []CommandKnowledge) (*Catalog, error) {
	seen := make(map[string]struct{}, len(commands))
	copyCommands := append([]CommandKnowledge(nil), commands...)
	for _, command := range copyCommands {
		if err := command.Validate(); err != nil {
			return nil, err
		}
		if _, exists := seen[command.ID]; exists {
			return nil, fmt.Errorf("duplicate command knowledge ID %q", command.ID)
		}
		seen[command.ID] = struct{}{}
	}
	sort.Slice(copyCommands, func(i, j int) bool { return copyCommands[i].ID < copyCommands[j].ID })
	return &Catalog{commands: copyCommands}, nil
}

func (c *Catalog) Commands() []CommandKnowledge {
	return append([]CommandKnowledge(nil), c.commands...)
}

func (c *Catalog) Len() int { return len(c.commands) }
