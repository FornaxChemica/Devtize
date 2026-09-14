# Contributing

Phase A contributions should remain inside configuration, process safety, read-only detection, reviewed registry knowledge, deterministic search, `doctor`, and `find`. Do not add mutation workflows or future-phase placeholder packages.

Before submitting a change, run:

```sh
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
go build ./cmd/dvz
```

Every behavior change needs focused tests. Process tests must assert executable and argument arrays exactly. Registry additions need reviewed provenance, risk/effect metadata, deterministic search coverage, and documentation that does not imply execution authority.

Use the naming contract from `AGENTS.md`: Devtize is the product, `devtize` is the repository name, and `dvz` is the executable.
