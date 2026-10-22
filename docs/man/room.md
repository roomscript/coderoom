# Working in a room

Each participant is a named agent with its own conversation. All participants
work in the directory where you launched coderoom.

## Shared files

Agents edit the same worktree. A change made by one agent is immediately
available to the others. Give agents focused tasks and coordinate overlapping
edits. Use `git diff` to inspect the result. Stage, revert, and commit through
your usual git workflow.

A role prompt tells an agent how to behave. It does not create filesystem
isolation or enforce read-only access. Backend permissions still apply to the
agent's tools. [`/shell`](shell.md) runs with your local permissions.

## Conversations and messages

The terminal shows the room's activity. Delivery into agent conversations
depends on what you submit:

| Input | Who receives it |
| --- | --- |
| `@ada <text>` | Ada. With `send-notices` enabled, the other agents in the room when you submit also receive a notice about the prompt. |
| Plain text | The agents in the room when you submit, except crashed agents. |
| `/handoff ada turing` | Turing receives Ada's last completed reply shown in the room. |

Each agent keeps its own conversation during its time in the room. Inviting
another agent starts a fresh conversation. Seeing an answer in the terminal
does not deliver that answer to every agent. Use a prompt or handoff to supply
the context another agent needs.

Agent responses remain text. coderoom executes room commands submitted by the
user. It does not execute command text found in agent responses.

## Waiting input

Sends, broadcasts, and handoffs can wait for agents to become ready. The composer
shows a staged message while it waits. An agent may still be starting or
working. Its CLI may also be briefly busy between user tasks.

In compose focus:

- Esc takes the staged message back for editing.
- Ctrl+C discards the staged message.
- Ctrl+X requests interruption of the agents it is waiting for, then sends when
  they become ready.

A waiting message keeps the agents chosen when you pressed Enter. Agents
invited later do not receive it. Removing and reinviting an alias starts a new
agent. The waiting message cannot transfer to that new agent.

[`/model`](model.md) changes require an idle agent and reject a busy target.

## Approvals

Backend approval requests appear in the room, one prompt at a time. Read the
request, select an offered option with the arrow keys or j/k, and press Enter.
The options depend on the backend and request. Other agents receive messages
through the delivery rules above.

Esc or Ctrl+C closes the prompt and sends Cancel if that option is offered.
Otherwise, it sends Decline. The requested action is refused. Cancel can also
interrupt the agent's turn.

PgUp and PgDn scroll the request details.

## Keyboard controls

| Key | Action |
| --- | --- |
| Enter in compose focus | Submit input. |
| Ctrl+O | Switch between compose and history focus. |
| PgUp / PgDn in compose or history focus | Scroll the transcript. |
| Ctrl+G in compose focus | Open `$EDITOR` for multiline input. |
| Ctrl+V in compose focus | Paste from the system clipboard. |
| Ctrl+C in compose focus | Clear the composer, or discard staged input. |
| Arrow keys in history focus | Move the text cursor. The view follows it. |
| Home / End in history focus | Move to the start or end of the current line. |
| Shift+arrows in history focus | Select text. |
| Ctrl+Shift+Left / Right in history focus | Select by word. |
| Ctrl+G in history focus | Open the transcript in `$EDITOR` for reading. |
| Ctrl+C in history focus | Copy selected text. With no selection, do nothing. |
| Esc in history focus | Clear the selection. With no selection, return to compose focus. |
| Keys during an approval prompt | See [approvals](#approvals). |

The separator label shows the current focus. [`/help`](help.md) lists the
controls in the room.

## Cancellation and lifetime

[`/cancel`](cancel.md) requests interruption and keeps the participant in the
room. The backend may still have work in flight when the request returns.
[`/remove`](remove.md) stops the participant and removes it.
[`/quit`](quit.md) shuts down the room and its agents. These operations leave
existing file changes in place.

coderoom has no room-resume command. A new launch starts fresh room state.
Command definitions, enabled policies, and session model changes belong to the
running room. Participant files and role prompts remain on disk. Backend CLIs
manage their own local records; those records do not restore a coderoom room.

[Manual](index.md)
