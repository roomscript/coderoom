# UI design: record-based rendering

## Motivation

The current model accumulates output as a flat `[]string` of lines. This produces a wall of undifferentiated text that is hard to scan. The record model replaces it with structured units that carry authorship, visual identity, and routing metadata, making conversations readable at a glance.

This document describes UI rendering concerns for records. The canonical
record model belongs in `internal/room` as `room.Record`; the UI consumes that
model and may wrap it with view-local state when needed.

---

## Record

A record is the atomic unit of viewport content. Canonical records are owned by
`internal/room`; the UI should render them rather than define a competing
source-of-truth record model.

```go
type ViewRecord struct {
    Record room.Record
    // Optional UI-local state such as collapsed/expanded or cached rendering
    // lives here, not in room.Record.
}

// room.Record remains the canonical semantic unit; the UI derives view state
// from it.
```

Canonical text and metadata live in `room.Record`. Wrapping and styling are
applied at render time, not stored.

A participant record retains its full `participant.Alias`, including instance
identity. The renderer passes that value to `RenderContext.ColorForAlias` and
uses only `.Name` in headers. Color policy can therefore distinguish historical
records from a replacement using the same name. Routing footers carry derived
display names; their colors use the separate `ColorForName` callback. Rendering
does not resolve those names into participant instances.

The history presenter keys departed participants and open streams by full alias;
stream IDs are scoped within each participant. Snapshot replacement and delta
application preserve those owners, and departure changes invalidate render
caches without marking a same-name replacement departed. Streaming and departure
queries accept resolved aliases. History receives separate instance and routing
name color callbacks and supplies both to viewport and transcript rendering.
The room component forwards both callbacks and accepts resolved aliases for its
streaming and departure queries. Its transcript projection remaps record indices
around local UI notices while preserving record, stream, and departure owners.

---

## Visual layout

Records are separated by a blank line to give the output room to breathe.

### User input

```
  > /invite ada

```

The `>` prefix is styled (bold or dimmed) to distinguish it from agent output. The line is echoed immediately on Enter, before any system response.

### Agent output

```
  ●  ada:
  This is the agent's response, which may span
  multiple lines as deltas stream in.

```

The header line is `● <alias>:` — the filled circle and alias text are both rendered in the agent's assigned colour, followed by a plain colon. The body renders below the header, left-aligned. No per-line prefix — the header provides authorship for the whole record.

### System record

```
  [ada joined]

```

Single line, dimmed. Used for lifecycle events (`starting`, `joined`, `left`, `crashed`). The `starting` and `joined` records carry the participant's alias and render in its assigned colour, still dimmed; other system records use the default colour. Listener routing notices are not rendered as separate system records; the sender record footer already shows the full routing list.

**Exception**: `/help` output is a multi-line system record for readability. Splitting it into individual records would insert blank lines between each help line, making the output hard to scan.

### Log record

```
  ▸ npm warn deprecated package

```

Single line, greyed out (lipgloss colour 240). Identical to current rendering; just modelled as a record.

### Routing footer

When the user sends a broadcast or direct message, the input record shows who
the UI routed the message to. Each alias in the footer is rendered in that
agent's assigned colour:

```
  > hello everyone
  → ada    → turing

```

For a direct send (`@ada do the thing`) the footer may include both the
directly addressed agent and any listener recipients implied by shared-room
routing:

```
  > @ada do the thing
  → ada    → tim

```

The `→` arrow is plain; only the alias text is coloured. The footer is part of
the rendered user-input record, but it is a UI-owned reference signal rather
than canonical room message state. The UI computes it from the routing decision
at submission time and renders it from view-local metadata. It is omitted when
there is no routing signal to show (e.g. `/invite`, `/who`, `/help`).

---

## Agent colour

Each agent is assigned a generated colour when it joins. The colour is stored in
`participant.Participant.Color` as a CSS/HTML hex string (for example,
`"#4ADE80"`) and used wherever the agent's alias appears (header bullet, routing
footer).

Colours are generated deterministically in OKLCH space, with hues advanced by
the golden angle to spread successive assignments around the hue wheel. The
result is converted to 24-bit sRGB. Assignment does not exhaust a short fixed
list, and a generated colour is not reused within the same session, including
after its participant leaves.

**Departed agents**: when an agent stops or crashes, all of its historical records (agent output headers, routing footer aliases) are immediately re-rendered in `ColorDeparted` (`#6b7280`, muted grey). This repaint persists across terminal resizes. The effect signals that the output belongs to a past participant without losing the authorship structure of the record.

---

## Streaming

Streaming output maps naturally onto the agent output record, but the opening,
extension, and closing of that record are room concerns. The UI should consume
already-projected record state from `internal/room` and render it. If the UI
needs fast access to which records are still open, that should be derived from
room-owned state supplied through the room component, not rebuilt from raw
session events in `history`.

---

## Wrapping

Wrapping moves from `syncViewport` into the record renderer. Each record renders to a `string` that fits `viewport.Width`. The agent output record uses a hanging indent equivalent (content left-aligned under the header, not prefixed). The flat `wrappedLines []string` cache is replaced by a per-record rendered cache: `renderedRecords []string` where each entry is the fully rendered, wrapped, styled text for one record. On resize, all records are re-rendered.

---

## Data model changes

| Current | Replaced by |
|---|---|
| `lines []string` | canonical `[]room.Record` plus UI-local view state |
| `wrappedLines []string` | rendered/cache state owned by the history view |
| `linePrefixes []string` | per-record render function (no stored prefix) |
| streaming reconstruction in UI | room-owned streaming/projection state |

The important boundary is: room owns canonical records; the UI renders them.
If the UI needs a wrapper type, it wraps `room.Record` rather than redefining
the canonical model.

---

## Open / deferred

- Acknowledgement footer (who confirmed receipt): deferred; requires session-level tracking not yet designed.
- Multi-line user input (paste): out of scope for Phase 1.
