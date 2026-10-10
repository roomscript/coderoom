# Command concepts

Working agreement for [#61](https://github.com/roomscript/coderoom/issues/61).
Keep this document roughly one page; implementation details belong in code or
[design docs](../../../docs/design/pkg-interpreter-composition.md).
`/who` exercises the prototype through its legacy completion path. Registration
and dispatch remain unchanged; this is a temporary bridge.

## Command and invocation

A **command** is stateless and owns its name, help metadata and preparation
behavior. `Prepare(Context)` creates an independent **invocation**, which holds
state for one use of the command.

```text
Prepare(context) -> invocation
Init()           -> initialize, no output
Next()           -> records and completion status
```

[`runtime.CommandRunner`](../runtime/command_runner.go) calls `Init` once, then
`Next` until `Done`, collecting records in order, including the final step.
It returns records without appending them. A step with no records and `Done: false`
returns an error; waiting and resumption are not supported yet.

## Capabilities and output

**Context** supplies only the capabilities commands need, through narrow
interfaces. `ParticipantReader` supplies participant information without session
execution access. Commands receive no raw mutable interpreter model.

**Records** use the existing canonical `room.Record` contract. Commands construct
records; the coordinator appends them to the transcript. The UI renders their
existing kinds without command-specific result handling.

The coordinator owns serialized execution, input acceptance, transcript changes,
submission outcomes, ordered publication and lifetime. Invocations do not perform
I/O or publish events in `Init` or `Next`.

## Current proof and open questions

Shared contracts live in [`runtime/command.go`](../runtime/command.go); `std`
depends on them, while the runner does not depend on command implementations.
`/who` captures participant values during preparation. `Init` initializes its
state; the first `Next` returns a system record and completes. The legacy
completion bridge appends it before publishing submission completion. The notice preserves
the existing `/who` display: sorted aliases as
`[agents] ada, tim`, or `[no agents]` when empty.

Its notice arrives through transcript deltas, with no `/who` presentation branch
in the UI. Generic module registration is deferred until we prove suspension.
Argument binding, waiting, execution requests, cancellation, later errors and
shutdown for pending invocations remain undecided. Add contracts when a concrete
command needs them; no generic fact or expectation framework yet.
