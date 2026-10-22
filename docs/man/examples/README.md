# Recorded examples

These transcripts were exported from running coderoom sessions on 2026-10-22.
They supply the output shown in the command pages.

| Capture | Setup and commands |
| --- | --- |
| [Terminal and echo agents](terminal.txt) | Shell commands, definitions, help, invitations, messages, handoff, and removal. Echo agents repeat prompts without model calls. |
| [Codex](codex.txt) | Invitations, a direct prompt, handoff, broadcast, notices, and removal. Codex CLI 0.163.0; `gpt-6.1-sol`, effort `low`. |
| [Claude Code](claude.txt) | Model listing and change, one cancellation, and a passing loop. Claude Code 2.1.304; the transcript shows the models and effort used. |
| [Multiline room output](room-output.txt) | Echo agents, an indented multiline reply, and its handoff preview. |

Sessions ran in a temporary directory with no participant files.
`CODEROOM_BACKEND` selected the live backend for each session.

## Reading the captures

`❯` marks the start of user input. Multiline input continues on indented lines.
The remaining text is room output. The files contain actual agent replies and
command results. Agent replies can contain mistakes. Model names and wording
reflect these recording sessions.

The command pages quote excerpts from these files. Excerpts keep the captured
wording and order. The Codex recording also includes
`npx @openai/codex --version`, which records the CLI version used.

## Capture method

1. Build coderoom from the checkout being documented.
2. Launch it in a temporary directory with the desired backend.
3. Enter the commands and wait for their results. For cancellation examples,
   submit `/cancel` while the participant is working.
4. Press Ctrl+O to focus history, then Ctrl+G to open the transcript in `$EDITOR`.
   Save a copy of that file.
5. Remove trailing whitespace from the copy and take excerpts for the manual.
6. Close the room with `/quit`.

These are plain-text transcript exports. coderoom removes terminal styling and
exports full command output. The files do not capture participant animations,
elapsed-time indicators, or the screen after the app exits. They end at the
chosen export point.

Live captures use backend credentials and consume quota. The terminal and
multiline examples use local commands and echo agents.

[Manual](../index.md)
