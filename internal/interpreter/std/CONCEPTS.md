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
Departures stay sticky. Staging is unwired; `/who` and `/shell` return nil.

## Capabilities and records

**Context** supplies narrow capabilities. `ParticipantReader` exposes participant
information through detached reads without session execution access.
`ShellLauncher` starts shell work using executor-owned workers and lifetime.
`Session.Subscribe(handle)` delivers all events immediately to one callback.
Staging uses `Session.SubscribeParticipants(handle)` instead: it atomically returns
initial views and subscribes to subsequent lifecycle facts in mutation order.
The lifecycle callback retains synchronized facts and wakes its private monitor.
Send handles routing results with synchronized state, filters kind and primary
alias, and self-unsubscribes on completion. Both keep publication in host callbacks.
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
The unwired `InvocationLifecycle` composes a prepared invocation with
`Start`/`Complete`/`Close`; the interpreter still uses the stateless `Go` bridge.
It keeps Context directly and delegates subscription, queue and worker lifetime
to an unexported participantMonitor; readiness policy and launch stay in the lifecycle.
For staged work, it atomically subscribes and captures initial views, collects
post-snapshot updates into a synchronized map, then merges initial views only for
missing aliases. Ordinary inspection reads are not an event-collection barrier. A private routine evaluates collected state without rereading Session,
then launches once and self-unsubscribes. Close cancels and joins that routine. Departures stay sticky. Initial failures return from
Start; later failures close and enqueue settlement. Nil Start means acceptance,
which may still be waiting.
`/who` prepares its original sorted alias notice and completes immediately.
Its queued completion appends the system record after launch acknowledgement.
The UI receives transcript deltas, with no `/who` result branch.

`/shell` uses the same path and returns its command record on completion. The
shell capability retains `ShellCompleted` for compatibility. Execution failures
are separate from submission success; user commands and loops remain legacy.
`runtime.Registry.Register(command)` indexes the type returned by `Statement()`.
`Lookup(parsed)` selects the command; metadata comes from the command itself.
Both commands are registered. Unwired `SendCommand` retains Session's send plan,
derives staging from it and submits the native session command with that plan.
It returns the original input record with actual routing.
Lifecycle staging is proved with a shared-feed fake. Production admission,
publication and stage integration remain unwired.

The public interpreter now selects `/who` in its runtime registry and queues it
through `monolith.SubmitCommand`. The existing coordinator supplies participant
reads and runs CommandRunner, preserving acknowledgement, completion identity,
publication and shutdown. `/shell` and unsupported input still use legacy dispatch;
the monolith's existing `/who` path remains available. Native send admission and
lifecycle staging remain unwired.
