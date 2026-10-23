# Package design: approval display escaping

Design for [issue #75](https://github.com/roomscript/coderoom/issues/75).

Status: implemented.

## Problem

Before this change, `internal/ui/room/approval/view.go` passed the request
through `expandTabs` and `ansi.Wrap` in `layout`, and `optionLines` did the
same with the result of `formatOption`, which may be a backend-supplied label
or fallback option ID. Both paths preserved terminal escape sequences, so a
backend could move the cursor, erase text, or conceal command text in the
approval surface. Unicode format characters, such as bidi overrides and
zero-width characters, could also reorder or hide approval text (Trojan
Source, CVE-2021-42574). The issue named only terminal controls; this change
covers format characters as well.

`Model.Set` stores the original request and detached options. `SelectedOption`
returns the selected original ID; display formatting stays separate from
submission. The stored request carries the
`[→ alias]` prefix added in `internal/ui/update.go`; escaping covers it too.

## Display contract

The pure, package-local `escapeApprovalText` helper (`escape.go`) turns
untrusted text into display text with this policy:

| Input | Display |
|---|---|
| LF and TAB | Preserve; existing multiline layout and tab expansion handle them |
| CR | Literal `\r` |
| Backspace | Literal `\b` |
| ESC | Literal `\x1b` |
| Other C0 controls and DEL | Lowercase two-digit hex escape, e.g. `\x00`, `\x7f` |
| Decoded C1 controls (U+0080–U+009F) | Lowercase four-digit Unicode escape, e.g. `\u009b` |
| Unicode format characters (category `Cf`) | Lowercase `\uhhhh`, or `\Uhhhhhhhh` above U+FFFF, e.g. `\u202e`, `\U000e0041` |
| Other valid Unicode text | Preserve, including wide characters and combining marks |
| Invalid UTF-8 bytes | Escape each invalid byte as `\xhh` |

For example, valid UTF-8 bytes `C2 9B` encode U+009B and display as `\u009b`;
an invalid lone byte `9B` displays as `\x9b`.

The `Cf` category is one rule with no list to maintain: it covers bidi
embeddings, overrides, and isolates, zero-width characters, U+2060, U+00AD,
U+FEFF, and tag characters. Legitimate joiners are escaped too, so U+200C/U+200D
in Persian or Indic text and emoji sequences show as visible escapes. The
approval panel favors visible evidence over a clean display.

Escape control characters individually rather than recognizing whole ANSI
sequences. Thus ESC followed by `[2J` displays as `\x1b[2J`; an OSC string
retains its visible payload while ESC, BEL, and C1 terminators are escaped.
Incomplete or unfamiliar sequences cannot bypass the policy. CRLF displays
as a visible `\r` followed by a newline; do not normalize away the CR.

Literal backslashes remain unchanged. This preserves existing command text,
but an existing literal `\x1b` and an escaped ESC have the same display.
This is a terminal-execution boundary, not a reversible encoding.

Known gaps, for follow-up issues:

- Right-to-left letters (Hebrew, Arabic) still reorder adjacent text through
  the terminal's implicit bidi algorithm, without any control or format
  character. Fixing that needs per-run isolation or labeling and is a separate
  design problem.
- Some blank-looking characters fall outside `Cf` and display unchanged:
  Hangul fillers U+115F, U+1160, U+3164, U+FFA0, the Braille blank U+2800,
  the combining grapheme joiner U+034F, and variation selectors.

The helper runs before tab expansion, width measurement, wrapping, and paging:

1. `layout` escapes `m.ask`, then expands tabs and wraps.
2. `optionLines` escapes the result of `formatOption`, including fallback IDs,
   then expands tabs using the two-column prefix and wraps.
3. Selection prefixes, overflow hints, and any future application styling are
   added after escaping. Do not escape the completed styled panel.

`View`, `Height`, resize, and request paging share the same layout. Escaped
characters consume their visible width and may span wrapped rows. The existing
one-row/tiny-panel policy remains: options take precedence and overflow is
indicated; request paging requires available request rows.

## Ownership and scope

Keep original request text and option IDs as the sole stored values. Derive
escaped text during layout; add no sanitized cache or second options list.
`Ask`, `Options`, `SelectedOption`, selection movement, confirmation, and
cancellation retain their contracts. `Set` still resets selection and paging;
`Clear` still removes the presentation. There are no new asynchronous tasks,
locks, callbacks, lifecycle states, or cleanup obligations.

Keep the helper in the approval package: this is its only established consumer.
Do not change adapter, interpreter, or Session values, and do not claim coverage
of other terminal output surfaces.

Stripping ANSI is smaller but can silently join or remove meaningful command
text and does not explain hidden input. An ANSI allowlist requires a parser and
allows backend styling to compete with approval presentation. Visible character
escaping is the recommended approach because it preserves evidence with a
small, deterministic transform.

## Verification

`escape_test.go` covers:

1. The helper's display contract, table-driven: printable text, literal
   backslashes, Unicode, individual invalid bytes, bidi overrides and
   isolates, invisible and tag characters, joiners, and preserved
   right-to-left letters.
2. Rendering regressions for request text, custom labels, and fallback IDs:
   CR, backspace, CSI movement/erasure, SGR concealment/color, OSC hyperlink
   and title sequences, C1 introductions, incomplete escape sequences, a
   Trojan Source bidi override, and a zero-width space. The tests assert exact
   visible escapes in `approval.Model.View()` and no raw C0/C1 controls or
   format characters other than layout LF. This holds for today's unstyled
   output; if trusted styling is added, assert on the escaped text before
   styling instead of escaping the styled panel.
3. Escaped text in bounded panels: width/height limits, `Height` agreement,
   request paging to the tail, resize, and option navigation. Original
   request/options stay intact and confirmation returns the original selected
   option ID, including an ID containing controls.

Existing newline, tab-stop, wide-character, tiny-panel, replacement, and
cancellation tests continue to pass. The tests inspect rendered strings; other
terminal output surfaces remain unaudited. Risks to watch in future changes:
escaping after width calculation, or a new untrusted text path that bypasses
the helper.
