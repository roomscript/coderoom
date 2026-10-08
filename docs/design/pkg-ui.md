# Package design: internal/ui

## Scope

The TUI is the user-facing layer for Phase 1: a single-agent terminal interface that resembles the Codex / Claude Code experience. It owns:

- Rendering room state to a scrollable output area
- Accepting user input and submitting it to the interpreter
- Bridging interpreter events into the Bubble Tea update loop

It is **not** responsible for prompt-language execution, session logic, message
routing, or agent lifecycle. It does not interact with `internal/session`
directly. It is also not the owner of canonical room/chat state; the
interpreter owns the live `internal/room` projection and supplies snapshots.

---

## Layout

```
┌─────────────────────────────────┐
│                                 │
│   scrollable output (viewport)  │
│                                 │
├──────── compose ──────────────▲─┤  ← top separator (label + ▲ when scrolled)
│ ❯ input text                    │  ← compose input (grows with content)
├─────────────────────────────▼───┤  ← bottom separator (▼ when scrolled)
│ ◆ ada (10s)  ● bob              │  ← toolbox: participant cells
└─────────────────────────────────┘
```

The viewport occupies all available height minus the compose input, two
separator lines, the toolbox row, and vertical margins. All regions resize on
`tea.WindowSizeMsg`.

The compose input is variable-height (grows as the user types newlines or long
wrapping lines) up to a cap of `min(8, terminal_height/3)`. When the content
exceeds the visible area, `▲` appears on the top separator and/or `▼` on the
bottom separator.

The toolbox sits below the compose area and renders only the participant cells
row. The separator lines framing the compose area are owned by the room
component, not the toolbox.

---

## Bubble Tea model

```go
// Top-level application model.
type Model struct {
    interp   *interpreter.Interpreter
    queue    *eventQueue
    room     room.Model     // history viewport + compose/approval input
    toolbox  toolbox.Model  // participant cells row
    debug    bool
    lastSize tea.WindowSizeMsg
}

// room.Model owns the scrollable history and the active input area.
// room.inputModel switches between compose and approval modes.
// history.Model wraps a bubbles/viewport for the output records.
// compose.Model wraps a bubbles/textarea for text entry.
// approval.Model handles approval prompts (option list + keyboard navigation).
```

The room component owns all content rendering and the two separator lines that
frame the compose area. The toolbox is a sibling, not a child, of the room.
The room component is the presentation adapter for interpreter-supplied room
snapshots. It does not hold or mutate a live `internal/room.Room`.

---

## Interpreter → Bubble Tea bridge

The interpreter publishes events independently of Bubble Tea's `Update` loop.
The bridge is an event queue and a long-running `tea.Cmd`. The UI does not
register as a session observer:

```go
// interpreterEventMsg wraps an interpreter.Event as a Bubble Tea message.
type interpreterEventMsg struct{ event interpreter.Event }

// awaitEvent returns a Cmd that blocks until the next event is available.
// It receives queue.out — the output channel of eventQueue.
func awaitEvent(ch <-chan interpreter.Event) tea.Cmd {
    return func() tea.Msg {
        e, ok := <-ch
        if !ok {
            return nil
        }
        return interpreterEventMsg{event: e}
    }
}

// Usage in Init and Update:
//   return awaitEvent(m.queue)
```

`eventQueue` owns an unbuffered input channel, an unbounded internal buffer (a plain slice), an output channel, and a pump goroutine that bridges them. `Push` on the input side completes quickly because the pump is always ready to receive. The consumer reads from the output side without ever blocking the producer.

```
interpreter.Observer.OnEvent → eventQueue.Push → [pump / []Event] → awaitEvent → Bubble Tea
```

No fixed-size buffers. No dropped events. If the UI falls behind, the internal slice grows; backpressure propagates naturally through the pump rather than through silent data loss.

`channelObserver` implements `interpreter.Observer` by delegating to the queue.
Each handled event re-arms `awaitEvent`. Room and participant state arrive as
snapshots; the UI does not re-read session state.

---

## Event rendering

| Event type | Rendered as |
|---|---|
| `AgentReady` | `[ada joined]` |
| `AgentStopped` | `[ada left]` |
| `AgentCrashed` | `[ada crashed]` |
| `AgentLog` | `▸ <line>` in grey (lipgloss); de-emphasised diagnostic output; does not participate in streaming state; appended as a standalone line like any other event |
| `AgentMessage` with streaming content | projected by `internal/room` into streaming room records |
| `AgentMessage` with flush content | closes the matching room-owned stream state |

Streaming state and record accumulation are owned by `internal/room`, not by
the TUI. The room component reads canonical room records and adapts them into
UI-local viewport state. `history.Model` wraps a `bubbles/viewport` and
re-renders content on every change, but it should not be the source of truth
for chat semantics.

User-authored routing footers are presentation, but their data comes from the
interpreter. For an addressed send, the interpreter retains the opaque routing
plan created at submission time and publishes its frozen targets. The UI
renders those targets and never recalculates listeners. This does not require
room to project `Broadcast`, `SharedSend`, or `SharedNotice` into canonical
message state.

---

## Command submission

Input is submitted to the interpreter on Enter. The interpreter parses and
executes it; the UI echoes accepted input and renders structured results from
interpreter events.

All prompt-language forms use `Submit`, including `/cancel <alias>`. Dedicated
interpreter methods are used only for structured UI interactions without a
prompt-language form, such as approval decisions and interrupting a staged
barrier batch.

Synchronous stage operations such as `TakeStageForEdit` run inside a `tea.Cmd`.
Their result returns to `Update` as a Bubble Tea message, so waiting for the
interpreter's serialized loop never blocks terminal rendering.

| Input | Command | Notes |
|---|---|---|
| `/invite <alias>` | `InviteCommand` | |
| `/policy enable send-notices` | `EnablePolicyCommand` | Enables listener notices for subsequent direct sends |
| `/policy enable echo-invites` | `EnablePolicyCommand` | Uses deterministic echo agents; must precede every invitation |
| `/cancel <alias>` | `CancelCommand` | Soft stop: cancels in-flight work for the agent but keeps it in the room |
| `/remove <alias>` | `RemoveCommand` | Hard stop: removes the agent from the room and stops its process |
| `/who` | interpreter query | Renders the current interpreter snapshot inline |
| `/help` | — | Renders available commands inline |
| `@<alias> <text>` | `SendToParticipantCommand` | Only the addressed participant is targeted unless `send-notices` is enabled |
| `<text>` | `BroadcastCommand` | Equivalent to direct send for single-agent sessions |
| `/quit` | interpreter shutdown request | Best-effort stop all agents before UI exit |

The exact `/help` presentation remains in the TUI. `/who`, validation errors,
and all effectful statements are resolved by the interpreter so non-UI callers
observe the same behavior.

### Command semantics (room model)

- `/invite <alias>` adds the agent to the shared room and starts its process.
- `/cancel <alias>` is a **soft stop**: it attempts to interrupt the agent's
  in-flight work but keeps the agent in the room (joined).
- `/remove <alias>` is a **hard stop**: it removes the agent from the room and
  stops its underlying process.

Open questions (deferred to implementation):
- What "stop" means per backend (true cancel vs best-effort stop/restart).
- How to surface "stop not supported" in the UI without noise.

---

## Dependencies

```
charm.land/bubbletea/v2                    # framework
charm.land/bubbles/v2/viewport             # scrollable history output
charm.land/bubbles/v2/textarea             # multi-line compose input
charm.land/lipgloss/v2                     # styling
github.com/rivo/uniseg                     # display-width-aware line metrics
```

The UI depends on the interpreter facade, not on session or agent packages.
It renders each participant's assigned color from interpreter snapshots and
does not allocate participant colors.

---

## Boundary

The TUI owns:
- Bubble Tea model, update, and view
- Interpreter-observer channel and `awaitEvent` wiring
- Rendering room state as styled text
- Presentation of interpreter-owned staged submissions, plus compose editing,
  focus, scrolling, and shortcuts

The interpreter owns parsing coordination, one command registry for the room,
shell lifetime, bounded-loop and staged-submission state, session dispatch, and the
live canonical room projection. The TUI never reads session internals directly.

More specifically:

- `internal/ui` coordinates presentation components and interpreter operations
- `internal/ui/room` adapts interpreter-supplied room snapshots into presentation state
- `internal/ui/room/history` is presentation-only and should not depend on
  `internal/room`
