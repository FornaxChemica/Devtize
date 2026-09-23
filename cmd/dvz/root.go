package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/FornaxChemica/devtize/internal/app"
	"github.com/FornaxChemica/devtize/internal/config"
	"github.com/FornaxChemica/devtize/internal/detect"
	"github.com/FornaxChemica/devtize/internal/history"
	"github.com/FornaxChemica/devtize/internal/operation"
	devprocess "github.com/FornaxChemica/devtize/internal/process"
	"github.com/FornaxChemica/devtize/internal/search"
	"github.com/FornaxChemica/devtize/internal/ui"
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
	color := config.ColorAuto
	if options.noColor {
		color = config.ColorNever
	}
	renderError(stderr, options.jsonOutput, err, ui.Options{Color: color, Environment: config.Environment(os.Environ())})
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
	root.AddCommand(statusCommand(deps, options, stdout))
	root.AddCommand(historyCommand(deps, options, stdout))
	root.AddCommand(repoCommand(deps, options, stdout))
	root.AddCommand(shipCommand(deps, options, stdout))
	root.AddCommand(versionCommand(options, stdout))
	return root, options, nil
}

func statusCommand(deps dependencies, rootOptions *rootOptions, stdout io.Writer) *cobra.Command {
	var options app.StatusOptions
	command := &cobra.Command{
		Use: "status", Short: "Inspect daily Git status without mutation", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			loaded, err := loadConfig(deps, rootOptions)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			response, err := statusService(deps).Run(command.Context(), options)
			if err != nil {
				return err
			}
			if rootOptions.jsonOutput {
				return writeJSON(stdout, response)
			}
			renderStatus(stdout, response, humanUIOptions(deps, loaded.Config))
			return nil
		},
	}
	command.Flags().BoolVar(&options.Remote, "remote", false, "verify the current branch against the live remote")
	command.Flags().StringVar(&options.RemoteName, "remote-name", "origin", "Git remote name used for optional live verification")
	return command
}

func historyCommand(deps dependencies, rootOptions *rootOptions, stdout io.Writer) *cobra.Command {
	var options app.HistoryOptions
	command := &cobra.Command{
		Use: "history", Short: "Show redacted Devtize execution history", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			loaded, err := loadConfig(deps, rootOptions)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			response, err := historyService(deps, loaded.Config).Run(options)
			if err != nil {
				return err
			}
			if rootOptions.jsonOutput {
				return writeJSON(stdout, response)
			}
			renderHistory(stdout, response, humanUIOptions(deps, loaded.Config))
			return nil
		},
	}
	command.Flags().IntVar(&options.Limit, "limit", 20, "maximum records to show (1-200)")
	command.Flags().BoolVar(&options.All, "all", false, "include records from every project")
	return command
}

func shipCommand(deps dependencies, rootOptions *rootOptions, stdout io.Writer) *cobra.Command {
	workflowOptions := app.ShipWorkflowOptions{Conventional: true}
	var checkOverrides []string
	command := &cobra.Command{
		Use: "ship [paths...]", Short: "Run reviewed checks, commit, and push without force",
		RunE: func(command *cobra.Command, paths []string) error {
			loaded, err := loadConfig(deps, rootOptions)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			checkChanged := command.Flags().Changed("check")
			compose := workflowOptions.Message != "" || len(paths) > 0 || checkChanged
			if !compose {
				options := app.ShipOptions{Remote: workflowOptions.Remote, DryRun: workflowOptions.DryRun, PlanJSON: workflowOptions.PlanJSON}
				service := shipService(deps, loaded.Config, stdout)
				response, planErr := service.Plan(command.Context(), options)
				if planErr != nil {
					return planErr
				}
				if !rootOptions.jsonOutput && !options.PlanJSON {
					renderShipPlan(stdout, response, humanUIOptions(deps, loaded.Config))
				}
				response, planErr = service.ExecutePlanned(command.Context(), options, command.InOrStdin(), response)
				if rootOptions.jsonOutput || options.PlanJSON {
					if options.PlanJSON {
						return writeJSON(stdout, response.Plan)
					}
					_ = writeJSON(stdout, response)
				} else if response.Result.Status != "" {
					renderRepoResult(stdout, response.Result, humanUIOptions(deps, loaded.Config))
				}
				return planErr
			}
			if workflowOptions.Message == "" {
				return &app.Error{Code: app.CodeInvalidUsage, Message: "ship paths and --check require --message"}
			}
			workflowOptions.Paths = paths
			workflowOptions.Checks = append([]string(nil), loaded.Config.Checks.Ship...)
			if checkChanged {
				workflowOptions.Checks = append([]string(nil), checkOverrides...)
			}
			service := shipWorkflowService(deps, loaded.Config, stdout, rootOptions.jsonOutput)
			response, err := service.Plan(command.Context(), workflowOptions)
			if err != nil {
				return err
			}
			if rootOptions.jsonOutput || workflowOptions.PlanJSON {
				if workflowOptions.PlanJSON {
					return writeJSON(stdout, response)
				}
			} else {
				renderShipWorkflowPlan(stdout, response, humanUIOptions(deps, loaded.Config))
			}
			if workflowOptions.DryRun {
				if rootOptions.jsonOutput {
					return writeJSON(stdout, response)
				}
				return nil
			}
			response, err = service.ExecuteLocal(command.Context(), workflowOptions, command.InOrStdin(), response)
			if err != nil {
				if rootOptions.jsonOutput {
					_ = writeJSON(stdout, response)
				} else if response.Local.Result.Status != "" {
					renderRepoResult(stdout, response.Local.Result, humanUIOptions(deps, loaded.Config))
				}
				return err
			}
			if !rootOptions.jsonOutput {
				renderRepoResult(stdout, response.Local.Result, humanUIOptions(deps, loaded.Config))
			}
			response, err = service.PlanPush(command.Context(), workflowOptions, response)
			if err != nil {
				if rootOptions.jsonOutput {
					_ = writeJSON(stdout, response)
				}
				return err
			}
			if !rootOptions.jsonOutput && response.Push != nil {
				renderShipPlan(stdout, *response.Push, humanUIOptions(deps, loaded.Config))
			}
			response, err = service.ExecutePush(command.Context(), workflowOptions, command.InOrStdin(), response)
			if rootOptions.jsonOutput {
				_ = writeJSON(stdout, response)
			} else if response.Push != nil && response.Push.Result.Status != "" {
				renderRepoResult(stdout, response.Push.Result, humanUIOptions(deps, loaded.Config))
			}
			return err
		},
	}
	command.Flags().StringVarP(&workflowOptions.Message, "message", "m", "", "commit message for composed ship")
	command.Flags().BoolVar(&workflowOptions.Conventional, "conventional", true, "require Conventional Commit syntax")
	command.Flags().StringArrayVar(&checkOverrides, "check", nil, "reviewed check capability ID (repeatable; replaces configured checks)")
	command.Flags().StringVar(&workflowOptions.Remote, "remote", "origin", "Git remote name")
	command.Flags().BoolVar(&workflowOptions.DryRun, "dry-run", false, "render and validate the plan without mutation")
	command.Flags().BoolVar(&workflowOptions.PlanJSON, "plan-json", false, "print versioned plan JSON and exit before confirmation")
	return command
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
				renderCommitPlan(stdout, response, humanUIOptions(deps, loaded.Config))
			}
			response, err = service.ExecutePlanned(command.Context(), options, command.InOrStdin(), response)
			if rootOptions.jsonOutput || options.PlanJSON {
				if options.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else if response.Result.Status != "" {
				renderRepoResult(stdout, response.Result, humanUIOptions(deps, loaded.Config))
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
			ui.New(stdout, firstUIOptions(nil)).Version(info)
			return nil
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
				renderRepoPlan(stdout, response, humanUIOptions(deps, loaded.Config))
			}
			response, err = service.ExecutePlanned(command.Context(), repoOptions, command.InOrStdin(), response)
			if options.jsonOutput || repoOptions.PlanJSON {
				if repoOptions.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else {
				if response.Result.Status != "" {
					renderRepoResult(stdout, response.Result, humanUIOptions(deps, loaded.Config))
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
			renderRepoPlan(stdout, response, humanUIOptions(deps, loaded.Config))
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
			renderRepoStatus(stdout, response, humanUIOptions(deps, loaded.Config))
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
				renderRepoPlan(stdout, response, humanUIOptions(deps, loaded.Config))
			}
			response, err = service.ExecuteInitialRedactionPlanned(command.Context(), redactOptions, command.InOrStdin(), response)
			if options.jsonOutput || redactOptions.PlanJSON {
				if redactOptions.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else if response.Result.Status != "" {
				renderRepoResult(stdout, response.Result, humanUIOptions(deps, loaded.Config))
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
				renderRepoDescriptionPlan(stdout, response, humanUIOptions(deps, loaded.Config))
			}
			response, err = service.ExecuteDescriptionPlanned(command.Context(), descriptionOptions, command.InOrStdin(), response)
			if options.jsonOutput || descriptionOptions.PlanJSON {
				if descriptionOptions.PlanJSON {
					return writeJSON(stdout, response.Plan)
				}
				_ = writeJSON(stdout, response)
			} else if response.Result.Status != "" {
				renderRepoResult(stdout, response.Result, humanUIOptions(deps, loaded.Config))
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
	return app.RepoService{WorkingDir: deps.workingDir, Config: cfg, Runner: deps.runner, History: history.Store{Path: localHistoryPath(deps)}, Output: output}
}

func commitService(deps dependencies, cfg config.Config, output io.Writer) app.CommitService {
	return app.CommitService{WorkingDir: deps.workingDir, Config: cfg, Runner: deps.runner, History: history.Store{Path: localHistoryPath(deps)}, Output: output}
}

func shipService(deps dependencies, cfg config.Config, output io.Writer) app.ShipService {
	return app.ShipService{WorkingDir: deps.workingDir, Config: cfg, Runner: deps.runner, History: history.Store{Path: localHistoryPath(deps)}, Output: output}
}

func shipWorkflowService(deps dependencies, cfg config.Config, output io.Writer, jsonOutput bool) app.ShipWorkflowService {
	service := app.ShipWorkflowService{
		WorkingDir: deps.workingDir, Config: cfg, Runner: deps.runner,
		History: history.Store{Path: localHistoryPath(deps)}, Output: output,
	}
	if !jsonOutput {
		service.Progress = ui.New(output, humanUIOptions(deps, cfg))
	}
	return service
}

func statusService(deps dependencies) app.StatusService {
	return app.StatusService{WorkingDir: deps.workingDir, Runner: deps.runner}
}

func historyService(deps dependencies, cfg config.Config) app.HistoryService {
	return app.HistoryService{WorkingDir: deps.workingDir, Config: cfg, History: history.Store{Path: localHistoryPath(deps)}}
}

func localHistoryPath(deps dependencies) string {
	configPath, err := config.UserPath(deps.environment, deps.userConfigDir)
	if err != nil || configPath == "" {
		return filepath.Join(".", "devtize", "history.jsonl")
	}
	return filepath.Join(filepath.Dir(configPath), "history.jsonl")
}

func findCommand(deps dependencies, options *rootOptions, service app.FindService, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use: "find <intent>", Short: "Find reviewed command knowledge without executing it", Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, words []string) error {
			loaded, err := loadConfig(deps, options)
			if err != nil {
				return app.Wrap(app.CodeConfigInvalid, config.Redact(err.Error()), err)
			}
			response, err := service.Find(words)
			if err != nil {
				return err
			}
			if options.jsonOutput {
				return writeJSON(stdout, response)
			}
			ui.New(stdout, humanUIOptions(deps, loaded.Config)).Find(response)
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
				renderDoctor(stdout, response, humanUIOptions(deps, loaded.Config))
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

func renderDoctor(writer io.Writer, response app.DoctorResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).Doctor(response)
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

func renderError(writer io.Writer, jsonOutput bool, err error, options ...ui.Options) {
	var operational *app.Error
	if !errors.As(err, &operational) {
		operational = app.Wrap(app.CodeProcessFailed, "command failed", err)
	}
	if jsonOutput {
		_ = writeJSON(writer, errorEnvelope{SchemaVersion: 1, Error: operational})
		return
	}
	ui.New(writer, firstUIOptions(options)).Error(operational)
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
	case app.CodeCheckFailed, app.CodeProcessTimeout, app.CodeProcessFailed, app.CodePartialExecution, app.CodeHistoryWriteFailed, app.CodeHistoryReadFailed, app.CodePostconditionFailed:
		return exitProcessFailure
	case app.CodeHistoryInvalid:
		return exitInvalid
	default:
		return exitProcessFailure
	}
}

func renderRepoPlan(writer io.Writer, response app.RepoResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).RepoPlan(response)
}

func renderRepoDescriptionPlan(writer io.Writer, response app.RepoDescriptionResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).RepoDescriptionPlan(response)
}

func renderCommitPlan(writer io.Writer, response app.CommitResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).CommitPlan(response)
}

func renderShipPlan(writer io.Writer, response app.ShipResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).ShipPlan(response)
}

func renderShipWorkflowPlan(writer io.Writer, response app.ShipWorkflowResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).ShipWorkflowPlan(response)
}

func renderRepoResult(writer io.Writer, result operation.ExecutionResult, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).Result(result)
}

func renderRepoStatus(writer io.Writer, response app.RepoResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).RepoStatus(response)
}

func renderStatus(writer io.Writer, response app.StatusResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).Status(response)
}

func renderHistory(writer io.Writer, response app.HistoryResponse, options ...ui.Options) {
	ui.New(writer, firstUIOptions(options)).History(response)
}

func firstUIOptions(options []ui.Options) ui.Options {
	if len(options) > 0 {
		return options[0]
	}
	return ui.Options{Color: config.ColorAuto, Environment: config.Environment(os.Environ())}
}

func humanUIOptions(deps dependencies, cfg config.Config) ui.Options {
	return ui.Options{Color: cfg.UI.Color, Environment: deps.environment}
}
