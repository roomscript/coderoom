# Temporary plan for #61

Issue: [Compose interpreter from command modules and staged delivery capabilities](https://github.com/roomscript/coderoom/issues/61).

This is a working checklist, not a new architecture contract. Implement one small
step at a time, inspect its diff and validation, then revise the next step from
what we learned. Do not implement the entire list in one pass. Leave commits to
the human. Move settled decisions into the design docs and remove this file when
the issue is complete.

## Resume here

Current checkpoint: the public interpreter facade now delegates to the existing
implementation in `internal/interpreter/monolith`, with shared `api` contracts.
The monolith still owns execution, transcript publication and shutdown; the facade
adds no runtime. It now selects `/who` in its own registry and uses the additive
`monolith.SubmitCommand` bridge for serialized admission and execution; other input
uses the original fallback. Existing monolith command handlers are untouched.
The send and staging prototypes remain **unwired**. `/who` and
`/shell` are registered; production addressed send still uses its legacy path.
Read `internal/interpreter/std/CONCEPTS.md` for the short concept agreement, then
`runtime/command.go`, `std/command_send.go` and their tests (under
`internal/interpreter/`). Inspect the worktree before editing; leave staging and
commits to the human.

- Keep `Invocation.Go(complete)` unchanged. The agreed departure API is
  **`Staging.DiscardInform(alias)`**, not a departure argument to `Go`.
- Session selects recipients once in `ParticipantSendPlan`. Staging derives
  readiness aliases from it. Its callback removes departed notices from the
  retained plan; it does not mutate the frozen readiness description.
  The plan retains private participant-instance references so Session rejects
  same-alias replacements even before their departure events reach staging.
  A real-Session send regression covers a blocked departure during initialization.
  Snapshot/event ordering is now handled separately by Session's atomic
  `SubscribeParticipants` boundary; production admission remains unwired.
- The unwired lifecycle keeps Context directly and delegates subscription,
  state merging, queue and worker lifetime to an unexported participantMonitor.
  Readiness/departure policy, launch and settlement remain in the lifecycle.
  It prototypes a private monitoring routine for waiting work. Start atomically
  captures initial views and subscribes to subsequent facts through
  `SubscribeParticipants`, then fills only missing aliases from the initial list.
  The callback records state and signals a private wake-up queue. The routine handles readiness, sticky departures, launch and failures.
  Launch validates the evaluated state revision and freezes collection atomically;
  updates collected during evaluation trigger a retry before dispatch.
- Close cancels and joins monitoring; an already-committed Go may finish admission.
  Go and completion signatures remain unchanged. Session admission must tolerate
  calls from the monitoring timeline and still serialize native execution.
  `Subscribe` handles routing continuations; `SubscribeParticipants` supplies
  atomic lifecycle initialization. Enqueue remains removed.
- The private queue sees collected facts, not departures still buffered upstream.
  Session queues lifecycle facts with mutations and excludes pre-snapshot facts
  from new subscriptions. The future adapter must preserve this boundary and
  prove admission ordering. Tests cover ready/stop/rejoin facts collected before evaluation, initialization cancellation,
  readiness/discard races, partial delivery and monitor cleanup.
- This is an explicit prototype exception to coordinator-owned workflow advancement.
  The interpreter still owns publication, admitted worker cancellation and shutdown.
  Production CommandRunner, registered commands and legacy sends are unchanged.
- The atomic snapshot/subscription boundary is implemented separately from
  production wiring.
  A real-Session lifecycle regression retains work on a busy snapshot despite a
  delayed ordinary ready notification, then launches once on the next idle fact.
  Another preserves newer idle facts arriving before the initial snapshot returns.
  Source tests cover callback serialization, pre-snapshot exclusion, readiness,
  removal/replacement, unsubscribe and shutdown. The earlier unordered fake remains
  a negative contract test; snapshot precedence is unchanged.
- Lifecycle transitions now use one `changeParticipants` boundary that owns
  locking, before/after view capture and post-unlock delivery. Callers only describe
  the participant change. `participant.Group` replaces Registry and owns membership,
  snapshots and ordered change subscriptions. Session retains runtime bookkeeping
  and translates participant facts into Session events. Group shares the Session
  state lock; no transition separately queues facts or flushes callbacks.
- `/who` now proves facade selection and the shared coordinator bridge. It uses
  CommandRunner, stage admission, detached reads, invocation identity, acknowledgement,
  completion publication and shutdown without changing the monolith's handlers.
  Invalid or unsupported input delegates before execution; selected-command errors
  never retry through fallback.
- Next: prove native asynchronous send admission and same-primary ownership through
  that shared execution owner. The `/who` bridge does not yet provide Session
  delivery or lifecycle staging.
  Resolve same-primary send ownership, causal transcript publication, stage
  operations and shutdown integration separately. Registered commands and legacy
  sends remain unchanged.

Last validation: `go test ./...`, `go test -race ./...` and `make lint` passed
for the atomic participant subscription and lifecycle staging draft. This does
not validate a production staging adapter.

## Scope assessment

The goal, invariants, acceptance criteria and non-goals are clear. The principal
behavior change is loop sends waiting for frozen recipients through staging.
The command/module refactor should otherwise preserve behavior and the public
facade. #39 is closed; the current parser already supplies parsed statements,
source spans and diagnostics. #55's decision/effect boundary is the starting point.

Loop staging is deliberately underspecified. Settle the questions below before
implementing that behavior. Package names and exact interface shapes are design
choices to prove incrementally, not requirements to decide up front.

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

## Decisions needed before loop staging

Record an explicit policy and examples in the composition/workflow docs:

1. Does a pending loop delivery occupy the single visible stage, or retain work
   separately? How does the user see its owner and waiting reason?
2. Can user work be accepted while the loop waits? If user and loop deliveries
   compete, who proceeds first, and how is starvation avoided?
3. What happens when a loop needs its next delivery while a user stage exists?
   Define coexistence during participant work and condition evaluation too.
4. What does editing a loop-owned stage return and do to the loop? Does discard
   stop the loop or only cancel the pending iteration? What does interrupt target,
   and how do cancellation failure/retry and startup waits behave?
5. When does the initial queued loop submission report success, and how are later
   cancellation/failure outcomes presented without repeating submission events?
6. Which participant-instance and delivered-turn identities establish completion?
   An alias becoming idle is insufficient when user work can coexist. Specify
   cancellation behavior, ignore unrelated turns and same-alias replacements,
   and define departure during condition evaluation.

These are unresolved product decisions, not implied defaults. A useful design
review is a transition table covering both submission orders, each loop phase,
stage operations, departure (including during condition evaluation), shutdown
and partial delivery, especially primary delivery succeeding while an optional
notice fails. These decisions can wait until we approach loop staging; they
do not block earlier command migrations. Establish completion identity during
the loop migration, before adding staged delivery or new coexistence behavior.

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
- [ ] 2a. Addressed-send preparation and direct delivery:
  Standalone `std.SendCommand` retains Session's frozen `ParticipantSendPlan`,
  deriving `Staging.WaitFor` and `ToInform` from it for readiness. It submits the
  native session command with that same plan and drafts
  original input records and partial delivery through queued shared-feed
  session continuations with explicit primary matching, registered before
  asynchronous admission. Invocation cleanup releases subscriptions on completion, failure or discard. It is
  not registered; the real session adapter and lifecycle staging remain unwired.
  Start with the standalone `Invocation.Staging() *Staging` draft: frozen aliases
  or nil, with existing commands returning nil. The draft is not execution wiring.
  Every send should use one staging path, immediately releasing ready work or
  retaining it until ready. Preserve sticky departures and frozen recipients.
  Retain Session's send plan as the delivery authority, derive staging readiness
  from its aliases, and correlate delivery outcomes as the module needs them.
  Prove direct delivery and partial results with a narrow fake
  adapter. Preserve submission milestones and keep existing staged sends working
  through the bridge. Review this boundary before wiring staging.
- [ ] 2b. Addressed-send staged delivery: the unwired lifecycle proves
  direct/staged delivery against the same shared-feed fake adapter from 2a.
  Production wiring is still pending. The
  `Staging.DiscardInform(alias)` draft lets send filter its retained Session plan
  before launch; primary departures fail staging. Tracking and readiness are
  proved but remain unwired. Preserve user admission, startup readiness, atomic edit/discard/interrupt, cancellation
  retry, partial delivery and submission milestones. Review separately from 2a;
  do not combine both sub-checkpoints into one implementation pass.
- [ ] 3. Migrate broadcast: extend the proven delivery capability for frozen
  optional recipients, departures and partial results. Joins must not expand a
  retained plan. Preserve existing behavior and cover its complete command path.
- [ ] 4. Migrate handoff: add detached canonical source reads to the command
  context. Preserve required departures, source selection and active-source
  completion waits. Do not duplicate the transcript or create another observer.
- [ ] 5. Migrate user command definitions (`/shell` is already migrated):
  prove command resolution, including definitions
  and named invocation. Preserve asynchronous launch, result publication,
  cancellation and stale-result behavior. These become the capabilities loop uses.
- [ ] 6. Migrate loop with its current delivery behavior: inject delivery,
  participant facts, shell and command resolution; test with narrow fakes rather
  than a fake complete Session. Establish participant-instance and delivered-turn
  completion identity. Cover unrelated idle/turn events, cancellation, same-alias
  replacement and departure during condition evaluation. Document any necessary
  completion-correlation correction explicitly; do not introduce staged sends yet.
- [ ] 7. Design loop staging: settle the policy questions above and record the
  transition table in composition/workflow docs. Define admission, visible stage
  ownership, edit/discard/interrupt, coexistence, partial delivery and submission
  milestones before changing runtime behavior.
- [ ] 8. Add staged loop delivery: first prove one iteration, then repeated turns
  and user coexistence in separate diffs. Count only actual delivery; evaluate
  conditions only after completion of the identified turn on the same participant
  instance. Cover both submission orders, cancellation retry, startup, departures,
  stale outcomes and primary success with optional-notice failure. Nested sends
  must not duplicate input records or terminal submission outcomes. Update UX and
  workflow docs with the agreed behavior.
- [ ] 9. Migrate remaining handlers and remove the temporary bridge. Confirm new
  commands require neither model methods nor workflow-specific central branches.
  Extract cohesive packages one at a time only where migrated code demonstrates
  value. `runtime`, `std`, `stage`, `user` and `ast` remain candidates, not a
  mandatory directory checklist; do not move the parser just to match a name.
- [ ] 10. Remove superseded plumbing, reconcile architecture/workflow docs,
  check every #61 acceptance criterion, run final validation and remove this plan.

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
- Modules prepare invocations and return records through completion callbacks.
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
must remain green. Changed expectations require either agreed loop staging behavior
or an explicitly reviewed and documented completion-correlation fix from step 6.
Run external CLI integration tests separately if a backend integration boundary
changes. No tests are needed for this planning-only change.

Non-goals: runtime plugins, configurable runtime stacking, new syntax, a broad
runtime interface, or unrelated UI/session/backend redesign.
