# Participant model and effort

Status: **draft**, for #71. This is the backend-neutral framework. Backend
details: `pkg-agent-claude-models.md` (Claude) and `pkg-agent-codex-models.md`
(Codex, to come).

---

## Participant

**Agreed** on 19 October 2026.

### Settings

`agent.Settings` groups the backend with its model and effort:

```go
type Settings struct {
    Backend string // the agent's Name(), such as "claude"
    Model   string // empty: no concrete value
    Effort  string // empty: no concrete value
}

// ModelLabel returns Model, or Backend + "-managed" when Model is empty.
func (s Settings) ModelLabel() string
// EffortLabel returns Effort, or Backend + "-managed" when Effort is empty.
func (s Settings) EffortLabel() string
```

An empty `Model` or `Effort` means the backend reported no concrete value:
unknown, not reported, uncertain after a failed change, or not applicable (a
Claude model without effort support). Settings do not distinguish these
cases, because all of them are displayed the same way.

The labels are the one display rule, used by invite messages, `/model` and
`/who` (#24). Each field falls back on its own, using the backend name, never
the alias. The `Label` suffix marks display text: `claude-managed` is not a
model, so raw values are read from the fields. Because `Settings` carries its
backend, events and records can carry settings alone and still render them.

### Settings in the view

`participant.View` replaces `Backend` with `Settings`. `participant` already
imports `agent`.

```go
type View struct {
    // ...existing fields, without Backend
    Settings agent.Settings
}
```

- `createAgent` sets `Settings{Backend: a.Name()}` where it sets `Backend`
  today, so a starting participant already shows its backend.
- At attach, the session takes `Model` and `Effort` from the agent. An agent
  without the capability, such as `echo`, keeps the backend only.
- A `/model` outcome replaces `Model` and `Effort` only. `Backend` always comes
  from `a.Name()`.
- `AgentStarting` keeps its own `Backend`. `/who` reads `Settings.Backend`.

The view carries no catalog. The catalog belongs to the adapter, and only
`/model` reads it, through the session.

### Status while `/model` runs

A new status, `changing-model`, reserves the participant for the whole
exchange, as `keepalive` does for maintenance:

```text
idle -> changing-model -> idle
changing-model -> crashed
```

- `BeginModelChange(now)` requires `idle`. The session additionally requires
  `StartupReady`, as it does for work.
- `FinishModelChange(now)` returns to `idle` when `ModelSelected` arrives,
  whether the change succeeded or failed. There is no timeout: a participant
  stuck in `changing-model` is ended with `/remove`.
- The session writes `Settings` under the same lock as `FinishModelChange`, so
  the status change that ends the exchange already carries the outcome.

Predicates, all derived from status:

| Predicate | `changing-model` | Why |
|---|---|---|
| `IsSendable`, `IsRoutable`, `IsReadyForWork` | false | no work starts during the exchange; staged sends wait, as for `keepalive` |
| `IsCancellable` | false | `/cancel` interrupts turns, and there is none |
| `IsRemovable` | true | `/remove` stops the process, which ends the exchange; its late result finds no participant and is dropped |

`PrepareForWork` rejects `changing-model` with `ErrWorkAlreadyStarted`, the
busy error, and `BeginKeepalive` rejects it because the participant is not
idle.

### Change delivery

No new `ChangeKind`. Settings change in exactly two places, and each coincides
with a status change that subscribers already receive:

- **Startup:** the session records the startup settings when it attaches the
  agent, so the `starting -> idle` change carries them.
- **`/model`:** `changing-model -> idle` carries the outcome.

### Lifetime

Settings belong to the participant instance. `/remove` discards them, and a
reinvite creates a new participant with the new process's startup settings.

### Status consumers

Statuses are matched explicitly in a few places, and each needs the new value:

- `ui/toolbox`: glyph and activity tier. Proposed glyph: `◑`, next to
  `preparing`'s `◐` and distinct from `keepalive`'s `◔`. Elapsed time is shown,
  as for other busy states.
- `interpreter/runtime/participant_updates.go`: `isPastStartup`.
- `pkg-participant.md` and `pkg-ui-toolbox-activity.md`: state lists.

---

## Agent

**Agreed** on 20 October 2026. `Settings` and its labels are described under
[Settings](#settings).

### Catalog entries

```go
// ModelOption is one catalog entry.
type ModelOption struct {
    Name    string   // what the user types: "opus", or "gpt-5.2"
    ID      string   // full model ID; equal to Name when Name is not an alias
    Efforts []string // supported efforts, backend-specific names; empty: none
}
```

A model matches an entry by `Name` or `ID`. Claude lists aliases (`opus`,
whose `ID` is `claude-opus-5-5`) next to full IDs. For entries without an
alias, and for Codex, `ID` equals `Name`.

### Capability

`ModelSelector` is optional, like `Keepaliver`. A backend without it, such as
`echo`, has no `/model`.

```go
type ModelSelector interface {
    // Settings returns the current settings, as last established. Valid
    // after Start; before that, only Backend is set. No I/O.
    Settings() Settings

    // Models starts a catalog read and returns immediately. An error means
    // nothing started. Otherwise one ModelsListed arrives on Read, unless
    // the process exits first. Accepted in any status after Start.
    Models() error

    // SelectModel starts a change and returns immediately. An error means
    // nothing started: effort not in opt.Efforts, a turn or another change in
    // progress, or a write failure. Otherwise exactly one ModelSelected
    // arrives on Read, unless the process exits first. An empty effort
    // selects the model only, sending no effort.
    SelectModel(opt ModelOption, effort string) error
}

// ModelsListed reports the outcome of a Models call (ModeSingle).
// Err is non-nil when the backend could not list its models.
type ModelsListed struct {
    Models []ModelOption
    Err    error
}

// ModelSelected reports the outcome of a SelectModel (ModeSingle).
// Settings is always set; fields the backend may not have applied are empty.
// Err is non-nil when the change did not fully apply.
type ModelSelected struct {
    Settings Settings
    Err      error
}
```

Every call that may need the backend works like `Send` and `KeepAlive`: it
starts the work, and the outcome arrives through `Read`, where the session's
reader handles it as it handles `KeepAlive`. The session needs no goroutine of
its own, and removal, crash and shutdown end the work through the paths that
already end `readLoop`. `Settings` stays synchronous: it is state the
handshake and `ModelSelected` already established.

### Contract

- **Listing.** `Models` is accepted while the participant is busy, since
  `/model <alias>` must work then, and takes no gate. A backend that cannot
  list during a turn answers from what it read at startup. Each call produces
  its own `ModelsListed`, and listings arrive in the order of the `Models`
  calls that started them, so callers can pair results with calls in order.
- **Validation.** The caller finds `opt` in a listing it has just received.
  `SelectModel` checks the effort against `opt.Efforts` before anything is
  sent, as a final guard.
- **Exclusivity.** `SelectModel` refuses to start while a turn, a pending
  prompt or another change exists. While a change runs, `Send`, `SendNotice`
  and `KeepAlive` return `ErrTurnInProgress`. The adapter keeps this gate even
  though the session serializes as well, as it does for prompts.
- **Read stream.** Listing and changes put no turn messages on `Read`: no
  stream fragments, no anchor flushes, no `KeepAlive`. Only `ModelsListed`,
  `ModelSelected`, and possibly `Log` lines. `ModelsListed` may arrive during
  a turn; the session's turn bookkeeping ignores both.
- **Outcome.** `ModelSelected.Settings` is the state after the exchange: fields
  the backend established are concrete, fields it may or may not have changed
  are empty. A failure does not imply rollback. After `ModelSelected` is read,
  `Settings()` returns the same value.
- **Distinct from keepalive.** `ModelSelected` carries success or failure;
  `KeepAlive` cannot, so it is never used to confirm a change.

### Per backend

| | Claude | Codex |
|---|---|---|
| `Models` | answered at once from the `initialize` catalog: `value` to `Name`, `resolvedModel` to `ID`, `supportedEffortLevels` to `Efforts` | `model/list`: `model` to `Name` and `ID`, `supportedReasoningEfforts` to `Efforts`. Whether it works during a turn is to be verified |
| Startup settings | `get_settings` `applied` | `thread/start` response: `model`, `reasoningEffort` |
| `SelectModel` | `set_model`, then `apply_flag_settings` when an effort is given, then `get_settings` | an acknowledgement turn with `model` (and `effort`) overrides |

Details: `pkg-agent-claude-models.md` and `pkg-agent-codex-models.md`.

---

## Session

**Agreed** on 20 October 2026.

Session is the authority for availability, serialization and the participant
snapshot. Adapters are reached only through `ModelSelector`, never under the
session lock: the session copies the agent handle under the lock and calls it
after releasing it.

A change validates against a fresh listing before anything is written. This
matters most for Codex, whose `turn/start` overrides persist for later turns:
an invalid model could break the thread, not just fail one change.

### ListModelsCommand

`/model <alias>`. It resolves the participant and its `ModelSelector`,
appends a read request to the participant's listing queue under the lock (see
[Reading the outcomes](#reading-the-outcomes)), and calls `Models()` after
unlocking. If `Models()` returns an error, the entry is removed and the
command fails. It is accepted in any status after startup and does not
reserve the participant. Agents without the
capability are rejected with "model selection not supported by <backend>".

### ChangeModelCommand

```go
type ChangeModelCommand struct {
    Alias  participant.Alias
    Model  string // Name or ID
    Effort string // empty: model only
}
```

`execute` admits the change and starts it; it does not wait for the outcome,
as `InviteCommand` does not wait for startup:

1. Resolve the participant and its `ModelSelector`. Unknown participants and
   agents without the capability are rejected.
2. Under the session lock: require `StartupReady` and `idle`, then
   `BeginModelChange`, record the pending request (`Model`, `Effort`) in the
   participant's runtime, and append the change to its listing queue. This is
   the same lock and status check that admits work and keepalive, so `/model`
   is serialized against both, and the participant stays reserved from
   listing to outcome.
3. After unlocking: `Models()`. If it returns an error, remove the queue entry
   and finish the change with it.

Rejections name the reason: not ready (starting), busy (working, preparing,
keepalive, or `changing-model`), crashed. No change waits for later, and no
work is interrupted.

### Reading the outcomes

`readLoop` handles both messages as it handles `agent.KeepAlive`.

**`ModelsListed`.** Each participant's runtime keeps a FIFO queue of listing
owners, one entry per `Models` call that started: a read request, or the
pending change. Listings arrive in call order, so each `ModelsListed` answers
the owner at the head of the queue, which is then removed. Owners are queued
before `Models` is called, because a listing may arrive before the call
returns; commands run serially, so queue order is call order. A listing never
answers a request it was not started for, so a failed read cannot abort a
change, nor a change's listing be shown as a read.

- **The owner is the pending change:**
  - `Err` set: finish the change with it.
  - No entry matches `Model` by `Name` or `ID`, or `Effort` is not in the
    entry's `Efforts`: finish with an error that lists the valid values.
  - Otherwise `SelectModel(opt, Effort)`, called after releasing the lock. If
    it returns an error, finish with it.
- **The owner is a read request:** publish `ModelsListed` with the
  participant's current `Settings` and status.
- **The queue is empty:** drop the listing.

**`ModelSelected`.** If the participant is still in `changing-model`, replace
`Settings.Model` and `Settings.Effort` with the outcome, keep
`Settings.Backend`, and finish the change.

**Finishing a change** changes state under the lock: clear the pending
request, update `Settings` when the finish comes from `ModelSelected`, and
`FinishModelChange`, so the status change to `idle` carries the settings. The
outcome is captured there. After unlocking, the session notifies the status
change and `ModelChangeFinished`: `notify` takes the session lock itself, and
observers never run under it. A finish that is not caused by `ModelSelected`
leaves `Settings` untouched.

```go
// ModelsListed reports a catalog requested by /model <alias>.
type ModelsListed struct {
    Alias    participant.Alias
    Settings agent.Settings // the participant's settings when listed
    Status   participant.Status
    Models   []agent.ModelOption
    Err      error
}

// ModelChangeFinished reports the outcome of a /model change. Settings are
// the participant's settings afterwards; Err is non-nil when the change did
// not fully apply.
type ModelChangeFinished struct {
    Alias    participant.Alias
    Settings agent.Settings
    Err      error
}
```

Messages for a participant that is gone, or not in the expected state, are
dropped. This covers removal with a late result. Removal, crash and shutdown
otherwise end the exchange through the paths that already end `readLoop`,
with no event.

### Startup

- `createAgent` sets `Settings{Backend: a.Name()}`.
- `attachParticipant` copies `Settings()` from a `ModelSelector`, under the
  same lock that attaches the agent, keeping `Backend`. The handle is called
  before taking the lock.
- `AgentReady` carries the participant's `Settings`, so the joined message
  renders them without reading the session.

---

## `/model`

**Agreed** on 20 October 2026.

### Grammar

```text
/model <alias>                  # show settings and the catalog
/model <alias> <model>          # select the model only
/model <alias> <model> <effort> # select both
```

Arguments are whitespace-separated, as for `/handoff`. A missing alias or
extra arguments are usage errors. `model` becomes a reserved command name.

- `promptlang`: a `Model` statement `{Alias, Model, Effort Located[string]}`,
  with empty `Model` and `Effort` for the read form.
- `internal/interpreter/std`: `ModelCommand`. Usage
  `/model <alias> [<model> [<effort>]]`, description "show or change an agent's
  model and effort". The read form runs `session.ListModelsCommand` and the
  mutation forms `session.ChangeModelCommand`, both through the unrouted invocation,
  like `/cancel`. The command completes on admission; its outcome arrives as
  a session event.

### Presentation

All output is system records in the canonical transcript, rendered by the room
from session events, so they show in the TUI and in non-interactive clients
alike. Settings use the labels.

Read form, from `ModelsListed`:

```text
[ada · claude · model: claude-opus-5-5 · effort: high]
models:
  default (claude-opus-5-5): low, medium, high, xhigh, max
  opus (claude-opus-5-5): low, medium, high, xhigh, max
  claude-haiku-4-5-20251001: no effort
```

- An entry shows `Name (ID)` when they differ, and `Name` otherwise.
- While the participant is in `changing-model`, the first line ends with
  `· changing model`: the settings shown are those before the change.
- A failed listing shows `[ada model list failed: <err>]`.

Outcome, from `ModelChangeFinished`:

```text
[ada model changed · model: claude-sonnet-5-5 · effort: high]
[ada model change failed: unknown model "sonet" (valid: default, opus, …) · model: claude-opus-5-5 · effort: high]
```

A successful change shows only its outcome, not the catalog. A failed
validation names the valid values in its error. Admission errors (busy, not
ready, not supported) are command errors, shown like other failed commands.

Joined message, from `AgentReady`:

```text
[ada joined · model: claude-opus-5-5 · effort: medium]
```

#25 extends this line with role, backend and resume ID.
