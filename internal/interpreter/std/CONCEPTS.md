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

**Staging** holds frozen `WaitFor` and `ToInform` aliases, or nil for no waiting.
Both groups gate idle, startup-ready readiness; `ToInform` also receives notices.
The command identifies its delivery targets; `WaitFor` does not imply delivery.
The lifecycle fails staging if a `WaitFor` alias departs. For a departed `ToInform`
alias it stops waiting and calls `DiscardInform`, which updates the delivery plan.
Departures stay sticky. Lifecycle readiness and new-interpreter stage operations are integrated;
public send registration remains separate.
`/who` and `/shell` return nil.

## Capabilities and records

**Context** supplies narrow capabilities. `ParticipantReader` exposes participant
information through detached reads without session execution access.
`ShellLauncher` starts shell work using executor-owned workers and lifetime.
`Session.Subscribe(handle)` delivers events to one callback. The native send
adapter scopes routing outcomes to the invocation that owns the dispatch;
lifecycle facts still use the shared interpreter observer.
Staging uses `Session.SubscribeParticipants(handle)` instead: it atomically returns
initial views and subscribes to subsequent lifecycle facts in mutation order.
The lifecycle callback retains synchronized facts and requests advancement on the
new interpreter's serialized lane.
Send handles routing results with synchronized state, filters kind and primary
alias, and self-unsubscribes on completion. Both hand results to the monolith publication queue.
`Close` releases subscriptions. Commands receive no raw model.

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
are separate from submission success; user commands and loops remain legacy.
`runtime.Registry.Register(command)` indexes the type returned by `Statement()`.
`Lookup(parsed)` selects the command; metadata comes from the command itself.
Both commands are registered. Unregistered `SendCommand` retains Session's send plan,
derives staging from it and submits the native session command with that plan.
It returns the original input record with actual routing.
Lifecycle staging is proved with a shared-feed fake. Native send admission and
causal publication are proved through the monolith bridge; public addressed sends remain on the legacy path while new stage operations are
proved through test-only send registration.

The new interpreter selects `/who` and `/shell` and executes them itself.
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
and subscription cleanup. Send registration remains test-only. Visible stage ownership and atomic
edit/discard/interrupt are proved; public send cutover is next.
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
fallback when the new interpreter owns no stage. Public send cutover is next.
