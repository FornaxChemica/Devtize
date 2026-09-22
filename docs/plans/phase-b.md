# Phase B Implementation Plan

## Scope

Phase B adds the first trusted mutation path for Devtize: deterministic Git and GitHub operations composed into the self-hosting workflow. The goal is not a general workflow engine, `raw`, shell passthrough, or daily Git assistant. The goal is one complete, typed, auditable path that can initialize and publish the current folder after it has passed all tests in temporary repositories and fake GitHub fixtures.

Phase B implements:

- Git repository inspection and initialization.
- Explicitly selected file staging without force-adding ignored files.
- First-commit message acceptance, with optional generation only through the configured AI boundary if that boundary exists and is verified.
- Initial commit creation.
- GitHub repository creation through installed `gh`.
- Origin inspection, exact verification, or safe configuration.
- Initial branch push without force.
- Immutable plan rendering before mutation.
- Risk classification, scoped confirmation, dry-run, history, postcondition verification, and partial-failure recovery.

Phase B does not implement `dvz raw`, arbitrary command execution, general force push, destructive recovery, pull request creation, deployment, TUI, MCP, provider sync, or broad daily Git workflows. The sole force update is the maintainer-authorized, exact-lease initial redaction in task 12.1.

## Critical Self-Hosting Rule

The actual Devtize folder must not be initialized, staged, committed, given a remote, or pushed manually by the coding agent or the user. During Phase B planning and implementation, all mutation tests must use temporary repositories, fake process runners, fake `gh` executables, or isolated opt-in GitHub fixtures.

After every Phase B acceptance criterion passes, the real self-hosting milestone is performed only by running `dvz` commands. The maintainer, not the coding agent, runs the real command in the Devtize folder. The command output must show the plan, request confirmation, execute through trusted adapters, verify postconditions, and write sanitized evidence.

## Assumptions

- Phase A acceptance criteria are complete before Phase B coding starts.
- The canonical Phase B command is `dvz repo create`; it means “create or verify the local Git repository and matching GitHub remote for this project.”
- The default repository name is derived from the project folder or project config and defaults to `Devtize` for this repository, but the user can override it with `--name`.
- The default initial branch is config value `git.default_branch`, then `main`.
- `gh` is the only GitHub write path in Phase B. Direct GitHub API clients are out of scope.
- Commit message generation is optional. A deterministic `--message` path must always work without AI. If AI is still disabled after Phase A, Phase B can implement `--generate-message` as an explicit `AI_UNAVAILABLE` result until the AI interface exists.

## Command Surface

### Canonical Workflow Command

```text
dvz repo create [paths...]
```

Purpose:

- Create or verify the local Git repository for the selected project.
- Stage disclosed files.
- Create the initial commit when no commit exists.
- Create or verify a GitHub repository.
- Configure or verify `origin`.
- Push the initial branch and set upstream.

Positional `paths`:

- Optional explicit file or directory paths relative to the project root.
- If omitted, the workflow plans a disclosed whole-project staging set using Git status and ignore rules.
- Paths outside the project root are rejected with `PLAN_INVALID`.
- Ignored files are excluded unless a future dedicated capability supports explicit, confirmed handling. Phase B must not force-add ignored files.

Required or important flags:

```text
--name <repo-name>             GitHub repository name; default from project config or folder name
--owner <owner-or-org>         GitHub owner; default from authenticated gh account when safely detectable
--visibility <private|public>  GitHub repository visibility; default private
--branch <name>                Initial branch; default config git.default_branch or main
--message <text>               Commit message to use for the initial commit
--generate-message             Request optional configured AI message generation
--remote <name>                Remote name; default origin
--description <text>           Optional GitHub repository description
--homepage <url>               Optional GitHub repository homepage
--dry-run                      Render and validate the plan; perform zero mutations
--plan-json                    Print the versioned plan JSON and exit before confirmation
--json                         Emit versioned machine-readable command output
--yes                          Skip only policy-allowed confirmations; remote write confirmation still requires explicit policy support
--no-color                     Disable ANSI styling
```

Validation:

- `--message` and `--generate-message` are mutually exclusive.
- `--visibility` accepts only `private` or `public`.
- `--owner` and `--name` must be safe GitHub owner/repository identifiers.
- `--branch` and `--remote` must be valid Git names and must not begin with `-`.
- Non-interactive execution requires enough flags to avoid prompts. If a message or required target cannot be determined, return `PLAN_INVALID` or `CONFIRMATION_DECLINED` rather than hanging.

### Supporting Read-Only Commands

```text
dvz repo plan [paths...]
dvz repo status
```

`dvz repo plan` builds and renders the same immutable plan as `dvz repo create --dry-run` with no mutation. It exists to make planning explicit and testable.

`dvz repo status` reports local Git state, remote state when configured, `gh` readiness, and whether the self-hosting workflow appears complete. It is read-only and may run only bounded inspect commands through reviewed adapters.

## Operation Schemas

All operations use versioned, explicit structs at the operation boundary. JSON snippets below define required external shape; Go structs should use typed fields rather than `map[string]any`.

### `git.repo.inspect`

Risk: `read_only`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path",
  "remote_name": "origin"
}
```

Outputs:

```json
{
  "is_repository": true,
  "git_dir": ".git",
  "head_branch": "main",
  "head_commit": "abc123",
  "has_commits": true,
  "is_detached_head": false,
  "working_tree_status": "clean|dirty|unborn|unknown",
  "tracked_changes": [],
  "untracked_paths": [],
  "ignored_selected_paths": [],
  "remotes": [{"name": "origin", "fetch_url": "...", "push_url": "..."}],
  "upstream": {"branch": "main", "remote": "origin", "merge_ref": "refs/heads/main"}
}
```

Preconditions:

- Project root exists and is a directory.

Postconditions:

- None; read-only.

### `git.repo.init`

Risk: `local_write`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path",
  "initial_branch": "main"
}
```

Adapter command:

```text
git init --initial-branch <branch>
```

Fallback:

- If installed Git does not support `--initial-branch`, run `git init`, then set the branch through a reviewed safe path only when no commit exists. The fallback must be tested by fake version capability.

Idempotency:

- If the project is already a Git repository, skip only after `git.repo.inspect` verifies that fact.
- If the repository exists but the selected branch conflicts with a non-empty existing branch, stop with `PRECONDITION_FAILED`.

Postconditions:

- `.git` exists for the selected root.
- Repository can be inspected.
- HEAD is not detached.

### `git.index.stage`

Risk: `local_write`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path",
  "paths": ["README.md", "docs"],
  "selection_mode": "explicit|disclosed_all"
}
```

Adapter command:

```text
git add -- <path>...
```

Rules:

- Use `--` before user-controlled paths.
- Do not run `git add -A` unless `selection_mode` is `disclosed_all` and the rendered plan lists the exact eligible paths that will be staged.
- Do not use `-f` or force-add ignored files.
- Reject paths outside the project root or paths that resolve through symlinks outside the root.
- Run deterministic secret filename/content preflight before staging. It may warn and require confirmation, but must not store full secret-like content.

Idempotency:

- If all selected paths are already staged with the intended index state, skip after verification.

Postconditions:

- Selected eligible paths appear in the index.
- Ignored selected paths remain unstaged and are reported.

### `git.commit.create`

Risk: `local_write`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path",
  "message": "Initial commit",
  "allow_empty": false
}
```

Adapter command:

```text
git commit --message <message>
```

Rules:

- Do not commit when the index is empty unless a future explicit `allow_empty` workflow is designed.
- Do not invoke an editor.
- Configure test repositories with local test identity only inside temporary repositories.

Idempotency:

- If a commit already exists and the workflow is in resume mode, skip only if the existing HEAD satisfies the workflow postcondition for the selected files. Do not create duplicate initial commits.

Postconditions:

- `HEAD` resolves to a commit.
- Commit subject equals the accepted message subject.
- Working tree and index status are reported for any remaining unstaged changes.

### `github.auth.inspect`

Risk: `read_only`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path"
}
```

Adapter command:

```text
gh auth status
```

Rules:

- Capture bounded output and redact account tokens or sensitive URLs.
- If auth cannot be safely determined, return `auth_unknown` and block remote writes unless the workflow has an explicit, tested way to continue safely.

Postconditions:

- None; read-only.

### `github.repo.inspect`

Risk: `read_only`.

Inputs:

```json
{
  "schema_version": 1,
  "owner": "OWNER",
  "name": "repo"
}
```

Adapter command:

```text
gh repo view <owner>/<name> --json nameWithOwner,visibility,description,url,sshUrl,defaultBranchRef
```

Outputs:

```json
{
  "exists": true,
  "name_with_owner": "OWNER/repo",
  "visibility": "PRIVATE",
  "url": "https://github.com/OWNER/repo",
  "ssh_url": "git@github.com:OWNER/repo.git"
}
```

### `github.repo.create`

Risk: `remote_write`.

Inputs:

```json
{
  "schema_version": 1,
  "owner": "OWNER",
  "name": "repo",
  "visibility": "private",
  "description": "",
  "homepage": ""
}
```

Adapter command:

```text
gh repo create <owner>/<name> --private
```

Exact flags may change during implementation after checking current `gh` behavior, but repository creation must not configure local Git remotes as a side effect. Remote configuration belongs to `git.remote.configure`. Tests must assert the final executable and argv exactly. Do not pass secrets on the command line.

Idempotency:

- Inspect before create.
- If a matching repository already exists with the expected owner/name/visibility and acceptable URL, treat creation as satisfied.
- If a repository exists with mismatched visibility or ownership, stop with `PRECONDITION_FAILED`.
- Name collision with an incompatible repository must never be overwritten.

Postconditions:

- Repository exists at the expected owner/name.
- Visibility matches the plan.
- Repository URL is captured for origin verification.

### `git.remote.inspect`

Risk: `read_only`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path",
  "remote_name": "origin"
}
```

Adapter command:

```text
git remote get-url --all <remote>
```

### `git.remote.configure`

Risk: `local_write`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path",
  "remote_name": "origin",
  "url": "git@github.com:OWNER/repo.git"
}
```

Adapter command:

```text
git remote add <remote> <url>
```

Rules:

- If the remote does not exist, add it.
- If the remote exists and every configured URL exactly matches an accepted URL for the GitHub repository, treat as satisfied.
- If the remote exists with an unexpected URL, stop with `PRECONDITION_FAILED`.
- Do not overwrite, rename, or remove remotes in Phase B.

Postconditions:

- Remote exists and points exactly to the expected repository URL.

### `git.branch.push`

Risk: `remote_write`.

Inputs:

```json
{
  "schema_version": 1,
  "project_root": "/absolute/path",
  "remote_name": "origin",
  "branch": "main",
  "set_upstream": true
}
```

Adapter command:

```text
git push --set-upstream <remote> <branch>
```

Rules:

- Never use `--force`, `--force-with-lease`, `--mirror`, or `--all`.
- Require a local commit before push.
- Stop if branch is detached or name is invalid.

Postconditions:

- Local branch has upstream `<remote>/<branch>`.
- Remote branch exists and points to the expected commit when safely verifiable.

## Adapter Interfaces

Adapters live under `internal/adapters/git` and `internal/adapters/github`. They depend on the process runner and expose typed methods to `internal/execute` or the application service. They do not prompt, render UI, read Cobra flags, or decide policy.

Git adapter interface:

```go
type GitAdapter interface {
    InspectRepo(ctx context.Context, input InspectRepoInput) (InspectRepoResult, error)
    InitRepo(ctx context.Context, input InitRepoInput) (InitRepoResult, error)
    Stage(ctx context.Context, input StageInput) (StageResult, error)
    CreateCommit(ctx context.Context, input CreateCommitInput) (CreateCommitResult, error)
    InspectRemote(ctx context.Context, input InspectRemoteInput) (InspectRemoteResult, error)
    ConfigureRemote(ctx context.Context, input ConfigureRemoteInput) (ConfigureRemoteResult, error)
    PushBranch(ctx context.Context, input PushBranchInput) (PushBranchResult, error)
}
```

GitHub adapter interface:

```go
type GitHubAdapter interface {
    InspectAuth(ctx context.Context, input InspectAuthInput) (InspectAuthResult, error)
    InspectRepo(ctx context.Context, input InspectGitHubRepoInput) (InspectGitHubRepoResult, error)
    CreateRepo(ctx context.Context, input CreateGitHubRepoInput) (CreateGitHubRepoResult, error)
}
```

Runner requirements:

- Every adapter call uses `internal/process`.
- Every command sets working directory explicitly.
- Every command uses executable plus argv; never shell command strings.
- Output capture is bounded and redacted.
- Tests assert exact executable, arguments, working directory, timeout, stdin policy, environment policy, and redaction fields.

## Planning And Execution Model

Phase B introduces the minimal operation packages required by the workflow:

- `internal/operation` for plans, operations, risks, effects, lifecycle statuses, digests, and typed operational errors.
- `internal/safety` for policy decisions and confirmation requirements.
- `internal/execute` for step execution, precondition checks, postcondition verification, cancellation, partial completion, and result collection.
- `internal/history` for append-only redacted JSON Lines records.
- `internal/workflow` only if needed to keep the self-hosting plan definition out of CLI wiring.

Do not create generic future abstractions beyond what `dvz repo create` uses.

Plan lifecycle:

```text
proposed -> validated -> policy_evaluated -> authorized -> running
         -> succeeded | failed | cancelled | partially_completed
```

Plan digest:

- Compute over the normalized immutable plan, including operation order, inputs, risk, effects, target owner/repo, branch, remote URL, selected paths, and tool versions where version-sensitive behavior matters.
- Confirmation applies to this digest.
- Any change to target, effect, path set, message, branch, remote, visibility, adapter resolution, or version-sensitive fallback invalidates prior confirmation.

Canonical operation order:

1. Inspect project, Git repository, selected paths, and tool readiness.
2. Inspect GitHub auth and target repository availability.
3. Initialize Git only when needed.
4. Stage disclosed eligible files.
5. Create the initial commit when needed.
6. Create or verify the GitHub repository.
7. Configure or verify `origin`.
8. Push the initial branch and set upstream.
9. Verify local HEAD, upstream, remote URL, and GitHub repository URL.
10. Record sanitized history and self-hosting evidence when requested.

## Confirmation Boundaries

The rendered plan must show:

- Project root.
- Git executable and version.
- `gh` executable and version/auth status.
- GitHub account or owner when safely known.
- Repository owner, name, visibility, and URL.
- Remote name and URL.
- Branch name.
- Commit message.
- Exact selected path list or disclosed all-files selection.
- Ignored/excluded path count and secret preflight warnings.
- Risk per operation and total workflow risk.
- Dry-run status.
- Plan digest.

Required confirmations:

- `local_write`: before `git.repo.init`, `git.index.stage`, `git.commit.create`, and `git.remote.configure`.
- `remote_write`: before `github.repo.create` and `git.branch.push`, with provider, owner, repo, visibility, branch, and remote displayed.
- Secret preflight warning: if likely secret filenames or content patterns are detected among selected paths, require an extra explicit confirmation. This confirmation cannot be bypassed by `--yes` in Phase B.

`--yes`:

- May skip local-write confirmation only if policy config permits it.
- Must not skip remote-write confirmation by default.
- Must not skip secret warnings, destructive/privileged denials, stale-plan checks, invalid schema checks, missing targets, auth failures, or changed plan digests.

Declines:

- Declining local-write confirmation stops before all mutation steps.
- Declining remote-write confirmation stops before remote operations and before subsequent remote-dependent local changes.
- Return `CONFIRMATION_DECLINED` and a cancelled outcome, not a process failure.

## Dry-Run Behavior

`--dry-run` must:

- Load config, inspect read-only local state, inspect safe tool readiness, validate inputs, classify risks, build the immutable plan, and render it.
- Perform zero intended mutations.
- Never call adapter mutation methods.
- Never create a GitHub repository, add a remote, stage, commit, or push.
- Return success when the plan is valid and all required preconditions can be evaluated without mutation.

Tests must prove zero mutation calls with fake adapters and prove temporary repositories remain unchanged after dry-run.

## Idempotency And Resume

Phase B idempotency is postcondition-based, not history-based.

Safe skips:

- Skip `git.repo.init` when inspect verifies the root is already a Git repository.
- Skip `git.index.stage` when selected eligible paths are already staged as intended.
- Skip `git.commit.create` when an existing initial commit satisfies the workflow postcondition.
- Skip `github.repo.create` when inspect verifies the exact target repository already exists with compatible properties.
- Skip `git.remote.configure` when the remote exists and exactly matches the expected URL.
- Skip `git.branch.push` when upstream exists and points to the expected commit.

Unsafe or blocking states:

- Detached HEAD.
- Existing origin with unexpected URL.
- Existing GitHub repository with incompatible owner/name/visibility.
- Non-empty remote branch that does not point to the expected commit.
- Empty staging selection.
- Missing Git or `gh`.
- `gh` unauthenticated or auth unknown for remote writes.
- Selected paths outside root, ignored paths only, or likely secret paths without explicit confirmation.

## Partial-Failure Recovery

On failure after one or more successful mutation steps:

- Stop by default.
- Record completed, skipped, failed, and pending steps.
- Show the failed operation code, provider, safe message, retryability, and next safe action.
- Never run unplanned cleanup.
- Never remove `.git`, delete commits, overwrite remotes, delete GitHub repositories, or force-push as automatic recovery.

Recovery guidance examples:

- Commit succeeded but GitHub repo creation failed: fix `gh` auth or repository name, then rerun the same `dvz repo create ...` command. The rerun must verify the existing commit before continuing.
- GitHub repo exists but origin is absent: rerun after confirming the target repo; Devtize may add origin if postconditions match.
- Origin points elsewhere: stop and ask the user to choose a different remote name or resolve manually outside the Phase B workflow.
- Push failed due to network/auth: rerun after fixing auth or connectivity; Devtize verifies local and remote state first.

## Error Behavior

Use stable operational codes:

```text
CONFIG_INVALID
TOOL_NOT_FOUND
TOOL_VERSION_UNSUPPORTED
AUTH_REQUIRED
PROJECT_NOT_FOUND
CAPABILITY_NOT_FOUND
PLAN_INVALID
POLICY_DENIED
CONFIRMATION_DECLINED
PRECONDITION_FAILED
PROCESS_TIMEOUT
PROCESS_FAILED
PARTIAL_EXECUTION
AI_UNAVAILABLE
AI_OUTPUT_INVALID
HISTORY_WRITE_FAILED
POSTCONDITION_FAILED
```

Rules:

- Missing Git returns `TOOL_NOT_FOUND` before mutation.
- Missing `gh` returns `TOOL_NOT_FOUND` before remote planning can execute.
- Unauthenticated `gh` returns `AUTH_REQUIRED` before remote writes.
- Detached HEAD returns `PRECONDITION_FAILED`.
- Existing unexpected origin returns `PRECONDITION_FAILED`.
- Invalid names, paths, visibility, branch, remote, or message return `PLAN_INVALID`.
- Policy refusal returns `POLICY_DENIED`.
- User cancellation returns `CONFIRMATION_DECLINED`.
- Failed subprocesses preserve the wrapped cause and safe diagnostic tail as `PROCESS_FAILED` or `PROCESS_TIMEOUT`.
- Failed postcondition checks return `POSTCONDITION_FAILED`; if earlier mutation succeeded, workflow status is `partially_completed`.

`--json` output must include schema version, plan ID, plan digest, status, operation results, error code, safe message, and recovery hints without ANSI styling.

## History

Phase B writes append-only JSON Lines records under the configured local state directory when history is enabled. Records include:

- schema version;
- execution ID, plan ID, and plan digest;
- invocation with dry-run/yes/json flags;
- normalized project identity, not full repository contents;
- Git and `gh` tool versions and normalized paths;
- redacted operation summaries, risks, decisions, and confirmation outcomes;
- step statuses, started/finished times, exit codes, safe diagnostic tails, observed changes, and postcondition results;
- recovery guidance;
- final status.

History must not store:

- tokens, cookies, credentials, auth headers, raw environment dumps, full command output, full diffs, or secret-like file content;
- the full AI prompt body if message generation exists;
- unrelated files or user shell history.

History write failure after successful mutation must be reported loudly as `HISTORY_WRITE_FAILED` while preserving the mutation result.

## Documentation Changes

Update documentation in the same implementation change:

- `README.md`: implemented `dvz repo create`, `repo plan`, and `repo status`; Phase B safety model; dry-run examples; exact self-hosting command; support matrix changes from discoverable to workflow-ready only for implemented capabilities.
- `docs/architecture.md`: add operation, safety, execute, history, Git adapter, and GitHub adapter paths actually implemented.
- `docs/safety.md`: document Phase B mutation policy, confirmation boundaries, secret preflight, dry-run guarantees, idempotency, and recovery limits.
- `docs/registry.md`: separate command knowledge from trusted Git/GitHub capabilities and list Phase B capability IDs.
- `docs/configuration.md`: document any new config fields consumed by Phase B, especially branch, history, confirmation policy, GitHub visibility, and AI disabled behavior.
- `docs/self-hosting.md`: create only after the real maintainer-run self-hosting milestone completes; include sanitized evidence, tool versions, plan summary, repository URL, verification results, and manual steps if any.

Docs must not claim self-hosting is complete until it has actually been performed through `dvz`.

## Ordered Tasks

### 1. [x] Reconfirm Phase Boundary And Current Behavior

Dependencies: Phase A complete.

Work:

- Read `AGENTS.md`, `MASTER_IDE_PROMPT.md`, and `docs/plans/phase-a.md`.
- Run Phase A verification commands before coding if the codebase exists.
- Confirm no Git/GitHub mutation has been manually performed in the Devtize folder.
- Record Phase B non-goals before implementation.

Tests:

- Existing Phase A suite.

Acceptance criteria:

- The implementation checkpoint states Phase B scope and confirms no actual Devtize repository mutation was performed.

### 2. [x] Add Operation, Safety, Execution, And History Foundations

Dependencies: task 1.

Affected packages/files:

- `internal/operation`
- `internal/safety`
- `internal/execute`
- `internal/history`
- focused tests and golden fixtures

Work:

- Define plan, operation, effect, risk, lifecycle status, decision, confirmation, digest, and typed operational error structs.
- Implement stable plan canonicalization and digesting.
- Implement policy evaluation for read-only, local-write, remote-write, destructive, and privileged risks.
- Implement minimal executor that checks preconditions, runs typed adapter operations, verifies postconditions, stops on failure, and records results.
- Implement append-only JSON Lines history with redaction and atomic-safe writes where practical.

Tests:

- Risk classification.
- Plan digest stability and digest changes when meaningful fields change.
- State transitions and cancellation.
- Policy denials for destructive and privileged effects.
- History redaction and schema versioning.
- Executor stops after failed steps and records partial completion.

Documentation:

- Update safety and architecture docs.

Risks:

- Building a generic workflow system larger than the self-hosting path needs.

Acceptance criteria:

- Mutation plans cannot execute without policy evaluation and matching confirmation digest.

### 3. [x] Implement Git Adapter Capabilities

Dependencies: task 2.

Affected packages/files:

- `internal/adapters/git`
- `internal/process` tests if new runner options are needed
- Git adapter tests

Work:

- Implement `git.repo.inspect`, `git.repo.init`, `git.index.stage`, `git.commit.create`, `git.remote.inspect`, `git.remote.configure`, and `git.branch.push`.
- Parse Git status and remote outputs into typed results.
- Preserve user path strings as literal argv entries and use `--` for staging paths.
- Add postcondition checks for each mutation.

Tests:

- Fake runner tests assert exact executable and argv for every operation.
- Temporary repository integration tests for init, inspect, staging explicit paths, ignored-file exclusion, commit with local test identity, remote add, and push to a local bare repository if useful.
- Failure tests for missing Git, detached HEAD, empty index, invalid branch, invalid remote, unexpected origin, and process timeout.

Documentation:

- Registry and safety docs list implemented Git capabilities.

Risks:

- Accidentally invoking Git in the actual Devtize folder during tests or development.

Acceptance criteria:

- All Git mutation behavior is verified in temporary repositories only.
- No adapter uses shell command strings or force options.

### 4. [x] Implement GitHub `gh` Adapter Capabilities

Dependencies: task 2.

Affected packages/files:

- `internal/adapters/github`
- fake `gh` fixtures under `testdata/`

Work:

- Implement `github.auth.inspect`, `github.repo.inspect`, and `github.repo.create` through `gh`.
- Parse `gh` JSON output into typed results.
- Redact auth diagnostics.
- Make repository existence and name collision behavior explicit.

Tests:

- Fake runner or fake executable tests for exact `gh` argv.
- Authenticated, unauthenticated, auth-unknown, missing `gh`, repo missing, repo exists compatible, repo exists incompatible, and create failure cases.
- No normal CI test creates a real GitHub repository.

Documentation:

- Document `gh` requirement and no-real-remote CI rule.

Risks:

- Depending on live user authentication or creating real remote resources in tests.

Acceptance criteria:

- GitHub remote-write behavior is fully testable without live GitHub access.

### 5. [x] Implement Planning For `dvz repo create`

Dependencies: tasks 2, 3, and 4.

Affected packages/files:

- `internal/app`
- `internal/workflow`, only if justified
- `cmd/dvz`
- plan golden tests

Work:

- Add `dvz repo create`, `dvz repo plan`, and `dvz repo status`.
- Build the canonical self-hosting plan from config, flags, project evidence, Git state, `gh` state, and selected paths.
- Render text and JSON plans with complete local and remote effects.
- Support `--dry-run`, `--plan-json`, `--json`, `--yes`, and non-interactive behavior.

Tests:

- Golden text and JSON plan output.
- CLI tests for required flags, invalid combinations, invalid names, invalid paths, non-interactive missing message, and no selected files.
- Plan tests for existing repo, existing commit, compatible existing remote, unexpected origin, existing GitHub repo, name collision, and dry-run.

Documentation:

- README command examples and configuration updates.

Risks:

- Hiding meaningful effects in summaries instead of listing exact targets.

Acceptance criteria:

- A user can see exactly what `dvz repo create` would mutate before any mutation occurs.

### 6. [x] Implement Confirmation And Execution For `dvz repo create`

Dependencies: task 5.

Affected packages/files:

- `internal/app`
- `internal/ui`, only for prompt/rendering ports if needed
- `cmd/dvz`
- execution tests

Work:

- Add confirmation prompts for local writes, remote writes, and secret preflight warnings.
- Ensure confirmation is bound to the immutable plan digest.
- Execute steps in canonical order.
- Verify postconditions immediately after each mutation.
- Return distinct cancelled, failed, partial, and succeeded outcomes.

Tests:

- Declining local-write confirmation causes zero mutation adapter calls.
- Declining remote-write confirmation performs no remote writes and no subsequent remote-dependent steps.
- Changed plan digest invalidates previous confirmation.
- `--yes` follows scoped policy and cannot skip remote write or secret warning by default.
- Partial failure records completed steps and recovery guidance.

Documentation:

- Safety docs describe confirmation boundaries.

Risks:

- Allowing UI or CLI flags to bypass policy or plan digest checks.

Acceptance criteria:

- Mutations can occur only after the exact rendered plan is authorized under policy.

### 7. [x] Implement Secret And Ignore Preflight

Dependencies: tasks 3 and 5.

Affected packages/files:

- likely `internal/safety`
- Git staging planner tests

Work:

- Detect likely secret filenames and common secret-like content patterns in selected files with bounded reads.
- Respect ignore rules and excluded paths.
- Report warnings without storing secret values.
- Require explicit confirmation for likely secrets.

Tests:

- Secret filename warnings for `.env`, private keys, token-like filenames, and credential files.
- Bounded content detection and redaction.
- Ignored files are not staged or force-added.
- `--yes` cannot bypass secret warning in Phase B.

Documentation:

- Safety docs explain limits and false positives.

Risks:

- Reading too much file content or leaking matched secret values.

Acceptance criteria:

- Staging selected files includes a deterministic, redacted safety preflight.

### 8. [x] Add Idempotent Resume And Recovery Reporting

Dependencies: tasks 3, 4, and 6.

Affected packages/files:

- `internal/execute`
- `internal/history`
- adapter postcondition tests

Work:

- Re-evaluate current Git/GitHub state before each operation.
- Skip satisfied steps only when postconditions verify.
- Generate recovery hints for every known partial-failure state.
- Ensure reruns do not rely solely on history records.

Tests:

- Rerun after init only.
- Rerun after commit but before GitHub repo creation.
- Rerun after GitHub repo creation but before origin.
- Rerun after origin but before push.
- Existing incompatible states stop safely.

Documentation:

- README troubleshooting and safety docs include partial-failure examples.

Risks:

- Treating history as proof of external state.

Acceptance criteria:

- Re-running a partially completed workflow continues only after live postcondition verification.

### 9. [x] Add Opt-In Live GitHub Fixture Documentation

Dependencies: task 4.

Affected files:

- `docs/self-hosting.md` later, or `docs/github-live-test.md` if separate
- README development/testing section

Work:

- Document an opt-in maintainer script or manual procedure for disposable live GitHub testing.
- Require an explicit environment variable such as `DEVTIZE_LIVE_GITHUB_TEST=1`.
- Require a disposable owner/repo name and cleanup instructions.
- Ensure CI never runs the live fixture.

Tests:

- Normal test suite verifies the live fixture is skipped by default.

Documentation:

- State that normal CI and local default tests create no real remote repositories.

Risks:

- Accidental real remote writes from test defaults.

Acceptance criteria:

- Maintainers have a documented live verification path, and default tests remain isolated.

### 10. [x] Update Documentation For Actual Phase B Behavior

Dependencies: tasks 2 through 9.

Affected files:

- `README.md`
- `docs/architecture.md`
- `docs/safety.md`
- `docs/registry.md`
- `docs/configuration.md`
- `docs/plans/phase-b.md`

Work:

- Synchronize docs with implemented command names, flags, schemas, errors, and support levels.
- Clearly separate implemented Phase B behavior from later roadmap items.
- Keep self-hosting evidence pending until the real maintainer-run command completes.

Tests:

- Link/text checks if available.
- Manual review for claims about unimplemented behavior.

Acceptance criteria:

- Docs describe exactly what the implementation does and do not imply the real repository has been self-hosted before it has.

### 11. [x] Final Phase B Verification Before Real Self-Hosting

Dependencies: all implementation tasks.

Work:

- Run formatting checks.
- Run `go test ./...`.
- Run `go vet ./...`.
- Run configured linter if present.
- Run `go build ./cmd/dvz`.
- Run focused CLI smoke checks for `dvz repo plan`, `dvz repo create --dry-run`, and `dvz repo status` in temporary repositories.
- Review diff for shell interpolation, force options, permission bypass, secret leaks, stale docs, accidental actual-repo mutation, and phase expansion.

Acceptance criteria:

- All Phase B acceptance criteria pass.
- No real Devtize folder Git/GitHub mutation has been performed manually.
- The final checkpoint tells the maintainer the exact `dvz repo create ...` command to run for real self-hosting.

### 11.1. [x] Repair An Unpushed Initial Commit

This corrective task was added after the first maintainer-run attempt exposed two
Phase B defects: the disclosed-all selection admitted the project-local Go build
cache, and a rerun after creating the first commit could stage changes without
committing them before push.

Scope:

- Add `--repair-unpushed-initial` to `dvz repo create` and `dvz repo plan`.
- Permit repair only when Git has exactly one local commit, no configured
  upstream, the expected GitHub repository already exists, and GitHub reports no
  default branch. These preconditions establish that the initial branch has not
  been published by this workflow.
- Show ignored tracked paths and exact staging effects in the immutable plan.
- Render the complete plan before asking for confirmation. Group large
  same-directory path inventories in human output with exact counts while
  retaining every literal path in the digested `--plan-json` representation.
- Untrack ignored generated artifacts with `git rm --cached` so working files
  are not deleted, stage disclosed changes, and amend the initial commit with the
  supplied conventional commit message.
- Classify the amend as destructive because it rewrites a commit. Require an
  additional plan-digest-bound confirmation using the exact response `repair`;
  `--yes` cannot bypass it.
- Keep the existing grouped local-write confirmation and the separate remote
  repository/push confirmation.
- When the existing remote identifies the exact same GitHub owner/repository but
  uses a protocol that `gh` is not configured to authenticate, disclose and
  update only the transport URL to the protocol reported by `gh auth status`.
  A different owner or repository remains a hard refusal.
- Refuse repair when any publication evidence exists. Never force-push, reset,
  delete a repository, overwrite a remote, or generalize this into arbitrary
  history editing.
- Exclude `/.cache/` and the root `/dvz` build output from future commits through
  the repository ignore file.

Tests:

- Fake-runner tests assert exact argv for untracking and amending.
- Application tests cover every repair precondition, dry-run zero mutation,
  mandatory destructive confirmation, and no push after a failed repair.
- A temporary repository integration test reproduces an initial commit that
  tracks generated artifacts and verifies a one-commit repaired history.
- No test mutates the actual Devtize repository or contacts a live GitHub
  repository.

Acceptance criteria:

- The current partial milestone can be repaired and completed solely through a
  maintainer-run `dvz repo create --repair-unpushed-initial ...` command.
- The repaired commit contains neither `.cache` nor the root `dvz` binary.
- The repair path is impossible after an upstream or GitHub default branch is
  observed.
- Task 12 remains unchecked until the maintainer personally runs and verifies
  the final command.

### 12. [x] Maintainer-Run Self-Hosting Milestone

Dependencies: task 11.1 complete.

Work:

- The maintainer runs the approved `dvz repo create ...` command in the Devtize folder.
- Devtize renders the plan and asks for required confirmations.
- The maintainer confirms or declines.
- Devtize executes through trusted adapters if confirmed.
- Devtize verifies postconditions and writes sanitized history.
- Add `docs/self-hosting.md` evidence after completion.

Tests:

- This is not a CI test. It is the real milestone performed once the tool is ready.

Acceptance criteria:

- The Devtize repository is initialized, committed, published, and pushed using Devtize itself.
- The coding agent did not manually run `git init`, `git add`, `git commit`, `gh repo create`, `git remote add`, or `git push` in the Devtize folder.
- The maintainer personally ran the real `dvz` command.

### 12.1. [x] Remove A Disclosed File From The Pushed Initial Commit

This maintainer-authorized remediation removes `MASTER_IDE_PROMPT.md` from the
single pushed initial commit while preserving the local ignored file.

Scope:

- Add `dvz repo redact-initial <path>` as a narrow Phase B remediation command.
- Require exactly one local commit on an attached branch, an existing upstream,
  and exact equality between `HEAD` and the remote-tracking commit before
  planning.
- Require the selected relative file to exist locally, be tracked, and match the
  current `.gitignore` rules.
- Disclose all staged files, the index-only removal, the commit rewrite, and the
  exact expected remote SHA in an immutable plan.
- Preserve the local selected file with `git rm --cached` and amend the initial
  commit using the existing commit message.
- Push only with `--force-with-lease=refs/heads/<branch>:<expected-sha>`; never
  use unconditional `--force`.
- Require a digest-bound `redact` confirmation before local mutation and a
  separate digest-bound `force-update` confirmation before the remote rewrite.
  Neither confirmation can be bypassed by `--yes`.
- Stop with actionable recovery if any local or remote precondition changes.
- Keep normal `repo create` and all generic command paths unable to force-push.

Tests:

- Fake-runner tests assert the exact lease argument and reject plain force.
- Application tests cover path validation, ignored/tracked checks, stale
  upstream state, both confirmations, changed plan digests, and partial failure.
- A temporary repository and local bare remote verify that the file remains on
  disk, disappears from the sole commit, and the rewritten commit is pushed.
- No test mutates the actual Devtize repository or contacts GitHub.

Acceptance criteria:

- The maintainer can remove `MASTER_IDE_PROMPT.md` from the one pushed commit
  exclusively through `dvz`.
- The local file remains present and ignored.
- A concurrent remote update causes the lease-protected push to fail safely.
- `docs/self-hosting.md` remains pending until the final rewritten state is
  verified.

## Cross-Cutting Test Matrix

| Area | Required coverage |
|---|---|
| CLI | `repo create`, `repo plan`, `repo status`, `repo set-description`, invalid flags, non-interactive behavior, `--json`, `--plan-json`, `--dry-run`, `--yes`, declined confirmations |
| Plans | canonical operation order, stable digest, digest invalidation, text and JSON goldens, complete local/remote effect rendering |
| Safety | local-write and remote-write policy, general destructive/privileged denial, guarded initial-repair confirmation, secret preflight, scoped `--yes`, plan-bound confirmation |
| Git adapter | exact argv, no shell, init, inspect, explicit staging, ignored-file exclusion, recovery-only untrack/amend, commit, remote inspect/add, push without force |
| GitHub adapter | exact `gh` argv, auth states, repo inspect/create, compatible existing repo, incompatible name collision, redaction |
| Dry-run | zero mutation adapter calls, no repository changes, no remote creation, valid plan output |
| Idempotency | existing repo, existing commit, existing repo target, existing origin, pushed branch, partial reruns |
| Recovery | partial records, safe hints, no automatic destructive cleanup, history redaction |
| Isolation | temporary repositories, fake process runners, fake `gh`, no live GitHub in normal CI |
| Documentation | README and focused docs match implemented behavior and avoid premature self-hosting claims |

## Phase Boundary Guardrails

- Do not implement `dvz raw`.
- Do not implement arbitrary `dvz git ...` passthrough.
- Do not execute discovered `CommandKnowledge`.
- Do not add a broad workflow catalog beyond the self-hosting path.
- Do not implement general force push, repository deletion, unexpected remote overwrite, reset hard, or destructive recovery beyond the exact unpublished-initial-commit repair in task 11.1 and exact-lease file redaction in task 12.1. Task 11.1 may normalize only an equivalent GitHub remote to the authenticated `gh` protocol.
- Do not create real GitHub repositories in normal CI.
- Do not mutate the actual Devtize folder except through the final maintainer-run `dvz` command after all acceptance criteria pass.
- Do not require AI for any Phase B success path.
- Do not claim self-hosting is complete until verified evidence exists.

## Measurable Phase B Acceptance Criteria

- [x] `go build ./cmd/dvz` succeeds.
- [x] `go test ./...` and `go vet ./...` pass.
- [x] Formatting checks pass for every changed Go file.
- [x] The configured linter passes, or no linter is configured and that is explicitly reported.
- [x] `dvz repo plan`, `dvz repo create --dry-run`, and `dvz repo status` work in temporary repositories.
- [x] Dry-run displays all intended local and remote effects and causes zero mutations, verified with fakes and temporary-repository tests.
- [x] Existing Git repositories, commits, origins, non-empty remotes, name collisions, detached HEAD, no files, ignored files, missing Git, missing `gh`, and unauthenticated `gh` receive explicit safe outcomes.
- [x] Staging uses explicit path arguments or a disclosed all-files selection; ignored files are not force-added.
- [x] Secret preflight warnings are redacted, deterministic, and cannot be bypassed by `--yes` in Phase B.
- [x] The normal repository workflow never overwrites an unexpected origin or pushes with force; initial redaction requires an exact expected-SHA lease.
- [x] Declining local-write or remote-write confirmation stops subsequent effects and returns `CONFIRMATION_DECLINED`.
- [x] Failures after partial progress are recorded with completed steps, pending steps, and actionable safe recovery guidance.
- [x] Re-running after partial completion skips only steps whose postconditions are verified from current state.
- [x] Unit tests assert exact executable and argument arrays for every Git and `gh` adapter operation.
- [x] Integration tests use temporary repositories and fake `gh` or controlled adapters; normal CI creates no real GitHub repositories.
- [x] An opt-in live GitHub fixture procedure is documented and skipped by default.
- [x] README and focused docs describe actual Phase B behavior, risk boundaries, dry-run, idempotency, rollback limits, and self-hosting procedure.
- [x] Before the real self-hosting milestone, no one has manually run Git or GitHub mutation commands in the Devtize folder.
- [x] An interrupted unpublished initial commit can be repaired only through the narrow, digest-confirmed `--repair-unpushed-initial` path.
- [x] The final real self-hosting milestone is performed by the maintainer through `dvz` commands and documented afterward in `docs/self-hosting.md` with sanitized evidence.
