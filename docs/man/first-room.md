# Your first room

This walkthrough starts an agent, requests a small change, and asks a second
agent to review it. Finish [installation](install.md) first.

The blocks below contain commands and prompts to enter. Recorded command
examples are linked from the [manual index](index.md#commands).

## Open the room

From the repository you want to change, launch `coderoom`. To use Claude Code
as your default, launch it with `CODEROOM_BACKEND=claude coderoom`.

Enter room commands in the composer and press Enter. Start an agent:

```text
/invite ada
```

The room shows the backend as the agent starts. When it joins, the room shows
its model and effort. An alias such as `ada` works without a configuration file.
Existing participant files supply any configured role and backend.

## Request a small change

Use a focused task from your project. For example:

```text
@ada Fix the typo in the README installation section. Keep the change limited to that typo.
```

Ada works in the repository and streams its response into the room. File edits
appear directly in your worktree. If the backend requests approval, select an
option with the arrow keys and press Enter.

Wait for Ada to finish before starting the review.

## Ask for a review

Start a second agent:

```text
/invite turing
@turing Review the current git diff for the README typo fix. Report any unintended changes. Leave files unchanged.
```

Turing has its own conversation. The shared files let it inspect Ada's changes.
The prompt supplies the review task. Ada's conversation history is separate.

To pass Ada's last completed reply to Turing as additional context, use a
handoff. A handoff starts a new turn for Turing. Run it after Turing's review
finishes:

```text
/handoff ada turing
```

A handoff includes the source's last completed reply shown in the room.

Inspect `git diff` yourself in another terminal. Stage and commit when ready.

## Save a repeated check

For a Go project, save a test command:

```text
/def tests /shell go test ./...
/tests
```

The room confirms the definition. `/tests` runs it and shows the result. For
another project, use its own test command in the definition.

For a task that needs repeated fixes and checks:

```text
/loop @ada Make the tests pass without weakening them /until /tests /max 3
```

The loop sends the task to Ada, waits for the turn to end, then runs `/tests`.
A passing check ends the loop. A failed check supplies evidence for the next
attempt. This example permits up to three agent turns.

## Interrupt or finish

To try interruption, start a separate task and cancel it while Ada is working:

```text
@ada Calculate pi to 100 decimal places and verify the result.
/cancel ada
```

Cancellation is asynchronous. Keep watching the participant's status. File
changes already made remain in the worktree.

To stop an agent and remove it from the room:

```text
/remove ada
```

To close the room and stop its agents:

```text
/quit
```

Your files remain. A new room starts fresh conversations and command definitions.

Read [working in a room](room.md) for message delivery and keyboard controls,
or browse the [command reference](index.md#commands).
