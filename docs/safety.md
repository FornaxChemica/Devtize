# Safety And Threat Model

Phase C keeps the existing read-only and repository behavior and adds focused daily Git workflows. `find` searches in-memory built-in knowledge and has no process runner dependency. `doctor` reads bounded configuration and project evidence, then runs only fixed version probes. `status` is offline unless live verification is explicitly requested, and `history` reads bounded local records. Repository and commit dry-runs inspect state and render typed plans without calling mutation adapters.

## Process Boundary

All subprocesses use an executable and argument slice, an explicit working directory, timeout, disabled stdin, a narrow environment, bounded output capture, and redaction values. The runner uses `exec.CommandContext` directly. Shell interpreters and command strings are prohibited. Git staging uses `git add -- <path>...`; initial push uses `git push --set-upstream`, and daily ship uses a full source/destination branch refspec. Project checks use only reviewed `go` and `gofmt` argument arrays. No path exposes arbitrary shell text or force options. Errors do not reproduce full command lines or environment values.

## Sensitive Data

Configuration accepts no credential fields. Diagnostics and history redact common token, password, secret, authorization, and bearer forms. `repo create` checks `gh auth status` before remote writes but does not read or store tokens. Selected files receive a bounded secret preflight based on filenames and common content patterns; warnings require an explicit confirmation that `--yes` cannot bypass.

History is redacted both before append and after read because the local file is untrusted. Public history output uses typed fields and does not expose arbitrary persisted invocation or project maps. Reads reject unsupported schemas, records over one MiB, and files over 32 MiB instead of silently dropping data. `status` and `history` do not append audit entries for themselves.

`dvz undo <execution-id> --dry-run` also treats history as untrusted. A source record can only locate typed `git.commit.created` evidence; it cannot supply a capability, executable, argument list, or authorization. Devtize independently verifies live branch, `HEAD`, parent, worktree, and configured upstream state. Legacy or ambiguous evidence produces an unavailable analysis. This milestone has no reset, restore, revert, push, confirmation, or history-write path.

## Risk Labels

Risk metadata describes what a discovered command could do if a developer later runs it independently. A label does not authorize Devtize to execute the command. Potentially overwriting commands such as `git restore` are labeled destructive even though their exact effect depends on arguments.

`repo create` renders a visible immutable plan with a digest. Disclosed local-write operations are grouped behind one digest-bound confirmation. GitHub repository creation and push are grouped behind a separate digest-bound remote-write confirmation. `--yes` may skip only policy-allowed local-write confirmation; it does not skip remote writes, secret warnings, invalid schemas, missing auth, stale plans, destructive denial, or privileged denial.

The sole Phase B destructive exception is `--repair-unpushed-initial`. It may amend exactly one local initial commit only when there is no upstream, the expected GitHub repository exists, GitHub reports no default branch, and disclosed local changes exist. The exact response `repair` is required against the plan digest even with `--yes`. Any publication evidence denies repair; the mode never force-pushes or performs arbitrary history editing.

The maintainer-authorized `repo redact-initial <path>` remediation is the only published-history rewrite. It requires one synchronized commit, a tracked local file already covered by `.gitignore`, content digests for every staged file, a live remote SHA matching the plan, and an exact-SHA `--force-with-lease`. It preserves the local file and commit message. Separate `redact` and `force-update` responses bind both destructive boundaries to the displayed plan; there is no `--yes` bypass or plain-force adapter.

Dry-run performs planning and validation only. Tests assert zero mutation adapter calls. Normal tests use temporary repositories, fake process runners, local bare repositories, or fake `gh`; CI must not create real GitHub repositories.

`dvz commit` refuses a detached branch, existing staged content, ignored paths, unchanged paths, traversal, invalid messages, stale `HEAD`, or changed selected-file content. It requires the exact response `commit` against the rendered plan digest. If commit creation fails after staging, Devtize leaves the disclosed paths staged, records partial completion, and tells the user to inspect the index; it never guesses a destructive cleanup.

Push-only `dvz ship` requires an attached branch with an exact configured upstream and one remote URL. It compares the live remote SHA to the local remote-tracking SHA, proves that SHA is an ancestor of local `HEAD`, discloses every outgoing commit and dirty path excluded from the push, and rechecks all of that around the digest-bound `push` confirmation. The adapter uses a full branch refspec and has no force option.

Composed ship accepts only reviewed check IDs. `go test`, `go vet`, and `go build` are classified `local_write` because project code is untrusted and tool execution may write caches or cause project-defined effects. `go.format.check` enumerates tracked and nonignored untracked files with NUL-delimited Git output and passes literal paths to `gofmt -l` in bounded batches. Checks have disabled stdin, fixed timeouts, a one-MiB diagnostic tail, a narrow environment, and no rollback promise. Failure stops before staging. Repository and file inventories are revalidated after checks.

`dvz status` invokes only repository, change, relation, and optional remote-branch reads. It never stages, fetches, pulls, rebases, resets, or writes refs. When a live SHA differs from local tracking state, Devtize reports stale tracking and does not infer ancestry from an object it has not fetched.

Idempotency is based on live postcondition checks, not history alone. Devtize may skip satisfied init, staging, commit, repository creation, remote configuration, or push steps only after current Git/GitHub state verifies the postcondition. Unexpected origins, detached HEAD, incompatible remote repositories, empty selections, missing tools, and unauthenticated `gh` stop with actionable errors.

Rollback is not promised for the Phase B workflow. Devtize never deletes `.git`, deletes GitHub repositories, changes a remote to a different repository, or performs destructive cleanup automatically. Force update exists only in the explicit one-file initial redaction and always uses an exact expected-SHA lease. Devtize may disclose and normalize an existing remote between SSH and HTTPS only when both URLs identify the exact same GitHub owner/repository and `gh` reports the destination protocol. The guarded initial-commit repair untracks only plan-disclosed ignored paths with `git rm --cached`, preserving working files.
