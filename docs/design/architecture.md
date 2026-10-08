# coderoom: Architecture

## Overview

A coderoom session consists of multiple AI agents sharing a git worktree. The repo is the shared workspace. `git diff` is the shared language of change.

The Session Controller is the central orchestrator. All commands, messages, and state changes flow through it.

---

## Component Map

```
+-----------------------------+
|         Terminal UI          |
| shared / private views       |
+--------------+--------------+
               |
               v
+-----------------------------+
|      Prompt Interpreter      |
| language, workflows, events  |
+--------------+--------------+
               |
               v
+-----------------------------+
|       Session Controller     |
| commands, routing, policy    |
+--------------+--------------+
               |
       +-------+-------+
       |               |
       v               v
+-------------+   +----------------+
| Agent       |   | Sandbox        |
| Registry    |   | Controller     |
+-------------+   +----------------+
       |               |
       v               v
+-------------+   +----------------+
| Agent       |   | chroot / net   |
| Runtime     |   | policy         |
+-------------+   +----------------+
       |
       v
+-------------+
| CLI Backend |
+-------------+
```

---

## Components

### 1. Terminal UI

- Shared room view (all agents + user)
- Private agent tabs
- Session roster with status indicators
- Command input

The intended UI boundary is raw input and structured intent sent to
`internal/interpreter`, with interpreter snapshots and events rendered by the
terminal. The interpreter parses input through `internal/promptlang`, owns
application workflows and the canonical room projection used for execution,
and serializes commands sent to the session controller.

The staged-submission cutover is complete: send, broadcast, and handoff
planning, frozen recipients, readiness requirements, lifecycle readiness, source selection,
dispatch, and interrupt coordination belong to the interpreter's `stageWorkflow`.
Startup and maintenance participants can be frozen recipients. Staging waits
for actual readiness, including StartupReady; interrupts cancel active turns
and continue waiting for startup. Unknown and crashed direct targets have
distinct errors. Handoff output waits apply only to active turns.
The TUI presents detached stage snapshots and invokes atomic edit, discard,
and interrupt operations through Bubble Tea commands. Its staged composer owns
text, status, focus, and approval-overlay presentation only.

The interpreter owns canonical transcript records. The TUI receives detached,
ordered `TranscriptChanged` deltas for records and stream/departure metadata,
and `StateChanged` snapshots for roster, approval, and stage presentation.
The construction-time observer receives an initial `StateChanged`, then all
application events in order. The UI consumes that queue without snapshot
queries. Snapshot records are for inspection, not a second transcript delivery
path. Input, shell, loop, and handoff semantic events do not echo
records already delivered through transcript deltas.

The TUI owns rendering, composer, focus, scrolling, and approval presentation.
Help formatting, startup tips, debug output, and event-formatted notices remain
presentation records. A deterministic index table translates canonical record
indices around those local notices, including open stream indices; it never
reconciles independent application projections. The TUI has no session observer,
room actor, or live session query. Handoff source markers and audits arrive from
the same canonical room used for execution.

Normal architecture tests use `go list` to reject direct production UI imports
of session/agent packages and transitive interpreter dependencies on UI,
Bubble Tea, Bubbles, or Lip Gloss (including legacy module paths). The execution
compatibility API has been removed; the TUI constructs no session commands and owns no registry, shell execution, or loop workflow state.
The CLI constructs and owns the interpreter lifetime; the TUI receives that
interpreter and its preinstalled observer queue, and closes only that queue.
Approval presentation consumes interpreter DTOs, and transcript renderers read detached command and file-change
details through interpreter helpers. Production UI packages import neither
`internal/session` nor `internal/agent`.

---

### 2. Prompt Interpreter

UI-independent application layer. Responsible for:

- Parsing and executing prompt-language statements
- Owning room-scoped command definitions
- Using one native definition catalog for statement dispatch and help metadata;
  debug display statements remain explicitly UI-only
- Executing shell-backed commands
- Coordinating bounded loops
- Coordinating pending barrier batches and interrupt-and-dispatch
- Owning the canonical room projection used for handoffs
- Exposing observable events and snapshots to front ends
- Preserving serialized session dispatch

See [`pkg-interpreter.md`](pkg-interpreter.md).

---

### 3. Session Controller

Central orchestrator. Responsible for:

- Dispatching structured session commands
- Routing messages to agents or channels
- Enforcing policies (capabilities, initiative levels)
- Tracking session state
- Controlling agent lifecycle

The session controller is the sole mutator of participant runtime state. It
coordinates concurrency, owns reader goroutines, and prevents invalid
transitions (for example, sends while a turn is already in flight).

---

### 4. Participant Registry

Tracks all participants in the session:

```
alias, backend, role, capabilities, initiative, status, color
```

The registry assigns each accepted participant a deterministic color. Colors
are monotonic and are not reused after removal during the session; participants
retain the assigned value as part of their identity for snapshots and history.
Neither the interpreter nor the UI selects participant colors.

Status values and their meaning:

| Status | Description |
|--------|-------------|
| `starting` | `Start()` has been called; the process is not yet confirmed live. |
| `attached` | Process is live and the agent is bound, but `AgentReady` has not yet been dispatched. `/remove` and sends are rejected in this window. |
| `idle` | No turn is active. `StartupReady` must also be set before messages can be delivered. |
| `keepalive` | Backend maintenance request in flight. No user turn is active, but the participant is temporarily non-sendable and occupies the request lane. |
| `preparing` | Committed to a send; anchor stream being established. |
| `working` | Turn in flight; agent is processing and streaming output. |
| `crashed` | Agent process exited unexpectedly. |

Session exposes `Participant(alias)` and `Participants()`, both returning
`participant.View`. The list is one locked snapshot of all registered
participants. Views carry actual status, explicit startup readiness, and turn
identity without agent handles or stream bookkeeping. Shared pure predicates
support recipient selection and execution guards; Session remains the lifecycle
and delivery authority.

The participant is the stateful runtime entity. Beyond identity and coarse
status, it carries active-turn runtime state used by the session and UI (for
example, tracked open streams while the participant is working) and enforces
runtime invariants for legal transitions.

Normal transitions include:

- `starting -> attached -> idle`
- `idle -> preparing -> working -> idle`
- `idle -> keepalive -> idle`

The UI may render `keepalive` distinctly in the roster, but it does not create
its own shared-room transcript record.

---

### 5. Agent Runtime

Manages CLI processes:

- Start and stop
- Send input to the process
- Capture and parse output
- Detect crashes and restart if needed

Agents are treated as managed processes, not API clients.
They are transport adapters with minimal internal state beyond buffering and
protocol handling.

---

### 6. Backend Adapters

Thin wrappers for each supported CLI tool. Interface:

```
start()
send_message(text)
read_output() -> text
stop()
```

Backends are treated as black boxes. Adapters handle tool-specific I/O quirks. Supported backends: Claude Code, Codex, Aider.

An adapter is intentionally stateless at the workflow level: it does not own
session or participant semantics. Runtime meaning is added one layer up by the
participant and session controller.

---

### 7. Sandbox Controller

Each agent runs inside a sandbox that constrains what it can access at the OS level. coderoom does not attempt to intercept decisions made inside the CLI tool itself. Instead, it defines the boundary within which the CLI operates.

Sandbox constraints:

- **Filesystem**: chroot or equivalent, scoped to the worktree and permitted paths
- **Network**: outbound connections restricted by policy (e.g. allow registry/CDN, deny arbitrary egress)

The sandbox is configured per agent at launch, based on the agent's role and capabilities.

Agent actions and approval requests are handled in the private channel between the human and that agent. The shared room shows only a flag when an agent requires attention:

```
[hopper requires attention]
```

The human reviews and responds in the private tab. Other agents are not exposed to the operational detail.

---

### 8. Message Router

Routes messages across channels:

- `shared`: broadcast to all agents and user
- `private`: user to one agent (includes reasoning and approval flows)
- `system`: internal session events

Rules:
- Shared room is the primary coordination layer
- No hidden agent-to-agent channels
- Reasoning and approval requests stay in private channels
- Shared room shows attention flags, not operational detail

---

### 9. Policy Engine

Controls what each agent is permitted to do at the session level:

- File write permissions (based on role)
- Initiative behaviour enforcement
- Risk boundary checks

Filesystem and network constraints are enforced by the Sandbox Controller at the OS level, not by the Policy Engine intercepting CLI behaviour.

---

## Execution Model

### Shared Worktree

All agents operate on the same git worktree. There is no patch synchronisation problem: `git diff` gives every agent and the user a consistent view of current changes. Agents interact with git directly through their CLI tools. coderoom does not wrap or duplicate git commands.

### Direct File Editing

Agents modify files directly, subject to sandbox constraints. The human retains control through git: staging, reverting, and committing happen outside the room in a normal terminal.

---

## Event Model

The system operates via discrete events. This enables replay, debugging, and session persistence.

```
SessionCreated
AgentInvited
AgentReady
MessageSent
CommandIssued
FileChanged
AgentCrashed
AgentRestarted
DecisionRecorded
```

Events are appended to `events.jsonl` in the session directory.

---

## Session State

Session state is persisted under `.coderoom/room-name/`, allowing multiple rooms per repo and rooms that reference paths outside a single repo.

```
.coderoom/
  <room-name>/
    session.json      # agents, roles, permissions, current task
    events.jsonl      # append-only event log
    decisions.md      # human-approved decisions
    agents/           # per-agent context and scratchpad
```

State includes: agents, roles, permissions, messages, decisions, and a reference to the current repo state.

---

## Runtime Flow

```
User: /task "Fix OAuth refresh test"

ada (builder):     proposes plan
turing (reviewer): critiques the approach
User: @ada proceed
ada:               implements fix (modifies repo)
User: @turing review diff
turing:            reviews using git diff
[hopper requires attention]
User: (private tab) approves test run
hopper:            executes tests, reports result to shared room
```

---

## System Model

```
agents      = managed processes
roles       = behavioral contracts
aliases     = interaction identity
messages    = routed events
git         = shared state
diff        = change context
sandbox     = OS-level boundary
controller  = session authority
participant = stateful collaborator
agent       = stateless transport adapter
human       = final decision-maker
```

---

## Open Questions

- Agent lifecycle: pause/resume without losing context
- Autonomy escalation: criteria for granting higher initiative
- Multi-session or distributed setups
- Sandbox implementation: chroot vs container vs namespace isolation
- Network policy granularity: per-agent or per-role
