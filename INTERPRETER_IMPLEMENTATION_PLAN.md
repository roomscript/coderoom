# Temporary interpreter implementation plan

This file tracks the incremental implementation of GitHub issue #38. It is
temporary and must be deleted in the final boundary-enforcement commit.

Implementation was parked after Step 6 while issue #53 established the model
and executor boundaries. That decomposition is complete and Step 7 is active.

Each step should leave the repository working and independently reviewable.
Run `go test ./...` before completing every step unless a narrower command is
explicitly listed in addition.

## 2. Move participant color allocation

- [x] Move participant color generation from `internal/ui/palette` to
      `internal/participant`.
- [x] Make `participant.Registry` own monotonic color allocation.
- [x] Assign color after registry validation succeeds.
- [x] Do not reuse colors after participant removal or asynchronous startup
      failure.
- [x] Remove `Color` from `session.InviteCommand`.
- [x] Remove participant palette state from the UI.
- [x] Keep departed-record, diff, and other rendering tokens in
      `internal/ui/palette`.
- [x] Move and update determinism, uniqueness, contrast, rejection, and removal
      tests.

Verification:

```text
go test ./internal/participant ./internal/session ./internal/ui/...
go test ./...
```

## 3. Make handoff input value-based

- [x] Replace `HandoffCommand.ResolveSource` with a resolved
      `session.HandoffSource` value.
- [x] Keep source selection in the current canonical room owner temporarily.
- [x] Preserve source record index and handoff audit metadata.
- [x] Update session, room, and UI tests.

Verification:

```text
go test ./internal/session ./internal/room ./internal/ui/...
go test ./...
```

## 4. Add the interpreter foundation

- [x] Create `internal/interpreter` without importing UI or terminal packages.
- [x] Define the narrow `SessionController` dependency.
- [x] Add the serialized operation/event loop.
- [x] Make the session observer enqueue and return without waiting.
- [x] Add interpreter observers, structured events, and immutable snapshots.
- [x] Add interpreter-owned approval DTOs and translation.
- [x] Add lifecycle and shutdown handling.
- [x] Add a recording session fake.
- [x] Test synchronous session callbacks and serialized `Execute` calls.
- [x] Leave the TUI on its existing execution path for now.

Verification:

```text
go test -race ./internal/interpreter
go test ./...
```

## 5. Move basic statement execution

### 5a. Centralize legacy session execution

- [x] Add temporary `SubmitWithFallback(raw, session.Command)` migration API.
- [x] Embed the interpreter in the TUI and deliver its observer events through
      a blocking `tea.Cmd` queue listener.
- [x] Add temporary synchronous
      `ExecuteLegacy(command session.Command) error`.
- [x] Enqueue `ExecuteLegacy` on the interpreter loop with a buffered one-shot
      result channel; return the original execution error to the caller.
- [x] Complete the interpreter's projection of synchronous causal session
      events before resolving the result. TUI observers continue consuming
      their independently queued events through Bubble Tea.
- [x] Return `ErrClosed` when shutdown has begun, and guarantee that shutdown
      resolves or rejects every accepted synchronous request.
- [x] Prohibit calling `ExecuteLegacy` from the interpreter loop or a
      synchronous session observer callback.
- [x] Add contract tests for serialization with submissions, unchanged error
      propagation, causal-event ordering, shutdown, and concurrent callers.
- [x] Replace every direct TUI `session.Execute` call—including approval,
      loops, immediate sends, and staged dispatch—with `ExecuteLegacy` while
      leaving parsing, planning, and rendering behavior unchanged.
- [x] Add a boundary test proving `internal/ui` contains no direct
      `session.Execute` call before command-by-command migration starts.
- [x] Keep mutable planning in its current TUI workflow during this checkpoint;
      `ExecuteLegacy` centralizes execution but is not a permanent ownership
      boundary.

### 5b. Establish temporary submission routing

- [x] Replace the current submit-everything/`UnknownCommand` fallback with
      explicit temporary branching in the existing TUI submission function:
      - legacy prompt commands continue through the current TUI parser and
        invoke `ExecuteLegacy` when they produce a session command;
      - legacy control/query and other non-session commands continue through
        the existing TUI handler;
      - native interpreter commands use `Submit` (initially none).
- [x] Keep `SubmitWithFallback` limited to data-only `session.Command` values;
      do not pass callbacks, UI state, or presentation behavior into the
      interpreter.
- [x] Introduce `SubmitWithFallback` at the submission boundary only after all
      direct session execution has been centralized through `ExecuteLegacy`.
- [x] Implement the documented mutually exclusive terminal outcomes:
      `InputRejected`, `UnknownCommand`, `SubmissionSucceeded`, or
      `SubmissionFailed`. Do not add a second completion event after rejection
      or unknown routing.
- [x] While a submission is unresolved, prevent the sequential TUI from
      starting another interpreter or legacy command. Release the gate on its
      single terminal outcome.
- [x] Test `/invite ada` followed immediately by `/who`: after the invite
      command returns and its synchronous events drain, `/who` observes `ada`
      in `Starting` state. Submission completion does not wait for the
      asynchronous `AgentStarted` event.
- [x] Test failure and shutdown paths release or reject the temporary gate
      without losing the composer's current draft.
- [x] Prove native handlers take precedence, fallbacks execute exactly once,
      and fallback causal events drain before the next submission.
- [x] Do not use precomputed fallbacks for workflows that depend on mutable
      session or room state; keep them on synchronous `ExecuteLegacy` and
      migrate those workflows as units.
- [x] Add the submission contract suite in dedicated
      `submit_contract_test.go` before migrating handlers.
- [x] Prove `Submit` only enqueues from the caller and all execution occurs on
      the interpreter-loop goroutine.
- [x] Prove sequential ordering and concurrent `session.Execute`
      serialization with multiple valid commands that all reach the fake.
- [x] Drain synchronous session events caused by one operation before planning
      or executing the next external submission.
- [x] Coalesce session-event wakeups so event bursts enqueue at most one drain
      marker.
- [x] Cover invalid arguments, undefined commands, execution failure,
      synchronous callbacks, and submission after shutdown.
- [x] Reject `Submit` and `SubmitWithFallback` with `ErrStagePending` while a
      stage exists, without parsing, room mutation, fallback, or session
      execution.
- [x] Move prompt parsing and fallback/unknown dispatch into the interpreter.

### 5c. Migrate control and query commands

- [x] Move `/who` semantics into the interpreter and route it through
      `Submit`.
- [x] Move `/help` command metadata into the interpreter while leaving visual
      formatting in the TUI, then route it through `Submit`.
- [x] Make the interpreter recognize `/quit` and emit an exit-request event;
      the TUI remains responsible for returning `tea.Quit`.
- [x] Route approval decisions through `Interpreter.ResolveApproval`, remove
      the TUI's `ResolveApprovalCommand` construction, and delete that
      `ExecuteLegacy` call.
- [x] Decide explicitly whether debug display commands remain UI-only or
      become interpreter commands; they must not expose UI behavior through a
      fallback callback.
- [x] Remove each migrated control/query command from the legacy TUI handler.

### 5d. Migrate session commands

- [x] Route eligible legacy data-only session commands (`/invite`, `/remove`,
      `/cancel`, and policy) through `SubmitWithFallback` so the interpreter
      loop remains the sole caller of `session.Execute`.
- [x] After the temporary routing model and control/query commands are stable,
      migrate `/invite` from `SubmitWithFallback` to native `Submit` handling.
- [x] Migrate `/remove` from `SubmitWithFallback` to native `Submit` handling
      and delete its fallback translation.
- [x] Migrate `/cancel` from `SubmitWithFallback` to native `Submit` handling
      and delete its fallback translation.
- [x] Migrate policy commands from `SubmitWithFallback` to native `Submit`
      handling and delete their fallback translations.
- [x] Move the room-scoped command registry.
- [x] Move shell execution, definitions, invocation, and cancellation.
- [x] Add a fake shell runner for interpreter tests.
- [x] Preserve existing syntax, routing, output, and error behavior.
- [x] Remove each TUI translator as its native interpreter handler lands.

Verification:

```text
go test ./internal/interpreter ./internal/promptlang ./internal/shell
go test ./...
```

## 6. Move bounded loops

- [x] Move active loop state and transitions into the interpreter.
- [x] Advance loops from queued participant lifecycle events.
- [x] Feed shell completion back through the interpreter loop.
- [x] Preserve condition evidence and user-visible status behavior.
- [x] Preserve cancellation, maximum-turn, stop, and crash behavior.
- [x] Move equivalent loop tests from `internal/ui`.

Verification:

```text
go test -race ./internal/interpreter
go test ./...
```

## 7. Move staged submissions

### 7a. Establish interpreter-owned staged state

- [x] Move frozen routing plans, barrier aliases, and staged state into the
      interpreter.
- [x] Route send, broadcast, and handoff into the stage workflow and move
      their mutable routing and barrier planning inputs with them; dispatch
      translation lands with each action's migration checkpoint.
- [x] Add a `stageWorkflow` to the interpreter workflow collection without
      adding stage-specific orchestration to the facade or executor.
- [x] Publish immutable staged state for presentation while keeping composer
      and terminal rendering state in the TUI.
- [x] Preserve the single-stage submission gate and prove that a rejected
      submission cannot parse, mutate the room, or execute a fallback.

Stop condition: the interpreter owns the frozen state for its native submission
path and can either dispatch an immediately ready submission or publish a
pending stage. Production TUI authority moves atomically in 7e after the full
workflow and stage-operation surface exists.

### 7b. Move lifecycle-driven dispatch

- [x] Preserve immediate and lifecycle-delayed dispatch.
- [x] Advance pending stages from queued participant lifecycle events.
- [x] Preserve target departure and partial-delivery behavior.
- [x] Freeze routing and barrier membership at submission time so later joins
      cannot alter a pending stage.

Stop condition: send and broadcast stages dispatch or terminate entirely from
interpreter-owned state and queued events, with equivalent interpreter tests
covering the migrated UI scenarios.

### 7c. Preserve handoff ordering

- [x] Resolve handoff sources from the interpreter-owned canonical room only
      immediately before dispatch.
- [x] Preserve handoff output/idle ordering and latest-source-turn readiness.
- [x] Preserve source or target departure, unrelated participant events, and
      late-joiner behavior.

Stop condition: handoff staging no longer depends on TUI-projected turn state
or room callbacks, and its audit source matches the canonical room snapshot.

### 7d. Add atomic stage operations

- [x] Implement `TakeStageForEdit() (string, bool)`.
- [x] Implement `DiscardStage() bool`.
- [x] Implement `InterruptAndDispatchStage() bool`.
- [x] Process all stage operations atomically on the interpreter loop.
- [x] Reject operations cleanly after shutdown.
- [x] Ensure accepted synchronous requests resolve or observe interpreter
      completion.
- [x] Add auto-dispatch/edit/discard races, duplicate interrupt, and shutdown
      tests.

Stop condition: edit, discard, and interrupt requests act on whichever stage
exists when their serialized operation runs, and every accepted request has a
defined shutdown outcome.

### 7e. Remove TUI stage ownership

Complete this cutover as a sequence of small changes. Each checkpoint must be
reviewable, pass the full test suite, and leave the application in a working
state before the next checkpoint begins. Do not combine transcript ownership,
room projection, or presentation-record migration with this phase.

#### 7e.1. Add the TUI stage-operation adapter

- [x] Invoke `TakeStageForEdit`, `DiscardStage`, and
      `InterruptAndDispatchStage` from `tea.Cmd` and return typed Bubble Tea
      result messages.
- [x] Do not connect the existing staged-composer key handlers to the new
      commands yet; the UI-owned workflow remains authoritative and the new
      adapter is exercised directly in boundary tests only.
- [x] Add ordering tests for operation result versus interpreter snapshot,
      including typing immediately after taking a stage for edit.

Stop condition: all three interpreter operations can be driven safely through
Bubble Tea without changing production stage ownership or behavior.

#### 7e.2. Present interpreter stage state

- [x] Add a narrow presenter that maps `Snapshot.Stage` to staged composer text
      and status only.
- [x] Preserve approval overlays when stage snapshots arrive or clear.
- [x] Do not replace, merge, reconcile, or otherwise migrate transcript state;
      the existing UI room projection remains the transcript authority for
      this phase.
- [x] Add focused tests for stage appearance, status refresh, clearing, draft
      restoration, approval overlap, and event-order permutations.

Stop condition: the TUI can render interpreter-owned stage state without
changing transcript records or calculating workflow readiness.

#### 7e.3. Route one staged action through the interpreter

- [x] Cut over `Send` first while retaining the legacy broadcast and handoff
      paths.
- [x] Preserve the existing UI transcript append at the dispatch boundary;
      canonical transcript migration is explicitly out of scope.
- [x] Verify immediate dispatch, delayed dispatch, frozen listeners, target
      departure, zero delivery, partial delivery, edit, discard, and interrupt.
- [x] Keep the existing end-to-end UI tests until the cut-over path has direct
      replacement coverage.

Stop condition: `Send` has exactly one stage authority in production and its
legacy UI path can be removed independently.

#### 7e.4. Route the remaining staged actions

- [ ] Cut over `Broadcast` as its own reviewed change and verify immediate,
      delayed, partial-delivery, departure, edit, discard, and interrupt paths.
- [ ] Cut over `Handoff` as a separate reviewed change and verify canonical
      source selection, source-turn ordering, departure, late joiners, edit,
      discard, and interrupt paths.
- [ ] Do not remove shared legacy helpers until both actions no longer use
      them.

Stop condition: send, broadcast, and handoff each use interpreter-owned stage
planning, transitions, and dispatch in the running application.

#### 7e.5. Remove dead TUI stage ownership

- [ ] Remove UI-owned frozen plans, barrier coordination, projected handoff
      readiness, and staged dispatch through `ExecuteLegacy`.
- [ ] Remove obsolete helpers and tests only after mapping each deleted
      scenario to retained interpreter or UI-boundary coverage.
- [ ] Update the architecture documentation to describe the final ownership
      boundary without claiming broader transcript migration.

Stop condition: the TUI is a stage presenter and input adapter only; all stage
workflow decisions are interpreter-owned, and transcript ownership is
unchanged.

Working agreement for 7e:

- One numbered checkpoint per change; 7e.4 uses separate changes for
  broadcast and handoff.
- No opportunistic architecture migration or unrelated cleanup.
- Do not delete broad legacy coverage in the same change that introduces a new
  production path.
- If a checkpoint requires dual transcript reconciliation or another new
  subsystem, stop and revise the plan before implementing it.

Verification:

```text
go test -race ./internal/interpreter
go test ./...
```

## 8. Cut the TUI over

- [ ] After all built-in commands are interpreter-owned, make their canonical
      definitions drive native dispatch and help metadata. Replace duplicated
      help catalogs and exhaustive usage lists with parameterized coverage,
      while retaining an independent invariant that every built-in recognized
      by `promptlang` has a registered command definition.
- [ ] Replace remaining `SubmitWithFallback` calls with `Submit`.
- [ ] Remove `SubmitWithFallback` after the final legacy translator is gone.
- [ ] Remove `ExecuteLegacy` after the final TUI workflow moves into the
      interpreter.
- [ ] Complete rendering of interpreter events and snapshots.
- [ ] Remove remaining UI-owned registry, shell execution, and loop state.
- [ ] Remove direct UI session observation, commands, and snapshot queries.
- [ ] Remove UI imports of `internal/session` and `internal/agent`.
- [ ] Remove obsolete UI tests and helpers only after equivalent interpreter
      coverage exists.

Verification:

```text
go test -race ./internal/interpreter ./internal/ui/... ./internal/session
go test ./...
```

## 9. Enforce the boundary and clean up

- [ ] Add a normal architecture test using `go list`.
- [ ] Reject transitive interpreter dependencies on `internal/ui`, Bubble Tea,
      Bubbles, and Lip Gloss.
- [ ] Reject direct UI imports of `internal/session` and `internal/agent`.
- [ ] Remove transitional adapters, duplicate execution paths, and obsolete
      APIs.
- [ ] Update implementation-status wording in the design documents.
- [ ] Delete this temporary plan file in this commit.

Verification:

```text
go test -race ./internal/interpreter ./internal/ui/... ./internal/session
go test ./...
git diff --check
```
