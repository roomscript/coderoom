# Package design: internal/participant

## Scope

`internal/participant` defines the states associated with an agentic session: a named
collaborator with identity (`alias`, `role`, `initiative`, assigned color) plus
turn-lifecycle state (`status`, tracked streams, turn anchor).

It is not:

- a transport adapter to an external CLI
- the orchestrator that decides when to send work
- a UI model

Those responsibilities belong to `agent`, `session`, and `ui` respectively.

The package exists to centralize participant invariants so they are enforced in
one place instead of being reimplemented ad hoc by the session controller.

## Observable view and runtime entity

`View` contains the participant fields safe to expose to front ends: identity,
initiative, actual lifecycle status, color, status timestamp, `StartupReady`,
and `TurnID`. `Participant`
embeds that view and adds live agent capabilities and runtime bookkeeping.
Embedding keeps one canonical copy of every observable field while allowing
session rosters and interpreter snapshots to return `View` values without
exposing an `agent.Agent` or mutable stream tracking.

```go
type Participant struct {
    View
    Agent       agent.Agent
    OpenStreams map[agent.StreamID]struct{}
    // private runtime state
}
```

`Participant.Snapshot` remains a detached runtime copy for session-internal
operations. Public application snapshots use `View` instead.

Session exposes `Participant(alias) (View, bool)` and `Participants() []View`.
The list is captured under one session lock and includes all registered
participants, including starting and crashed participants. Both queries return
value copies without transport handles, stream maps, or mutation methods.
They do not filter recipients or rewrite lifecycle status.

`StartupReady` and `TurnID` live in the embedded `View`, with no duplicate
private runtime fields. Startup sets Idle before marking StartupReady; observers
can therefore distinguish the final startup window without inventing a status.
TurnID stays stable after a turn completes, until another turn starts.

View owns pure predicates for shared-room eligibility (`IsRoutable`), transport
status (`IsSendable`), full readiness for a new turn (`IsReadyForWork`), active
turns (`HasActiveTurn`), cancellation, and removal. Runtime send/cancel guards
add the required bound-agent check. Failed startup is removable even though it
never reached StartupReady; attached-runtime removal retains its startup guard.

This API cleanup preserves existing behavior. Shared-room selection continues
to include idle/working participants and exclude startup, preparing, keepalive,
and crashed participants. Transport status checks retain their existing rules;
IsReadyForWork also exposes the startup-completion requirement for workflow use.
Issue #56 separately changes staging behavior using this observable state.


## Identity and color allocation

`Participant.Color` is part of participant identity for the lifetime of a
session. An individual participant stores its assigned color but does not choose
one independently. Distinct deterministic assignment requires collection-level
state, so `participant.Registry` owns the participant color allocator.

```go
type Registry struct {
    participants map[string]*Participant
    colors       ColorAllocator
}
```

When a participant is added, the registry validates the addition and assigns
the next color before publishing the participant. `session.InviteCommand`
therefore carries only the alias and other non-visual invitation semantics; it
does not accept a UI- or interpreter-selected color.

Allocation is monotonic for one registry/session. Removing a participant does
not release its color because historical records retain participant identity;
reusing the same color for a later collaborator could visually conflate them.
The sequence is deterministic so equivalent invitation order produces
equivalent colors in the TUI, tests, and non-interactive clients.

An addition rejected during validation does not consume a color. Once the
registry accepts and publishes the participant, the color remains consumed even
if asynchronous agent startup later fails, matching the participant's presence
in lifecycle events and historical room records.

The allocator implementation and its perceptual color-generation tests move
from `internal/ui/palette` into `internal/participant`. UI-only color tokens,
such as departed-record and diff colors, remain in the UI palette package.
See [`pkg-participant-colors.md`](pkg-participant-colors.md) for the generation
algorithm, readability requirements, terminal behavior, and verification.

---

## State model

Current participant statuses:

- `idle`: no turn is in flight
- `starting`: the agent process is starting and cannot receive work yet
- `attached`: the agent process is live, but startup has not fully committed
- `keepalive`: backend maintenance is in flight; the participant is temporarily
  non-sendable but no user turn is active
- `preparing`: the session has committed to a new turn, but the participant has
  not entered `working` yet
- `working`: a turn is in flight and turn-scoped stream fragments may arrive
- `crashed`: the agent process exited unexpectedly

The normal lifecycle is:

```text
starting -> attached -> idle
idle -> preparing -> working -> idle
idle -> keepalive -> idle
* -> crashed
```

`preparing` exists to close the race between "the session decided to send" and
"the participant is visibly in-flight". Once a participant is in `preparing`,
other callers can no longer observe it as available for another direct send.

`keepalive` exists to reserve the request lane for backend maintenance without
inventing a user-visible conversation turn. While a participant is in
`keepalive`, direct sends, shared-room dispatch, and handoff delivery treat it
as busy.

---

## Invariants

The participant package enforces these runtime rules:

- a participant cannot start a new turn while already `preparing` or `working`
- a participant cannot start a new turn while `keepalive` is in flight
- a participant cannot receive work while `starting`, `attached`, or `crashed`
- a participant cannot become `idle` while its turn anchor is still open
- a stream flush for an untracked stream is invalid
- turn-scoped streams may only be tracked while the participant is
  `preparing` or `working`

This is why the participant is not just a bag of fields. It owns the legality
of state transitions; the session owns when to attempt them.

---

## Transition API

The intended lifecycle is:

1. `New`
2. `BeginStartup`
3. `AttachAgent`
4. `CommitIdle`
5. `PrepareForWork`
6. `BeginWorking`
7. `TrackStream` / `CloseStream`
8. `BecomeIdle`

Exceptional paths:

- `AbortWork` rolls back a `preparing` or `working` participant to `idle`
- `BeginKeepalive` / `FinishKeepalive` bracket a maintenance-only
  `idle -> keepalive -> idle` transition
- `Crash` clears turn state and moves the participant to `crashed`

### Why `PrepareForWork` exists

`PrepareForWork` is the race-closure step.

Without it, the caller would need to:

1. observe `idle`
2. call the agent
3. later mark the participant `working`

That leaves a window where another caller can still observe the participant as
idle even though work has already been committed. `PrepareForWork` closes that
window synchronously before the transport call happens.

### Why `TrackStream` accepts `preparing`

The participant allows stream tracking in both `preparing` and `working`.

This is deliberate. A backend may emit stream activity very quickly relative to
the point where the session finishes the state transition into `working`.
Accepting `TrackStream` during `preparing` means those early fragments are still
legal and do not force the session to choose between a race and an invariant
violation.

### Why `BecomeIdle` is stricter than "no visible streams left"

`BecomeIdle` is gated by the participant's turn anchor, not just by whether the
currently observed auxiliary streams have all flushed.

Auxiliary streams such as output, reasoning, command execution, and file-change
messages may open and close in phases during one turn. The participant only
becomes idle when the caller has already closed the anchor stream and then asks
for the final `working -> idle` transition.

That separation is intentional:

- `CloseStream` answers whether the close should end the turn
- `BecomeIdle` validates that ending the turn is now legal

---

## Anchor ownership

The participant stores a single `anchor` field plus the broader
`OpenStreams` set. It also retains the session-global turn ID assigned when
`BeginWorking` succeeds. The ID remains available after the participant
returns to idle. Coordinators use it to reject delayed completion signals from
an older turn or removed participant incarnation; it is lifecycle identity,
not presentation state.

- `anchor` means "this stream authoritatively defines turn lifetime"
- `OpenStreams` means "these turn-scoped streams are currently open"

The participant package does not define where anchors come from. That is an
agent-level concern. It only enforces the rule that a participant must not
become idle while its anchor remains open.

---

## Design boundary

`internal/participant` owns:

- deterministic, session-scoped participant color allocation
- retaining assigned color as participant identity
- participant statuses
- legal state transitions
- tracked-open-stream bookkeeping
- anchor-gated idle validation

It does not own:

- deciding which message to send
- choosing direct send vs notice send
- constructing stream IDs
- translating backend protocol into messages

For those concerns see:

- [`pkg-session.md`](pkg-session.md)
- [`pkg-agent-codex.md`](pkg-agent-codex.md)
- [`pkg-agent-messages.md`](pkg-agent-messages.md)
