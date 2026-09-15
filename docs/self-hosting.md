# Self-Hosting Evidence

Devtize completed its first self-hosting milestone on 2026-09-15. The
maintainer used the locally built `dvz` executable to initialize, repair, and
publish the repository through reviewed Git and GitHub adapters.

## Repository

- Repository: `https://github.com/FornaxChemica/Devtize`
- Visibility: public
- Default branch: `main`
- Verified initial commit: `01afcc5ad8100717ed4d006fa873926abac2a693`
- Commit message: `chore: self-host Devtize with Devtize`
- Devtize build metadata: `devel`, commit `unknown`, build time `unknown`

## Executed Plans

The local redacted history records these maintainer-confirmed workflows:

1. `plan_repo_create_20260914230756` initialized Git, staged the disclosed
   files, created the initial commit, created the GitHub repository, and
   configured `origin`. Its first push failed and was recorded as partially
   completed.
2. `plan_repo_create_20260915000043` removed disclosed generated artifacts,
   amended the verified unpublished initial commit, normalized the equivalent
   authenticated remote URL, and pushed `main` successfully.
3. `plan_repo_redact_initial_20260915003538` removed the ignored
   `MASTER_IDE_PROMPT.md` from the published initial commit while preserving
   the local file, then replaced the remote commit with an exact-SHA
   force-with-lease.
4. `plan_repo_set_description_20260915013758` set and verified the public
   GitHub repository description.

Plan digests and timestamps remain in the local redacted JSON Lines history.
They are not copied here because the plan IDs and verified outcomes provide
sufficient reproducible evidence without publishing workstation paths.

## Verification

After the final remediation:

- local `HEAD`, `origin/main`, and live GitHub `main` resolved to the same SHA;
- the repository contained exactly one initial commit;
- `MASTER_IDE_PROMPT.md`, `.cache`, and the root `dvz` binary were absent from
  the commit;
- `MASTER_IDE_PROMPT.md` and `dvz` remained present locally and ignored;
- `origin` identified `FornaxChemica/Devtize`;
- GitHub reported the repository as public with `main` as its default branch;
- the full Go test suite, race suite, vet, formatting check, and build passed.

## Manual Decisions

- The maintainer supplied the Conventional Commit message and personally
  approved every Devtize confirmation.
- The repository was initially created as private because the first command
  requested private visibility. The maintainer later changed it to public.
- The repository was renamed from `devtize` to `Devtize`; the Go module path
  remains lowercase to preserve Go import compatibility.
- No credentials, raw authentication output, environment dump, or repository
  file contents are included in this evidence.

The coding agent did not manually run the Git or GitHub commands used to create
the initial repository, stage its initial contents, create its initial commit,
configure its initial remote, or publish its initial branch.
