# Persisted History

Devtize stores append-only JSON Lines history at
`<platform-config-dir>/devtize/history.jsonl`. An absolute `XDG_CONFIG_HOME`
replaces the platform configuration directory. File permissions are `0600` and
the containing directory is created with `0700` when Devtize first records a
mutation.

## Record Schema

Persisted records use schema version 1 and contain execution and plan IDs, an optional workflow ID, the
plan digest, start and finish timestamps, redacted invocation metadata, a
project identifier, execution status, typed step results, recovery hints, and
optional typed observed changes.
They do not contain full diffs, repository contents, environment dumps, model
prompts, or unbounded process output.

New records include a stable `workflow` value in invocation metadata. Readers
derive known workflows from legacy plan-ID prefixes when that field is absent.
Composed ship uses the optional `workflow_id` field to correlate its local and remote plans. Adding these optional fields does not change the persisted schema version.

Verified successful `commit` and `ship.local` records now include a
`git.commit.created` observed change with branch, before commit, and after
commit. New execution IDs include a digest-derived suffix to avoid same-second
collisions. Both additions remain schema version 1; existing IDs and records
without observations remain valid.

## Reading And Redaction

`dvz history` returns the newest 20 matching records for the current project.
`--limit` accepts 1 through 200 and `--all` removes the project filter. Human
and JSON output use a typed view and never expose the persisted free-form maps.

Records are redacted before append and again after read. Fields or values that
look like tokens, passwords, secrets, authorization values, or bearer values
are replaced. A record may be at most one MiB and the history file may be at
most 32 MiB per read. Missing files are an empty successful result. Malformed,
oversized, or unsupported records return `HISTORY_INVALID`; filesystem failures
return `HISTORY_READ_FAILED`. Devtize never repairs, truncates, or deletes the
history file automatically.

`history.enabled: false` stops future mutation records from being appended but
does not hide existing records. Reading status or history does not create a new
history entry.

`dvz undo` performs an exact, project-scoped execution-ID lookup under the same
one-record and 32-MiB bounds. Duplicate IDs are rejected as `HISTORY_INVALID`.
Missing observations are never reconstructed from commit messages or
timestamps; the record is reported as unavailable for compensation planning.
