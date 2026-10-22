# Broadcast

Enter plain text to send a prompt to the room's participants:

```text
<text>
```

Ada and Turing were both in the room for this broadcast:

Recorded excerpt ([source](examples/codex.txt)). `❯` marks user input.

```text
❯ Reply with exactly: ready.
  → ada      → turing

● ada:

  ready.

● turing:

  ready.
```

With one agent, plain text goes to that agent. With several agents, each
receives the prompt in its own conversation and can reply.

The broadcast goes to the agents in the room when you press Enter, except
crashed agents. Agents invited later do not receive it. A broadcast waits for
those agents to become ready. An agent that leaves during the wait is dropped
from the recipients. If no recipients remain, the broadcast fails.

Use Esc to edit staged input, Ctrl+C to discard it, or Ctrl+X to request
interruption and send when ready. See [waiting input](room.md#waiting-input).

Related: [send](send.md), [handoff](handoff.md).

[Manual](index.md)
