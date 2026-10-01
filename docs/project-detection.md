# Project Detection

Phase D.3 strengthens the original Phase A `internal/detect` path. It is a
bounded, read-only input to `dvz doctor`; there is no second detector and no new
command.

## Roots And Bounds

The project root is the nearest directory containing a safe recognized marker:

```text
.git
go.mod
package.json
tsconfig.json
deno.json
deno.jsonc
deno.lock
pnpm-workspace.yaml
bun.lock
bun.lockb
pnpm-lock.yaml
yarn.lock
package-lock.json
```

Non-Git markers must be regular non-symlink files. The nearest enclosing
workspace is a directory with non-empty `package.json` workspace metadata or
`pnpm-workspace.yaml`. Traversal stops at the enclosing Git root, otherwise at
the filesystem root. Detection inspects at most 64 ancestors and 16 manifests,
reads at most 1 MiB per manifest, and never expands workspace patterns or
visits workspace members.

Evidence paths use forward slashes and are relative to a distinct workspace
root when one exists, otherwise to the project root. Unsafe markers and
bounded-input failures are reported through stable diagnostics rather than
treated as project authority.

## Manifest And Runtime Evidence

`package.json` must be exactly one complete top-level JSON object; trailing
values, malformed content, excessive nesting, and oversized input are rejected
with safe diagnostics. Arbitrary manifest fields and workspace globs are not
returned to the caller.

- Any safe `package.json` supplies generic JavaScript evidence but never npm.
- Non-empty `engines.node` supplies explicit Node evidence.
- Non-empty `engines.bun`, Bun `packageManager` metadata, or either Bun lock
  supplies Bun evidence.
- `deno.json`, `deno.jsonc`, or `deno.lock` supplies Deno evidence.
- `tsconfig.json` supplies TypeScript-flavored JavaScript evidence without
  selecting a runtime executable.

Runtime labels are distinct and sorted. They describe files and metadata, not
installed tools or versions.

## Package-Manager Resolution

Only `npm`, `pnpm`, `yarn`, and `bun` are accepted in a bounded, safe
`name@version` `packageManager` value. The version is retained as opaque display
metadata. Resolution order is:

1. valid project-root metadata;
2. valid workspace-root metadata;
3. `pnpm-workspace.yaml`;
4. project-root locks, then workspace-root locks;
5. within one scope: `bun.lock`, `bun.lockb`, `pnpm-lock.yaml`, `yarn.lock`,
   then `package-lock.json`.

Matching evidence for one manager is not a conflict. Distinct managers,
invalid controlling metadata, or different project/workspace metadata versions
make the result ambiguous and low-confidence. The higher-precedence value is
still shown for diagnosis, alongside rejected alternatives and a deterministic
resolution hint. Valid unambiguous metadata is high-confidence; one workspace
marker or lockfile is medium-confidence. A plain `package.json` remains
unresolved and receives an informational hint.

## Security And Support Boundary

Detection performs filesystem reads only. It does not invoke `node`, `bun`,
`deno`, `npm`, `pnpm`, `yarn`, Corepack, package scripts, or lifecycle hooks.
It does not install dependencies, read network documentation, synchronize
command knowledge, or write project/configuration/cache/history state.

Accordingly, the support matrix labels JavaScript/TypeScript project and
package-manager evidence as `detected`. Executable discovery, executable
versions, dependency setup, lifecycle-script policy, and trusted capabilities
remain planned work.
