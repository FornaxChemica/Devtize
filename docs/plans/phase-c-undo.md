# Phase C.3 Constrained Undo Planning

## Delivered Behavior

`dvz undo <execution-id> --dry-run` performs a bounded, project-scoped history lookup and validates typed commit evidence against live Git state. Successful `commit` and `ship.local` records can produce one immutable `git.commit.uncommit_preserve_changes` plan when the recorded commit is the current single-parent `HEAD`, the branch matches, the worktree is clean, and the configured live upstream does not contain the commit.

Unsupported, failed, legacy, published, stale, or ambiguous records return `eligibility: unavailable` with a stable reason and no operations. Missing execution IDs return `HISTORY_ENTRY_NOT_FOUND`.

## Safety Boundary

History is an untrusted locator. Capability identity and operation shape come only from reviewed source-owned registry metadata. Git evidence is re-read through literal, no-shell adapter calls. The command performs no reset, restore, revert, push, prompt, confirmation, or history append, and every human response ends with `No changes were made`.

The planned compensation is classified as `local_write` and `compensating`, not exact. Its intended future binding is `git reset --mixed <verified-parent>`, but no executable binding exists in this milestone.

## Compatibility

History remains schema version 1. New successful local commit records add optional typed `observed_changes`, and new execution IDs add a digest-derived suffix. Legacy records and IDs remain readable but cannot be compensated when commit evidence is absent.

## Verification

- Unit coverage validates history lookup, evidence, registry metadata, exact Git argv, every unavailable reason, and malicious input rejection.
- CLI and renderer coverage verifies human, JSON, ANSI-free, and 60/80/120-column output.
- Temporary repositories and local bare remotes prove unpublished eligibility, published refusal, and byte-for-byte non-mutation.
- Formatting, full tests, race tests, vet, build, and diff checks must pass before the Phase C acceptance checkbox is completed.
