# coderoom manual

coderoom runs named coding agents in one terminal room. Each agent has its own
conversation. All agents work on the same files.

## Getting started

- [Install coderoom](install.md)
- [Start your first room](first-room.md)
- [Coming from Codex](coming-from-codex.md)
- [Coming from Claude Code](coming-from-claude.md)

## Working in a room

- [Files, conversations, approvals, and keyboard controls](room.md)
- [Participants, roles, and backend selection](participants.md)

## Commands

Enter these in the room. Angle brackets mark values to supply. Square brackets
mark optional arguments. Leave the brackets out when entering a command.

Recorded excerpts link to their [source transcripts](examples/README.md). `❯`
marks the start of user input. Multiline input continues on indented lines.
The remaining text is captured room output. Model
names and agent replies reflect the recording session and may differ in yours.

| Command | Purpose |
| --- | --- |
| [`/invite <alias>`](invite.md) | Start an agent. |
| [`@<alias> <text>`](send.md) | Send a prompt to one agent. |
| [`<text>`](broadcast.md) | Send a prompt to all agents. |
| [`/handoff <from> <to>`](handoff.md) | Send one agent's latest output to another. |
| [`/who`](who.md) | List agents with their state, role, settings and resume ID. |
| [`/model <alias> [<model> [<effort>]]`](model.md) | Show or change model settings. |
| [`/cancel <alias>`](cancel.md) | Request interruption of an agent's work. |
| [`/remove <alias>`](remove.md) | Stop and remove an agent. |
| [`/shell <program>`](shell.md) | Run a shell program. |
| [`/def <name> /shell <program>`](def.md) | Save a shell command for this room. |
| [`/<name>`](user-command.md) | Run a saved command. |
| [`/loop @<alias> <prompt> /until /<name> /max <turns>`](loop.md) | Repeat a task until a check passes or the turn limit is reached. |
| [`/policy enable <name>`](policy.md) | Enable a room policy. |
| [`/help`](help.md) | Show commands and keyboard controls. |
| [`/quit`](quit.md) | Close the room. |

Developer diagnostics are described in [debug commands](debug.md).

[Project README](../../README.md)
