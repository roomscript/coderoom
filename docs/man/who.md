# /who

List the agents currently in the room with their state, role, backend, model,
effort and resume ID.

```text
/who
```

With Ada on Codex and Turing on Claude, while Turing works on a prompt:

Recorded excerpt ([source](examples/who.txt)). `❯` marks user input.

```text
❯ /who

   alias   role     backend  model            effort  resume-id
●  ada     builder  codex    gpt-6.1-sol      low     01a16f92-ede9-7a70-95e0-6bce9d09645b
◆  turing  none     claude   claude-opus-5-5  medium  f7c3fa70-6559-43ed-872c-04ca92b2a3bc
```

The table is sorted by alias. The first column uses the participant display
glyphs, with `◆` for working. A participant without a role shows `none`. Model
and effort show `<backend>-managed` when the backend reports no value, and `-`
marks a value that is not known yet, such as for an agent still starting.

`resume-id` is the full ID the backend CLI accepts with `--resume`: the Codex
thread ID or the Claude session ID. When the table is wider than the terminal,
each participant is shown as a block with one field per line. The transcript
export (Ctrl+G) always uses the table, with IDs on one line.

After both agents leave:

```text
❯ /who

[no agents]
```

The output is a snapshot: it keeps the values captured when the command ran,
even after participants change or leave. This command leaves participants
unchanged; [`/model`](model.md) changes model settings.

[Manual](index.md)
