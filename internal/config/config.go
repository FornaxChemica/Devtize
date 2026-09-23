package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/FornaxChemica/devtize/internal/registry"
	"gopkg.in/yaml.v3"
)

const SchemaVersion = 1

type ColorMode string

const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

type Config struct {
	Version int           `json:"version" yaml:"version"`
	UI      UIConfig      `json:"ui" yaml:"ui"`
	AI      AIConfig      `json:"ai" yaml:"ai"`
	Safety  SafetyConfig  `json:"safety" yaml:"safety"`
	History HistoryConfig `json:"history" yaml:"history"`
	Checks  ChecksConfig  `json:"checks" yaml:"checks"`
	Git     GitConfig     `json:"git" yaml:"git"`
	GitHub  GitHubConfig  `json:"github" yaml:"github"`
}

type UIConfig struct {
	Color ColorMode `json:"color" yaml:"color"`
}

type AIConfig struct {
	Provider string `json:"provider" yaml:"provider"`
}

type SafetyConfig struct {
	ConfirmLocalWrites  bool   `json:"confirm_local_writes" yaml:"confirm_local_writes"`
	ConfirmRemoteWrites bool   `json:"confirm_remote_writes" yaml:"confirm_remote_writes"`
	AllowYesFor         string `json:"allow_yes_for" yaml:"allow_yes_for"`
}

type HistoryConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type ChecksConfig struct {
	Ship []string `json:"ship" yaml:"ship"`
}

type GitConfig struct {
	DefaultBranch string `json:"default_branch" yaml:"default_branch"`
}

type GitHubConfig struct {
	Visibility string `json:"visibility" yaml:"visibility"`
}

type Overrides struct {
	Color      *ColorMode
	AIProvider *string
}

type LoadOptions struct {
	WorkingDir    string
	Environment   map[string]string
	UserConfigDir func() (string, error)
	Overrides     Overrides
}

type Result struct {
	Config      Config   `json:"config"`
	Sources     []string `json:"sources"`
	UserPath    string   `json:"user_path"`
	ProjectPath string   `json:"project_path,omitempty"`
}

type fileConfig struct {
	Version *int         `yaml:"version"`
	UI      *fileUI      `yaml:"ui"`
	AI      *fileAI      `yaml:"ai"`
	Safety  *fileSafety  `yaml:"safety"`
	History *fileHistory `yaml:"history"`
	Checks  *fileChecks  `yaml:"checks"`
	Git     *fileGit     `yaml:"git"`
	GitHub  *fileGitHub  `yaml:"github"`
}

type fileUI struct {
	Color *ColorMode `yaml:"color"`
}

type fileAI struct {
	Provider *string `yaml:"provider"`
}

type fileSafety struct {
	ConfirmLocalWrites  *bool    `yaml:"confirm_local_writes"`
	ConfirmRemoteWrites *bool    `yaml:"confirm_remote_writes"`
	AllowYesFor         []string `yaml:"allow_yes_for"`
}

type fileHistory struct {
	Enabled *bool `yaml:"enabled"`
}

type fileChecks struct {
	Ship []string `yaml:"ship"`
}

type fileGit struct {
	DefaultBranch *string `yaml:"default_branch"`
}

type fileGitHub struct {
	Visibility *string `yaml:"visibility"`
}

func Defaults() Config {
	return Config{
		Version: SchemaVersion,
		UI:      UIConfig{Color: ColorAuto},
		AI:      AIConfig{Provider: "disabled"},
		Safety:  SafetyConfig{ConfirmLocalWrites: true, ConfirmRemoteWrites: true, AllowYesFor: "local_write"},
		History: HistoryConfig{Enabled: true},
		Git:     GitConfig{DefaultBranch: "main"},
		GitHub:  GitHubConfig{Visibility: "private"},
	}
}

func Load(options LoadOptions) (Result, error) {
	if options.WorkingDir == "" {
		return Result{}, errors.New("working directory is required")
	}
	if options.Environment == nil {
		options.Environment = Environment(os.Environ())
	}
	if options.UserConfigDir == nil {
		options.UserConfigDir = os.UserConfigDir
	}

	userPath, err := UserPath(options.Environment, options.UserConfigDir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve user config path: %w", err)
	}
	result := Result{Config: Defaults(), UserPath: userPath}
	if err := applyFile(userPath, &result); err != nil {
		return result, err
	}

	projectPath, err := FindProjectConfig(options.WorkingDir)
	if err != nil {
		return result, err
	}
	result.ProjectPath = projectPath
	if projectPath != "" {
		if err := applyFile(projectPath, &result); err != nil {
			return result, err
		}
	}

	if color := options.Environment["DVZ_COLOR"]; color != "" {
		result.Config.UI.Color = ColorMode(color)
		result.Sources = append(result.Sources, "env:DVZ_COLOR")
	}
	if provider := options.Environment["DVZ_AI_PROVIDER"]; provider != "" {
		result.Config.AI.Provider = provider
		result.Sources = append(result.Sources, "env:DVZ_AI_PROVIDER")
	}
	if options.Environment["NO_COLOR"] != "" {
		result.Config.UI.Color = ColorNever
		result.Sources = append(result.Sources, "env:NO_COLOR")
	}
	if options.Overrides.Color != nil {
		result.Config.UI.Color = *options.Overrides.Color
		result.Sources = append(result.Sources, "cli:color")
	}
	if options.Overrides.AIProvider != nil {
		result.Config.AI.Provider = *options.Overrides.AIProvider
		result.Sources = append(result.Sources, "cli:ai-provider")
	}
	if err := result.Config.Validate(); err != nil {
		return result, err
	}
	return result, nil
}

func (c Config) Validate() error {
	if c.Version != SchemaVersion {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	switch c.UI.Color {
	case ColorAuto, ColorAlways, ColorNever:
	default:
		return fmt.Errorf("unsupported UI color mode %q", c.UI.Color)
	}
	if c.AI.Provider != "disabled" {
		return fmt.Errorf("AI provider %q is unavailable in this phase", c.AI.Provider)
	}
	if c.Git.DefaultBranch == "" {
		return errors.New("git.default_branch is required")
	}
	if err := registry.ValidateCheckIDs(c.Checks.Ship); err != nil {
		return err
	}
	switch c.GitHub.Visibility {
	case "private", "public":
	default:
		return fmt.Errorf("unsupported GitHub visibility %q", c.GitHub.Visibility)
	}
	return nil
}

func UserPath(environment map[string]string, userConfigDir func() (string, error)) (string, error) {
	if xdg := environment["XDG_CONFIG_HOME"]; xdg != "" {
		if !filepath.IsAbs(xdg) {
			return "", errors.New("XDG_CONFIG_HOME must be absolute")
		}
		return filepath.Join(xdg, "devtize", "config.yaml"), nil
	}
	base, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "devtize", "config.yaml"), nil
}

func FindProjectConfig(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve project config start: %w", err)
	}
	for {
		candidate := filepath.Join(dir, ".dvz.yaml")
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if info.IsDir() {
				return "", fmt.Errorf("project config %s is a directory", candidate)
			}
			return candidate, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("inspect project config %s: %w", candidate, statErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func applyFile(path string, result *Result) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open config %s: %w", path, err)
	}
	defer file.Close()

	var layer fileConfig
	decoder := yaml.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.KnownFields(true)
	if err := decoder.Decode(&layer); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	if layer.Version == nil {
		return fmt.Errorf("parse config %s: version is required", path)
	}
	if *layer.Version != SchemaVersion {
		return fmt.Errorf("parse config %s: unsupported version %d", path, *layer.Version)
	}
	result.Config.Version = *layer.Version
	if layer.UI != nil && layer.UI.Color != nil {
		result.Config.UI.Color = *layer.UI.Color
	}
	if layer.AI != nil && layer.AI.Provider != nil {
		result.Config.AI.Provider = *layer.AI.Provider
	}
	if layer.Safety != nil {
		if layer.Safety.ConfirmLocalWrites != nil {
			result.Config.Safety.ConfirmLocalWrites = *layer.Safety.ConfirmLocalWrites
		}
		if layer.Safety.ConfirmRemoteWrites != nil {
			result.Config.Safety.ConfirmRemoteWrites = *layer.Safety.ConfirmRemoteWrites
		}
		if layer.Safety.AllowYesFor != nil {
			result.Config.Safety.AllowYesFor = strings.Join(layer.Safety.AllowYesFor, ",")
		}
	}
	if layer.History != nil && layer.History.Enabled != nil {
		result.Config.History.Enabled = *layer.History.Enabled
	}
	if layer.Checks != nil && layer.Checks.Ship != nil {
		result.Config.Checks.Ship = append([]string(nil), layer.Checks.Ship...)
	}
	if layer.Git != nil && layer.Git.DefaultBranch != nil {
		result.Config.Git.DefaultBranch = *layer.Git.DefaultBranch
	}
	if layer.GitHub != nil && layer.GitHub.Visibility != nil {
		result.Config.GitHub.Visibility = *layer.GitHub.Visibility
	}
	result.Sources = append(result.Sources, path)
	return nil
}

func Environment(values []string) map[string]string {
	environment := make(map[string]string, len(values))
	for _, value := range values {
		name, content, found := strings.Cut(value, "=")
		if found {
			environment[name] = content
		}
	}
	return environment
}

var (
	secretAssignment = regexp.MustCompile(`(?i)(token|password|secret|authorization)(\s*[:=]\s*)([^\s,;]+)`)
	bearerValue      = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`)
)

func Redact(value string) string {
	value = secretAssignment.ReplaceAllString(value, "$1$2<redacted>")
	return bearerValue.ReplaceAllString(value, "Bearer <redacted>")
}
