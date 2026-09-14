package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenFilesAreMissing(t *testing.T) {
	working := t.TempDir()
	user := t.TempDir()
	result, err := Load(LoadOptions{WorkingDir: working, Environment: map[string]string{}, UserConfigDir: func() (string, error) { return user, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Config != Defaults() || len(result.Sources) != 0 {
		t.Fatalf("unexpected defaults: %#v", result)
	}
}

func TestLoadPrecedence(t *testing.T) {
	working := t.TempDir()
	user := t.TempDir()
	writeConfig(t, filepath.Join(user, "devtize", "config.yaml"), "version: 1\nui:\n  color: always\n")
	writeConfig(t, filepath.Join(working, ".dvz.yaml"), "version: 1\nui:\n  color: auto\n")
	never := ColorNever
	result, err := Load(LoadOptions{
		WorkingDir: working, Environment: map[string]string{"DVZ_COLOR": "always"},
		UserConfigDir: func() (string, error) { return user, nil }, Overrides: Overrides{Color: &never},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.UI.Color != ColorNever {
		t.Fatalf("color = %q, want CLI override never", result.Config.UI.Color)
	}
	wantSources := 4
	if len(result.Sources) != wantSources {
		t.Fatalf("sources = %#v, want %d layers", result.Sources, wantSources)
	}
}

func TestLoadRejectsUnknownFieldsAndVersions(t *testing.T) {
	for name, content := range map[string]string{
		"syntax":  "version: [\n",
		"unknown": "version: 1\nfuture: true\n",
		"version": "version: 2\n",
		"ai":      "version: 1\nai:\n  provider: hosted\n",
	} {
		t.Run(name, func(t *testing.T) {
			working := t.TempDir()
			writeConfig(t, filepath.Join(working, ".dvz.yaml"), content)
			_, err := Load(LoadOptions{WorkingDir: working, Environment: map[string]string{}, UserConfigDir: func() (string, error) { return t.TempDir(), nil }})
			if err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
}

func TestProjectConfigDiscoveryUsesNearestAncestor(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".dvz.yaml")
	writeConfig(t, want, "version: 1\n")
	got, err := FindProjectConfig(nested)
	if err != nil || got != want {
		t.Fatalf("FindProjectConfig() = %q, %v; want %q", got, err, want)
	}
}

func TestUserPathSupportsXDGAndPlatformFallback(t *testing.T) {
	path, err := UserPath(map[string]string{"XDG_CONFIG_HOME": "/tmp/xdg"}, func() (string, error) { return "/platform", nil })
	if err != nil || path != filepath.Join("/tmp/xdg", "devtize", "config.yaml") {
		t.Fatalf("XDG path = %q, %v", path, err)
	}
	path, err = UserPath(map[string]string{}, func() (string, error) { return "/platform", nil })
	if err != nil || path != filepath.Join("/platform", "devtize", "config.yaml") {
		t.Fatalf("platform path = %q, %v", path, err)
	}
	if _, err := UserPath(map[string]string{"XDG_CONFIG_HOME": "relative"}, func() (string, error) { return "/platform", nil }); err == nil {
		t.Fatal("relative XDG_CONFIG_HOME was accepted")
	}
}

func TestRedactRemovesSecretValues(t *testing.T) {
	input := "token=abc123 password: hunter2 Authorization=Bearer-secret Bearer live_value"
	result := Redact(input)
	for _, secret := range []string{"abc123", "hunter2", "Bearer-secret", "live_value"} {
		if strings.Contains(result, secret) {
			t.Fatalf("redacted output contains %q: %s", secret, result)
		}
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func FuzzRedact(f *testing.F) {
	f.Add("sample")
	f.Add("with spaces")
	f.Fuzz(func(t *testing.T, value string) {
		secret := hex.EncodeToString([]byte(value))
		if secret == "" {
			return
		}
		result := Redact("token=" + secret)
		if result != "token=<redacted>" {
			t.Fatalf("redaction result for %q = %q", value, result)
		}
	})
}
