# coderoom: Concept

## The Problem

A single LLM does not consistently produce the quality needed for production code. Different models have different blind spots. A reviewer that did not write the code catches things the author missed. The same reason human code review works.

Working with multiple LLMs in structured roles materially improves results. One implements. The second reviews, identifying deviations from the design. But this pattern is ad hoc today: it requires manual coordination, separate sessions, and no shared context.

Working with LLMs well requires structure: clear roles, controlled autonomy, and a shared workspace where the human remains the decision authority. coderoom formalises that structure.

---

## The Quality Signal

The same pattern that works for human code review works for multi-LLM review: a deterministic quality function compresses the review surface. Instead of reading every line, the human validates whether the quality signal is moving in the right direction.

In practice:

- LLM 1 generates code
- LLM 2 reviews against the design and quality criteria
- A deterministic function (lint, test coverage, static analysis) evaluates the output
- Results feed back to LLMs for self-correction
- Human reviews the signal and the acceptance criteria, not every line
- Human assesses whether the change degrades the architecture and instructs refactors accordingly.

The quality function changes by domain. The coordination pattern does not.

---

## What coderoom Is

coderoom is a CLI-native multi-agent coding workspace. It lets a developer invite multiple AI coding tools into a shared session, assign them roles, control their autonomy, and coordinate collaboration through structured communication channels.

It is not another coding agent. It is a coordination layer for multiple agents working together in a controlled environment.

The design goal: make the terminal feel like a collaborative workshop where agents are named collaborators with defined responsibilities, not interchangeable tools.

---

## Agent Model

Each participant in a session is defined by:

- **Backend**: the underlying CLI tool (Claude Code, Codex, Aider, etc.)
- **Alias**: a human-friendly name for interaction (e.g. `ada`, `turing`)
- **Role**: a behavioral contract (builder, reviewer, tester, architect)
- **Capabilities**: what the agent is allowed to do (read, write, test, shell)
- **Initiative level**: how autonomously the agent acts

Example session participants:

```
alias   backend       role      capabilities     initiative
ada     claude-code   builder   write+test       active
turing  codex         reviewer  read+comment     suggestive
hopper  aider         tester    test-only        manual
```

---

## Roles

Roles define how an agent behaves, independent of which model backs it.

| Role | Responsibility |
|---|---|
| Builder | Writes and modifies code |
| Reviewer | Critiques changes, identifies issues |
| Tester | Runs tests, validates behaviour |
| Architect | Proposes system design decisions |
| Security Reviewer | Evaluates risks and vulnerabilities |
| Documenter | Maintains documentation |

Roles act as behavioral contracts and permission hints.

The backend agent and the participant are distinct concepts:

- the backend agent is the transport adapter to an external CLI
- the participant is the stateful collaborator visible in the room

Runtime state such as working/idle and active turn activity belongs to the
participant. Session coordinates and mutates that state; the participant
validates transition invariants; the UI consumes the resulting state.

---

## Initiative Levels

Agents operate under controlled autonomy. The level determines how proactively an agent acts without being explicitly addressed.

| Level | Behaviour |
|---|---|
| Manual | Only acts when explicitly addressed |
| Active | May propose changes |
| Autonomous | May act within policy constraints |

Principle: start explicit, grant autonomy gradually.

---

## Communication Model

The system separates communication into distinct channels:

1. **Shared Room**: visible to all agents and the user; primary coordination space
2. **Private Agent Channel**: user to single agent; it includes reasoning; not visible to others

Rules:
- Agents see all shared messages
- No agent-to-agent private messaging
- Reasoning does not leak into the shared room
- Agents may ACK messages in the shared room for coordination

The UI should consume this communication through session events and participant
state. It should never invoke agents directly.

---

## Interaction Model

Users interact with named collaborators, not model names.

```
@ada implement the minimal fix
@turing review Ada's diff for edge cases
@hopper run the test suite
```

The CLI provides structured commands for session management and coordination:

```
/create room bugfix-auth

/invite claude-code as ada with role builder
/invite codex as turing with role reviewer

/task "Fix failing OAuth refresh test"
```

---

## Design Principles

1. Human-in-the-loop authority
2. Agents are collaborators, not owners
3. Shared room is the coordination layer
4. Git is the source of truth
5. Explicit before autonomous
6. Transparency over hidden behaviour
7. Structure over free-form chat
