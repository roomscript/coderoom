# Coming from Claude Code

coderoom runs a Claude Code process for each participant you invite. Each
participant has a name, its own conversation, and access to the shared worktree.

## Start with your existing setup

The Claude backend uses the `claude` executable on `PATH` in stream-json mode.
Complete Claude Code setup and authentication before starting. Launch the room
with `CODEROOM_BACKEND=claude coderoom` to use Claude as your default. Participant
files can choose another backend. See [backend selection](participants.md#backend-selection).

Each participant starts with Claude Code's configured model and effort.
coderoom appends its participant identity and role prompt to Claude's system
prompt. Claude Code handles its own configuration and repository instructions.

## What changes

| Familiar task | In coderoom |
| --- | --- |
| Send a task | Invite an agent, then address it: `@ada <text>`. |
| Split work | Invite separate named participants and give each a focused task. |
| Review another agent's work | Ask a reviewer to inspect `git diff`. Use `/handoff` for additional context. |
| Change model or effort | Use `/model ada` to list choices, then `/model ada <model> [<effort>]`. |
| Approve tool use | Select an option in the room's approval prompt. |
| Interrupt work | Use `/cancel ada`. Ctrl+C in the composer clears input. |
| Repeat a task against a check | Use a shell-backed definition and `/loop`. |

Slash commands at the start of input belong to coderoom. Use `/help` for its
command list. coderoom supplies the terminal interface and keyboard controls.

The adapter disables Claude's `AskUserQuestion` tool, background tasks,
monitoring and scheduling tools, notification and remote-trigger tools, and
tools for contacting other local sessions. Assign work through room input and
coordinate participants through coderoom. Tool approval requests still use the
room's approval flow.

Model changes keep the conversation and happen without a model turn. See
[`/model`](model.md) for supported choices and availability rules.

## Context and lifetime

Participants share files. Their conversation histories are separate. Use a
prompt or handoff to give a participant the context it needs. Read
[message delivery](room.md#conversations-and-messages).

Removing and reinviting a participant starts a fresh conversation. coderoom has
no command to resume a previous room or import an existing Claude Code session.
Files already edited remain in the worktree.

Continue with [your first room](first-room.md) or the [manual](index.md).
