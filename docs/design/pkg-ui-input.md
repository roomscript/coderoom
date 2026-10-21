# Package design: input box interaction

This document specifies how the user interacts with the input box at the bottom
of the coderoom TUI, including editing, submission, multi-line composition, and
how those choices coexist with scrolling the output viewport.

Scope: Phase 1 UX decisions for `internal/ui` only (not session routing or agent
policies).

## Goals

- Make the primary loop fast: read output → type → send.
- Avoid confusing key conflicts between input editing and viewport scrolling.
- Support multi-line messages without introducing a heavy “mode” system.
- Preserve a familiar “compose in editor” escape hatch for longer edits.
- Keep room for future shortcuts without locking the UI into a brittle scheme.

## Non-goals (for now)

- Full Vim/Emacs-like keybindings inside the input.
- Complex focus management across multiple panes.
- In-room rich formatting or markdown preview.

## Layout context

See `docs/design/pkg-ui.md` for the overall layout. This spec focuses on the
input row and its interaction with the viewport above it.

## Approval panel sizing

The room owns the available width and height. The approval component uses its
natural height up to that limit, leaving one history row plus the header and
separators. Options stay below the request; the complete request is visible
whenever it fits. `Height()` and `View()` derive from the same wrapped layout.
Tabs expand to eight-column stops before wrapping, counting option prefixes;
wrapping measures ANSI-aware display widths.

When the request does not fit, `PgUp`/`PgDn` scroll it while approval input is
focused. `Up`/`Down` or `k`/`j` select options. History-focused paging and mouse
wheel scrolling retain their history behavior. The status row marks hidden
request rows with `R↑`/`R↓` and option rows with `O↑`/`O↓`, with navigation hints
where width allows.

Small panels prioritize a visible selected option, then a status row, then
request text. When no request rows are visible, request paging is a no-op and
its key hint is omitted; hidden-content indicators remain visible. An option window keeps every choice reachable. A one-row panel
shares the selected label with overflow indicators; very narrow panels use `…`
for hidden request text and `↕` for hidden options. Labels may be shortened to
fit those indicators. Resizing preserves the selected choice and clamps request
scrolling; replacement or dismissal resets request scrolling. Saved input,
history selection, and focus restoration retain their existing ownership.

## Interaction model

The input area is treated as the primary focus target at all times:

- Keystrokes go to the input editor (not the viewport).
- The viewport is scrolled with the mouse wheel/trackpad by default.
- Keyboard scroll shortcuts may be added, but must not steal arrow keys from the
  input once multi-line editing exists.

## Keybindings

### Editing (input-focused)

- `Left` / `Right`: move cursor within the input.
- `Up` / `Down`: move cursor across lines in the input.
- `Home` / `End`: jump to the beginning / end of the current line.
- `Ctrl+Left` / `Ctrl+Right`: move cursor one word backward / forward.
- `Ctrl+W` / `Alt+Backspace`: delete the word before the cursor.
- `Ctrl+K`: delete from the cursor to the end of the line.
- Standard text editing keys remain available (Backspace/Delete, etc.).

Rationale: once input is multi-line, arrows must be unambiguous. Reserving
arrows for input editing avoids mental context switching.

### Newline vs submit

- `Enter`: submit the current input buffer as a single message to the shared
  room, then clear the input.
- `Alt+Enter`: insert a newline at the cursor position in the input.

Notes:

- `Alt+Enter` is preferred because it is commonly detectable in terminal apps
  and does not depend on terminals emitting a distinct `Ctrl+Enter` sequence.
- `Alt+Enter` is not universally reliable across all terminal stacks (e.g.
  certain SSH/tmux/screen combinations). We intentionally ship a single chord
  first and defer alternate bindings to a future keybinding design doc to keep
  shortcuts consistent.
- Submission should be possible even when the buffer contains newlines; i.e.
  `Enter` sends the whole buffer.

### Editor compose

- `Ctrl+G`: open the user’s `$EDITOR` on a temporary file pre-filled with the
  current input buffer; when the editor exits, replace the input buffer with
  the file contents.

Rationale: matches workflows developers already know (`git commit`, `gh pr
create`) and provides a powerful path for long edits without making the in-TUI
editor overly complex.

Failure cases:

- If `$EDITOR` is unset/empty, fall back to `$VISUAL`. If both are unset, emit a
  system record explaining how to set an editor and keep the input buffer
  unchanged.
- If the editor process exits non-zero (compose canceled), restore the input
  buffer to its exact pre-compose value (do not clear).

Normalization:

- On return from the editor, trim exactly one trailing newline (`\n`) from the
  file contents if present. Do not trim any other whitespace and do not collapse
  multiple trailing newlines.

### Scrolling the viewport

- Mouse wheel / trackpad scroll: scrolls the viewport (output area).
- `PgUp` / `PgDown`: scroll the viewport by half a page, except when approval
  input is focused (see approval panel sizing above).

Principles:

- Scrolling should work while the input remains focused.
- Scrolling should not depend on the cursor position within the input.
- Because wheel scrolling requires terminal mouse reporting, some terminals may
  require `Shift+drag` (or an equivalent terminal override) for native text
  selection while the app is running.
- Do not use `Alt+…` scroll shortcuts (reserved for input composition, e.g.
  `Alt+Enter` = newline).
- Avoid `Ctrl+PgUp/PgDn` as a default because some terminal emulators bind it to
  tab switching (notably GNOME Terminal by default).

## Focus and mouse semantics

- The input is always focused (no explicit “focus viewport” toggle in Phase 1).
- The mouse wheel scrolls the viewport regardless of whether the pointer is over
  the viewport or the input row.
- Terminal-native mouse selection may require a modifier such as `Shift` while
  the app is capturing wheel events.

Rationale: consistent behavior is easier to learn than hit-testing or implicit
focus changes.

## Input height and internal scrolling

The input area is variable-height up to a fixed maximum. As the user adds
newlines or types long lines that wrap, the input grows and the viewport shrinks.

Constraints:

- The input must have a maximum height so a long draft cannot push the viewport
  off-screen.
- Once the maximum height is reached, the input editor becomes internally
  scrollable via keyboard navigation (not via the mouse wheel, which remains
  reserved for viewport scrolling).
- Height accounts for visual rows (wrapping), not just logical newlines, so a
  single long line that wraps is treated the same as multiple short lines.

Recommended maximum height:

- `min(8 lines, 1/3 of terminal height)` (exact tuning may change, but the max
  must be explicit).

### Scroll indicators

When the input content exceeds the visible area, scroll indicators appear on the
border lines framing the compose area:

- `▲` on the top separator (between history and compose): content is hidden above
  the visible portion.
- `▼` on the bottom separator (between compose and toolbox): content is hidden
  below the visible portion.

Both indicators are placed at the right end of their respective separator lines,
trimming one dash to make room. Neither indicator appears when all content fits
in the visible area.

## Data model and submission semantics

- The input buffer is a `string` that may contain embedded `\n`.
- On submit, the raw buffer is echoed into the room as the user’s message and
  routed using the same rules as single-line input (see `docs/design/pkg-ui.md`
  “Command parsing”).
- The UI should treat whitespace-only messages as no-ops (do not send).

## Session state during editor compose

While the TUI is suspended in `$EDITOR`, the session continues to run:

- Agents may emit output.
- Events accumulate in the UI queue.

On resume, the UI must process any queued events and re-render so the user sees
everything that happened during composition.

## Implementation notes (non-binding)

The input component is `bubbles/textarea`, wrapped in `compose.Model`
(`internal/ui/room/compose`). Key handling is mostly delegated to the textarea's
default keymap; the compose layer intercepts `Enter` (submit), `Alt+Enter`
(newline), `Ctrl+C` (clear), and remaps `Ctrl+Left`/`Ctrl+Right` to the
textarea's word-movement bindings.

## Accessibility and ergonomics

Mouse-only scrolling is not sufficient for all environments (e.g. SSH sessions,
limited terminals). Even if keyboard shortcuts ship later, this spec should keep
them in mind:

- Provide at least one keyboard scroll path (this spec includes `PgUp` / `PgDown`
  for half-page scrolling).
- Ensure the chosen newline chord does not block submission or basic navigation.

## Prompt format

The compose input uses a 2-character prompt prefix:

- First logical line: `❯ `
- Continuation lines (wrapped or multi-line): `  ` (two spaces, aligned with
  the content column above)

This applies to both soft-wrapped single lines and hard-newline multi-line
content. Line numbers are not shown.

## Open questions

_(none open)_
