# Safety And Threat Model

Phase B keeps Phase A read-only behavior for `dvz find` and `dvz doctor`, and adds one mutation workflow: `dvz repo create`. `find` searches in-memory built-in knowledge and has no process runner dependency. `doctor` reads bounded configuration and project evidence, then runs only fixed version probes. `repo plan` and `repo create --dry-run` inspect state and render a typed plan without calling mutation adapters.

## Process Boundary

All subprocesses use an executable and argument slice, an explicit working directory, timeout, disabled stdin, a narrow environment, bounded output capture, and redaction values. The runner uses `exec.CommandContext` directly. Shell interpreters and command strings are prohibited. Git staging uses `git add -- <path>...`; push uses `git push --set-upstream` and never force options. Errors do not reproduce full command lines or environment values.

## Sensitive Data

Configuration accepts no credential fields. Diagnostics and history redact common token, password, secret, authorization, and bearer forms. `repo create` checks `gh auth status` before remote writes but does not read or store tokens. Selected files receive a bounded secret preflight based on filenames and common content patterns; warnings require an explicit confirmation that `--yes` cannot bypass.

## Risk Labels

Risk metadata describes what a discovered command could do if a developer later runs it independently. A label does not authorize Devtize to execute the command. Potentially overwriting commands such as `git restore` are labeled destructive even though their exact effect depends on arguments.

`repo create` renders a visible immutable plan with a digest. Disclosed local-write operations are grouped behind one digest-bound confirmation. GitHub repository creation and push are grouped behind a separate digest-bound remote-write confirmation. `--yes` may skip only policy-allowed local-write confirmation; it does not skip remote writes, secret warnings, invalid schemas, missing auth, stale plans, destructive denial, or privileged denial.

The sole Phase B destructive exception is `--repair-unpushed-initial`. It may amend exactly one local initial commit only when there is no upstream, the expected GitHub repository exists, GitHub reports no default branch, and disclosed local changes exist. The exact response `repair` is required against the plan digest even with `--yes`. Any publication evidence denies repair; the mode never force-pushes or performs arbitrary history editing.

The maintainer-authorized `repo redact-initial <path>` remediation is the only published-history rewrite. It requires one synchronized commit, a tracked local file already covered by `.gitignore`, content digests for every staged file, a live remote SHA matching the plan, and an exact-SHA `--force-with-lease`. It preserves the local file and commit message. Separate `redact` and `force-update` responses bind both destructive boundaries to the displayed plan; there is no `--yes` bypass or plain-force adapter.

Dry-run performs planning and validation only. Tests assert zero mutation adapter calls. Normal tests use temporary repositories, fake process runners, local bare repositories, or fake `gh`; CI must not create real GitHub repositories.

Idempotency is based on live postcondition checks, not history alone. Devtize may skip satisfied init, staging, commit, repository creation, remote configuration, or push steps only after current Git/GitHub state verifies the postcondition. Unexpected origins, detached HEAD, incompatible remote repositories, empty selections, missing tools, and unauthenticated `gh` stop with actionable errors.

Rollback is not promised for the Phase B workflow. Devtize never deletes `.git`, deletes GitHub repositories, changes a remote to a different repository, or performs destructive cleanup automatically. Force update exists only in the explicit one-file initial redaction and always uses an exact expected-SHA lease. Devtize may disclose and normalize an existing remote between SSH and HTTPS only when both URLs identify the exact same GitHub owner/repository and `gh` reports the destination protocol. The guarded initial-commit repair untracks only plan-disclosed ignored paths with `git rm --cached`, preserving working files.
