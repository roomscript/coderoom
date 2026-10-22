# /who

List the agents currently in the room and their backends.

```text
/who
```

With Ada and Turing both using Codex:

Recorded excerpt ([source](examples/codex.txt)). `❯` marks user input.

```text
❯ /who

[agents] ada (codex), turing (codex)
```

The list is sorted by alias. An empty room shows `[no agents]`. This command
reads the current list and leaves participants unchanged. The participant
display shows their activity; [`/model`](model.md) shows model settings.

[Manual](index.md)
