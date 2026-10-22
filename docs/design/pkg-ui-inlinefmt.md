# Package design: inline text formatting in agent output

This document specifies how the TUI renders inline formatting in agent output
records. The goal is to make LLM output more readable by applying lightweight
visual treatment to the emphasis and quotation conventions that models naturally
produce.

Status: implemented (Phase 1).

## Goals

- Improve readability of agent output without introducing a full markdown
  renderer.
- Render bold and italic emphasis in the participant's color.
- Render quoted spans in the participant's color.
- Degrade gracefully when delimiters are unmatched (e.g. mid-stream).

## Non-goals

- Full markdown rendering (headings, lists, code blocks, tables).
- Nested spans (e.g. bold inside a quote). Phase 1 treats spans as flat.
- Applying formatting to user input echoes or system records.

## Scope

Formatting is applied only to **agent output records** at render time.

## Formatting rules

All styles are applied relative to the participant's assigned color. Delimiters
are either stripped (for markdown-style emphasis) or kept and styled (for
quotation marks).

When patterns overlap (e.g. `**"quoted"**`), Phase 1 does not attempt nested
styling. The renderer should apply **only the first matching span** according to
the “Rendering order” priority, and treat any delimiters inside that span as
plain text.

Algorithm (Phase 1):

- Scan left-to-right to find the next opening delimiter position.
- At that position, try delimiter types in “Rendering order” priority.
- The first delimiter type that has a matching closer in the remaining text
  wins; render that span as a single styled unit (no nested spans inside).
  - For **bold** and **code** spans, the delimiters are omitted from the
    rendered output; only the inner text is styled.
  - For **italic** and **quote** spans, the delimiters remain visible and are
    styled along with the inner text.
- If no matching closer exists for any delimiter type at that position, emit the
  opening characters as plain text and continue scanning after them.

### Bold

- Pattern: `**text**`
- Style: bold + participant color
- Delimiters: stripped

### Italic

- Pattern: `*text*`
- Style: italic + participant color
- Delimiters: kept and styled

### Code span

- Pattern: `` `text` `` or ``` ```text``` ```
- Style: participant color
- Delimiters: stripped
- Note: when both could match at the same position, ``` ```...``` ``` takes
  priority over `` `...` ``.

### Typographic double quotes

- Pattern: `“text”` (U+201C LEFT DOUBLE QUOTATION MARK, U+201D RIGHT)
- Style: participant color
- Delimiters: kept and styled

### Typographic single quotes

- Pattern: `‘text’` (U+2018 LEFT SINGLE QUOTATION MARK, U+2019 RIGHT)
- Style: participant color
- Delimiters: kept and styled

### Guillemets

- Pattern: `«text»` (U+00AB, U+00BB)
- Style: participant color
- Delimiters: kept and styled

### Single guillemets

- Pattern: `‹text›` (U+2039, U+203A)
- Style: participant color
- Delimiters: kept and styled

### ASCII double quotes

- Pattern: `"text"` (U+0022)
- Style: participant color
- Delimiters: kept and styled
- Constraint: the content must not itself contain `"` (balanced pair only, no
  nesting).

### ASCII single quotes

ASCII `'` (U+0027) doubles as an apostrophe and cannot be matched with a simple
balanced-pair rule. A quotation is recognised only when the opening and closing
`'` sit at a token boundary.

**Rule:**

- The opening `'` must be immediately preceded by whitespace or an opening
  bracket (`(`, `[`, `{`), or be at the start of the string.
- The closing `'` must be immediately followed by whitespace, sentence
  punctuation (`,` `.` `:` `;` `!` `?`), a closing bracket (`)`, `]`, `}`), or
  be at the end of the string.
- The content between the quotes must not itself contain `'`.

**Reference pattern:**

```
(?:^|[\s({\[])'([^']+)'(?:[\s,.:;!?)\]}]|$)
```

The boundary characters (leading whitespace/bracket and trailing
whitespace/punctuation) are **consumed** by the match and must be reproduced
literally in the styled output; only the `'…'` span itself is coloured.

Note: quoted text may contain apostrophes (e.g. `'it won't work'`) as long as
the closing quote satisfies the boundary rule.

**Examples:**

| Input | Matches? | Reason |
|---|---|---|
| `the 'quick' fox` | yes | spaces on both sides |
| `('quoted')` | yes | `(` before, `)` after |
| `it's fine` | no | `'` mid-word, no boundary before |
| `don't worry` | no | same |
| `end of 'sentence'.` | yes | period follows closing `'` |
| `'leading` | no | no matching closing `'` |

## Rendering order

Spans are scanned left to right in one pass. The following priority order
resolves ambiguous openers:

1. Code span (``` ``` or `` ` ``)
2. Bold (`**`)
3. Italic (`*`)
4. ASCII double quote (`"`)
5. ASCII single quote (`'`, boundary rule)
6. Paired quote delimiters (curly quotes and guillemets; no ambiguity between them)

Note: bold is evaluated before italic so that `**text**` is not misinterpreted
as two adjacent italic markers.

## Mid-stream handling

While a record is still streaming, the closing delimiter of an open span may not
have arrived yet. The renderer must handle this defensively:

- If a recognised opening delimiter has no matching closing delimiter in the
  current text, render the opening characters as plain text and do not apply any
  style.
- Re-render the record in full on each delta; do not cache partial span state.

Example:

- Delta 1: `Here is **bold`
  - Renders with no emphasis applied (opening delimiter is treated as plain text).
- Delta 2: `Here is **bold** now`
  - Re-renders and applies bold styling to `bold` (delimiters omitted).

## Implementation notes

- Use `lipgloss` style composition to combine bold/italic with the participant
  color: `lipgloss.NewStyle().Bold(true).Foreground(color)`.
- Glyph width for non-ASCII quote characters should be computed with
  `ansi.StringWidth` if alignment matters.
- The ASCII single-quote pattern requires the match to include boundary
  characters. The replacement must re-emit them outside the styled span.

Known risk / constraint:

- Lip Gloss pads multiline `Render` output to a rectangle. Style each logical
  line separately using `inlinefmt.RenderLines`; never apply `Render` to an
  inline fragment that contains newlines. Preserve newline separators and
  original spaces rather than trimming the result.
- Wrapping must be ANSI-aware. If wrapping is performed on strings that contain
  embedded ANSI escape sequences without accounting for escape sequences and
  display width, styles may bleed or wrap at incorrect columns. Implementations
  should verify how the current record renderer/wrapper handles ANSI, and adjust
  the pipeline accordingly before enabling this feature.
- The record renderer owns body indentation. Its unwrapped transcript path
  prefixes every logical line, including blank lines, while preserving source
  spaces. Inline formatting adds no block padding. Wrapped and unwrapped paths
  share the continuation-prefix logic.

Implementation ordering:

- Apply inline styling before any width-based wrapping, since the span renderer
  needs to see the original delimiters in the unwrapped text. If wrapping is
  performed after styling, ensure it is ANSI-aware (so it does not break escape
  sequences).
