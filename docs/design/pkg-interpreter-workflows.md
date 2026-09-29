# Interpreter workflow architecture

This document defines the target boundary for extracting interpreter workflows
without weakening the interpreter's serialized ordering guarantees. It guides
GitHub issue #53 and the staged-batch migration.

## Target shape

`Interpreter` remains the public facade and composition root. Its operation
loop is the sole serialized execution context for mutable interpreter
workflows; `interpreterModel` is their structural owner, and the facade does
not implement workflow-specific procedures.

```go
type Interpreter struct {
    model    interpreterModel
    executor interpreterExecutor
}
```

These names describe responsibility boundaries, not field-grouping wrappers:

- `interpreterModel` owns the canonical room, command registry, and loop/future
  stage workflows. It routes inputs, owns decisions, and returns instruction
  sequences. It never runs instructions or calls the executor.
- `interpreterExecutor` owns operation serialization, lifetime cancellation,
  shutdown acceptance, interpreter-owned asynchronous work, the generic
  causal instruction runner, event dispatcher, and session-event inbox.
- `instructionRunner` is executor-owned and runs instructions through narrow
  model, session, shell, completion, publication, and snapshot ports. It
  contains no workflow-specific branches.
- `eventDispatcher` owns observers, queued delivery, its delivery goroutine,
  flush barriers, and event shutdown. Its focused `Publish`, `AddObserver`,
  `Flush`, and `Close` operations preserve publication order without blocking
  the serialized interpreter loop on observers.
- `sessionEventInbox` owns cross-goroutine session-event buffering and its
  coalesced drain wake-up. `Record` reports whether the executor must enqueue a
  drain; `Take` removes the current event burst without changing that marker;
  and `CompleteDrain` atomically clears it only if the inbox is still empty.
  The inbox knows nothing about models, workflows, or instructions.
- `snapshotCache` holds the latest immutable interpreter snapshot. The
  operation loop refreshes it; concurrent and post-shutdown readers receive
  detached copies without touching mutable model state.

Only components whose extraction makes an invariant clearer should be split
out. The first proof is a cohesive loop workflow plus the smallest viable
instruction runner. Executor, dispatcher, and inbox extraction follows only where
it makes the resulting coordinator simpler.

The dependency direction is executor to model. The model never calls the
executor. Approval state is loop-confined model state. `Interpreter.Snapshot`
delegates to the executor: accepted reads settle earlier session events and
refresh the immutable cache on the operation loop, while reads after shutdown
return a detached copy from that cache without accessing the model.

## Confinement rule

Interpreter workflows are confined to the interpreter operation loop. They do
not use locks, start goroutines, perform I/O, execute session commands, mutate
the room, or publish observer events. They own complete decision processes and
return interpreter instructions.

The boundary has three acceptance rules:

1. Adding or changing a loop transition does not require adding a loop-specific
   method to `Interpreter`.
2. The executor-owned runner executes instructions without knowing which workflow
   produced them.
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
  globally unambiguous to the model. The model does not allocate
  request IDs on a workflow's behalf.

Session planning, session execution, and shell requests carry a `workflowRef`.
The model routes their typed completions through one data-oriented entry
point. A workflow accepts a completion only when all three fields match its
current pending request. Otherwise the completion is stale and cannot change
workflow state.

## Session gateway and planning-dependent requests

Workflows cannot construct every `session.Command`. Some commands require a
session-owned plan at execution time, and frozen staged dispatches require
their own planning inputs. Instructions therefore carry interpreter-level session
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
decision made when they were staged, so they use a separate planning instruction and
later execute `executePlannedSharedSendRequest` with the opaque, session-bound
plan returned by the gateway.

```go
type planSharedSendInstruction struct {
    target workflowRef
    alias  string
}

type readParticipantStateInstruction struct {
    target workflowRef
}

type readHandoffSourceInstruction struct {
    target workflowRef
    alias  string
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
cross the workflow boundary as instructions: the workflow does not call the
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
opaque planning value. Handoff obtains its canonical room-derived source
through the correlated read instruction described below. If Step 7 exposes
another session-owned planning primitive, add a generic planning or inspection
instruction/result pair; do not add stage-specific orchestration to
`Interpreter`.

Handoff source resolution is also a correlated read instruction. After the
stage workflow observes the matching completed turn, the runner asks
`modelPort.ReadHandoffSource` for the latest eligible source. This preserves
the output projection/idle ordering guard without giving either the workflow or
executor direct room access.

## Closed instruction vocabulary

The initial package-private instructions are:

```go
type executeSessionInstruction struct {
    target  workflowRef
    request sessionRequest
}

type planSharedSendInstruction struct {
    target workflowRef
    alias  string
}

type readParticipantStateInstruction struct {
    target workflowRef
}

type readHandoffSourceInstruction struct {
    target workflowRef
    alias  string
}

type startShellInstruction struct {
    target  workflowRef
    request shellRequest
}

// Compatibility instructions for the existing command and legacy APIs.
type executeCommandInstruction struct {
    command    session.Command
    completion submissionCompletion
}

type startUserShellInstruction struct {
    raw     string
    command string
    program string
}

type readRosterInstruction struct { raw string }
type requestCloseInstruction struct{}
type shutdownSessionInstruction struct{}

type appendRecordInstruction struct { record room.Record }
type publishEventInstruction struct { event Event }
type publishSnapshotInstruction struct{}
type requestSnapshotInstruction struct{}
```

The compatibility instructions preserve existing command behavior while issue
#38 is parked:

- `executeCommandInstruction` executes native and fallback commands whose
  complete `session.Command` already exists. The fallback use disappears when
  `ExecuteLegacy` and `SubmitWithFallback` are removed; planning-dependent
  workflows continue to use `executeSessionInstruction` and `sessionRequest`.
- `startUserShellInstruction` preserves ordinary user shell execution, which
  has different submission timing from a workflow-correlated shell request.
- `readRosterInstruction` keeps session-owned participant inspection outside
  the model and returns a detached completion.
- `requestCloseInstruction` stops accepting operations, while
  `shutdownSessionInstruction` shuts down the session and projects its final
  causal events. Their separation preserves `/quit` ordering.

These instructions are part of the closed exhaustive vocabulary for as long as
their public compatibility APIs remain. They are not workflow-specific runner
hooks.

The runner uses an exhaustive type switch. Instructions never apply themselves
to `*Interpreter`; that would only relocate coupling. The runner targets narrow
capabilities: `modelPort`, the session gateway, shell executor, and event
dispatcher. The model port owns canonical-room operations:

- `ApplySessionEvent` updates the room projection and routes the event to
  workflows.
- `AppendRecord` mutates the canonical room.
- `ReadHandoffSource` resolves room-derived data.
- `Snapshot` reads model state.

The runner controls when these operations occur but never reads or mutates the
room directly. The session gateway is executor-owned; it is not part of
`interpreterModel`.

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

type submissionCompletion struct {
    raw       string
    operation string
    err       error
}

type rosterCompletion struct {
    raw          string
    participants []participant.View
}
```

`submissionCompletion` and `rosterCompletion` are compatibility completions
routed through the same model-owned decision boundary. They contain detached
data and introduce no callback from the model to the executor.

There are no `any` payloads, callbacks, or workflow-specific runner hooks.

## Instruction sequences and snapshots

Every model or workflow transition returns a finite ordered sequence:

```go
type instruction interface { instruction() }
type instructionSequence []instruction
```

There is no batch or transition wrapper. Snapshot publication is requested by
adding `requestSnapshotInstruction{}` to the sequence. It belongs to the whole
causal chain, not the point where the instruction appears.

- The runner records whether it encounters a snapshot request in the initial
  sequence or any causal event- or completion-derived sequence.
- Nested sequences contribute to the same coalesced request.
- A transition that otherwise has no work returns a sequence containing only
  `requestSnapshotInstruction{}`.
- At most one snapshot is published after the complete synchronous chain has
  settled and all causal session events have been applied.
- An asynchronous shell completion begins a new causal chain and therefore may
  publish its own final snapshot.

Room records and explicit public events remain ordered instructions. A final
snapshot does not replace them.

`publishSnapshotInstruction` is the compatibility form for existing commands
whose public contract places `StateChanged` at a specific point before their
terminal event. It publishes immediately when reached. New workflow transitions
use `requestSnapshotInstruction`, which coalesces publication after the causal
chain settles. Keeping the distinction explicit preserves event ordering while
the legacy command surface is still supported.

## Deterministic instruction algorithm

The operation loop applies one causal chain at a time. Instructions returned by a
causal session event are completed before the session-command result is sent
back to its workflow.

The runner's private work queue contains either an instruction or a typed
completion-delivery item. Completion delivery is not a workflow instruction
and is never returned by a workflow; it is how the runner delays calling
`interpreterModel.ApplyCompletion` until earlier event-derived instructions settle.

```text
run(initialSequence):
    queue = instructions from initialSequence
    snapshotRequested = false

    while queue is not empty:
        item = pop front

        deliver completion:
            resultSequence = model.ApplyCompletion(item.completion)
            queue = instructions from resultSequence + queue

        request final snapshot:
            snapshotRequested = true

        publish compatibility snapshot:
            publish the current composed snapshot immediately

        append record:
            model.AppendRecord(record)

        publish event:
            dispatcher.Publish(event)

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

            eventInstructions = []
            for each causal event in order:
                eventSequence, applied = model.ApplySessionEvent(event)
                if not applied:
                    continue
                append eventSequence to eventInstructions

            queue = eventInstructions
                + deliver-completion(target, result)
                + queue

    drain any session events queued before the chain boundary
    if snapshotRequested:
        publish exactly one snapshot
```

`model.ApplySessionEvent` projects each captured event into the room and routes
it to workflows. The runner invokes it for every event in the captured burst
before any event-derived instruction executes. This preserves the invariant
that all
synchronous events from one session command update model state before
another session command may run. After projection, event-derived instructions
run in event and sequence order. Only after those instructions settle does the
runner deliver the command result to its workflow; result-derived instructions
then run before instructions that originally followed the session request.

`model.ApplySessionEvent` filters stale `ApprovalCleared` events before room
projection or workflow routing. A rejected clear does not request a snapshot,
preserving the pre-decomposition contract.

If an event-derived instruction executes another session request, the runner
repeats the same capture/project/queue procedure: it projects that nested
command's complete captured event burst before executing instructions derived
from the burst.

External operations retain the existing outer rule: drain previously queued
session events before applying the operation and again after it completes. A
drained burst uses the same two-phase rule: project and route every event first,
then run the concatenated event-derived instructions.

## Progress invariants

Instruction execution has no arbitrary count limit. A valid operation may
involve any number of participants or records, so instruction volume is not
evidence of a cycle.

Progress is instead enforced structurally:

- Every planning, session, or shell instruction expecting a result registers one
  unique pending `workflowRef` in its workflow generation.
- A matching completion consumes that pending request exactly once before it
  may return further instructions.
- Duplicate, unknown, and stale completions return an empty sequence.
- A workflow phase defines which result-producing instructions it may issue. For
  example, a waiting loop cannot issue another participant dispatch until an
  idle event advances it to condition evaluation.
- The runner uses an iterative queue rather than recursive Go calls, so a
  large valid sequence does not grow the call stack.
- State-machine tests cover every phase's permitted instructions and ensure one
  input cannot synchronously re-enter the same phase with another pending
  request.

A non-progressing instruction cycle is therefore a workflow programming bug
caught by phase and correlation tests, not a user-visible “too many
instructions” outcome.

## Shell execution and stale completions

`shellRequest` contains presentation-independent execution data:

```go
type shellRequest struct {
    command string
    program string
}
```

The `startShellInstruction.target` supplies correlation. The shell executor starts a
child process and enqueues a completion operation containing the unchanged
`workflowRef`, request, and result. It never invokes workflow code from the
shell goroutine.

On the operation loop, `interpreterModel.ApplyCompletion` checks correlation. A
stale completion returns no workflow-transition instructions. Existing observable
shell-result recording is preserved independently when required: the
completion operation can append the command record and publish
`ShellCompleted`, but it cannot advance a newer workflow generation.

## Workflow composition

`interpreterModel` owns a small workflow collection and exposes coordinator-level
operations:

```go
func (m *interpreterModel) PreflightSubmission(raw string) instructionSequence
func (m *interpreterModel) Submit(
    raw string,
    statement promptlang.Statement,
    fallback session.Command,
) instructionSequence
func (m *interpreterModel) ApplySessionEvent(event session.Event) instructionSequence
func (m *interpreterModel) ApplyCompletion(completion workflowCompletion) instructionSequence
```

These operations are state transitions, not parsers or instruction factories.
`PreflightSubmission` preserves model-owned rejection decisions that must occur
before parsing, including the pending-stage gate. `Submit` applies an already
parsed statement, while the `Apply...` operations apply external facts to model
state. Each returns the instructions caused by that transition. The submit
operation never reads stage or workflow state directly. Unknown-command
classification is also model-owned: `Submit` returns a `publishEventInstruction`
for `UnknownCommand` rather than an executor-facing “handled” flag.

The loop workflow is the first consumer. Staged batches are the second. If the
same request, instruction, and correlation vocabulary cannot represent both without
workflow-specific cases in `Interpreter`, revise the model-level
vocabulary rather than adding `applyLoop...` or `applyStage...` methods.

## Worked sequence: loop participant turn

Input:

```text
/loop @ada fix tests /until /tests /max 3
```

1. `interpreterModel.Submit(raw, statement)` validates the condition and
   asks the loop workflow to start generation 7. The unchanged `raw` value is
   retained for room records and submission events.
2. The workflow returns input-record and `InputAccepted` instructions followed by:

   ```text
   executeSessionInstruction{
       target:  {kind: loop, generation: 7, requestID: 21},
       request: planAndExecuteSharedSendRequest{alias: "ada", ...},
   }
   ```

3. The session gateway plans and executes the shared send.
4. Synchronous `ParticipantStatusChanged` and `SharedSend` events update the
   room and are offered to all workflows. Event-derived instructions settle first.
5. The successful result is routed to loop generation 7/request 21.
6. The loop moves from dispatching to waiting and returns instructions for the
   `[loop] turn 1/3...` record/event plus `SubmissionSucceeded`.
7. A `requestSnapshotInstruction` causes one final snapshot after the causal
   sequence settles.

A stop or crash received during step 4 is retained by the loop workflow and
reconciled when step 5 arrives. An idle rollback during dispatch does not start
condition evaluation.

## Worked sequence: loop shell condition

1. A matching participant-idle event reaches a waiting loop at generation 7.
2. The workflow enters evaluating, allocates request 22, and returns:

   ```text
   startShellInstruction{
       target:  {kind: loop, generation: 7, requestID: 22},
       request: shellRequest{command: "/tests", program: "go test ./..."},
   }
   ```

3. The shell executor runs outside the operation loop and enqueues its result.
4. The completion operation records/publishes the shell result and routes the
   correlated completion to the model.
5. On failure below `/max`, the loop enters dispatching, allocates request 23,
   and returns another `executeSessionInstruction` with the evidence-bearing prompt.
6. On success, cancellation, or maximum turns, it clears generation 7 and
   returns the corresponding loop-status instructions.

## Worked sequence: staged dispatch

1. The stage workflow allocates request 29 and returns
   `readParticipantStateInstruction`. Its completion provides detached barrier and
   routable participants used to decide immediate dispatch versus staging.
2. For a staged direct send, the workflow then allocates request 30 and returns
   `planSharedSendInstruction{target: {stage, 4, 30}, alias: "ada"}`.
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
   instructions including `requestSnapshotInstruction{}`.

The instruction runner contains no staging branches. Only the session gateway
knows which interpreter-level request maps to which session command.

## Worked sequence: stale shell completion

1. Loop generation 7 starts shell request 22.
2. The participant stops and generation 7 finishes before the process exits.
3. A new loop starts as generation 8 and may allocate request 23.
4. Completion `{loop, 7, 22}` arrives.
5. The completion's shell command record and public completion event are
   preserved if required by current behavior.
6. `interpreterModel.ApplyCompletion` rejects the stale correlation because the
   active loop is generation 8. It returns no loop-transition instructions and does
   not alter generation 8.

## Implementation sequence

1. Restore the completed Step 6 implementation.
2. Review and approve this target design and ordering contract.
3. Complete the responsibility decomposition tracked in
   `INTERPRETER_DECOMPOSITION_PLAN.md`, including executor ownership of the
   instruction runner, inbox, and dispatcher.
4. Keep approvals loop-confined and serve external reads through the immutable
   snapshot cache.
5. Only after decomposition, resume staged batches as the second workflow
   consumer and validate the abstraction.

## Acceptance criteria

- `Interpreter` contains no loop- or stage-specific instruction handlers.
- The executor-owned runner executes instructions without knowing their
  originating workflow.
- Workflows contain no synchronization, goroutines, I/O, room mutation, or
  observer publication.
- Session planning and execution remain serialized behind the session gateway.
- Causal session-event instructions settle before command-result workflow replies.
- Snapshot requests coalesce across one complete causal chain.
- Stale asynchronous completions cannot advance a newer workflow generation.
- Pending requests are unique and consumed exactly once; stale, duplicate, and
  phase-invalid completions cannot advance a workflow.
- Loop and staged-batch behavior, output, and ordering remain unchanged except
  for separately documented intentional behavior.
- Race tests, lint, and the full repository test suite pass.
