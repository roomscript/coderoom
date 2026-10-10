# Temporary plan for #61

Issue: [Compose interpreter from command modules and staged delivery capabilities](https://github.com/roomscript/coderoom/issues/61).

This is a working checklist, not a new architecture contract. Implement one small
step at a time, inspect its diff and validation, then revise the next step from
what we learned. Do not implement the entire list in one pass. Leave commits to
the human. Move settled decisions into the design docs and remove this file when
the issue is complete.

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
- [ ] 1. Migrate `/who`: prototype `std.WhoCommand` and `runtime.CommandRunner`
  are exercised only through the legacy participant-result completion. Original
  registration, dispatch and participant-read instructions remain. The bridge
  returns the original notice as a canonical record, with exactly-once/order tests.
  Generic module wiring remains deferred. The callback prototype now replaces
  `Init`/`Next` with `Go(complete)`, using queued, correlated completions. Prove
  asynchronous `/shell` via its existing execution seam before settling capabilities
  and completing generic registration.
- [ ] 2a. Addressed-send preparation and direct delivery: introduce prepared plans,
  frozen required/optional recipients and correlated delivery outcomes as the
  module needs them. Prove direct delivery and partial results with a narrow fake
  adapter. Preserve submission milestones and keep existing staged sends working
  through the bridge. Review this boundary before shaping the staging interface.
- [ ] 2b. Addressed-send staged delivery: route the prepared plan through staging
  and prove direct/staged delivery against the same fake adapter from 2a. Preserve
  user admission, startup readiness, atomic edit/discard/interrupt, cancellation
  retry, partial delivery and submission milestones. Review separately from 2a;
  do not combine both sub-checkpoints into one implementation pass.
- [ ] 3. Migrate broadcast: extend the proven delivery capability for frozen
  optional recipients, departures and partial results. Joins must not expand a
  retained plan. Preserve existing behavior and cover its complete command path.
- [ ] 4. Migrate handoff: add detached canonical source reads to the command
  context. Preserve required departures, source selection and active-source
  completion waits. Do not duplicate the transcript or create another observer.
- [ ] 5. Migrate shell and command definitions, in separate diffs as appropriate:
  prove injected shell execution and command resolution, including definitions
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
- Modules return decisions/effects. Narrow context ports cannot independently
  execute Session or mutate/observe the canonical room. Acceptance and terminal
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
