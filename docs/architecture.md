# Architecture

Phase B has the Phase A read-only application paths plus one trusted mutation workflow:

```text
cmd/dvz -> internal/app -> internal/search -> internal/registry -> registry/builtin
cmd/dvz -> internal/app -> internal/detect -> internal/process -> git or gh
cmd/dvz -> internal/app -> internal/operation -> internal/safety
cmd/dvz -> internal/app -> internal/adapters/git -> internal/process -> git
cmd/dvz -> internal/app -> internal/adapters/github -> internal/process -> gh
cmd/dvz -> internal/app -> internal/history
```

`cmd/dvz` owns Cobra wiring, output streams, JSON encoding, and process exit codes. Application services coordinate typed requests and responses without importing Cobra. Registry entries are reviewed discovery metadata. Search is deterministic and has no process dependency. Detection owns project evidence and tool-version interpretation. `internal/process` is the only general subprocess boundary.

Configuration is loaded before command behavior and is passed as typed state. Domain packages do not import Cobra. Phase B adds only the operation, history, and adapter pieces needed by `dvz repo create`; it does not add `raw`, TUI, MCP, or a broad workflow catalog.

## Trust Boundary

CLI input, config files, project markers, executable output, and registry descriptions are data, not instructions. Tool detection invokes only fixed version arguments through the process runner. Knowledge returned by `dvz find` contains no callback, executable adapter, or authorization state and cannot be promoted to execution.

Mutations enter through application services, immutable typed plans, safety policy, reviewed adapters, postcondition checks, and redacted history. `dvz repo create` groups local-write confirmation separately from remote repository creation and push. Unexpected remotes, detached HEAD, incompatible GitHub repositories, missing auth, and likely secret selections stop before unsafe mutation. The narrow unpublished-initial-commit repair remains in the repository application service and adapter boundary; it is not a general history-editing workflow.

The one-file `repo redact-initial` remediation uses the same application, plan, adapter, confirmation, history, and postcondition path. Its force-with-lease adapter requires an explicit expected remote SHA and is not available to discovery, passthrough, or the normal repository workflow.
