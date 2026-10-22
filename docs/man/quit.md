# /quit

Close the room and stop its agents.

```text
/quit
```

coderoom shuts down room work and agent processes before returning to the shell.
File changes remain in the worktree. Inspect them with your usual git tools.

The next launch starts a new room. Conversations, saved command definitions,
enabled policies, and session model changes start fresh. Participant files and
role prompts remain on disk. See [room lifetime](room.md#cancellation-and-lifetime).

Related: [remove](remove.md), [cancel](cancel.md).

[Manual](index.md)
