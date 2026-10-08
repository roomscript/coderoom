# Interpreter workflow architecture

This document describes the implemented interpreter workflow boundary and
serialized ordering guarantees. The responsibility decomposition and staged
submission migration are complete; normal package-graph tests enforce the
interpreter/UI boundary.

## Entry-point map for the core model (#55)

Start with [interpreter concepts](../../internal/interpreter/CONCEPTS.md), then
the API files. This map identifies the current paths beneath those entry points;
it does not imply the core algorithm refactor is complete.

| Concept / responsibility | Entry point | Current algorithm / implementation |
|---|---|---|
| Submit a request | `Submit` in `api_requests.go` | `handleInput` in `core_input.go` checks the pending-stage gate, parses input, and runs `model.Submit` decisions. |
| Edit or discard pending work | `TakeStageForEdit`, `DiscardStage` in `api_requests.go` | Atomic stage operations remove the retained plan, then return the draft or discard result. |
| Interrupt work | `InterruptAndDispatchStage` in `api_requests.go` | Stage interruption selects cancellable blockers; lifecycle events establish readiness. |
| Resolve an approval | `ResolveApproval` in `api_requests.go` | `resolveApprovalOperation.apply` validates the choice, sends it to session, and updates approval state. |
| Receive session facts | `sessionObserver.OnEvent` in `core_events.go` | Buffer incoming facts; `ApplySessionEvents` and `ApplySessionEvent` update the room/approval state and advance affected workflows. |
| Receive ordinary shell results | `shellCompletedOperation.apply` in `core_events.go` | Record the shell outcome and publish observable changes. |
| Receive workflow shell results | `workflowShellCompletedOperation.apply` in `core_events.go` | Route the result to current workflow work; stale results cannot advance a replacement workflow. |
| Receive synchronous execution results | `instructionRunner.executeCommand` / `executeSession` | Apply causal events before delivering the result to the model. These are not asynchronous waits. |
| Publish interpreter events | `publish` in `api_events.go` | Queue facts for ordered observer delivery; publication does not execute a workflow. |
| Subscribe to published events | `WithObserver` in `api_events.go` | Install observers before startup; deliver initial state followed by ordered events. |
| Inspect application state | `Snapshot`, `ResolveCommand` in `api.go`; record helpers in `api_records.go` | Return detached state or command definitions without initiating work. |
| Construct and close | `New` in `interpreter.go`, `Close` in `api.go` | Construct dependencies and start execution; close settles accepted operations and shuts down owned work. |
| Configure shell execution | `WithShellRunner` in `api_shell.go` | Replace the shell dependency before startup. |

The API methods and event publication boundary are collected without changing
signatures or behavior. Public state/event DTOs remain in `types.go`, and the
session dependency contract remains in `session.go`. Construction stays separate
from request handling.

`core_input.go` exposes the input entry algorithm and its model decisions:
check whether input is allowed, parse it, select its workflow, and execute its
returned actions. `core_events.go` exposes incoming facts, complete-burst state
updates, workflow advancement, and asynchronous shell resumption. Outgoing event
publication remains in `api_events.go`. Queue draining and execution mechanics
remain in the executor and runner.

These entry algorithms preserve the existing causal order: project the complete
session-event burst before running derived actions, then apply the execution
result. Routing outcomes go to execution completion rather than room projection;
stale approval-clear events cannot clear a newer approval.

The command-specific preparation and wait/resume decisions still live in the
workflows. The next readability checkpoint is to make those paths expose plans
and real waits without requiring readers to reconstruct instruction/completion
mechanics. These core files are navigation and algorithm entry points, not a
claim that the remaining workflow refactor is complete.

## Implemented shape

`Interpreter` remains the public facade and composition root. Its operation
loop is the sole serialized execution context for mutable interpreter
workflows; `interpreterModel` is their structural owner, and the facade does
not implement workflow-specific procedures.

```go
type Interpreter struct {
    model    *interpreterModel
    executor *interpreterExecutor
}
```

These names describe responsibility boundaries, not field-grouping wrappers:

- `interpreterModel` owns the canonical room, command registry, and loop and
  stage workflows. It routes inputs, owns decisions, and returns instruction
  sequences. It never runs instructions or calls the executor.
- `interpreterExecutor` owns operation serialization, lifetime cancellation,
  shutdown acceptance, interpreter-owned asynchronous work, the generic
  causal instruction runner, event dispatcher, and session-event inbox.
- `instructionRunner` is executor-owned and runs instructions through narrow
  model, session, shell, completion, publication, and snapshot ports. It
  contains no workflow-specific branches.
- `eventDispatcher` owns observers, queued delivery, its delivery goroutine,
  flush barriers, and event shutdown. Observers are fixed by `WithObserver`
  before startup. Its focused `Publish`, `Flush`, and `Close` operations preserve
  publication order without blocking
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

The currently implemented closed request vocabulary is:

```go
type sessionRequest interface{ sessionRequest() }

type createPlanAndExecuteParticipantSendRequest struct {
    alias         string
    message    string
    notice string
}

type executePlannedParticipantSendRequest struct {
    plan          session.ParticipantSendPlan
    message    string
    notice string
}

type broadcastRequest struct {
    aliases []string
    text    string
}
```

Immediate loop turns use `createPlanAndExecuteParticipantSendRequest`, which deliberately
plans immediately before execution.

The implemented staged-submission vocabulary includes handoff and cancellation
requests:

```go
type handoffRequest struct {
    fromAlias   string
    toAlias     string
    requiredReadyAliases []string
    source      session.HandoffSource
}

type cancelRequest struct {
    alias string
}
```

Staged sends must preserve the routing decision made when they were staged, so
they use a separate planning instruction and
later execute `executePlannedParticipantSendRequest` with the opaque, session-bound
plan returned by the gateway.

```go
type prepareSendInstruction struct {
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

type sendPlanResult struct {
    participants []participantState
    plan    session.ParticipantSendPlan
    targets []string
}

type participantState struct {
    alias  string
    status participant.Status
    turnID uint64
}

type participantStateResult struct {
    readinessRequirements []participantState
}

type handoffSourceResult struct {
    source session.HandoffSource
    ok     bool
}
```

Planning and participant inspection are synchronous and serialized but still
cross the workflow boundary as instructions: the workflow does not call the
session. Their correlated completions freeze the opaque plan, detached target
aliases, and readiness requirements in staged state. `participantState` deliberately
omits agents and other live session-owned references.

The executor-owned session gateway currently translates the loop request
immediately before execution:

```go
func (g sessionGateway) Execute(request sessionRequest) error {
    switch request := request.(type) {
    case createPlanAndExecuteParticipantSendRequest:
        plan := g.session.CreateParticipantSendPlan(request.alias)
        return g.session.Execute(session.SendToParticipantCommand{
            Plan:          plan,
            Message:    request.message,
            Notice: request.notice,
        })
    }
}
```

The gateway knows session planning and command types, but not loop or stage
phases or reply semantics. Planning and detached participant inspection occur
on the serialized interpreter loop. `handoffRequest` covers handoff dispatch;
`cancelRequest` covers interrupt paths. Neither requires a separate
opaque planning value. Handoff obtains its canonical room-derived source
through the correlated read instruction described below. If Step 7
exposes another session-owned planning primitive, add a generic planning or
inspection instruction/result pair; do not add stage-specific orchestration to
`Interpreter`.

Interrupt-and-dispatch emits one correlated `cancelRequest` per currently
blocked, unacknowledged alias. Each successful request records its own progress
before causal events and remains acknowledged in stage state. A retry after a
partial failure therefore targets only failed aliases and cannot cancel a
successful alias twice.

Handoff source resolution is a correlated read instruction.
After the stage workflow observes the matching completed turn, the runner asks
`modelPort.ReadHandoffSource` for the latest eligible source. This preserves
the output projection/idle ordering guard without giving either the workflow or
executor direct room access.

## Closed instruction vocabulary

The currently implemented package-private instructions are:

```go
type executeSessionInstruction struct {
    target           workflowRef
    request          sessionRequest
    recordsOnSuccess []room.Record
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
type prepareSendInstruction struct {
    target workflowRef
    alias  string
}
type planBroadcastInstruction struct { target workflowRef }
type readParticipantStateInstruction struct { target workflowRef }
type readHandoffSourceInstruction struct {
    target workflowRef
    alias  string
}
type requestCloseInstruction struct{}
type shutdownSessionInstruction struct{}

type appendRecordInstruction struct { record room.Record }
type publishEventInstruction struct { event Event }
type publishSnapshotInstruction struct{}
type requestSnapshotInstruction struct{}
```

For routing commands, `recordsOnSuccess` are applied when at least one recipient
accepted delivery, including partial success. Their routing fields come from
`RoutingCompleted`, not frozen plan targets or the returned error. They are
applied before the causal event burst; workflow completion follows those events.
Non-routing commands retain success-only behavior. An empty list preserves the
normal event-then-completion order.

`prepareSendInstruction`, `planBroadcastInstruction`,
`readParticipantStateInstruction`, and `readHandoffSourceInstruction` provide
the frozen-stage inputs. Shared sends carry their policy-aware frozen plan;
broadcasts carry their frozen alias list directly. Participants joining after
planning cannot become recipients.

The executor instructions preserve native command behavior:

- `executeCommandInstruction` executes native commands whose complete
  `session.Command` already exists. Planning-dependent workflows use
  `executeSessionInstruction` and `sessionRequest`; the legacy public execution
  gateway has been removed.
- `startUserShellInstruction` preserves ordinary user shell execution, which
  has different submission timing from a workflow-correlated shell request.
- `readRosterInstruction` keeps session-owned participant inspection outside
  the model and returns a detached completion.
- `requestCloseInstruction` stops accepting operations, while
  `shutdownSessionInstruction` shuts down the session and projects its final
  causal events. Their separation preserves `/quit` ordering.

These instructions are part of the closed exhaustive vocabulary. They are not
workflow-specific runner hooks.

The runner uses an exhaustive type switch. Instructions never apply themselves
to `*Interpreter`; that would only relocate coupling. The runner targets narrow
capabilities: `modelPort`, the session gateway, shell executor, and event
dispatcher. The model port owns canonical-room operations:

- `ApplySessionEvent` updates the room projection and routes the event to
  workflows.
- `AppendRecord` mutates the canonical room.
- `Snapshot` reads model state.

The runner controls when these operations occur but never reads or mutates the
room directly. The session gateway is executor-owned; it is not part of
`interpreterModel`.

Completions form a closed data-only vocabulary as well:

```go
type workflowCompletion interface{ workflowCompletion() }

type sessionCompletion struct {
    routing session.RoutingResult
    target workflowRef
    err    error
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

Correlated shared-send planning and participant-state completions provide
frozen routing and readiness inputs to the implemented stage workflow.

`submissionCompletion` and `rosterCompletion` are native command completions
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

`publishSnapshotInstruction` is the immediate publication form for native commands
whose public contract places `StateChanged` at a specific point before their
terminal event. It publishes immediately when reached. New workflow transitions
use `requestSnapshotInstruction`, which coalesces publication after the causal
chain settles. Keeping the distinction explicit preserves event ordering across
native command and workflow transitions.

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

        publish immediate snapshot:
            publish the current composed snapshot immediately

        append record:
            model.AppendRecord(record)

        publish event:
            dispatcher.Publish(event)

        start shell:
            launch and continue; its result is a future operation

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
func (m *interpreterModel) CheckInputAllowed(raw string) instructionSequence
func (m *interpreterModel) Submit(
    raw string,
    statement promptlang.Statement,
    fallback session.Command,
) instructionSequence
func (m *interpreterModel) ApplySessionEvent(
    event session.Event,
) (instructionSequence, bool)
func (m *interpreterModel) ApplyCompletion(completion workflowCompletion) instructionSequence
```

These operations are state transitions, not parsers or instruction factories.
`CheckInputAllowed` preserves model-owned rejection decisions that must occur
before parsing, including the pending-stage gate. `Submit` applies an already
parsed statement, while the `Apply...` operations apply external facts to model
state. Each returns the instructions caused by that transition. The submit
operation never reads stage or workflow state directly. Unknown-command
classification is also model-owned: `Submit` returns a `publishEventInstruction`
for `UnknownCommand` rather than an executor-facing “handled” flag.

`ApplySessionEvent` returns `applied == false` when the event must be discarded
before room projection, workflow routing, and snapshot publication. This is
currently used for stale `ApprovalCleared` events; the Boolean therefore forms
part of the causal-ordering contract rather than being generic “handled” state.

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
       request: createPlanAndExecuteParticipantSendRequest{alias: "ada", ...},
   }
   ```

3. The session gateway plans and executes the shared send.
4. Lifecycle events update the room and workflows. The runner captures the
   single `RoutingCompleted` result from the execution burst for its correlated
   command completion; the outcome alone does not trigger a state snapshot.
5. The actual routing result is routed to loop generation 7/request 21.
6. If the primary recipient accepted delivery, the loop moves to waiting and returns instructions for the
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

## Stage algorithm for the readability refactor (#55)

This is the behavior-preserving algorithm to make visible in the implementation.
It describes existing behavior, not a new execution mechanism. Shared readiness
and interruption mechanics support explicit send, broadcast, and handoff decisions.
The first implementation checkpoint is one complete addressed-send path, reviewed
for readability before extending the structure to the other commands.

```text
submit staged input:
    freeze the command's recipients and readiness requirements
    reject unavailable required participants or an empty audience
    publish input acceptance
    if recipients or required source output are not ready:
        retain the stage and report queued submission success
        wait for lifecycle and output events
    otherwise:
        begin dispatch

advance waiting stage after a relevant event:
    update readiness, departures, and required source completion
    if a required participant is unavailable or no recipients remain:
        discard the stage with an explanation
        return
    if recipients or required source output are not ready:
        retain the stage and refresh its presentation
        return
    begin dispatch

begin dispatch:
    for a handoff, resolve completed context from the canonical room
    if no eligible handoff source exists:
        clear the stage and report failure
        return
    request delivery to the remaining eligible frozen recipients

finish delivery:
    record input if any recipient accepted delivery, before causal output
    project the complete causal session-event burst
    apply its derived instructions before the correlated delivery completion
    clear the stage and report actual deliveries and failures
    publish the settled snapshot
```

Acceptance, queued submission success, and delivery are distinct milestones.
`InputAccepted` follows planning validation. A waiting stage reports
`SubmissionSucceeded` when retained; an immediate dispatch reports its submission
outcome after execution. Later failure of a queued dispatch produces
`OperationFailed`, while an immediate failure produces `SubmissionFailed`.
`StagedInputDispatched` reports delivered aliases even on partial success; input
records retain delivered, failed, and unattempted routing from the session result.
No delivery means no submitted user record. Delivery does not mean agent work
has completed.

Command-specific decisions remain explicit:

| Command | Frozen decision | Waiting and departure rules |
|---|---|---|
| Addressed send | Session-owned primary target and policy-enabled notice recipients | Wait for frozen recipients, including startup. Losing the primary target discards the stage; departed notice recipients are removed from its plan. |
| Broadcast | Audience selected during planning | Wait for frozen recipients. Departed recipients are removed; discard only when no recipients remain. |
| Handoff | Source, destination, and readiness barrier | Source and destination must remain available. Wait for barrier readiness and completed source output before resolving context. Source is context metadata, not a delivery recipient. |

Later joiners and same-alias replacements do not enlarge the frozen audience.
Idle status alone does not establish startup readiness: startup waits end on
`AgentReady`. Handoff output waits apply to active turns, not startup or maintenance;
stale turn output cannot satisfy the current source-completion requirement.

### Edit, discard, and interrupt transitions

These operations run atomically on the interpreter operation loop. Already
buffered session events settle before an operation runs, so automatic dispatch
can win before edit or discard. In that case the operation finds no stage and
returns false. Removing a stage invalidates its pending correlated completions.

```text
take stage for edit:
    if no stage remains, return no draft
    remove the stage, refresh presentation, and return its original input

discard stage explicitly:
    if no stage remains, return false
    remove the stage, refresh presentation, and return true

interrupt and dispatch:
    if no stage remains or cancellation requests are pending, return false
    recompute readiness blockers
    if none remain:
        reject a duplicate interrupt request
        mark interruption requested and resume dispatch or source resolution
        return true
    select blocked active turns not already successfully cancelled
    if none are cancellable, return false
    mark interruption requested and issue one cancellation per selected alias
    return true

receive cancellation completion:
    remove the matching pending request
    retain successful cancellation acknowledgement, or report its failure
    refresh presentation
    let causal lifecycle and output events drive readiness and dispatch
```

Cancellation acceptance does not establish readiness. Startup and maintenance
continue waiting and are never cancellation targets. After partial cancellation
failure, retries target only aliases without successful acknowledgement. Explicit
discard and edit do not emit the explanatory discard record/event used when a
required participant becomes unavailable.

Existing regression anchors are `TestStageWorkflow_freezesSendPlanAndDispatchesWhenReady`,
`TestStageWorkflow_discardsSendWhenAddressedTargetDeparts`,
`TestStageWorkflow_departedBroadcastTargetDoesNotBlockRemainingTargets`,
`TestStageWorkflow_handoffWaitsForSourceProjectionAndIdle`,
`TestSubmit_planningDuringIdleBeforeSessionReadyWaitsForStartup`,
`TestStageOperations_interruptDispatchesAfterCausalIdle`,
`TestStageOperations_partialCancelRetryTargetsOnlyFailures`,
`TestStageOperations_autoDispatchWinsBeforeEditOrDiscard`, and
`TestInstructionRunner_partialDeliveryRecordsInputBeforeCausalOutput`.

### Addressed-send implementation checkpoint

`workflow_stage_send.go` owns the addressed-send algorithm. One synchronous
preparation instruction obtains the session routing plan and readiness facts.
`prepareSend` freezes the plan, rejects unavailable required targets, accepts
input, and dispatches now or retains the plan. `resumeSendOnReadiness` is the
resumption entry point: primary departure discards the plan, unmet readiness
continues waiting, and readiness starts delivery. `startSendDispatch` removes
departed notice recipients from the frozen routing plan before requesting delivery.
Preparation results are consumed once; stale or repeated results cannot restart
retained work.

Readiness updates, atomic stage operations, and interruption remain shared.
`workflow_stage_delivery.go` owns the shared completion and outcome reporting.
`workflowCollection` routes typed completions directly to their handlers; no
second production completion switch intervenes. The instruction runner's causal
ordering remains unchanged. `TestStageWorkflow_replacedSendIgnoresOldCompletions`
protects the replacement stage after edit or discard.

This is the first readability checkpoint, not completion of #55. Stage state,
broadcast/handoff transitions, and the remaining instruction-dispatch invariant
panics still need their planned review. No new subpackage is needed for this path.

## Worked sequence: staged dispatch

1. The stage workflow requests preparation: `prepareSendInstruction` for an
   addressed send, `planBroadcastInstruction` for a broadcast, or participant
   inspection for a handoff.
2. Send preparation reads the routing plan and participant readiness synchronously
   on the serialized session path, then returns one `sendPlanResult`. The
   `prepareSend` algorithm freezes those values, validates the primary target,
   accepts input, and dispatches now or retains pending work. There is no separate
   send-readiness completion. Broadcast planning still requests participant
   inspection separately.
3. Sends and broadcasts retain readiness requirements for their frozen aliases.
   Handoff retains its readiness barrier. A retained send returns control and
   resumes through `resumeSendOnReadiness` after a lifecycle event.
4. Lifecycle events update the frozen readiness requirements. Once dispatchable, the workflow
   allocates request 31. Handoffs first return
   `readHandoffSourceInstruction`, then use its correlated canonical-room
   result; sends and broadcasts proceed directly to execution.
5. The session gateway translates and executes the request. A planned shared
   send uses the plan frozen in step 3 after removing notice recipients that departed
   while staged; it cannot gain later or same-alias replacement notice recipients.
   Handoff execution receives the resolved source value and frozen active
   required-ready aliases in `handoffRequest`.
6. When any delivery is accepted, sends, broadcasts, and handoffs commit the
   submitted user record before projecting the causal burst, using actual
   accepted, failed, and unattempted aliases. A handoff records its destination
   as the recipient; its source is context metadata, not a delivery target.
   `HandoffDelivered` adds accepted context to the room, while `RoutingCompleted`
   is captured for the command completion, including partial failure.
7. The correlated result commits delivered aliases or clears/discards the
   stage according to existing partial-delivery rules, returning record/event
   instructions including `requestSnapshotInstruction{}`.

The instruction runner contains no staging branches. It applies the generic,
record-only success-before-events data carried by an execution instruction
without knowing which workflow or request selected it. The narrow record list
cannot introduce nested execution or workflow control. Only the session
gateway knows which interpreter-level request maps to which session command.

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

## Implementation status

The responsibility decomposition, bounded loops, and staged submissions are
implemented. The TUI uses construction-time observers and interpreter events;
execution compatibility and duplicate UI workflow state have been removed.
Normal package-graph tests enforce the completed dependency boundary.

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
- Loop and staged-submission behavior, output, and ordering remain unchanged except
  for separately documented intentional behavior.
- Race tests, lint, and the full repository test suite pass.
