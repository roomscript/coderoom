# Interpreter workflow architecture

This document defines the target boundary for extracting application workflows
without weakening the interpreter's serialized ordering guarantees. It guides
GitHub issue #53 and the staged-batch migration.

## Target shape

`Interpreter` remains the public facade and composition root. Its operation
loop is the single serialized owner of mutable application workflows, but the
facade does not implement workflow-specific procedures.

```go
type Interpreter struct {
    app        application
    runtime    interpreterRuntime
    dispatcher eventDispatcher
    inbox      sessionEventInbox

    // Temporary synchronized boundary until cached snapshots replace reads
    // made after the operation loop has stopped.
    approval approvalSnapshotState
}
```

These names describe responsibility boundaries, not field-grouping wrappers:

- `application` owns the canonical room, command registry, staging, and the
  loop/stage workflows. It routes inputs to workflows and returns effects.
- `interpreterRuntime` owns operation serialization, lifetime cancellation,
  shutdown acceptance, and interpreter-owned asynchronous work.
- `eventDispatcher` owns observers, queued delivery, flush barriers, and event
  shutdown.
- `sessionEventInbox` owns cross-goroutine session-event buffering and its
  coalesced drain wake-up.
- `approvalSnapshotState` temporarily owns active approval state and its lock.
  It is deliberately outside loop-confined `application` state because
  `Snapshot` may read it after operation-loop shutdown. It moves into
  `application` only after snapshots are served from an immutable cache.

Only components whose extraction makes an invariant clearer should be split
out. The first proof is a cohesive loop workflow plus the smallest viable
effect executor. Runtime, dispatcher, and inbox extraction follows only where
it makes the resulting coordinator simpler.

## Confinement rule

Application workflows are confined to the interpreter operation loop. They do
not use locks, start goroutines, perform I/O, execute session commands, mutate
the room, or publish observer events. They own complete decision processes and
return application effects.

The boundary has three acceptance rules:

1. Adding or changing a loop transition does not require adding a loop-specific
   method to `Interpreter`.
2. `Interpreter` applies effects without knowing which workflow produced them.
3. Workflow components never execute I/O, publish events, start goroutines, or
   synchronize themselves.

## Correlation vocabulary

Replies and asynchronous completions use data-only correlation values. They do
not contain callbacks or closures.

```go
type workflowKind uint8

const (
    workflowLoop workflowKind = iota + 1
    workflowStage
)

type workflowRef struct {
    kind       workflowKind
    generation uint64
    requestID  uint64
}
```

- `kind` selects the receiving workflow.
- `generation` changes whenever a workflow instance is replaced or finished.
- `requestID` is allocated monotonically by the owning workflow. It is unique
  within that workflow's scope; the `(kind, generation, requestID)` tuple is
  globally unambiguous to the application. The application does not allocate
  request IDs on a workflow's behalf.

Session planning, session execution, and shell requests carry a `workflowRef`.
The application routes their typed completions through one data-oriented entry
point. A workflow accepts a completion only when all three fields match its
current pending request. Otherwise the completion is stale and cannot change
workflow state.

## Session gateway and planning-dependent requests

Workflows cannot construct every `session.Command`. Some commands require a
session-owned plan at execution time, and frozen staged dispatches require
their own planning inputs. Effects therefore carry application-level session
requests rather than partially constructed `session.Command` values.

The initial closed request vocabulary is:

```go
type sessionRequest interface{ sessionRequest() }

type planAndExecuteSharedSendRequest struct {
    alias         string
    directText    string
    listenersText string
}

type executePlannedSharedSendRequest struct {
    plan          session.SharedSendPlan
    directText    string
    listenersText string
}

type broadcastRequest struct {
    text string
}

type handoffRequest struct {
    fromAlias   string
    toAlias     string
    idleAliases []string
    source      session.HandoffSource
}

type cancelRequest struct {
    alias string
}
```

Immediate loop turns use `planAndExecuteSharedSendRequest`, which deliberately
plans immediately before execution. Staged sends must preserve the routing
decision made when they were staged, so they use a separate planning effect and
later execute `executePlannedSharedSendRequest` with the opaque, session-bound
plan returned by the gateway.

```go
type planSharedSendEffect struct {
    target workflowRef
    alias  string
}

type readParticipantStateEffect struct {
    target workflowRef
}

type sharedSendPlanResult struct {
    plan    session.SharedSendPlan
    targets []string
}

type participantState struct {
    alias  string
    status participant.Status
    turnID uint64
}

type participantStateResult struct {
    barrier []participantState
    routable []participantState
}
```

Planning and participant inspection are synchronous and serialized but still
cross the workflow boundary as effects: the workflow does not call the
session. Their correlated completions freeze the opaque plan, detached target
aliases, and barrier inputs in staged state. `participantState` deliberately
omits agents and other live session-owned references.

The centralized session gateway translates execution requests immediately
before execution:

```go
func (g sessionGateway) Execute(request sessionRequest) error {
    switch request := request.(type) {
    case planAndExecuteSharedSendRequest:
        plan := g.session.PlanSharedSend(request.alias)
        return g.session.Execute(session.SharedSendCommand{
            Plan:          plan,
            TextDirect:    request.directText,
            TextListeners: request.listenersText,
        })
    case executePlannedSharedSendRequest:
        return g.session.Execute(session.SharedSendCommand{
            Plan:          request.plan,
            TextDirect:    request.directText,
            TextListeners: request.listenersText,
        })
    // broadcast, handoff, and cancel translate without workflow knowledge.
    }
}
```

The gateway knows session planning, detached participant inspection, and
command types, but not loops, stages, workflow phases, or reply semantics.
Planning and inspection occur on the serialized interpreter loop.
`broadcastRequest`, `handoffRequest`, and `cancelRequest` cover the remaining
current staged-dispatch and interrupt paths; they do not require a separate
opaque planning value. The application supplies canonical room-derived values,
such as a resolved handoff source, as data when invoking a workflow. If Step 7
exposes another session-owned planning primitive, add a generic planning or
inspection effect/result pair; do not add stage-specific orchestration to
`Interpreter`.

## Closed effect vocabulary

The initial package-private effects are:

```go
type executeSessionEffect struct {
    target  workflowRef
    request sessionRequest
}

type planSharedSendEffect struct {
    target workflowRef
    alias  string
}

type readParticipantStateEffect struct {
    target workflowRef
}

type startShellEffect struct {
    target  workflowRef
    request shellRequest
}

type appendRecordEffect struct { record room.Record }
type publishEventEffect struct { event Event }
```

The executor uses an exhaustive type switch. Effects never apply themselves to
`*Interpreter`; that would only relocate coupling. The executor targets narrow
capabilities: the session gateway, shell executor, canonical room, and event
dispatcher.

Completions form a closed data-only vocabulary as well:

```go
type workflowCompletion interface{ workflowCompletion() }

type sessionCompletion struct {
    target workflowRef
    err    error
}

type sharedSendPlanCompletion struct {
    target workflowRef
    result sharedSendPlanResult
}

type participantStateCompletion struct {
    target workflowRef
    result participantStateResult
}

type shellCompletion struct {
    target  workflowRef
    request shellRequest
    result  shell.Result
}
```

There are no `any` payloads, callbacks, or workflow-specific executor hooks.

## Effect batches and snapshots

Every application or workflow transition returns an `effectBatch`:

```go
type effectBatch struct {
    effects         []effect
    publishSnapshot bool
}
```

Snapshot publication belongs to the whole causal chain, not one effect.

- The executor ORs `publishSnapshot` across the initial batch, causal
  session-event batches, and result-derived batches.
- Nested replies inherit the accumulated flag.
- Empty transitions may request a snapshot with an empty effect list.
- At most one snapshot is published after the complete synchronous chain has
  settled and all causal session events have been applied.
- An asynchronous shell completion begins a new causal chain and therefore may
  publish its own final snapshot.

Room records and explicit public events remain ordered effects. A final
snapshot does not replace them.

## Deterministic effect algorithm

The operation loop applies one causal chain at a time. Effects returned by a
causal session event are completed before the session-command result is sent
back to its workflow.

The executor's private work queue contains either an effect item or a typed
completion-delivery item. Completion delivery is not a workflow effect and is
never returned by a workflow; it is how the generic executor delays calling
`application.HandleCompletion` until earlier event-derived effects settle.

```text
applyBatch(initial):
    queue = effect items from initial.effects
    publishSnapshot |= initial.publishSnapshot

    while queue is not empty:
        item = pop front

        deliver completion:
            resultBatch = application.HandleCompletion(item.completion)
            queue = effect items from resultBatch.effects + queue
            publishSnapshot |= resultBatch.publishSnapshot

        append record / publish event:
            apply immediately

        start shell:
            launch and continue; its result is a future operation

        plan shared send:
            obtain immutable plan and detached targets from session gateway
            prepend a correlated completion-delivery item to queue

        read participant state:
            copy barrier/routable aliases, statuses, and turn IDs
            prepend a correlated completion-delivery item to queue

        execute session request:
            translate and execute request
            collect session events queued before the post-execution drain

            eventEffects = []
            for each causal event in order:
                update canonical room projection
                eventBatch = application.HandleSessionEvent(event)
                append eventBatch.effects to eventEffects
                publishSnapshot |= eventBatch.publishSnapshot

            queue = eventEffects
                + deliver-completion(target, result)
                + queue

    drain any session events queued before the chain boundary
    if publishSnapshot:
        publish exactly one snapshot
```

Every captured event is projected into the room and offered to the application
before any event-derived effect executes. This preserves the invariant that all
synchronous events from one session command update application state before
another session command may run. After projection, event-derived effects run
in event and batch order. Only after those effects settle does the executor
deliver the command result to its workflow; result-derived effects then run
before effects that originally followed the session request.

If an event-derived effect executes another session request, the executor
repeats the same capture/project/queue procedure: it projects that nested
command's complete captured event burst before executing effects derived from
the burst.

External operations retain the existing outer rule: drain previously queued
session events before applying the operation and again after it completes. A
drained burst uses the same two-phase rule: project and route every event first,
then execute the concatenated event-derived effects.

## Progress invariants

Effect execution has no arbitrary count limit. A valid operation may involve
any number of participants or records, so effect volume is not evidence of a
cycle.

Progress is instead enforced structurally:

- Every planning, session, or shell effect expecting a result registers one
  unique pending `workflowRef` in its workflow generation.
- A matching completion consumes that pending request exactly once before it
  may return further effects.
- Duplicate, unknown, and stale completions return an empty batch.
- A workflow phase defines which result-producing effects it may issue. For
  example, a waiting loop cannot issue another participant dispatch until an
  idle event advances it to condition evaluation.
- The executor uses an iterative queue rather than recursive Go calls, so a
  large valid batch does not grow the call stack.
- State-machine tests cover every phase's permitted effects and ensure one
  input cannot synchronously re-enter the same phase with another pending
  request.

A non-progressing effect cycle is therefore a workflow programming bug caught
by phase and correlation tests, not a user-visible “too many effects” outcome.

## Shell execution and stale completions

`shellRequest` contains presentation-independent execution data:

```go
type shellRequest struct {
    command string
    program string
}
```

The `startShellEffect.target` supplies correlation. The shell executor starts a
child process and enqueues a completion operation containing the unchanged
`workflowRef`, request, and result. It never invokes workflow code from the
shell goroutine.

On the operation loop, `application.HandleCompletion` checks correlation. A
stale completion returns no workflow-transition effects. Existing observable
shell-result recording is preserved independently when required: the
completion operation can append the command record and publish
`ShellCompleted`, but it cannot advance a newer workflow generation.

## Workflow composition

`application` owns a small workflow collection and exposes coordinator-level
operations:

```go
func (a *application) HandleSubmission(
    raw string,
    statement promptlang.Statement,
) effectBatch
func (a *application) HandleSessionEvent(event session.Event) effectBatch
func (a *application) HandleCompletion(completion workflowCompletion) effectBatch
```

The loop workflow is the first consumer. Staged batches are the second. If the
same request, effect, and correlation vocabulary cannot represent both without
workflow-specific cases in `Interpreter`, revise the application-level
vocabulary rather than adding `applyLoop...` or `applyStage...` methods.

## Worked sequence: loop participant turn

Input:

```text
/loop @ada fix tests /until /tests /max 3
```

1. `application.HandleSubmission(raw, statement)` validates the condition and
   asks the loop workflow to start generation 7. The unchanged `raw` value is
   retained for room records and submission events.
2. The workflow returns input-record and `InputAccepted` effects followed by:

   ```text
   executeSessionEffect{
       target:  {kind: loop, generation: 7, requestID: 21},
       request: planAndExecuteSharedSendRequest{alias: "ada", ...},
   }
   ```

3. The session gateway plans and executes the shared send.
4. Synchronous `ParticipantStatusChanged` and `SharedSend` events update the
   room and are offered to all workflows. Event-derived effects settle first.
5. The successful result is routed to loop generation 7/request 21.
6. The loop moves from dispatching to waiting and returns effects for the
   `[loop] turn 1/3...` record/event plus `SubmissionSucceeded`.
7. The chain publishes one final snapshot if any constituent batch requested
   it.

A stop or crash received during step 4 is retained by the loop workflow and
reconciled when step 5 arrives. An idle rollback during dispatch does not start
condition evaluation.

## Worked sequence: loop shell condition

1. A matching participant-idle event reaches a waiting loop at generation 7.
2. The workflow enters evaluating, allocates request 22, and returns:

   ```text
   startShellEffect{
       target:  {kind: loop, generation: 7, requestID: 22},
       request: shellRequest{command: "/tests", program: "go test ./..."},
   }
   ```

3. The shell executor runs outside the operation loop and enqueues its result.
4. The completion operation records/publishes the shell result and routes the
   correlated completion to the application.
5. On failure below `/max`, the loop enters dispatching, allocates request 23,
   and returns another `executeSessionEffect` with the evidence-bearing prompt.
6. On success, cancellation, or maximum turns, it clears generation 7 and
   returns the corresponding loop-status effects.

## Worked sequence: staged dispatch

1. The stage workflow allocates request 29 and returns
   `readParticipantStateEffect`. Its completion provides detached barrier and
   routable participants used to decide immediate dispatch versus staging.
2. For a staged direct send, the workflow then allocates request 30 and returns
   `planSharedSendEffect{target: {stage, 4, 30}, alias: "ada"}`.
3. The correlated planning completion returns an opaque immutable plan and its
   detached targets. The workflow freezes those values with its action, barrier
   aliases, and generation 4. Broadcast and handoff do not need this second
   planning round trip.
4. Lifecycle events update the frozen barrier. Once dispatchable, the workflow
   allocates request 31 and returns `executePlannedSharedSendRequest`,
   `broadcastRequest`, or `handoffRequest` with target `{stage, 4, 31}`.
5. The session gateway translates and executes the request. A planned shared
   send uses the exact plan frozen in step 3 and cannot gain later listeners.
6. Causal departure, delivery, handoff, and status events update the room and
   workflows before the command result is returned.
7. The correlated result commits delivered aliases or clears/discards the
   stage according to existing partial-delivery rules, returning record/event
   effects and requesting one final snapshot.

The effect executor contains no staging branches. Only the session gateway
knows which application-level request maps to which session command.

## Worked sequence: stale shell completion

1. Loop generation 7 starts shell request 22.
2. The participant stops and generation 7 finishes before the process exits.
3. A new loop starts as generation 8 and may allocate request 23.
4. Completion `{loop, 7, 22}` arrives.
5. The completion's shell command record and public completion event are
   preserved if required by current behavior.
6. `application.HandleCompletion` rejects the stale correlation because the
   active loop is generation 8. It returns no loop-transition effects and does
   not alter generation 8.

## Implementation sequence

1. Restore the completed Step 6 implementation.
2. Review and approve this target design and ordering contract.
3. Extract runtime infrastructure only where needed for a readable coordinator.
4. Introduce the minimal request/effect executor and progress-invariant tests.
5. Move the complete loop decision process behind `loopWorkflow`.
6. Verify unchanged loop syntax, output, failure behavior, and ordering.
7. Move staged batches as the second consumer and validate the abstraction.
8. Revisit approvals and snapshot ownership after immutable cached snapshots
   have a separate design.

## Acceptance criteria

- `Interpreter` contains no loop- or stage-specific transition executors.
- `Interpreter` applies effects without knowing their originating workflow.
- Workflows contain no synchronization, goroutines, I/O, room mutation, or
  observer publication.
- Session planning and execution remain serialized behind the session gateway.
- Causal session-event effects settle before command-result workflow replies.
- Snapshot requests coalesce across one complete causal chain.
- Stale asynchronous completions cannot advance a newer workflow generation.
- Pending requests are unique and consumed exactly once; stale, duplicate, and
  phase-invalid completions cannot advance a workflow.
- Loop and staged-batch behavior, output, and ordering remain unchanged except
  for separately documented intentional behavior.
- Race tests, lint, and the full repository test suite pass.
