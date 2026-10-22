# Package design: internal/agent and internal/agent/codex

This document covers two packages designed together:

- **`internal/agent`** — the `Agent` interface, shared types (`Message`), and the `SendAndWait` free function.
- **`internal/agent/codex`** — the Codex implementation of `Agent`: a thin process wrapper around the Codex app-server.

---

## internal/agent

### Agent interface

```go
type Agent interface {
    Start() error
    Send(prompt string) (StreamID, error)
    SendNotice(prompt string) (StreamID, error)
    Read() (Message, error)
    Interrupt() error
    Stop() error
}
```

Errors — both IO errors and turn failures — are returned as the `error` return value of `Read()`. There is one place to check for errors.

`Message` and all content types (`Output`, `Reasoning`, `Command`, `FileChangeSet`, `Log`, etc.) are defined in `internal/agent`. See [`pkg-agent-messages.md`](pkg-agent-messages.md) for the full message model, streaming lifecycle, and accumulation semantics.

### SendAndWait

`SendAndWait` is a free function that works for any `Agent` implementation:

```go
func SendAndWait(a Agent, prompt string) (string, error)
```

It calls `Send` then accumulates `Output` stream messages until the turn-level `ModeFlush`, returning the full text response. `Log` messages are discarded. `SendAndWait` is a convenience for callers that only need the final text response and do not need to observe intermediate tool activity.

`Send` + `Read` are the low-level primitives for callers that need to process output as it streams, including `MessageLog` messages.

`Send` and `SendNotice` both return the turn anchor `StreamID`. Callers that
need full lifecycle control track that anchor and treat its flush as the
authoritative turn-end signal. Convenience helpers such as `SendAndWait` may
ignore the returned anchor when they consume the full turn inline.

---

## internal/agent/codex

### What this package is

A thin process wrapper around the Codex app-server. It starts the process, speaks JSON-RPC 2.0 over stdio, and implements the `Agent` interface.

It is **not** responsible for sequencing turns across multiple agents, routing messages, or any session-level logic. That belongs in the session and router layers.

### API model

The API follows the same model as the `http` package: calls are blocking. If the caller needs concurrency, it adds a goroutine at the call site. The package owns two persistent goroutines after `Start()` returns: a stderr reader that captures diagnostic lines and queues them for `Read()`, and a short-lived one in `Stop()` that races graceful exit against a timeout. No other goroutines are introduced during steady-state operation.

| Method | Behaviour |
|---|---|
| `Start()` | Launches the process; blocks until initialize + thread/start handshakes complete, including any configured thread-scoped startup instructions |
| `Send(prompt)` | Writes the turn/start request to stdin and returns immediately — no stdout read |
| `Read()` | Blocks until a meaningful message is ready; translates Codex notifications into `agent.Message` |
| `Stop()` | Closes stdin and waits for the process to exit; kills if it hangs |

`Send` is a pure write. It does not read stdout.

When startup instructions are configured, `Start()` is also responsible for
delivering them during `thread/start`. In Version 1, the intended mapping is
the synthesized participant system prompt -> Codex `developerInstructions`.
This keeps participant role/identity setup out of the visible conversation
history.

`Read()` blocks until it can return a meaningful message — either a stdout-derived notification (`delta`, `done`) or a queued stderr line (`log`). Unknown or unrecognised notifications are discarded; the observer records them.

Visible output is item-scoped, not turn-scoped: each `item/agentMessage/delta`
opens or extends a transcript stream keyed by `turnId + itemId`. When
`turn/completed` arrives, the adapter inspects `turn.items[]` and emits one
`Output + ModeFlush` per completed `agentMessage` item, using that same stream
ID.

The Codex-specific mapping is:

| Source | Message(s) |
|---|---|
| `item/agentMessage/delta` | `{ID: "codex:output:<turnId>:<itemId>", Mode: ModeStream, Content: Output{Text: "..."}}` |
| `item/reasoning/delta` | `{ID: "codex:reasoning:<itemId>", Mode: ModeStream, Content: Reasoning{Text: "..."}}` |
| `item/commandExecution/outputDelta` | `{ID: "codex:command:<itemId>", Mode: ModeStream, Content: Command{Output: "..."}}` |
| `item/completed` (commandExecution) | `{..., Mode: ModeStream, Content: Command{ExitCode: &n}}` then `{..., Mode: ModeFlush, Content: Empty{}}` |
| `item/completed` (fileChange) | `{ID: "codex:filechange:<itemId>", Mode: ModeStream, Content: FileChangeSet{...}}` then ModeFlush |
| `turn/completed` | for each completed `agentMessage` item: `{ID: "codex:output:<turnId>:<itemId>", Mode: ModeFlush, Content: Output{}}` |
| `turn/failed` | error returned from `Read()` |
| stderr line | `{ID: "codex:log:<…>", Mode: ModeSingle, Content: Log{Text: "..."}}` |

Stderr is captured via a dedicated pipe and read by a background goroutine started in `Start()`. Lines are queued internally and surfaced through `Read()` alongside stdout-derived messages. This keeps the `Read()` interface as the single point of consumption for all agent output.

### SendNotice semantics

`SendNotice` starts a normal turn (`turn/start`) but provides an output schema that
encourages a minimal JSON acknowledgement (e.g. `{"acknowledge":true}`) and suppresses
that acknowledgement from `Read()`.

Even when the notice is fully acknowledged (or produces no deltas), the adapter
still emits a synthetic `Output + ModeFlush` on `codex:notice-turn` derived from
`turn/completed` (or `turn/failed`). This lets downstream consumers treat a
silent notice as a complete lifecycle without overloading normal output streams.

### KeepAlive semantics

`SendNotice` and `KeepAlive` share the same notice-turn mechanism. `KeepAlive`
starts a maintenance notice constrained to the same minimal JSON
acknowledgement shape as delivery notices. The adapter suppresses all response content
and emits `agent.KeepAlive{}` when the turn completes or fails. This preserves
the existing session keepalive contract while ensuring the maintenance request
exercises the model-side thread context rather than merely reading metadata.

The pinned app-server protocol has no per-turn switch that disables built-in
tools. The maintenance prompt therefore explicitly forbids tool use. As a
defensive boundary, the adapter also suppresses every `item/*` notification and
auto-declines approval requests without forwarding them to the UI. The first
tool-related notification marks the maintenance turn failed, emits a prominent
diagnostic, and interrupts the turn; subsequent tool output remains suppressed.

### Turn anchors

`Send` and `SendNotice` return a turn anchor to the caller.

The anchor is the agent-level contract that defines turn lifetime. It is not
"the last currently visible output stream"; it is the explicit stream whose
flush means "this turn is now over".

Why this exists:

- Codex can emit multiple auxiliary streams during one turn (`Output`,
  `Reasoning`, `Command`, `FileChangeSet`).
- Those streams may open and close in phases.
- If a caller inferred turn completion from "all currently observed streams are
  closed", it could end the turn too early and misclassify later valid output as
  stray protocol noise.

The adapter therefore gives callers a stable anchor immediately on `Send*`:

- for normal visible turns, the anchor represents the turn lifecycle even though
  visible output remains item-scoped
- for notice turns, the adapter emits a dedicated synthetic flush on
  `codex:notice-turn`

Consumer rule:

- treat the returned anchor as authoritative turn-end
- do not infer turn completion from auxiliary stream closure alone

This is the bridge between agent protocol semantics and participant/session
lifecycle semantics.

### Protocol observer

```go
type ProtocolObserver interface {
    OnSend(msg string)
    OnReceive(msg string)
}
```

An optional observer receives every raw JSON line before any processing. A file-based implementation provides a wire log for debugging; a slice-based implementation captures messages for test assertions.

`msg` is the raw JSON without the trailing newline. Implementations must be fast; avoid operations that can block for non-trivial time (network calls, contested locks). A log file write is acceptable.

Hook points:
- **Send path**: after serialising the request, before writing to stdin — `observer.OnSend(raw)`
- **Read path**: after reading a raw line from stdout, before parsing — `observer.OnReceive(raw)`

The observer sees the truth: whatever actually flows over stdio, regardless of whether our parsing is correct. This makes it the right tool for validating protocol assumptions (such as "errors arrive as turn/failed notifications, not as RPC error responses") without encoding that validation into production logic.

### Sequential request constraint

The Codex app-server processes one turn at a time. The call sequence is always:

```
initialize → thread/start → turn/start → turn/start → …
```

`Start()` consumes the responses from initialize and thread/start. After that, `Read()` is expected to see only notification lines (method-bearing, no id). This is an observed invariant monitored by the protocol observer, not a guarantee the package enforces. If `Read()` encounters a response line it skips it deliberately — response lines after `Start()` are unexpected and the observer log is the right place to investigate them.

### Normal flow

```mermaid
sequenceDiagram
    participant C as Caller
    participant X as Codex process

    Note over C,X: Start()
    C->>X: cmd.Start()
    C->>X: initialize → stdin
    X-->>C: response ← stdout
    C->>X: thread/start → stdin
    X-->>C: response{threadID} ← stdout

    Note over C,X: Send("prompt") — pure write
    C->>X: turn/start → stdin

    Note over C,X: Read() × N
    X-->>C: turn/started ← stdout
    X-->>C: item/agentMessage/delta ← stdout
    X-->>C: turn/completed ← stdout

    Note over C,X: Stop()
    C->>X: stdin.Close()
    X-->>C: process exits (graceful)
    Note over C,X: Kill() + Wait() if process hangs
```

### Thread-safety contract

Only one goroutine may call `Read()` at a time. `Stop()` may be called from a different goroutine to interrupt a blocked `Read()` — that is the one permitted concurrent pair. No other concurrent combinations are allowed.

### Shutdown

`Stop()` closes stdin, which signals EOF to the Codex process and is the expected graceful exit path. If the process does not exit within `stopGracePeriod` (5 s), it is killed. In either case `Wait()` is called before `Stop()` returns.

If `Read()` is blocked on another goroutine, the process death closes stdout and unblocks `Read()` with an error. Callers should treat a `Read()` error following `Stop()` as clean EOF, not a failure.

### Design boundary

The session and router layers own:

- Deciding when to call `Send` or `SendAndWait`
- Spawning goroutines to read streaming output concurrently with other events
- Sequencing turns across multiple agents
- Restarting a crashed agent

This package owns:

- Process lifecycle (start, kill, reap)
- JSON-RPC framing over stdio
- Translating Codex notifications into `agent.Message`
- Wire-level observability (`ProtocolObserver`)

## Session model selection

Codex implements `agent.ModelSelector` for `/model`. It lists through
`model/list`, changes settings with a guarded acknowledgement turn, and reads
them back with `thread/read`. See [participant model and effort](pkg-agent-codex-models.md)
for admission, outcome handling, quota implications, and recording/replay tests.

## Test fixture startup ordering

Investigation on 2026-10-22: `TestNoticeFilter_compliantAck` intermittently
returns `ModeStream` (numeric value zero) where it expects the synthetic notice
`ModeFlush`.

The cause was the initialization order in the test fixture:

1. `setupNoticeClient` calls `newWithIO` with an already populated stdout buffer.
2. `newWithIO` starts the workers before returning.
3. `setupNoticeClient` then sets the thread, turn state, and `noticePending`.

The stdout worker can consume the fake response before step 3. With the notice
state still idle, `interceptNotice` returns `noticeUnhandled`. The ordinary
agent-message decoder emits the acknowledgement as `Output + ModeStream`.
Another possible ordering consumes `turn/started` while idle, then installs
`noticePending` before the delta. The pending state has not become active, so
the delta also falls through to ordinary output.

Locks protect the state accesses, but do not require initialization to happen
before the worker reads. This is an ordering race that need not trigger a Go
data-race report. Changing the assertion to ignore streamed output would hide
the fixture error.

### Reproduction

An initial 1,000 repetitions passed. The following unchanged-test run reproduced
the exact failure four times across 20,000 repetitions, with no data-race
warnings:

```sh
go test -race ./internal/agent/codex -run '^TestNoticeFilter_compliantAck$' -count=10000 -cpu=2,4
```

The test uses in-memory I/O and makes no live model calls.

### Correction

`newUnstartedWithIO` constructs the fixture without launching workers.
`setupNoticeClient` installs the complete notice state before calling
`initWorkers`. The keepalive fixture reuses the same construction helper and
keeps its existing start-last ordering. `newWithIO` still constructs and starts
workers for callers that need no additional initialization.

The nearby `TestKeepAlive_startsHiddenAcknowledgementTurn` had the same ordering
problem. It now sets the thread and admits its maintenance turn before starting
workers to consume the preloaded response. Production code and assertions are
unchanged. No sleeps or retries were added.

Production `startNoticeTurn` installs `noticePending` before writing the
`turn/start` request. The reproduced failure is in the test's simulated setup.

The corrected notice test passed the same 20,000-run stress command above.
The notice and keepalive tests then passed 40,000 combined repetitions with the
race detector. `make pre-commit` passed, including lint and the full race-enabled
test suite.
