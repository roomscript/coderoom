# /loop

Repeat an agent task and check the result with a saved shell command.

```text
/loop @<alias> <prompt> /until /<name> /max <turns>
```

This recording uses a check that always passes, so it ends after one turn.
Ada was already in the room. For real work, replace `true` with your project
check, such as `go test ./...`.

Recorded excerpt ([source](examples/claude.txt)). `❯` marks user input.

```text
❯ /def passes /shell true

[defined] /passes

❯ /loop @ada Reply with exactly: checked. Do not use tools. /until /passes /max 1

[loop] turn 1/1 sent to @ada

↦ ada:

  checked.

● shell:

  $ /passes

  status: success
  exit code: 0
  stdout:
  (none)
  stderr:
  (none)
  error:
  (none)
  exit 0

[loop] condition /passes succeeded
```

The `↦` marker identifies the [reply available for handoff](handoff.md).

The condition must be an existing shell-backed definition. The turn limit must
be a positive integer.

## Each attempt

1. Send the prompt to the target agent.
2. Wait for the agent turn to end.
3. Run the condition command.
4. Stop if the condition succeeds. Otherwise, send another prompt with the
   original task and the failed check's status, exit code, stdout, stderr, and
   error details.

The room displays attempts and check results. Exit code zero passes the check.
The loop checks after the first turn, so it always starts with an agent attempt.
`/max 3` allows up to three agent turns. Reaching the limit ends the loop even
if the condition still fails.

## Failure

The loop stops when its target crashes or is removed, when delivery fails, or
when the condition is cancelled. Reinviting the same alias creates a new agent;
the existing loop keeps its original target identity.

Loops support one agent prompt and one shell-backed condition.

Related: [def](def.md), [shell](shell.md), [cancel](cancel.md), [remove](remove.md).

[Manual](index.md)
