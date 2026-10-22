# Debug commands

These commands help inspect the terminal UI during development. Launch with
`CODEROOM_DEBUG=1 coderoom` to enable them.

| Command | Action |
| --- | --- |
| `/debugview` | Print viewport diagnostics into the room. |
| `/debugrows` | Toggle row numbers in the transcript. |

Both commands take no arguments. Without debug mode, they report that debug
commands are disabled. These controls affect presentation and leave agent
conversations unchanged.

To capture raw backend traffic, launch with `coderoom --agent-log /path/to/log`.
The log is appended to that file and can contain prompts, responses, and tool
details.

[Manual](index.md)
