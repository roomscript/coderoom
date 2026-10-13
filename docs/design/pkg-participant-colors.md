# Package design: participant colors

## Status

The generation algorithm is implemented. Its current implementation lives in
`internal/ui/palette`; the interpreter-boundary work moves participant
generation and allocation to `internal/participant` as described in
[`pkg-participant.md`](pkg-participant.md).

This design supersedes the fixed palette and assignment rules previously
documented in `docs/design/pkg-ui-records.md`. The ownership move is target
architecture and is not yet reflected in the current code.

## Goal

`participant.Group` should assign every participant a stable, readable
colour without depending on a short, predefined list. The candidate space is
the full 24-bit sRGB range expressible as CSS/HTML hex colours (`#RRGGBB`); this
does not mean maintaining a list of every value or using CSS named colours.

The participant package owns the generation algorithm, monotonic allocation
state, and assignment. An individual `Participant` retains its assigned colour
but does not choose it. The UI consumes that value from interpreter snapshots.

`internal/ui/palette` remains the home for UI-only semantic tokens such as the
departed-record fallback and file-change colours. It does not retain participant
allocation state after the ownership move.

## Expected behaviour

- The registry's allocator returns a valid `#RRGGBB` colour for every practical
  participant count instead of falling back to the default terminal colour.
- Generation is deterministic: the same initial palette and assignment order
  produce the same colours.
- A generated colour is not reused during a session, including after its
  participant departs.
- An addition rejected during registry validation does not consume a colour.
- Once a participant is accepted by the registry, its colour remains consumed
  even if asynchronous agent startup later fails.
- Existing participants keep their assigned colour. Generating a new colour
  must not change earlier assignments.
- Colours remain bright enough to read on dark terminal backgrounds and are
  spread perceptually so consecutive assignments are easy to distinguish.
  Readability means a WCAG contrast ratio of at least 4.5:1 against `#111827`,
  the representative dark background used by palette tests.
- Existing semantic colours, such as departed-agent and file-change colours,
  remain fixed tokens and are not part of participant colour generation.

## Colour model

Generate colours in a perceptual colour space such as OKLCH. Keep lightness and
chroma within ranges suitable for a dark background, and advance hue by the
golden angle:

```text
hue(n) = (initialHue + n * 137.508 degrees) mod 360 degrees
```

Convert the result to sRGB, adjusting out-of-gamut values before formatting the
final hex code. The implementation may vary lightness or chroma when necessary
to avoid a previously emitted sRGB value. It must not rely on randomness.

Golden-angle hue distribution gives useful separation for prefixes of the
sequence, regardless of how many participants will eventually join. It cannot
guarantee that arbitrarily many colours remain visually distinct; the goal is
to avoid an artificial eight-colour exhaustion limit.

## Terminal compatibility

Hex values are the canonical stored colours. Lip Gloss may quantize them for
terminals that expose ANSI-256 or ANSI-16 profiles, so distinct source colours
can look alike on those terminals. Palette generation does not change stored
colours based on the active terminal profile. Profile-specific assignment can
be considered separately if reduced-colour terminals prove important.

## Package ownership

After the ownership move:

```text
internal/participant
  participant colour generation
  monotonic allocator state
  registry assignment
  determinism, uniqueness, and contrast tests

internal/ui/palette
  departed-record fallback colour
  file-change and other semantic rendering colours
```

`session.InviteCommand` does not accept a colour, and neither the interpreter
nor the UI selects one.

## Verification

Tests should establish that:

- a substantial sequence contains only valid, unique `#RRGGBB` values;
- separate registries/allocators produce the same sequence;
- colours are not exhausted after the current eight assignments;
- advancing the allocator does not mutate or alter earlier results;
- rejected registry additions do not advance the allocator;
- participant removal does not make its colour reusable; and
- generated colours maintain at least 4.5:1 contrast against `#111827` over a
  substantial sequence.

Exact expected hex values should be asserted only where needed to preserve the
sequence as a compatibility contract.
