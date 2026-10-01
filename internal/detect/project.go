package detect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxProjectAncestors = 64
	maxProjectManifests = 16
	maxDiagnostics      = 32
)

var projectMarkers = []string{
	".git", "go.mod", "package.json", "tsconfig.json", "deno.json", "deno.jsonc",
	"deno.lock", "pnpm-workspace.yaml", "bun.lock", "bun.lockb", "pnpm-lock.yaml",
	"yarn.lock", "package-lock.json",
}

var lockfiles = []struct {
	file    string
	manager string
}{
	{"bun.lock", "bun"},
	{"bun.lockb", "bun"},
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"package-lock.json", "npm"},
}

type inspectedDirectory struct {
	path     string
	markers  map[string]bool
	manifest *packageManifest
}

type detectionScan struct {
	directories   map[string]*inspectedDirectory
	diagnostics   []pathDiagnostic
	manifestCount int
	limitReported bool
}

type pathDiagnostic struct {
	code     string
	severity string
	path     string
	message  string
}

type managerCandidate struct {
	manager    string
	version    string
	kind       string
	path       string
	scope      string
	confidence Confidence
	priority   int
}

type invalidManager struct {
	path   string
	reason string
}

func DetectProject(start string) (Project, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Project{}, fmt.Errorf("resolve project start: %w", err)
	}
	dir = filepath.Clean(dir)
	scan := &detectionScan{directories: make(map[string]*inspectedDirectory)}

	var root, workspace string
	current := dir
	for ancestor := 0; ancestor < maxProjectAncestors; ancestor++ {
		inspected := scan.inspect(current)
		if root == "" && len(inspected.markers) > 0 {
			root = current
		}
		if root != "" && workspace == "" {
			if inspected.markers["pnpm-workspace.yaml"] || inspected.manifest != nil && inspected.manifest.hasWorkspace {
				workspace = current
				break
			}
			if inspected.markers[".git"] {
				break
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
		if ancestor == maxProjectAncestors-1 {
			scan.addDiagnostic("ancestor_limit", "warning", current, "project ancestor inspection limit reached")
		}
	}

	if root == "" {
		project := Project{Status: "not_found"}
		project.Diagnostics = scan.normalizedDiagnostics(dir)
		return project, nil
	}
	if workspace == root {
		workspace = ""
	}

	project := Project{Status: "detected", Root: root, WorkspaceRoot: workspace, Confidence: ConfidenceMedium}
	base := root
	if workspace != "" {
		base = workspace
	}
	project.Diagnostics = scan.normalizedDiagnostics(base)

	runtimeSet := make(map[string]struct{})
	var candidates []managerCandidate
	var invalid []invalidManager
	collectProjectEvidence(&project, scan.inspect(root), "project", base, runtimeSet, &candidates, &invalid)
	if workspace != "" {
		collectProjectEvidence(&project, scan.inspect(workspace), "workspace", base, runtimeSet, &candidates, &invalid)
	}
	for runtime := range runtimeSet {
		project.Runtimes = append(project.Runtimes, runtime)
	}
	sort.Strings(project.Runtimes)
	selectPackageManager(&project, candidates, invalid)

	for _, item := range invalid {
		project.Diagnostics = append(project.Diagnostics, Diagnostic{
			Code: "package_manager_invalid", Severity: "warning", Path: relativeSlash(base, item.path),
			Message: "packageManager metadata is invalid or unsupported",
		})
	}
	project.Diagnostics = sortedDiagnostics(project.Diagnostics)
	sort.SliceStable(project.Evidence, func(i, j int) bool {
		left, right := project.Evidence[i], project.Evidence[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Value != right.Value {
			return left.Value < right.Value
		}
		return left.Scope < right.Scope
	})
	return project, nil
}

func (s *detectionScan) inspect(dir string) *inspectedDirectory {
	if inspected := s.directories[dir]; inspected != nil {
		return inspected
	}
	inspected := &inspectedDirectory{path: dir, markers: make(map[string]bool)}
	s.directories[dir] = inspected
	for _, marker := range projectMarkers {
		path := filepath.Join(dir, marker)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			s.addDiagnostic("marker_unreadable", "warning", path, "project marker could not be inspected")
			continue
		}
		if marker != ".git" && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			s.addDiagnostic("marker_unsafe", "warning", path, "non-Git project marker is not a regular non-symlink file")
			continue
		}
		inspected.markers[marker] = true
	}
	if inspected.markers["package.json"] {
		if s.manifestCount >= maxProjectManifests {
			if !s.limitReported {
				s.addDiagnostic("manifest_limit", "warning", filepath.Join(dir, "package.json"), "package manifest inspection limit reached")
				s.limitReported = true
			}
			return inspected
		}
		s.manifestCount++
		manifest, diagnostic := readPackageManifest(filepath.Join(dir, "package.json"))
		if diagnostic != nil {
			s.addDiagnostic(diagnostic.code, diagnostic.severity, diagnostic.path, diagnostic.message)
		} else {
			inspected.manifest = &manifest
		}
	}
	return inspected
}

func (s *detectionScan) addDiagnostic(code, severity, path, message string) {
	if len(s.diagnostics) >= maxDiagnostics {
		return
	}
	s.diagnostics = append(s.diagnostics, pathDiagnostic{code: code, severity: severity, path: path, message: message})
}

func (s *detectionScan) normalizedDiagnostics(base string) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, len(s.diagnostics))
	for _, item := range s.diagnostics {
		diagnostics = append(diagnostics, Diagnostic{
			Code: item.code, Severity: item.severity, Path: relativeSlash(base, item.path), Message: item.message,
		})
	}
	return sortedDiagnostics(diagnostics)
}

func collectProjectEvidence(project *Project, dir *inspectedDirectory, scope, base string, runtimes map[string]struct{}, candidates *[]managerCandidate, invalid *[]invalidManager) {
	markers := dir.markers
	addRuntime := func(runtime, marker string, confidence Confidence) {
		runtimes[runtime] = struct{}{}
		project.Evidence = append(project.Evidence, Evidence{
			Kind: "runtime", Path: relativeSlash(base, filepath.Join(dir.path, marker)), Value: runtime, Confidence: confidence, Scope: scope,
		})
		if confidence == ConfidenceHigh {
			project.Confidence = ConfidenceHigh
		}
	}
	if markers[".git"] {
		project.Evidence = append(project.Evidence, Evidence{Kind: "repository", Path: relativeSlash(base, filepath.Join(dir.path, ".git")), Value: "git", Confidence: ConfidenceHigh, Scope: scope})
		project.Confidence = ConfidenceHigh
	}
	if markers["go.mod"] {
		addRuntime("go", "go.mod", ConfidenceHigh)
	}
	if markers["package.json"] {
		addRuntime("javascript", "package.json", ConfidenceMedium)
		if dir.manifest != nil {
			if dir.manifest.hasNodeEngine {
				addRuntime("node", "package.json", ConfidenceHigh)
			}
			if dir.manifest.hasBunEngine {
				addRuntime("bun", "package.json", ConfidenceHigh)
			}
			if dir.manifest.hasPackageManager {
				path := filepath.Join(dir.path, "package.json")
				if dir.manifest.packageManagerValid {
					priority := 0
					if scope == "workspace" {
						priority = 1
					}
					*candidates = append(*candidates, managerCandidate{
						manager: dir.manifest.packageManager, version: dir.manifest.packageManagerVersion,
						kind: "metadata", path: path, scope: scope, confidence: ConfidenceHigh, priority: priority,
					})
					project.Confidence = ConfidenceHigh
					if dir.manifest.packageManager == "bun" {
						addRuntime("bun", "package.json", ConfidenceHigh)
					}
				} else {
					*invalid = append(*invalid, invalidManager{path: path, reason: "invalid_" + scope + "_metadata"})
				}
			}
		}
	}
	if markers["tsconfig.json"] {
		addRuntime("javascript", "tsconfig.json", ConfidenceMedium)
		addRuntime("typescript", "tsconfig.json", ConfidenceMedium)
	}
	for _, marker := range []string{"deno.json", "deno.jsonc", "deno.lock"} {
		if markers[marker] {
			addRuntime("deno", marker, ConfidenceHigh)
		}
	}
	if markers["pnpm-workspace.yaml"] {
		*candidates = append(*candidates, managerCandidate{
			manager: "pnpm", kind: "workspace_marker", path: filepath.Join(dir.path, "pnpm-workspace.yaml"),
			scope: "workspace", confidence: ConfidenceMedium, priority: 2,
		})
	}
	for order, lock := range lockfiles {
		if !markers[lock.file] {
			continue
		}
		addRuntime("javascript", lock.file, ConfidenceMedium)
		if lock.manager == "bun" {
			addRuntime("bun", lock.file, ConfidenceHigh)
		}
		priority := 3 + order
		if scope == "workspace" {
			priority = 8 + order
		}
		*candidates = append(*candidates, managerCandidate{
			manager: lock.manager, kind: "lockfile", path: filepath.Join(dir.path, lock.file), scope: scope,
			confidence: ConfidenceMedium, priority: priority,
		})
	}
}

func selectPackageManager(project *Project, candidates []managerCandidate, invalid []invalidManager) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].path < candidates[j].path
	})
	base := project.Root
	if project.WorkspaceRoot != "" {
		base = project.WorkspaceRoot
	}
	for _, candidate := range candidates {
		project.Evidence = append(project.Evidence, Evidence{
			Kind: "package_manager", Path: relativeSlash(base, candidate.path), Value: managerCandidateValue(candidate),
			Confidence: candidate.confidence, Scope: candidate.scope,
		})
	}
	if len(candidates) == 0 {
		if len(invalid) > 0 {
			project.Ambiguous = true
			project.PackageManagerConfidence = ConfidenceLow
			project.ResolutionHint = "Use one supported packageManager declaration and align recognized lockfiles with it."
		}
		for _, item := range invalid {
			appendRejectedAlternative(project, RejectedAlternative{Kind: "metadata", Value: "packageManager", Reason: item.reason})
		}
		if project.ResolutionHint == "" && containsRuntime(project.Runtimes, "javascript") && hasPackageEvidence(project.Evidence) {
			project.ResolutionHint = "Declare packageManager in package.json or add one recognized lockfile."
		}
		return
	}

	selected := candidates[0]
	project.PackageManager = selected.manager
	project.PackageManagerVersion = selected.version
	project.PackageManagerConfidence = selected.confidence
	for _, item := range invalid {
		project.Ambiguous = true
		appendRejectedAlternative(project, RejectedAlternative{Kind: "metadata", Value: "packageManager", Reason: item.reason})
	}
	for _, candidate := range candidates[1:] {
		if candidate.manager != selected.manager {
			project.Ambiguous = true
			appendRejectedAlternative(project, RejectedAlternative{
				Kind: candidate.kind, Value: managerCandidateValue(candidate), Reason: "conflicts_with_selected",
			})
			continue
		}
		if candidate.kind == "metadata" && selected.kind == "metadata" && candidate.version != selected.version {
			project.Ambiguous = true
			appendRejectedAlternative(project, RejectedAlternative{
				Kind: candidate.kind, Value: managerCandidateValue(candidate), Reason: "conflicting_version",
			})
		}
	}
	if project.Ambiguous {
		project.PackageManagerConfidence = ConfidenceLow
		project.ResolutionHint = "Use one supported packageManager declaration and align recognized lockfiles with it."
	}
	sort.SliceStable(project.RejectedAlternatives, func(i, j int) bool {
		left, right := project.RejectedAlternatives[i], project.RejectedAlternatives[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Value != right.Value {
			return left.Value < right.Value
		}
		return left.Reason < right.Reason
	})
}

func appendRejectedAlternative(project *Project, candidate RejectedAlternative) {
	for _, existing := range project.RejectedAlternatives {
		if existing == candidate {
			return
		}
	}
	project.RejectedAlternatives = append(project.RejectedAlternatives, candidate)
}

func managerCandidateValue(candidate managerCandidate) string {
	if candidate.version == "" {
		return candidate.manager
	}
	return candidate.manager + "@" + candidate.version
}

func hasPackageEvidence(evidence []Evidence) bool {
	for _, item := range evidence {
		if item.Path == "package.json" || strings.HasSuffix(item.Path, "/package.json") {
			return true
		}
	}
	return false
}

func containsRuntime(runtimes []string, want string) bool {
	for _, runtime := range runtimes {
		if runtime == want {
			return true
		}
	}
	return false
}

func relativeSlash(base, path string) string {
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(relative)
}

func sortedDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Path != diagnostics[j].Path {
			return diagnostics[i].Path < diagnostics[j].Path
		}
		if diagnostics[i].Code != diagnostics[j].Code {
			return diagnostics[i].Code < diagnostics[j].Code
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	return diagnostics
}
