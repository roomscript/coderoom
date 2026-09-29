# Temporary interpreter responsibility decomposition plan

This plan tracks GitHub issue #53. It is deliberately separate from
`INTERPRETER_IMPLEMENTATION_PLAN.md`, which tracks the parked command and TUI
migration in issue #38.

The purpose of this work is to make `Interpreter` a small facade and
composition root before adding staged batches or any other workflow. This plan
must be completed, or explicitly revised, before issue #38 resumes.

## Name and scope

Call this work **Interpreter responsibility decomposition**.

It preserves the existing serialized execution model and user-visible
behavior. It changes ownership and physical code boundaries only.

In scope:

- interpreter model state and workflow ownership
- session-event ingestion
- event dispatch
- interpreter execution and shutdown ownership
- snapshot ownership needed to make those boundaries coherent
- the existing generic instruction runner and `loopWorkflow`

Out of scope:

- staged barrier batches
- TUI cutover
- new prompt-language behavior
- removal of legacy migration APIs
- a generic workflow framework

## Target shape

The target is responsibility-based, not merely nested field grouping:

```go
type Interpreter struct {
    model    interpreterModel
    executor interpreterExecutor
}
```

The supporting components have behavioral boundaries:

- `interpreterModel` owns the canonical room, command registry, workflows,
  submission routing, completion decisions, and interpreter snapshots. It
  returns `instructionSequence` values but never runs them.
- `interpreterExecutor` owns operation serialization, lifetime cancellation,
  accepted-work shutdown guarantees, asynchronous worker tracking, the
  session-event inbox, event dispatcher, and generic causal instruction runner.
- `instructionRunner` is a focused executor component. It owns the iterative
  instruction queue and uses narrow ports for model state, session I/O,
  shell launch, asynchronous completion enqueueing, event publication, and
  snapshot composition. It knows no loop or future stage semantics.
- `sessionEventInbox` owns cross-goroutine buffering, its mutex, drain-pending
  bookkeeping, and wake-up coalescing.
- `eventDispatcher` owns observers, queued delivery, flush barriers, and event
  shutdown.
- `snapshotCache` owns the latest immutable interpreter snapshot. The operation
  loop refreshes it, and concurrent or post-shutdown readers receive detached
  copies without accessing mutable model state.

Components must encapsulate invariants and operations. Moving fields into a
sub-struct without moving the behavior that governs them does not complete a
step.

## Dependency direction

The dependency graph is deliberately one-way:

```text
Interpreter facade
    ├── interpreterModel (state and decisions -> instructionSequence)
    └── interpreterExecutor
            ├── instructionRunner -> narrow modelPort
            ├── sessionEventInbox
            └── eventDispatcher
```

The executor invokes the model; the model never calls the executor.
In particular, the model does not publish, enqueue completions, start
shell workers, or run its own instructions.

The executor-owned `instructionRunner` runs the causal-chain algorithm. Its
model port is limited to operations such as applying a captured session event,
appending a canonical record, resolving room-derived data, routing a typed
completion, and obtaining an interpreter snapshot. The model performs every
canonical-room read and mutation; the runner only controls ordering. Session
execution, shell launch, completion
enqueueing, and event publication use separate executor-owned ports. This avoids
a model ↔ executor callback cycle.

During Step 1, before `interpreterExecutor` exists as a cohesive component, the
facade may temporarily hold `instructionRunner` beside `interpreterModel`. This
is an explicit transitional state. Step 4 moves the unchanged runner under the
executor; the dependency direction does not change.

## Instruction vocabulary

Workflow and model transitions return only a finite ordered sequence:

```go
type instruction interface {
    instruction()
}

type instructionSequence []instruction
```

There is no batch or transition wrapper. Instructions produced by causal
session events and correlated completions are prepended to the runner's private
queue according to the documented ordering algorithm.

Final snapshot publication is requested by an instruction:

```go
type requestSnapshotInstruction struct{}
```

The runner does not publish immediately when it encounters this instruction.
It records the request, continues until the complete causal queue settles, and
then publishes one snapshot. Multiple requests coalesce naturally. This also
allows an otherwise empty state transition to request publication without
out-of-band result metadata.

## Snapshot ownership

The operation loop composes model state and the session roster, then stores one
immutable interpreter snapshot in the executor-owned cache:

```go
func (i *Interpreter) Snapshot() Snapshot {
    return i.executor.Snapshot()
}

// Invoked only on the serialized executor path.
snapshotCache.Store(composeSnapshot(model.Snapshot(), session.Roster()))
```

Approval state is part of the loop-confined model snapshot. While the executor
is running, `Interpreter.Snapshot` uses a serialized operation that first
settles prior session events and refreshes the cache. Once shutdown prevents
operation acceptance, reads use the cache directly. Every cache read returns a
detached copy, so callers cannot mutate the cached value.

## Invariants

- One operation loop remains the sole serialized interpreter coordinator.
- `session.Execute` remains serialized.
- Workflows remain operation-loop-confined and contain no synchronization,
  goroutines, I/O, room mutation, or observer publication.
- Every captured synchronous session-event burst is fully projected before an
  event-derived instruction executes.
- Event-derived instructions settle before command-result completion delivery.
- Shutdown resolves or rejects every accepted synchronous request.
- Public facade behavior, event ordering, snapshots, and error identity remain
  unchanged.
- `Interpreter` does not gain workflow-specific instruction handlers.

## 0. Restore the architectural checkpoint

- [x] Remove the uncommitted staged-batch experiment.
- [x] Reassess commit `0b86a45` and remove stage-only gateway vocabulary while
      the stage migration is parked, unless a non-stage consumer already needs
      it.
- [x] Retain the generic instruction runner, correlation model, causal-ordering
      tests, and cohesive `loopWorkflow`.
- [x] Reset every Step 7 checkbox in `INTERPRETER_IMPLEMENTATION_PLAN.md` to
      the actual committed Step 6 baseline.
- [x] Remove stage-experiment additions from
      `docs/design/pkg-interpreter-workflows.md`, or label them explicitly as
      approved future design rather than implemented behavior.
- [x] Verify issue #38, issue #53, and both implementation plans make no claim
      that removed staged-batch work is complete.
- [x] Run the full baseline verification before decomposition starts.

Stop condition: the worktree contains no partially authoritative stage state,
API, snapshot, or dispatch path.

## 1. Extract the model boundary

- [x] Introduce `interpreterModel` as the owner of the canonical room, command
      registry, and workflows. Session access remains behind an executor-owned
      gateway.
- [x] Move submission routing behind
      `interpreterModel.Submit(raw, statement, fallback)`
      without adding command-specific methods to `Interpreter`. Preserve the
      pre-parse stage gate through `interpreterModel.PreflightSubmission(raw)`;
      the operation applies returned instructions without inspecting stage
      state.
- [x] Move session-event projection and workflow routing behind
      `interpreterModel.ApplySessionEvent`.
- [x] Move completion routing behind the model boundary; it returns an
      `instructionSequence` from `interpreterModel.ApplyCompletion` and has no
      executor dependency.
- [x] Extract the current causal-chain algorithm into a generic
      `instructionRunner` using narrow model, session, shell, completion,
      publication, and snapshot ports.
- [x] Keep `instructionRunner` outside `interpreterModel`; its temporary
      facade-level placement is removed when Step 4 makes it executor-owned.
- [x] Move snapshot construction behind `interpreterModel` while
      keeping approval at its documented temporary boundary until Step 5.
- [x] Compose model and approval snapshots only through the documented
      temporary snapshot adapter until Step 5 replaces it with the cache.
- [x] Keep facade and operation types free of room, registry, and workflow
      implementation details.

Stop condition: `Interpreter` no longer owns room, registry, workflows, or
their transition procedures as independent fields and methods.

## 2. Extract the session-event inbox

- [x] Introduce `sessionEventInbox` owning the event buffer, mutex,
      drain-pending flag, and coalesced wake-up decision.
- [x] Give it focused operations for recording an event and taking a complete
      captured burst.
- [x] Keep enqueueing the drain operation at the executor boundary; the inbox
      must not know about workflows or model instructions.
- [x] Preserve the rule that a pending drain marker is cleared only after the
      inbox is observed empty by that drain operation.
- [x] Move existing burst, wake-up-coalescing, and causal-ordering tests to the
      narrowest useful level.

Stop condition: no session-event mutex, slice, or pending flag remains directly
on `Interpreter`.

## 3. Extract the event dispatcher

- [x] Introduce `eventDispatcher` owning the event queue, observer set and
      lock, delivery goroutine, flush barriers, and shutdown.
- [x] Expose focused `Publish`, `AddObserver`, `Flush`, and `Close` behavior.
- [x] Preserve observer ordering and the guarantee that slow observers do not
      block the operation loop.
- [x] Preserve shutdown delivery and barrier semantics.
- [x] Keep interpreter events as values; the dispatcher must not interpret
      workflow or command meaning.

Stop condition: observer and event-delivery primitives no longer appear as
independent fields or procedures on `Interpreter`.

## 4. Extract the executor

- [x] Introduce `interpreterExecutor` owning the operation queue, lifetime
      context, cancellation, close state, accepted-work guarantees, worker
      tracking, inbox, dispatcher, and the existing `instructionRunner`.
- [x] Make the run loop read as a coordination algorithm: drain prior events,
      apply one operation, drain causal events, and handle shutdown.
- [x] Keep operations data-oriented; they may invoke only narrow executor and
      model ports. They must not call back through the public `Interpreter`
      facade or grow command- or workflow-specific execution logic.
- [x] Preserve serialization between submissions, legacy execution, snapshots,
      approvals, shell completions, and shutdown.
- [x] Preserve rejection after shutdown and completion of accepted synchronous
      requests.
- [x] Ensure the executor depends on `modelPort`, while the model has no
      executor, dispatcher, enqueue, shell, or instruction-runner dependency.

Stop condition: `Interpreter` is primarily construction and public delegation,
not the owner of queue, goroutine, event-delivery, or shutdown algorithms.

## 5. Stabilize snapshot and approval ownership

- [x] Define one immutable cached snapshot published by the operation loop.
- [x] Serve reads after shutdown from that cache rather than mutable live
      model state.
- [x] Move approval state into the loop-confined model only after cached
      snapshots remove the external read requirement.
- [x] Remove the temporary approval lock only when race tests prove the new
      ownership model.
- [x] Do not introduce synchronization inside workflow components.

Stop condition: mutable model state is operation-loop-confined and
post-shutdown snapshot reads require no model-state locks.

## 6. Enforce and document the boundary

- [ ] Add structural tests or package-local assertions for the important
      dependency rules where practical.
- [ ] Update `docs/design/pkg-interpreter.md` and
      `docs/design/pkg-interpreter-workflows.md` to match implemented ownership.
- [ ] Remove obsolete adapters, forwarding helpers, and temporary names exposed
      by the previous flat structure.
- [ ] Review `Interpreter` fields and methods individually; every remaining
      member must belong to facade construction or public delegation.
- [ ] Review issue #53 against the acceptance criteria below before unblocking
      issue #38.

## Acceptance criteria

- `Interpreter` is a facade/composition root, not the physical owner of
  model, inbox, dispatcher, and executor primitives.
- State and decision behavior is cohesive behind `interpreterModel`.
- Executor, inbox, and dispatcher each have one documented invariant and focused
  methods that enforce it.
- Adding a loop transition does not require a loop-specific method on
  `Interpreter`.
- Adding the future stage workflow has an obvious home and does not require
  adding stage execution machinery to `Interpreter`.
- No user-visible behavior or event ordering changes.
- `go test -race ./internal/interpreter` passes.
- `golangci-lint run ./...` passes.
- `go test ./...` passes.

## Review checkpoints

Review after each numbered step. Do not combine inbox, dispatcher, executor, and
snapshot extraction into one patch. If a step only moves fields without
clarifying ownership or reducing `Interpreter` responsibility, stop and revise
the design before continuing.
