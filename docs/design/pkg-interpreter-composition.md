# Interpreter composition: discussion draft

Status: incremental migration. The facade selects `/who` and `/shell` through its
registry; the monolith retains execution and publication ownership.

This follows [#55](https://github.com/roomscript/coderoom/issues/55) and considers
[#39](https://github.com/roomscript/coderoom/issues/39). The existing contracts in
[pkg-interpreter.md](pkg-interpreter.md) and
[pkg-interpreter-workflows.md](pkg-interpreter-workflows.md) remain authoritative.

## Public facade and monolith fallback

The current interpreter implementation has moved to `internal/interpreter/monolith`
behind its existing interface. The public `internal/interpreter` facade initially
delegates unsupported requests to one instance. `/who` and `/shell` are selected in the facade
and admitted through the shared coordinator bridge. Shared `api` contracts keep front-end
imports stable and let both implementations publish identical event and snapshot
types without importing the parent package. `runtime` and `std` remain separate.

Keep Session execution, canonical transcript projection, publication and shutdown
with one runtime while migrating command selection. The monolith is that owner
for this checkpoint. An additive `SubmitCommand` method queues a selected runtime
command on its existing serialized path, reusing admission checks, CommandRunner,
completion tracking, publication and shutdown. Its command catalog and handlers,
including `/who`, are untouched. The facade registry currently selects `/who` and `/shell`.
Native asynchronous send admission and same-primary routing ownership remain the
next proof; this command bridge does not establish either. Later route a command to the modular path only when its needed
capabilities are proved; otherwise delegate before execution starts. Never retry
through fallback after the selected path begins execution.

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

`std.SendCommand` is an unwired preparation/direct-delivery prototype.
`SendCommand.Prepare` resolves the parsed name through
`SessionExecution.ParticipantByName`, validates that view, and asks Session to
create one policy-aware `ParticipantSendPlan` using the resolved alias. It does
not select identity from the readiness snapshot or resolve the name again at
launch. The invocation retains that plan.
The plan retains resolved `participant.Alias` values and its Session binding.
Session rejects delivery to same-name replacements, even when departure notifications have not
reached staging. A primary replacement fails the send; a notice replacement
reports a partial-delivery failure. This delivery guard does not establish the
snapshot/event collection boundary required by the future adapter.

`Staging` derives `WaitFor` from `PrimaryParticipant()` and `ToInform`
from the remaining `Targets()`. Staging does not select delivery recipients.
`Context.Session` supplies shared-feed `SessionExecution`; `Go(complete)` submits the native
`session.SendToParticipantCommand` with its retained plan. `Staging.DiscardInform`
lets the host tell the invocation about a departed notice recipient before Go.
Send uses `DiscardUnavailableNoticeRecipients` to update the plan. The lifecycle
drops that alias from its waits and retains sticky departures, including same-name
replacements. An unavailable `WaitFor` alias fails staging and closes the
invocation instead. Callbacks run on the host's serialized path before launch;
the invocation ignores them after launch or close. Readiness and departure
handling are proved in the unwired lifecycle against the shared-feed fake.
The invocation constructs the
original input record from `ParsedStatement.Raw` only if delivery reaches someone;
partial delivery preserves both routing metadata and the execution error.
Routing completion matches the full primary alias, so an outcome for a same-name
replacement cannot complete the original invocation. Notice-discard callbacks
also require the exact frozen alias. `/who` renders `.Name`; shell output uses
a local zero-ID display alias, without claiming participant identity.
The capability revalidates availability at delivery and owns causal session-event
handling. No session adapter is implemented yet. Lifecycle staging can retain
and release this invocation; publication ordering, admission and the adapter
remain production integration work before registration or interpreter wiring.

An unwired `InvocationLifecycle` is now a private-monitor concurrency prototype.
It retains Context directly. Its unexported participantMonitor component owns
subscription, collected state, initial merge, wake-up queue and stop/join mechanics;
the lifecycle retains readiness/departure policy, launch and result settlement.
Start explicitly selects ordinary or staged execution. Staged preparation returns
a readiness outcome without calling Go or changing lifecycle state. Starting
execution and handling its failure are separate steps; resource cleanup closes
once and never changes lifecycle state implicitly.
`Start(complete)` uses `Session.SubscribeParticipants` to atomically capture initial
views and subscribe to subsequent lifecycle facts. It collects updates into a
synchronized alias-keyed readiness map and merges initial views only for missing
aliases. Facts collected during initialization are necessarily after the snapshot
boundary, so preserving them cannot restore an older ready state.
An already-ready invocation starts immediately; otherwise the lifecycle starts
one private monitoring routine. Its single subscription callback records facts
and signals a buffered wake-up channel. Wake-ups coalesce; the worker evaluates
the latest collected state. Evaluation reads a state revision; launch commits
only if that revision is still current, freezing collection under its lock.
Changed facts trigger reevaluation, including events collected during
DiscardInform. Command callbacks and Go run outside the collector lock.
No `Enqueue` capability is exposed.

The worker owns waiting advancement, optional-recipient plan changes, launch and
failure settlement. It stops monitoring and unsubscribes before calling the
unchanged `Invocation.Go(complete)`. A nil Go result means admission, not delivery
or participant completion. Completion callbacks still only enqueue results for
host publication. The Session capability must admit work safely from the monitor
while preserving the host's serialized native execution lane. The prototype does
not directly call raw Session or publish room records from the worker.

Lifecycle state is protected separately from participant collection. Close signals
cancellation before taking the lifecycle lock, then settles and joins monitoring.
If launch has already committed, Close waits for Go to return admission; it cannot
cancel native delivery itself. The host still owns admitted delivery cancellation,
completion draining and public outcomes. A reader that blocks during initialization
must return before Close can finish; no read-cancellation API is introduced.

A nil Start result means acceptance, possibly while waiting. Initial failures
return from Start; failures on the monitoring timeline close and enqueue settlement.
Complete accepts a result once, preserving delayed launch failure over an earlier
callback emitted inside the failed Go. Tests use channels for asynchronous launch
and failure, and cover initial merges, departure ordering, partial delivery,
initialization cancellation, readiness/discard races and monitor cleanup.

The queue can only account for facts already received by its callback. Tests prove
that collected departures precede evaluation, including ready/stop/rejoin ordering.
The worker cannot see facts still queued for its callback. The atomic subscription
settles snapshot/event ordering, but does not guarantee availability after launch
commits. The future production adapter must preserve this subscription boundary
and serialize admission; this prototype does not validate that adapter.

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

`TestInvocationMonitor_InitialBusySnapshotRequiresCollectedBusyFact` reproduces
the lifecycle consequence with a narrow fake: an older collected ready fact hides
a newer busy snapshot when the busy notification is pending upstream. The lifecycle
attempts launch, and the fake execution guard rejects it; initial submission fails
instead of remaining staged. Collecting the busy fact before the read returns
retains waiting work, which launches once when a later idle fact arrives.

This negative fixture violates the lifecycle subscription boundary. Snapshot
precedence is not a safe local fix:
`TestInvocationMonitor_InitialBusySnapshotPreservesLaterReadyFact` covers a busy
snapshot captured before a newer collected idle event. That event must win to
avoid retaining ready work indefinitely. Neither snapshots nor lifecycle events
carry a comparable source revision, so the monitor cannot distinguish these
histories from the conflicting statuses alone.

Session now provides `SubscribeParticipants(handle) -> (initialViews, unsubscribe)`.
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

The monitor uses this capability directly rather than combining `Subscribe` with
`ParticipantReader.Participants()`. A real-Session lifecycle regression keeps work
waiting on a busy snapshot despite a delayed ordinary `AgentReady`, then launches
once on the later native idle transition. Another preserves a native idle fact
collected while the initial busy snapshot is returning. Source tests cover blocked
callbacks,
post-snapshot ordering, reads and unsubscribe from callbacks, startup readiness,
removal/replacement identity and shutdown. Production admission, stage operations
and transcript publication remain unwired.

This is an explicit experiment with lifecycle-owned monitoring, departing from
the production rule that workers only enqueue results and the coordinator advances
workflows. Production CommandRunner, stage operations and workflow ownership are
unchanged. The initial publication, shutdown and cancellation integration still
needs separate review before wiring.

Invocation.Staging remains the sole frozen requirement description. Participant
facts are progress state, not another delivery plan. /who and /shell return nil
staging and create no monitor.

Runtime's `SessionExecution.ParticipantByName` resolves an input name through
Session before a command retains work. `CreateParticipantSendPlan` accepts the
resolved `participant.Alias`; `Staging.WaitFor`, `ToInform`, and `DiscardInform`
carry that same value. The monitor filters events by the full alias, and both
collected state and discarded-notice state are keyed by alias. Snapshot merging
fills only untouched instance entries: an old departure cannot overwrite a
replacement's state, and a replacement's readiness cannot satisfy an old wait.
Required-instance departures fail staging; optional-instance departures exclude
only that notice. Names are derived for diagnostics. This identity boundary does
not establish snapshot/event ordering, Session membership, or completion of a
specific invocation or turn. The staging prototype remains unwired.

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

The executor assigns monotonically increasing invocation IDs and consumes each
completion once. Failed launches invalidate queued callbacks. Shutdown drains
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

### Session continuation prototype

Addressed send registers a continuation through `Context.Session.Subscribe`
before `Execute(session.Command)`. Subscribe delivers the shared Session event
feed through one immediate callback. Send guards callback state against racing
Close calls, checks the primary recipient, self-unsubscribes and reports records
through the host's completion queue. Send ignores unrelated events
and accepts `RoutingParticipantSend` only when its primary recipient matches
`Plan.PrimaryParticipant()`. An alias appearing as a notice is insufficient.
Tests deliver the same events to simultaneous subscriptions for different primary
aliases. Primary-alias matching does not distinguish overlapping requests to the
same primary; the production adapter must keep at most one such send awaiting a
routing outcome.

The lifecycle uses the separate atomic `SubscribeParticipants` boundary for
readiness and departures before execution. It returns initial views and only
post-snapshot lifecycle facts. Its callback records facts and wakes the private
monitor; ordinary participant inspection reads establish no collection ordering.
Unsubscribe stops delivery without waiting for in-flight callbacks; callbacks
must tolerate a racing Close. The lifecycle disables collection and joins its
monitor, so captured callbacks cannot restore closed work.
The capability's production adapter is still absent; raw `Session.Execute` does
not provide asynchronous admission or a monitored subscription lifetime.

The native session command retains its frozen `ParticipantSendPlan`; no execution
path reselects recipients. `Invocation.Close` releases send continuations on
completion, failed admission or discard. Worker lifetime, actual input-record
publication before causal output, submission milestones and stage presentation
remain interpreter responsibilities to prove during later wiring.
