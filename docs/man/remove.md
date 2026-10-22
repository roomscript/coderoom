# /remove

Stop an agent and remove it from the room.

```text
/remove <alias>
```

Ada was the last agent in this room. Removing Ada left the room empty:

Recorded excerpt ([source](examples/codex.txt)). `❯` marks user input.

```text
❯ /remove ada

[ada left]

❯ /who

[no agents]
```

The participant's process is stopped. File changes remain in the worktree.
Removal during early startup may be rejected; wait for startup to settle and
retry. An unknown alias produces an error. Process-stop failures are reported.

Pending work keeps the identity of the original participant. Removing and
reinviting the same alias cannot transfer that work to the new agent. A loop
targeting the removed participant stops when it observes the departure.

You can invite the alias again to start a new conversation. Its model settings
come from the backend defaults, and its role comes from the configuration
loaded when coderoom started.

Related: [invite](invite.md), [cancel](cancel.md), [quit](quit.md).

[Manual](index.md)
