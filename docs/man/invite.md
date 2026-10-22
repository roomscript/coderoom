# /invite

Start a named agent in the room.

```text
/invite <alias>
```

This session used the Codex backend:

Recorded excerpt ([source](examples/codex.txt)). `❯` marks user input.

```text
❯ /invite ada

[ada starting · codex]

[ada joined · model: gpt-6.1-sol · effort: low]
```

An alias starts with a letter and can contain letters, digits, hyphens, and
underscores. Aliases are case-sensitive. Each alias can appear once in the room.

coderoom uses the participant configuration loaded at startup. An alias without
a configuration file uses the default backend and starts with identity
instructions. See [participants](participants.md).

The room reports startup, then joining with the model and effort. Startup
failures appear in the room. A message sent while an agent starts can wait until
it is ready. Remove a failed participant before inviting the same alias again.

Each invitation starts a new conversation. Reinviting an alias after removal
uses the loaded configuration and the backend's initial model settings.

Related: [send](send.md), [remove](remove.md), [model](model.md).

[Manual](index.md)
