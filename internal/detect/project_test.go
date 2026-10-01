package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDetectProjectFindsGoAndJavaScriptEvidence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/project\n")
	writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"pnpm@10.0.0"}`)
	writeFile(t, filepath.Join(root, "pnpm-lock.yaml"), "lockfileVersion: 9\n")
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := DetectProject(nested)
	if err != nil {
		t.Fatal(err)
	}
	if project.Status != "detected" || project.Root != root || project.PackageManager != "pnpm" || project.Confidence != ConfidenceHigh {
		t.Fatalf("unexpected project: %#v", project)
	}
	if project.Ambiguous {
		t.Fatal("matching metadata and lockfile should not be ambiguous")
	}
	if project.PackageManagerVersion != "10.0.0" || project.PackageManagerConfidence != ConfidenceHigh {
		t.Fatalf("package manager detail = %#v", project)
	}
}

func TestDetectProjectReportsConflictingPackageManagers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"bun@1.2.0"}`)
	writeFile(t, filepath.Join(root, "yarn.lock"), "")
	project, err := DetectProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if project.PackageManager != "bun" || !project.Ambiguous {
		t.Fatalf("conflicting project = %#v", project)
	}
}

func TestDetectProjectUsesLockfilePriorityAndDoesNotInferNPM(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{}`)
	writeFile(t, filepath.Join(root, "bun.lock"), "")
	writeFile(t, filepath.Join(root, "package-lock.json"), `{}`)
	project, err := DetectProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if project.PackageManager != "bun" || !project.Ambiguous {
		t.Fatalf("lockfile priority result = %#v", project)
	}

	onlyPackage := t.TempDir()
	writeFile(t, filepath.Join(onlyPackage, "package.json"), `{}`)
	project, err = DetectProject(onlyPackage)
	if err != nil {
		t.Fatal(err)
	}
	if project.PackageManager != "" {
		t.Fatalf("package.json alone inferred %q", project.PackageManager)
	}
}

func TestDetectProjectReturnsNotFound(t *testing.T) {
	project, err := DetectProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if project.Status != "not_found" || project.Root != "" {
		t.Fatalf("unexpected project: %#v", project)
	}
}

func TestDetectProjectRecognizesEveryPackageManagerFixture(t *testing.T) {
	tests := []struct {
		name    string
		manager string
	}{
		{"bun-current", "bun"},
		{"bun-legacy", "bun"},
		{"pnpm", "pnpm"},
		{"yarn", "yarn"},
		{"npm", "npm"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project, err := DetectProject(fixturePath("locks", test.name))
			if err != nil {
				t.Fatal(err)
			}
			if project.PackageManager != test.manager || project.PackageManagerConfidence != ConfidenceMedium || project.Ambiguous {
				t.Fatalf("project = %#v", project)
			}
		})
	}
}

func TestDetectProjectKeepsRuntimeEvidenceDistinctAndSorted(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{fixturePath("runtime", "node"), []string{"javascript", "node"}},
		{fixturePath("runtime", "deno"), []string{"deno"}},
		{fixturePath("runtime", "typescript"), []string{"javascript", "typescript"}},
		{fixturePath("locks", "bun-current"), []string{"bun", "javascript"}},
	}
	for _, test := range tests {
		project, err := DetectProject(test.path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(project.Runtimes, test.want) {
			t.Fatalf("%s runtimes = %v, want %v", test.path, project.Runtimes, test.want)
		}
	}
}

func TestDetectProjectRecognizesEachDenoMarkerAndBunMetadata(t *testing.T) {
	for _, marker := range []string{"deno.json", "deno.jsonc", "deno.lock"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, marker), `{}`)
			project, err := DetectProject(root)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(project.Runtimes, []string{"deno"}) {
				t.Fatalf("project = %#v", project)
			}
		})
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"engines":{"bun":"1.2"}}`)
	project, err := DetectProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project.Runtimes, []string{"bun", "javascript"}) || project.PackageManager != "" {
		t.Fatalf("project = %#v", project)
	}
}

func TestDetectProjectFindsNestedWorkspaceAndNormalizesEvidencePaths(t *testing.T) {
	workspace := fixturePath("monorepo")
	projectRoot := filepath.Join(workspace, "packages", "app")
	project, err := DetectProject(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if project.Root != projectRoot || project.WorkspaceRoot != workspace || project.PackageManager != "pnpm" || project.PackageManagerVersion != "10.0.0" {
		t.Fatalf("project = %#v", project)
	}
	for _, evidence := range project.Evidence {
		if filepath.IsAbs(evidence.Path) || strings.Contains(evidence.Path, `\`) {
			t.Fatalf("unsafe evidence path %q", evidence.Path)
		}
	}
	if !hasEvidence(project, "runtime", "packages/app/package.json", "javascript", "project") || !hasEvidence(project, "package_manager", "package.json", "pnpm@10.0.0", "workspace") {
		t.Fatalf("evidence = %#v", project.Evidence)
	}
}

func TestDetectProjectUsesNearestWorkspaceAndHonorsGitBoundary(t *testing.T) {
	t.Run("nearest workspace", func(t *testing.T) {
		outer := t.TempDir()
		writeFile(t, filepath.Join(outer, "package.json"), `{"workspaces":["inner/*"],"packageManager":"yarn@4.0.0"}`)
		inner := filepath.Join(outer, "inner")
		writeFile(t, filepath.Join(inner, "package.json"), `{"workspaces":["apps/*"],"packageManager":"pnpm@10.0.0"}`)
		app := filepath.Join(inner, "apps", "web")
		writeFile(t, filepath.Join(app, "package.json"), `{}`)
		project, err := DetectProject(app)
		if err != nil {
			t.Fatal(err)
		}
		if project.WorkspaceRoot != inner || project.PackageManager != "pnpm" || project.Ambiguous {
			t.Fatalf("project = %#v", project)
		}
	})

	t.Run("git boundary", func(t *testing.T) {
		outside := t.TempDir()
		writeFile(t, filepath.Join(outside, "package.json"), `{"workspaces":["repo/*"],"packageManager":"yarn@4.0.0"}`)
		repo := filepath.Join(outside, "repo")
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		app := filepath.Join(repo, "app")
		writeFile(t, filepath.Join(app, "package.json"), `{}`)
		project, err := DetectProject(app)
		if err != nil {
			t.Fatal(err)
		}
		if project.WorkspaceRoot != "" || project.PackageManager != "" {
			t.Fatalf("workspace escaped Git boundary: %#v", project)
		}
	})
}

func TestDetectProjectPackageManagerPrecedenceAndConflicts(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*testing.T) string
		manager     string
		version     string
		confidence  Confidence
		ambiguous   bool
		alternative string
	}{
		{
			name: "project metadata beats workspace metadata",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "package.json"), `{"workspaces":["packages/*"],"packageManager":"yarn@4.0.0"}`)
				child := filepath.Join(root, "packages", "app")
				writeFile(t, filepath.Join(child, "package.json"), `{"packageManager":"bun@1.2.0"}`)
				return child
			},
			manager: "bun", version: "1.2.0", confidence: ConfidenceLow, ambiguous: true, alternative: "yarn@4.0.0",
		},
		{
			name: "workspace marker beats project lock",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "pnpm-workspace.yaml"), "packages:\n  - packages/*\n")
				child := filepath.Join(root, "packages", "app")
				writeFile(t, filepath.Join(child, "package.json"), `{}`)
				writeFile(t, filepath.Join(child, "yarn.lock"), "")
				return child
			},
			manager: "pnpm", confidence: ConfidenceLow, ambiguous: true, alternative: "yarn",
		},
		{
			name: "project lock beats workspace lock",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "package.json"), `{"workspaces":["packages/*"]}`)
				writeFile(t, filepath.Join(root, "package-lock.json"), `{}`)
				child := filepath.Join(root, "packages", "app")
				writeFile(t, filepath.Join(child, "package.json"), `{}`)
				writeFile(t, filepath.Join(child, "yarn.lock"), "")
				return child
			},
			manager: "yarn", confidence: ConfidenceLow, ambiguous: true, alternative: "npm",
		},
		{
			name: "invalid metadata lowers lock confidence",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"cargo@1.0.0"}`)
				writeFile(t, filepath.Join(root, "pnpm-lock.yaml"), "")
				return root
			},
			manager: "pnpm", confidence: ConfidenceLow, ambiguous: true, alternative: "packageManager",
		},
		{
			name: "different metadata versions conflict",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "package.json"), `{"workspaces":["packages/*"],"packageManager":"pnpm@9.0.0"}`)
				child := filepath.Join(root, "packages", "app")
				writeFile(t, filepath.Join(child, "package.json"), `{"packageManager":"pnpm@10.0.0"}`)
				return child
			},
			manager: "pnpm", version: "10.0.0", confidence: ConfidenceLow, ambiguous: true, alternative: "pnpm@9.0.0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project, err := DetectProject(test.setup(t))
			if err != nil {
				t.Fatal(err)
			}
			if project.PackageManager != test.manager || project.PackageManagerVersion != test.version || project.PackageManagerConfidence != test.confidence || project.Ambiguous != test.ambiguous {
				t.Fatalf("project = %#v", project)
			}
			if !hasAlternative(project, test.alternative) || project.ResolutionHint == "" {
				t.Fatalf("alternatives/hint = %#v / %q", project.RejectedAlternatives, project.ResolutionHint)
			}
		})
	}
}

func TestDetectProjectTreatsMatchingBunLocksAsOneManager(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "bun.lock"), "")
	writeFile(t, filepath.Join(root, "bun.lockb"), "")
	project, err := DetectProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if project.PackageManager != "bun" || project.Ambiguous || len(project.RejectedAlternatives) != 0 {
		t.Fatalf("project = %#v", project)
	}
}

func TestDetectProjectUnresolvedPackageIsInformational(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{}`)
	project, err := DetectProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if project.PackageManager != "" || project.PackageManagerConfidence != "" || project.Ambiguous || project.ResolutionHint == "" || len(project.Diagnostics) != 0 {
		t.Fatalf("project = %#v", project)
	}
}

func TestDetectProjectRejectsUnsafeAndMalformedMarkers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/safe\n")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "go.mod"), filepath.Join(nested, "package.json")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(nested, "tsconfig.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := DetectProject(nested)
	if err != nil {
		t.Fatal(err)
	}
	if project.Root != root || containsRuntime(project.Runtimes, "javascript") || countDiagnostic(project, "marker_unsafe") != 2 {
		t.Fatalf("project = %#v", project)
	}
}

func TestDetectProjectBoundsManifestAndAncestorInspection(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		root := t.TempDir()
		content := append([]byte(`{"padding":"`), make([]byte, maxManifestBytes)...)
		writeFile(t, filepath.Join(root, "package.json"), string(content))
		project, err := DetectProject(root)
		if err != nil {
			t.Fatal(err)
		}
		if countDiagnostic(project, "manifest_too_large") != 1 {
			t.Fatalf("diagnostics = %#v", project.Diagnostics)
		}
	})

	t.Run("manifest count", func(t *testing.T) {
		root := t.TempDir()
		current := root
		for index := 0; index < maxProjectManifests+1; index++ {
			current = filepath.Join(current, "p")
			writeFile(t, filepath.Join(current, "package.json"), `{}`)
		}
		project, err := DetectProject(current)
		if err != nil {
			t.Fatal(err)
		}
		if countDiagnostic(project, "manifest_limit") != 1 {
			t.Fatalf("diagnostics = %#v", project.Diagnostics)
		}
	})

	t.Run("ancestor count", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "go.mod"), "module example.test/too-far\n")
		current := root
		for index := 0; index < maxProjectAncestors+1; index++ {
			current = filepath.Join(current, "d")
			if err := os.Mkdir(current, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		project, err := DetectProject(current)
		if err != nil {
			t.Fatal(err)
		}
		if project.Status != "not_found" || countDiagnostic(project, "ancestor_limit") != 1 {
			t.Fatalf("project = %#v", project)
		}
	})
}

func TestDetectProjectMalformedManifestUsesSafeDiagnostic(t *testing.T) {
	project, err := DetectProject(fixturePath("malformed"))
	if err != nil {
		t.Fatal(err)
	}
	if countDiagnostic(project, "manifest_invalid") != 1 {
		t.Fatalf("diagnostics = %#v", project.Diagnostics)
	}
	for _, diagnostic := range project.Diagnostics {
		if strings.Contains(diagnostic.Message, "pnpm") || strings.ContainsAny(diagnostic.Message, "\n\r\t") {
			t.Fatalf("unsafe diagnostic = %#v", diagnostic)
		}
	}
}

func TestDetectProjectDoesNotExposeControlBearingPackageManager(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"npm@10\u000asecret"}`)
	project, err := DetectProject(root)
	if err != nil {
		t.Fatal(err)
	}
	encoded := fmt.Sprintf("%#v", project)
	if strings.Contains(encoded, "secret") || !project.Ambiguous || countDiagnostic(project, "package_manager_invalid") != 1 {
		t.Fatalf("unsafe project = %s", encoded)
	}
}

func TestDetectProjectUnreadableManifestFailsSafely(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owner-only permission behavior differs on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "package.json")
	writeFile(t, path, `{}`)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	project, err := DetectProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if countDiagnostic(project, "manifest_unreadable") != 1 {
		t.Skipf("filesystem permits reads despite mode 000; diagnostics=%#v", project.Diagnostics)
	}
}

func TestRelativeSlashIsStableForNativeNestedPaths(t *testing.T) {
	base := filepath.Join("root", "workspace")
	path := filepath.Join(base, "packages", "app", "package.json")
	if got := relativeSlash(base, path); got != "packages/app/package.json" {
		t.Fatalf("relativeSlash = %q", got)
	}
}

func hasEvidence(project Project, kind, path, value, scope string) bool {
	for _, evidence := range project.Evidence {
		if evidence.Kind == kind && evidence.Path == path && evidence.Value == value && evidence.Scope == scope {
			return true
		}
	}
	return false
}

func hasAlternative(project Project, value string) bool {
	for _, alternative := range project.RejectedAlternatives {
		if alternative.Value == value {
			return true
		}
	}
	return false
}

func countDiagnostic(project Project, code string) int {
	count := 0
	for _, diagnostic := range project.Diagnostics {
		if diagnostic.Code == code {
			count++
		}
	}
	return count
}

func fixturePath(parts ...string) string {
	all := append([]string{"..", "..", "testdata", "projects", "javascript"}, parts...)
	path, err := filepath.Abs(filepath.Join(all...))
	if err != nil {
		panic(err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
