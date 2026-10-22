# Send to an agent

Address one participant with `@`:

```text
@<alias> <text>
```

After inviting Ada, send a short prompt:

Recorded excerpt ([source](examples/codex.txt)). `❯` marks user input.

```text
❯ @ada Reply with exactly: Hello from Codex.
  → ada

● ada:

  Hello from Codex.
```

The addressed agent receives the prompt. By default, other agents receive no
listener notice. [`send-notices`](policy.md#send-notices) enables those notices
for subsequent sends. The room displays the interaction for you.

Everything after the alias is sent to the agent as text, including any slash
commands written inside the prompt.

If the target is busy or starting, the message waits in the staged composer.
An unknown or crashed target produces an error. The message stays tied to the
original agent; removing and reinviting its alias cannot redirect it to
the replacement.

Use Esc to edit staged input, Ctrl+C to discard it, or Ctrl+X to request
interruption and send when ready. See [waiting input](room.md#waiting-input).

Related: [broadcast](broadcast.md), [handoff](handoff.md), [cancel](cancel.md).

[Manual](index.md)
