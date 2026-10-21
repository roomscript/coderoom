# Codex: participant model and effort

Implements #71 on app-server 0.163.0. The shared contract is
[participant-models.md](participant-models.md). Protocol evidence and limitations
are recorded in [the stdio experiments](../playground/poc-codex-stdio.md).

## Startup and catalog

`Settings()` returns the model and reasoning effort from `thread/start`, without
I/O. Missing or null values remain unknown (`codex-managed`). The registry sends
no model/effort override; test-only client options continue to work.

`Models()` sends `model/list`, follows `nextCursor` until exhausted, and emits
one `ModelsListed`. `model` supplies both Name and ID; effort names come from
`supportedReasoningEfforts[].reasoningEffort`. Listings work during turns and
hold no prompt gate. Concurrently outstanding listings publish in call order.
RPC errors, malformed responses, and repeated cursors fail the whole listing.
Catalog membership is validation, not a guarantee of account access.

## Selection and exclusivity

`SelectModel` validates effort against the supplied catalog option, then admits
one maintenance acknowledgement turn with `model` and optional `effort` in
`turn/start`. An empty effort is omitted; the CLI owns its resulting value.
Overrides persist in the existing thread. Conversation survives the change;
configuration files and other participant instances are untouched.

The exchange reserves the adapter's existing prompt gate through confirmation
and readback. Turn state remains non-idle throughout: the active turn transitions
to `turnModelReadback` at completion, and only `finishModelChange` returns it to
idle. Admission checks turn state alone. It refuses admission during ordinary work, notice, keepalive, or
another change. `Send`, `SendNotice`, and `KeepAlive` refuse during the exchange.
The session additionally reserves the participant as `changing-model`.

The acknowledgement uses the maintenance prompt and structured output schema.
All its output and lifecycle flushes are suppressed. Existing maintenance item
classification detects tool, unknown, and malformed activity; approval requests
are declined and unexpected activity triggers interruption. A violation remains
a failed change even if a later terminal notification says completed. A model
change never emits `KeepAlive`. It consumes quota and adds a minimal exchange to
conversation history.

Only a `completed` terminal status without an error or maintenance violation
confirms success. Failed, interrupted, malformed, and rejected turns preserve
failure. Interruption uses the existing `turn/started` lifecycle boundary: a
start response can arrive before the turn is active. There is no timeout or
rollback. Removal, process exit, or shutdown ends the exchange through the
existing client lifetime, without a fabricated successful outcome.

## Readback and outcomes

After every terminal outcome or rejected start request, send `thread/read`.
`thread.model` and `thread.reasoningEffort` establish the resulting settings,
including after failure or interruption: failed generation can leave overrides
applied. Report these settings together with the confirmation error. Null or
missing fields stay unknown. If readback fails, both potentially changed fields
become unknown and the readback error joins the confirmation error.

0.163.0 also emits `thread/settings/updated`. Explicit readback is used here to
avoid maintaining a second notification-driven settings projection and to obtain
one final outcome after the exchange. A successful readback does not turn a
failed acknowledgement into success.

Store the settings and release the gate before enqueueing the single
`ModelSelected`, so work triggered by that outcome is accepted. Initial write
failure releases the gate and returns an error without an outcome, as required
by `ModelSelector`. Subsequent exchange failures arrive on `Read`.

## RPC responses and testing

Register response handlers by request ID before writing. The stdout worker
removes and invokes matching handlers outside the RPC lock; unrelated responses
remain ignored. Startup keeps the existing synchronous handshake. Listing and
readback add no worker or blocking reads in public methods.

Unit tests use fake pipes for validation, ordering, error and lifecycle cases.
Integration tests alone communicate with real agents and record protocol steps.
Unrelated notifications continue through the normal stdout handler during a
change. Known approval requests are declined. Unsupported server requests get
an explicit JSON-RPC method-not-found error, including during readback; an
unsupported item request during confirmation also fails and interrupts that turn.

Normal tests replay those steps through the existing replay peer, exercising
catalog, selection, readback and conversation behavior without quota costs.

### Running the checks

The model cases follow the standard fixture layout:
`testdata/transcripts/<version>/model-<case>/conversation.md`, numbered
`conversation-XX.md` actions, and `output.transcript`.

- Offline replay: `go test ./internal/agent/codex -run 'TestReplayFixtures/0.163.0/model-'`.
- Integration-only recording of the current cases:
  `CODEX_MODEL_RECORD=1 go test -tags integration ./internal/agent/codex -run TestRecordModelScenarios -count=1 -v`.
- The standard `codex-record <version> <case>` command records these inputs too;
  the upgrade scripts discover and copy them alongside other conversations.

Conversation action kinds extend the existing `kind`/body convention:

| Kind | Body | Behavior |
|---|---|---|
| `model-list` | Required model IDs, separated by whitespace | List and validate the catalog |
| `model-select` | Model and optional effort | Confirm successful selection and readback |
| `model-select-failed` | Model and optional effort | Deliberately bypass catalog admission; require backend failure and readback |
| `model-interrupt` | Model and optional effort | Interrupt after activation; require failed confirmation and readback |
| `model-settings` | Expected model and effort | Check the current settings without a request |

The recorder and ordinary fixture replay use one shared model-action driver. The standard collector records prompt output and
other existing expectations; model actions additionally assert their own outcomes
and reject leaked turn output. Wire matching preserves selection overrides.
Historical recordings remain in their original version directories.

### Smoke isolation and action dependencies

The shared live smoke session runs only ordinary prompt/notice cases on its
configured model and effort. Discovery excludes every conversation containing a
model action, regardless of directory name. Model cases keep independent clients
through the standard recorder and its dedicated integration test; they remain in
the upgrade recording workflow. Their explicit model IDs and account-dependent
failure assumption are intentional current prerequisites. If those assumptions
change, review the new version's inputs while preserving historical recordings.
Catalog-relative default/first/last model selectors are a separate future feature.

The model-action driver depends on `agent.ModelSelector`, `Read`, and `Interrupt`
through a small interface. Recording observers retain only traffic. A separate
activity observer always wraps recording, and replay uses the same wrapper
without recording. Existing Codex
configuration types in transcript inputs are independent of this action-driver
interface.
