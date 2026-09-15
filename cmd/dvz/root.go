package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/detect"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/search"
	"github.com/FornaxChemica/devtize/registry/builtin"
	"github.com/spf13/cobra"
)

var (
	version = "devel"
	commit  = "unknown"
	builtAt = "unknown"
)

const (
	exitSuccess           = 0
	exitInvalid           = 2
	exitCancelled         = 3
	exitPolicyDenied      = 4
	exitMissingDependency = 5
	exitProcessFailure    = 6
)

type rootOptions struct {
	jsonOutput bool
	noColor    bool
}

type dependencies struct {
	workingDir    string
	environment   map[string]string
	userConfigDir func() (string, error)
	runner        detect.Runner
}

func defaultDependencies() dependencies {
	workingDir, err := os.Getwd()
	if err != nil {
		workingDir = "."
	}
	return dependencies{
		workingDir: workingDir, environment: config.Environment(os.Environ()),
		userConfigDir: os.UserConfigDir, runner: devprocess.NewRunner(),
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	command, options, err := newRootCommand(defaultDependencies(), stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitProcessFailure
	}
	command.SetArgs(args)
	err = command.Execute()
	if err == nil {
		return exitSuccess
	}
	var operational *app.Error
	if !errors.As(err, &operational) {
		err = app.Wrap(app.CodeInvalidUsage, err.Error(), err)
	}
	renderError(stderr, options.jsonOutput, err)
	return exitCode(err)
}

func newRootCommand(deps dependencies, stdout, stderr io.Writer) (*cobra.Command, *rootOptions, error) {
	catalog, err := builtin.Catalog()
	if err != nil {
		return nil, nil, fmt.Errorf("load builtin registry: %w", err)
	}
	options := &rootOptions{}
	root := &cobra.Command{
		Use:           "dvz",
		Short:         "Devtize is a local-first developer command layer",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().BoolVar(&options.jsonOutput, "json", false, "emit versioned JSON output")
	root.PersistentFlags().BoolVar(&options.noColor, "no-color", false, "disable colored output")

	findService := app.FindService{Search: search.New(catalog.Commands())}
	root.AddCommand(findCommand(deps, options, findService, stdout))
	root.AddCommand(doctorCommand(deps, options, catalog.Len(), stdout))
	root.AddCommand(commitCommand(deps, options, stdout))
	root.AddCommand(repoCommand(deps, options, stdout))
	root.AddCommand(versionCommand(options, stdout))
	return root, options, nil
}

func commitCommand(deps dependencies, rootOptions *rootOptions, stdout io.Writer) *cobra.Command {
	options := app.CommitOptions{Conventional: true}
	command := &cobra.Command{
		Use: "commit [paths...]", Short: "Create a verified commit from disclosed changed paths",
		RunE: func(command *cobra.Command, paths []string) error {
			options.Paths = paths
			loaded, err := loadConfig(deps, rootOptions)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			service := commitService(deps, loaded.Config, stdout)
			response, err := service.Plan(command.Context(), options)
			if err != nil {
				return err
			}
			if !rootOptions.jsonOutput && !options.PlanJSON {
				renderCommitPlan(stdout, response)
			}
			response, err = service.ExecutePlanned(command.Context(), options, command.InOrStdin(), response)
			if rootOptions.jsonOutput || options.PlanJSON {
				if options.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else if response.Result.Status != "" {
				renderRepoResult(stdout, response.Result)
			}
			return err
		},
	}
	command.Flags().StringVarP(&options.Message, "message", "m", "", "commit message")
	command.Flags().BoolVar(&options.Conventional, "conventional", true, "require Conventional Commit syntax")
	command.Flags().BoolVar(&options.DryRun, "dry-run", false, "render and validate the plan without mutation")
	command.Flags().BoolVar(&options.PlanJSON, "plan-json", false, "print versioned plan JSON and exit before confirmation")
	return command
}

func versionCommand(options *rootOptions, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Show Devtize build information", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			info := app.VersionInfo(version, commit, builtAt)
			if options.jsonOutput {
				return writeJSON(stdout, info)
			}
			_, err := fmt.Fprintf(stdout, "%s %s (%s, commit %s, built %s)\n", info.Product, info.Version, info.Command, info.Commit, info.BuiltAt)
			return err
		},
	}
}

func repoCommand(deps dependencies, options *rootOptions, stdout io.Writer) *cobra.Command {
	var repoOptions app.RepoOptions
	var redactOptions app.RedactInitialOptions
	var descriptionOptions app.RepoDescriptionOptions
	repo := &cobra.Command{Use: "repo", Short: "Plan and run the Phase B repository self-hosting workflow", Args: cobra.NoArgs}
	addRepoFlags := func(command *cobra.Command) {
		command.Flags().StringVar(&repoOptions.Name, "name", "", "GitHub repository name")
		command.Flags().StringVar(&repoOptions.Owner, "owner", "", "GitHub owner or organization")
		command.Flags().StringVar(&repoOptions.Visibility, "visibility", "", "GitHub repository visibility: private or public")
		command.Flags().StringVar(&repoOptions.Branch, "branch", "", "initial branch name")
		command.Flags().StringVar(&repoOptions.Message, "message", "", "initial commit message")
		command.Flags().BoolVar(&repoOptions.GenerateMessage, "generate-message", false, "request optional AI commit message generation")
		command.Flags().StringVar(&repoOptions.Remote, "remote", "origin", "Git remote name")
		command.Flags().StringVar(&repoOptions.Description, "description", "", "GitHub repository description")
		command.Flags().StringVar(&repoOptions.Homepage, "homepage", "", "GitHub repository homepage")
		command.Flags().BoolVar(&repoOptions.DryRun, "dry-run", false, "render and validate the plan without mutations")
		command.Flags().BoolVar(&repoOptions.PlanJSON, "plan-json", false, "print versioned plan JSON and exit before confirmation")
		command.Flags().BoolVar(&repoOptions.Yes, "yes", false, "skip policy-allowed local-write confirmation")
		command.Flags().BoolVar(&repoOptions.RepairInitial, "repair-unpushed-initial", false, "repair one verified unpublished initial commit")
	}
	create := &cobra.Command{
		Use: "create [paths...]", Short: "Create or verify the local Git repository and GitHub remote",
		RunE: func(command *cobra.Command, paths []string) error {
			repoOptions.Paths = paths
			loaded, err := loadConfig(deps, options)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			service := repoService(deps, loaded.Config, stdout)
			response, err := service.Plan(command.Context(), repoOptions)
			if err != nil {
				return err
			}
			if !options.jsonOutput && !repoOptions.PlanJSON {
				renderRepoPlan(stdout, response)
			}
			response, err = service.ExecutePlanned(command.Context(), repoOptions, command.InOrStdin(), response)
			if options.jsonOutput || repoOptions.PlanJSON {
				if repoOptions.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else {
				if response.Result.Status != "" {
					renderRepoResult(stdout, response.Result)
				}
			}
			return err
		},
	}
	addRepoFlags(create)
	plan := &cobra.Command{
		Use: "plan [paths...]", Short: "Render the immutable repository workflow plan without mutation",
		RunE: func(command *cobra.Command, paths []string) error {
			repoOptions.Paths = paths
			repoOptions.DryRun = true
			loaded, err := loadConfig(deps, options)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			service := repoService(deps, loaded.Config, stdout)
			response, err := service.Plan(command.Context(), repoOptions)
			if err != nil {
				return err
			}
			if options.jsonOutput || repoOptions.PlanJSON {
				if repoOptions.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				return writeJSON(stdout, response)
			}
			renderRepoPlan(stdout, response)
			return nil
		},
	}
	addRepoFlags(plan)
	status := &cobra.Command{
		Use: "status", Short: "Inspect repository self-hosting status without mutation", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			loaded, err := loadConfig(deps, options)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			service := repoService(deps, loaded.Config, stdout)
			response, err := service.Status(command.Context(), repoOptions.Remote)
			if err != nil {
				return err
			}
			if options.jsonOutput {
				return writeJSON(stdout, response)
			}
			renderRepoStatus(stdout, response)
			return nil
		},
	}
	status.Flags().StringVar(&repoOptions.Remote, "remote", "origin", "Git remote name")
	redact := &cobra.Command{
		Use: "redact-initial <path>", Short: "Remove one ignored local file from the synchronized pushed initial commit", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			redactOptions.Path = args[0]
			loaded, err := loadConfig(deps, options)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			service := repoService(deps, loaded.Config, stdout)
			response, err := service.PlanInitialRedaction(command.Context(), redactOptions)
			if err != nil {
				return err
			}
			if !options.jsonOutput && !redactOptions.PlanJSON {
				renderRepoPlan(stdout, response)
			}
			response, err = service.ExecuteInitialRedactionPlanned(command.Context(), redactOptions, command.InOrStdin(), response)
			if options.jsonOutput || redactOptions.PlanJSON {
				if redactOptions.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else if response.Result.Status != "" {
				renderRepoResult(stdout, response.Result)
			}
			return err
		},
	}
	redact.Flags().StringVar(&redactOptions.Remote, "remote", "origin", "Git remote name")
	redact.Flags().BoolVar(&redactOptions.DryRun, "dry-run", false, "render and validate the plan without mutations")
	redact.Flags().BoolVar(&redactOptions.PlanJSON, "plan-json", false, "print versioned plan JSON and exit before confirmation")
	setDescription := &cobra.Command{
		Use: "set-description", Short: "Set or verify the GitHub repository description", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			loaded, err := loadConfig(deps, options)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			service := repoService(deps, loaded.Config, stdout)
			response, err := service.PlanDescription(command.Context(), descriptionOptions)
			if err != nil {
				return err
			}
			if !options.jsonOutput && !descriptionOptions.PlanJSON {
				renderRepoDescriptionPlan(stdout, response)
			}
			response, err = service.ExecuteDescriptionPlanned(command.Context(), descriptionOptions, command.InOrStdin(), response)
			if options.jsonOutput || descriptionOptions.PlanJSON {
				if descriptionOptions.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else if response.Result.Status != "" {
				renderRepoResult(stdout, response.Result)
			}
			return err
		},
	}
	setDescription.Flags().StringVar(&descriptionOptions.Owner, "owner", "", "GitHub owner or organization")
	setDescription.Flags().StringVar(&descriptionOptions.Name, "name", "", "GitHub repository name")
	setDescription.Flags().StringVar(&descriptionOptions.Description, "description", "", "new GitHub repository description")
	setDescription.Flags().BoolVar(&descriptionOptions.DryRun, "dry-run", false, "render and validate the plan without mutation")
	setDescription.Flags().BoolVar(&descriptionOptions.PlanJSON, "plan-json", false, "print versioned plan JSON and exit before confirmation")
	repo.AddCommand(create, plan, status, redact, setDescription)
	return repo
}

func repoService(deps dependencies, cfg config.Config, output io.Writer) app.RepoService {
	userConfigDir, err := deps.userConfigDir()
	if err != nil || userConfigDir == "" {
		userConfigDir = "."
	}
	historyPath := filepath.Join(userConfigDir, "devtize", "history.jsonl")
	return app.RepoService{WorkingDir: deps.workingDir, Config: cfg, Runner: deps.runner, History: history.Store{Path: historyPath}, Output: output}
}

func commitService(deps dependencies, cfg config.Config, output io.Writer) app.CommitService {
	userConfigDir, err := deps.userConfigDir()
	if err != nil || userConfigDir == "" {
		userConfigDir = "."
	}
	historyPath := filepath.Join(userConfigDir, "devtize", "history.jsonl")
	return app.CommitService{WorkingDir: deps.workingDir, Config: cfg, Runner: deps.runner, History: history.Store{Path: historyPath}, Output: output}
}

func findCommand(deps dependencies, options *rootOptions, service app.FindService, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use: "find <intent>", Short: "Find reviewed command knowledge without executing it", Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, words []string) error {
			if _, err := loadConfig(deps, options); err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			response, err := service.Find(words)
			if err != nil {
				return err
			}
			if options.jsonOutput {
				return writeJSON(stdout, response)
			}
			for _, result := range response.Results {
				fmt.Fprintf(stdout, "%s\n  %s\n  source: %s; risk: %s; match: %s (%s)\n", result.Command, result.Summary, result.Source.Kind, result.Risk, result.MatchReason, result.Confidence)
				fmt.Fprintf(stdout, "  versions: %s (%s)\n", result.VersionRange, result.VersionStatus)
				fmt.Fprintf(stdout, "  effect: %s\n", strings.Join(result.Effects, "; "))
			}
			return nil
		},
	}
}

func doctorCommand(deps dependencies, options *rootOptions, registryCount int, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use: "doctor", Short: "Inspect local Devtize readiness without making changes", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			toolStateDir, err := os.MkdirTemp("", "devtize-doctor-")
			if err != nil {
				return app.Wrap(app.CodeProcessFailed, "could not create an isolated directory for tool detection", err)
			}
			defer os.RemoveAll(toolStateDir)

			loaded, loadErr := loadConfig(deps, options)
			check := app.ConfigCheck{Status: "valid", Sources: loaded.Sources}
			if len(check.Sources) == 0 {
				check.Sources = []string{"defaults"}
			}
			if loadErr != nil {
				check.Status = "invalid"
				check.Detail = config.Redact(loadErr.Error())
			}
			service := app.DoctorService{
				WorkingDir: deps.workingDir, Project: detect.DetectProject,
				Tools: detect.ToolDetector{Runner: deps.runner}, RegistryCount: registryCount, ToolStateDir: toolStateDir,
			}
			response, err := service.Run(command.Context(), check)
			if err != nil {
				return err
			}
			if options.jsonOutput {
				if err := writeJSON(stdout, response); err != nil {
					return err
				}
			} else {
				renderDoctor(stdout, response)
			}
			if loadErr != nil {
				return app.Wrap(app.CodeConfigInvalid, "configuration is invalid", loadErr)
			}
			return nil
		},
	}
}

func loadConfig(deps dependencies, options *rootOptions) (config.Result, error) {
	var overrides config.Overrides
	if options.noColor {
		color := config.ColorNever
		overrides.Color = &color
	}
	return config.Load(config.LoadOptions{
		WorkingDir: deps.workingDir, Environment: deps.environment,
		UserConfigDir: deps.userConfigDir, Overrides: overrides,
	})
}

func renderDoctor(writer io.Writer, response app.DoctorResponse) {
	fmt.Fprintf(writer, "Devtize doctor: %s\n", response.Status)
	fmt.Fprintf(writer, "config: %s\n", response.Config.Status)
	if response.Project.Status == "detected" {
		fmt.Fprintf(writer, "project: detected (%s, confidence %s, ambiguous %t)\n", response.Project.Root, response.Project.Confidence, response.Project.Ambiguous)
	} else {
		fmt.Fprintln(writer, "project: not found")
	}
	for _, tool := range response.Tools {
		line := fmt.Sprintf("%s: %s", tool.ProviderID, tool.Status)
		if tool.Version != "" {
			line += " " + tool.Version
		}
		if tool.AuthStatus != "" {
			line += "; auth " + tool.AuthStatus
		}
		fmt.Fprintln(writer, line)
	}
	fmt.Fprintf(writer, "registry: %s (%s; %d reviewed builtin entries)\n", response.Registry.Status, response.Registry.KnowledgeStatus, response.Registry.Entries)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type errorEnvelope struct {
	SchemaVersion int        `json:"schema_version"`
	Error         *app.Error `json:"error"`
}

func renderError(writer io.Writer, jsonOutput bool, err error) {
	var operational *app.Error
	if !errors.As(err, &operational) {
		operational = app.Wrap(app.CodeProcessFailed, "command failed", err)
	}
	if jsonOutput {
		_ = writeJSON(writer, errorEnvelope{SchemaVersion: 1, Error: operational})
		return
	}
	fmt.Fprintln(writer, operational.SafeText())
}

func exitCode(err error) int {
	var operational *app.Error
	if !errors.As(err, &operational) {
		return exitProcessFailure
	}
	switch operational.Code {
	case app.CodeInvalidUsage, app.CodeConfigInvalid, app.CodeCapabilityNotFound, app.CodeProjectNotFound:
		return exitInvalid
	case app.CodePlanInvalid, app.CodePreconditionFailed:
		return exitInvalid
	case app.CodeConfirmationDeclined:
		return exitCancelled
	case app.CodePolicyDenied:
		return exitPolicyDenied
	case app.CodeToolNotFound, app.CodeToolVersionUnsupported:
		return exitMissingDependency
	case app.CodeAuthRequired:
		return exitMissingDependency
	case app.CodeProcessTimeout, app.CodeProcessFailed, app.CodePartialExecution, app.CodeHistoryWriteFailed, app.CodePostconditionFailed:
		return exitProcessFailure
	default:
		return exitProcessFailure
	}
}

func renderRepoPlan(writer io.Writer, response app.RepoResponse) {
	fmt.Fprintf(writer, "Plan %s\n", response.Plan.ID)
	fmt.Fprintf(writer, "digest: %s\n", response.Plan.Digest)
	fmt.Fprintf(writer, "project: %s\n", response.Plan.ProjectRoot)
	fmt.Fprintf(writer, "dry-run: %t\n", response.Plan.DryRun)
	fmt.Fprintf(writer, "selected files (%s):\n", response.Selection.Mode)
	renderPaths(writer, response.Selection.Paths)
	if len(response.Selection.UntrackPaths) > 0 {
		fmt.Fprintf(writer, "tracked ignored paths to remove from the commit (%d):\n", len(response.Selection.UntrackPaths))
		renderPaths(writer, response.Selection.UntrackPaths)
	}
	for _, warning := range response.Warnings {
		fmt.Fprintf(writer, "warning: %s\n", warning)
	}
	fmt.Fprintln(writer, "operations:")
	for _, op := range response.Plan.Operations {
		fmt.Fprintf(writer, "  - %s [%s] %s\n", op.CapabilityID, op.Risk, op.Summary)
		for _, effect := range op.Effects {
			fmt.Fprintf(writer, "    effect: %s %s\n", effect.Kind, effect.Target)
		}
	}
}

func renderRepoDescriptionPlan(writer io.Writer, response app.RepoDescriptionResponse) {
	fmt.Fprintf(writer, "Plan %s\n", response.Plan.ID)
	fmt.Fprintf(writer, "digest: %s\n", response.Plan.Digest)
	fmt.Fprintf(writer, "project: %s\n", response.Plan.ProjectRoot)
	fmt.Fprintf(writer, "dry-run: %t\n", response.Plan.DryRun)
	fmt.Fprintf(writer, "repository: %s\n", response.GitHubRepo.NameWithOwner)
	fmt.Fprintf(writer, "current description: %s\n", response.GitHubRepo.Description)
	if len(response.Plan.Operations) == 0 {
		fmt.Fprintln(writer, "operations: none (description already matches)")
		return
	}
	fmt.Fprintf(writer, "new description: %s\n", response.Plan.Operations[0].Inputs["description"])
	fmt.Fprintln(writer, "operations:")
	for _, op := range response.Plan.Operations {
		fmt.Fprintf(writer, "  - %s [%s] %s\n", op.CapabilityID, op.Risk, op.Summary)
		for _, effect := range op.Effects {
			fmt.Fprintf(writer, "    effect: %s %s\n", effect.Kind, effect.Target)
		}
	}
}

func renderCommitPlan(writer io.Writer, response app.CommitResponse) {
	fmt.Fprintf(writer, "Plan %s\n", response.Plan.ID)
	fmt.Fprintf(writer, "digest: %s\n", response.Plan.Digest)
	fmt.Fprintf(writer, "project: %s\n", response.Plan.ProjectRoot)
	fmt.Fprintf(writer, "branch: %s\n", response.Git.HeadBranch)
	fmt.Fprintf(writer, "HEAD: %s\n", response.Git.HeadCommit)
	fmt.Fprintf(writer, "dry-run: %t\n", response.Plan.DryRun)
	fmt.Fprintf(writer, "message: %s\n", response.Plan.Operations[1].Inputs["message"])
	fmt.Fprintf(writer, "selected files (%s):\n", response.Selection.Mode)
	renderPaths(writer, response.Selection.Paths)
	for _, warning := range response.Selection.Warnings {
		fmt.Fprintf(writer, "warning: %s\n", warning)
	}
	fmt.Fprintln(writer, "operations:")
	for _, op := range response.Plan.Operations {
		fmt.Fprintf(writer, "  - %s [%s] %s\n", op.CapabilityID, op.Risk, op.Summary)
		for _, effect := range op.Effects {
			fmt.Fprintf(writer, "    effect: %s %s\n", effect.Kind, effect.Target)
		}
	}
}

func renderPaths(writer io.Writer, paths []string) {
	if len(paths) <= 100 {
		for _, path := range paths {
			fmt.Fprintf(writer, "  - %s\n", path)
		}
		return
	}
	type group struct {
		prefix string
		paths  []string
	}
	groupsByPrefix := map[string][]string{}
	for _, path := range paths {
		parts := strings.Split(path, "/")
		prefix := path
		if len(parts) >= 2 {
			prefix = strings.Join(parts[:2], "/")
		}
		groupsByPrefix[prefix] = append(groupsByPrefix[prefix], path)
	}
	var groups []group
	for prefix, grouped := range groupsByPrefix {
		groups = append(groups, group{prefix: prefix, paths: grouped})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].prefix < groups[j].prefix })
	for _, grouped := range groups {
		if len(grouped.paths) > 10 && strings.Contains(grouped.paths[0], "/") {
			fmt.Fprintf(writer, "  - %s/** (%d paths)\n", grouped.prefix, len(grouped.paths))
			continue
		}
		for _, path := range grouped.paths {
			fmt.Fprintf(writer, "  - %s\n", path)
		}
	}
	fmt.Fprintln(writer, "  exact paths are retained in the plan digest and available with --plan-json")
}

func renderRepoResult(writer io.Writer, result operation.ExecutionResult) {
	fmt.Fprintf(writer, "result: %s\n", result.Status)
	for _, step := range result.Steps {
		fmt.Fprintf(writer, "  - %s: %s\n", step.CapabilityID, step.Status)
		if step.RecoveryHint != "" {
			fmt.Fprintf(writer, "    recovery: %s\n", step.RecoveryHint)
		}
	}
}

func renderRepoStatus(writer io.Writer, response app.RepoResponse) {
	if !response.Git.IsRepository {
		fmt.Fprintln(writer, "repository: not initialized")
		return
	}
	fmt.Fprintln(writer, "repository: initialized")
	fmt.Fprintf(writer, "branch: %s\n", response.Git.HeadBranch)
	fmt.Fprintf(writer, "commit: %s\n", response.Git.HeadCommit)
	fmt.Fprintf(writer, "working tree: %s\n", response.Git.WorkingTreeStatus)
	if response.Git.Upstream == "" {
		fmt.Fprintln(writer, "upstream: not configured")
	} else {
		fmt.Fprintf(writer, "upstream: %s\n", response.Git.Upstream)
	}
	if len(response.Git.RemoteURLs) == 0 {
		fmt.Fprintln(writer, "origin: not configured")
	} else {
		fmt.Fprintf(writer, "origin: %s\n", strings.Join(response.Git.RemoteURLs, ", "))
	}
}
