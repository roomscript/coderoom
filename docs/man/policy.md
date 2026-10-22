# /policy

Enable a policy for the current room.

```text
/policy enable <name>
```

The available policies are `send-notices` and `echo-invites`. Enabling a policy
again has no additional effect. Policies last until the room closes. There is
no disable command in the current room.

## send-notices

Ada and Turing were both in the room. After enabling notices, a prompt to
Ada showed both recipients:

Recorded excerpt ([source](examples/codex.txt)). `❯` marks user input.

```text
❯ /policy enable send-notices

[policy] send-notices enabled

❯ @ada Reply with exactly: noted.
  → ada      → turing

● ada:

  noted.
```

Subsequent direct `@alias` sends also notify the other agents in the room when
you submit. The addressed agent receives its prompt once. Other agents receive
a notice about the send. This changes delivery into their conversations and can
cause additional agent activity. It does not distribute the addressed agent's
reply automatically.

You can enable this policy at any time. Broadcasts already send a prompt to each
agent in the room; handoffs send the reply to the destination.

## echo-invites

Recorded excerpt ([source](examples/terminal.txt)). `❯` marks user input.

```text
❯ /policy enable echo-invites

[policy] echo-invites enabled

❯ /invite ada

[ada starting · echo]

[ada joined · model: echo-managed · effort: echo-managed]

❯ @ada Hello
  → ada

● ada:

  Hello
```

The echo agent replies with `Hello`. Enabling this policy for the first time
fails after the first invitation, even if you removed every agent. Once enabled,
all subsequent invitations use agents that repeat their input. This is useful
for trying room commands without live model calls.

Shell programs still run normally. This policy changes participant creation
and leaves your local shell permissions unchanged.

[Manual](index.md)
