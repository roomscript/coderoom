# /def

Save a shell-backed command for the current room.

```text
/def <name> /shell <program>
```

Save a command that prints `ready`, then run it:

Recorded excerpt ([source](examples/terminal.txt)). `❯` marks user input.

```text
❯ /def ready /shell printf 'ready\n'

[defined] /ready

❯ /ready

● shell:

  $ /ready

  status: success
  stdout:
  ready
  exit 0
```

The room confirms `[defined] /ready`. Run the saved command with
[`/ready`](user-command.md). For a project check, define `/tests` with your test
command and use it as a [`/loop`](loop.md) condition.

Names start with a letter and may contain letters, digits, hyphens, and
underscores. Names are case-sensitive. Built-in names, reserved names, and names
already defined in the room cannot be reused.

Definitions contain one shell program and take no arguments.

Definitions last until the room closes. Restarting coderoom clears them.

Related: [shell](shell.md), [saved commands](user-command.md), [loop](loop.md).

[Manual](index.md)
