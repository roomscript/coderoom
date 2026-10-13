# Interpreter migration boundary

`interpreter.Interpreter` is the public entry point. Its existing methods and
options remain available to the CLI and front ends. The current implementation
lives in `monolith`, preserving its interface and behavior.

The facade initially delegates all requests to one monolith instance, including
the registered `/who` and `/shell` module paths. The monolith remains the sole
owner of Session execution, canonical room state, ordered publication, admitted
workers and shutdown. The facade adds no observer, execution queue or transcript.

`api` contains the shared events, snapshots, submission errors and detached record
inspection helpers. Both the facade and monolith alias these contracts, so the
monolith never imports its parent package and event types need no conversion.
`runtime` and `std` stay independent command contracts and modules.

The next capability is serialized production admission shared by modular commands
and fallback work. Command selection will happen before execution; an invocation
stays on its selected path through completion. An execution error must not cause
fallback and duplicate work. No command selection or admission cutover is included
in this package move.

Start at `api.go` and `api_requests.go` for the facade; see
[monolith concepts](monolith/CONCEPTS.md) for current execution algorithms and
[composition design](../../docs/design/pkg-interpreter-composition.md) for the
migration agreement.
