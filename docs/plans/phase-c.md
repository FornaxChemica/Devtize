# Phase C Implementation Plan

## Scope

Phase C turns the Phase B operation foundations into reusable daily Git
workflows. It adds deterministic `status`, `commit`, `ship`, `history`, and
constrained `undo` behavior without arbitrary shell execution or AI authority.

The first vertical slice is `dvz commit`. It must be useful independently and
safe enough for Devtize to commit its own post-self-hosting changes.

## Guardrails

- Do not implement `dvz raw`, arbitrary `dvz git ...` passthrough, TUI, MCP,
  provider sync, runtime setup, cloud operations, or AI providers.
- Every mutation is represented by an immutable plan before execution.
- Only reviewed adapters may invoke Git or GitHub commands.
- Never infer authorization from history, prior prompts, or conversational
  context.
- Never force-add ignored files, stage undisclosed paths, force-push, reset
  hard, or guess an inverse operation.
- Tests mutate only temporary repositories, local bare remotes, or fakes.
- `docs/plans/*.md` remains planning material and is excluded from the first
  dogfooded Phase C commit at the maintainer's request.

## Command Surface

### First Slice

```text
dvz commit [paths...] --message <conventional-message>
dvz commit [paths...] --message <message> --conventional=false
dvz commit [paths...] --message <message> --dry-run
dvz commit [paths...] --message <message> --plan-json
```

Defaults and behavior:

- Explicit paths select only those changed files.
- With no paths, Devtize selects every changed, non-ignored path and displays
  each path before confirmation.
- `--message` is required in the first slice; AI generation remains absent.
- Conventional Commit validation is enabled by default.
- `--dry-run` and `--plan-json` perform no mutation.
- One digest-bound confirmation authorizes staging and commit creation.
- There is no `--yes` bypass in the first slice.

### Later Phase C Commands

```text
dvz status
dvz ship [paths...] --message <message> [--check <capability-id>...]
dvz history [--limit <n>] [--json]
dvz undo <execution-id> --dry-run
```

`status` may initially compose existing read-only repository inspection.
`ship` adds checks, commit, push, and optional pull-request boundaries only
after `commit` is stable. `undo` may expose only registered compensations and
must explain when no exact compensation exists.

## First-Slice Operations

### `git.index.stage`

- Risk: `local_write`
- Inputs: project root and exact normalized relative paths
- Preconditions: repository exists, attached branch, non-empty eligible
  selection, paths remain within the project, ignored files excluded
- Effect: stage disclosed paths using `git add -- <paths...>`
- Idempotency: repeatable for identical file content

### `git.commit.create`

- Risk: `local_write`
- Inputs: project root and validated message
- Preconditions: staging succeeded and the index contains a change
- Effect: create one commit using `git commit --message <message>`
- Postconditions: `HEAD` changed, commit count increased by one, commit message
  matches, and selected paths are no longer unstaged
- Recovery: if staging succeeds but commit creation fails, report that the
  disclosed paths remain staged and provide a retry command; do not unstage
  automatically

## Validation

Conventional Commit syntax for the first slice:

```text
<type>[optional scope][!]: <description>
```

Accepted types: `build`, `chore`, `ci`, `docs`, `feat`, `fix`, `perf`,
`refactor`, `revert`, `style`, and `test`.

The description must be non-empty. Newlines are rejected in the first slice.
`--conventional=false` permits another non-empty single-line message while
keeping all other safety behavior.

Paths must be normalized relative paths inside the project root. Absolute
paths, traversal, `.git`, ignored files, unchanged paths, duplicates, and
paths absent from current Git status are rejected or excluded with an explicit
reason. Rename records must preserve both old and new path semantics rather
than parsing display text ambiguously.

## Plan And Confirmation

The plan includes:

- project root, current branch, current `HEAD`, and selection mode;
- every selected path and a SHA-256 digest for present file content;
- the exact commit message;
- ordered stage and commit operations;
- local-write risk and effects;
- recovery limits.

Immediately before mutation, Devtize verifies the plan digest, `HEAD`, branch,
selected-path status, and content digests. Any change requires a fresh plan.

The CLI renders the complete plan before asking:

```text
Create this commit for plan sha256:<digest>? Type commit to continue:
```

Non-interactive EOF is a declined confirmation and never hangs.

## Application And Adapter Boundaries

- Add a focused `CommitService` under `internal/app`.
- Reuse `operation.Plan`, safety risks, confirmation semantics, history, and
  the Git adapter.
- Extend the Git adapter only for narrowly required read operations such as
  inspecting the staged index and commit message.
- Keep Cobra wiring and rendering in `cmd/dvz`.
- Do not introduce a generic workflow DSL until `commit` and `ship` expose
  concrete shared complexity.

## Error Behavior

- `PROJECT_NOT_FOUND`: no Git repository at the selected root.
- `PLAN_INVALID`: invalid message, path, schema, or digest.
- `PRECONDITION_FAILED`: detached HEAD, no eligible changes, stale `HEAD`,
  changed file content, ignored selection, or unexpected staged state.
- `CONFIRMATION_DECLINED`: no mutation started.
- `PARTIAL_EXECUTION`: staging completed but commit failed.
- `POSTCONDITION_FAILED`: Git returned success but the expected commit state
  could not be verified.
- `PROCESS_FAILED` and `PROCESS_TIMEOUT`: safe wrapped adapter failures.

Every partial failure names completed steps, current known state, and a safe
retry path without destructive cleanup.

## Testing

- Unit tests for Conventional Commit validation and path normalization.
- Plan tests for stable operation order, digest invalidation, complete path
  disclosure, content changes, stale `HEAD`, and detached HEAD.
- Confirmation tests proving no adapter mutation before exact `commit` input.
- Fake-runner tests asserting exact Git executable, argv, directory, timeout,
  environment policy, and literal metacharacter preservation.
- Temporary-repository tests for successful commits, selected-path commits,
  ignored files, deletions, staged changes, commit failure recovery, and
  postcondition verification.
- CLI tests for human output, JSON, plan JSON, dry-run, EOF, invalid messages,
  and exit-code mapping.
- Full formatting, tests, race tests, vet, and build.

## Ordered Tasks

### 1. [x] Close Phase B Evidence

- Record sanitized self-hosting evidence.
- Verify live repository metadata and branch SHA.
- Mark the Phase B milestone complete without publishing workstation secrets.

### 2. [x] Implement Commit Read Models And Validation

- Add Conventional Commit validation.
- Add exact changed-path and staged-index inspection required by planning.
- Cover parser and adapter contracts.

### 3. [x] Implement `CommitService` Planning

- Validate repository and branch state.
- Resolve explicit or disclosed-all changed paths.
- Reject ignored, unchanged, escaping, or ambiguous paths.
- Add file digests and build the immutable plan.

### 4. [x] Implement Confirmation, Execution, And Recovery

- Revalidate plan authority immediately before mutation.
- Stage only disclosed paths.
- Create and verify the commit.
- Record success, cancellation, and partial failure in redacted history.

### 5. [x] Add `dvz commit` CLI And Documentation

- Add flags, human rendering, JSON, plan JSON, dry-run, and stable errors.
- Update README, architecture, safety, registry, and integration smoke tests.

### 6. [x] Verify And Dogfood The First Slice

- Run the complete verification matrix.
- Build `./dvz` without staging the ignored binary.
- Give the maintainer an explicit `dvz commit` command selecting all intended
  files except `docs/plans/*.md`.
- The maintainer reviews the plan and personally runs the commit.

### 7. [x] Implement Read-Only Status And History

- Normalize daily status separately from the Phase B bootstrap status.
- Render bounded, redacted, versioned history records.

### 8. [x] Implement `ship`

- Plan reviewed checks, commit, and push as separate immutable local and remote boundaries.
- Stop on failed checks and require separate remote confirmation.
- Verify live remote state before push and on resume.
- Preserve push-only JSON compatibility and leave PR creation for a future dedicated boundary.

### 8.2. [x] Add Polished Deterministic Terminal UX

- Render detailed plans, status, history, progress, results, and errors through `internal/ui`.
- Adapt color, symbols, and wrapping to TTY capabilities with ASCII and no-color fallbacks.
- Keep JSON ANSI-free and cover 60, 80, and 120 column layouts with goldens.

### 8.1. [x] Implement And Dogfood Push-Only `ship`

- Detect an attached branch that is ahead of its configured upstream.
- Read the live remote SHA and require it to match the remote-tracking SHA.
- Prove fast-forward ancestry and disclose every outgoing commit.
- Bind the remote URL, expected SHA, local `HEAD`, and excluded dirty paths into
  the immutable plan.
- Require the digest-bound confirmation word `push` and push with a full branch
  refspec without force.
- Verify live remote, local `HEAD`, upstream, and remote-tracking state.
- Treat reruns as verified no-ops and uncertain process failures as partial
  execution requiring live reinspection.
- Test mutations only with fakes and local bare remotes.

### 9. [x] Implement Constrained `undo` Planning

- Inspect exact project-scoped execution records and expose only the registered planner-only compensation.
- Validate typed commit evidence against live branch, `HEAD`, parent, worktree, and upstream state.
- Render digest-bound dry-run plans and limitations without executing or recording a mutation.
- Never infer reversal from free-form or legacy history.

## First-Slice Acceptance Criteria

- [x] Explicit-message commits work with AI disabled.
- [x] Conventional Commit validation is enabled by default and bypassable only
  through the explicit `--conventional=false` flag.
- [x] Plans disclose exact paths, message, branch, `HEAD`, risks, and effects.
- [x] Dry-run causes zero mutation calls.
- [x] Exact digest-bound `commit` confirmation is required.
- [x] Stale plans and changed selected files fail before staging.
- [x] Only disclosed paths are staged; ignored files are never force-added.
- [x] Partial staging/commit failures have actionable recovery and history.
- [x] Temporary-repository tests prove successful and failed workflows.
- [x] Full tests, race tests, vet, formatting, and build pass.
- [x] Devtize can create its next commit while excluding every
  `docs/plans/*.md` file.

## Composed Ship And Terminal UX Acceptance Criteria

- [x] Push-only invocation and schema-version-1 JSON remain compatible.
- [x] Composed ship discloses checks, selected paths, message, branch, remote,
  risks, effects, and the deferred remote boundary.
- [x] Only four reviewed Go check IDs are accepted; configuration cannot
  provide executable names, arguments, environment values, or shell text.
- [x] Check failure causes zero staging, commit, or remote-write calls.
- [x] Local and remote mutation boundaries require separate digest-bound
  `commit` and `push` confirmations.
- [x] The exact push plan is created only after the commit SHA exists and fresh
  live-remote inspection succeeds.
- [x] Partial outcomes have correlated redacted history and deterministic
  `dvz ship` recovery guidance.
- [x] Human output has tested color/no-color, Unicode/ASCII, redirected, and
  60/80/120-column behavior; JSON remains ANSI-free.
- [x] Unit, integration, race, vet, formatting, build, and local-bare-remote
  acceptance checks pass.
- [x] PR creation, executable undo, pushed-commit revert, AI, TUI, MCP,
  provider sync, and arbitrary execution remain outside this milestone.

## Constrained Undo Planning Acceptance Criteria

- [x] `dvz undo <execution-id> --dry-run` never mutates Git, remotes, or history.
- [x] History locates evidence but never supplies executable behavior or authorization.
- [x] Eligible future commit records produce one digest-bound planner-only compensation operation.
- [x] Published, stale, ambiguous, unsupported, and legacy records return stable unavailable reasons without guessing.
- [x] Existing schema-version-1 history remains readable through additive observed changes.
- [x] Human and JSON output are stable, ANSI-safe, and explicit that no changes were made.
- [x] Full tests, race tests, vet, formatting, build, diff checks, and local-bare-remote acceptance pass.
