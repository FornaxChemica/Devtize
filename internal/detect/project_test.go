package detect

import (
	"os"
	"path/filepath"
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
