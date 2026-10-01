package detect

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const (
	maxManifestBytes  = 1 << 20
	maxJSONDepth      = 64
	maxManagerVersion = 128
)

type packageManifest struct {
	hasWorkspace          bool
	hasNodeEngine         bool
	hasBunEngine          bool
	hasPackageManager     bool
	packageManagerValid   bool
	packageManager        string
	packageManagerVersion string
}

func readPackageManifest(path string) (packageManifest, *pathDiagnostic) {
	file, err := os.Open(path)
	if err != nil {
		return packageManifest{}, manifestDiagnostic("manifest_unreadable", path, "package manifest could not be read")
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return packageManifest{}, manifestDiagnostic("manifest_unreadable", path, "package manifest could not be read")
	}
	if len(content) > maxManifestBytes {
		return packageManifest{}, manifestDiagnostic("manifest_too_large", path, "package manifest exceeds the 1 MiB inspection limit")
	}
	manifest, err := parsePackageManifest(content)
	if err != nil {
		return packageManifest{}, manifestDiagnostic("manifest_invalid", path, "package manifest is not one valid bounded JSON object")
	}
	return manifest, nil
}

func parsePackageManifest(content []byte) (packageManifest, error) {
	if jsonDepth(content) > maxJSONDepth {
		return packageManifest{}, errors.New("manifest nesting exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return packageManifest{}, errors.New("manifest is not an object")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return packageManifest{}, errors.New("manifest has trailing data")
	}

	var manifest packageManifest
	if raw, ok := object["packageManager"]; ok {
		manifest.hasPackageManager = true
		var value string
		if json.Unmarshal(raw, &value) == nil {
			manager, version, valid := parsePackageManager(value)
			manifest.packageManagerValid = valid
			manifest.packageManager = manager
			manifest.packageManagerVersion = version
		}
	}
	if raw, ok := object["engines"]; ok {
		var engines map[string]json.RawMessage
		if json.Unmarshal(raw, &engines) == nil {
			manifest.hasNodeEngine = nonEmptyJSONString(engines["node"])
			manifest.hasBunEngine = nonEmptyJSONString(engines["bun"])
		}
	}
	if raw, ok := object["workspaces"]; ok {
		manifest.hasWorkspace = nonEmptyWorkspaces(raw)
	}
	return manifest, nil
}

func parsePackageManager(value string) (string, string, bool) {
	if value != strings.TrimSpace(value) || strings.Count(value, "@") != 1 {
		return "", "", false
	}
	manager, version, _ := strings.Cut(value, "@")
	switch manager {
	case "npm", "pnpm", "yarn", "bun":
	default:
		return "", "", false
	}
	if len(version) == 0 || len(version) > maxManagerVersion {
		return "", "", false
	}
	for index, character := range version {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		if index > 0 && strings.ContainsRune(".-_+~:", character) {
			continue
		}
		return "", "", false
	}
	return manager, version, true
}

func nonEmptyJSONString(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func nonEmptyWorkspaces(raw json.RawMessage) bool {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		return len(list) > 0
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return false
	}
	if packages, ok := object["packages"]; ok && json.Unmarshal(packages, &list) == nil {
		return len(list) > 0
	}
	return false
}

func jsonDepth(content []byte) int {
	depth, maximum := 0, 0
	inString, escaped := false, false
	for _, character := range content {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		if character == '"' {
			inString = true
			continue
		}
		switch character {
		case '{', '[':
			depth++
			if depth > maximum {
				maximum = depth
			}
		case '}', ']':
			depth--
		}
	}
	return maximum
}

func manifestDiagnostic(code, path, message string) *pathDiagnostic {
	return &pathDiagnostic{code: code, severity: "warning", path: path, message: message}
}
