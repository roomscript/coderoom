# UI design (open): queued message area above composer

Status: open / draft

This document proposes a small “queue area” UI that appears above the composer
when one or more messages are pending delivery to agents. It makes message
queueing (default) visible and actionable without introducing a new “queue
room” channel.

## Motivation

The current interaction primitives assume a single in-flight agent turn: send a
message, then either wait for completion or issue `/cancel`. With multiple
agents, the user needs to:

- send follow-ups while agents are busy (queue by default)
- see what is queued and for whom
- optionally interrupt a busy agent to send a queued message immediately

The queue area provides a compact, always-in-context view of pending deliveries
without polluting the transcript with transport mechanics.

## Design constraints

- Preserve the communication model in `docs/design/concept.md`:
  - Shared Room and Private Agent Channel remain the only “rooms”.
  - Queue state is a delivery concern, not a third conversation surface.
- Avoid import cycles: record rendering must not depend on higher-level UI
  packages.
- Keep default behavior safe: queue (do not interrupt) unless explicitly asked.

## Goals

- Show queued messages above the composer only when relevant.
- For each queued message, show per-agent delivery state at a glance.
- v1: only display the queue but don't allow interactions like edit/drop.
- v2: introduce minimal actions: edit, drop
- v3: introduce the interrupt-send workflow
- Queue status and interactions don't create events in the history (no “queue update spam”).
- Scope the queue to user-authored messages only (not agent-to-agent messages).

## Non-goals

- Full per-record selection UI in the main viewport.
- A “queue room” with its own transcript.
- Sophisticated scheduling (priorities, fairness across agents) beyond simple
  per-agent FIFO.
- Agent-side partial turn injection; queueing operates at turn boundaries.
- Defining or enabling agent-to-agent communication semantics.

## Concept: queued entry and target states

A queued entry is a user-authored message that is destined for one or more
agents. It is not a transcript record; it is a UI+delivery object.

Important scope decision (v1+):

- This queue is the *user messages queue*. Only user actions can create queued
  entries.
- Agents may propose messages to send, but they do not enqueue messages
  automatically (keeps behavior predictable and avoids runaway queue growth).

### Data model (conceptual)

```
QueuedEntry {
  id: string            // stable identifier for UI actions
  createdAt: time
  text: string
  targets: map[alias]TargetState
}

TargetState = queued | dispatched | cancelled | failed
```

Notes:

- `dispatched` means the system has handed the message to the agent backend as a
  new turn (i.e. it is no longer waiting in the user queue).
- `cancelled` is user-initiated removal of a queued target before dispatch.
- `failed` is a delivery failure before dispatch (e.g. agent removed/crashed
  before it could be dispatched, backend adapter rejects start).

Derived UI groupings:

- waitingFor = aliases where `TargetState == queued`
- dispatchedTo = all other aliases

Rationale: storing the per-target state avoids painting the system into a corner
when adding interrupt/retry/failure handling.

### Shared predicates (keep logic in one place)

Multiple parts of the system need to agree on which target states count as
“pending delivery”:

- visibility (should the queue area show?)
- display grouping (waiting vs dispatched)
- actions (drop / interrupt-send)

To avoid subtle drift, define these predicates once and reuse them:

- pending delivery (queue-visible): `queued`
- droppable: `queued` only
- interrupt-eligible: `queued` only

## State machine (events-backed)

This section defines the per-target state transitions and the events that
trigger them. Only states with reliable triggers are modeled.

### TargetState transitions

```
queued ──(dispatch turn start)────> dispatched
queued ──(user drops target)──────> cancelled
queued ──(agent removed/crashed)──> failed
```

### Trigger definitions (conceptual)

- dispatch turn start:
  - triggered when the dispatcher starts a new agent turn from a queued entry
  - requires a concrete “turn started”/“request accepted” signal from the agent
    adapter (do not infer from output heuristics)
- user drops target:
  - triggered by an explicit user action (v2+), e.g. `/queue drop q7` or UI
    affordance
- agent removed/crashed:
  - triggered by an explicit session lifecycle event indicating the agent can no
    longer receive work

If the system lacks these explicit events today, they must be introduced before
implementing the queue UI. Avoid adding “sending/completed” states until there
is a reliable event source for them.

## Rendering: queue area placement and behavior

### Placement

The queue area renders between the viewport and the composer:

```
[viewport transcript ...]
─── history/compose separator (focus indicator) ───
[queue area (conditional)]
[composer textarea]
```

### Visibility rules

- Hidden when there are no queued entries with any target in `queued` state.
- Shown when at least one entry has at least one target in `queued` state.
- Optionally cap displayed entries to the most recent N (e.g. N=3), with a
  `(+K more)` summary line.

QUESTION: how many entries at most will we show if the queue grows? An idea would be to restrict it to a small number, like you can only queue 1-2 messages (number is open to discussion). this would make it predictable and you know you need to manage the current messages before queueing more. this would avoid the situation where we create 10 queued messages and then have to scroll/query through the messages. 


### Entry display (collapsed)

Each entry is one to a few lines:

- First line: short text preview (single-line, truncation by width)
- Second line: per-agent badges grouped by state

QUESTION: should we restrict it to single line per entry? I imagine this as a table

Example:

```
Queued: "hi"
  waiting: ada, turing   sent: hopper
```

If the system has color-by-alias, alias badges should reuse that color (same as
participant headers), but the state labels remain neutral/dim.

### Entry display (expanded - optional)

If needed later, allow expanding an entry to show the full text (multi-line),
but keep v1 collapsed to protect space.

## Interaction model

### V1 (visibility)

Default behavior when sending to a busy agent:

- message is enqueued (per-agent FIFO)
- queue area appears and shows:
  - the new entry
  - which agents are waiting (queued) vs already dispatched

No actions are possible with the queue

### V2 (edit/drop)

Actions (suggested minimal set):

- Edit most recent queued entry (keybinding TBD; see “Open questions”)
- `/queue drop <id>` (or `/queue drop last`): remove a queued entry

If a queue item targets multiple agents, “drop” should remove the entry for all
targets still in `queued` state. (Targets already `sent` cannot be “unsent”;
they can only be cancelled via per-agent turn cancellation.)

Drop display semantics:

- After dropping, keep the entry visible as long as it has any target state in
  `queued` (i.e. still pending delivery).
- If no targets are pending, the entry can disappear from the queue area.

### V3 (interrupt + send now)

Add an explicit action that applies only to targets that are still waiting:

- “send now” = for each target in `queued`:
  1) interrupt the agent’s current turn (if any)
  2) dispatch this entry immediately

Possible UI affordances:

- Keybinding in history mode: `Ctrl+I` “interrupt waiting agents for newest
  queued entry”
- Command: `/queue send-now <id>` or `/queue interrupt <id>`

Rule: never interrupt automatically on plain send; interrupt is always explicit.

## Delivery engine sketch (non-UI)

Each agent maintains:

```
AgentInbox {
  runningTurn: optional TurnID
  queue: []QueuedEntryID  // or per-agent message objects
}
```

Dispatcher policy:

- At most one in-flight turn per agent.
- When an agent becomes idle, dequeue the next queued message for that agent and
  start a new turn.

“Idle” signal:

- The queue must react to an explicit agent lifecycle signal (e.g. “turn
  completed/failed/cancelled” for that agent) rather than polling output stream
  heuristics.
- The concrete event name and source (session vs backend adapter) should be
  chosen before implementation.

## Transcript interaction (avoid noise)

Queue operations should not create transcript records for every state change.
Recommended:

- When the user enqueues a message: keep the user input record as-is (normal).
- When delivery state changes, update the routing indicators on the original
  user input record rather than appending new “queue status” records.

### Routing indicators (proposal)

The user input record already has routing metadata (footer). Extend it to show
per-target delivery status without adding new records.

Example footer evolution:

- immediately after submit:
  - `⏳ ada    ⏳ turing` (queued)
- once dispatched:
  - `→ ada    → turing` (dispatched)

If an interrupt is issued (v3), emit a single system record to make the
disruptive action auditable, but do not emit records for normal queue drainage.

## Open questions

- Should the queue area appear in compose focus only, history focus only, or
  both? (Proposal: both; it is contextual to composing, but delivery status
  matters while browsing history too.)
- What is the best “edit queued” binding given terminal conflicts (avoid
  conflicting with multi-line navigation)?
- How to map queued entry IDs to a friendly UI reference (e.g. `q1`, `q2`)?
- Do we want to surface `failed` and `cancelled` targets in the queue area (for
  closure), or keep the UI strictly “pending only”?
- Are queued entry targets fixed at creation time or dynamic when the participant list
  changes? Proposal for v1: fixed at creation time; newly invited agents do not
  retroactively become targets of existing queued entries.
