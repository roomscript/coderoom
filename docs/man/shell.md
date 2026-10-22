# /shell

Run a shell program in the directory where you launched coderoom.

```text
/shell <program>
```

Print a short message. The room shows the command, status, output, and exit
code.

Recorded excerpt ([source](examples/terminal.txt)). `❯` marks user input.

```text
❯ /shell printf 'ready\n'

● shell:

  $ printf 'ready\n'

  status: success
  stdout:
  ready
  exit 0
```

For a project check, run `/shell git diff --stat` or `/shell go test ./...`.

coderoom executes the program through `/bin/sh -c` with its inherited environment
and your local permissions. Shell syntax such as pipes and redirection works.
Your interactive shell's aliases and startup configuration may differ.

The room shows captured output and the result after execution. Exit code zero
means success. A nonzero exit code means failure. Launch errors are reported.
The result is visible to you in the room; it is not automatically sent to agent
conversations. Loops explicitly supply failed check output to their target.

Agent tool approvals apply to backend tool requests. `/shell` executes your
submitted program directly. [`/cancel`](cancel.md) targets agents. Closing the
room cancels its shell work.

Related: [def](def.md), [saved commands](user-command.md), [loop](loop.md).

[Manual](index.md)
