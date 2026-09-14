package app

type BuildInfo struct {
	SchemaVersion int    `json:"schema_version"`
	Product       string `json:"product"`
	Command       string `json:"command"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	BuiltAt       string `json:"built_at"`
}

func VersionInfo(version, commit, builtAt string) BuildInfo {
	return BuildInfo{SchemaVersion: 1, Product: "Devtize", Command: "dvz", Version: version, Commit: commit, BuiltAt: builtAt}
}
