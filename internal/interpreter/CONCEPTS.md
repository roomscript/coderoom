# Interpreter migration boundary

`interpreter.Interpreter` is the public entry point. Its existing methods and
options remain available to the CLI and front ends. The current implementation
lives in `monolith`, preserving its interface and behavior.

The facade selects `/who` and `/shell` through its own runtime registry. It submits the selected
command through the additive `monolith.SubmitCommand` bridge, which queues it on
the existing coordinator and uses `runtime.CommandRunner`. Other statements and
parse failures delegate to the original `Submit` interface before execution.
The monolith's command handlers and registry remain intact. The monolith remains the sole
owner of Session execution, canonical room state, ordered publication, admitted
workers and shutdown. The facade adds no observer, execution queue or transcript.

`api` contains the shared events, snapshots, submission errors and detached record
inspection helpers. Both the facade and monolith alias these contracts, so the
monolith never imports its parent package and event types need no conversion.
`runtime` and `std` stay independent command contracts and modules.

The shared bridge owns stage admission, detached participant reads, invocation
identity, launch acknowledgement, queued completion publication and shutdown.
The facade adds only command selection; it never retries through fallback after a
selected command fails. Unsupported input is parsed again by the unchanged legacy
Submit path, preserving its admission-before-syntax-error behavior.

Native asynchronous send admission and same-primary result ownership remain the
next capability. Session delivery and InvocationLifecycle staging are unwired;
the bridge currently supplies participant reads and the existing shell launcher.

Start at `api.go` and `api_requests.go` for the facade; see
[monolith concepts](monolith/CONCEPTS.md) for current execution algorithms and
[composition design](../../docs/design/pkg-interpreter-composition.md) for the
migration agreement.
