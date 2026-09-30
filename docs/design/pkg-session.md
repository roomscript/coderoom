# Package design: internal/session

## Scope

The Session Controller is the central orchestrator of a coderoom session. It receives structured commands, dispatches to agents, and forwards agent output to the appropriate channel.

It is the layer that owns goroutines. The agent package is synchronous; the session controller spawns one reader goroutine per agent to stream output without blocking.

It is **not** responsible for parsing raw user input, coordinating language
workflows, selecting room-visible handoff sources, or rendering output. Those
belong to the interpreter, prompt-language, room, and TUI layers respectively.

State ownership model:

- `agent` is a synchronous transport adapter to the external CLI
- `participant` is the stateful runtime entity
- `session` is the sole mutator/coordinator of participant state
- `interpreter` is the application facade that dispatches session commands
- `ui` consumes interpreter state and does not interact with session directly

Participant invariants and state-machine rules live in
[`pkg-participant.md`](pkg-participant.md). The session does not duplicate those
rules; it coordinates when to invoke them.

---

## Input model

The session controller exposes a single entry point:

```go
func (s *Session) Execute(cmd Command) error
```

`Command` is a sealed interface — only types within the `session` package can implement it. Dispatch is via an unexported method; no type switch required:

```go
type Command interface {
    execute(s *Session) error
}
```

The prompt-language package parses raw user input. The interpreter translates
the resulting statement into one of the concrete session command types before
calling `Execute`:

```go
// InviteCommand adds an agent to the session and starts it.
type InviteCommand struct {
    Alias string
}

// CancelCommand interrupts in-flight work for an agent but keeps it in the
// registry/room (the agent remains "joined").
//
// Semantics:
// - Best-effort: not all agent backends support true cancellation.
// - Does not remove history/records; it only affects ongoing execution.
type CancelCommand struct {
    Alias string
}

// RemoveCommand stops and removes an agent from the session (hard stop).
type RemoveCommand struct {
    Alias string
}

// BroadcastCommand sends a message to the shared room and to its recipients.
// Aliases freezes staged routing. A nil value preserves the legacy behavior of
// selecting all routable participants at execution time.
type BroadcastCommand struct {
    Aliases []string
    Text    string
}

// SharedSendCommand sends a message to one agent in the shared room.
// Plan freezes the policy-aware routing decision. The caller supplies both
// texts — the session controller does not format messages. A shared room event
// is emitted so the TUI displays it to everyone.
type SharedSendCommand struct {
    Plan          SharedSendPlan
    TextDirect    string
    TextListeners string
}

// SharedSendPlan is an opaque, immutable routing decision created by Session.
type SharedSendPlan struct {
    // session ownership, addressed alias, and listener aliases are private
}

func (s *Session) PlanSharedSend(alias string) SharedSendPlan
func (p SharedSendPlan) Targets() []string

type EnablePolicyCommand struct {
    Name policy.Name
}

// HandoffCommand delivers a source already selected from canonical room state.
// Session validates participants and delivery barriers but does not query room
// or call back into the application layer to select content.
type HandoffCommand struct {
    FromAlias   string
    ToAlias     string
    IdleAliases []string
    Source      HandoffSource
}

// PrivateSendCommand sends a message directly to one agent's private channel.
// Nothing is emitted to the shared room and no other agents are notified.
// Used for approval flows and reasoning that should not pollute the shared room.
type PrivateSendCommand struct {
    Alias string
    Text  string
}
```

Each command type carries only the fields it needs. Adding a new command requires implementing `execute` — the compiler enforces it.

Invite-time participant configuration is resolved inside `session`, not in the
TUI. Session may consult repo-local config using the invited alias, derive the
participant's runtime role, and pass the synthesized startup prompt into the
agent factory so the backend can apply it during startup.

Session owns the invite-backend decision and passes either `default` or `echo`
to one backend-aware agent factory. When `echo-invites` was enabled before the
first invitation, Session requests `echo`; otherwise it requests `default`.
The application composition root maps that choice to a concrete adapter. The
interpreter still issues the same `InviteCommand`, and both adapters follow the
ordinary participant lifecycle.

---

## Output model

The session controller notifies observers of session events. Observers are registered at construction time via `WithObserver`, following the same pattern as `ProtocolObserver` in the codex package:

```go
type Observer interface {
    OnEvent(e Event)
}
```

Implementations must be fast; avoid operations that can block for non-trivial time. A blocking observer will stall all agent reader goroutines. If an observer needs to process events on its own goroutine, it puts the event on an internal queue inside its `OnEvent` implementation — the session controller is not responsible for that decoupling.

Multiple observers remain supported for infrastructure concerns such as event
logging. At the application boundary, the interpreter is the observer: it
updates its canonical room projection, advances workflows, and publishes
snapshots and events to the UI. The TUI does not register directly.

`session.Event` is the canonical runtime event model for coderoom.

- session publishes `session.Event`
- interpreter applies `session.Event` to its room projection
- interpreter exposes room and participant snapshots to the UI
- UI renders those snapshots rather than consuming `session.Event`
- future persistence / replay should derive from `session.Event`

We do **not** want a second peer event model owned by another package. If a
persisted event-log schema is needed later, it should be defined as a
projection of `session.Event`, not as a competing source of truth. See
[`pkg-room.md`](pkg-room.md) for the room/record projection layer that sits
between session events and the UI.

`Event` is defined in the session package as a sealed interface implemented by
concrete event structs:

```go
type Event interface {
    sessionEvent()
}

type AgentStarting struct{ Alias string }
type AgentStarted struct{ Alias string }
type AgentStopped struct{ Alias string }
type AgentCrashed struct{ Alias string }

type AgentLog struct {
    Alias string
    Text  string
}

type AgentMessage struct {
    Alias string
    Msg   agent.Message
}

type ParticipantStatusChanged struct {
    Alias string
    From  participant.Status
    To    participant.Status
    Since time.Time
}

type Broadcast struct{ Text string }

type SharedSend struct {
    Alias string
    Text  string
}

type SharedNotice struct {
    Alias string
    Text  string
}

type ContextHandoff struct {
    FromAlias string
    ToAlias   string
    Text      string
    Preview   string

    SourceRecordIndex int
    BarrierAliases    []string
    IdleAliases       []string
    BusyAliases       []string
    RejectionReason   string
}

type ApprovalRequested struct {
    Alias string
    ID    int64
    Req   agent.ApprovalRequest
}

type ApprovalCleared struct {
    Alias string
    ID    int64
}
```

Observers branch on concrete event type, then on message content when handling
`AgentMessage`. `AgentMessage` carries the full `agent.Message` value without
translation. Consumers type-switch on `event.Msg.Content` to handle specific
content types (`Output`, `Reasoning`, `Command`, `FileChangeSet`, etc.). See
[`pkg-agent-messages.md`](pkg-agent-messages.md) for the message model.

`AgentLog` remains a dedicated event type with `Text` set directly. This lets
observers handle diagnostic lines without inspecting message content.

`ParticipantStatusChanged` is emitted for every `participant.Status`
transition the session drives, including the idle transition after a turn
ends. `From`, `To`, and `Since` are sufficient for an observer that only needs
to track status; full participant identity (role, initiative, color) is read
from `session.Roster()` by the interpreter, not reconstructed by the UI.

`ApprovalRequested` carries the queue-managed approval `ID`, the participant
`Alias`, and the `agent.ApprovalRequest` payload. `ApprovalCleared` carries the
same `ID` plus the cleared alias so consumers can dismiss the active prompt
without re-reading session state.

---

## Agent lifecycle

`InviteCommand` constructs the participant without presentation input and calls
`registry.Add`, which assigns the participant's deterministic color. It then
starts the agent. On success, it emits `AgentStarted` and launches a reader
goroutine for that agent. A participant removed later does not release its
color for reuse during the session.

Agent process lifecycle is rooted in the session lifecycle context. The
session derives one child context per invited agent and passes that child into
the backend adapter via the configured factory. This gives teardown a strict
hierarchy: session shutdown cancels all agent contexts, while removing or
failing one agent cancels only that agent's subtree.

The reader goroutine loops on `agent.Read()`, forwarding each message to
observers as an `AgentMessage` event (or `AgentLog` for `Log` content). The
session also inspects messages for participant state management — it does not
accumulate or translate content. Open-stream tracking lives on the participant
itself, and the session is the sole mutator of that runtime state. The session
drives participant transitions; the participant validates whether they are
legal:

- Successful `Send` / `SendNotice` calls first commit the participant to the
  turn (`PrepareForWork`), then transition it to `working` once the adapter
  returns the turn anchor
- First `Output`, `Reasoning`, `Command`, or `FileChangeSet` fragment (`ModeStream`) → open a tracked stream for that participant
- Matching `ModeFlush` for one of those streams → close the tracked stream
- Anchor flush for the participant's active turn → `MarkIdle`, which emits
  `ParticipantStatusChanged` (`To: participant.StatusIdle`)

Notice turns are the special case: `SendNotice` may be fully silent, so the
session primes a synthetic `codex:notice-turn` stream on send and closes it when
the adapter emits the matching flush.

When `Read()` returns an error, the goroutine checks whether shutdown was
requested (via a per-agent stop channel) to emit `AgentStopped` vs
`AgentCrashed`, then exits.

`RemoveCommand` removes the participant from the registry, cancels the reader
goroutine's context (so it will emit `AgentStopped` rather than `AgentCrashed`
when it exits), then calls `agent.Stop`.

`CancelCommand` looks up the participant and rejects it if the agent is still starting or has crashed. It calls `agent.Interrupt()`, which is best-effort — the call returns nil for all no-op cases (no active turn, or the backend does not support cancellation). The agent remains in the registry and its reader goroutine continues running.

---

## Message routing

| Command | Routing |
|---|---|
| `BroadcastCommand` | Emits `Broadcast`; sends text to the frozen `Aliases`, or to all currently routable participants when `Aliases` is nil for legacy callers |
| `SharedSendCommand` | Executes a session-created `SharedSendPlan`: sends `TextDirect` to its addressed participant and `TextListeners` to its frozen listeners; emits one `SharedSend` event and one `SharedNotice` event per delivered listener |
| `EnablePolicyCommand` | Idempotently enables a room-local runtime policy; unknown policies fail |
| `PrivateSendCommand` | Sends text to the addressed agent only; no shared room event; no other agents notified |

Shared room visibility is a property of the event kind, but the session does
not own the final chat projection. It emits runtime events; the room package
decides how those events become rooms and records for the UI.

`PlanSharedSend` freezes the policy-aware audience when the user submits the
message. Enabling `send-notices` later or making another participant routable
does not alter an existing plan. Planning is not a reservation: participants
may become unavailable before execution, and those delivery attempts fail
normally. Plans are immutable to callers, valid only for their creating
session, and execution reports all successful recipients on partial failure.

---

## Concurrency model

- One goroutine per agent (the reader loop) — spawned on `InviteCommand`, exits on agent death or `RemoveCommand`.
- `Execute` runs on the caller's goroutine (the interpreter's serialized execution loop).
- Reader goroutines call `observer.OnEvent` directly; the observer must not block.
- The registry and session state are accessed from both `Execute` and reader goroutines. A mutex protects shared access.
- Keepalive candidate selection happens under the session lock, but adapter
  calls such as `KeepAlive()` happen after unlocking. Session first claims the
  participant (`idle -> keepalive`) under lock, then touches the backend out of
  lock so one slow keepalive request cannot stall unrelated reads or sends.

---

## Design boundary

The session controller owns:
- Command execution and dispatch
- Agent lifecycle (start, stop, crash detection)
- Message routing to agents
- Mutation of participant runtime state
- Emitting session events

The room package owns:
- Projection of `session.Event` into chat-visible rooms and records
- Record accumulation / finalization for streaming agent output
- The canonical room data model consumed by the UI

The prompt-language package owns:
- Parsing raw user input into UI-independent statements

The interpreter owns:
- Translating statements and forwarding commands to session
- Coordinating shell commands, definitions, and loops
- Owning the live room projection and selecting handoff sources
- Publishing application snapshots and events

The TUI owns rendering and presentation-only interaction state.

The TUI does not talk to agents or session directly and does not assemble chat
semantics from raw session events.
