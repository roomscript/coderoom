# Plan: a Claude Code backend for coderoom

The working plan for the Claude Code backend. Written on 15 October 2026; kept next to the package so later branches can follow it. Lasting design belongs in `docs/design/`; this file can be removed once the work is done.

## Status

Updated on 24 October 2026.

- Phase 1 (spike): **done.** Report: `docs/playground/poc-claude-stream-json.md`. Playground: `playground/claude-stream-json/`.
- Integration approach: **decided.** Direct integration with the claude CLI over stream-json from Go (option A below), the same pattern as Codex app-server.
- Phase 2 (gap analysis): **done.** Design: `docs/design/pkg-agent-claude.md`. Decided on 16 October 2026: keepalive postponed, `acceptForSession` supported, backend in the participant config, `AskUserQuestion` disallowed, self-started turns prevented and defended, tools that reach outside the room disallowed. The choice of `agent-record` instead of `claude-record` was reversed on 24 October 2026: see "Agent testing" below.
- Phase 3 (implementation): **adapter done and registered.** See the phase 3 list for what is left and where it moved.
- Multi-backend configuration: **merged** to main.
- Register Claude: **done.** `claude` is a valid backend.
- Since registration, on main: replays matched to prompts by uuid, runtime model and effort selection (#71), provider explanations on failed turns, and participant details on join and in `/who` (#25, #24).
- README: **done.** It lists Claude Code as a backend.
- Agent testing: **next.** The adapter has scripted-peer unit tests and live integration tests, but no recorded transcript replay suite.

## Merge order

The remaining work is split so that shared changes reach main in small steps instead of piling up on this branch.

1. **Adapter package, not wired in.** Independent review, then merge `internal/agent/claude` to main. Nothing calls it, so users see no change.
2. **Multi-backend configuration** (merged): `docs/design/multi-backend-config.md`. Steps, one commit each:
   1. `agent.Agent` gains `Name()`; the optional `agent.ExternalDependencies` interface (Codex, echo, test fakes; Claude has `Name()` only).
   2. `config`: `backend` field, `CODEROOM_BACKEND` default, load at startup, change notice on `/invite`.
   3. `internal/agent/registry`: backend registry, startup validation, dependency checks reported in the room; one factory in `cmd/coderoom`.
   4. Display: backend name in `View`, the starting line and `/who`.
3. **Register Claude** (done): `ExternalDependencies` on the Claude client (`claude --version`), `--agent-log` (the observer and version check are shared with Codex: `internal/agent/wirelog`, `internal/agent/depcheck`), a `registry.Claude` constructor added to `registry.NewDefault`. Before Claude can be invited, also handle these findings from the review of 17 October 2026:
   - **Done.** A prompt whose replay is never matched leaves the client busy until `Stop`: a replay that arrives during a self-started turn, a replay Claude changed, or a prompt starting with `/` run as a slash command. Checked live: a leading `/` runs a local command with no replay; `Send` now escapes it.
   - **Done.** Cancelling the parent context (`WithContext`) drops queued approvals without answering them, so the turn hangs until `Stop`. It now stops the process.
   - **Done.** `Stop` during `Start` may race with the fields `Start` sets. Confirmed by reading, but no caller does it: the session attaches an agent only after `Start` returns. Documented as unsupported; cancelling the parent context aborts a start, and a test covers that.
   - **Done.** Missing tests: the process exiting mid-turn while a listener is blocked; parent context cancellation.
4. **Agent testing**, for both backends. Design: `docs/design/agent-transcripts.md`. Steps, one change each:
   1. One-time cleanup to the Codex baseline fixtures (0.163.0); future upgrades retain previous recordings.
   2. Standard transcript schema and a backend-neutral `internal/transcript`; one mechanical fixture migration; format and replay details consolidated in `docs/design/agent-transcripts.md`.
   3. Claude recording and replay (`cmd/claude-record`, `cmd/claude-replay`), after the capability requirements and the handling of random IDs are decided.
5. **README** (done): Claude Code is listed as a backend.
6. **Demo** (phase 3 "done when").

## Why

coderoom's concept (`docs/design/concept.md`) starts from "different models have different blind spots". Today `internal/agent/` has only `codex` and `echo`, so every room is one vendor reviewing itself. A Claude backend makes the multi-model premise demonstrable ("Codex builds, Claude decides") and does not depend on the interpreter migration (#61).

## Background

- May 2026 research (`docs/playground/research-llm-cli.md`, `poc-claude-*.md`) concluded that `claude --print --input-format stream-json` exits without reading stdin, and fell back to one process per turn with `--resume`.
- Phase 1 overturned that on Claude Code 2.1.300. One long-running process works:

```
claude --print --verbose \
  --input-format stream-json --output-format stream-json \
  --replay-user-messages --include-partial-messages \
  --permission-prompts host --permission-prompt-tool stdio \
  --model <model>
```

- The protocol is the one the Agent SDK uses (control requests and responses on stdin and stdout). The SDK source (`claude-agent-sdk-python`) and the SDK docs ("Handle approvals and user input") are the reference for message shapes.

## Decisions

| Topic | Decision |
|---|---|
| Integration | Option A: speak stream-json directly from Go. No SDK sidecar, no ACP. ACP may come later as a generic backend for tools that speak it natively (Gemini CLI) |
| Claude Code version | Target only the installed version (2.1.300 at the spike; later live checks ran on 2.1.301 to 2.1.303). Pinning and upgrades come later |
| Personal config | Load it, as coderoom does for Codex. No `--safe-mode`. It stays documented (E9) as context for the sandbox work (#59) |
| Models in live runs | Start with haiku; use a stronger model only when haiku cannot show the behaviour |
| Fixtures | Playground recordings stay local (git-ignored). Phase 3 records its own replay transcripts in the Codex style |

## Ground rules

- Work on main. The `claude-backend` worktree and branch were used until the adapter merged.
- Never stage or commit. The human does all git staging and commits.
- Live runs use the human's Claude login and cost usage: use a cheap model, small prompts. Live runs are fine while preparing playgrounds; otherwise run them only when asked.
- Follow the existing conventions: playground programs as their own Go module with `pg-…` targets in `playground/Makefile`; reports as `docs/playground/poc-….md`.
- Start the child with a clean environment: drop the variables a parent Claude Code session exports (`CLAUDECODE`, `CLAUDE_CODE_*`, `CLAUDE_PID`, `CLAUDE_EFFORT`).

## Phase 1: spike (done)

Ten experiments, all run live on haiku. Summary (details in the report):

| # | Experiment | Outcome |
|---|---|---|
| E1 | Idle start, init | Stays alive without a TTY. `initialize` control request answers in under 3 s. `system/init` comes with every turn, not on start |
| E2 | Two turns | Context kept. One `result` per turn |
| E3 | Approvals | Needs `--permission-prompt-tool stdio`; without it prompts are auto-denied. Allow, deny and continue all work |
| E4 | Interrupt | `interrupt` control request ends the turn in tens of ms; process survives |
| E5 | Partial messages | Text, tool calls and structured tool results map to coderoom types. Reasoning text needs `--thinking-display summarized`. Bash has no exit code |
| E6 | cwd | Correct from a non-shell parent; `cd` persists across Bash calls |
| E7 | `--json-schema` | Per process, and not enforced: the model may skip `StructuredOutput` |
| E8 | Stop and resume | Close stdin and SIGTERM both exit cleanly; `--resume` restores context |
| E9 | `--safe-mode` | Works with OAuth, but drops the project `CLAUDE.md`. Not used |
| E10 | Tools that ask, act outside a turn, reach outside the room | `AskUserQuestion` and `ExitPlanMode` arrive as approvals. Background Bash and cron start turns on their own. `ListAgents` sees other local sessions without a prompt |

## Phase 2: gap analysis and design decisions

The option decision is already made, so phase 2 is about how the backend fits coderoom.

1. Extend `docs/playground/poc-claude-stream-json.md` (or a new `docs/design/` note) with:
   - a mapping table from Claude events to `agent.Message` content types, and to `ApprovalRequest` and `ApprovalDecision` (`internal/agent/approvals.go`);
   - the gaps against `agent.Agent` (`internal/agent/agent.go`): `Start`, `Send` (with the turn anchor `StreamID`), `SendNotice`, `Read`, `Interrupt`, `Stop`, and the optional `Keepaliver`.
2. Decide the open questions from the report:
   1. **Approval kinds:** how `AskUserQuestion` (a decision carrying answers) and `ExitPlanMode` (a plan to approve) extend `ApprovalRequest` and `ApprovalDecision`.
   2. **Turns Claude starts on its own** (background Bash, cron): show them as unprompted participant output, disable the tools with `--disallowedTools`, or decide per tool. Check what Codex does with background work.
   3. **Reaching outside the room:** run participants with `--disallowedTools ListAgents,SendMessage,PushNotification,RemoteTrigger` now, or leave it to #59. Check whether `SendMessage` prompts.
   4. **Bash exit code:** is `is_error` enough for `Command`?
   5. **Project allow rules:** retry E9's allow rule with a project outside `/tmp`.
   6. **`/decide`:** `--json-schema` needs its own process and a check for missing `structured_output`.
   7. **`SendNotice`:** a normal turn with filtered output, as with Codex.
   8. **Turn anchor:** map the replayed user message `uuid` to `StreamID`, given its ~1.5 s delay.
3. Update `docs/playground/research-llm-cli.md`: multi-turn stream-json works, and `--safe-mode` exists.

**Done when:** the mapping and gaps are written, and the human has decided the open questions. Stop here for review.

## Phase 3: implementation, test-first (after review)

Mirror the structure of `internal/agent/codex/`.

1. **Done.** `internal/agent/claude/` package and the design doc `docs/design/pkg-agent-claude.md`.
2. **Moved** to "Agent testing" in the merge order: transcript recording and replay are designed for both backends. Unit tests use a scripted peer meanwhile.
3. **Done.** Process management: clean environment, `initialize` handshake, bounded stop (close stdin, SIGTERM, kill), crash detection.
4. **Done.** `Send` and `Read`, turn ownership from the prompt replay, turn-end flush on `result`.
5. **Done.** `SendNotice`, matching the Codex behaviour.
6. **Done.** Approvals through `ApprovalListener`, with per-request withdrawal.
7. **Done.** `Interrupt` with the `interrupt` control request.
8. **Done.** Self-started turns: output suppressed and summarised as one log line.
9. **Moved** to "Multi-backend configuration" and "Register Claude" in the merge order.
10. **Done.** Live integration tests behind `//go:build integration` (`make test-integration-claude`), run on haiku.

**Done when:** a room with one Codex and one Claude participant runs the README demo (`/def tests /shell go test ./...`, `/loop … /until /tests /max 3`) with Claude as the builder and, separately, as the reviewer.

## Risks

- **Protocol drift:** stream-json is an SDK-level detail. Record the Claude Code version in transcripts and let replay tests catch changes. Pinning comes later.
- **Personal config in agents:** plugins, MCP servers, skills and memory load from the user's setup, as with Codex. Real isolation belongs to the sandbox work (#59); `--safe-mode` is a known option.
- **Reaching outside the room:** `ListAgents` (and possibly `SendMessage`) lets a participant see and contact the user's other Claude sessions. The plan-mode file goes to `~/.claude/plans/`, outside the project.
- **Self-started turns:** output that no `Send` asked for can break coderoom's turn assumptions.
- **Approval semantics differ from Codex:** Claude's permission model (rules, modes, read-only auto-approval, path checks) does not map one to one to coderoom's approval kinds. Document the mapping rather than forcing it.
- **Cost of live testing:** keep live runs few and small; rely on replay.

## Out of scope

- `/decide` itself (language work); E7 only checked feasibility.
- Sandboxing and CI auth (API keys instead of the CLI login).
- Changes to the interpreter (#61).
- Fixing the root Makefile: `make pg-…` from the repo root does nothing, because GNU make skips pattern rules for `.PHONY` targets. Run playground targets with `make -C playground …`.
