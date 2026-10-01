# GitHub CLI reference fixtures

These files are unmodified stdout from the fixed local command
`gh help reference`. They are test data only and are always parsed as
untrusted discovery metadata.

| File | Origin | SHA-256 |
|---|---|---|
| `reference-2.93.0.txt` | Homebrew GitHub CLI 2.93.0 release binary, empty home/config/state and reviewed environment capture on 2026-10-01; <https://github.com/cli/cli/releases/tag/v2.93.0> | `428e8337a8b18a86abf12c76c31048f6554953c7eb8ca097e0a813a1b2ee1f96` |
| `reference-2.80.0.txt` | Official `cli/cli` tag `v2.80.0`, commit `fdd9e7646b7b8ce1d77f738e412ffc608c2f0545`, built locally and captured with empty home/config/state on 2026-10-01; <https://github.com/cli/cli/releases/tag/v2.80.0> | `027863713c858aad3792f7d286679a3505f1f187736f4a82f0ef212cffbfa9ad` |

The parser deliberately omits over-depth `#### gh ...` command sections. The
inventory counts asserted in `reference_test.go` cover every accepted `##` and
`###` command plus its owned aliases and flags.
