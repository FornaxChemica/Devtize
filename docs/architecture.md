# Architecture

Phase C builds on the Phase A read-only paths and Phase B repository workflows with daily commit, composed ship, status, history, and constrained undo-planning commands:

```text
cmd/dvz -> internal/app -> internal/search -> internal/registry -> registry/builtin
cmd/dvz -> internal/app -> internal/detect -> internal/process -> git or gh
cmd/dvz -> internal/app -> internal/operation -> internal/safety
cmd/dvz -> internal/app -> internal/adapters/git -> internal/process -> git
cmd/dvz -> internal/app -> internal/adapters/github -> internal/process -> gh
cmd/dvz -> internal/ui
internal/app -> internal/adapters/golang -> internal/process -> go or gofmt
cmd/dvz -> internal/app -> internal/history
```

`cmd/dvz` owns Cobra wiring, output streams, JSON encoding, and process exit codes. `internal/ui` owns adaptive human rendering and has no process access. Application services coordinate typed requests and responses without importing Cobra. Registry entries are reviewed discovery metadata or a closed set of executable check capabilities. Search is deterministic and has no process dependency. Detection owns project evidence and tool-version interpretation. `internal/process` is the only general subprocess boundary.

Configuration is loaded before command behavior and is passed as typed state. Domain packages do not import Cobra. The focused `CommitService` reuses operation, history, safety, and Git adapter boundaries without introducing a generic workflow DSL. `raw`, TUI, MCP, and a broad workflow catalog remain absent.

`StatusService` composes only reviewed Git reads. Local inspection and tracking relation are offline; optional live inspection uses `ls-remote` without fetching. `HistoryService` reads a bounded JSON Lines store through a narrow reader interface and exposes typed entries instead of persisted free-form maps. Neither service enters the mutation plan or confirmation path, and neither records its own read.

`UndoService` performs an exact project-scoped history lookup, then treats the returned record as untrusted evidence. Only a registered compensation can be proposed, and only after live Git inspection proves the recorded commit is the current single-parent `HEAD` on the same branch with a clean worktree and no conflicting live upstream state. It returns a dry-run operation plan but has no executor or mutation adapter method.

## Trust Boundary

CLI input, config files, project markers, executable output, and registry descriptions are data, not instructions. Tool detection invokes only fixed version arguments through the process runner. Knowledge returned by `dvz find` contains no callback, executable adapter, or authorization state and cannot be promoted to execution.

Mutations enter through application services, immutable typed plans, safety policy, reviewed adapters, postcondition checks, and redacted history. `dvz repo create` groups local-write confirmation separately from remote repository creation and push. Unexpected remotes, detached HEAD, incompatible GitHub repositories, missing auth, and likely secret selections stop before unsafe mutation. The narrow unpublished-initial-commit repair remains in the repository application service and adapter boundary; it is not a general history-editing workflow.

The one-file `repo redact-initial` remediation uses the same application, plan, adapter, confirmation, history, and postcondition path. Its force-with-lease adapter requires an explicit expected remote SHA and is not available to discovery, passthrough, or the normal repository workflow.

`dvz commit` plans from an attached branch and clean index, discloses exact changed paths and content digests, revalidates them after confirmation, stages only those paths, and verifies the new `HEAD` and message. Existing staged content is rejected so an undisclosed path cannot enter the commit.

`ShipService` retains the compatible push-only path. `ShipWorkflowService` first binds reviewed check IDs, toolchain identity, selected paths, content digests, and the inspected remote boundary into one local plan. It executes checks before staging and revalidates repository state afterward. Because a commit SHA cannot be predicted safely, the service creates a second exact push plan only after commit postconditions pass. Both plans share a workflow ID but require independent digest-bound confirmations.
