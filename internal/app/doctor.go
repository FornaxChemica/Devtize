package app

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/FornaxChemica/devtize/internal/detect"
	"github.com/FornaxChemica/devtize/internal/registry"
)

type ProjectDetector func(string) (detect.Project, error)

type InstallationDetector interface {
	Detect(context.Context, detect.ToolSpec) detect.ToolInstallation
}

type DoctorService struct {
	WorkingDir    string
	Project       ProjectDetector
	Tools         InstallationDetector
	RegistryCount int
	RegistryCache registry.CacheLoadResult
	ToolStateDir  string
}

type ConfigCheck struct {
	Status  string   `json:"status"`
	Sources []string `json:"sources,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

type RegistryCheck struct {
	Status          string   `json:"status"`
	Source          string   `json:"source"`
	KnowledgeStatus string   `json:"knowledge_status"`
	Entries         int      `json:"entries"`
	Warnings        []string `json:"warnings,omitempty"`
}

type DoctorResponse struct {
	SchemaVersion int                       `json:"schema_version"`
	Status        string                    `json:"status"`
	Config        ConfigCheck               `json:"config"`
	Project       detect.Project            `json:"project"`
	Tools         []detect.ToolInstallation `json:"tools"`
	Registry      RegistryCheck             `json:"registry"`
}

func (s DoctorService) Run(ctx context.Context, configCheck ConfigCheck) (DoctorResponse, error) {
	response := DoctorResponse{
		SchemaVersion: 1, Status: "ok", Config: configCheck,
		Registry: RegistryCheck{Status: "available", Source: "builtin", KnowledgeStatus: "builtin_only", Entries: s.RegistryCount, Warnings: append([]string(nil), s.RegistryCache.Warnings...)},
	}
	if configCheck.Status != "valid" {
		response.Status = "error"
	}
	project, err := s.Project(s.WorkingDir)
	if err != nil {
		return response, Wrap(CodeProjectNotFound, "project detection failed", err)
	}
	response.Project = project
	if project.Status == "not_found" && response.Status == "ok" {
		response.Status = "warning"
	}

	specs := []detect.ToolSpec{
		{ProviderID: "git", Executable: "git", VersionArgs: []string{"--version"}, MinimumVersion: "2.23.0", WorkingDir: s.WorkingDir},
		{
			ProviderID: "gh", Executable: "gh", VersionArgs: []string{"--version"},
			MinimumVersion: "2.0.0", WorkingDir: s.WorkingDir, EnvOverlay: ghIsolatedEnvironment(s.ToolStateDir),
		},
	}
	for _, spec := range specs {
		installation := s.Tools.Detect(ctx, spec)
		response.Tools = append(response.Tools, installation)
		if spec.ProviderID == "git" {
			response.Registry = registryCheck(s, installation)
		}
		if installation.Status != detect.ToolInstalled && response.Status == "ok" {
			response.Status = "warning"
		}
	}
	sort.Slice(response.Tools, func(i, j int) bool { return response.Tools[i].ProviderID < response.Tools[j].ProviderID })
	if response.Registry.Status == "warning" && response.Status == "ok" {
		response.Status = "warning"
	}
	return response, nil
}

func registryCheck(s DoctorService, installation detect.ToolInstallation) RegistryCheck {
	check := RegistryCheck{Status: "available", Source: "builtin", KnowledgeStatus: "builtin_only", Entries: s.RegistryCount, Warnings: append([]string(nil), s.RegistryCache.Warnings...)}
	switch s.RegistryCache.Status {
	case registry.CacheInvalid:
		check.Status = "warning"
		check.KnowledgeStatus = "cache_invalid"
	case registry.CacheExact, registry.CacheStale:
		check.Source = "builtin+sync"
		check.KnowledgeStatus = "synced_exact"
		if s.RegistryCache.Snapshot == nil || installation.Status != detect.ToolInstalled || s.RegistryCache.Snapshot.KnowledgeVersion != installation.Version {
			check.Status = "warning"
			check.KnowledgeStatus = "synced_stale"
		}
	}
	return check
}

func ghIsolatedEnvironment(root string) map[string]string {
	if root == "" {
		return nil
	}
	return map[string]string{
		"HOME":            root,
		"GH_CONFIG_DIR":   filepath.Join(root, "gh-config"),
		"XDG_CONFIG_HOME": filepath.Join(root, "xdg-config"),
		"XDG_STATE_HOME":  filepath.Join(root, "xdg-state"),
	}
}
