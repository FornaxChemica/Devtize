package detect

import (
	"strings"
	"testing"
)

func TestParsePackageManifestRequiresOneBoundedObject(t *testing.T) {
	deep := strings.Repeat(`{"x":`, maxJSONDepth+1) + `0` + strings.Repeat(`}`, maxJSONDepth+1)
	tests := []struct {
		name    string
		content string
		valid   bool
	}{
		{"object", `{"packageManager":"npm@10.0.0"}`, true},
		{"whitespace", " \n{\"workspaces\":[\"packages/*\"]}\n", true},
		{"array", `[]`, false},
		{"null", `null`, false},
		{"trailing", `{} {}`, false},
		{"malformed", `{"packageManager":`, false},
		{"deep", deep, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePackageManifest([]byte(test.content))
			if (err == nil) != test.valid {
				t.Fatalf("err = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestParsePackageManagerAcceptsOnlySafeSupportedMetadata(t *testing.T) {
	tests := []struct {
		value   string
		manager string
		version string
		valid   bool
	}{
		{"npm@10.0.0", "npm", "10.0.0", true},
		{"pnpm@10.0.0+sha.1", "pnpm", "10.0.0+sha.1", true},
		{"yarn@4.0.0-rc.1", "yarn", "4.0.0-rc.1", true},
		{"bun@1.2.0", "bun", "1.2.0", true},
		{"cargo@1.0.0", "", "", false},
		{"npm@", "", "", false},
		{"npm@10 0", "", "", false},
		{" npm@10", "", "", false},
		{"npm@10\nunsafe", "", "", false},
		{"npm@https://example.invalid/tool", "", "", false},
	}
	for _, test := range tests {
		manager, version, valid := parsePackageManager(test.value)
		if manager != test.manager || version != test.version || valid != test.valid {
			t.Fatalf("parsePackageManager(%q) = %q, %q, %t", test.value, manager, version, valid)
		}
	}
}

func TestParsePackageManifestRecognizesWorkspaceAndRuntimeMetadata(t *testing.T) {
	manifest, err := parsePackageManifest([]byte(`{
  "packageManager":"bun@1.2.0",
  "engines":{"node":">=22","bun":"1.2"},
  "workspaces":{"packages":["packages/*"]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.hasWorkspace || !manifest.hasNodeEngine || !manifest.hasBunEngine || !manifest.hasPackageManager || !manifest.packageManagerValid || manifest.packageManager != "bun" {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func FuzzParsePackageManifest(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"packageManager":"pnpm@10.0.0"}`),
		[]byte(`{} {}`),
		[]byte(`{"workspaces":["packages/*"]}`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content []byte) {
		if len(content) > maxManifestBytes+1 {
			t.Skip()
		}
		_, _ = parsePackageManifest(content)
	})
}

func FuzzParsePackageManager(f *testing.F) {
	for _, seed := range []string{"npm@10.0.0", "pnpm@10.0.0", "cargo@1", "npm@10\nsecret"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		manager, version, valid := parsePackageManager(value)
		if valid && (manager == "" || version == "" || strings.ContainsAny(manager+version, "\r\n\t ")) {
			t.Fatalf("unsafe accepted metadata: %q %q", manager, version)
		}
	})
}
