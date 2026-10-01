# Phase D.2: Version-Aware GitHub CLI Knowledge Sync

## Status

Implementation complete and verified on 2026-10-01. Every checked task and
acceptance criterion below has corresponding implementation and automated or
isolated built-binary verification.

Phase D.2 continues the shipped Phase D.1 provider-sync work. It does not claim
completion of the broader runtime ecosystem or richer provider-sync roadmap in
`MASTER_IDE_PROMPT.md`.

## Goal

Add a second bounded, version-aware discovery provider:

```text
dvz sync gh
dvz sync gh --dry-run
dvz --json sync gh [--dry-run]
dvz find --provider gh <intent>
```

Devtize will detect the exact installed GitHub CLI version, invoke the fixed
local `gh help reference` source in an isolated environment, parse official
built-in command paths, aliases, usage, and flags into quarantined command
knowledge, and publish a validated immutable provider snapshot after the normal
digest-bound `sync` confirmation. Later `find` and `doctor` calls consume the
last valid snapshot without invoking `gh`.

This phase proves that the Phase D.1 cache and publication model scales to a
provider with nested commands and flags without creating a generic plugin
system or granting execution authority to introspected help.

## User Value

- GitHub CLI changes independently of Devtize releases. Sync makes discovery
  match the installed `gh` version instead of relying on model memory or a
  stale hand-maintained catalog.
- One local reference document exposes substantially more useful knowledge
  than recursive command invocation. On the planning machine, isolated
  `gh 2.93.0` produced an 86,383-byte reference containing 207 command
  headings, 524 flag rows, and 41 alias rows.
- Provider filtering keeps a larger catalog usable and deterministic.
- The entire path remains local-first, offline after synchronization, and
  useful with AI disabled.

The observed counts above are fixture evidence, not a permanent product claim.
Parser limits leave bounded room for later `gh` versions.

## Scope

Phase D.2 includes:

- one additional provider accepted by `dvz sync`: `gh`;
- one fixed local source command: `gh help reference`;
- depth-two built-in GitHub CLI command paths;
- official aliases, usage syntax, and normalized flag metadata;
- provider-specific snapshot validation within cache schema version 1;
- coexistence of independent Git and GitHub CLI snapshots;
- offline multi-provider search and an optional `--provider git|gh` filter;
- per-provider cache state in `find` and `doctor` through additive JSON fields;
- existing immutable cache publication, confirmation, revalidation, history,
  terminal rendering, and error behavior.

## Explicit Non-Goals

Do not implement any of the following in this phase:

- executing command knowledge or generating capabilities from help;
- invoking discovered `gh` commands, aliases, or extensions;
- authentication checks, GitHub API calls, browser launches, or network docs;
- synchronization of user aliases or installed extensions;
- recursive `gh <command> --help` traversal;
- nested Git help, Git flags, or changes to the Phase D.1 Git source;
- `dvz sync` with no provider, multiple providers, or an `all` provider;
- automatic/background sync, cache TTLs, or configuration-controlled argv;
- `dvz explain`, cheatsheets, AI, `raw`, passthrough, TUI, or MCP;
- a provider plugin API, parser DSL, generic workflow engine, or provider
  registration through YAML.

GitHub CLI knowledge remains discovery-only even where Devtize already has a
separately reviewed executable GitHub capability. The two registries must not
be joined implicitly.

## Assumptions And Compatibility Contract

- Phase D.1 is complete and its checked plan is the implementation baseline.
- The supported synchronization providers become exactly `git` and `gh`.
- `dvz sync <provider>` continues to require exactly one provider argument.
- Existing `dvz sync git` human output, JSON fields, snapshot digests, cache
  paths, history shape, and behavior remain compatible.
- Existing schema-version-1 Git snapshots remain readable byte-for-byte. New
  optional fields must use `omitempty` so recomputing an old snapshot digest
  does not add zero-value JSON fields.
- Existing builtin command JSON is unchanged. New knowledge fields are
  additive and omitted when absent.
- Existing push-only and composed `ship`, repository workflows, history, and
  undo behavior are outside this change and must retain their tests.
- `gh help reference` is treated as untrusted local data. Structural
  validation, not the tool's exit code alone, determines whether publication
  is allowed.
- No new Go dependency is required.

## Public Interfaces

### Sync

- [x] `dvz sync gh` plans and, after exact confirmation, publishes GitHub CLI
  discovery knowledge.
- [x] `dvz sync gh --dry-run` performs version detection, one bounded help
  invocation, parsing, normalization, and full validation, but writes no cache
  or history.
- [x] `dvz --json sync gh [--dry-run]` uses the existing schema-version-1
  `SyncResponse`. Add optional parsed/published alias and flag counts; omit
  those fields for Git so existing Git JSON remains unchanged.
- [x] `dvz sync git` remains supported exactly as shipped.
- [x] No provider, more than one provider, or any provider other than `git` or
  `gh` returns `INVALID_USAGE` with the accepted values in the hint.
- [x] Normal publication requires the digest-bound word `sync`. EOF or any
  other response returns `CONFIRMATION_DECLINED` and writes nothing.
- [x] Missing and unsupported `gh` retain `TOOL_NOT_FOUND` and
  `TOOL_VERSION_UNSUPPORTED`. Reference, parser, validation, and publication
  failures return `SYNC_FAILED` with a provider-specific safe hint.

### Find

- [x] Add `dvz find --provider <id> <intent>` for `git` and `gh`.
- [x] Omitting `--provider` searches all reviewed builtins and all valid loaded
  provider snapshots.
- [x] An unsupported provider returns `INVALID_USAGE`; a supported provider
  with no matching knowledge returns `CAPABILITY_NOT_FOUND` and a provider-
  specific hint.
- [x] Add optional `provider` and per-provider registry state to the version-1
  `FindResponse`. Preserve all existing result fields and ordering rules.
- [x] Add optional command kind, usage, flags, and matched-field evidence to
  search results. Existing builtin results omit absent fields.
- [x] `find` continues to have no process-runner dependency and never offers an
  execution prompt.

### Doctor

- [x] Preserve existing aggregate `RegistryCheck` fields and add a sorted
  `providers` list with provider ID, cache status, knowledge version, detected
  version, entry count, and provider-scoped warnings.
- [x] A missing snapshot is `not_synced`, not an error. Stale or invalid cache
  data is a warning. Corruption in one provider cannot hide valid data from
  another provider.
- [x] `doctor` compares the already detected live Git and `gh` versions with
  their own snapshots and never mutates cache state.

## Knowledge Model Additions

Extend `registry.CommandKnowledge` only with fields needed by the parsed
reference:

```text
kind: group | command
usage: exact bounded usage suffix from the heading
flags: []FlagKnowledge
```

`FlagKnowledge` contains only display/search metadata:

```text
long_name: required normalized --long-name
short_name: optional normalized -x
value_hint: optional bounded untrusted syntax such as string or OWNER/REPO
summary: required single-line help description
```

The existing `aliases` field stores fully qualified aliases such as
`gh pr co`. Alias text remains search metadata and is never resolved or
executed by Devtize.

Requirements:

- [x] New fields are additive, typed, bounded, deterministically ordered, and
  omitted from JSON/YAML when absent.
- [x] `CommandKnowledge.Validate` validates syntax and sizes without treating
  synced usage, flags, aliases, summaries, or risk labels as authority.
- [x] Synced GitHub CLI entries use stable IDs derived only from validated
  path segments, for example `sync.gh.pr.create`.
- [x] Every entry remains `discoverable`, uses risk `unclassified`, and uses
  the effect `Discovery-only help metadata; effects are not reviewed.`
- [x] No synced field can name a capability ID, adapter binding, executable
  path, environment value, validator, operation risk, or confirmation policy.

## Fixed Source And Isolation

### Installation Detection

- Detect `gh` with the existing reviewed `gh --version` path and minimum
  version `2.0.0`.
- Preserve the resolved executable path, exact parsed version, and detection
  time in the snapshot.
- Use the same isolated environment for version detection and reference
  capture. Do not inspect auth state.

### Reference Invocation

Invoke exactly:

```text
gh help reference
```

through `internal/process` with:

- the resolved executable path;
- literal argv `help`, `reference`;
- the isolated temporary directory as the working directory;
- disabled stdin;
- a 15-second timeout;
- a 2-MiB capture limit;
- allowlisted inherited environment limited to `PATH`, `SYSTEMROOT`, and
  `WINDIR`;
- isolated `HOME`, `GH_CONFIG_DIR`, `XDG_CONFIG_HOME`, and `XDG_STATE_HOME`;
- `GH_PROMPT_DISABLED=1`, `GH_NO_UPDATE_NOTIFIER=1`, `GH_PAGER=cat`,
  `PAGER=cat`, `NO_COLOR=1`, `CLICOLOR=0`, `TERM=dumb`, and `LC_ALL=C`.

Reject truncated stdout or stderr. Reject unexpected non-empty stderr. Never
inherit tokens, host overrides, auth headers, editor/browser settings, user
configuration, aliases, or extension directories.

## GitHub Reference Parser Contract

Add a provider-owned, versioned parser in `internal/adapters/github`; do not
put Markdown parsing rules in `internal/registry`.

The parser must:

- [x] Require the exact document anchor `# gh reference`.
- [x] Accept command headings only from `## gh ...` and `### gh ...`.
- [x] Derive path segments from validated lowercase/hyphen tokens and stop at
  the first usage token beginning with `-`, `[`, `<`, or `{`.
- [x] Require every `###` path to extend its current `##` parent by exactly one
  segment. Maximum command depth is two beneath `gh`.
- [x] Preserve the bounded heading suffix as display-only usage syntax without
  interpreting it as argv or an input schema.
- [x] Read the first non-empty plain-text paragraph after a heading as its
  summary. Reject missing, multiline-ambiguous, or oversized summaries rather
  than guessing.
- [x] Parse only the documented `Aliases` block attached to the current
  command. Each alias must begin with `gh`, contain only validated path
  segments, and stay within depth and count limits.
- [x] Parse only fixed-column flag rows attached to the current command.
  Normalize a required long name, optional short name, optional value hint,
  and a non-empty single-line summary. Reject conflicting duplicates.
- [x] Determine `group` versus `command` in a second pass: a path with parsed
  child headings is a group; all others are commands. This classification is
  navigation metadata only.
- [x] Ignore ordinary prose after capturing the reviewed fields, but reject
  unexpected Markdown heading structure that could make command ownership
  ambiguous.
- [x] Require unique command paths and stable alias ownership.
- [x] Require anchors including `gh api`, `gh auth login`, `gh issue create`,
  `gh pr create`, and `gh repo create`.
- [x] Normalize and sort commands by stable ID, aliases lexically, and flags by
  long name then short name before digesting.

Hard limits:

| Item | Limit |
|---|---:|
| Source bytes | 2 MiB |
| Command depth below `gh` | 2 |
| Command paths | 512 |
| Total flags | 4,096 |
| Total aliases | 1,024 |
| Aliases per command | 32 |
| Path or flag-name bytes | 128 |
| Usage or value-hint bytes | 1,024 |
| Summary bytes | 1,024 |
| Process time | 15 seconds |

Any limit violation fails the candidate snapshot and retains the prior valid
snapshot. The parser never silently truncates knowledge.

## Cache And Compatibility Design

Validated GitHub CLI snapshots live at:

```text
<platform-user-cache>/devtize/registry/v1/gh/snapshot-<sha256>.json
```

- [x] Keep cache schema version 1. Add optional `max_flags` and `max_aliases`
  to `SyncLimits` with `omitempty` so old Git snapshot digests remain stable.
- [x] Split snapshot validation into shared envelope checks plus an explicit
  provider switch for `git` and `gh`. Keep source argv, limits, parser ID,
  command identity, provenance, and metadata rules provider-specific.
- [x] Do not introduce dynamic parser registration or a provider interface
  wider than the two proven sources need.
- [x] Retain current immutable content-addressing, restrictive permissions,
  same-directory temporary publication, reread validation, bounded loading,
  three-snapshot retention, and corrupt-newest fallback.
- [x] Load each provider independently. A malformed `gh` snapshot cannot
  invalidate Git knowledge, and a malformed Git snapshot cannot invalidate
  GitHub CLI knowledge.
- [x] Preserve valid Git cache files and their computed digests under the new
  validator. Add a checked fixture made by the shipped Phase D.1 schema.
- [x] Deduplicate by normalized provider plus command path. A reviewed builtin
  wins only its exact provider/path duplicate; no cross-provider deduplication
  occurs.

## Application And Publication Flow

1. Resolve the platform cache root and load the selected provider's last valid
   snapshot.
2. Create the provider-specific isolated temporary directory.
3. Detect the exact installed executable and version with reviewed literal
   argv.
4. Invoke the provider's one fixed reference source under hard limits.
5. Parse into an in-memory quarantined snapshot, normalize it, compute the
   source digest and content digest, and validate the complete snapshot.
6. Build one immutable `registry.knowledge.publish` local-write operation with
   provider, cache path, old/new digests, executable path, version, parser,
   exact logical source argv, counts, and limits.
7. In dry-run, return the validated plan and remove temporary state without
   cache or history writes.
8. In apply mode, require exact `sync` confirmation.
9. Re-run the provider version probe in the same isolation policy and reload
   the provider cache. Refuse publication if executable path, version, or old
   cache digest changed.
10. Atomically publish, reload, and verify digest plus command, flag, and alias
    counts.
11. Record the existing redacted global `registry.sync` history shape with
    provider `gh` and safe counts. Never store raw help, command summaries,
    flag descriptions, environment data, auth data, or user config.

The application layer may use a small explicit provider specification selected
by a `git`/`gh` switch. Provider-specific invocation and parsing stay in their
adapters. Do not create a generalized provider plugin framework.

## Search And Ranking

- Search exact command paths first, then exact aliases, then exact flag names,
  complete token matches, prefix matches, and conservative fuzzy matches.
- Reviewed builtins retain source priority over synced entries at equal score.
- An exact command path always outranks a description or flag match regardless
  of source.
- Group results remain searchable but rank below an equally matching leaf
  command unless the query exactly names the group path.
- Flag descriptions may aid ranking, but the result must report that a flag
  matched and must not imply that Devtize can execute it.
- Stable ties are source priority, provider ID, and stable knowledge ID.
- Provider filtering occurs before scoring and does not load or run a tool.
- Low-confidence output returns multiple candidates and never invents missing
  usage, flags, aliases, effects, or version compatibility.

## Ordered Implementation Tasks

### 1. [x] Reconfirm Baseline And Freeze Compatibility Fixtures

Dependencies: Phase D.1 complete.

Affected files:

- `docs/plans/phase-d-gh-sync.md`
- existing Git sync/search/doctor tests and goldens
- new legacy cache fixtures under `testdata/registry/`

Work:

- Read `AGENTS.md`, `MASTER_IDE_PROMPT.md`, this plan, and the Phase D.1 plan.
- Confirm a clean or understood worktree and identify unrelated user changes.
- Run the existing focused sync/search/doctor tests before editing.
- Capture current `dvz sync git`, `find`, `doctor`, help, and JSON behavior as
  compatibility tests where coverage is not already exact.
- Check in a schema-version-1 Git snapshot fixture generated by the shipped
  Phase D.1 shape and assert its digest still validates.

Completion evidence:

- Existing behavior is represented by tests that fail on unintended Git or
  public JSON changes.

### 2. [x] Add Typed Discovery Metadata Without Breaking Old Digests

Dependencies: Task 1.

Affected packages/files:

- `internal/registry/registry.go`
- `internal/registry/cache.go`
- `internal/registry/*_test.go`

Work:

- Add command kind, usage, and `FlagKnowledge` as optional typed fields.
- Add optional flag/alias limits to `SyncLimits`.
- Add shared validation for bounded discovery metadata.
- Prove builtin JSON and old Git snapshot canonical bytes/digests are
  unchanged when new fields are absent.

Completion evidence:

- Old fixtures load and digest identically; malformed new fields are rejected.

### 3. [x] Make Snapshot Validation Explicitly Provider-Specific

Dependencies: Task 2.

Affected packages/files:

- `internal/registry/cache.go`
- `internal/registry/cache_test.go`

Work:

- Separate shared snapshot envelope validation from Git and GitHub CLI source
  validation.
- Preserve every Phase D.1 Git invariant.
- Add the reviewed `gh` source argv, parser, depth, count, provenance, identity,
  and discovery-only metadata rules.
- Keep loader, publisher, retention, and content-address logic provider-neutral
  only where behavior is already proven identical.

Completion evidence:

- Git and `gh` snapshots validate independently; an unknown provider and any
  cross-provider source metadata are rejected.

### 4. [x] Implement The GitHub CLI Reference Adapter And Parser

Dependencies: Tasks 2 and 3.

Affected packages/files:

- `internal/adapters/github/reference.go`
- `internal/adapters/github/reference_test.go`
- `internal/adapters/github/testdata/`

Work:

- Implement the exact isolated `gh help reference` command specification.
- Parse headings, summaries, usage, aliases, flags, groups, and leaf commands
  according to this plan.
- Record parser ID/version, source argv/digest, exact tool version, and capture
  time.
- Add at least two official-version fixtures with documented origin and digest.
  Include the isolated `gh 2.93.0` fixture and one older layout fixture.
- Add fuzz tests for arbitrary reference bytes.

Completion evidence:

- Fixtures produce deterministic normalized inventories and hostile/truncated
  input cannot escape bounds or create authority metadata.

### 5. [x] Generalize The Existing Sync Service Only As Far As Two Providers

Dependencies: Tasks 3 and 4.

Affected packages/files:

- `internal/app/sync.go`
- `internal/app/sync_test.go`
- `internal/app/error.go` only if a stable existing code needs a safe hint

Work:

- Replace Git-specific branching and messages with a small explicit provider
  selection for `git` and `gh`.
- Preserve the existing `SyncResponse` and add optional alias/flag counts.
- Reuse immutable planning, exact confirmation, stale-plan checks,
  publication, postcondition verification, and global history.
- Keep provider adapters responsible for argv and parsing.
- Ensure errors name the selected provider without exposing paths, config, or
  raw reference text unnecessarily.

Completion evidence:

- Service tests cover Git compatibility and the full `gh` dry-run, decline,
  stale-plan, success, and failure lifecycle.

### 6. [x] Load And Merge Independent Provider Snapshots

Dependencies: Tasks 3 and 5.

Affected packages/files:

- `cmd/dvz/root.go`
- `internal/registry/cache.go`
- focused root/cache/catalog tests

Work:

- Load bounded Git and `gh` cache directories independently at composition.
- Merge all valid discovery knowledge with builtins by provider/path.
- Preserve provider-scoped warnings and continue when either cache is absent or
  invalid.
- Wire `dvz sync gh` while keeping exactly-one-provider argument validation.

Completion evidence:

- A process with valid Git plus corrupt `gh`, and the inverse, retains every
  valid result and reports only the affected provider warning.

### 7. [x] Add Provider-Filtered, Flag-Aware Offline Search

Dependencies: Tasks 2 and 6.

Affected packages/files:

- `internal/search/search.go`
- `internal/search/search_test.go`
- `internal/app/find.go`
- `internal/app/find_test.go`
- `cmd/dvz/root.go`

Work:

- Add the provider filter and additive response metadata.
- Extend deterministic scoring with aliases, flags, command kind, and matched
  field evidence.
- Keep exact paths and reviewed builtins at the documented priorities.
- Retain the no-runner architecture and regression spy.

Completion evidence:

- Representative queries find `gh pr create`, `gh auth login`, and exact flag
  owners with stable reasons; filtering never invokes a process.

### 8. [x] Add Per-Provider Doctor State

Dependencies: Task 6.

Affected packages/files:

- `internal/app/doctor.go`
- `internal/app/app_test.go` and/or `internal/app/doctor_test.go`
- `cmd/dvz/root.go`

Work:

- Compare each loaded snapshot with its already detected installation.
- Add sorted provider details while retaining aggregate schema-version-1
  fields.
- Define aggregate status deterministically: invalid or stale providers warn;
  absent optional snapshots do not make tool readiness fail.

Completion evidence:

- Exact, stale, absent, invalid, missing-tool, and mixed-provider states are
  covered without cache writes or extra tool calls.

### 9. [x] Complete Terminal And JSON UX

Dependencies: Tasks 5, 7, and 8.

Affected packages/files:

- `internal/ui/renderer.go`
- `internal/ui/renderer_test.go`
- `internal/ui/testdata/`
- `testdata/golden/`
- `cmd/dvz/root_test.go`
- `integration/cli_test.go`

Work:

- Render provider, exact version, source, command/group count, alias count,
  flag count, limits, cache transition, risk, effect, and warnings.
- Keep compact aligned output at 60, 80, and 120 columns with ASCII fallback,
  no-color support, redirected stability, and no spinner/pager.
- Do not dump hundreds of commands in the sync result. Show exact counts and a
  small deterministic anchor sample; `find` is the inspection interface.
- Keep JSON ANSI-free and prompts on stderr in JSON mode.

Completion evidence:

- Goldens cover Git compatibility plus `gh` plan/result, dry-run, errors,
  provider-filtered find, and mixed doctor state.

### 10. [x] Prove Isolation, Bounds, And Non-Execution End To End

Dependencies: Tasks 4 through 9.

Affected files:

- adapter, service, cache, search, CLI, and integration tests
- fake `gh` fixtures/executables under testdata as locally established

Work:

- Assert exact executable path, argv, directory, environment allowlist,
  overlays, timeout, capture limit, and disabled stdin.
- Test hostile aliases, extension names, headings, flag text, usage syntax,
  ANSI, control bytes, duplicate paths, duplicate flags, depth/count/byte
  limits, timeout, truncation, malformed Markdown, and missing anchors.
- Verify token and auth environment variables never reach the fake runner.
- Verify no discovered path, alias, or flag is invoked.
- Verify dry-run, decline, parse failure, and validation failure leave cache,
  history, repositories, and remotes byte-for-byte unchanged.
- Run integration tests with fake `gh` only; ordinary CI requires no auth or
  network and creates no GitHub resources.

Completion evidence:

- Safety tests would fail on shell use, config leakage, extension execution,
  partial publication, cross-provider corruption, or mutation from `find`.

### 11. [x] Update Documentation And Compatibility Notes

Dependencies: Tasks 5 through 10 behavior finalized.

Affected files:

- `README.md`
- `docs/architecture.md`
- `docs/safety.md`
- `docs/registry.md`
- `docs/configuration.md`
- `docs/persisted-history.md`
- `docs/releases.md`
- `docs/plans/phase-d-gh-sync.md`
- command help goldens

Work:

- Document exact local source commands, cache paths, support levels, provider
  filter, flags/aliases as non-authoritative metadata, isolation, and limits.
- Explain that `gh` can be workflow-ready for a narrow reviewed capability set
  while synchronized command knowledge remains merely discoverable.
- Document that user aliases, extensions, auth state, and network docs are
  deliberately excluded.
- Record additive schema-v1 fields and unchanged Git snapshot compatibility.
- Keep roadmap features visibly unimplemented.

Completion evidence:

- Documentation describes only verified behavior and contains no live account,
  token, personal path, or inflated provider claim.

### 12. [x] Run Full Verification And Prepare A Maintainer Ship Command

Dependencies: Tasks 1 through 11.

Run:

```text
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/dvz
git diff --check
```

Also exercise an isolated built binary with fake provider state:

```text
dvz sync git --dry-run
dvz sync gh --dry-run
dvz --json sync gh --dry-run
dvz find --provider gh create pull request
dvz --json find --provider gh auth login
dvz doctor
```

Work:

- Run focused parser/cache/search/service/CLI tests before the full suite.
- Confirm no test used external credentials, provider APIs, or real remote
  writes.
- Review the final diff for shell interpolation, unbounded reads, leaked auth
  environment, capability promotion, stale docs, and accidental phase growth.
- Mark a task or acceptance criterion complete only after its implementation
  and verification pass.
- Build the final `dvz` binary and provide the maintainer an exact `dvz ship`
  command with message `feat(sync): add version-aware GitHub CLI knowledge`.
  The implementation agent must not commit or push automatically.

Completion evidence:

- Every command above passes, all acceptance criteria are checked, and any
  skipped platform or optional real-binary observation is reported explicitly.

## Test Matrix

### Parser And Adapter

- [x] At least two official `gh` version fixtures retain exact provenance and
  deterministic normalized output.
- [x] The isolated `gh 2.93.0` fixture asserts its complete command and flag
  inventory, including direct commands, nested commands, groups, aliases,
  usage arguments, and unusual value hints.
- [x] Parser fuzzing never panics or exceeds configured bounds.
- [x] Exact runner tests cover literal argv, isolation, environment, timeout,
  capture, truncation, stderr, cancellation, and missing executable behavior.

### Cache And Compatibility

- [x] Old Git snapshots keep their exact digest and load successfully.
- [x] Git and `gh` first publish, unchanged publish, version change, retention,
  corrupt-newest fallback, bounded scan, permissions, and concurrent reads are
  covered.
- [x] Provider/source/parser swaps and authority-bearing metadata are rejected.
- [x] Failed `gh` sync retains its own prior valid snapshot and never touches
  the Git cache.

### Search And Doctor

- [x] Exact path, alias, flag, token, prefix, fuzzy, group/leaf, source-priority,
  provider-filter, and stable-tie fixtures pass.
- [x] Builtin Git behavior and existing result ordering remain covered.
- [x] `find` performs zero runner calls with any cache combination.
- [x] Doctor covers exact, stale, absent, invalid, missing, unsupported, and
  mixed Git/`gh` states.

### Application And CLI

- [x] Dry-run, decline, cache race, executable/version race, timeout, malformed
  source, publish failure, postcondition failure, redaction, and history are
  tested for `gh`.
- [x] Git sync response and golden compatibility tests remain green.
- [x] Human output is tested at 60, 80, and 120 columns, color/no-color,
  Unicode/ASCII, and redirected output.
- [x] JSON success, dry-run, errors, find filtering, and doctor provider state
  are versioned, stable, and ANSI-free.
- [x] CLI integration uses fake tools and isolated config/cache/state roots and
  proves no repository or remote mutation.

## Risks And Mitigations

- **Reference format drift:** version the provider parser, require structural
  anchors, use two-version fixtures, fail closed, and retain the previous valid
  snapshot.
- **User alias or extension leakage:** use an empty isolated home/config/state,
  inherit no auth or `GH_*` values, and invoke only `help reference`.
- **False execution authority:** keep all parsed fields in `CommandKnowledge`,
  force `discoverable` plus `unclassified`, and reject capability/adapter data
  in snapshots.
- **Old cache digest breakage:** make every added persisted field optional and
  lock the shipped Git snapshot bytes and digest in a regression fixture.
- **One corrupt provider disabling discovery:** load, validate, warn, and merge
  each provider independently.
- **Parser memory or CPU abuse:** enforce process bytes/time plus parser counts,
  field sizes, depth, and no-silent-truncation rules before publication.
- **Search noise from hundreds of entries:** add explicit provider filtering,
  exact-path/alias/flag ranking, group/leaf tie behavior, and stable low-
  confidence alternatives.
- **Sensitive help text or environment disclosure:** inherit no credentials,
  retain no raw help in history, bound diagnostics, and pass errors through
  existing redaction.
- **Premature generic architecture:** use an explicit two-provider selection
  and extract shared code only where Git and `gh` now demonstrate identical
  behavior.

## Measurable Acceptance Criteria

- [x] Confirmed `dvz sync gh` publishes a validated content-addressed snapshot
  from exactly `gh help reference` and records sanitized global history.
- [x] `dvz sync gh --dry-run` performs complete detection and validation but
  leaves cache, history, repository state, and remotes unchanged.
- [x] Offline `dvz find --provider gh` discovers version-matched built-in
  GitHub CLI command paths, aliases, usage, and flags without invoking `gh`.
- [x] Existing `dvz sync git`, old Git cache digests, builtin search results,
  and schema-version-1 public fields remain compatible.
- [x] Git and GitHub CLI caches coexist, and corruption or staleness in one is
  isolated and visibly reported without discarding the other.
- [x] No user alias, installed extension, auth token, custom host, pager,
  editor, browser, shell, network documentation source, discovered command, or
  provider API is invoked.
- [x] No introspected command, alias, argument, or flag can become a capability,
  operation, argv, adapter binding, or authorization decision.
- [x] Malformed, partial, duplicate, oversized, over-depth, truncated, or
  structurally incompatible reference data never replaces a valid snapshot.
- [x] Every cache write is represented by an immutable digest-bound local-write
  plan with exact confirmation, live precondition revalidation, postcondition
  verification, and redacted history.
- [x] Human output remains readable across terminal widths and capabilities;
  JSON is stable, versioned, detailed, and ANSI-free.
- [x] Normal CI passes without network access, GitHub authentication, external
  writes, or real repository creation.
- [x] Formatting, all tests, race tests, vet, build, diff checks, and the fake-
  provider acceptance workflow pass on the final tree.
- [x] README and focused docs accurately distinguish synchronized discovery
  from Devtize's narrow reviewed GitHub execution capabilities.

## Phase Boundary

Phase D.2 ends with two local-help discovery providers sharing a validated
snapshot/publication contract while retaining provider-owned invocation and
parsing. It does not complete runtime/package-manager detection or the broader
provider-sync roadmap.

The next milestone should be chosen separately after usability evidence from
Git plus GitHub CLI search is reviewed. Reasonable candidates are runtime and
package-manager detection, `dvz explain` over existing offline knowledge, or a
third discovery provider. None is authorized by this plan.
