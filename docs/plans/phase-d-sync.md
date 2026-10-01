# Phase D.1: Version-Aware Git Knowledge Sync

## Goal

Implement the first provider-sync vertical slice:

```text
dvz sync git
dvz sync git --dry-run
dvz --json sync git [--dry-run]
```

The command detects the installed Git version, reads the official built-in
command inventory, validates it in quarantine, and publishes a versioned local
knowledge snapshot. `dvz find` then searches reviewed built-ins plus the last
valid snapshot without invoking Git. Synced knowledge remains discovery-only
and can never become an executable capability.

This slice intentionally stops at depth one. It does not invoke discovered
commands, parse flags or nested subcommands, read aliases, run external
`git-*` executables, use network documentation, or add another provider.

## Public Contract

- [x] Add `dvz sync git`; no provider or an unsupported provider returns
  `INVALID_USAGE`.
- [x] `--dry-run` performs detection, bounded introspection, parsing, and
  validation but writes no cache or history.
- [x] A normal sync renders an immutable local-write plan and requires the
  digest-bound word `sync` before publishing the cache.
- [x] Global `--json` returns a schema-version-1 `SyncResponse` with provider,
  installation, parser, source argv, limits, cache-before/cache-after state,
  parsed and published counts, status, warnings, plan, and result. JSON never
  contains ANSI or prompts; any required prompt is written to stderr.
- [x] EOF or any response other than `sync` returns
  `CONFIRMATION_DECLINED` and performs no cache write.
- [x] Missing or unsupported Git retains the existing tool error codes.
  Introspection, parsing, validation, and cache publication failures return
  `SYNC_FAILED` with a redacted actionable hint.
- [x] Human output uses the existing adaptive renderer, textual progress, and
  exact counts. It uses no banner, spinner, cursor animation, or pager.

## Data And Trust Model

### Git Source

- [x] Detect Git with the reviewed `git --version` path and preserve resolved
  executable path, exact parsed version, and detection time.
- [x] Invoke exactly:

  ```text
  git help --all --no-external-commands --no-aliases --verbose
  ```

- [x] Run in an isolated temporary home/config directory with disabled stdin,
  `LC_ALL=C`, `GIT_CONFIG_NOSYSTEM=1`, pager variables disabled, a 10-second
  timeout, and a 1-MiB capture limit. Reject truncated output.
- [x] Never invoke a command name discovered from help. Repository files,
  aliases, external commands, hooks, pagers, and user Git configuration must
  not influence the inventory.

### Parser And Limits

- [x] Add a versioned Git-help parser that accepts only command rows from the
  reviewed command sections: main porcelain, ancillary command groups,
  interacting-with-others, and low-level command groups. Exclude repository,
  file-format, protocol, and interface documentation sections.
- [x] Accept only lowercase command names matching
  `[a-z0-9][a-z0-9-]*`, non-empty single-line descriptions, and unique command
  paths. Reject malformed or conflicting rows instead of guessing.
- [x] Enforce depth `1`, at most `256` commands, at most `1 MiB` input, at most
  `128` bytes per command name, and at most `1,024` bytes per description.
- [x] Require a main-porcelain section and anchor commands including
  `git status`; an empty or structurally incompatible inventory fails sync.
- [x] Generate stable IDs as `sync.git.<command>`, exact version metadata, and
  source provenance. Assign synced entries risk `unclassified` and effect
  `Discovery-only help metadata; effects are not reviewed.` This value is
  valid only for discovery knowledge and remains invalid for operation plans.

### Cache Schema

- [x] Store cache data beneath
  `<platform-user-cache>/devtize/registry/v1/git/` with directory mode `0700`
  and files mode `0600` where supported.
- [x] Use immutable, content-addressed
  `snapshot-<sha256>.json` files. Write a same-directory temporary file,
  encode, flush, close, validate by rereading, then rename to its previously
  nonexistent digest path. Loader ignores temporary files.
- [x] Define a strict schema-version-1 provider snapshot containing:
  last observed installation; last sync attempt; last valid knowledge version;
  parser ID/version; exact source argv; capture timestamp and digest; enforced
  limits; sorted normalized commands; and safe failure metadata.
- [x] Keep at most three valid snapshots after publishing a new one. Cleanup
  happens only after the new snapshot is visible; cleanup failure is a warning,
  not corruption of the successful sync.
- [x] On failed sync, publish at most a status snapshot that reuses the previous
  validated command set and records the newly observed installation/failure.
  Never replace valid commands with partial parser output.
- [x] Loader scans at most eight snapshot files and 8 MiB total, validates
  schema, filename/content digest, provider, ordering, uniqueness, provenance,
  and limits, then deterministically selects the newest valid snapshot.
  Corrupt snapshots are ignored with diagnostics; reviewed built-ins always
  remain available.
- [x] A cache is `exact` when its knowledge version matches the last observed
  installation, `stale` when they differ, `not_synced` when no knowledge exists,
  and `invalid` only when files exist but no valid snapshot can be loaded.

## Registry And Search Integration

- [x] Extend `KnowledgeSource` additively with tool version, parser version,
  capture time, and source digest while preserving existing built-in JSON.
- [x] Validate `builtin` and `sync` provenance separately. Synced entries must
  remain `discoverable`; no cache field may name an adapter, validator, or
  executable capability.
- [x] Merge reviewed built-ins with synced knowledge by normalized command
  path. A reviewed built-in wins and the duplicate synced row is omitted.
- [x] Rank by score, then source priority (`builtin` before `sync`), then stable
  ID. Preserve all existing built-in results and JSON fields.
- [x] Report `exact` or `stale` only on synced results; built-in results retain
  their current compatibility behavior.
- [x] Add additive registry status/warnings to `FindResponse`. A malformed or
  absent cache never prevents offline built-in search.
- [x] Preserve the invariant that `dvz find` has no process-runner dependency
  and executes zero subprocesses.
- [x] Extend `dvz doctor` to compare its live Git detection with loaded cache
  metadata and report `builtin_only`, `synced_exact`, `synced_stale`, or
  `cache_invalid` without modifying the cache.

## Application And Publication Flow

1. [x] Resolve the platform cache path and load the last valid Git snapshot.
2. [x] Detect Git and create an isolated temporary introspection directory.
3. [x] Run the one reviewed help command under the hard limits.
4. [x] Parse into an in-memory quarantine snapshot, normalize and sort it, and
   validate the complete schema and digest.
5. [x] Build an immutable `registry.knowledge.publish` operation containing
   provider, cache path, old/new digests, tool version, parser version, source
   argv, counts, limits, risk `local_write`, and cache-only effects.
6. [x] For dry-run, render/return the validated plan and delete quarantine
   state without cache or history writes.
7. [x] For apply, require exact `sync` confirmation, then rerun `git --version`.
   If executable path or version changed, return `PRECONDITION_FAILED` and
   publish nothing.
8. [x] Atomically publish the immutable snapshot, reread it, and verify its
   digest and command count before reporting success.
9. [x] Record a redacted global `registry.sync` history entry with no project
   root, raw help output, environment, or command descriptions. Record only
   provider, versions, digests, counts, limits, approval, and result.

Keep the implementation narrow:

- `internal/app`: `SyncService`, response, plan, policy, and history coordination.
- `internal/adapters/git`: fixed help invocation and parser for Git output.
- `internal/registry`: snapshot schema, validation, immutable storage, merge,
  provenance, and cache status.
- `cmd/dvz` and `internal/ui`: Cobra wiring plus human/JSON rendering.

Do not create a generic provider plugin system, workflow DSL, network fetcher,
or empty provider directories in this slice.

## Test Matrix

- [x] Parser unit and golden tests cover Apple Git and upstream Git layouts,
  category filtering, stable sorting, duplicate names, malformed rows, unknown
  sections, long fields, count limits, truncation, and empty output.
- [x] Fuzz the help parser and snapshot decoder with arbitrary untrusted bytes.
- [x] Fake-runner tests assert exact executable, argv, directory, timeout,
  disabled stdin, environment allowlist/overlay, capture limit, and zero calls
  to discovered commands.
- [x] Cache tests cover first publish, unchanged sync, version change,
  failed-parse preservation, content-address verification, corrupt newest
  fallback, bounded scans, temporary-file ignoring, retention, permissions,
  cleanup warnings, and concurrent readers during publication.
- [x] Search tests prove built-ins outrank synced entries, ties are stable,
  stale/exact labels are correct, cache warnings are additive, and `find`
  invokes no runner.
- [x] Service tests cover dry-run, declined confirmation, stale plan after Git
  changes, missing Git, timeout, truncated output, invalid cache, publish
  failure, post-publish verification failure, redaction, and global history.
- [x] CLI goldens cover help, 60/80/120-column human plans/results/warnings,
  redirected ASCII, color/no-color, JSON success, JSON dry-run, and stable
  errors. Color is never the only status signal.
- [x] Integration tests use a fake Git executable with controlled version/help
  output and isolated cache/config/history roots. They verify cache reuse by a
  later `dvz find`, failed-sync fallback, no network access, no repository
  mutation, and byte-for-byte dry-run/decline behavior.
- [x] Run `gofmt` check, `go test ./...`, `go test -race ./...`, `go vet ./...`,
  `go build ./cmd/dvz`, and `git diff --check`.

## Documentation

- [x] Update README command examples, support matrix, local-first guarantees,
  troubleshooting, and roadmap claims.
- [x] Update architecture, safety/threat model, registry/provider authoring,
  configuration/state paths, persisted schemas/migrations, and release notes.
- [x] Document cache deletion as safe loss of derived discovery data, while
  making clear that Devtize never deletes it automatically to repair errors.
- [x] Document that Git sync is local help introspection only and performs no
  network documentation access.
- [x] Leave `gh`, language/package-manager providers, nested command help,
  flags, network docs, executable capability generation, AI, TUI, and MCP as
  explicit later work.

## Risks And Mitigations

- **Help output drift:** version the parser, require structural anchors, reject
  partial output, and retain the previous valid commands.
- **Untrusted configuration or PATH content:** isolate Git config/home, exclude
  aliases and external commands, validate resolved Git, and never invoke a
  discovered name.
- **Cache corruption or interrupted writes:** publish immutable
  content-addressed snapshots and choose only fully validated files.
- **False authority:** mark risk/effects unreviewed, keep support discovery-only,
  and omit all adapter/capability fields from the cache schema.
- **Search regression:** deduplicate by path, prioritize built-ins, preserve
  existing output fields, and retain no-runner regression tests.
- **Unbounded local data:** enforce process, parser, file-count, snapshot-count,
  per-file, and total-scan limits before allocation or publication.

## Acceptance Criteria

- [x] `dvz sync git --dry-run` produces a complete validated plan and performs
  zero cache/history/repository mutations.
- [x] Confirmed `dvz sync git` publishes one validated content-addressed
  snapshot and records sanitized global history.
- [x] `dvz find` discovers synced Git commands offline while reviewed built-ins
  retain priority and existing behavior.
- [x] A changed installed version is visibly stale until a matching snapshot
  succeeds; failed sync retains the previous valid commands.
- [x] Corrupt, oversized, incompatible, or partial cache data never replaces
  built-ins or becomes executable.
- [x] No alias, external Git command, pager, hook, shell, network source, or
  discovered command is invoked.
- [x] JSON is versioned, stable, and ANSI-free; human output remains readable
  across terminal capabilities and redirected output.
- [x] Every mutation is an immutable digest-bound cache-publication plan with
  exact confirmation, precondition revalidation, postcondition verification,
  and redacted history.
- [x] Full automated checks and fake-Git acceptance workflows pass on the final
  tree before any checkbox is marked complete.

## Phase Boundary

Phase D.1 proves one provider and one official depth-one source. The next slice
may add nested Git help only after a separate reviewed safe-invocation design.
Additional providers begin with detection and discovery and reuse the validated
snapshot contract; they do not inherit Git parsing rules. Provider sync never
grants execution permission.
