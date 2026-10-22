# /handoff

Send one agent's last completed reply shown in the room to another agent.

```text
/handoff <from> <to>
```

This recording uses echo agents, which repeat their input. Ada replied with
three lines, including an indented line. The handoff shows the reply it sent:

Recorded excerpt ([source](examples/room-output.txt)). `❯` marks user input.

```text
❯ @ada First line
    nested line
  Last line
  → ada

↦ ada:

  First line
    nested line
  Last line

❯ /handoff ada turing
  → turing

[handoff ada -> turing]
  ↦ source: ada latest output
  > First line
  >   nested line
  > Last line
```

Both agents must already be in the room and have different aliases. The handoff
waits for readiness and for an active source turn to finish. It then selects
the source's latest completed output and sends it to the destination as a new
turn. The room shows a handoff preview.

The `↦` marker identifies the reply currently available for handoff. In the
preview, `↦ source: ada latest output` names the source of the transferred text.

The handoff includes the source's last completed reply shown in the room. If a
turn has several reply messages, it uses the last completed one. Conversation
history, reasoning, and tool details stay outside the handoff. Files are already
shared through the worktree. Give the destination a clear task when you need a
specific action, such as reviewing the current diff.

A missing or crashed source or destination produces an error. A source with
no completed reply shown in the room also produces an error. The source and
destination stay fixed while the handoff waits. Removing and reinviting either
alias cannot redirect it to the new agent.

The handoff also waits for other agents that were ready when you submitted it.
If one becomes busy before delivery, it can delay the handoff. Only the
destination receives the reply.

Staged handoffs use the controls described under [waiting input](room.md#waiting-input).

Related: [send](send.md), [broadcast](broadcast.md).

[Manual](index.md)
