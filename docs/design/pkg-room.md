# Package design: internal/room

## Scope

The room package owns the chat/room state.

It sits between:

- `internal/session`, which publishes runtime events and owns agent/process
  coordination
- `internal/interpreter`, which owns the live room and exposes snapshots
- `internal/ui/room`, which adapts those snapshots into Bubble Tea presentation state

The room package is **not** responsible for:

- agent lifecycle
- command execution
- participant state transitions
- policy enforcement

It is responsible for:

- defining rooms
- defining records
- defining room membership
- projecting `session.Event` into chat-visible room state
- maintaining streaming and completed records over time
- producing immutable snapshots for interpreter publication

---

## Why this package exists

Before the interpreter cutover, the UI assembled semantic chat state directly
from `session.Event`. The interpreter now owns that projection.

That is too much responsibility in the rendering layer. The product needs a
stable room/record model that can later support:

- handoff
- copy/reuse actions on prior outputs
- richer room types (shared room, private rooms, etc.)
- expansion/collapse of large records

So the new split is:

- session owns runtime facts
- room owns in-memory room state
- UI owns presentation

---

## Core model

The room package should define two main concepts:

### Room

A room is the in-memory state for one visible collaboration space.

Version 1 likely needs only:

- shared room

Future versions may add:

- private agent rooms
- approval/system-focused rooms

Room is the general data model, not the UI state container.

That means:

- `room.Room` owns participant membership for that room
- `room.Room` owns record membership, ordering, identity, and completion state
- `room.Room` owns the underlying record content and metadata
- `room.Room` does **not** own presentation-only state such as:
  - collapsed vs expanded
  - viewport scroll position
  - selection/focus
  - transient styling state

That presentation state belongs in `internal/ui`.
For the room pane specifically, the adapter boundary is `internal/ui/room`.

That boundary is behavioral, not a ban on importing the package. Lower-level
UI packages such as `internal/ui/room/history` (and its `record`
sub-package) may import `internal/room` to consume the canonical
`Record`/`Kind` types directly — that's the point of keeping `room.Record`
canonical instead of redefining it (see "Record" below); a type alias gives
zero-cost reuse with no parallel type to keep in sync. What they must not
depend on is `internal/room`'s behavioral surface — `Room`, `Observer`,
`OnEvent`, `Snapshot`, or anything else that talks to a live room instance.
Only `internal/interpreter` holds a `*room.Room` and drives it. UI packages see
snapshots and plain `Record`/`Kind` values handed to them.

Participant membership in a room may be:

- all participants in the session, or
- a subset of session participants

The session remains the source of truth for participant runtime state and
transitions. Room maintains only the membership list for a particular room —
which aliases belong to it — not a mirrored snapshot of their status, role,
or approval state.

For V1, membership comes from the same agent-lifecycle events Room already
consumes for system records: `AgentReady` adds an alias,
`AgentStopped`/`AgentCrashed` remove it. No new event is needed for
the shared room, where membership is simply every participant that has
joined and not since departed. `AgentStarting` still produces its own
"starting" system record but does not add membership — an alias is a member
once it has actually joined, not while it's still coming up.

Mutating membership for a future subset/private room — adding or removing
one alias from one room without affecting others — has no documented
mechanism yet. That's out of scope for V1 and needs its own design once
private rooms are introduced; this doc should not be read as already having
solved it.

Room does not project participant or approval state. The interpreter reads
participant snapshots from session and translates approval events into its
application-facing state. Neither needs Room's involvement. Routing them
through Room would duplicate state and require runtime fields unrelated to
chat projection. See "UI integration" for how the UI receives them.

### Record

A record is one chat-visible unit within a room.

Examples:

- a user broadcast
- a direct send to an agent
- the routed recipient set for that send
- an agent's completed visible reply
- a system/lifecycle notice
- a handoff

The key point is:

- records are semantic chat units
- records are not raw transport fragments
- records are not UI view models

`room.Record` should contain the canonical content and metadata for the chat
unit. The UI may wrap a `room.Record` with additional per-view state, but that
state should not live in `room.Record` itself.

`room.Record` is the canonical record model for the product. UI rendering
packages should consume it; room should not reuse a UI-owned record type as its
source of truth.

---

## Input / output boundary

### Input

The room package consumes `session.Event`.

`session.Event` remains the canonical runtime event model. Room does not define
its own peer event stream for the same facts.

In practice, the room package exposes a projection type that consumes session
events. The interpreter owns that projection and controls the order in which
events update room state, advance workflows, and reach front ends. The UI does
not register as a session observer.

V1 should prefer the simpler shape:

```go
i := interpreter.New(ctx, sess, cwd)
```

That keeps the dependency one-way:

- UI submits intent to `interpreter`
- interpreter calls `session` for effectful actions
- `session` publishes `session.Event`
- interpreter applies those events to its `room.Room`
- interpreter emits state changes
- UI renders the supplied transcript deltas and application snapshots

The important boundary is:

- session emits runtime facts
- room consumes runtime facts and updates in-memory room state
- room, not UI, owns the async release/buffering off the session observer path
- UI does not re-derive chat semantics from session events

### Output

The room package exposes room state and record updates to consumers such as the
UI. An update may carry a narrow trigger identifying an applied room input, so
an application coordinator can advance dependent workflows only after the
required room state is observable. For example, an agent-turn-completed trigger
lets handoff coordination wait until the authoritative turn-ending flush has
been applied and the source output has been sealed. The trigger includes the
session-global turn ID, preventing a delayed completion from satisfying a
workflow waiting on a newer turn or a re-invited alias. Coordinators retain the
latest projected turn independently of temporary runtime status, so keepalive
and failed send preparation do not erase valid projection progress. Most
updates, including locally appended records, carry no trigger.

The UI should render room state, not derive chat semantics from `session.Event`
directly.

The UI integration point is the room Bubble Tea presenter:
`internal/ui/room`. Canonical records and stream metadata arrive through ordered
interpreter `TranscriptChanged` deltas. Application snapshots supply participants,
approvals, and stage state, but their room records are not reapplied to the live
transcript. The presenter has no live `room.Room`, observer queue, or session observer API.
Room component tests also construct this presentation-only model.

User-authored input, shell results, definitions, and loop records are appended
by the interpreter through room-owned APIs. Help formatting, startup tips,
debug output, and event-formatted notices remain local presentation records.
The presenter translates canonical indices around those notices without
observing the session or reconciling another projection. See
[`pkg-interpreter.md`](pkg-interpreter.md#room-ownership-and-transcript-delivery)
for the delivery contract and coverage mapping.

---

## Relationship to session events

`session.Event` and room records are not the same thing.

### `session.Event`

- runtime fact
- coordination-oriented
- may be low-level / streaming
- owned by session

### `room.Record`

- chat-visible unit
- projection-oriented
- may accumulate multiple runtime events into one stable message
- owned by room

Example:

- several `AgentMessage` streaming events may contribute to one completed
  agent-output record

That accumulation belongs in room, not in UI.

User-authored records are different: they are created intentionally by the
interpreter and inserted into room directly. They are not reconstructed from
`session.Event`.

---

## Streaming behavior

Room must support in-progress records.

For agent output, a likely model is:

- first streaming output event opens an in-progress record
- subsequent output events update that same record
- flush/finalization closes the record

This preserves the current streaming UX while removing accumulation logic from
the UI.

The room package therefore owns:

- record identity
- open vs completed state
- accumulation of visible output text for one logical record
- message state required to represent in-progress and completed chat-visible
  records correctly over time

The interpreter records actual outcomes on canonical user-input records:
`Routing` contains accepted deliveries, `FailedRouting` contains failed recipients,
and `UnsentRouting` contains unattempted recipients. A record is committed when
at least one delivery is accepted, including partial success. Total failure
remains an execution failure without a committed user-input record. The UI
formats these detached fields without calculating recipients or reconstructing
routing from session events.

---

## Record metadata

Records should carry enough metadata for future actions without forcing the UI
to scrape rendered text.

Likely metadata includes:

- room identity
- record identity
- record kind
- alias / author
- source turn ID or stream identity when relevant
- full text payload
- optional neutral preview / summary text when the product needs a canonical
  short form independent of UI rendering
- completion state

This matters for future commands such as handoff, copy, inspect, or summarize.

The canonical record content should live in `room.Record`. Any UI-specific
derived state such as "collapsed" or "routing footer shown to the user" should
be maintained separately by the UI and keyed by room ID + record ID when
needed.

---

## Handoff implications

The room package is the natural place to expose the latest completed chat-visible
agent output as a reusable record.

That means `/handoff` should resolve from room-owned completed records rather
than:

- querying the UI viewport/history directly
- reassembling low-level `session.Event` fragments ad hoc in the command path

This is one of the main reasons to introduce room as a package-level concept.

---

## Session integration

Session publishes runtime events.

Room subscribes to those events and updates its model.

The interpreter is the session observer exposed to the application layer. Its
observer callback quickly enqueues each event. On the interpreter execution
loop, it applies that event synchronously to `room.Room`, advances any workflow
that depends on the event, and only then publishes a new snapshot.

Room therefore does not own a second asynchronous event path between session
and interpreter. Session events and local application records are applied on
the same serialized loop, preserving their accepted order. Room may retain an
`OnEvent(session.Event)` projection method, but the interpreter calls it; Room
is not independently registered with session.

Session remains the source of truth for runtime coordination state. Room keeps a
projected in-memory model for one room:

- room membership
- record state

This gives the system two distinct observation boundaries:

- runtime observer: session -> interpreter
- application observer: interpreter -> UI

Session should not need to know how many records a particular event becomes, or
how the UI chooses to render them.

This keeps the session focused on orchestration.

### Concurrency

Room mutation occurs on the interpreter execution loop. Snapshot reads return
copied values and may be served safely to observers. The interpreter owns the
queue that releases session emitters quickly and the observer delivery that
keeps front-end work off its execution loop; Room does not push Bubble Tea work
or maintain its own scheduling policy.

---

## UI integration

For chat/record rendering, the UI should depend on room state rather than on
raw `session.Event`.

That means:

- UI no longer owns record assembly
- `internal/ui/room` renders canonical `room.Record` values from ordered
  interpreter transcript deltas; snapshot records are for inspection
- UI renders participant and approval state from interpreter events and
  snapshots
- UI may maintain view-local state for a room record, such as collapsed/expanded
- UI-specific concerns remain in UI:
  - viewport
  - styling
  - focus
  - collapsed/expanded interaction

The room package should stay presentation-agnostic.

Within the UI stack, `internal/ui/room/history` is presentation-only: it
renders records and viewport state supplied by `internal/ui/room` and never
holds or drives a `*room.Room` itself. It may still import `internal/room`
for the `Record`/`Kind` types it renders — see "Core model" for the
type-vs-behavior distinction this relies on.

---

## Routing and rooms

The old `router` abstraction is the wrong level for this system.

Routing decisions belong partly to:

- session command execution and policy
- room projection semantics

They do not justify a separate package whose only purpose is “route messages
across channels.”

The room package provides the visible destination structure; session decides
what to send and to whom.

---

## Design boundary

Session owns:

- commands
- policies
- agent lifecycle
- runtime events

Room owns:

- rooms
- room membership
- records
- projection of runtime events into chat-visible state
- room-local insertion of user-authored records
- immutable snapshots consumed by the interpreter

UI owns:

- rendering
- interaction
- viewport/focus/selection
- per-record view state such as collapsed/expanded
- rendering participant and approval state supplied by the interpreter

---

## Initial API direction

The first implementation should make the three concepts explicit:

- `Room`: canonical in-memory room model
- `Record`: canonical chat-visible unit
- `Snapshot`: copied room state published by the interpreter

A plausible V1 shape:

- `type Room struct { ... }`
- `type Record struct { ... }`
- `func (r *Room) OnEvent(e session.Event)`
- `func (r *Room) AppendRecord(rec Record)`
- `func (r *Room) Snapshot() Snapshot`

The exact names may change, but the architectural constraint should hold:

- room owns projection and canonical in-memory room state for chat/records
- interpreter owns Room and applies session events to it in serialized order
- UI owns display state built on interpreter-supplied room snapshots

---

## Open questions

These are known gaps in this design, deliberately deferred rather than
blocking V1:

---

## Summary

`internal/room` should become the canonical chat data model of the product.

- `session.Event` stays canonical for runtime facts
- room projects those facts into rooms and records
- UI renders rooms and records

This removes semantic chat assembly from the UI and gives future features such
as handoff a stable non-UI source of truth.
