package app

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/FornaxChemica/devtize/internal/detect"
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
	ToolStateDir  string
}

type ConfigCheck struct {
	Status  string   `json:"status"`
	Sources []string `json:"sources,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

type RegistryCheck struct {
	Status          string `json:"status"`
	Source          string `json:"source"`
	KnowledgeStatus string `json:"knowledge_status"`
	Entries         int    `json:"entries"`
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
		Registry: RegistryCheck{Status: "available", Source: "builtin", KnowledgeStatus: "builtin_only", Entries: s.RegistryCount},
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
		if installation.Status != detect.ToolInstalled && response.Status == "ok" {
			response.Status = "warning"
		}
	}
	sort.Slice(response.Tools, func(i, j int) bool { return response.Tools[i].ProviderID < response.Tools[j].ProviderID })
	return response, nil
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
