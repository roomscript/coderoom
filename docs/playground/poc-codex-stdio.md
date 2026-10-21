# POC: Codex CLI via app-server (stdio)

## Goal

Validate that Codex can be driven over its app-server stdio transport using JSON-RPC 2.0, enabling a single persistent process with a clean, secure communication channel.

---

## Transport choice

The app-server supports three transports: `stdio` (default), WebSocket, and Unix socket.

WebSocket (`ws://`) is marked as unsupported in the official documentation. The stdio transport is the documented, supported path: the server reads JSONL from stdin and writes JSONL to stdout. No port binding, no socket accessible to other processes — the communication channel is private to the parent-child process relationship.

```
npx @openai/codex app-server
```

No `--listen` flag needed; stdio is the default.

---

## Protocol

JSON-RPC 2.0 over newline-delimited JSON (JSONL). No `jsonrpc: "2.0"` header on the wire. Each message is one line.

Flow:
1. `initialize` — handshake with client info and capabilities
2. `thread/start {cwd}` — create a persistent thread, receive `thread.id`
3. `turn/start {threadId, input: [{type: "text", text: "..."}]}` — submit prompt
4. Read notifications until `turn/completed`
5. Repeat step 3 for subsequent turns on the same thread

Valid input item types: `text`, `image`, `localImage`, `skill`, `mention`.

Notification sequence per turn:
```
thread/started
mcpServer/startupStatus/updated
turn/started
item/agentMessage/delta     streaming text chunks (delta field)
item/completed              full item; text at params.item.text
turn/completed
```

---

## Findings

**Passed** on both WebSocket and stdio transports. Single process, context preserved across turns. Second turn answered "4 × 3 = 12."

No external dependencies required — stdlib `os/exec` with stdin/stdout pipes handles the transport. `gorilla/websocket` removed.

---

## Success criteria

All criteria met:

1. app-server starts and communicates over stdio.
2. `initialize` and `thread/start` succeed.
3. Both turns complete via `turn/completed`.
4. Second turn response contains 12.

---

## How to run

```
cd playground
make pg-codex-ws
```

## Model selection probe (20 October 2026, 0.140.0)

Step 1 of #71. Runnable experiment:
`playground/codex-stdio/models_integration_test.go`, built only with the
`integration` tag. No adapter changes are involved. The initialize response
confirmed app-server version 0.140.0. Evidence, reduced to catalog, startup,
completion and rollout-context fields without conversation or account data:
[codex-model-probe-0.140.0.json](codex-model-probe-0.140.0.json).
Raw wire logs remain in `/tmp/codex-model-probe*.jsonl` on the experiment host.

Run from `playground/codex-stdio`:

```sh
CODEX_MODEL_PROBE_MODEL=<model> CODEX_MODEL_PROBE_LOG=/tmp/codex-model-probe.jsonl \
  go test -tags integration -run '^TestModelProtocol$' -v -count=1
```

This consumes quota when the account permits generation. An explicit model is
required to avoid accidental spending. `CODEX_MODEL_PROBE_INVALID=1` adds an
invalid-effort probe and its follow-up; `CODEX_MODEL_PROBE_EXPECT_FAILURE=1`
allows exploratory runs where confirmation is expected to fail. Without that
flag, unsuccessful confirmation fails the test. The original exploratory runs
reported PASS because they only checked protocol exchange completion; that was
not successful model confirmation. The probe now distinguishes those outcomes.

### Established

- `model/list` answers while a turn is active, without starting another turn.
  `limit: 1` returned one entry and `nextCursor: "1"`; sending that cursor
  returned the next entry and `nextCursor: "2"`. The adapter must follow
  pagination rather than assume one response contains the entire catalog.
- `thread/start` reported configured model `gpt-6.1-sol` and effort `low`.
- Catalog presence does not guarantee account access. Both `gpt-5.4-mini` and
  `gpt-5.5` were advertised but failed with HTTP 400: model not supported when
  using Codex with a ChatGPT account. The configured `gpt-6.1-sol` failed with
  the same error. No tested model produced a successful acknowledgement.
- A failed generation does **not** roll back overrides. The failed turn's
  rollout `turn_context` recorded the requested model/effort, and the next turn
  without overrides recorded the same pair and failed on the same model.
- A model-only request retained the earlier effort (`low`) in the recorded
  context. This checks setting the same model without effort; effort handling
  when switching to a different supported model remains unverified.
- In the mini-model run an immediately interrupted turn completed with
  `status: interrupted`; its requested `medium` effort persisted into the next
  turn's context. The other runs raced with the service rejection and completed
  as failed instead. Cancellation cannot be treated as confirmation or rollback.
- An arbitrary effort string (`bogus`) was accepted by `turn/start` and appeared
  in that turn and the next turn's context. Both generations failed on model
  access, so this does not establish how the service handles invalid effort.
  Validate effort against the catalog before sending anything.

### Still unverified; implementation dependency

Successful confirmation, absence of silent substitution, cross-model effort
behavior, and preservation of conversational memory require a working model for
this account/version. Rollout context establishes persisted local thread
settings, not successful service execution. Do not infer effective settings from
it after a failed acknowledgement. A pre-admission RPC rejection and its effect
on settings also remain untested. No adapter behavior has been chosen around
these unknowns, and step 1 remains open.

### Follow-up: 6.1 Sol and 6 Sol, version comparison

The user identified `gpt-6.1-sol` and `gpt-6-sol` as available models. A further
0.140.0 probe of `gpt-6-sol` also failed with the account/model support error.
The local model cache reports client version 0.163.0 and contains both Sol
models; 0.140.0 logs a cache decoding error (`base_instructions` missing) and
falls back to an older catalog. This points to a version compatibility issue,
rather than establishing that the account lacks access. The earlier account
access conclusion was too broad.

A comparison using **0.163.0**, without changing `CODEX_VERSION`, succeeded:

| Turn | Overrides | Completion | Recorded settings |
|---|---|---|---|
| Remember token | `gpt-6.1-sol`, `low` | completed, `OK` | `gpt-6.1-sol`, `low` |
| Persistence | none | completed, `OK` | `gpt-6.1-sol`, `low` |
| Model-only switch | `gpt-6-sol` | completed, `OK` | `gpt-6-sol`, `low` |
| Switch back; attempt interrupt | `gpt-6.1-sol`, `medium` | completed, `OK` | `gpt-6.1-sol`, `medium` |
| Recall token | none | completed, `juniper` | `gpt-6.1-sol`, `medium` |

This establishes successful execution and conversational memory across model
switches, with requested settings reflected in rollout contexts. There was no
observed substitution. It does not prove service internals cannot substitute.
The newer protocol also emits `thread/settings/updated`, reporting the changed
model and effort; 0.140.0 did not emit that notification in these probes.
Evidence: [codex-model-probe-0.163.0.json](codex-model-probe-0.163.0.json).

The interruption attempts on 0.163.0 raced with completion: `turn/interrupt`
answered `no active turn to interrupt`. They do **not** establish interrupted
confirmation behavior on 0.163.0. The revised test fails that assertion instead
of reporting a misleading pass. Memory and all four ordinary-turn completion
assertions passed. The earlier 0.140.0 mini-model run remains the evidence for
persistence following an actual interrupted completion.

Reproduce the version comparison:

```sh
CODEX_MODEL_PROBE_VERSION=0.163.0 \
CODEX_MODEL_PROBE_MODEL=gpt-6.1-sol \
CODEX_MODEL_PROBE_OTHER_MODEL=gpt-6-sol \
CODEX_MODEL_PROBE_LOG=/tmp/codex-model-probe-sol.jsonl \
  go test -tags integration -run '^TestModelProtocol$' -v -count=1
```

Before step 2, choose the baseline: retain the 0.140.0 contract with newer
live verification, or update the pin/recordings and design for 0.163.0. The new
settings notification is relevant to outcome reporting and should be evaluated
if adopting the newer baseline. No production adapter or version pin was changed.

### Interruption after activation (21 October 2026, 0.163.0)

The earlier interruption failure was a race with **activation**, not completion:
wire order was `turn/start` response, `turn/interrupt`, rejection (`no active
turn to interrupt`), then `turn/started`. A start response supplies an ID before
that turn necessarily becomes active. Waiting for `turn/started` fixes the
probe; production cancellation should use that lifecycle boundary too.

The corrected integration probe passed all five turns:

1. `gpt-6.1-sol` with `low`: completed.
2. No overrides: completed with the same settings.
3. `gpt-6-sol` without effort: completed, retaining `low`.
4. `gpt-6.1-sol` with `medium`, interrupted after `turn/started`: interrupt RPC
   succeeded and completion was `interrupted`, with no agent output.
5. No overrides: completed with `gpt-6.1-sol`, `medium`, and recalled `juniper`.

`thread/settings/updated` reported the new model and effort before the
interrupted completion. The interrupted turn had no persisted `turn_context`,
but the follow-up turn's context confirmed the settings persisted. Therefore
absence of a context record is not evidence that a selection rolled back.
An interrupted acknowledgement must report failure even when settings changed.

Evidence: [codex-model-probe-0.163.0-started.json](codex-model-probe-0.163.0-started.json).
The runnable probe now defaults to 0.163.0, waits for activation before listing
or interrupting, and checks both the interrupt response and terminal status.
The next verification work is pre-admission rejection and failure reporting on
this baseline, and evaluating the settings notification as a source for the
per-field outcome. Step 6 will convert adapter scenarios into replay fixtures;
these playground evidence files are not a replacement for CI replay coverage.

### Rejection, failed generation, and settings readback (21 October 2026)

The extended probe (`CODEX_MODEL_PROBE_FAILURES=1`) passed on 0.163.0, including
all five success/interruption turns above and these additional checks:

- `thread/read` returns the current settings as `thread.model` and
  `thread.reasoningEffort`, with no generation turn. Before and after a malformed
  `turn/start` (`input` is a string instead of a list), both reported
  `gpt-6.1-sol` and `medium`, despite that request containing different overrides.
  The malformed request was rejected at the RPC boundary; no partial mutation
  was observed for that rejection class.
- An admitted turn selecting unsupported `gpt-5.4-mini` with `low` completed as
  failed. `thread/settings/updated` reported that pair, and a following
  `thread/read` confirmed it remained current. Failure does not restore the
  prior settings. These are reported thread settings, not proof of successful
  generation on the selected model.
- A subsequent turn explicitly selecting `gpt-6.1-sol` with `low` succeeded.
  A failed confirmation does not make the thread irrecoverable.

Evidence: [codex-model-probe-0.163.0-failures.json](codex-model-probe-0.163.0-failures.json).
This adds a useful design option absent from the original 0.140.0 proposal:
read settings after completion, including failure/interruption, rather than
infer each field solely from success. A failed read must still leave potentially
changed fields unknown. Successful readback reports the actual thread settings
independently of whether the acknowledgement succeeded. Outcome reporting must
retain the acknowledgement error. Do not generalize the malformed-input result
to every kind of RPC error without evidence.
