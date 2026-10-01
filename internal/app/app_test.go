package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/detect"
	"github.com/FornaxChemica/devtize/internal/registry"
	"github.com/FornaxChemica/devtize/internal/search"
	"github.com/FornaxChemica/devtize/registry/builtin"
)

type toolDetector struct {
	installations map[string]detect.ToolInstallation
}

func (f toolDetector) Detect(_ context.Context, spec detect.ToolSpec) detect.ToolInstallation {
	return f.installations[spec.ProviderID]
}

func TestFindJoinsOrdinaryTrailingWords(t *testing.T) {
	catalog, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	response, err := (app.FindService{
		Search: search.New(catalog.Commands()), Registry: app.FindRegistry{Status: "invalid", Warnings: []string{"cache ignored"}},
	}).Find([]string{"initialize", "git", "repository"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Query != "initialize git repository" || response.Results[0].Command != "git init" || response.Registry.Status != "invalid" || len(response.Registry.Warnings) != 1 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestFindProviderValidationAndScopedMiss(t *testing.T) {
	catalog, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	service := app.FindService{Search: search.New(catalog.Commands())}
	if _, err := service.FindWithProvider([]string{"status"}, "docker"); !hasCode(err, app.CodeInvalidUsage) {
		t.Fatalf("err=%v", err)
	}
	if _, err := service.FindWithProvider([]string{"create", "pull", "request"}, "gh"); !hasCode(err, app.CodeCapabilityNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestDoctorReportsOptionalFailuresWithoutCrashing(t *testing.T) {
	service := app.DoctorService{
		WorkingDir: "/work", RegistryCount: 12,
		Project: func(string) (detect.Project, error) { return detect.Project{Status: "not_found"}, nil },
		Tools: toolDetector{installations: map[string]detect.ToolInstallation{
			"git": {ProviderID: "git", Status: detect.ToolInstalled},
			"gh":  {ProviderID: "gh", Status: detect.ToolMissing},
		}},
	}
	response, err := service.Run(context.Background(), app.ConfigCheck{Status: "valid"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "warning" || response.Project.Status != "not_found" || len(response.Tools) != 2 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestErrorPreservesCause(t *testing.T) {
	cause := errors.New("cause")
	err := app.Wrap(app.CodeConfigInvalid, "safe", cause)
	if !errors.Is(err, cause) || err.SafeText() != "CONFIG_INVALID: safe" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestDoctorIsolatesGitHubState(t *testing.T) {
	detector := &recordingToolDetector{}
	service := app.DoctorService{
		WorkingDir: "/work", RegistryCount: 12, ToolStateDir: "/isolated",
		Project: func(string) (detect.Project, error) { return detect.Project{Status: "not_found"}, nil },
		Tools:   detector,
	}
	if _, err := service.Run(context.Background(), app.ConfigCheck{Status: "valid"}); err != nil {
		t.Fatal(err)
	}
	if detector.ghSpec.EnvOverlay["HOME"] != "/isolated" || detector.ghSpec.EnvOverlay["GH_CONFIG_DIR"] == "" || detector.ghSpec.EnvOverlay["XDG_STATE_HOME"] == "" {
		t.Fatalf("gh environment was not isolated: %#v", detector.ghSpec.EnvOverlay)
	}
}

func TestDoctorReportsSyncedKnowledgeStaleAgainstLiveGit(t *testing.T) {
	service := app.DoctorService{
		WorkingDir: "/work", RegistryCount: 20,
		RegistryCache: registry.CacheLoadResult{Status: registry.CacheExact, Snapshot: &registry.ProviderSnapshot{KnowledgeVersion: "2.50.1"}},
		Project:       func(string) (detect.Project, error) { return detect.Project{Status: "detected", Root: "/work"}, nil },
		Tools: toolDetector{installations: map[string]detect.ToolInstallation{
			"git": {ProviderID: "git", Status: detect.ToolInstalled, Version: "2.51.0"},
			"gh":  {ProviderID: "gh", Status: detect.ToolInstalled},
		}},
	}
	response, err := service.Run(context.Background(), app.ConfigCheck{Status: "valid"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "warning" || response.Registry.KnowledgeStatus != "synced_stale" || response.Registry.Source != "builtin+sync" {
		t.Fatalf("response=%#v", response)
	}
}

type recordingToolDetector struct {
	ghSpec detect.ToolSpec
}

func (d *recordingToolDetector) Detect(_ context.Context, spec detect.ToolSpec) detect.ToolInstallation {
	if spec.ProviderID == "gh" {
		d.ghSpec = spec
	}
	return detect.ToolInstallation{ProviderID: spec.ProviderID, Status: detect.ToolInstalled}
}
