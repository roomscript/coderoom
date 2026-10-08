# Interpreter concepts

The interpreter turns requests and session events into actions, keeping plans
until they can proceed. It owns prompt meaning, command definitions, workflows,
and the room's conversation history. Session owns lifecycle and delivery;
front ends present interpreter state and events.

This is the proposed core model for #55, not a description of new Go types.

| Concept | Meaning |
|---|---|
| Request | What the user wants: submit, edit, discard, interrupt, or resolve an approval. |
| Action | What to do: send, execute a command, or evaluate a condition. |
| Plan | The action, chosen recipients, source-selection rules, and requirements. |
| Requirements | What must be true: recipients are ready, or source output is complete. |
| Pending work | A plan kept for later because its requirements are not yet met. |
| Workflow | Rules for moving a request through actions, waits, and outcomes. |
| Outcome | Acceptance, queued success, delivery, failure, or work completion: distinct milestones. |
| Published event | An outcome, transcript change, or state change delivered to observers. |

## Core algorithms

```text
handle user request:
    check whether the request is allowed in the current state
    interpret and validate the request
    prepare a plan
    execute it if ready, otherwise keep it as pending work
    report the outcome and observable changes

handle session event or asynchronous result:
    apply session facts to the room and application state
    check whether the event or result advances current work
    advance affected workflows, ignoring results from superseded work
    discard or fail work that can no longer proceed
    execute actions whose requirements are now satisfied
    report the outcomes and observable changes
```

Session events update the room even when no workflow is waiting for them.
Matching a result to current work determines whether that work advances.
Inspection reads detached state; closing settles accepted work and stops execution.

## Example: addressed send

```text
User requests: send “hello” to Ada.
Prepare a plan: message, fixed recipients, readiness requirements.
Ada is starting: keep the plan and wait.
Ada becomes ready: execute the plan when all required recipients are ready.
Session reports delivery: publish the outcome.
```

## Waiting and observation

Preparation reads and session commands run synchronously. Actual waits return
control; later readiness, output, or shell results resume work. Accepting a
cancellation request does not mean the participant is ready.

Staged recipients stay fixed. Optional recipients who leave can be removed;
losing a required target discards the stage. Session checks readiness again at delivery.

Published events tell observers what happened. They receive initial state, then
events in order: conversation changes and detached state. Input accepted for
delivery is recorded before its resulting output. Events caused by execution are
applied before its result advances work. Delivery means adapter acceptance, not
work completion.

See [workflow design](../../docs/design/pkg-interpreter-workflows.md) for detailed
ordering guarantees and [session concepts](../session/CONCEPTS.md) for delivery terms.
