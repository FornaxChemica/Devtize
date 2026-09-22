# Phase C Status And History Plan

## Scope

Implement Phase C Task 7 as two deterministic read-only commands:

```text
dvz status [--remote] [--remote-name origin] [--json]
dvz history [--limit 20] [--all] [--json]
```

`status` is offline by default. `history` is scoped to the current project by
default. Neither command creates a plan, asks for confirmation, mutates state,
or records itself in history.

## Ordered Tasks

1. [x] Add typed tracking-relation and live-branch Git read models.
2. [x] Implement deterministic daily status and recommendations.
3. [x] Add bounded history reads, recursive redaction, and legacy workflow
       identification.
4. [x] Wire human and versioned JSON CLI output.
5. [x] Add unit, adapter, temporary-repository, CLI, and golden tests.
6. [x] Update user, architecture, safety, configuration, and schema docs.
7. [x] Run formatting, tests, race tests, vet, build, and available linting.
8. [x] Dogfood both commands on Devtize and record usability findings.
9. [x] Mark Phase C Task 7 complete only after all acceptance criteria pass.

## Acceptance Criteria

- [x] Default status performs no network or mutation calls.
- [x] Live status uses `ls-remote` without fetching and reports unpublished,
      synchronized, locally-ahead, and stale-tracking states.
- [x] Local status handles dirty, ahead, behind, diverged, detached, unborn,
      missing-upstream, and missing-remote repositories.
- [x] Output paths and recommendations are deterministic and stably ordered.
- [x] History is bounded, newest-first, project-scoped, schema-validated, and
      recursively redacted, including legacy Phase B records.
- [x] Missing history succeeds empty; invalid history fails without mutation.
- [x] Mutation tests use only temporary repositories, local bare remotes, and
      fake runners.
- [x] The complete verification matrix passes.
- [x] No plans are included in the implementation commit command.

## Dogfood Findings

- Offline status disclosed staged, unstaged, untracked, and ignored state while
  leaving `HEAD`, the index, and working files unchanged.
- Project history correctly identified both current workflow fields and legacy
  plan-ID prefixes, including the earlier `repo.set-description` execution.
- A failed live check returned an actionable `PROCESS_FAILED`; a permitted
  retry verified `origin/main` matched local `HEAD` at `16d0ff7` without fetch.
