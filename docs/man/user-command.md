# Run a saved command

Run a shell command you defined in this room:

```text
/<name>
```

See [`/def`](def.md) for a recorded example that saves `/ready` and runs it.

The saved program runs with the same directory, environment, permissions, and
result handling as [`/shell`](shell.md). Each invocation executes the program
again. A name that has not been defined produces an error. Saved commands take
no arguments.

Definitions belong to the current room and disappear when it closes. See
[`/def`](def.md) for naming and definition rules.

[Manual](index.md)
