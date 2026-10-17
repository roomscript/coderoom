# Temporary plan for #61

Issue: [Compose interpreter from command modules and staged delivery capabilities](https://github.com/roomscript/coderoom/issues/61).

This is a working checklist, not a new architecture contract. Implement one small
step at a time, inspect its diff and validation, then revise the next step from
what we learned. Do not implement the entire list in one pass. Leave commits to
the human. Move settled decisions into the design docs and remove this file when
the issue is complete.

## Agreed runtime decisions

Runtime changes require explicit agreement before implementation.
WaitFor and ToInform now both guarantee participant availability and propagated
completion of prior work; departure behavior distinguishes them. Removed the
separate WaitForCompletion list and Staging.Routing override. Stage presentation
shows all frozen prerequisites; actual delivery routing remains Session-owned.
Session facts distinguish the last completed turn from the current/aborted turn,
so startup, keepalive and failed preparation cannot introduce nonexistent waits.
CommandError and redundant preview/event plumbing were removed by agreement.

## Session execution identity checkpoint

Session.Execute now returns (session.ExecutionID, error) for every command,
including failures and commands that start no work. Delivery passes the ID to
PrepareForWork; BeginWorking commits pending provenance as ActiveExecutionID.
Broadcast recipients share that origin, and participant instance identity separates
completion facts. Existing work keeps its origin when receiving a notice.
Participant ActiveExecutionID exists only during running work and clears on
completion, abort or crash. Participant View owns LastCompletedExecutionID,
updated by normal completion and supplied through ParticipantSnapshot and ordered
ParticipantWorkCompleted facts before idle.
The terminal AgentMessage carries ExecutionID into canonical room state; room
retains LastCompletedExecutionIDs per participant instance. There is no separate turn
counter, and backend agent turn/stream IDs remain unchanged. Runtime's asynchronous
execution capability retains its admission-only return contract.

## Resume here

The new `internal/interpreter` selects `/who`, `/shell`, addressed sends, broadcasts and handoffs and executes them
itself. Definitions, named invocations and loops also use the modular path. It schedules `executeCommand` on the shared serialized lane, asks the
monolith to admit input, then prepares, starts, tracks, completes and closes its
own `runtime.InvocationLifecycle`. It also owns one retained visible stage and
its admission/edit/discard/interrupt policy. Unrecognized commands use monolith
fallback after the new interpreter checks its stage admission rule.
There is no Execution interface, execution wrapper or SubmitInvocation callback.

The monolith supplies only infrastructure for this path: `Schedule`,
`AdmitCommand`, invocation-scoped Context and `CommandSubmission` publication
methods. The submission is metadata/capabilities, not a lifecycle owner.
`QueueCompletion` preserves causal delivery ordering and shutdown draining;
`WithShutdown` lets the new interpreter close its own remaining invocations
before publication shuts down. No migrated lifecycle is stored, started,
completed or closed by the monolith. `SubmitCommand` remains a compatibility
bridge for legacy callers and direct-delivery proofs. Existing handlers stay intact.
`WithStageSnapshot` projects the new interpreter's stage into the same detached
snapshot/event stream; legacy stage operations are called only when there is no
new-interpreter stage.

The migration destination is the new interpreter replacing the monolith. Move
command and stage policy into the new interpreter; expose only the monolith
capabilities needed during that transition. Do not add parallel interface/type
hierarchies around an existing lifecycle or move its policy into the monolith.
Keep one execution lane and canonical publication path until those remaining
responsibilities can be transferred and the monolith removed.

Read `internal/interpreter/std/CONCEPTS.md`, `runtime/invocation_lifecycle.go`,
`command_lifecycle.go` and `monolith/command_admission.go` before continuing.
Inspect both staged and unstaged changes; leave staging and commits to the human.

Current invariants and proof:

- Keep `Invocation.Go(complete)` unchanged. Departed notices use
  `Staging.DiscardInform(alias)`; required departures fail waiting work.
- Session selects recipients once in `ParticipantSendPlan`. Staging derives its
  frozen aliases from that plan. DiscardInform changes the retained delivery
  plan, not the readiness description. Instance references reject replacements
  even before their departures reach the lifecycle.
- `SubscribeParticipants` atomically captures state and subscribes to later facts
  in mutation order. The lifecycle's participantSubscription merges initial
  state only for untouched aliases, retains sticky departures and validates the
  state revision before launch. Ordinary inspection is not an event barrier.
- Participant Group owns membership, snapshots and ordered change subscriptions
  under the Session state lock; `changeParticipants` owns transition capture and
  post-unlock delivery. Existing source tests cover ordering and replacement.
- Native send admission remains on the monolith's serialized lane. Routing
  terminal events are invocation-scoped. Same-primary early rejection is private to
  SubmitCommand compatibility; native availability remains Session-owned.
  Input records precede causal delivery events; terminal publication follows projection.
- Lifecycle identity, pending state, acceptance and cleanup belong to the new
  interpreter. Completion callbacks only queue work. The new interpreter consumes
  results on the shared lane, closes resources and publishes accepted
  results. During native delivery the monolith runs queued result work before
  causal event projection, then publishes terminal outcomes after those events.
  Duplicate/failed-start/late results are rejected by the new interpreter.
- Shutdown drains admitted execution and queued results, then invokes the new
  interpreter's cleanup hook. Both explicit close and context cancellation close
  its remaining lifecycles in submission order. The monolith knows no lifecycle.
- Real-Session tests through the new interpreter prove busy/startup waiting,
  required departure, close while waiting, direct/partial delivery, causal
  record ordering and subscription cleanup. Send is publicly registered.
  `/who` and `/shell` use this lifecycle path with nil staging.

Lifecycle advancement checkpoint:

- Participant callbacks collect facts and queue advancement. The new interpreter
  calls runtime ProcessEvents on the shared lane, alongside retention, stage operations,
  completion and shutdown. Runtime retains readiness policy and transitions.
- Removed the private monitor goroutine, lifecycle mutex and stop/join machinery.
  The collector lock and revision commit still arbitrate against concurrent facts.
- Start reports initial waiting before queued advancement can run. Readiness or
  departure during retention cannot bypass visible-stage ownership.

Current visible-stage checkpoint:

- A waiting invocation retains its source and immutable stage description. Routing
  presentation for send is derived from `WaitFor` followed by `ToInform`.
  Lifecycle `Readiness` supplies detached collected facts; `RequestEventProcessing` only
  queues advancement and projection on the shared lane, never rereads Session.
- The stage stays visible until delivery settles. It blocks new work, including
  fallback commands and malformed input; presentation-only debug commands remain
  available. A legacy stage still blocks migrated commands at admission.
- `DiscardWaiting` shares the advancement lane and uses the collector revision boundary
  to prevent edit/discard after committed delivery or collected readiness. A
  successful removal stops collection, closes resources and revokes native admission, with
  no input record or completion notice. Ready/failure progress runs only through queued ProcessEvents calls.
- Interrupt cancels only frozen active, cancellable blockers. Failed aliases can
  be retried; successful cancellations are remembered but do not imply readiness.
  Startup/maintenance waits are not cancelled. Required departures publish the
  legacy discard notice; partial delivery preserves actual routing and failure.
- New-interpreter tests cover detached/published snapshots, both submission orders
  with legacy staging, admission, editing/resubmission, discard, retry/partial
  cancellation, startup, departures, frozen joins and shutdown. Runtime tests prove
  late readiness cannot revive discard and collected readiness wins over removal.

Public addressed-send cutover checkpoint:

- SendCommand is publicly registered; lifecycle and stage regressions use production
  registration. Definitions and loops remained fallback at this checkpoint.
- Preparation publishes frozen InputAccepted routing only after validating the
  target. Missing/crashed targets retain participant-unavailable classification.
- The new interpreter owns send acceptance routing, unavailable-target error
  classification and direct/queued outcome selection. Monolith admission only
  checks input and supplies capabilities; publication adds source metadata and
  preserves causal order for caller-supplied events.
- Direct sends settle submission after actual delivery and dispatch publication.
  Waiting sends acknowledge retention; later failures do not repeat submission
  outcomes. Partial routing and causal record ordering are preserved.
- Scheduled turns drain admitted native delivery before the next queued operation
  can inspect readiness. Regressions cover consecutive modular sends and fallback
  broadcast behind a direct send, alongside both stage submission orders.
- UI tests consume the stage snapshot published before queued acknowledgement.
  Loop staging and coexistence remain separate design checkpoints.

Broadcast cutover checkpoint:

- BroadcastCommand is publicly registered and composes the same runtime delivery
  mechanics, lifecycle and visible stage as send. Preparation freezes non-crashed
  instances, including startup waits; optional departures cannot add joins or replacement instances.
- Std owns construction, frozen departure policy, terminal validation and formatting.
  Runtime Delivery supplies subscription, launch, single terminal-event acceptance
  and cleanup.
- SessionExecution retains Subscribe/Execute: subscribe to invocation-scoped
  runtime.ExecutionEvent before executing the actual Session command.
  ExecutionCompleted forwards actual routing; ExecutionFailed terminates protocol
  failures without invented recipients or kind. Missing routing fails in Delivery;
  malformed and multiple outcomes fail at the adapter rather than stall.
  There is no DeliveryRequest, routing template
  or ownership-matching predicate; runtime owns terminal subscription mechanics.
- Common native admission has no alias reservation; Session's locked transitions
  own availability. Only the private SubmitCommand compatibility wrapper preserves
  historical early rejection of queued addressed sends to the same primary.
- Capability regressions cover identical-recipient ownership, native same-primary
  admission, protocol failures without routing metadata and private bridge behavior.
  Interpreter identity, lifecycle driving, staging, settlement and shutdown stay put.
- Transfer execution/publication capability implementations when fallback work
  migrates; remove the monolith and SubmitCommand bridge once no consumers remain
  at checkpoint 9. Keep runtime mechanics driven by the interpreter.
- Empty rooms fail before acceptance. All frozen recipients departing discard
  waiting input with the existing notice. Partial delivery preserves actual and
  failed routing; total failure creates no input record.
- Regressions cover direct success/partial/total/missing results, publication
  order, waiting stage operations, startup, departures, replacements, joins and
  cleanup. UI tests consume retained state before queued acknowledgement.

Handoff cutover checkpoint:

- HandoffCommand is publicly registered and uses the same interpreter-driven
  lifecycle, stage operations and delivery machinery. Std freezes explicit source
  and destination identities and existing routable/startup-ready bystanders.
  Required departures discard waiting work; optional departures remove bystander
  prerequisites. Joins and replacements cannot expand the retained plan.
- Context.History exposes only LatestOutput(alias), a detached canonical read.
  After staging is satisfied, Go reads the source and fills the retained Session
  command's Source before execution. There is no re-Prepare phase, source-owned
  readiness subscription, HandoffSources capability or ReadinessGate.
- WaitFor and ToInform share one readiness rule: idle, startup-ready, with the
  last completed turn propagated into canonical state. Session's atomic facts
  supply last-completed history in participant views; startup,
  keepalive and aborted work do not advance it.
  The interpreter feeds projected turn facts from the existing room observer into
  the lifecycle and queues advancement after projection. Runtime evaluates facts;
  the interpreter drives advancement, settlement and shutdown.
- Stage presentation shows every frozen prerequisite. ExecutionCompleted returns
  actual routing without source previews; Session's HandoffDelivered audit projects its preview
  through the canonical room. Std consumes no preview or causal-event payload.
- Regressions cover idle before final projection, current versus stale execution origins,
  source selection and audit outcomes, frozen prerequisites, required departures,
  edit/discard/interrupt and shutdown. Existing UI source-flush, non-completing
  flush, starting-bystander, late-join and failed-notice tests use the public path.
- Execution/publication and projection-hook implementations remain temporary
  monolith capabilities, transferred and removed with fallback at checkpoint 9.

User command checkpoint:

- DefineCommand and UserCommand are publicly registered. promptlang.Registry stays
  in promptlang; the interpreter owns one instance exposed as Context.NamedCommands
  through the agreed Define/Resolve capability. The legacy loop and public queued
  ResolveCommand API share that same instance via WithNamedCommands.
- Definitions store unevaluated shell bodies. Definition records precede successful
  submission outcomes. Duplicate/reserved names retain diagnostic codes and spans.
  Named invocation resolves before acceptance; undefined names publish UnknownCommand
  without recording input. Execution reuses shellInvocation with /name display text.
- The interpreter drains already-queued synchronous results after Start bookkeeping
  and acknowledgement, before the next input. This preserves definition confirmation
  order for a queued definition followed immediately by invocation. Callbacks still
  only queue results; failed starts and stale/duplicate completions cannot settle twice.
- Named shell execution retains asynchronous launch, completion events, cancellation
  and joined shutdown. The compatibility shell event reads its display name from
  the command-owned record rather than reconstructing named-command policy.
- Existing legacy handlers remain for direct monolith compatibility until removal
  at checkpoint 9. Registry ownership and public command execution have moved out.

Current checkpoint: loop cutover and its event-processing review are complete; commit checkpoint 6, then continue with checkpoint 9.

Checkpoint 6, first agreed substep: runtime.ExecutionCompleted now carries the
ExecutionID returned by Session.Execute alongside the actual routing outcome,
including failed delivery. Subscribe/Execute and loop behavior remain unchanged.
By subsequent agreement, Delivery.Go passes the full ExecutionCompleted event
and a separate execution/protocol error to its callback. Execution identity now
reaches helper consumers without a second subscription or additional result type.
Fresh execution scopes are implemented by subsequent agreement:
Context.Session exposes SessionExecutionProvider, retaining participant reads,
planning and lifecycle subscriptions. CreateExecution returns an independent,
single-use Subscribe/Execute handle for each delivery. Provider lifetime belongs
to the interpreter; closing one scope leaves siblings usable, while closing the
invocation revokes existing and future scopes. Causal publication stays at the
provider boundary. SubmitCommand keeps its private compatibility admission policy.
Loop behavior is unchanged. Event processing is implemented by subsequent
agreement: Context.EventProcessing.RequestEventProcessing queues interpreter attention, and the
optional InvocationEventHandler.HandlePendingEvents consumes facts on the interpreter lane.
The EventProcessingRequester capability is bound before Prepare. The lifecycle uses the same ProcessEvents
entry point for waiting readiness and running invocation processing; it ignores
requests after settlement/closure. Existing commands need no processor method.
Intermediate publication is implemented by subsequent agreement as one
interpreter-owned invocationPublication component, exposed through
Context.Publication (runtime.InvocationPublication). Publish takes existing records
and explicit api.Events and runs only in Go or HandlePendingEvents on the lane.
The component binds invocation identity/source and checks the existing lifecycle's
IsRunning fact rather than retaining another active flag. Prepare, Close and stale
handles cannot publish. Final settlement still belongs to the interpreter.
CommandSubmission.PublishRecords extracts the existing record/event/snapshot path
without closing execution scopes or synthesizing shell events. Final publication
reuses that construction and preserves its existing terminal ordering. The native
provider batches before/after-causal-event sequences so intermediate updates and
final output cannot overwrite one another. A cached child shell result is not
replayed when final output contains no new shell record. The monolith remains a
temporary publication mechanism removed with fallback at checkpoint 9.
Loop admission/acknowledgement boundaries are implemented as an agreed substep:
the interpreter checks pending modular loops before preparation, with no separate
reservation state. Optional runtime.SubmissionAcknowledgement.ReadyToAcknowledge supplies
a pure milestone query after Go/event handling; the interpreter acknowledges once
or settles a still-pending submission at completion/shutdown. Loop execution now uses that milestone after its first delivery settles. Boundary tests use a narrow
loop invocation fake, including pre-Prepare rejection and admission after settlement.
Std.LoopCommand is now publicly registered. Its invocation uses a fresh delivery
scope per iteration and freezes the resolved condition and participant instance.
Callbacks append facts to an invocation-owned inbox and request attention;
HandlePendingEvents alone updates phases, publishes output and starts subsequent
work. First delivery settlement supplies the acknowledgement milestone. Session
still decides immediate delivery availability; loop staging remains out of scope.
Normal completion must match the delivered ExecutionID and be recorded in room.
An observed end of that same active execution without normal completion permits
condition evaluation after rollback. A real-Session cancellation regression proves
that interruption with a terminal flush emits the matching completion before idle,
waits for room history, and cannot be triggered by an unrelated participant's idle.
Unrelated idle/completion and replacement instances cannot advance the loop.
Condition results retain iteration identity. Crash handling consumes the actual
participant-subscription StatusCrashed transition. Regressions cover all loop
phases and a real reader crash followed by immediate loop admission. The interpreter
drains synchronous event-handler completions before the next input, matching Go's
existing settlement ordering.
By explicit agreement, departure stops the invocation and late condition output
is ignored; host-owned worker shutdown remains unchanged. Legacy loop code remains
only for direct monolith callers until checkpoint 9. The concrete event-processing
flow has been reviewed and retained for checkpoint 6. The real-Session cancellation
regression completes the agreed follow-up; the cutover is ready for commit.

Event-processing review completed for checkpoint 6: retain the parameterless
handler because the invocation owns its inbox and one processing turn can consume
several collected facts. Empty or redundant processing requests are valid. The
concrete flow is subcommand callback -> collect fact -> request attention ->
interpreter checks identity -> lifecycle calls HandlePendingEvents -> loop decides
its next action -> interpreter accepts completion and settles. During checkpoint 9,
transfer temporary execution/publication mechanisms without moving loop policy
into runtime. Runtime changes still require explicit agreement.

Future handoff history selectors (latest, earlier, all) remain a separate feature;
this checkpoint adds only the latest-output read required by current syntax.

Validation: `go test ./...`, `go test -race ./...` and
`go test -race ./internal/interpreter/... -count=3` passed. The interrupted
publication-test check also passed with `go test -race ./internal/interpreter/monolith`.
The updated `golangci-lint run ./...` passes on the current Go toolchain. Local
G703 annotations document intentionally caller-selected paths in the replay and
upgrade-comparison CLI utilities; G703 remains enabled globally. Relevant CLI and
transcript tests pass.

## Scope assessment

Loop staging is deferred to [#65](https://github.com/roomscript/coderoom/issues/65)
as a separate feature. This migration preserves existing loop delivery behavior,
then proceeds to remaining handlers and monolith removal. The former checkpoints
7–8 are outside this migration; their design decisions and acceptance criteria
are recorded in #65. Existing send, broadcast and handoff staging remains in scope.
#39 is closed; the parser already supplies parsed statements, source spans and
diagnostics. #55's decision/effect boundary is the starting point.

Relevant starting points:

- `docs/design/pkg-interpreter-composition.md`: existing proposal; its #39
  prerequisite discussion predates the completed parser work.
- `docs/design/pkg-interpreter-workflows.md`: implemented ordering and ownership.
- `command_definitions.go`: execution/help catalog already exists, but handlers
  receive the whole model.
- `core_events.go`, `workflow_collection.go`: result routing names loop/stage
  explicitly; correlation already includes generation and request identity.
- `core_send.go`, `stage_plan.go`, `core_delivery.go`: addressed-send proof path.
- `core_loop.go`: loop sends currently dispatch session requests immediately.
- `core_input.go`, `core_stage_operations.go`: current admission and atomic stage
  operations to preserve or deliberately extend.

## Deferred feature: loop staging

[#65](https://github.com/roomscript/coderoom/issues/65) owns the policy decisions,
transition table, UX and implementation of staged loop delivery. Establish correct
participant-instance and ExecutionID correlation during loop migration, preserving
current delivery behavior. Resolve coexistence and staged delivery in the follow-up.

## Small implementation checkpoints

Each checkpoint migrates a complete command path and introduces only the
contracts that path needs. Split a checkpoint into smaller diffs when necessary,
but keep each diff working and independently reviewable. Existing commands
continue through a temporary legacy-handler bridge until migrated. Keep code in
the current package until a real consumer demonstrates a useful boundary.

- [x] 0. Read #61 and the current implementation; create this temporary plan.
- [x] 1. Migrate `/who`: `std.WhoCommand` is registered in the interpreter-owned
  runtime registry. Shared dispatch selects by parsed statement type, supplies
  detached participant reads and launches `Go(complete)`. Legacy participant-read
  instructions and result routing are removed. Help uses registered metadata in
  its original position. Tests cover ordering, source, exactly-once completion,
  shutdown and another registered command without a specific handler.
  `/shell` is now registered through the same path. All registered commands
  acknowledge successful launch before queued records; completions report execution
  outcomes without a second submission result. Shell compatibility events remain.
  User commands, definitions and loop execution remain on the legacy path.
- [ ] 1a. Deferred: revisit runner responsibilities after send and staging are
  clearer. Keep invocation identity, acknowledgement, completion tracking and
  shutdown settlement in the interpreter. `runtime.CommandRunner` continues to
  call `Prepare` and `Go`; coordinator extraction is not the next checkpoint.
- [x] 1b. Move the current implementation behind the new public facade:
  `monolith` retains the existing implementation/interface; shared `api` contracts
  preserve front-end types without a parent-package dependency. The facade delegates
  all work to one runtime, with no routing changes. Public API tests stay at the
  facade boundary; implementation tests move with the monolith. This is separate
  from admission and command wiring.
- [x] 1c. Wire one modular facade command: `/who` is selected through the facade
  runtime registry and queued through the additive `monolith.SubmitCommand` bridge.
  Existing monolith files/handlers remain unchanged. Regressions prove selection,
  publication order, duplicate completion rejection, accepted work at shutdown,
  stage admission, source identity and existing /who output.
- [x] 1d. Wire `/shell` through the facade registry and the same coordinator bridge.
  Verify structured output and execution errors, launch-before-completion ordering,
  and cancellation with joined shutdown. Existing monolith handlers remain intact.
- [x] 1e. Prove native addressed-send admission through the shared owner without
  public registration. Tests cover direct and partial delivery, rejection,
  missing outcomes, same-primary overlap with legacy sends, worker admission,
  causal transcript ordering, cleanup and admitted delivery during shutdown.
- [x] 1f. Expose scheduling, admission, capability and publication APIs. The new
  interpreter owns the complete command/lifecycle algorithm and pending map.
  The monolith has no migrated lifecycle references or orchestration callback.
  Tests cover failure/duplicate/late results, shutdown/context cancellation,
  real-Session readiness, departure and partial delivery with causal publication.
- [x] 2a. Addressed-send preparation and direct delivery: SendCommand retains
  Session's frozen ParticipantSendPlan and derives readiness and presentation
  aliases from it. The same plan drives native delivery. Invocation-scoped routing
  and queued completion preserve original input, partial routing, causal ordering
  and cleanup. Direct and waiting paths are proved through the new interpreter;
  public registration remains a separate cutover.
- [x] 2b-i. Addressed-send visible staging with test-only registration: the new
  interpreter owns one stage, detached readiness presentation and single-stage
  admission across migrated and fallback input. Atomic edit/discard revoke scope
  and prevent late delivery. Interrupt retains failed aliases for retry and waits
  for causal readiness. Tests cover startup, partial cancellation, departures,
  frozen recipients, resubmission and joined shutdown.
- [x] 2b-ii. Public addressed-send cutover: register SendCommand after comparing
  legacy direct/queued submission milestones, semantic events, cancellation retry,
  partial delivery and interaction with existing workflows. Keep other commands
  on fallback. No loop staging behavior change in this checkpoint.
- [x] 3. Migrate broadcast: extend the proven delivery capability for frozen
  optional recipients, departures and partial results. Joins must not expand a
  retained plan. Preserve existing behavior and cover its complete command path.
- [x] 4. Migrate handoff: add detached canonical source reads to the command
  context. Preserve required departures, source selection and active-source
  completion waits. Do not duplicate the transcript or create another observer.
- [x] 5. Migrate user command definitions (`/shell` is already migrated):
  prove command resolution, including definitions
  and named invocation. Preserve asynchronous launch, result publication,
  cancellation and stale-result behavior. These become the capabilities loop uses.
- [x] 6. Migrate loop with its current delivery behavior: inject delivery,
  participant facts, shell and command resolution; test with narrow fakes rather
  than a fake complete Session. Establish participant-instance and delivered-turn
  completion identity. Cover unrelated idle/turn events, cancellation, same-alias
  replacement and departure during condition evaluation. Document any necessary
  completion-correlation correction explicitly; do not introduce staged sends yet.
- Deferred 7–8. Loop-staging design and implementation moved to
  [#65](https://github.com/roomscript/coderoom/issues/65); they do not block this
  migration. After checkpoint 6, continue with checkpoint 9.
- [x] 9a. Application-defined commands: promptlang.Registry resolves shell and
  application definitions in one namespace. Each named invocation resolves once,
  retaining its definition through admission and preparation; RequireShell validates
  resolved loop conditions without a second lookup. NewWithApplicationCommands validates/copies a batch
  before interpreter startup. The registry owns each complete application definition
  (name, description and callback), with no parallel implementation map. Application
  commands remain available during staging; participant work submitted by callbacks
  passes through existing admission. Callbacks run in the ordered
  publication dispatcher after preceding observer events, and queue results back
  to the interpreter for acknowledgement/settlement. Success means dispatch acceptance;
  shutdown drains admitted callbacks before closing completion intake. UI-owned
  debug callbacks now enqueue UI messages,
  replacing dedicated parser types and interpreter events. Existing New is unchanged.
  Arguments and asynchronous callback completion remain out of scope.
Registry unification is deferred to [#66](https://github.com/roomscript/coderoom/issues/66).
It is outside the current application-command change; do not expand this diff to merge
the runtime and promptlang registries or decide their package ownership.

- [ ] 9. Migrate remaining handlers and remove the temporary bridge and monolith
  fallback. Transfer the remaining monolith responsibilities to the new interpreter;
  deprecate and remove the monolith once no consumers remain. Confirm new
  commands require neither model methods nor workflow-specific central branches.
  Extract cohesive packages one at a time only where migrated code demonstrates
  value. `runtime`, `std`, `stage`, `user` and `ast` remain candidates, not a
  mandatory directory checklist; do not move the parser just to match a name.
- [ ] 10. Remove superseded plumbing, reconcile architecture/workflow docs,
  check every #61 acceptance criterion, run final validation and remove this plan.

## Proposed runtime changes for the remaining handlers

The execution capability and quit changes below are implemented by agreement.
Review any further runtime changes before implementing the remaining-handler batch.
They preserve existing product behavior rather than introduce new features.

- Execution capability update is implemented by agreement, without migrating new
  commands: ExecutionCompleted replaces DeliveryCompleted, carrying Session.Execute's
  ID, authoritative Err and optional actual routing. Empty Result.Kind means absent;
  Result.Err is derived from Err only when routing exists. ExecutionFailed remains
  a host/protocol failure for malformed or multiple routing outcomes. Delivery
  requires routing; commands retain kind/recipient/role validation. Ordinary command
  success/failure can now complete without invented routing. Completion means the
  Session command returned, not participant work completion. Remaining handler
  migration is a separate step.
- `/policy enable` is publicly registered as std.PolicyCommand. It captures input
  in Prepare, subscribes before admitting Session execution, and consumes
  ExecutionCompleted/ExecutionFailed without requiring routing. Existing submission
  acknowledgement keeps success/failure pending until execution and causal
  publication; no runtime or central command branches were added. The invocation
  closes its execution subscription. Legacy policy handling/help remains only for
  monolith compatibility until removal.
- `/invite`, `/remove` and `/cancel` are now publicly registered modular commands.
  They reuse std's unroutedInvocation with policy, preserving subscription
  cleanup and submission settlement after execution/causal publication. Invite
  supplies the requested name; remove/cancel capture the current instance or
  preserve a missing name for Session rejection. Session remains lifecycle authority.
  No runtime changes or new central dispatch branches were needed. Legacy handlers
  and help remain for direct monolith callers. `/help` is the next migration step.
- `/quit` is migrated by agreement as a UI application command, like `/debugview`.
  The callback queues a request; asynchronous cleanup calls Interpreter.Close(),
  which owns Session shutdown and publication draining, then queues completion behind drained submission outcomes
  before UI exit. The dedicated parser statement, handler and ExitRequested event
  are removed. No ShutdownRequester runtime capability is needed.
- `/help` can use existing registry metadata and publication machinery; identify
  the narrow read needed when migrating it, without creating another catalogue.
  Registry unification and its ownership decision remain deferred to #66.
- UI debug commands are already migrated as application callbacks. They need no
  additional runtime capability or dedicated interpreter event types.

Implement the remaining-handler batch after that contract review. Keep removal of
the temporary monolith execution/publication bridge a separate reviewable step.

Result ownership and lifecycle subscriptions migrate alongside each command that
needs them, rather than as separate interpreter-wide prerequisite steps:

- Register preparation/execution result owners with generation/request identity;
  reject unknown, stale and duplicate results without advancing work.
- Register lifecycle-fact subscriptions separately from result routing. New
  retained commands receive relevant facts without adding central branches or
  independent Session observers. Replacement/unregistration prevents stale work
  from advancing.
- The coordinator projects complete causal event bursts before derived effects or
  execution outcomes advance workflows. The legacy bridge must preserve this
  ordering for unmigrated handlers too.
- Modules prepare invocations, publish intermediate records/events through their
  publication capability, and return final records through completion callbacks.
  Session execution goes through the host's serialized admission capability;
  modules cannot independently execute raw Session or mutate/observe the
  canonical room. Acceptance and terminal
  submission reporting stay at the outer submission boundary.

## Guardrails and validation

One serialized coordinator continues to own execution, canonical projections,
ordered publication, result routing and shutdown. Workers only enqueue results.
Session remains final lifecycle/readiness/policy/delivery authority. Preserve
detached front-end state, frozen recipients, causal transcript ordering, atomic
stage operations, partial results and interruption retries throughout.

For each code checkpoint, run focused affected tests and existing regressions for
the touched path. Use table-driven variants and narrow fakes; assert observable
behavior rather than error text. Run `make test`, `make test-race` and `make lint`
at cross-cutting checkpoints and at completion. Existing startup, causal ordering,
partial delivery, stale completion, cancellation retry and atomic stage tests
must remain green. Changed expectations require an explicitly reviewed and documented
completion-correlation fix from step 6. Loop staging remains deferred to #65.
Run external CLI integration tests separately if a backend integration boundary
changes. No tests are needed for this planning-only change.

Non-goals: runtime plugins, configurable runtime stacking, new syntax, a broad
runtime interface, or unrelated UI/session/backend redesign.

Projection readiness correction: keep completion IDs in room, read through Context.CompletedExecutions (CompletedExecutionIDReader) during runtime readiness evaluation. Atomic Session observer attachment establishes the historical completion cursor; work completed before attachment cannot block new sends awaiting replay.
