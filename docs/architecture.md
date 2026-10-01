# Architecture

Phase D.3 builds on the Phase A read-only paths, Phase B repository workflows, Phase C daily commands, and Phase D.1/D.2 discovery sync with bounded JavaScript/TypeScript project detection:

```text
cmd/dvz -> internal/app -> internal/search -> internal/registry -> registry/builtin
cmd/dvz -> internal/app/SyncService -> internal/adapters/git -> internal/process -> git help
internal/app/SyncService -> internal/registry/CacheStore
cmd/dvz -> internal/app -> internal/detect -> internal/process -> git or gh
cmd/dvz -> internal/app -> internal/detect -> bounded project filesystem reads
cmd/dvz -> internal/app -> internal/operation -> internal/safety
cmd/dvz -> internal/app -> internal/adapters/git -> internal/process -> git
cmd/dvz -> internal/app -> internal/adapters/github -> internal/process -> gh
cmd/dvz -> internal/ui
internal/app -> internal/adapters/golang -> internal/process -> go or gofmt
cmd/dvz -> internal/app -> internal/history
```

`cmd/dvz` owns Cobra wiring, output streams, JSON encoding, and process exit codes. `internal/ui` owns adaptive human rendering and has no process access. Application services coordinate typed requests and responses without importing Cobra. Registry entries are reviewed discovery metadata or a closed set of executable check capabilities. Search is deterministic and has no process dependency. Detection owns project evidence and tool-version interpretation. `internal/process` is the only general subprocess boundary.

Project detection remains inside `internal/detect` and strengthens the original
Phase A path rather than adding a second detector. One bounded ancestor walk
selects the nearest project marker and nearest enclosing workspace, stopping at
the Git boundary. Strict package-manifest parsing and package-manager
resolution are pure filesystem/domain logic. Only `DoctorService` and the UI
consume the additive result; registry, configuration, cache, history, and
operation schemas are unchanged.

Configuration is loaded before command behavior and is passed as typed state. Domain packages do not import Cobra. Focused services reuse operation, history, safety, and adapter boundaries without introducing a generic workflow DSL. `raw`, TUI, MCP, and a broad workflow catalog remain absent.

`SyncService` is deliberately provider-specific in this slice. It detects Git, runs one reviewed depth-one help invocation in a temporary isolated directory, parses into an in-memory quarantine snapshot, and asks `CacheStore` to publish only after plan confirmation and tool revalidation. `CacheStore` owns strict schema validation, content addressing, bounded loading, deterministic fallback, permissions, and retention. Search receives a merged in-memory catalog at CLI construction and has no runner dependency.

`StatusService` composes only reviewed Git reads. Local inspection and tracking relation are offline; optional live inspection uses `ls-remote` without fetching. `HistoryService` reads a bounded JSON Lines store through a narrow reader interface and exposes typed entries instead of persisted free-form maps. Neither service enters the mutation plan or confirmation path, and neither records its own read.

`UndoService` performs an exact project-scoped history lookup, then treats the returned record as untrusted evidence. Only a registered compensation can be proposed, and only after live Git inspection proves the recorded commit is the current single-parent `HEAD` on the same branch with a clean worktree and no conflicting live upstream state. It returns a dry-run operation plan but has no executor or mutation adapter method.

## Trust Boundary

CLI input, config files, project markers, executable output, cache files, and registry descriptions are data, not instructions. Tool detection invokes only fixed version arguments through the process runner. Git sync accepts reviewed depth-one inventory rows. GitHub CLI sync parses bounded depth-two `##`/`###` headings, aliases, usage, and fixed-column flags from only `gh help reference`; over-depth sections are excluded. Each provider owns its parser and cache validation, and one corrupt cache cannot hide another. Neither sync path invokes a discovered name. Knowledge returned by `dvz find` contains no callback, executable adapter, validator, or authorization state and cannot be promoted to execution.

JavaScript/TypeScript project files are likewise untrusted data. Detection uses
`Lstat`, accepts non-Git markers only as regular non-symlink files, reads at
most 1 MiB from each of at most 16 manifests, accepts exactly one top-level
JSON object, and inspects at most 64 ancestors. Workspace globs and members are
never expanded. Metadata selects only an informational result and cannot name
an executable, arguments, capability, or adapter.

Mutations enter through application services, immutable typed plans, safety policy, reviewed adapters, postcondition checks, and redacted history. `dvz repo create` groups local-write confirmation separately from remote repository creation and push. Unexpected remotes, detached HEAD, incompatible GitHub repositories, missing auth, and likely secret selections stop before unsafe mutation. The narrow unpublished-initial-commit repair remains in the repository application service and adapter boundary; it is not a general history-editing workflow.

The one-file `repo redact-initial` remediation uses the same application, plan, adapter, confirmation, history, and postcondition path. Its force-with-lease adapter requires an explicit expected remote SHA and is not available to discovery, passthrough, or the normal repository workflow.

`dvz commit` plans from an attached branch and clean index, discloses exact changed paths and content digests, revalidates them after confirmation, stages only those paths, and verifies the new `HEAD` and message. Existing staged content is rejected so an undisclosed path cannot enter the commit.

`ShipService` retains the compatible push-only path. `ShipWorkflowService` first binds reviewed check IDs, toolchain identity, selected paths, content digests, and the inspected remote boundary into one local plan. It executes checks before staging and revalidates repository state afterward. Because a commit SHA cannot be predicted safely, the service creates a second exact push plan only after commit postconditions pass. Both plans share a workflow ID but require independent digest-bound confirmations.
