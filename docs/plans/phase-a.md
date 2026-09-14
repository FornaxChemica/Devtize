# Phase A Implementation Plan

## Scope

Phase A builds the smallest useful Devtize foundation:

- a Go module and `dvz` Cobra CLI skeleton;
- typed configuration loading and validation;
- a safe subprocess runner used only for bounded read-only detection;
- basic project and tool detection for Git and GitHub CLI;
- registry types and a reviewed built-in Git command corpus;
- deterministic, read-only `dvz find`;
- read-only `dvz doctor`;
- stable text and versioned JSON output;
- README and focused supporting docs that describe only implemented behavior.

Phase A does not implement AI, `raw`, mutation workflows, Git/GitHub write adapters, remote repository creation, sync, TUI, MCP, cloud calls, shell passthrough, or arbitrary command execution.

## Assumptions

- The module path will use the actual repository path once known; Phase A must not publish a fake owner as final.
- The executable and invocation prefix are `dvz`; the product and project are Devtize.
- Phase A may define domain types needed to keep boundaries clean, but it must not create empty future-phase packages.
- Detection is read-only and may run official CLI version/status commands through `internal/process`; `find` must not call the process runner at all.
- GitHub authentication detection may be conservative and report `auth_unknown` when safe detection is unavailable or ambiguous.

## Ordered Tasks

### 1. [x] Inspect Current Repository State

Dependencies: none.

Affected files:

- existing repository files only for inspection.

Work:

- List current files and identify whether `go.mod`, `cmd/`, `internal/`, `registry/`, `testdata/`, `README.md`, or docs already exist.
- Confirm there are no nested `AGENTS.md` files that override root guidance for planned paths.
- Record the current phase as Phase A and note explicit non-goals before coding.

Tests:

- None.

Documentation:

- None.

Risks:

- Accidentally overwriting user work if generated scaffolding assumes an empty repository.

Acceptance criteria:

- The implementation checkpoint names existing files, nested instructions found or absent, and the selected Phase A scope.

### 2. [x] Create Minimal Go Module And CLI Entrypoint

Dependencies: task 1.

Affected packages/files:

- `go.mod`
- `go.sum`, if Cobra introduces dependencies
- `cmd/dvz/main.go`
- `internal/app`, only if needed for command construction
- CLI tests under the package convention established during implementation

Work:

- Initialize the Go module with the real module path when available, or a clearly temporary local module path that is not documented as final.
- Add Cobra and create a thin `cmd/dvz` composition root.
- Implement root help, `version`, global `--json`, and `--no-color` flags where useful for Phase A commands.
- Define build metadata defaults that are deterministic in tests.
- Establish exit code constants and boundary rendering without spreading Cobra into domain packages.

Tests:

- CLI help smoke tests for `dvz --help` and `dvz version`.
- Golden tests for stable text output and JSON shape where command output is public.
- Unit test exit-code mapping for success and invalid use.

Documentation:

- README installation/build section should show `go build ./cmd/dvz` and `dvz version`.

Risks:

- Letting Cobra-specific concerns leak into core packages.
- Documenting planned commands as available.

Acceptance criteria:

- `go build ./cmd/dvz` produces a working binary.
- `dvz --help` and `dvz version` work without config, AI, network, or project files.
- Domain packages do not import Cobra.

### 3. [x] Define Core Phase A Domain Types

Dependencies: task 2.

Affected packages/files:

- `internal/registry`
- `internal/detect`
- `internal/search`
- `internal/safety`, only for risk values needed by search metadata
- `internal/app`, for use-case request/response structs if needed

Work:

- Define explicit structs for provider identity, installation status, command knowledge, command source/provenance, search requests/results, risk values, and detection evidence.
- Keep types small and tied to Phase A behavior.
- Include support-level values needed to distinguish `planned`, `detected`, and `discoverable`; do not imply executable or workflow-ready support unless behavior exists.
- Reserve capability IDs only as stable metadata where useful, not as executable adapters.

Tests:

- Unit tests for risk string values and command knowledge validation.
- Tests that invalid or incomplete command knowledge is rejected before indexing.

Documentation:

- README support matrix legend must match implemented status values.

Risks:

- Over-modeling future workflow, plan, history, AI, or MCP concepts before they are used.
- Treating command knowledge as executable authority.

Acceptance criteria:

- Phase A has enough typed data to power detection, doctor, and find without `map[string]any` at trust boundaries.
- No trusted mutation capability is exposed.

### 4. [x] Implement Configuration Loading And Validation

Dependencies: task 3.

Affected packages/files:

- `internal/config`
- `dvz.example.yaml`
- config fixtures under `testdata/`

Work:

- Use YAML as the canonical config format.
- Load defaults, user config, project config, environment references, and CLI overrides according to documented precedence.
- Use platform-correct user config/state/cache locations.
- Support Phase A fields only: UI color/interactive defaults, AI provider disabled, history enabled flag if needed for shape, and sync notice/max help depth as inert config fields only if documented as future-facing.
- Validate schema version, unknown or unsupported values, and secret-like plaintext in committed config examples.
- Provide redaction helpers for likely secrets in config-derived diagnostics.

Tests:

- Unit tests for default config, missing config, invalid YAML, unsupported version, precedence order, project config discovery, platform path behavior, and redaction.
- Golden tests for safe config error messages.

Documentation:

- Add `dvz.example.yaml`.
- README configuration section should describe only Phase A fields that are read.

Risks:

- Accepting plaintext secrets or printing secret-like values in diagnostics.
- Implementing future workflow config that is not consumed.

Acceptance criteria:

- Config works with AI disabled and no network.
- Invalid config returns a stable `CONFIG_INVALID`-style error.
- Tests prove precedence and redaction.

### 5. [x] Implement Safe Process Runner

Dependencies: task 3.

Affected packages/files:

- `internal/process`
- process tests

Work:

- Implement a runner that accepts executable, argument slice, working directory, timeout, stdin policy, environment allowlist/overlay, capture limits, and redaction metadata.
- Use `exec.CommandContext` or equivalent; never use a shell command string.
- Resolve executables with `exec.LookPath` where appropriate.
- Capture bounded stdout/stderr and classify timeout, missing executable, and non-zero exit.
- Make runner injectable for detection and doctor tests.

Tests:

- Unit tests proving no shell invocation.
- Tests preserving arguments with spaces, quotes, wildcards, `$`, `>`, `|`, `;`, and leading dashes as literal argv entries.
- Timeout, missing executable, non-zero exit, capture truncation, working directory, and environment policy tests.

Documentation:

- README or `docs/safety.md` should summarize the no-shell subprocess boundary.

Risks:

- Accidentally using shell interpolation for convenience.
- Leaking secret-bearing args or environment values in errors.

Acceptance criteria:

- All external process use in Phase A goes through `internal/process`.
- Tests assert exact executable and args.

### 6. [x] Implement Read-Only Tool And Project Detection

Dependencies: tasks 4 and 5.

Affected packages/files:

- `internal/detect`
- `internal/app`
- detection fixtures under `testdata/projects`

Work:

- Detect project root from safe filesystem evidence without executing project files.
- Detect elementary runtime/project evidence for Go and JavaScript enough to report confidence and ambiguity.
- Detect Git and `gh` installation with executable path and parsed or unparseable version.
- For `gh`, perform only safe auth-readiness detection; return installed/authenticated/auth-unknown/missing states without exposing tokens.
- Represent stale command knowledge as not applicable or not yet implemented in Phase A unless a local built-in version marker requires it.

Tests:

- Unit tests for project root discovery, no-project behavior, conflicting evidence, Go module evidence, JS package manager evidence priority, missing tools, malformed versions, and auth-unknown handling.
- Fake runner tests for exact `git --version` and `gh --version` or safe auth commands if used.

Documentation:

- README doctor section should explain detection states and their limits.

Risks:

- Running project scripts during detection.
- Overstating GitHub auth state.
- Treating detection as workflow support.

Acceptance criteria:

- Detection returns evidence, confidence, and ambiguity.
- Missing Git or `gh` does not crash `dvz doctor`.
- No project files or lifecycle scripts are executed.

### 7. [x] Build Reviewed Git Command Corpus

Dependencies: task 3.

Affected packages/files:

- `registry/builtin`
- `internal/registry`
- corpus fixtures or golden files under `testdata/`

Work:

- Seed a small reviewed Git command corpus for Phase A discovery, including at minimum initialization, status, diff, add, commit, branch, log, remote, push, pull, restore, and reset-soft discovery entries.
- Include command path, summary, aliases, intent phrases, examples, source, supported version notes if known, risk/effect notes, and a clear “discovery only” provenance.
- Label risky commands by effect even though `find` never executes them.
- Validate corpus at startup or test time.

Tests:

- Unit tests for corpus validation.
- Golden coverage that required commands are present with stable IDs and risk/effect metadata.

Documentation:

- README discovery section should say built-ins are reviewed seed knowledge and are not executable recipes.

Risks:

- Including destructive examples without clear labels.
- Making the corpus too broad or version-specific for Phase A.

Acceptance criteria:

- Corpus supports useful offline Git discovery.
- No corpus entry can be executed directly.

### 8. [x] Implement Deterministic Search For `dvz find`

Dependencies: tasks 3 and 7.

Affected packages/files:

- `internal/search`
- `internal/app`
- `cmd/dvz` find command wiring
- search fixtures/goldens under `testdata/`

Work:

- Normalize case and tokenize command paths, descriptions, aliases, examples, and intent phrases.
- Rank exact command path matches first, then aliases, prefix/token matches, then conservative fuzzy matches.
- Preserve deterministic stable ordering.
- Join ordinary trailing CLI words into one natural-language intent.
- Support `--json` with schema version, query, results, source, risk/effect note, confidence or match reason, and command path.
- Ensure `find` never invokes the process runner, adapters, AI, shell, or project lifecycle scripts.

Tests:

- Unit tests for ranking, tokenization, fuzzy limits, stable ties, low-confidence multiple candidates, empty query, and metacharacter-preserving input.
- Spy runner test proving zero execution calls for `find`.
- CLI golden tests for `dvz find initialize git repository`, `dvz find show working tree changes`, and JSON output.

Documentation:

- README command philosophy section distinguishes `find` from `raw` and workflows.
- Explain that quotes are needed only for shell metacharacters.

Risks:

- Search confidence appearing too authoritative for ambiguous input.
- Accidentally wiring detection or execution into find.

Acceptance criteria:

- `dvz find initialize git repository` returns `git init`.
- `find` works offline and without AI.
- Text and JSON outputs are stable and tested.

### 9. [x] Implement Read-Only `dvz doctor`

Dependencies: tasks 4, 5, and 6.

Affected packages/files:

- `internal/app`
- `cmd/dvz` doctor command wiring
- `internal/ui` only if a small renderer package is justified by repeated output needs

Work:

- Report config validity, project detection, Git installation/version status, `gh` installation/version/auth-readiness status, and built-in registry availability.
- Keep output concise, scannable, and non-interactive.
- Support JSON output with schema version and stable status codes.
- Distinguish installed, missing, unsupported/unparseable version, stale or not-yet-synced knowledge, and auth-unknown where applicable.
- Avoid printing credentials, raw auth responses, or full environment data.

Tests:

- Unit tests with fake detectors/runners for installed, missing, unparseable, auth-unknown, invalid config, and no project.
- Golden CLI tests for text and JSON doctor output with normalized paths.

Documentation:

- README doctor examples should match implemented output shape.

Risks:

- Leaking environment or auth details.
- Failing the command when optional tools are missing instead of reporting actionable status.

Acceptance criteria:

- `dvz doctor` is read-only and works without network.
- Missing Git or `gh` produces a clear diagnostic and appropriate overall status.
- Output is deterministic enough for golden tests.

### 10. [x] Define Error And Output Conventions

Dependencies: tasks 2, 4, 5, 8, and 9.

Affected packages/files:

- `internal/app` or focused error package if needed
- `cmd/dvz`
- output golden tests

Work:

- Use stable operational codes for Phase A: `CONFIG_INVALID`, `TOOL_NOT_FOUND`, `TOOL_VERSION_UNSUPPORTED`, `PROJECT_NOT_FOUND`, `CAPABILITY_NOT_FOUND`, `PROCESS_TIMEOUT`, and `PROCESS_FAILED`.
- Keep human diagnostics concise and actionable.
- Keep `--json` free of ANSI styling and versioned.
- Respect `--no-color` and `NO_COLOR`; avoid spinners.
- Ensure non-interactive mode never waits for input.

Tests:

- Unit tests for error code mapping.
- CLI tests for JSON error output and no-color behavior.

Documentation:

- README troubleshooting section lists common Phase A errors and safe next steps.

Risks:

- Collapsing invalid use, missing dependency, and process failure into indistinguishable errors.

Acceptance criteria:

- Phase A commands return distinct, documented exit behavior.
- JSON and text diagnostics are stable.

### 11. [x] Write README And Supporting Documentation

Dependencies: tasks 2 through 10.

Affected files:

- `README.md`
- `docs/architecture.md`
- `docs/safety.md`
- `docs/registry.md`
- `docs/configuration.md`, if the README would otherwise become too dense
- `docs/plans/phase-a.md` updates if implementation discoveries change the plan

Work:

- Document product purpose, maturity, local-first behavior, command philosophy, and Phase A limits.
- Include the architecture diagram and trust boundary.
- Separate implemented features from roadmap.
- Document `find` versus `raw` versus workflows, making clear that only `find`, `doctor`, `version`, and root help exist in Phase A.
- Include risk classes and explain that Phase A performs no intended mutations.
- Include ecosystem support matrix with accurate support levels.
- Explain config, build, test, installation, shell completion status if any, troubleshooting, contributing, security reporting, and roadmap links.
- Mention `dvz sync`, AI, workflows, TUI, and MCP only as future work unless implemented.

Tests:

- Link or text checks if the project adopts them.
- Manual review that docs do not claim Phase B behavior as shipped.

Risks:

- Inflating roadmap items into current features.
- Letting docs diverge from implemented command names or output.

Acceptance criteria:

- README satisfies the required sections from `AGENTS.md` and the master prompt for the actual Phase A state.
- Docs include no credentials, personal account identifiers, or unimplemented-behavior claims.

### 12. [x] Add CI And Local Validation Commands

Dependencies: tasks 2 through 11.

Affected files:

- `.github/workflows/ci.yml`
- optional linter config only if a linter is actually adopted

Work:

- Add CI for formatting, tests, vet, lint if configured, and build.
- Keep workflow permissions minimal.
- Do not require network credentials or real GitHub auth.
- Avoid claiming OS matrix coverage until configured and passing.

Tests:

- Local equivalent validation commands.

Documentation:

- README development section lists exact commands maintainers should run.

Risks:

- Adding a linter without pinning or documenting how to run it.
- CI depending on user auth or remote state.

Acceptance criteria:

- CI validates Phase A without mutation, AI, or external provider credentials.

### 13. [x] Final Phase A Verification And Checkpoint

Dependencies: all prior tasks.

Affected files:

- No new implementation files unless verification exposes required fixes.

Work:

- Run formatting check.
- Run `go test ./...`.
- Run `go vet ./...`.
- Run configured linter, if configured.
- Run `go build ./cmd/dvz`.
- Run CLI smoke checks for `dvz --help`, `dvz version`, `dvz doctor`, and `dvz find initialize git repository`.
- Review diff for shell interpolation, permission bypass, secret leakage, phase expansion, stale docs, and accidental mutation behavior.

Tests:

- Full Phase A suite.

Documentation:

- Update docs only for discovered discrepancies between planned and actual behavior.

Risks:

- Reporting success while skipping a relevant check.
- Sliding Phase B work into the milestone while fixing verification failures.

Acceptance criteria:

- All applicable Phase A checks pass, or skipped/unavailable checks are explicitly reported with reasons.
- The checkpoint lists changed files, tests run, skipped checks, assumptions, residual risks, and remaining Phase B work.

## Cross-Cutting Test Matrix

| Area | Required coverage |
|---|---|
| CLI | `dvz --help`, `dvz version`, `dvz doctor`, `dvz find`, invalid use, `--json`, `--no-color`, non-interactive behavior |
| Config | defaults, user/project precedence, CLI overrides, invalid schema/version, missing config, platform paths, redaction |
| Process | no shell, literal args with metacharacters, timeout, missing executable, non-zero exit, env policy, capture bounds |
| Detection | project evidence, conflicting evidence, Git installed/missing/unparseable, `gh` installed/missing/unparseable/auth-unknown |
| Registry | corpus validation, stable metadata, risk/effect labels, no executable promotion |
| Search | ranking order, aliases, fuzzy limits, stable ties, low-confidence results, zero runner calls |
| Output | golden text, versioned JSON, stable error codes, no ANSI in JSON |

## Phase Boundary Guardrails

- Do not implement `dvz raw`; mention it only to explain that it is not available in Phase A.
- Do not implement `dvz sync`; built-in registry data may exist, but help introspection and cache refresh are later work.
- Do not implement Git mutations such as init, add, commit, remote add, push, or GitHub repository creation.
- Do not create `internal/workflow`, `internal/execute`, `internal/history`, `internal/ai`, or `internal/mcp` unless an actual Phase A code path needs a narrow type; prefer leaving those packages absent.
- Do not add Bubble Tea, Lip Gloss, MCP SDKs, AI SDKs, cloud SDKs, or plugin systems.
- Do not execute arbitrary discovered commands, model output, or shell strings.
- Do not require network access or hosted services for any Phase A command or test.

## Measurable Phase A Acceptance Criteria

- [x] `go build ./cmd/dvz` succeeds.
- [x] `go test ./...` and `go vet ./...` pass.
- [x] Formatting checks pass for every changed Go file.
- [x] The configured linter passes, or no linter is configured and that is explicitly reported.
- [x] `dvz --help`, `dvz version`, `dvz doctor`, and `dvz find initialize git repository` work from the built binary.
- [x] `dvz find initialize git repository` returns `git init` and never invokes the process runner.
- [x] `dvz find --json ...` emits stable versioned JSON with source, risk/effect note, confidence or match reason, and command path.
- [x] `dvz doctor` distinguishes config validity, project detection, Git status, `gh` status, version parse failures, and auth-unknown without exposing credentials.
- [x] Process runner tests prove executable and argv are separate and shell metacharacters remain literal arguments.
- [x] Config tests prove precedence, invalid config handling, path behavior, and redaction.
- [x] README and docs describe actual Phase A behavior and clearly label later phases as roadmap.
- [x] No Phase A command requires AI, network access, remote credentials, mutation, TUI, or MCP.
