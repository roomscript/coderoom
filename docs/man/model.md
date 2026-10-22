# /model

Show or change a participant's model and reasoning effort.

```text
/model <alias> [<model> [<effort>]]
```

Use `/model ada` to list settings and available choices. The listing included
`sonnet`, which supports effort `low`. Changing both produced:

Recorded excerpt ([source](examples/claude.txt)). `❯` marks user input.

```text
❯ /model ada sonnet low

[ada model changed · model: claude-sonnet-5-5 · effort: low]
```

Use `/model ada sonnet` to change only the model.

An unsupported model or effort produces an error with valid choices. A
model-only change sends no effort setting. The backend reports the resulting
effort, which may differ from the previous value.

Listing works while the participant is busy. Changing settings requires an
idle, ready participant. Busy, starting, or crashed targets reject changes.
Changes are neither queued nor used to interrupt work.

The room reports the result when the backend responds. The conversation stays
in place. The change lasts until the participant is removed. Participant files
and CLI configuration stay unchanged. Reinviting starts with backend defaults.
Values the backend does not report appear as `codex-managed` or `claude-managed`.

## Backend behavior

Codex applies a change through a short acknowledgement turn. This consumes quota
and adds an exchange to the conversation. The reply is hidden. coderoom reads
the settings afterwards, including after failure. A failed or interrupted
change may still have applied the requested values; check the reported settings.

Claude changes settings without a model turn.

Related: [participant setup](participants.md), [cancel](cancel.md).

[Manual](index.md)
