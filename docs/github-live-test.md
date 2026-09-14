# Opt-In Live GitHub Test

Normal Devtize tests must not create real GitHub repositories. They use temporary repositories, local bare repositories, fake process runners, or fake `gh` behavior.

Maintainers may perform a disposable live check only when all of the following are true:

- `DEVTIZE_LIVE_GITHUB_TEST=1` is set.
- The authenticated `gh` account is a disposable personal account or test organization.
- The repository name is unique and disposable.
- The maintainer is prepared to delete the test repository afterward.

Suggested manual procedure:

```sh
tmpdir="$(mktemp -d)"
cd "$tmpdir"
printf 'live fixture\n' > README.md

dvz repo plan --owner OWNER --name devtize-live-fixture-YYYYMMDD --visibility private --message "Initial commit"
dvz repo create --owner OWNER --name devtize-live-fixture-YYYYMMDD --visibility private --message "Initial commit"
```

After verifying the result, delete the disposable repository with GitHub’s normal UI or a separately confirmed `gh` command. Do not run this fixture from CI, and do not use it against the real Devtize folder.
