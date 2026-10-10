# Command concepts

Working agreement for [#61](https://github.com/roomscript/coderoom/issues/61).
Keep this document roughly one page; details belong in code or
[design docs](../../../docs/design/pkg-interpreter-composition.md).
`/who` uses a temporary bridge; registration and dispatch remain unchanged.

## Command and invocation

A **command** is stateless and owns its name, help metadata and preparation.
`Prepare(Context)` creates an independent **invocation** for one use.

```text
Prepare(context) -> invocation
Go(complete)    -> launch error, or accepted work
complete(result) -> final records and execution error
```

`Go` returns control after initiating work. Completion may happen immediately
(`/who`) or later. Launch acceptance and work completion are different milestones.
There is no `Init`, `Next`, polling, or generic fact/expectation contract.

## Capabilities and records

**Context** supplies narrow capabilities. `ParticipantReader` exposes participant
information without session execution access. Commands receive no raw model.
Future execution capabilities must use executor-owned workers and lifetime.

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
Its queued completion appends the system record before submission success.
The UI receives transcript deltas, with no `/who` result branch.

Next, introduce `/shell` through its existing execution seam. It acknowledges
submission after launch, before work finishes; preserve that distinction and
its current structured event until deliberately retired. Execution capabilities,
per-invocation cancellation and generic registration remain to be proved.
