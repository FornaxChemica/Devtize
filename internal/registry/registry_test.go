package registry_test

import (
	"testing"

	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/safety"
	"github.com/FornaxChemica/devtize/registry/builtin"
)

func TestBuiltinCatalogIsValidAndDiscoveryOnly(t *testing.T) {
	catalog, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Len() != 12 {
		t.Fatalf("catalog length = %d, want 12", catalog.Len())
	}
	seen := map[string]bool{}
	for _, command := range catalog.Commands() {
		seen[command.Command()] = true
		if command.Support != registry.SupportDiscoverable {
			t.Fatalf("%s support = %s", command.ID, command.Support)
		}
	}
	for _, command := range []string{"git init", "git status", "git diff", "git add", "git commit", "git branch", "git log", "git remote", "git push", "git pull", "git restore", "git reset --soft"} {
		if !seen[command] {
			t.Errorf("required command %q is missing", command)
		}
	}
}

func TestCatalogRejectsIncompleteKnowledge(t *testing.T) {
	_, err := registry.NewCatalog([]registry.CommandKnowledge{{
		ID: "bad", ProviderID: "git", CommandPath: []string{"git", "status"},
		Summary: "status", Risk: safety.RiskReadOnly, Effects: []string{"reads"},
		Source:  registry.KnowledgeSource{Kind: "builtin", Locator: "test"},
		Support: registry.SupportDiscoverable,
	}})
	if err == nil {
		t.Fatal("catalog accepted provenance without a digest")
	}
}

func TestBuiltinChecksAreClosedAndRiskClassified(t *testing.T) {
	checks := registry.BuiltinChecks()
	if len(checks) != 4 {
		t.Fatalf("checks = %d, want 4", len(checks))
	}
	for _, check := range checks {
		if check.ID == "" || check.ProviderID != "go" || !check.Risk.Valid() || check.Effect == "" {
			t.Fatalf("invalid check: %#v", check)
		}
	}
	if err := registry.ValidateCheckIDs([]string{"go.test", "go.test"}); err == nil {
		t.Fatal("duplicate checks accepted")
	}
	if err := registry.ValidateCheckIDs([]string{"go.test", "shell.command"}); err == nil {
		t.Fatal("unknown check accepted")
	}
}

func TestUndoCompensationIsPlannerOnly(t *testing.T) {
	capability, ok := registry.CompensationByID("git.commit.uncommit_preserve_changes")
	if !ok || capability.ProviderID != "git" || capability.SourceCapabilityID != "git.commit.create" || capability.Risk != safety.RiskLocalWrite || capability.Support != registry.SupportPlanned {
		t.Fatalf("capability=%#v ok=%t", capability, ok)
	}
}
