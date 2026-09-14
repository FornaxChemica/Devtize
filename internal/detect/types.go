package detect

import "time"

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

type Evidence struct {
	Kind       string     `json:"kind"`
	Path       string     `json:"path"`
	Value      string     `json:"value,omitempty"`
	Confidence Confidence `json:"confidence"`
}

type Project struct {
	Status         string     `json:"status"`
	Root           string     `json:"root,omitempty"`
	Runtimes       []string   `json:"runtimes,omitempty"`
	PackageManager string     `json:"package_manager,omitempty"`
	Evidence       []Evidence `json:"evidence,omitempty"`
	Confidence     Confidence `json:"confidence,omitempty"`
	Ambiguous      bool       `json:"ambiguous"`
}

type ToolStatus string

const (
	ToolInstalled          ToolStatus = "installed"
	ToolMissing            ToolStatus = "missing"
	ToolVersionUnparseable ToolStatus = "version_unparseable"
	ToolVersionUnsupported ToolStatus = "version_unsupported"
	ToolError              ToolStatus = "error"
)

type ToolInstallation struct {
	ProviderID string     `json:"provider"`
	Executable string     `json:"executable"`
	Path       string     `json:"path,omitempty"`
	Version    string     `json:"version,omitempty"`
	Status     ToolStatus `json:"status"`
	Readiness  string     `json:"readiness"`
	AuthStatus string     `json:"auth_status,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	DetectedAt time.Time  `json:"-"`
}
