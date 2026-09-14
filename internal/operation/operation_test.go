package operation

import (
	"testing"
	"time"

	"github.com/FornaxChemica/devtize/internal/safety"
)

func TestDigestStableAndChangesForMeaningfulFields(t *testing.T) {
	created := time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC)
	plan := Plan{
		SchemaVersion: 1, ID: "plan", Intent: "test", CreatedAt: created, ProjectRoot: "/tmp/project",
		Operations: []Operation{{ID: "op", CapabilityID: "git.repo.init", ProviderID: "git", Summary: "init", Risk: safety.RiskLocalWrite, Inputs: map[string]any{"branch": "main"}}},
	}
	left, err := Digest(plan)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	right, err := Digest(plan)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if left != right {
		t.Fatalf("digest was not stable: %s != %s", left, right)
	}
	plan.Operations[0].Inputs["branch"] = "trunk"
	changed, err := Digest(plan)
	if err != nil {
		t.Fatalf("digest changed: %v", err)
	}
	if changed == left {
		t.Fatalf("digest did not change after branch input changed")
	}
}
