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
git:
  default_branch: main
github:
  visibility: private
```

Supported color values are `auto`, `always`, and `never`. Phase B currently emits no ANSI styling, including in JSON. `ai.provider` exists to make the no-AI state explicit and accepts only `disabled`.

`safety.confirm_local_writes` and `safety.confirm_remote_writes` document the default confirmation policy. `safety.allow_yes_for` may include `local_write`; `--yes` is still not a universal bypass and cannot skip remote-write confirmation or potential-secret warnings in Phase B. `history.enabled` controls append-only redacted execution history. `git.default_branch` defaults the initial branch for `dvz repo create`. `github.visibility` defaults to `private`.

Configuration layers are merged in this order, highest precedence first:

```text
CLI flags > environment > nearest project config > user config > defaults
```

`--no-color` is the Phase A CLI override. Environment inputs are `NO_COLOR`, `DVZ_COLOR`, and `DVZ_AI_PROVIDER`. The nearest `.dvz.yaml` found from the working directory upward is the project config. User configuration is `<platform-config-dir>/devtize/config.yaml`; an absolute `XDG_CONFIG_HOME` replaces the platform base when present.

Missing files are valid and use defaults. Files are read with a one-megabyte bound. Configuration contains no secret fields; error rendering still redacts common credential-like assignments and bearer values.
