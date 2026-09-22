# Persisted History

Devtize stores append-only JSON Lines history at
`<platform-config-dir>/devtize/history.jsonl`. An absolute `XDG_CONFIG_HOME`
replaces the platform configuration directory. File permissions are `0600` and
the containing directory is created with `0700` when Devtize first records a
mutation.

## Record Schema

Persisted records use schema version 1 and contain execution and plan IDs, the
plan digest, start and finish timestamps, redacted invocation metadata, a
project identifier, execution status, typed step results, and recovery hints.
They do not contain full diffs, repository contents, environment dumps, model
prompts, or unbounded process output.

New records include a stable `workflow` value in invocation metadata. Readers
derive known workflows from legacy plan-ID prefixes when that field is absent.
Adding this optional field does not change the persisted schema version.

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
