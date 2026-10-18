# Temporary interpreter infrastructure

All command dispatch, invocation lifecycles, readiness and visible staging belong
to the public interpreter and its runtime/std components. This package no longer
has a Submit path, command handlers, workflow engine or module runner.

It temporarily supplies:

- One serialized scheduling and Session execution lane.
- Independent invocation-scoped execution providers and terminal streams.
- Shell workers, cancellation and joined shutdown.
- Canonical room and approval projection, detached snapshots and transcript deltas.
- Ordered observer/application publication and source diagnostics.
- Queued named-command reads from the public interpreter's registry.

`CommandSubmission` carries source metadata and capabilities; it owns no lifecycle.
Input acceptance and its record are published together so InputAccepted precedes
the input transcript delta. During delivery, input records precede causal Session
events; intermediate output and terminal outcomes follow that causal burst.
Completion callbacks queue owner work rather than settling invocations here.

The publication instruction vocabulary contains only records, semantic events and
snapshots. Session facts update canonical state without executing command policy.
The room observer queues completion attention for the public interpreter.

Shutdown closes admission, drains admitted Session execution and shell workers,
flushes ordered application callbacks and queued results, invokes public lifecycle
cleanup, then flushes and closes publication. Scope closure revokes existing and
future admission without affecting siblings from another provider.

Transfer this infrastructure with its tests to the interpreter owner before
removing this package. Help metadata already lives in the public interpreter;
registry unification remains a separate feature.
