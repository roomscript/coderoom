# Command concepts

Working agreement for [#61](https://github.com/roomscript/coderoom/issues/61).
Keep this document roughly one page; details belong in code or
[design docs](../../../docs/design/pkg-interpreter-composition.md).
`/who` and `/shell` use the same registry dispatch and launch workflow.

## Command and invocation

A **command** is stateless and owns its name, help metadata and preparation.
`Statement()` identifies its accepted AST type using a representative value.
`Prepare(ParsedStatement, Context)` validates input and creates an **invocation**.

```text
Prepare(statement, context) -> invocation
Go(complete)    -> launch error, or accepted work
complete(result) -> final records and execution error
```

`Go` returns control after initiating work. Completion may happen immediately
(`/who`) or later. A nil launch error publishes submission success before any
completion records; preparation or launch errors fail the submission.
There is no `Init`, `Next`, polling, or generic fact/expectation contract.

## Capabilities and records

**Context** supplies narrow capabilities. `ParticipantReader` exposes participant
information without session execution access. `ShellLauncher` starts shell work
using executor-owned workers and lifetime. Shell display text comes from its
statement. Commands receive no raw model; user commands remain on the legacy path.

**Completion** contains existing `room.Record` values and an error. Commands
construct records and transfer ownership when calling `complete`; they must not
mutate those records afterward. The callback only enqueues. The interpreter
appends records and publishes outcomes in order on its serialized path.

The executor binds callbacks to invocation identities, consumes each completion
once, and ignores duplicate results or results from failed launches. Shutdown
drains retained completions, then fails unfinished invocations with `ErrClosed`
and rejects late callbacks.

## Current proof and next step

Contracts and `CommandRunner.Go` live in `runtime`; commands live in `std`.
`/who` prepares its original sorted alias notice and completes immediately.
Its queued completion appends the system record after launch acknowledgement.
The UI receives transcript deltas, with no `/who` result branch.

`/shell` uses the same path and returns its command record on completion. The
shell capability retains `ShellCompleted` for compatibility. Execution failures
are separate from submission success; user commands and loops remain legacy.
`runtime.Registry.Register(command)` indexes the type returned by `Statement()`.
`Lookup(parsed)` selects the command; metadata comes from the command itself.
Both commands are registered; user commands and definitions are future checkpoints.
