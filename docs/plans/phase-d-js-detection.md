# Phase D.3: JavaScript/TypeScript Project And Package-Manager Detection

## Status

Implementation complete and verified on 2026-10-01. The existing commit
`8c6f9c3` baseline passed `go test -count=1 ./...` before this plan was created,
and every checked item below has focused or repository-wide verification.

The master roadmap originally names runtime and package-manager work as Phase
D. Phase D.1 and D.2 are already published, so this bounded follow-up retains
the D.3 number.

## Goal And Phase Boundary

Strengthen the existing read-only project detector and `dvz doctor` so they
report bounded JavaScript/TypeScript project, runtime, package-manager, and
workspace evidence. This phase adds no command and invokes no Node ecosystem
executable. Selected tools are informational detection results, never
execution authority.

This slice includes nearest-project discovery, nearest enclosing workspace
discovery, strict package-manifest parsing, deterministic evidence and
conflict resolution, additive schema-version-1 JSON, warning semantics, human
rendering, fixtures, fuzz coverage, and built-binary read-only verification.

It explicitly excludes setup, dependency installation, lifecycle scripts,
Corepack, command-knowledge sync or builtins for Node ecosystem tools, other
language ecosystems, configuration/cache/registry/history schema changes,
network access, AI, TUI, MCP, and every mutation.

## Public Compatibility Contract

Preserve every existing `detect.Project` and evidence JSON field and schema
version 1. Add only optional project fields:

- `workspace_root`
- `package_manager_version`
- `package_manager_confidence`
- `rejected_alternatives`
- `resolution_hint`
- `diagnostics`

Add optional `scope` to evidence. Alternatives contain `kind`, `value`, and a
stable `reason`. Diagnostics contain stable `code`, `severity`, `path`, and a
safe `message`. Evidence paths are slash-normalized and relative to the
workspace root when a distinct workspace exists, otherwise the project root.
Untrusted metadata and control characters must never be reproduced raw.

## Detection Rules And Limits

- The project root is the nearest safe recognized marker. Preserve existing
  markers and add `tsconfig.json`, `deno.json`, `deno.jsonc`, `deno.lock`, and
  `pnpm-workspace.yaml`.
- Non-Git markers must be regular non-symlink files. Unsafe markers yield
  bounded diagnostics and do not become evidence.
- Search no more than 64 ancestors and inspect no more than 16 manifests.
  Read no more than 1 MiB from any manifest. Do not recurse into workspace
  members or expand workspace globs.
- A workspace is the nearest enclosing directory with non-empty
  `package.json` workspace metadata or `pnpm-workspace.yaml`. Workspace search
  includes the project root and stops at the enclosing Git root, or the
  filesystem root when no Git root is present.
- Parse `package.json` as exactly one complete top-level JSON object. Reject
  malformed, trailing, oversized, excessively nested, unreadable, or unsafe
  input without crashing or exposing raw content.
- Generic `package.json` is JavaScript evidence and never implies npm.
  `engines.node` is explicit Node evidence; Bun metadata or locks are Bun
  runtime and package-manager evidence; Deno config or lock files are Deno
  evidence; `tsconfig.json` is TypeScript-flavored JavaScript evidence without
  selecting a runtime executable.
- Accept `packageManager` only for `npm`, `pnpm`, `yarn`, or `bun` in a safe,
  bounded `name@version` form. Preserve the safe version opaquely.

Package-manager precedence is:

1. valid project-root `packageManager` metadata;
2. valid workspace-root metadata;
3. `pnpm-workspace.yaml`;
4. project-root locks, then workspace-root locks;
5. within a scope: `bun.lock`, `bun.lockb`, `pnpm-lock.yaml`, `yarn.lock`, then
   `package-lock.json`.

Matching evidence for one manager is not ambiguous. Distinct managers,
invalid controlling metadata, or conflicting project/workspace declarations
set `ambiguous: true`, reduce package-manager confidence to `low`, retain
structured rejected alternatives, and emit a deterministic resolution hint.
Unambiguous metadata is high confidence; a single workspace marker or lock is
medium. A generic JavaScript project with no manager remains unambiguous and
unresolved with an informational hint.

## Dependency-Ordered Work

1. [x] Record this phase boundary, additive public contract, precedence,
   limits, and non-goals after proving the existing baseline.
2. [x] Add filesystem fixtures for metadata-only projects, every recognized
   lock, current and legacy Bun locks, Node, Deno, TypeScript, malformed
   manifests, conflicts, and nested monorepos.
3. [x] Implement bounded manifest parsing and safe marker inspection; pass
   focused parser, size-limit, symlink, malformed-input, and fuzz tests.
4. [x] Implement deterministic root/workspace discovery and evidence ordering;
   pass monorepo, Git-boundary, traversal-limit, and cross-platform path tests.
5. [x] Implement package-manager selection, confidence, alternatives,
   diagnostics, and hints; pass the full precedence/conflict table.
6. [x] Integrate additive results into `DoctorService`; prove warning semantics
   and the exact existing Git/`gh` runner calls.
7. [x] Update human and JSON rendering; pass golden coverage for redirected
   output and representative terminal widths.
8. [x] Add built-binary tests proving read-only behavior and zero calls to
   sentinel Node, Bun, Deno, npm, pnpm, Yarn, and Corepack executables.
9. [x] Update README, architecture, safety, release notes, and focused project
   detection documentation without claiming executable/version/setup support.
10. [x] Complete final security/diff review and all applicable verification;
    mark acceptance items only when evidence exists.

## Acceptance Criteria

- [x] Metadata and every lockfile select according to the documented table;
  matching Bun locks do not conflict and distinct managers do.
- [x] Project/workspace metadata, workspace markers, and locks resolve with
  stable precedence, alternatives, confidence, diagnostics, and hints.
- [x] Package-only projects never imply npm and remain informational.
- [x] Runtime evidence is distinct, bounded, safe, and stably sorted.
- [x] Nested monorepos, nearest workspaces, Git boundaries, scan limits,
  symlinks, special files, unreadable inputs, and missing projects fail safely.
- [x] Ambiguity and warning diagnostics make doctor a warning; unresolved
  JavaScript evidence remains a valid informational result.
- [x] Doctor invokes exactly its existing Git and `gh` checks and no ecosystem
  command.
- [x] Human and JSON output are deterministic, schema-version-1, ANSI-free
  where required, additive, and control-character safe.
- [x] Built-binary tests prove project/config/cache/history state is unchanged
  and all ecosystem sentinel executables receive zero calls.
- [x] README and focused docs distinguish detected project evidence from
  planned executable/version detection and setup support.
- [x] Final formatting, uncached normal and race tests, vet, build, diff check,
  and worktree review pass with every skipped check disclosed.

## Final Verification

```text
test -z "$(gofmt -l .)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build ./cmd/dvz
git diff --check
git status --short
```

The final review must explicitly check for unbounded reads, symlink traversal,
raw untrusted strings, accidental ecosystem subprocesses, unsupported support
claims, unrelated edits, and checked boxes without corresponding evidence.
