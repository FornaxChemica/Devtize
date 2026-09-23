# Configuration

Devtize uses strict YAML with schema version 1. Unknown fields, missing or unsupported versions, unsupported color values, unsupported GitHub visibility values, and any AI provider other than `disabled` are rejected.

```yaml
version: 1
ui:
  color: auto
ai:
  provider: disabled
safety:
  confirm_local_writes: true
  confirm_remote_writes: true
  allow_yes_for:
    - local_write
history:
  enabled: true
checks:
  ship:
    - go.format.check
    - go.test
    - go.vet
    - go.build
git:
  default_branch: main
github:
  visibility: private
```

Supported color values are `auto`, `always`, and `never`. `auto` styles only a capable TTY, `always` forces ANSI for human output, and `never`, `NO_COLOR`, `--no-color`, redirected auto output, and JSON remain unstyled. `ai.provider` exists to make the no-AI state explicit and accepts only `disabled`.

`checks.ship` is an ordered list of reviewed capability IDs. Supported values are `go.format.check`, `go.test`, `go.vet`, and `go.build`. Unknown IDs, duplicates, and lists longer than 16 are rejected. A project or user layer replaces the lower-precedence list as a unit; repeated `--check` flags replace configured checks for one composed `ship` invocation. An empty list is valid and is disclosed as `no checks configured`.

`safety.confirm_local_writes` and `safety.confirm_remote_writes` document the default confirmation policy. `safety.allow_yes_for` may include `local_write`; `--yes` is still not a universal bypass and cannot skip remote-write confirmation or potential-secret warnings in Phase B. `history.enabled` controls append-only redacted execution history; existing records remain readable through `dvz history` when recording is disabled. `git.default_branch` defaults the initial branch for `dvz repo create`. `github.visibility` defaults to `private`.

Configuration layers are merged in this order, highest precedence first:

```text
CLI flags > environment > nearest project config > user config > defaults
```

`--no-color` is the Phase A CLI override. Environment inputs are `NO_COLOR`, `DVZ_COLOR`, and `DVZ_AI_PROVIDER`. The nearest `.dvz.yaml` found from the working directory upward is the project config. User configuration is `<platform-config-dir>/devtize/config.yaml`; an absolute `XDG_CONFIG_HOME` replaces the platform base when present.

Missing files are valid and use defaults. Files are read with a one-megabyte bound. Configuration contains no secret fields; error rendering still redacts common credential-like assignments and bearer values.

History is stored at `<platform-config-dir>/devtize/history.jsonl`, or beneath `XDG_CONFIG_HOME` when configured. See [Persisted History](persisted-history.md) for its schema, read bounds, and compatibility policy.
