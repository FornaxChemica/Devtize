# Registry And Search

The registry ships twelve reviewed Git command knowledge entries. Each entry has a stable knowledge ID, provider and command path, summary, aliases, intent phrases, examples, version note, risk/effect metadata, discoverable support level, source locator, and corpus digest. Phase D.2 can supplement them with independent validated local snapshots from the exact installed Git and GitHub CLI versions.

Command knowledge is not a capability. It has no validator, adapter binding, execution function, or permission grant. `dvz find` can render it but cannot run it.

Search normalizes case and tokens, then ranks exact command paths, exact aliases, exact flag names, complete token matches, prefix matches, and conservative fuzzy matches. Exact paths outrank description or flag matches; leaf commands normally outrank equally matching groups. Scores use source priority, provider ID, and stable ID ordering for ties. A reviewed builtin wins only an exact provider/path duplicate. Builtins retain `not_checked` compatibility status. Synced results report `exact` or `stale` and include tool/parser/capture provenance and matched-field evidence. `--provider git|gh` filters before scoring. Search does not run tool detection or any provider process.

Phase B also has trusted capabilities implemented in source code, not inferred from command knowledge:

- `git.repo.inspect`
- `git.repo.init`
- `git.index.stage`
- `git.commit.create`
- `git.remote.inspect`
- `git.remote.configure`
- `git.branch.push`
- `github.auth.inspect`
- `github.repo.inspect`
- `github.repo.create`
- `github.repo.description.update`

These capabilities are available only through the typed `dvz repo` application paths. `github.repo.description.update` is restricted to `repo set-description`, requires remote-write confirmation for the exact plan digest, and verifies the remote value after mutation. The capabilities have typed inputs, risk metadata, adapter bindings, plan rendering, confirmation policy, and tests. The recovery-only `git.index.untrack` and `git.commit.amend_initial` capabilities are reachable solely through `--repair-unpushed-initial` after its unpublished-history preconditions pass. `git.remote.update` is limited to changing protocol for two canonical URLs that identify the same GitHub repository. Help text or synced knowledge cannot promote itself into this list.

`git.commit.amend_initial_preserve_message` and `git.branch.force_push_with_lease` are additionally restricted to `repo redact-initial`. The latter schema requires the exact expected remote commit and always renders `--force-with-lease=refs/heads/<branch>:<sha>`; no plain-force capability exists.

Phase C reuses the reviewed `git.index.stage` and `git.commit.create` capabilities through `dvz commit`. That path requires a clean index, exact changed-path disclosure, content digests, Conventional Commit validation by default, and postcondition verification. It does not make command knowledge executable.

`dvz ship` reuses `git.branch.push` only after live remote inspection, fast-forward ancestry verification, exact outgoing-commit disclosure, and remote-write confirmation. Its adapter uses `git push <remote> refs/heads/<branch>:refs/heads/<branch>` and cannot request force.

Phase C adds four reviewed executable check capabilities: `go.format.check`, `go.test`, `go.vet`, and `go.build`. They are source-owned definitions with fixed adapter bindings, arguments, risks, effects, timeouts, and output bounds. Configuration may select these IDs but cannot supply executable names, arguments, environment values, or shell text. Unknown and duplicate IDs are rejected.

`dvz status` uses reviewed read-only adapter methods for repository state, exact change categories, local tracking relation, and optional live branch inspection. The live check is explicit, invokes `git ls-remote --heads`, and never fetches. `dvz history` does not execute provider commands.

`git.commit.uncommit_preserve_changes` is a reviewed planner-only compensation for a verified `git.commit.create` transition. Its intended future behavior is a narrowly bound mixed reset to the verified parent, classified `local_write`. It is currently `planned`, has no executable adapter binding, and can appear only in an eligible `dvz undo ... --dry-run` plan. History cannot register or select another capability.

## Provider Knowledge Sync

`dvz sync git` detects the installed version and invokes exactly:

```text
git help --all --no-external-commands --no-aliases --verbose
```

The versioned parser accepts only depth-one rows from main porcelain, ancillary, interacting-with-others, and low-level sections. It requires `git status`, rejects malformed/duplicate/oversized inventories, caps output at one MiB and commands at 256, and marks every generated row `discoverable` with risk `unclassified`. It does not parse aliases, flags, nested help, repository/file-format docs, or network sources.

`dvz sync gh` detects GitHub CLI 2.0.0 or newer and invokes exactly:

```text
gh help reference
```

Its provider-owned parser requires `# gh reference`, accepts only bounded
`## gh ...` and `### gh ...` command paths, and safely excludes deeper command
sections. It validates summaries, display-only usage, fully qualified official
aliases, and fixed-column long/short flag metadata. Required anchors include
`gh api`, `gh auth login`, `gh issue create`, `gh pr create`, and
`gh repo create`. Limits are two MiB, 512 accepted command paths, 4,096 flags,
1,024 aliases, and 15 seconds. User aliases and extensions are not loaded.

Validated snapshots live at `<platform-user-cache>/devtize/registry/v1/<provider>/snapshot-<sha256>.json`. Files are strict schema version 1, content-addressed, immutable, loaded independently under file/count/byte bounds, and retained three deep after successful publication. Temporary, corrupt, oversized, and incompatible files are ignored with provider-scoped diagnostics. The newest valid snapshot per provider is used; reviewed builtins and another provider remain available when one cache is invalid. Phase D.1 Git snapshots preserve their canonical bytes and digest.

Cache deletion is a safe loss of derived discovery data. It does not affect configuration, history, repositories, or trusted capabilities, and a later confirmed sync can rebuild it. Devtize does not delete invalid cache data automatically. GitHub CLI flags, aliases, summaries, and usage are display/search metadata only: they cannot name or become capabilities, adapters, validators, argv, environment, risks, or confirmation policy. The existing narrow GitHub workflow capabilities remain separately reviewed source code. Additional providers require their own reviewed parser and source design; the two explicit providers do not form a plugin contract.
