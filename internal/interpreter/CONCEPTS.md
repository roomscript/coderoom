# Interpreter migration boundary

`interpreter.Interpreter` is the public entry point. Its existing methods and
options remain available to the CLI and front ends. The facade selects `/who`,
`/shell` and addressed sends through its runtime registry. Broadcast, handoff,
user definitions and loops continue through monolith fallback.

The new interpreter owns command preparation, invocation identity, lifecycle
advancement, completion acceptance, visible staging and stage operations. It
runs that algorithm through `monolith.Schedule` on the shared serialized lane.
The monolith supplies admission, invocation-scoped capabilities, canonical room
projection, ordered publication and shutdown draining. It owns no migrated
lifecycle. The facade adds no Session observer, execution queue or transcript.

The new interpreter selects acceptance records/routing and send failure events.
Monolith admission publishes no acceptance, and its publication methods carry
caller-supplied events in causal order without send acknowledgement policy.

Addressed sends publish frozen routing on acceptance and delivered input before
causal Session events. Direct sends report submission success or failure after
delivery and `StagedInputDispatched`. Waiting sends acknowledge when retained;
later failures use `OperationFailed`, without a second submission outcome.
Successful partial delivery still publishes its actual routing and dispatch
before failure. Required departures discard retained work with the existing
notice. Both legacy and modular stages block competing work.

`api` contains shared events, snapshots, submission errors and detached record
inspection helpers. Both facade and monolith alias these contracts. `runtime`
and `std` contain command contracts and modules. `SubmitCommand` remains a
compatibility bridge for legacy callers and direct-delivery proofs.

Start at `api.go` and `api_requests.go`; see
[composition design](../../docs/design/pkg-interpreter-composition.md) for the
migration agreement. Loop staging and coexistence policy remain deferred.
