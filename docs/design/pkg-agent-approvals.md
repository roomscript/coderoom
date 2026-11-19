# Design: Agent-level approvals (Codex app-server)

Codex app-server can ask the *client* (coderoom) to approve or deny actions
mid-turn (exec, file changes, permissions). Codex models these as **JSON-RPC
requests** emitted on stdout (they include an `id`), and it expects the client
to respond with a **JSON-RPC response** that references the same `id`.

This document defines a minimal, agent-level approval interface focused on
replicating the Codex CLI workflow in v1, while keeping UI components
presentation-only and avoiding cross-boundary state leaks.

- All prompts are handled in the shared room (no rooms/private contexts yet).
- The Codex client owns protocol details and only asks the application for a
  high-level decision.

## Goals

- Handle Codex approval requests for turns started via `turn/start`.
- Keep protocol details contained inside `internal/agent/codex`.
- Provide one generic approval callback interface that the session can
  implement without depending on Codex request schemas.
- Preserve ordering: the user should see approvals in the order Codex requested
  them.
- Keep approval correlation (IDs / pending state) out of UI component state.
- Process approvals FIFO so the UI handles one prompt at a time.

## Non-goals (v1)

- Rooms/private contexts for approvals.
- Rich “apply suggested policy amendment” UX (execpolicy/network policy).
- Multi-approval selection UI (accept/decline all).
- Surfacing approval requests as `agent.Message` values (we may add this later).

## Codex protocol surface (v2)

Codex emits server-initiated JSON-RPC requests with these methods:

- `item/commandExecution/requestApproval`
- `item/fileChange/requestApproval`
- `item/permissions/requestApproval`

Each request includes a JSON-RPC `id`. The client must respond with:

```json
{"id": <same-id>, "result": { ...method-specific response... }}
```

## API design

`internal/agent` exposes an optional approval listener.

### Listener

```go
type ApprovalListener interface {
    Decide(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}
```

The listener:

- receives the Codex client's lifecycle context (`ctx`), which is cancelled when
  the agent stops or is shut down — implementations must respect it to avoid
  blocking indefinitely,
- receives structured presentation inputs plus a small set of normalized decision
  options,
- returns a normalized decision (or an error),
- does **not** need to carry protocol identifiers in the response (Codex client
  already has them from parsing the request).

Important layering note:

- `internal/ui/room/approval` is presentation-only (render + key navigation).
  Panel sizing and request scrolling follow
  [approval panel sizing](pkg-ui-input.md#approval-panel-sizing).
- Approval transport/correlation (what request is pending, which decision
  completes it) must live outside the UI component layer.

### Request / decision types

```go
type ApprovalKind string

const (
    ApprovalCommandExecution ApprovalKind = "commandExecution"
    ApprovalFileChange       ApprovalKind = "fileChange"
    ApprovalPermissions      ApprovalKind = "permissions"
)

type ApprovalRequest struct {
    Kind         ApprovalKind
    Details      ApprovalDetails
    Presentation ApprovalPresentation

    // Options is the set of normalized decisions supported by this request in v1.
    // Example: []ApprovalOption{OptionAccept, OptionDecline, OptionCancel}
    Options []ApprovalOption
}

type ApprovalPresentation struct {
    Summary     string
    Explanation string
    Evidence    string
}

type ApprovalOption string

const (
    OptionAccept           ApprovalOption = "accept"
    OptionAcceptForSession ApprovalOption = "acceptForSession"
    OptionDecline          ApprovalOption = "decline"
    OptionCancel           ApprovalOption = "cancel"
)

type ApprovalDecision struct {
    Choice ApprovalOption
}
```

Notes:

- `OptionCancel` means “deny and interrupt the current turn” in the Codex
  protocol (supported for command/file approvals). It is still exposed as a
  normalized option in v1.
- Permissions approvals do not have an explicit “cancel” response in the schema.
  In v1, the listener may still choose `OptionDecline`; the Codex client will
  translate that into an “empty grant” response (no additional permissions).
- `OptionAcceptForSession` is supported for command/file approvals (Codex v2).

### Default behavior (no listener)

If no listener is configured:

- Codex client responds with a safe default: `decline` (or empty grant for
  permissions).
- Codex client emits an `agent.Message{Kind: agent.MessageLog, Text: ...}`
  describing the auto-decline (so the user understands why the agent is blocked).

This makes coderoom safe-by-default even before the UI supports approvals.

## Internal algorithm (Codex client)

To avoid blocking the stdout scan goroutine, approvals are processed by a
dedicated worker loop inside the Codex client.

High-level flow:

1. Stdout scan reads a JSON-RPC message line.
2. If it is a server request (`id` present) and method is an approval request:
   - enqueue an internal `approvalJob{rpcID, method, params}` onto a **buffered**
     channel.
   - continue scanning stdout.
3. A single `approvalLoop` goroutine:
   - dequeues jobs in order,
   - normalizes `ApprovalRequest{Details, Presentation, Options}` for the listener,
   - calls `listener.Decide(ctx, req)` (may block; unblocks when `ctx` is cancelled),
   - translates `ApprovalDecision` to the correct schema-specific `result`,
   - writes the JSON-RPC response (`{"id":..., "result":...}`) to stdin.

This ensures:

- scanStdout never blocks on user input,
- approvals are processed sequentially,
- stdin writes remain serialized.

## Session-managed approvals (recommended)

V1 can keep the shared-room UX while preventing approval state from leaking into
the root UI model by managing approvals inside the Session Controller.

### High-level flow

1. Codex client receives a JSON-RPC approval request and calls
   `ApprovalListener.Decide(ctx, req)`.
2. The listener (implemented by session) allocates an internal `approvalID`,
   stores a pending entry keyed by `approvalID`, and publishes a session event:

   - `ApprovalRequested` (payload includes `ID`, agent alias, and
     `ApprovalRequest`).

   This publish must be concurrency-safe and must not call `Session.Execute`
   (which is single-goroutine by contract).
3. Session only publishes the head of the queue as `ApprovalRequested`.
4. UI observes `ApprovalRequested` and displays that one approval prompt in
   the shared room via `room.ShowApproval(req)`.
5. When the user confirms an option, UI issues a session command:

   - `ResolveApprovalCommand{Choice}`

   The command may also carry `ApprovalID` defensively, but the queue owner is
   session, not the UI.
6. Session resolves the active entry, unblocks the listener's `Decide` call,
   and, if another approval is queued, immediately publishes the next
   `ApprovalRequested` event.

### Why session (not UI) owns correlation

- Approvals are a blocking request/response handshake. They require correlation
  between a specific pending request and a specific user decision.
- UI components should remain presentation-only; keeping correlation state and
  queue ownership in session avoids leaking approval lifecycle concepts into the
  root UI model.

### Listener wiring

Codex client accepts an optional approval listener via a constructor option:

```go
func WithApprovalListener(l agent.ApprovalListener) Option
```

The Session's `AgentFactory` is responsible for wiring this listener at agent
construction time (e.g., when `/invite <alias>` starts an agent). UI should not
wire backend options.

### Serialization of approval responses

Approval responses share the same stdin and ordering constraints as other RPC
writes (`turn/start`, `turn/interrupt`). The implementation must serialize all
stdin writes behind a single lock (the same lock used by `writeRequest`), so
that approval responses cannot interleave with other JSON-RPC messages.

### Interrupt and pending approvals

If the user calls `/cancel` while an approval request is pending:

- The interrupt request may reach Codex before or after the approval response,
  depending on scheduling.
- V1 behavior: coderoom does not attempt to reorder or auto-drain approvals. The
  listener should still answer approvals if asked.
- On shutdown (client `Stop()` / context cancellation), coderoom intentionally
  drops any buffered approvals rather than blocking shutdown waiting for human
  input. Approvals are only meaningful while the Codex process is alive to
  receive the JSON-RPC response.

### Presentation inputs

Adapters preserve full supplied reasons in Presentation.Explanation and normalize
additional request evidence separately. Command/file summaries and operation
evidence are derived in room. Codex grantRoot is additional file scope evidence;
permission summaries identify reported filesystem/network scope. Diagnostics use
agent.ApprovalPrompt with generic kind labels plus full command/cwd or affected
file paths from the operation and any supplemental command/file evidence. Thus
missing-listener, listener-error and invalid-choice logs retain the requested
action even when no manual history record is created.
Adapters never shorten, escape or style these inputs. See the shared contract below.

### Choice validation

The Codex client validates that the listener's returned `Choice` is present in
`req.Options`. If not, it falls back to the safe default for that request kind.

## Error handling

- If the listener returns an error, codex client responds with the safe default
  (decline/empty grant) and logs the error as an
  `agent.Message{Kind: agent.MessageLog, Text: ...}`.
- If stdin write fails while responding, the client treats this as fatal and the
  agent will crash (consistent with other write failures).

## Open questions

- Should approvals be surfaced as `agent.Message` (so the session/UI owns the full
  UX), rather than being handled inside the Codex client?
  - Decision: no. The approval listener is responsible for surfacing the prompt
  to the UI. We keep approval transport out of `agent.Message` in v1.
- Should we support `acceptForSession` in v1 (for command/file approvals)?
  - Decision: yes. If the backend supports it (Codex does), the listener may
    return it.

## Shared permission presentation

All approval kinds use `ApprovalRequest.Presentation` for summary, explanation
and supplemental evidence. Explanation is supplied context stored once; Evidence
excludes extracted explanations and supplements common operation data.
Codex labels reported filesystem
and network grants, preserves the supplied reason verbatim, and retains complete
scope JSON including unknown fields. Missing cwd is unknown; missing scope is
explicitly unavailable. Claude labels the exact tool or `Plan approval`, retaining
complete inputs (including Read inputs and unknown fields), or the full plan plus
additional fields. Plans supply no invented explanation. Raw protocol metadata
is never a display fallback. These fields do not affect Operation or policy.

Room creates one retained approval record on manual activation and updates it
on authoritative settlement by ID and alias. An empty permission summary displays
`Permission request`, with an unavailable-evidence notice
when necessary. Queued requests have no record until activation; accepted choices
and withdrawals follow the existing Session FIFO and cancellation authority.

Room's pure DeriveApprovalPresentation derives summaries and evidence from common
operations, appends supplemental evidence and keeps the supplied explanation
separate. History composes the heading, explanation and evidence; interpreter
projects the same detached fields and paths. Evidence alone is not a complete
request document. No consumer assumes evidence already contains an explanation.
Raw backend data and operations remain policy/identity inputs; presentation does
not affect policy, response payloads or Session diagnostic identity. See
[approval presentation contract](approval-presentation-contract.md).
