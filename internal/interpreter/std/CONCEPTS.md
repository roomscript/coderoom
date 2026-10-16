# Command concepts

Working agreement for [#61](https://github.com/roomscript/coderoom/issues/61).
Changes to runtime contracts or semantics require explicit agreement before implementation.
Keep this document roughly one page; details belong in code or
[design docs](../../../docs/design/pkg-interpreter-composition.md).
`/who`, `/shell`, addressed sends, broadcasts and handoffs use registry dispatch and lifecycle execution.

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
(`/who`) or later. `/who` and `/shell` acknowledge launch before completion
records. Addressed sends acknowledge waiting when retained, or report direct
submission results after actual delivery. Preparation or launch errors fail the
submission.
There is no `Init`, `Next`, polling, or generic fact/expectation contract.

**Staging** holds frozen `WaitFor` and `ToInform` aliases, or nil for no waiting.
Both groups require idle, startup-ready participants and canonical propagation of
the last completed turn. Startup, keepalive and aborts create no completion wait.
Stage presentation shows all frozen prerequisites; Session reports actual delivery
routing. `ToInform` holds optional
recipients: contextual notices for send, work recipients for broadcast.
The command identifies its delivery targets; `WaitFor` does not imply delivery.
The lifecycle fails staging if a `WaitFor` alias departs. For a departed `ToInform`
alias it stops waiting and calls `DiscardInform`, which updates the delivery plan.
Departures stay sticky. Lifecycle readiness and new-interpreter stage operations are integrated;
addressed sends are publicly registered.
`/who` and `/shell` return nil.

## Capabilities and records

**Context** supplies narrow capabilities. `ParticipantReader` exposes participant
information through detached reads without session execution access.
`ShellLauncher` starts shell work using executor-owned workers and lifetime.
`Session.Subscribe(handle)` installs an invocation-scoped terminal-event listener
before `Session.Execute(command)` starts delivery. `runtime.DeliveryCompleted`
carries the execution ID returned by Session.Execute and the actual routing outcome; `runtime.ExecutionFailed` carries an
execution/protocol failure without fabricated routing. Other invocations and
legacy dispatch never reach this stream, even with identical recipients.
`runtime.ExecutionEvent` is separate from Session's participant events so host
protocol failures do not become invented Session facts.
Staging uses `Session.SubscribeParticipants(handle)` separately: it atomically
returns a ParticipantSnapshot of current views including participant-owned
LastCompletedExecutionID, and subscribes to later participant facts in mutation order. Derived
completion facts precede idle. ActiveExecutionID belongs to participant state and
clears when work ends; terminal messages carry ExecutionID for projection.
The lifecycle callback collects facts and queues advancement on the interpreter's
serialized lane.

`runtime.Delivery` supplies subscribe-before-launch, single-terminal-event
acceptance and subscription cleanup. Each std invocation owns its retained
command, departure policy, semantic validation and record formatting. It passes
only the actual Session command. An invalid terminal outcome fails the invocation
instead of waiting for another event. There is no routing template or shared
reservation. Only the temporary SubmitCommand bridge retains its private early
rejection of queued sends to the same primary; Session owns actual availability.
Completion only queues owner work; the interpreter still drives lifecycle and
final settlement. Close releases the terminal subscription; lifecycle close also
releases participant subscriptions. Commands receive no raw model.

`Context.History.LatestOutput(alias)` reads detached completed canonical output.
Handoff Go reads it after staging and fills the retained Session command's Source.
History supplies data only; it owns no readiness subscription. The interpreter
uses `Context.CompletedExecutions` through `CompletedExecutionIDReader.LastCompletedExecutionID` when
driving lifecycle readiness. Room owns recorded completion IDs; runtime retains no copy.
The room observer only queues interpreter advancement after room updates.
The atomic Session observer attachment snapshot establishes the historical cursor:
completions committed before attachment do not require replay into the new room. There is no re-Prepare phase or generic readiness gate.

**CommandResult** contains existing `room.Record` values and an error. Commands
construct records and transfer ownership when calling `complete`; they must not
mutate those records afterward. The callback only enqueues. The interpreter
appends records and publishes outcomes in order on its serialized path.

The executor binds callbacks to invocation identities, consumes each completion
once, and ignores duplicate results or results from failed launches. Shutdown
drains retained completions, then fails unfinished invocations with `ErrClosed`
and rejects late callbacks.

## Current proof and next step

Contracts and `CommandRunner` live in `runtime`; commands live in `std`.
The new interpreter owns `InvocationLifecycle` construction, Start, Advance,
Complete and Close, including its pending identity map. It schedules its own algorithm on the
shared lane and calls monolith admission/capability/publication APIs explicitly.
The monolith stores no migrated lifecycle. No Execution interface or execution
wrapper is needed. Legacy callers retain the stateless runner.
It keeps Context directly and delegates subscription and synchronized fact
collection to an unexported participantSubscription. Readiness policy and launch
stay in the lifecycle. All lifecycle methods run on the new interpreter's lane;
participant callbacks only collect facts and invoke RequestAdvance to queue work.
There is no private worker, lifecycle mutex or join. Staged work atomically
subscribes and captures initial views, then merges them only for untouched
aliases. Ordinary inspection reads are not an event-collection barrier.
Start evaluates initial readiness and reports whether to retain a stage. Later
Advance calls evaluate collected facts without rereading Session, discard departed
notices and launch once. The collector revision guards launch and manual removal.
Close stops collection and releases subscriptions on the lane. Departures stay
sticky. Initial failures return from Start; later failures close and enqueue
settlement. A nil Start error means acceptance, which may still be waiting.
`/who` prepares its original sorted alias notice and completes immediately.
Its queued completion appends the system record after launch acknowledgement.
The UI receives transcript deltas, with no `/who` result branch.

`/shell` uses the same path and returns its command record on completion. The
shell capability retains `ShellCompleted` for compatibility. Execution failures
are separate from submission success; loop execution remains legacy.
`runtime.Registry.Register(command)` indexes the type returned by `Statement()`.
`Lookup(parsed)` selects the command; metadata comes from the command itself.
All seven commands are registered. `SendCommand` retains Session's send plan,
derives staging from it and submits the native session command with that plan.
It returns the original input record with actual routing.
Lifecycle staging is proved with a shared-feed fake. Native send admission and
causal publication are proved through the monolith bridge; public addressed sends use the same stage operations.

The new interpreter selects `/who`, `/shell`, addressed sends, broadcasts and handoffs and executes them itself.
`monolith.Schedule` supplies the shared serialized lane; `AdmitCommand` checks
input admission and returns Context plus `CommandSubmission` publication methods.
The new interpreter prepares and starts its lifecycle, queues raw completions,
accepts them once, closes resources and publishes accepted results.
The monolith retains only capabilities and publication metadata. During native
delivery, queued completion work runs before causal events; accepted input records
publish before those events, terminal outcomes afterward. `WithShutdown` notifies
the new interpreter after execution/results drain so it can close its remaining
lifecycles. Explicit close and context cancellation follow the same path.
`SubmitCommand` remains a legacy compatibility bridge, with unchanged handlers.

Real-Session tests through the new interpreter prove busy/startup waiting,
required departure, close while waiting, direct/partial delivery, causal ordering
and subscription cleanup. Send registration is public. Visible stage ownership and atomic
edit/discard/interrupt use the same path.
The destination remains the new interpreter replacing the monolith.

The new interpreter now retains one visible stage for waiting work. Lifecycle
`RequestAdvance` queues advancement and projection on the same lane; `Readiness` supplies collected frozen-instance
facts. The monolith projects that detached stage through `WithStageSnapshot`;
it owns no new stage policy. Input admission covers fallback and migrated input,
with presentation-only debug commands still available.

Edit/discard use `DiscardWaiting`: readiness/committed Go wins, otherwise removal
stops collection, closes resources and revokes native scope. Interrupt cancels frozen active
blockers, remembers successful cancellation and retries only failures. Actual
readiness still gates delivery, including startup. Semantic dispatch/discard
notices and actual partial routing survive completion. Legacy operations remain
fallback when the new interpreter owns no stage. Direct sends settle submission
after delivery and dispatch; waiting sends
acknowledge retention and report later failures without a second submission event.

Broadcast and send have command-specific invocations that compose the same
`runtime.Delivery` mechanics and native adapter. Preparation
freezes non-crashed participant instances into `ToInform`; departed instances are
removed from the delivery command without changing the readiness description.
All recipients departing returns `runtime.ErrNoActiveTargets`, which discards a
retained stage with the existing notice. Direct and waiting broadcasts use the
same submission milestones and partial-routing publication as addressed sends.

## User definitions and named invocation

Context.UserCommands exposes Define and Resolve through UserCommandRegistry.
promptlang.Registry remains the implementation and owns validation and unevaluated
bodies. The interpreter owns one registry per instance; the temporary monolith
WithUserCommands bridge shares it with legacy loop and queued ResolveCommand calls.
All access runs on the serialized lane. Remove the bridge when those callers migrate.

DefineCommand prepares a definition invocation; Go stores the body and queues its
confirmation record. The interpreter publishes that record before submission
success. Reserved and duplicate names remain classified registry diagnostics.
UserCommand resolves before input acceptance and then uses the existing
shellInvocation, executing the resolved program while displaying /name. Missing
names publish UnknownCommand with the original span and create no input record.
Shell workers, cancellation, joined shutdown and compatibility completion events
are unchanged. The compatibility adapter takes display text from the command record.

The interpreter drains already-queued synchronous results after lifecycle Start
and submission bookkeeping, before admitting the next queued input. The producer
callback never settles a lifecycle. This preserves definition confirmation before
an immediately queued invocation and still rejects duplicate or stale results.

Delivery.Go passes the full DeliveryCompleted event to its callback, preserving
ExecutionID alongside Result. Actual delivery failures remain in Result.Err;
execution/protocol failures pass a zero event and a separate error. Commands that
only format routing outcomes read Result; future loop execution can retain the ID.
