# /cancel

Request interruption of an agent's current work and keep it in the room.

```text
/cancel <alias>
```

Ada was using Claude Code. The cancellation was sent while the calculation
was in progress:

Recorded excerpt ([source](examples/claude.txt)). `❯` marks user input.

```text
❯ @ada Calculate pi to 100 decimal places and verify the result. Do not use tools.
  → ada

❯ /cancel ada

[→ ada] cancel requested

▸ claude: turn interrupted
```

Interruption is best-effort and asynchronous. The backend can still have work
in flight when the request returns. Watch the participant's status before
assuming the turn has ended. Unknown or unavailable participants produce an
error. File changes already made remain in the worktree.

Once Ada is idle, you can send another task in the same conversation.

Use [`/remove`](remove.md) to stop and remove a participant.

Ctrl+C in compose focus clears input or discards a staged message. See
[keyboard controls](room.md#keyboard-controls).

[Manual](index.md)
