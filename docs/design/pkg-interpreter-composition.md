# Interpreter composition: discussion draft

Status: proposal for review; no runtime behavior or package boundaries have changed.

This follows [#55](https://github.com/roomscript/coderoom/issues/55) and considers
[#39](https://github.com/roomscript/coderoom/issues/39). The existing contracts in
[pkg-interpreter.md](pkg-interpreter.md) and
[pkg-interpreter-workflows.md](pkg-interpreter-workflows.md) remain authoritative.

## Current responsibility map

| Responsibility | Current implementation | Composition opportunity |
|---|---|---|
| Syntax and typed statements | `internal/promptlang/{parse,action}.go` | Independent AST/parser boundary; source spans and categorized diagnostics are missing |
| User command definitions | `promptlang.Registry`, owned by `interpreterModel` | Move runtime name resolution and reservation policy out of the syntax package |
| Native command dispatch and help | `command_definitions.go` | Register cohesive command modules without handing each the whole model |
| Mutable application state | `model.go`, transcript and approval files | Keep one canonical projection and shared application services |
| Stage planning and waits | `stageWorkflow`, `core_send/broadcast/handoff.go`, stage support files | Separate command-specific planning from reusable readiness and interruption policy |
| Loop orchestration | `loopWorkflow`, `core_loop.go` | Depend on participant delivery and shell capabilities rather than session protocol |
| Effect execution and causal ordering | `executor.go`, `instruction_runner.go`, `session_execution.go` | Retain one coordinator; isolate session and shell adapters behind narrow ports |
| Result ownership | `core_events.go`, `workflowRef` | Route correlated results to their registered owner without central workflow-kind switches |
| Front-end contract | `api*.go`, event dispatcher, snapshot cache | Preserve the facade, publication order, detached state, and lifetime ownership |

The package is already composed along the decision/effect boundary: workflows
return instructions and one runner executes them. Its remaining coupling is
explicit: the command catalog receives `*interpreterModel`, the model names each
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

Staging decorates planned delivery. It retains a frozen plan, tracks readiness
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

- Distinguish a parsed statement, a prepared delivery plan and a dispatch effect.
  Decorators operate on prepared plans, preserving frozen required/optional
  recipients and source-selection rules.
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

## Relationship to #39

Implement #39 first as a focused syntax and diagnostics change. Typed statements
already exist, but parsing uses command switches, nodes have no source spans,
errors are mostly unclassified, and `internal/ui/update.go` reparses input for
debug handling, stage detection and outcome presentation. Those paths need an
explicit replacement to meet the issue's no-independent-UI-parser criterion.

Keep raw input and source spans through execution. Separate syntax validation
from room-scoped command lookup and reserved-name policy. #39 does not need to
settle the runtime composition, and runtime experiments can initially use
existing statements. Parser extensibility does not necessarily mean dynamic
grammar registration; grammar and runtime command registration are separate.

## Incremental proof and review decisions

1. Complete #39 while preserving submission and diagnostic behavior where the
   issue does not explicitly change it.
2. Prove one addressed-send path using a delivery port: direct delivery without
   staging, then the same prepared plan through staging. Keep code in the current
   package until the boundary is useful without exporting plumbing.
3. Separate command-specific plans from shared staging mechanics; verify
   broadcast departures and handoff source waits before extraction.
4. Inject staged delivery and shell capabilities into the loop. Remove central
   workflow routing switches only after correlated ownership is demonstrated.
5. Extract cohesive packages and construct the catalog from installed modules.

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
