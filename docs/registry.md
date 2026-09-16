# Registry And Search

The registry ships twelve reviewed Git command knowledge entries. Each entry has a stable knowledge ID, provider and command path, summary, aliases, intent phrases, examples, version note, risk/effect metadata, discoverable support level, source locator, and corpus digest.

Command knowledge is not a capability. It has no validator, adapter binding, execution function, or permission grant. `dvz find` can render it but cannot run it.

Search normalizes case and tokens, then ranks exact command paths, exact reviewed phrases, complete token matches, prefix matches, and conservative fuzzy matches. Scores use stable ID ordering for ties. Exact and reviewed matches outrank fuzzy matches, and unrelated low-confidence input returns no result. Phase A results report their reviewed version range with `not_checked` status because search does not run tool detection.

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

The first `dvz ship` slice reuses `git.branch.push` only after live remote inspection, fast-forward ancestry verification, exact outgoing-commit disclosure, and remote-write confirmation. Its adapter uses `git push <remote> refs/heads/<branch>:refs/heads/<branch>` and cannot request force.

The corpus is available offline. A future `dvz sync` may supplement it with bounded, version-matched CLI help, but introspected help will remain quarantined discovery data with provenance and cannot create trusted mutation capabilities.
