package detect

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var projectMarkers = []string{
	".git", "go.mod", "package.json", "bun.lock", "bun.lockb", "pnpm-lock.yaml",
	"yarn.lock", "package-lock.json",
}

func DetectProject(start string) (Project, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Project{}, fmt.Errorf("resolve project start: %w", err)
	}
	root := ""
	for current := dir; ; current = filepath.Dir(current) {
		found, findErr := hasProjectMarker(current)
		if findErr != nil {
			return Project{}, findErr
		}
		if found {
			root = current
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	if root == "" {
		return Project{Status: "not_found"}, nil
	}

	project := Project{Status: "detected", Root: root, Confidence: ConfidenceMedium}
	if exists(filepath.Join(root, ".git")) {
		project.Evidence = append(project.Evidence, Evidence{Kind: "repository", Path: ".git", Value: "git", Confidence: ConfidenceHigh})
		project.Confidence = ConfidenceHigh
	}
	if exists(filepath.Join(root, "go.mod")) {
		project.Runtimes = append(project.Runtimes, "go")
		project.Evidence = append(project.Evidence, Evidence{Kind: "runtime", Path: "go.mod", Value: "go", Confidence: ConfidenceHigh})
		project.Confidence = ConfidenceHigh
	}
	if exists(filepath.Join(root, "package.json")) {
		project.Runtimes = append(project.Runtimes, "javascript")
		project.Evidence = append(project.Evidence, Evidence{Kind: "runtime", Path: "package.json", Value: "javascript", Confidence: ConfidenceMedium})
	}
	detectPackageManager(root, &project)
	sort.Strings(project.Runtimes)
	return project, nil
}

func hasProjectMarker(dir string) (bool, error) {
	for _, marker := range projectMarkers {
		_, err := os.Stat(filepath.Join(dir, marker))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect project marker %s: %w", marker, err)
		}
	}
	return false, nil
}

func detectPackageManager(root string, project *Project) {
	explicit := packageManagerMetadata(filepath.Join(root, "package.json"))
	lockfiles := []struct{ file, manager string }{
		{"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"}, {"package-lock.json", "npm"},
	}
	seen := map[string]struct{}{}
	if explicit != "" {
		manager := strings.SplitN(explicit, "@", 2)[0]
		project.PackageManager = manager
		seen[manager] = struct{}{}
		project.Evidence = append(project.Evidence, Evidence{Kind: "package_manager", Path: "package.json", Value: explicit, Confidence: ConfidenceHigh})
		project.Confidence = ConfidenceHigh
	}
	for _, candidate := range lockfiles {
		if !exists(filepath.Join(root, candidate.file)) {
			continue
		}
		seen[candidate.manager] = struct{}{}
		project.Evidence = append(project.Evidence, Evidence{Kind: "package_manager", Path: candidate.file, Value: candidate.manager, Confidence: ConfidenceMedium})
		if project.PackageManager == "" {
			project.PackageManager = candidate.manager
		}
	}
	project.Ambiguous = len(seen) > 1
}

func packageManagerMetadata(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	var packageFile struct {
		PackageManager string `json:"packageManager"`
	}
	if json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&packageFile) != nil {
		return ""
	}
	return strings.TrimSpace(packageFile.PackageManager)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
