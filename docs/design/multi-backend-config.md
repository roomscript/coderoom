# Design: multi-backend participant configuration

Status: **draft**. Decisions agreed in discussion are marked **Decided**;
everything else is a proposal.

With the Claude Code adapter (`pkg-agent-claude.md`), coderoom has more than
one real agent backend. This document covers how a participant's backend is
configured, validated, built and shown.

---

## Goals

- Choose a backend per participant: `codex` or `claude`.
- Let users set their own default backend.
- Catch configuration mistakes when coderoom starts, not at the first
  `/invite`.
- Report missing external tools early without slowing startup down.
- Keep the knowledge of a backend (its name, what it needs to run) in its agent
  package.
- Show which backend each participant runs.

## Non-goals

- Selecting the backend from `/invite` (for example `/invite ada claude`).
  Possible later; not needed now. **Decided.**
- Per-participant model or effort settings. Each backend uses the CLI's
  configured default, as Codex does today. **Decided.**
- Reloading configuration during a session. **Decided.**
- Configuring the echo backend. Echo is for testing only and is selected by the
  `EchoInvites` policy, never by a file or the environment. **Decided.**

---

## Today

- `config.Config.ForParticipant(alias)` reads
  `.coderoom/participants/<alias>.yaml` and the role prompt **at invite
  time**. A missing file gives identification-only config.
- The YAML accepts `alias` and `role` and rejects unknown keys.
- `InviteCommand` passes `AgentBackendDefault`, or `AgentBackendEcho` when the
  `EchoInvites` policy is on, to the factory in `cmd/coderoom`. The factory
  builds echo or Codex. It exists twice, with and without `--agent-log`.
- The room shows `[ada starting]`; `/who` shows `[agents] ada, tim`.

---

## Configuration sources

The backend for a participant comes from, in order:

1. `backend` in `.coderoom/participants/<alias>.yaml`
2. the `CODEROOM_BACKEND` environment variable
3. `codex`

**Decided.** An alias with no YAML file still gets a backend, from 2 or 3.

The environment variable is per user, so the same repository can run with
different defaults for different people (for example someone who only has
Claude installed). The YAML is per repository.

`config` does not read the environment itself. `cmd/coderoom` reads
`CODEROOM_BACKEND` and passes it to the loader as the default. This keeps
`config` free of process state and easy to test.

Each resolved value records its source (the file path, or the environment
variable), so errors can name where the value came from.

### YAML

```yaml
alias: ada
role: builder
backend: claude
```

`backend` is optional. `config` stores it as a plain string and does not know
which names exist. **Decided:** `config` depends on nothing in coderoom.

---

## Loading model

**Decided:** load everything once at startup.

`config` gets a load-all entry point, roughly:

```go
type Defaults struct {
	Backend string // from CODEROOM_BACKEND; empty means "codex"
}

// Load reads every participant file and every role prompt they reference.
func Load(root string, defaults Defaults) (*Snapshot, error)

// Participant returns the loaded config for alias, or the default config
// when no file exists for it.
func (s *Snapshot) Participant(alias string) (ParticipantConfig, error)

// ConfiguredBackends maps each source ("<path>/ada.yaml",
// "CODEROOM_BACKEND") to the backend name it requests.
func (s *Snapshot) ConfiguredBackends() map[string]string
```

`ParticipantConfig` gains `Backend` (the requested name) and its source.

`Load` fails on anything that fails today at invite (malformed YAML, alias
and filename mismatch, missing role prompt), but for every file, at startup.
`Participant` only rejects an invalid alias.

### Edits during a session

**Decided:** the snapshot is not reloaded. When `/invite` runs, coderoom checks
whether **the invited participant's** sources changed since startup and, if
so, shows a notice. The invite continues with the loaded config.

Sources for a participant are its YAML file and the role prompt it uses. A
change is any of:

- the file's content differs from what was loaded (content hash, not mtime);
- a file was created for an alias that had none at startup;
- a file loaded at startup was deleted.

The check only compares; it does not parse the new content, so a broken edit
does not block an invite. Proposed notice:

```text
[ada.yaml changed since startup; using the loaded version. Restart coderoom to apply it]
```

---

## Agent identity and dependencies

**Decided:** what a backend is called and what it needs to run belong to its
agent.

### Name

`agent.Agent` gains:

```go
// Name returns the backend's canonical name, as used in configuration and
// shown to users.
Name() string
```

Every agent implements it, including echo and test fakes, because every
participant is shown with a name. The configured string is matched against
it; neither `config` nor the registry keeps its own copy of the names.

### External dependencies

Agents that rely on tools outside coderoom implement an optional interface.
Agents without external tools (echo, test fakes) do not. **Decided.**

```go
// ExternalDependencies is implemented by agents that rely on tools outside
// coderoom.
type ExternalDependencies interface {
	ExternalDependencies() []Dependency
}

// Dependency is one check. Checks run in order; later checks may assume
// earlier ones passed.
type Dependency struct {
	Description string                          // "npx is installed"
	Verify      func(ctx context.Context) error // nil means satisfied
}
```

| Backend | Checks |
|---|---|
| `codex` | `npx` is installed; `npx <package>@<version> --version` runs; later: a valid session |
| `claude` | `claude --version` runs; later: a valid session |

coderoom does not sign users in, so a valid session check is the natural next
entry on each list.

Checks are built from the same command the adapter launches (package, version,
`CODEX_VERSION_OVERRIDE`, a `WithCommand` override), so a check cannot pass for
one command while the adapter starts another.

---

## The registry

**Decided:** `internal/agent/registry` owns the list of configurable backends,
builds agents at invite, and validates configuration. `cmd/coderoom` builds it
and no longer imports adapter packages.

It depends on configuration only through an interface of built-in types, which
`config.Snapshot` satisfies without either package importing the other:

```go
// Configuration is what Validate needs from the loaded config.
type Configuration interface {
	ConfiguredBackends() map[string]string // source -> requested name
}
```

Sketch:

```go
// Options are the session facilities every backend accepts.
type Options struct {
	Context          context.Context
	ApprovalListener agent.ApprovalListener
	SystemPrompt     string
	Log              io.Writer // --agent-log; nil when off
}

type Constructor func(cwd string, opts Options) agent.Agent

func New(cwd string, ctors ...Constructor) (*Registry, error) // rejects duplicate names
func (r *Registry) Build(name string, opts Options) (agent.Agent, error)
func (r *Registry) ValidateConfig(cfg Configuration) error
func (r *Registry) VerifyDependencies(ctx context.Context, name string) error
```

The registry learns each name by building a bare instance (`ctor(cwd,
Options{})`) and calling `Name()`. This relies on constructors having no side
effects: today `codex.New` and `claude.New` only set fields and start nothing.
That becomes a written requirement for every adapter.

Echo is not registered, so `codex` and `claude` are the only valid names in a
file or in `CODEROOM_BACKEND`.

`--agent-log` works for every backend: each constructor wraps `Options.Log`
in its own protocol log observer (`codex.NewLogObserver`, and a new
`claude.NewLogObserver`).

---

## Validation

Two kinds of check, with different costs, handled differently. **Decided.**

### Configuration validation: aborts startup

Cheap, local and deterministic, so coderoom fails fast and clearly:

- every file loads (`config.Load`);
- the default backend is a known name, when `CODEROOM_BACKEND` is set;
- every backend named in a participant file is a known name.

The default is checked separately: if every file names a backend, a bad
default would otherwise only show up at the first `/invite` of an alias
without a file. An unknown default aborts startup even when the repository has
no participant files.

`ValidateConfig` collects every problem before returning, sorted by source, so
one startup reports all broken files:

```text
coderoom: configuration errors:
  .coderoom/participants/ada.yaml: unknown backend "abc" (known: claude, codex)
  CODEROOM_BACKEND: unknown backend "cluade" (known: claude, codex)
```

### Dependency checks: reported in the room

External checks can be slow (`npx` downloads the package the first time) and
can depend on the network, so they never block startup or `/invite`. They
exist only to warn early. **Decided.**

- After startup, the session runs the checks for the backends in use: every
  backend named in a file, plus the default, since any alias without a file
  gets it. One goroutine per backend; each backend's checks run in order and
  stop at the first failure.
- A failure is shown as a system line in the room; a pass shows nothing:

  ```text
  [codex unavailable: npx is installed: not found in PATH]
  ```

- There is no timeout. A check that hangs only means no early warning, which
  is today's behaviour. Checks run under the session context, so their
  processes are killed when coderoom exits.
- `/invite` never waits for a check and never re-runs one. It starts the agent
  as today; a missing tool shows up as the adapter's own start error.
- Inviting a participant **cancels the check for its backend**. Starting the
  agent tests more than the check does, a late "unavailable" line would
  contradict what the user already sees, and two first-time `npx` runs of the
  same package can conflict in the npm cache. Checks for other backends go on.
- A result that arrives after its check was cancelled is dropped.
- With the `EchoInvites` policy on, no checks run.

The session owns the checks: it starts them with the registry, keeps one
cancel function per backend, and calls it at invite. No results are stored,
and the registry stays a stateless table.

---

## Building the agent at invite

- `InviteCommand` gets the participant's config from the snapshot.
- The session cancels the dependency check for the participant's backend, if
  one is running.
- When `EchoInvites` is on, the factory builds echo directly. Otherwise it
  calls `Registry.Build` with the configured backend.
- The factory returns `(agent.Agent, error)`. On error, the invite removes the
  participant it already added and reports the error.
- After the factory returns, the session copies `a.Name()` into
  `participant.View` before `ParticipantStatusChanged` and `AgentStarting`
  are sent. The view holds the name the agent **declares**, not the name that
  was requested.

The two factories in `cmd/coderoom` become one thin wrapper that maps session
facilities to `registry.Options`.

---

## Display

The name shown is `View`'s copy of `Agent.Name()`. It is shown always, for
every backend, so it is never hidden. **Decided.**

| Where | Today | Proposed |
|---|---|---|
| Room, on invite | `[ada starting]` | `[ada starting · claude]` |
| `/who` | `[agents] ada, tim` | `[agents] ada (claude), tim (codex)` |

With `EchoInvites` on, both show `echo`, which is what actually runs.

---

## Delivery

Small steps to main, each leaving it working (see
`internal/agent/claude/PLAN.md`):

1. `agent.Agent` gains `Name()`; `agent.ExternalDependencies` and
   `agent.Dependency` are added; Codex implements both, echo and test fakes
   implement `Name()`.
2. `config`: `backend` field, `Defaults`, `Load`, `Snapshot`,
   `ConfiguredBackends`, change detection. Session switches to the snapshot.
3. `internal/agent/registry`; configuration validation at startup;
   dependency checks reported in the room, cancelled by `/invite`; one factory
   in `cmd/coderoom`. Only `codex` is registered.
4. Display: the name in `View`, the starting line, `/who`.

Registering Claude follows as its own step.

---

## Changes outside the agent packages

| Package | Change |
|---|---|
| `internal/agent` | `Agent.Name()`; `ExternalDependencies`, `Dependency`; test fakes implement `Name()` |
| `internal/agent/{codex,claude,echo}` | `Name()`; Codex and Claude list their dependencies; `claude.NewLogObserver` |
| `internal/agent/registry` (new) | constructors, `Build`, `ValidateConfig`, `VerifyDependencies` |
| `internal/config` | `backend` field, `Defaults`, `Load`, `Snapshot`, change detection |
| `internal/session` | uses the snapshot; `AgentBackend` removed; factory returns an error; invite rollback; name copied into `View`; change notice; runs and cancels dependency checks; a "backend unavailable" event |
| `internal/participant` | `View` gains the backend name |
| `internal/room`, `internal/interpreter/std` | starting line, `/who`, the unavailable line; their tests |
| `cmd/coderoom` | reads `CODEROOM_BACKEND`, loads and validates at startup, builds the registry, one factory |
| `docs/participants.md`, `docs/design/participant-roles.md` | `backend` field, startup loading, the change notice |

---

## Open questions

1. **System line wording and record type** for "backend unavailable" in the
   room. Proposed: a session event `BackendUnavailable{Backend, Check, Err}`,
   shown as `[codex unavailable: npx is installed: not found in PATH]`.
