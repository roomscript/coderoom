# Claude: participant model and effort

Status: **draft**, for #71. This covers the Claude side only. The
backend-neutral contract (terms, display, `/model`, availability, ownership)
goes in `participant-models.md`, and Codex in `pkg-agent-codex-models.md`.
Evidence: `docs/playground/poc-claude-stream-json.md`, E11 to E13, run on
Claude Code 2.1.303.

---

## Protocol

Everything goes through control requests on the existing stream-json
connection. None of them starts a model turn, uses quota, or adds to the
conversation.

| Need | Request | Answer |
|---|---|---|
| Catalog | `initialize` (already sent by `Start`) | `models[]`: `value`, `resolvedModel`, `displayName`, `description`, and for models with effort `supportsEffort: true` and `supportedEffortLevels` |
| Current settings | `get_settings` | `applied.model` (full ID) and `applied.effort` (`null` when the model has no effort) |
| Change model | `set_model {model}` | `success`, or `error` such as `Model 'sonet' not found` |
| Change effort | `apply_flag_settings {settings: {effortLevel}}` | always `success` |
| Clear effort | `apply_flag_settings {settings: {effortLevel: null}}` | `success` |

Observed behaviour that shapes the design:

- **`applied` is the truth.** E13 checked it against the Messages API request:
  `output_config.effort` always matched `applied.effort`. `effective` reflects
  only the settings files and does not follow runtime changes.
- **The conversation survives `set_model`**, and the next turn runs on the new
  model (E12).
- **Invalid efforts are ignored silently.** `apply_flag_settings` accepts
  `bogus`, keeps the previous effort and still answers `success`. The adapter
  must validate efforts against the catalog. Invalid models are rejected by
  Claude itself.
- **An effort selection outlives model changes.** It is carried to the next
  model. A model without effort support reports `null`, and the selection comes
  back once a model with effort is selected again.
- **Clearing the effort does not restore the configured one.** After
  `effortLevel: null`, `applied.effort` is `medium` even when the user's CLI
  settings say `low`. This held both for project settings and for `--settings`.
- **There is no "back to the configured model".** `set_model` with `null` is a
  no-op, and `set_model default` selects the account default, not the
  configured model.
- **`set_model` echoes a replayed user message.** Before its `success`, Claude
  writes `isReplay: true` with content `<local-command-stdout>Set model to
  ...</local-command-stdout>` and a fresh uuid, and no turn follows. The
  client ignores such replays unless their UUID matches the pending prompt;
  otherwise they would count as a self-started turn that never ends. Matching
  UUIDs retain ownership even when a user pastes local-command markup.
- **Nothing is persisted.** The settings files are not touched, and
  `~/.claude.json` only gains caches and usage statistics, as on every run.

Two corrections to the issue text: the `initialize` response carries the
catalog but no current model, and the effort is known at startup even when
none is configured. The `default` catalog entry's `resolvedModel` is only the
account default, not necessarily the session's model.

---

## Startup

`Start` sends `get_settings` right after `initialize`. Both answers are
recorded before `Start` returns:

- **Catalog:** the `models[]` list as received.
- **Settings:** `applied.model` and `applied.effort`. Both are known values for
  the invite message, since they are what the session runs with. A `null`
  effort (a model without effort support) is reported as unknown, so it shows
  as `claude-managed`.

If `get_settings` fails, `Start` still succeeds and both fields are unknown,
which is displayed as `claude-managed`. A participant should not fail to start
over a metadata read.

No `--model` or `--effort` flags are passed. `WithModel` stays for tests only,
as today: the registry does not use it.

---

## Listing and validation

`Models()` answers from the catalog read at startup: it pushes `ModelsListed`
on the read queue at once, in any status, with no request to Claude. No control
request that re-reads the catalog is known (not verified). Each entry has `Name` from `value`, `ID` from
`resolvedModel`, and `Efforts` from `supportedEffortLevels` (empty without
`supportsEffort`).

The session validates a change against that listing before anything is sent
(see `participant-models.md`). A model matches by `Name` or `ID`, which accepts
aliases (`opus`) and the full IDs that `applied.model` reports
(`claude-opus-5-5`). Claude would also accept valid IDs missing from the
catalog, but #71 asks for catalog validation. `SelectModel` checks the effort
against `opt.Efforts` once more, since Claude itself ignores invalid efforts.

---

## Changing settings

One `/model` mutation is one exchange, started by `SelectModel` and run while
the client admits no prompt. Its outcome is one `ModelSelected` on `Read`:

1. `set_model <opt.ID>`. If it fails, skip step 2.
2. If an effort was given: `apply_flag_settings {effortLevel: <effort>}`.
3. `get_settings`, always, including after a failure in step 1 or 2.
   `ModelSelected` reports `applied` as the new current settings. If the readback
   fails, both fields become unknown: the earlier steps may or may not have
   applied.

The ID is sent, not the name, as agreed on 20 October 2026. Aliases can share
an ID (`default` and `opus` both resolve to `claude-opus-5-5`), and Claude
picks the first catalog entry that matches, so what is sent is what the
readback reports. The cost: selecting `default` pins the model it resolves to
now, instead of following the account default.

A model-only change sends no effort. The effort is whatever Claude runs with
afterwards, as read back: today the previous effort carries over (E11), or
`claude-managed` for a model without effort support. The user selects an
effort explicitly if they want another one. coderoom does not reset or restore
the effort, so the adapter keeps no record of earlier selections. This
replaces #71's rule that a model-only change resets an earlier explicit
effort.

### Exclusivity

The client already admits one prompt at a time (`admitPrompt`). The exchange
uses the same gate:

- It starts only when no turn is running and no prompt is pending, so
  `set_model` never lands mid-turn and a self-started turn is not overtaken.
- While it runs, `Send` and `SendNotice` fail with `ErrTurnInProgress`.
- It ends only when its last answer arrives, or when the process exits
  (including through `Stop`). Until then the gate stays closed.

The session serializes `/model` against turns as well (see
`participant-models.md`). The adapter gate keeps the client correct on its own,
as it does for prompts.

### Control responses

Today only the `initialize` response is matched (`initRequestID`). The exchange
needs a general mechanism: requests register their `request_id`, and the
stdout reader routes the matching `control_response`, with its body, to the
request's handler. The exchange advances as each answer arrives and pushes
`ModelSelected` at the end, so no caller blocks. `initialize` moves onto the
same mechanism. Responses to unknown IDs, such as `interrupt`, stay ignored.

There is no timeout. Giving up on an outstanding request would reopen the gate
while Claude may still apply it, so a later turn could run on a model nobody
selected. Prompts have no timeout either. Control answers took milliseconds in
E11; if Claude stops answering, the participant stays in `changing-model`
until the user runs `/remove`, which stops the process and so ends the
exchange. Process exit ends the exchange without `ModelSelected`, as the
contract allows, so no answer can arrive after the gate reopens.

---

## Removal and reinvite

Settings live in the `claude` process. `/remove` stops it, and a reinvite
starts a new one from the CLI-managed settings. Nothing carries over.

---

## Display

Agreed on 19 October 2026:

- **Whatever the readback says is shown.** coderoom does not correct or
  restore values, for example a cleared effort falling back to `medium` rather
  than the configured one.
- **A `null` effort shows as `claude-managed`.** It is true, and keeps one
  rule for "no concrete value".
- **Names.** The current model is the full ID from `applied.model`. `/model`
  lists catalog entries as `value (resolvedModel)` and accepts either name.
