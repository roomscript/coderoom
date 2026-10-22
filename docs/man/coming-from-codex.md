# Coming from Codex

coderoom starts a Codex conversation for each participant you invite. You can
give them different tasks and pass results between them.

## Start with your existing setup

The Codex backend uses `npx @openai/codex app-server`. It needs `npx` on `PATH`
even if your usual `codex` executable came from another installer. Complete
Codex authentication before starting. See [installation](install.md).

The room starts each participant with Codex's configured model and effort.
Participant identity and role instructions are supplied as thread developer
instructions. Your backend setup continues to control Codex's own configuration
and repository instruction loading.

## What changes

| Familiar task | In coderoom |
| --- | --- |
| Send a task | Invite an agent, then address it: `@ada <text>`. |
| Work with several conversations | Give each participant an alias. They share the worktree. |
| Review another agent's work | Ask a reviewer to inspect `git diff`. Use `/handoff` for additional context. |
| Change model or effort | Use `/model ada` to list choices, then `/model ada <model> [<effort>]`. |
| Approve a tool action | Answer the approval prompt in the room. |
| Interrupt work | Use `/cancel ada`. Ctrl+C in the composer clears input. |
| Reuse a check | Save it with `/def` and run it by name. |

Slash commands at the start of input belong to coderoom. Use `/help` for its
command list. Tasks such as review can be expressed as ordinary prompts to an
agent. coderoom provides its own terminal interface and keyboard controls.

Model changes keep the conversation. Codex applies a change through a short
acknowledgement turn, which consumes quota. See [`/model`](model.md) for details.

## Context and lifetime

Each participant has its own conversation. Other agents receive context through
addressed messages, broadcasts, and handoffs. The room transcript alone does
not give an agent every conversation. Read [message delivery](room.md#conversations-and-messages).

Removing and reinviting a participant starts a fresh conversation. coderoom has
no command to resume a previous room or import an existing Codex conversation.
Files already edited remain in the worktree.

Continue with [your first room](first-room.md) or the [manual](index.md).
