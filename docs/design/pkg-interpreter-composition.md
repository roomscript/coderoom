# Interpreter composition: discussion draft

Status: incremental migration. The facade selects `/who`, `/shell`, addressed
sends, broadcasts and handoffs through its registry and owns their lifecycles. The monolith
supplies
serialized Session execution and canonical publication.

This follows [#55](https://github.com/roomscript/coderoom/issues/55) and considers
[#39](https://github.com/roomscript/coderoom/issues/39). The existing contracts in
[pkg-interpreter.md](pkg-interpreter.md) and
[pkg-interpreter-workflows.md](pkg-interpreter-workflows.md) remain authoritative.

## Public facade and monolith fallback

The current interpreter implementation has moved to `internal/interpreter/monolith`
behind its existing interface. The public `internal/interpreter` facade initially
delegates unsupported requests to one instance. `/who`, `/shell`, addressed sends, broadcasts and handoffs are selected in the facade
and admitted through the shared coordinator bridge. Shared `api` contracts keep front-end
imports stable and let both implementations publish identical event and snapshot
types without importing the parent package. `runtime` and `std` remain separate.

Keep Session execution and canonical publication on one serialized lane during
migration. The new interpreter schedules its own command algorithm there; the
monolith supplies `Schedule` rather than receiving a callback that creates or
returns a lifecycle. The new interpreter calls `AdmitCommand`, prepares the
invocation, constructs/starts its existing concrete `InvocationLifecycle`, and
retains it in its own pending map. Completion acceptance, duplicate rejection,
identity and cleanup all belong to the new interpreter. No Execution interface
or command execution wrapper is involved.

`AdmitCommand` returns a `CommandSubmission` containing invocation-scoped Context
and publication methods, or nil after publishing rejection. Admission publishes
no accepted input. The new interpreter chooses acceptance records/routing, error
classification and direct versus queued terminal events; the monolith publishes
the supplied events in causal order and attaches source diagnostics. Its methods run on
the shared lane; only `QueueCompletion` is producer-safe. The new interpreter
explicitly publishes queued acceptance after Start and direct-send submission
results after delivery. It publishes completion records/outcomes after settling
the lifecycle. Work running on the
lane must not call blocking queue methods such as Snapshot or Close.

The monolith adapter retains completion work within a native routing burst. The
new interpreter settles that work before the monolith projects causal events;
input records publish before those events and terminal outcomes afterward.
For other completions, work runs on the same queue. Callbacks only queue work,
which avoids reentering lifecycle transitions from Start/Go. Failed starts and
late/duplicate results cannot publish because the new interpreter removes their
pending identity. The monolith stores and invokes no migrated lifecycle.

After execution and completion draining, `WithShutdown` invokes the new
interpreter's cleanup hook on that lane, before publication closes. The new
interpreter closes remaining lifecycles and publishes their outcomes in
submission order. This also handles parent-context cancellation. The monolith
keeps worker cancellation and canonical projection until those move in a later
checkpoint; it never adopts new lifecycle or stage policy.

`/who`, `/shell`, addressed sends, broadcasts, handoffs, definitions and named
invocations use this path. Other commands delegate to the monolith before
execution; never retry fallback after selected execution starts. `SubmitCommand`
remains a temporary legacy bridge. The destination is the new interpreter
replacing and removing the monolith. Real-Session tests cover busy/startup
waiting, departures, cleanup, direct/partial delivery and causal ordering.
Scheduled turns drain their admitted native deliveries before the next queued
input can inspect readiness. This preserves submission order across modular
sends and fallback work without adding an execution lane.

Existing implementation-file references below describe the `monolith` package
unless qualified otherwise.

## Current responsibility map

| Responsibility | Current implementation | Composition opportunity |
|---|---|---|
| Syntax and typed statements | `internal/promptlang/{parse,action}.go` | Completed #39 boundary with source spans and categorized diagnostics |
| User command definitions | `promptlang.Registry`, owned by `interpreterModel` | Move runtime name resolution and reservation policy out of the syntax package |
| Native command dispatch and help | `command_definitions.go`, `runtime.Registry` | `/who` uses registry dispatch; other handlers retain their legacy path |
| Mutable application state | `model.go`, transcript and approval files | Keep one canonical projection and shared application services |
| Stage planning and waits | `stageWorkflow`, `core_send/broadcast/handoff.go`, stage support files | Separate command-specific planning from reusable readiness and interruption policy |
| Loop orchestration | `loopWorkflow`, `core_loop.go` | Depend on participant delivery and shell capabilities rather than session protocol |
| Effect execution and causal ordering | `executor.go`, `instruction_runner.go`, `session_execution.go` | Retain one coordinator; isolate session and shell adapters behind narrow ports |
| Result ownership | `core_events.go`, `workflowRef` | Route correlated results to their registered owner without central workflow-kind switches |
| Front-end contract | `api*.go`, event dispatcher, snapshot cache | Preserve the facade, publication order, detached state, and lifetime ownership |

The package is already composed along the decision/effect boundary: workflows
return instructions and one runner executes them. Its remaining coupling is
explicit: legacy catalog handlers receive `*interpreterModel`, the model names each
workflow, result routing switches on workflow kinds, and workflow plans and
outcomes expose session types.

## Options

| Option | Strength | Main cost |
|---|---|---|
| `user(stage(std(session)))`, with one runtime interface | Easy to construct variants; clear interception order | Submission, effects, events, results, snapshots and lifetime do not naturally share one small interface; ordering becomes distributed |
| `user(std(stagedSession(session)))` | A staged session adapter could serve multiple clients | Staging needs raw drafts, input milestones, transcript-based handoff source selection and structured edit/discard operations, which are application concerns |
| Collection of command runtimes | Commands can be independently registered and tested | A command collection alone does not define shared state, effect execution or asynchronous result ownership |
| Command modules plus composable capabilities | Small command dependencies; staging can wrap delivery without owning every command | Requires a precise plan/result contract and explicit shared coordinator |

The last option is the recommended direction. Use command modules for language
behavior and decorators where a capability has a meaningful shared contract.
Do not require every component to implement the entire interpreter API.

## Proposed composition

```text
front end -> Interpreter facade -> one serialized coordinator
                                      |
                           AST -> command catalog
                                      |
                      command/workflow modules
                         |                 |
                  delivery capability   shell capability
                         |
                  optional staging policy
                         |
                    session adapter
                         |
                       Session

coordinator owns canonical transcript, application state,
correlated result routing, ordered publication and shutdown
```

The session adapter implements only session-backed operations: participant
inspection, frozen send planning, delivery, cancellation, lifecycle commands and
approval responses. It neither retains pending work nor emits user submission
milestones. Session remains the final readiness, policy and delivery authority.

Staging retains frozen `WaitFor` and `ToInform` aliases, tracks readiness
and source-completion requirements, coordinates interruption retries and emits a
dispatch effect when the plan can proceed. Draft edit/discard and the current
single-stage admission rule are application policy exposed through the facade.
They must remain atomic on the shared coordinator.

User modules implement `/loop`, `/shell`, `/def`, help and other application
behavior using explicit capabilities. Session-backed command modules translate
AST nodes into adapter requests. A module may be a simple handler; only commands
that retain work need a workflow instance. Each command supplies its help
metadata alongside its handler, preserving the existing single catalog.

`/loop` is session-independent in implementation only after this separation:
it still needs participant delivery, turn completion, cancellation/departure
facts, command resolution and shell execution. It cannot be a session-free
feature in behavior.

Handoff is a boundary case. Its latest completed source comes from the canonical
room, not Session. Provide command modules with a shared command context exposing
session and room capabilities, alongside delivery, shell and command resolution.
Handoff can inspect participant facts and canonical room output through this
context; staging owns the wait for source completion and the adapter performs
the final session command. Keep access behind interfaces: room reads use detached
values and session effects go through the serialized runner. Do not duplicate
the transcript or give commands independent execution and observation paths.

## Contracts to establish before extraction

- Distinguish a parsed statement, a prepared invocation and a dispatch effect.
  Session freezes send recipients in `ParticipantSendPlan`; staging derives its
  readiness description from that plan. Commands preserve source-selection rules.
- Distinguish acceptance, queued submission success, delivery acceptance and
  participant completion. An `Execute(node) error` contract is insufficient.
- Assign each retained workflow an owner and generation/request identity.
  Route results to that owner; stale results cannot advance replacement work.
- Project a complete causal session-event burst into shared state before
  executing derived effects or advancing the dispatch result. Modules cannot
  independently observe Session or call it from worker goroutines.
- Preserve partial-delivery metadata and transcript-before-output ordering.
  The coordinator publishes each user input and terminal submission outcome once;
  nested workflow operations must not masquerade as fresh user submissions.
- Keep state mutation confined to one loop. Shell workers return results to it;
  modules do not introduce independent queues, goroutines or shutdown ownership.

These are semantic contracts, not a proposal for a large exported `Runtime`
interface. Prefer narrow delivery, participant-facts, transcript and shell ports.
Reuse domain types where appropriate; introduce interpreter-owned values where
session protocol currently leaks into user workflow logic.

## Package direction

Potential boundaries, once their contracts are demonstrated:

```text
interpreter/ast       syntax, nodes, spans, parsing diagnostics
interpreter/runtime   shared plans, effects, outcomes and narrow ports
interpreter/std       session-backed command modules and adapter
interpreter/stage     delivery staging and interruption policy
interpreter/user      loop, definitions, shell and application commands
interpreter           facade, coordinator, projection and construction
```

`ast` has no execution dependencies. `std`, `stage` and `user` depend on shared
contracts rather than importing one another or the parent facade. Construction
injects implementations. The coordinator runs effects against adapters. Keep
runtime small; it must not become the old monolith under a new name. These
directories are candidates, not requirements to create in one change.

## Registered command launch and completion

`std.SendCommand` is publicly registered through the new interpreter.
`SendCommand.Prepare` resolves the parsed name through
`SessionExecution.ParticipantByName`, validates that view, and asks Session to
create one policy-aware `ParticipantSendPlan` using the resolved alias. It does
not select identity from the readiness snapshot or resolve the name again at
launch. The invocation retains that plan.
The plan retains resolved `participant.Alias` values and its Session binding.
Session rejects delivery to same-name replacements, even when departure notifications have not
reached staging. A primary replacement fails the send; a notice replacement
reports a partial-delivery failure. This delivery guard does not establish the
snapshot/event collection boundary supplied separately by atomic subscriptions.

`Staging` derives `WaitFor` from `PrimaryParticipant()` and `ToInform`
from the remaining `Targets()`. Staging does not select delivery recipients.
`Context.Session` supplies invocation-scoped `SessionExecution`; `Go(complete)` submits the native
`session.SendToParticipantCommand` with its retained plan. `Staging.DiscardInform`
lets the lifecycle tell the invocation about a departed notice recipient before Go.
Send uses `DiscardUnavailableNoticeRecipients` to update the plan. The lifecycle
drops that alias from its waits and retains sticky departures, including same-name
replacements. An unavailable `WaitFor` alias fails staging and closes the
invocation instead. The lifecycle applies notice departures before launch,
on the serialized lane; the invocation ignores them after launch or close. Readiness and departure
handling are proved against the shared-feed fake and through the new interpreter.
The invocation constructs the
original input record from `ParsedStatement.Raw` only if delivery reaches someone;
partial delivery preserves both routing metadata and the execution error.
Terminal validation checks the full primary alias; an outcome for a same-name
replacement fails the original invocation without recording delivery. Notice-discard callbacks
also require the exact frozen alias. `/who` renders `.Name`; shell output uses
a local zero-ID display alias, without claiming participant identity.
The capability revalidates availability at delivery and owns causal session-event
handling. The monolith supplies the serialized admission adapter and invocation-scoped
routing; new-interpreter lifecycle tests prove waiting and causal publication.
Public stage presentation and operations use the new interpreter.

`InvocationLifecycle` is constructed and driven by the new interpreter on the
shared serialized lane. It retains Context directly. Its participantSubscription
collects facts under a collector lock and merges the atomic initial snapshot only
for untouched aliases. Departures stay sticky and participant instance identity
is preserved. Ordinary inspection reads are not an event-collection barrier.

Participant callbacks never advance the lifecycle. They collect facts and invoke
`RequestEventProcessing`, which only queues owner work. The new interpreter checks the
pending invocation identity, calls `ProcessEvents`, then projects the visible stage.
Start, ProcessEvents, Complete, Readiness, DiscardWaiting and Close all run on that same
lane. There is no private monitor goroutine, lifecycle mutex, wake channel or join.
The lane is supplied temporarily by the monolith; the new interpreter owns every
advancement decision and no arbitrary scheduling capability is exposed to commands.

`Start(complete)` subscribes and evaluates initial facts. An already-ready
invocation launches immediately. Otherwise Start reports that it entered waiting,
and the interpreter retains its stage before processing any queued advancement.
Facts can arrive during subscription or before retention, but cannot launch or
fail the invocation outside the lane. ProcessEvents evaluates the latest collected
facts without rereading Session. Optional departures update the retained delivery
plan before launch. Required unavailability reports a typed error carrying the
alias. Launch commits only if the collector revision is still current, freezing
collection under its lock. Facts arriving during DiscardInform force reevaluation;
command callbacks and Go run outside the collector lock.

The lifecycle unsubscribes before calling the unchanged Invocation.Go(complete).
A nil Go result means admission, not delivery or participant completion. Execution
may finish asynchronously; completion callbacks only queue results for the owner
to accept on the lane. Initial failures return from Start; later failures close
resources and queue settlement. Complete preserves a delayed launch failure over
an earlier callback emitted inside the failed Go, and accepts settlement once.

Close runs on the lane, stops collection, unsubscribes and closes unfinished
invocations. It does not interrupt Start or Go from another goroutine. Those
operations must return promptly; admitted execution and worker cancellation remain
with their capabilities. The monolith drains admitted execution and completion
work before invoking the new interpreter's cleanup hook. Late facts and already
queued advancement or completion cannot revive removed invocation identities.

DiscardWaiting shares the lane with launch and also checks collected facts:
collected readiness or failure defeats removal even before ProcessEvents runs. Blocked
work is removed only after committing the collector revision. This preserves the
existing edit/discard behavior without an independent advancement timeline.
Tests cover initialization merging, sticky departures, same-name replacement,
partial delivery, discard arbitration, late queued work and shutdown cleanup.

Collection accounts only for facts already delivered to its callback. Atomic
subscription orders the snapshot and later facts; it cannot guarantee availability
after launch commits. Session remains the final readiness and membership guard.

**Snapshot/event ordering boundary.**

Raw `Session.Participants()` is not an observer-delivery barrier. Session commits
state under its mutex, releases it, and then notifies observers. A preceding
observer can block `AgentReady` while a snapshot already exposes idle,
startup-ready state. `TestParticipants_canOvertakeReadyNotification` proves this
with a real Session. Draining the interpreter inbox before a raw read does not
establish collection ordering when a notification has not reached that inbox.

This observation does not demonstrate unsafe send delivery. `std.SendCommand`
retains frozen participant identities, subscribes before admission, and consumes
routing completion once. Session checks membership and readiness at execution;
the retained-send replacement regression proves rejection despite an uncollected
old departure. Partial delivery is reported through the existing routing result.

`TestInvocationLifecycle_InitialBusySnapshotRequiresCollectedBusyFact` reproduces
the lifecycle consequence with a narrow fake: an older collected ready fact hides
a newer busy snapshot when the busy notification is pending upstream. The lifecycle
attempts launch, and the fake execution guard rejects it; initial submission fails
instead of remaining staged. Collecting the busy fact before the read returns
retains waiting work, which launches once when a later idle fact arrives.

This negative fixture violates the lifecycle subscription boundary. Snapshot
precedence is not a safe local fix:
`TestInvocationLifecycle_InitialBusySnapshotPreservesLaterReadyFact` covers a busy
snapshot captured before a newer collected idle event. That event must win to
avoid retaining ready work indefinitely. Neither snapshots nor lifecycle events
carry a comparable source revision, so the collector cannot distinguish these
histories from the conflicting statuses alone.

Session now provides `SubscribeParticipants(handle) -> (ParticipantSnapshot, unsubscribe)`.
Snapshot capture and subscription installation share the Session state lock.
One `changeParticipants` boundary owns lifecycle mutation, before/after view
capture and delivery. Callers describe registration, state/readiness changes or
removal without queuing facts or remembering a flush. `participant.Group` replaces Registry and owns membership, snapshots, fact
capture and subscription queues. It queues participant-owned changes while that
same lock is held; delivery starts after unlocking. Session translates status,
readiness and removal facts into its lifecycle events. Group imports no Session
vocabulary and adds no independent collection lock. A subscriber never
receives facts from before its initial snapshot. The feed includes the internal
attached status and startup readiness separately; it does not change canonical
observer notifications. Each subscriber serializes its own drain without a new
worker goroutine. Unsubscribe drops queued facts, permits an in-flight callback
to finish, and can be called from a callback. Session shutdown releases subscriptions.

The lifecycle uses this capability directly rather than combining `Subscribe` with
`ParticipantReader.Participants()`. A real-Session lifecycle regression keeps work
waiting on a busy snapshot despite a delayed ordinary `AgentReady`, then launches
once on the later native idle transition. Another preserves a native idle fact
collected while the initial busy snapshot is returning. Source tests cover blocked
callbacks,
post-snapshot ordering, reads and unsubscribe from callbacks, startup readiness,
removal/replacement identity and shutdown. Admission and transcript publication
now work through the new-interpreter lifecycle boundary; visible stage operations
are also integrated with public addressed-send registration.

The private-monitor experiment has been replaced with owner-lane advancement.
Legacy CommandRunner and stage operations remain unchanged. The new interpreter
owns lifecycle advancement, completion and cleanup; the monolith supplies ordered
scheduling/publication and a shutdown notification. Public addressed-send cutover is complete.

Invocation.Staging remains the sole frozen requirement description. Participant
facts are progress state, not another delivery plan. /who and /shell return nil
staging and create no participant subscription.

Runtime's `SessionExecution.ParticipantByName` resolves an input name through
Session before a command retains work. `CreateParticipantSendPlan` accepts the
resolved `participant.Alias`; `Staging.WaitFor`, `ToInform`, and `DiscardInform`
carry that same value. The subscription filters events by the full alias, and both
collected state and discarded-notice state are keyed by alias. Snapshot merging
fills only untouched instance entries: an old departure cannot overwrite a
replacement's state, and a replacement's readiness cannot satisfy an old wait.
Required-instance departures fail staging; optional-instance departures exclude
only that notice. Names are derived for diagnostics. This identity boundary does
not establish snapshot/event ordering, Session membership, or completion of a
specific invocation or turn. Public staged sends use the new interpreter.

Runtime tests use local command and invocation fakes. Tests exercising real
standard commands through the runner, registry, and lifecycle live in `std`,
so Runtime's package tests do not depend on a consuming command package.

An interpreter-owned `runtime.Registry` registers `Command` implementations directly.
`Command.Statement()` returns a representative AST value identifying the accepted
type, not default arguments. Lookup indexes the concrete type of the parsed value.
The command owns metadata and validates arguments in `Prepare(ParsedStatement,
Context)`; there is no separate builder or adapter. Registration rejects duplicate
names and statement types and preserves order for help. The interpreter owns a registry populated explicitly from module catalog entries.
Registry dispatch preserves the parsed statement through preparation.
`std.ShellCommand` accepts only `promptlang.Shell`. `promptlang.UserCommand` and
`promptlang.UserDefinition` identify named invocations and definitions; both retain
their legacy interpreter execution path until separate modules are introduced.

`std.WhoCommand` and `std.ShellCommand` are registered in that registry. The catalog retains a module
entry to preserve help ordering, with metadata read from the registered command
and no legacy handler. Shared dispatch selects the command by parsed statement
type and emits a `goInvocationInstruction`; its outcome carries original source
metadata. The executor supplies detached participant values and calls
`CommandRunner.Go`. The former participant-read instruction and result branch
are removed. Both commands report submission success when `Go` returns nil, before
queued completion records are published. Preparation or launch errors instead
report submission failure. Completion reports execution errors separately.

The new interpreter assigns monotonically increasing invocation IDs and consumes
each completion once. The legacy bridge retains its own tracking until removed. Failed launches invalidate queued callbacks. Shutdown drains
completions retained while accepted operations finish, then settles unfinished
execution with `ErrClosed`; late callbacks cannot mutate closed state.
Commands transfer ownership of completion records and do not publish events.

The sorted alias notice is unchanged, but arrives through canonical transcript
deltas without command-specific UI presentation. `ParticipantsListed` remains
available for source compatibility. Tests cover record order and exactly-once
publication.

Registry dispatch now drives both commands through the same invocation path.
The callback contract replaces `Init`/`Next`. Prototype tests cover immediate
and delayed completion, duplicate callbacks, launch failure, and shutdown.

`std.ShellCommand` uses the shared registry launch and completion path.
`ShellLauncher` supplies the working directory and
asynchronous execution; the executor owns cancellation and joins workers on close.
The command constructs the original command record. The executor capability retains
the raw shell result for the existing `ShellCompleted` event and source diagnostics.
Submission succeeds after launch; completion publishes the record, event, and
snapshot in that order. Results generated during shutdown are retained and applied
after workers finish, including cancelled shell records. Loop shell execution and
definition storage remain unchanged. Completion errors use `OperationFailed`
except shell results, whose existing `ShellCompleted` carries the execution error.
No completion emits another submission outcome.

## Relationship to #39

#39 is complete: typed AST nodes carry source spans and categorized diagnostics,
and the UI consumes parsed submission metadata instead of independently parsing
input. Keep those contracts through this migration. Grammar and runtime command
registration remain separate; this work introduces no new syntax.

## Incremental proof and review decisions

1. Review the callback `/who` bridge and prove asynchronous `/shell` execution
   before generalizing module registration or command dispatch.
2. Prove addressed-send preparation and direct delivery, then staged delivery in
   a separate checkpoint, using the same fake adapter and preserving behavior.
3. Migrate broadcast, then handoff, extending contracts only as each needs them.
4. Migrate shell and definitions, then loop with its existing delivery behavior.
5. Settle loop staging policy before adding staged loop sends. Migrate correlated
   ownership and lifecycle subscriptions alongside their command consumers.

Extract cohesive packages only where their contracts have demonstrated value.

Loop-generated sends will use staging. This deliberately changes their current
immediate plan-and-dispatch behavior: a turn waits for its frozen recipients to
be ready before delivery. Define how retained loop work interacts with the
single user-visible stage, input admission and edit/discard/interrupt operations
before implementing it. A loop advances its turn count only on actual delivery,
and starts its condition only after participant completion; queue acceptance is
neither milestone. Nested sends must not duplicate user submission outcomes.

Also decide whether composition is only a construction-time implementation tool
or an intended configurable feature. Start with static construction; runtime
plugins and arbitrary stacking are not required by the current use cases.

Validation should reuse the existing causal ordering, stale completion,
startup readiness, partial delivery, cancellation retry and atomic stage tests.
Add capability tests proving direct and staged delivery against the same fake
adapter, and loop tests against fake delivery/shell ports without a fake Session.

### Invocation-scoped terminal events

`Context.Session.Subscribe(handle)` installs the invocation's terminal-event
listener before `Execute(command)` admits its native command. The stream emits
`runtime.ExecutionCompleted` whenever Session.Execute returns, with its execution
ID, authoritative Err and optional actual routing Result. No routing is represented
by an empty Result.Kind; ordinary errors do not fabricate routing metadata.
When routing exists, Result.Err is derived from Err. Malformed or multiple routing
outcomes emit `runtime.ExecutionFailed`, reserved for host/protocol failures.
`runtime.Delivery` rejects completion without routing; commands validate routing
kind, recipients and roles. Completion means startup launched for invite,
interruption requested for cancel, or delivery attempts finished for sends;
it never means participant work finished. These runtime execution events
are distinct from Session's participant events and canonical publication.
Command-specific semantic validation fails an invalid terminal event instead of
ignoring it. A legacy or another invocation's outcome cannot reach this listener,
including concurrent broadcasts with identical recipients.

The lifecycle uses the separate atomic `SubscribeParticipants` boundary for
readiness and departures before execution. It returns initial views and only
post-snapshot lifecycle facts. Its callback collects facts and queues advancement;
ordinary participant inspection reads establish no collection ordering.
Unsubscribe stops delivery without waiting for in-flight callbacks. The lifecycle
disables collection and unsubscribes on the lane, so captured callbacks and queued
advancement cannot restore closed work.

The native command retains its frozen plan; no execution path reselects
recipients. Runtime Delivery suppresses duplicate and late terminal events, while the
interpreter retains lifecycle identity and final settlement authority. Native
execution drains on the shared lane before later input inspects readiness.
Session's locked participant transitions decide actual availability.

The SubmitCommand compatibility bridge alone preserves early rejection of two
queued addressed sends to the same primary. Its private `legacyCommandSession`
wrapper reserves until completion/close; this is neither correlation nor general
participant-availability policy. Remove the wrapper and its reservations with the
bridge at checkpoint 9.

## Visible stage ownership during migration

The new interpreter retains one waiting invocation as its visible stage. It owns
source, frozen presentation routing, interruption state and input admission. The
monolith's `WithStageSnapshot` hook projects the detached stage into the existing
snapshot/event stream; it does not decide stage policy. When there is no new
stage, legacy stage operations and projection remain available. The new
interpreter checks stage admission before routing any input, including fallback
or invalid input; presentation-only debug commands remain available.

Send-stage routing presentation is derived from `Staging.WaitFor` followed by
`Staging.ToInform`; the retained Session plan remains the delivery authority.
Lifecycle `Readiness` exposes only collected facts for frozen requirements;
`RequestEventProcessing` queues lifecycle advancement and stage projection on the owner lane. Departures remain sticky, optional
recipients disappear from routing, and joins cannot expand the retained plan.
Runtime reports required-participant unavailability as a typed error carrying the
alias. The interpreter translates that outcome into a visible discard notice;
it does not infer the failure cause from readiness views.

`DiscardWaiting` runs on the owner lane and commits the collector revision:
committed Go or collected readiness/failure defeats removal. Otherwise it freezes
collection and closes the invocation. The new interpreter
revokes the submission scope, removes stage/identity and publishes the cleared
snapshot without an input record or extra completion. It does not return a draft
that can still deliver. Only queued ProcessEvents calls progress waiting work.

Interrupt selects frozen active/cancellable blockers from those same facts. The
monolith executes cancellation on the shared lane and projects its causal facts;
the new interpreter owns successful-acknowledgement tracking, retry and notices.
Acknowledgement is not idle, startup waits are not cancelled, successful aliases
are not cancelled again, and failed aliases remain eligible for retry. Required
departures produce the legacy discard notice rather than a duplicate execution
failure. Actual delivery publishes routing and a staged-dispatch semantic event.

Real-Session and lifecycle tests cover snapshots, cross-path admission, manual
removal, readiness races, resubmission, partial cancellation/delivery, startup,
departures, frozen joins and shutdown cleanup. Public send registration preserves
direct versus queued submission milestones: direct sends settle after causal delivery and dispatch publication;
queued sends acknowledge retention and publish later failures separately. Both
paths publish dispatch before partial-delivery failure. User command definitions and
loops remain fallback; stage admission works in both submission orders.

## Delivery responsibility boundary

Commands in `std` own validation, frozen recipient selection, native command
construction, departure policy, terminal-result validation and record formatting.
Their invocations compose `runtime.Delivery`, a helper for subscription, launch,
single-terminal-event acceptance and cleanup. Delivery has no command-type
switches, routing predicates, stage, identity or publication. `InvocationLifecycle`
provides readiness/transitions; the interpreter constructs it and decides when to
advance, settle or close it, owning identity, stage operations and shutdown.

A command subscribes to its execution's terminal events and supplies only the
actual Session command. The capability emits execution completion with optional
actual routing, or a host/protocol failure. Delivery rejects completion without
routing. No synthetic routing template is needed to terminate missing outcomes. Session owns actual routing and participant state.
Semantic validation may reject an invalid terminal event, but must report failure
rather than leave the invocation waiting for another result.

The retained delivery command and Staging have different consumers: delivery and
readiness respectively. Handoff prerequisites can differ from its recipients. Stage presentation shows
all frozen prerequisites; actual delivery routing comes from Session.
Departures may update the command while the readiness description remains frozen
for stage presentation. Neither representation is an error-routing template.

Common native admission imposes no alias reservation. Scheduled execution drains
before subsequent input inspects readiness, and Session's locked transitions
remain the participant-availability authority. The private SubmitCommand wrapper
preserves only that bridge's historical queued-send early rejection. It is not
part of the reusable capability contract.

To add a delivery command, implement its command and invocation policy in `std`,
compose Delivery, and validate/format its terminal result. Use Staging and
Delivery.UpdatePrepared for frozen departure changes. Register the command and
test the complete path. Runtime mechanics and the execution host need no routing
metadata or command-specific branches; introduce new capabilities only for
information or effects a command actually requires.

The monolith remains a temporary host for serialized execution, scoped terminal
events, canonical projection/publication and draining. As remaining fallback
workflows migrate (checkpoints 4–9 in PLAN.md), transfer those implementations to
the new interpreter's execution/publication owner while retaining the runtime
contract. At checkpoint 9, once no fallback handlers or SubmitCommand callers
remain, remove the bridge, its private reservation wrapper and the monolith.
Runtime mechanics remain driven by the interpreter throughout.

## Handoff history and staging boundary

`Context.History` is a narrow canonical read capability. Its current operation,
`LatestOutput(alias)`, returns detached eligible output and its canonical record
index for that participant instance. It has no readiness methods or subscriptions.
Future latest/earlier/all selectors can extend history selection when implemented;
no new syntax or full-history copying is part of this checkpoint.

Prepare freezes the handoff identities and declares staging. Once staging is
satisfied, Go reads history, fills the retained Session command's Source and
submits it. Re-running Prepare would risk reselecting participants and is not
needed. WaitFor and ToInform share participant readiness and turn-propagation
guarantees. Runtime compares the last completed Session turn against projected
completion facts; the interpreter supplies those
facts and drives advancement using the existing canonical observer and shared lane.
The history capability and SessionExecution keep separate read and effect roles.

ExecutionCompleted carries execution identity/error and optional actual routing,
without source preview payloads. Session audit facts, including the
handoff preview, reach the UI through canonical projection. Std does not consume
or reconstruct previews, and CommandResult needs no semantic-event field. The
redundant HandoffCompleted notification is removed. The interpreter remains
the event-ordering, invocation-identity and settlement owner. The projection hook
and history implementation move out with the remaining monolith capabilities at
checkpoint 9.

### User definitions and named invocation checkpoint

The new interpreter owns one promptlang.Registry. Context.NamedCommands exposes
only Define and Resolve, implemented by that registry. Std DefineCommand stores
unevaluated definitions and formats confirmation; std UserCommand resolves a name
before acceptance and reuses shellInvocation. The interpreter retains admission,
diagnostic classification, acceptance milestones, lifecycle driving and publication.
No new shell execution or lifecycle contract is introduced.

Definition confirmation precedes submission success. Already-queued synchronous
results settle after Start bookkeeping and before the next input, so queued named
invocation cannot overtake definition confirmation. Unknown names retain their
registry diagnostic and do not become accepted input. Shell records and completion
events display /name while the worker runs the resolved program. Cancellation,
joined shutdown and stale-result rejection use the existing owned execution path.

WithNamedCommands temporarily shares the interpreter-owned registry with legacy loop
and public ResolveCommand dispatch on the same lane. The bridge and legacy handlers
are removed when remaining monolith consumers migrate at checkpoint 9.

### Execution provider checkpoint

Context.Session now exposes SessionExecutionProvider. Participant inspection,
atomic lifecycle subscriptions and Session-owned send planning remain on the
provider. Each delivery calls CreateExecution to obtain a fresh single-use
SessionExecution containing only Subscribe and Execute. Scope unsubscription
closes that scope independently; interpreter-owned provider closure revokes all
existing scopes and prevents new admission. This enables repeated deliveries
within one invocation without changing loop behavior or adding loop staging.

The temporary monolith adapter separates commandSessionProvider (scope lifetime
and ordered publication) from commandSession (one execution's admission and
terminal events). Causal completion and publication remain on the single lane.
The SubmitCommand compatibility provider retains its private early same-primary
restriction. Native providers add no alias reservations. Remove these temporary
implementations as their responsibilities transfer out of the monolith at checkpoint 9.

### Interpreter-driven event processing

Context.EventProcessing.RequestEventProcessing is an attention request, not an arbitrary-work
scheduler. The interpreter assigns identity and binds it before command Prepare;
producer callbacks collect facts and enqueue attention only. The serialized lane
checks the pending identity before invoking lifecycle ProcessEvents. Requests from
failed preparation/start, settled invocations or closed interpreters cannot revive work.

While waiting, lifecycle ProcessEvents evaluates staging readiness. While running,
it invokes the optional InvocationEventHandler.HandlePendingEvents method. Std owns the
command's collected facts, phases and decisions; runtime gates calls by lifecycle
state; interpreter owns scheduling, identity, final completion and shutdown.
Existing invocations require no extra method. Final results still use Go's
completion callback. Intermediate publication and loop migration remain separate
steps requiring agreement; loop staging remains deferred to #65.

### Invocation publication component

The interpreter creates one invocationPublication per admitted invocation and
exposes it as Context.Publication through runtime.InvocationPublication. The
component binds the invocation ID and original submission and owns the publication
policy for acceptance, active output, and final outcomes. Std supplies content;
interpreter owns permission, source metadata, ordering, settlement and shutdown.

Publish(records, events...) is available during Go and HandlePendingEvents on the
serialized lane. It reads existing pending identity and lifecycle IsRunning state,
retaining no duplicate lifecycle flag. Calls from Prepare or Close and stale handles
cannot emit output. Publication leaves execution scopes and the invocation active.
Only the interpreter accepts final completion and invokes final publication.

The temporary monolith exposes CommandSubmission.PublishRecords by extracting the
existing record/event/snapshot sequence from final completion. That operation does
not close resources or invent shell events. Final completion reuses the sequence,
retaining compatibility shell events only when publishing a new shell record.
Delivery batching accumulates output rather than overwriting one slot: input
records precede causal Session events, while updates and terminal outcomes follow.
Transfer the mechanism out and remove the bridge with the monolith at checkpoint 9.


## Application-defined named commands

Applications can contribute argument-free named commands with callbacks through
`interpreter.NewWithApplicationCommands(ctx, session, cwd, commands, opts...)`.
It returns an interpreter and an error, preserving the existing `New` constructor
for callers without application commands. The complete batch is validated and
copied before interpreter startup; failure leaves no partial registration and does
not take ownership of the caller's Session. Registrations are immutable afterward.

`interpreter.ApplicationCommand` aliases `promptlang.ApplicationDefinition`,
which holds `Name`, `Description` and `Run func() error` in the registry.
The callback runs in the ordered publication dispatcher after earlier interpreter events have reached observers,
without blocking the interpreter operation lane. It must return promptly and must
not wait for interpreter processing or call blocking interpreter APIs such as
Snapshot, ResolveCommand or Close. A nil result acknowledges dispatch
acceptance, not completion of work handled later by the application. Errors fail
the submission. The interpreter owns admission, input/source publication,
acknowledgement and invocation settlement; callbacks do not repeat admission rules.
Callbacks queue their results back to the interpreter, which accepts and settles
on its lane. Shutdown drains admitted publication callbacks before closing result
intake, then flushes their final outcomes through the existing dispatcher.

`promptlang.Registry` remains the single named-command namespace. General Resolve
returns either UserDefinition (a shell body) or ApplicationDefinition (the complete
name, description and callback). Define and RegisterApplication share identifier, built-in-name
and duplicate checks, so shell definitions cannot shadow application commands.
Each named invocation resolves once. The interpreter retains the returned definition
through admission and selects application or shell preparation from that value.
Preparation does not resolve it again, and staged admission uses the selected
definition kind. Application commands remain available while input is staged;
participant commands submitted by their callbacks pass through the existing
interpreter admission rules. No per-application stage flag is needed.
RequireShell validates an already-resolved definition without
consulting the registry; loop conditions resolve once and apply this validation
before launching any participant work. There is no parallel interpreter map of
application implementations. Registration
stores complete definitions as immutable values in one registry; storing a callback
does not execute it or give promptlang responsibility for scheduling. Application
message types remain outside promptlang.

The UI contributes quit, debugview and debugrows through Observer.ApplicationCommands.
Quit dispatches a UI request; an asynchronous call to `Interpreter.Close()` shuts
down the Session and drains publication before queueing completion behind drained
outcomes.
Their callbacks enqueue UI-defined messages in the observer's FIFO alongside
wrapped interpreter events. The UI loop owns debug enablement, viewport diagnostics
and row overlays, and builds debug help from the same registrations. Closing the
queue rejects later application dispatch. There are no debug-specific parser ASTs,
reserved names, interpreter events or interpreter admission branches. Another
application command requires registration rather than a parser or event-type edit.
Parameterized commands and asynchronous callback completion are deferred.
