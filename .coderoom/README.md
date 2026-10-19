# `.coderoom`

This directory contains repo-local participant and role configuration used by
coderoom.

Current contents:

- `participants/*.yaml` define the local participants:

  | Role | Codex | Claude |
  |---|---|---|
  | Architect | ada | rex |
  | Builder | bill | ed |
  | Reviewer | tim | eve |

- `prompts/roles/architect.md` defines the `architect` role prompt
- `prompts/roles/builder.md` defines the `builder` role prompt
- `prompts/roles/reviewer.md` defines the `reviewer` role prompt

Notes:

- role names used in `participants/*.yaml` must have a corresponding prompt in
  `prompts/roles/*.md`
- these prompts are intentionally short and repo-focused
