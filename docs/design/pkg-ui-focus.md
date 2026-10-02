# Package design: shared-room Variant 1 (input focus + viewport focus)

This document proposes a two-focus interaction model for the shared-room TUI.
It is intended as a Phase 1 direction, with the option to evolve later.

Variant 1 keeps the current "viewport + composer" structure but makes focus
explicit and leverages terminal-native mouse selection (no mouse reporting).

See also: `docs/design/pkg-ui-input.md` for input box interaction details.

## Goals

- Keep the core UX recognizable to Codex / Claude Code users.
- Make scrolling and copy/paste straightforward.
- Support mouse-wheel / trackpad scrolling directly in the room UI.
- Keep implementation maintainable (avoid terminal scrollback hacks).

## Non-goals (for now)

- Multiple panes.
- In-app mouse drag selection.
- Editing or mutating viewport content.
- Hidden-content cursoring or selection semantics.

## Concepts

There are two focus targets:

1. **Composer focus**: user edits input (default).
2. **Viewport focus**: user navigates output history with the keyboard.

Within those focus targets, history interaction should be modeled explicitly as
two modes over the same rendered transcript surface:

- **Live**: follow mode is armed.
- **Browse**: follow mode is disarmed and the user is reading historical
  content.

Mouse-wheel scrolling is handled by the app. Because that requires terminal
mouse reporting, many terminals will require an override gesture such as
`Shift+drag` for terminal-native text selection while coderoom is running.
coderoom does not yet implement its own mouse drag selection semantics.

This yields four valid UI states:

| State | Focus | History interaction | Cursor |
|---|---|---|---|
| `ComposeLive` | composer | viewport pinned to tail; follow armed | inactive |
| `ComposeBrowse` | composer | viewport browsing only | inactive |
| `HistoryLive` | viewport | cursor at live end; follow armed | visible |
| `HistoryBrowse` | viewport | cursor away from live end; browse mode | visible |

There is one transcript and one viewport. The distinction is behavioral, not a
separate history model.

## Keybindings

### Focus switching

- `Ctrl+O`: toggle focus between composer and viewport.

### Composer focus (default)

- Text editing uses the multiline textarea.
- `Alt+Enter`: insert newline.
- `Enter`: submit the full buffer as one message; clear composer.
- `Ctrl+C`: clear the composer buffer (CLI convention). No-op if buffer is
  already empty.
- `/quit`: exit the session.
- `Ctrl+G`: open `$EDITOR` on a temp file seeded with the composer text.
  - Cancel/non-zero exit restores pre-compose buffer.
  - On success, replace buffer with file contents (trim exactly one trailing
    newline).
- `PgUp` / `PgDn`: scroll the history viewport without activating history
  cursor semantics.

Composer-side history browsing is viewport-only. It must not operate through a
hidden history cursor.

### Viewport focus

Viewport is read-only.

- A visible cursor moves across the rendered history surface.
- An active keyboard selection is part of viewport/history state, not composer
  state.
- The cursor operates on the **visible history surface only**:
  - wrapped visible lines
  - visible separator blank lines
  - currently rendered record content
- The cursor does **not** operate on:
  - hidden transcript content
  - collapsed command output not currently rendered
  - any raw `room.Record` representation behind the rendered view
- `Left` / `Right`: move one visible cell backward / forward.
- `Ctrl+Shift+Left` / `Ctrl+Shift+Right`: extend or shrink the selection by
  one visible word. Forward selection stops at the word end, before trailing
  whitespace or a line break; backward selection stops at the word start.
  Reversing direction shrinks an existing word selection by one word.
- `Up` / `Down`: move one visible row up / down, preserving a preferred
  column where possible.
- `PgUp` / `PgDn`: move by roughly one viewport height while preserving cursor
  semantics.
- `Home` / `End`: move to the start / end of the current visible line.
- `Ctrl+O`: return focus to the composer.
- `Esc`: return focus to the composer (escape hatch).
- `Ctrl+C`: copy the active visible history selection as plain text.
- `Ctrl+G`: open `$EDITOR` seeded with the transcript content in read-only
  mode (see below).

If a movement would place the cursor outside the currently visible viewport, the
viewport scrolls just enough to keep the cursor visible.

If an approval prompt temporarily takes input focus while history selection is
active, that interruption must not discard the current selection. Returning to
history should restore the prior selection state until the user explicitly
clears or changes it.

### PgUp / PgDn scope

`PgUp` / `PgDn` always scroll the viewport, regardless of which focus is
active. Arrow keys follow focus: in composer focus they move the cursor within
the textarea; in viewport focus they move the history cursor. Viewport scrolling
in history focus is a consequence of cursor movement rather than a separate
primary interaction.

`PgUp` / `PgDn` are also the primary transition between `ComposeLive` and
`ComposeBrowse`:

- `ComposeLive` + `PgUp` => `ComposeBrowse`
- `ComposeBrowse` + `PgDn` reaching the bottom => `ComposeLive`

Returning to the bottom in composer focus is an explicit re-arm of live follow.

### `Ctrl+G` in viewport focus

1. Export the transcript to a temp file (plain text, no ANSI).
2. Set the file permissions to 444 (read-only) before launching the editor.
3. Open the editor with `$EDITOR <file>` (same path resolution as composer
   Ctrl+G).
4. The export contains the full transcript, not just the visible portion.
5. Any changes the editor saves are discarded on return to the TUI.

Rationale: making the file read-only at the OS level works across editors
without requiring editor-specific flags, and communicates intent clearly.

## Focus indicator

The separator line between viewport and composer shows the current focus in
Phase 1:

- Composer focus: separator renders as `─── compose ───` (or similar).
- Viewport focus: separator renders as `─── history ───` (or similar).

Exact text and styling are implementation details.

In viewport focus, the history area should also render a clear cursor indicator.
This replaces the current “highlight the first visible row” approximation.

## Output follow behavior

- When new output arrives, auto-scroll (follow) if and only if the UI is in a
  **Live** state.
- If the user has scrolled up, do not force the viewport back to bottom.
- Follow behavior exists in both focus targets:
  - `ComposeLive`: viewport follows output; no history cursor is active.
  - `HistoryLive`: cursor remains at live end and viewport follows output.

If the user has moved the history cursor away from the live end, new output
must not steal focus or reposition the cursor.

`LIVE` in the header means follow is armed. It is not merely a synonym for
"viewport currently bottom-aligned."

## State transitions

These transitions should be treated as the design contract:

| From | Event | To | Rule |
|---|---|---|---|
| `ComposeLive` | `PgUp` | `ComposeBrowse` | scroll viewport up |
| `ComposeLive` | `Ctrl+O` | `HistoryLive` | place cursor at live end |
| `ComposeBrowse` | `PgDn` reaches bottom | `ComposeLive` | re-arm follow |
| `ComposeBrowse` | `Ctrl+O` | `HistoryBrowse` or `HistoryLive` | adopt cursor from current viewport; if already at tail, prefer `HistoryLive` |
| `HistoryLive` | move cursor off live end | `HistoryBrowse` | browse mode begins |
| `HistoryLive` | `Esc` / `Ctrl+O` | `ComposeLive` | preserve live follow without exposing a hidden cursor |
| `HistoryBrowse` | move cursor back to live end | `HistoryLive` | re-arm follow |
| `HistoryBrowse` | `Esc` / `Ctrl+O` | `ComposeBrowse` | preserve viewport, suspend cursor semantics |

Entering history focus must derive cursor placement from the currently visible
viewport, not from stale hidden cursor state left behind while composing.

## Implementation notes

- Mouse reporting is enabled in cell-motion mode so wheel events reach the
  history viewport.
- Viewport focus is implemented by routing key events either to the textarea
  model or the viewport model depending on focus state.
- Keep one transcript, one viewport, and one optional cursor. The explicit
  interaction state is what determines behavior.
- Transcript export for `Ctrl+G` in viewport focus should strip ANSI escape
  sequences to keep the editor readable.
- The canonical room record model remains unchanged. Cursor state is UI-local
  state derived from rendered history layout.
