# Package design: toolbox activity monitor (participant cells)

This document proposes a toolbox section that displays per-participant activity
cells directly beneath the composer. The intent is to make the room feel “live”
without forcing the user to read the transcript to understand what agents are
doing.

Status: implemented (Phase 1).

## Goals

- Provide a stable, glanceable view of participant status.
- Avoid distracting horizontal jitter while timers tick.
- Fit naturally under the composer as part of the toolbox area.
- Scale from 0 → many participants with predictable layout.

## Ownership

To keep state consistent across the system, **activity state** lives with the
participant/session model, while **presentation** lives in the UI.

Session/participant owns:

- Participant status: `idle | starting | attached | keepalive | preparing | working | crashed`
- `since` timestamp for the current status
- Allowed state transitions (enforced centrally)

UI owns:

- Glyph mapping (e.g. `●/◌/⏹◆/✖`)
- Spinner animation (alternating glyphs) and the 1Hz tick loop
- Elapsed formatting and layout (cell width, alias truncation, overflow)

## Layout

The UI has three vertical regions:

1. History (viewport)
2. Composer (textarea), framed above and below by separator lines owned by the
   room component
3. Toolbox (below composer)

The toolbox renders **only the participant cells row**. The separator line
visually separating the compose area from the toolbox is the room's bottom
border, not part of the toolbox. This keeps both compose-area borders (and their
scroll indicators) under the same owner.

If other toolbox elements are added later (shortcuts, active room, help hints),
they appear *below* the cells.

### Cell grid model

Participant cells are laid out in an invisible table, but Phase 1 intentionally
caps the display to a single row:

- Up to **N cells per row**
- **No wrapping**: participants that do not fit are collapsed into a `+N` overflow
  indicator at the end of the row.

The primary objective is stability: a participant’s cell should remain in the
same column as time ticks.

#### Choosing N

N is derived from terminal width and a fixed cell width:

- `cellWidth` is constant (see “Cell format”).
- `N = max(1, floor(innerWidth / cellWidth))`

## Cell format

Each participant cell renders as:

```
<glyph> <alias> (<elapsed>)
```

The entire cell (glyph, alias, and elapsed) is rendered in the participant's
assigned colour. Cells for participants with no colour set are rendered in the
default terminal colour.

Examples:

- Idle: `● ada`
- Keepalive: `◔ ada (12s)`
- Working: `⏹ ada (10s)` and `◆ ada (11s)` (alternates each second)

Status is communicated **only** via the leading glyph. We intentionally do not
print words like “idle/working/starting” inside the cell to keep the toolbox
compact and stable.

The “worst case” cell width is planned so the cell does not resize as time
grows.

### Cell width

To avoid jitter, each cell targets a fixed width in terminal columns.

Definition (conceptual):

- `cellWidth = glyphWidth + 1 + aliasMax + 1 + len("(59m59s)")`

Notes:

- Some glyphs (●, ⏹, ◆) render as width 2 in certain terminals. Implementations
  should compute width using `ansi.StringWidth` (or equivalent) rather than
  assuming `glyphWidth == 1`.

Phase 1 constants:

- `aliasMax = 10`
- max elapsed string: `(59m59s)`

### Alias width

Aliases are constrained to a fixed display width:

- If longer than `aliasMax`, truncate with an ellipsis.
- If shorter, pad with spaces to `aliasMax`.

This prevents grid jitter when participants with different alias lengths are
present.

### Elapsed time formatting

Time should be compact and monotonic:

- `< 60s`: `12s`
- `< 60m`: `3m12s`
- `>= 60m`: `1h02m`

The formatted duration must have a maximum width so the cell width remains
stable. The above formats have predictable max lengths for realistic sessions.

## Status states

Each participant has a state machine that drives the status glyph and
`<elapsed>`.

### States

- `idle`: no active turn
- `starting`: agent process is starting
- `attached`: agent process is live, but startup is not fully committed yet
- `keepalive`: backend maintenance request in flight
- `preparing`: the session has committed to a send and is establishing turn state
- `working`: agent is processing a request / streaming output
- `crashed`: agent crashed
- `stopped`: agent left / stopped

Idle cells do not show an elapsed field (no `(…)` suffix).

### What counts as “busy”

A participant is considered busy for dispatch/routing purposes if it is in any
non-idle runtime state, including `keepalive`.

`keepalive` is special:

- it blocks new sends like other busy states
- it is visible in the toolbox as participant activity
- it does not create a user-visible room-history record on its own

### What counts as “working”

The toolbox does not infer work from transcript events. It renders the current
participant status projected by session/participant.

That means:

- `preparing` means the session has committed to a send and is establishing the
  turn anchor
- `working` means a real turn is in flight
- `keepalive` means backend maintenance is in flight without a user turn

### Start/stop rules

- Enter `starting` when session begins agent startup.
- Transition `starting -> attached -> idle` during startup commit.
- Enter `preparing` when session commits to a new user-visible send.
- Transition `preparing -> working` when the turn anchor is established.
- Enter `keepalive` when session claims the participant for backend
  maintenance.
- Return to `idle` when the active turn or keepalive round-trip completes.
- Enter `crashed` when the agent exits unexpectedly.
- Remove the participant from the activity monitor when it leaves the session.

Elapsed semantics:

- `starting`: elapsed since `agent.starting`.
- `attached`: elapsed since the agent became live but not yet fully started.
- `preparing`: elapsed since the send was committed.
- `keepalive`: elapsed since backend maintenance started.
- `working`: elapsed since entering `working`.
- `crashed`: elapsed since `agent.crashed`.
- `idle`/`stopped`: no elapsed displayed.

## Tick / animation model

The UI updates once per second **only if** any participant is in `starting`,
`attached`, `keepalive`, `preparing`, `working` or `crashed`. Otherwise, no
periodic ticks run.

### Spinner glyphs

Working state uses an alternating glyph as a low-noise spinner:

- Even seconds: `⏹`
- Odd seconds: `◆`

Idle uses `●`.
Starting uses `◌` (static).
Attached uses `◌` (static).
Preparing uses `◐` (static).
Keepalive uses `◔` (static).
Crashed uses `✖` (static).

### Legend (discoverability)

The meaning of glyphs should be discoverable without expanding each cell. A
small legend can live in help text or a toolbox hint line (not inside the cells
themselves), e.g.:

- `●` idle
- `◌` starting / attached
- `◐` preparing
- `◔` keepalive
- `⏹` / `◆` working
- `✖` crashed

## Ordering and stability

To keep the display useful when capped to one row, ordering is activity-first:

1. `working`
2. `preparing`
3. `keepalive`
4. `starting` / `attached`
5. `crashed`
6. `idle`

Within the same state tier, participants are sorted by alias (stable,
deterministic).

Stability guarantees:

- A participant’s cell does not move due to ticking.
- Cells may move when a participant changes state (e.g. idle → working) because
  activity-first ordering takes priority over positional stability in the
  single-row capped display.

## Overflow

If there are more participants than can fit in the single row:

- The last visible slot is replaced by a `+N` indicator (e.g. `+3`) showing how
  many participants are not displayed.
- Hidden participants still update their internal state/elapsed; they are simply
  not rendered until they become visible (e.g. by becoming active and rising in
  the activity-first order).

## Future work

- Multi-row layout for very wide participant lists (revisit if activity-first movement is
  too disruptive).
- Optional affordance to expand the participant list temporarily (e.g. press a key to show
  all participants for one tick).
