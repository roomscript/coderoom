# Codex /model implementation plan

Temporary progress tracker for #71. Follow the existing interpreter/session
`ModelSelector` contract and keep changes focused on the Codex adapter.

- [x] **1. Verify the pinned protocol (0.163.0).** Check whether successful
  acknowledgement establishes explicitly supplied model/effort without silent
  substitution; determine model-only effort behavior and which fields remain
  known after failed or interrupted confirmation. Verify `model/list` during an
  active turn and its pagination. Record evidence in playground notes. Live
  agent communication belongs only in integration tests, including experiments.
- [ ] **2. Document the Codex design.** Create
  `docs/design/pkg-agent-codex-models.md` describing startup settings, catalog
  mapping, confirmation, exclusivity, and per-field failure rules. Resolve any
  material ambiguity exposed by step 1 before implementing that behavior.
- [ ] **3. Implement model selection.** Implement `ModelSelector`: capture
  `thread/start` settings, list models through `model/list`, validate effort,
  and apply model/optional effort through a minimal acknowledgement turn.
  Preserve conversation and subsequent-turn settings without configuration
  writes. Model-only changes send no effort override.
- [ ] **4. Integrate confirmation safeguards.** Reuse existing maintenance
  mechanics for exclusive admission, tool prevention, and suppressed output.
  Emit `ModelSelected` with distinct success/failure outcomes rather than
  collapsing completion into `KeepAlive`. Handle process exit, interruption,
  shutdown, and gate release using existing lifecycle paths.
- [ ] **5. Verify behavior and update documentation.** Add focused unit/fake
  coverage for inherited settings, catalog/listing errors, validation,
  model-only changes, busy rejection, confirmation outcomes, and lifecycle
  cleanup. Check the existing session/interpreter integration and update
  participant/backend docs. Run relevant lint and unit checks.
- [ ] **6. Record and replay protocol tests for cost-free CI.** Extend the
  existing recording/replay test workflow with model listing and selection
  scenarios, including persistence across subsequent turns and confirmation
  failures/interruption where practical. Capture fixtures through explicitly
  tagged integration tests that communicate with real agents and consume quota.
  Replay those fixtures in ordinary tests with no live agent communication or
  quota cost. Verify normal CI runs the replay coverage and keeps live tests
  separate.

No automatic commits. Update this checklist as work completes and record any
protocol findings or scope decisions alongside the relevant step.

## Step 1 progress — 20 October 2026

Added an integration-only playground probe and recorded outcomes in
`docs/playground/poc-codex-stdio.md` with reduced JSON evidence. Confirmed busy
listing, pagination, persisted overrides after failure/interruption, and
model-only retention of effort when selecting the same model. All three tested
models were rejected by the account, including the configured model. Successful
confirmation and cross-model behavior remain unverified; step 1 is not complete.
No adapter implementation has started.

### Sol follow-up

Both `gpt-6.1-sol` and `gpt-6-sol` execute successfully on 0.163.0. Verified
model-only cross-model effort retention, persistence, switch-back with explicit
effort, and conversation memory. The 0.140.0 client rejects both models and
cannot decode the current model cache. 0.163.0 additionally reports
`thread/settings/updated`. Its interruption attempts raced with completion, so
that scenario remains unverified for the newer version. Awaiting the user's
baseline choice before designing against these different protocol capabilities.

### Baseline — 21 October 2026

The user upgraded and verified 0.163.0. Resume protocol verification on that
baseline, including interruption and failure outcomes. Upgrade commands are now
`make codex-upgrade-next` and `make codex-upgrade-last` (direct to latest).

### Activation and interruption verified — 21 October 2026

Corrected the probe to interrupt after `turn/started`, rather than immediately
after the start response. The five-turn 0.163.0 integration probe passed:
successful selection, persistence, model-only effort retention, acknowledged
interruption, and conversation memory after interruption. The selected effort
persisted despite interrupted completion; `thread/settings/updated` reported
the change beforehand. Evidence and details are in the playground notes.
Remaining step 1 checks: pre-admission rejection and failure reporting on the
new baseline; evaluate settings notifications for per-field outcomes.

### Step 1 complete — 21 October 2026

The extended integration probe passed, checking pre-admission malformed-input
rejection without settings mutation, failed generation with persisted overrides,
settings readback via `thread/read`, and recovery using a valid model. Both
`thread/settings/updated` and `thread/read` expose settings on 0.163.0. Step 2
should account for that readback when specifying per-field outcomes after errors;
keep acknowledgement success/failure distinct from the reported thread settings.
The two passing live probe runs and reduced evidence are documented under
`docs/playground/`. CI replay work remains step 6.
