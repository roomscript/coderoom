# Approval policy: implementation and validation

Status: shell and local GET validation implemented, 2026-11-12. Presentation
redesign remains separate. `internal/approval` evaluates one typed operation;
Session translates its explicit Approve, Decline or Review result into backend
choices. Review enters the existing FIFO human queue. Automatic approval is
always once, never a session-wide backend rule.

## Implementation map

- `internal/agent/approvals.go`: sealed file, command and HTTP operation values.
  Adapters retain full raw requests independently from shortened display text.
- `internal/approval/policy.go`: evaluation dispatch and stable redacted results.
- `files.go`, `ignored.go`: existing containment and protected/ignored
  write behavior, including hard-link restrictions, unchanged by shell work.
- `shell.go`, `shell_read.go`, `shell_git.go`: mvdan Bash AST parsing, literal
  arguments, complete branch validation, cwd propagation and local execution facts.
- `get.go`, `gitleaks.go`: outbound-input screening and bounded local scanner.
- `internal/session/approval_policy.go`: backend choice mapping,
  reason/version/config audit, cancellation and the existing approval listener.

Default application construction enables this policy with the project root.
There are no shell/Git trust switches that leave the feature permanently off.
The evaluator never runs a candidate command, performs an expansion or sends a
candidate HTTP request. It runs only fixed Git metadata queries and Gitleaks.

## File decisions

Require all targets, project root and cwd. Resolve symlinks and nearest existing
ancestors for new writes; reject traversal and project escapes. Existing targets
must be regular files. Existing writes need a known single hard link. Approval
decisions do not depend on participant role; independently configured capability
enforcement is unchanged. Protected names (AGENTS.md and .git components,
case-insensitive), ignored writes and failed ignore checks
retain their existing review behavior. Reads do not gain new protected-path
restrictions. Session fills only missing file cwd; it never invents shell cwd.

## Supported shell grammar

Parse at most 128 KiB and unwrap at most eight literal shell wrappers. Support
pipelines, lists, && and || only when all executed branches are covered. Even a
branch that looks unreachable must pass. Reject assignments, substitutions,
variables, globs, loops, functions, redirections, background jobs, negation and
unsupported constructs/options. Single-quoted metacharacters remain text.
Conservative literal parsing also rejects unquoted backslash escapes, braces,
tildes and equals expansion syntax; unsupported does not mean dangerous.

Track successful cd across &&. Pipeline directories do not escape their child
shell. If OR or a following list statement could see different cwd states,
require review. Existing directories must resolve inside the project. File
operands must resolve to contained regular files; ls also permits directories.
Recursive directory traversal is unsupported.

| Command | Supported arguments |
|---|---|
| cat | -n, -b, -s, --, contained files; no files means stdin |
| grep | -n, -i, -q, -F, -E, -v, -c; -e literal pattern; --; contained files |
| ls | combinations of l, a, h, t, d, 1; --; contained files/directories |
| head | -n followed by a positive integer up to 100000; --; contained files |
| echo | literal arguments; no leading option or backslash interpretation |
| cd | exactly one literal contained directory, no options |
| git status | --short, --porcelain, -- followed by contained paths |
| git diff/show | --stat, --cached, --staged, --name-only, --name-status, --no-ext-diff, --no-textconv; -- followed by contained paths |
| git show | additionally HEAD and HEAD:contained-file |

Unknown Git revisions/options require review. This is deliberately an explicit
subset, not a claim about every read-like Git command.

Executable lookup must resolve to installed utilities under /usr/bin, /bin or
the system coreutils directory /usr/lib/cargo/bin/coreutils, with regular,
non-group/world-writable targets. A project executable named cat does not pass.
Shell builtins are handled explicitly. BASH_ENV, ENV, CDPATH, exported shell
option/trace variables and imported Bash functions
prevent automatic shell approval. These are local facts inherited by backend
processes, not an attestation of arbitrary backend-specific environment changes.

Git configuration is queried with a fixed bounded `git config --null --list`,
not a candidate invocation. Reject fsmonitor, pager, diff, filter and alias
configuration capable of invoking helpers. Unknown GIT_* and pager environment
changes require review; the ordinary installed `cat` pager is accepted. Metadata
output and time are bounded. This intentionally also reviews some harmless Git
configuration rather than interpreting arbitrary helpers. Backend sandboxing
still governs optional Git index refresh writes and evaluation/execution races.

Supported wrappers are sh/bash -c with exactly one literal body, and bash
--noprofile --norc -c. For zsh, support only -f -c and the common literal-command
grammar, with both known system zshenv locations absent or comments-only. Zsh
startup code, login wrappers, unknown flags and positional arguments require
review. Never drop flags or treat zsh-specific expansions as Bash literals.

## Required examples covered by tests

| Outcome | Command |
|---|---|
| Approve | ls -la docs |
| Approve | cat docs/design.md \| head -n 20 |
| Approve | git status --short; git diff --stat; echo done |
| Approve | cd docs && ls && head -n 20 design.md |
| Approve | /bin/sh -c 'git status --short && git diff --stat' |
| Approve | echo '$(rm -rf .)' |
| Review | ls; rm -rf docs |
| Review | git diff && git push |
| Review | echo ok \| sh |
| Review | git diff --output=/tmp/change.patch |
| Review | echo "$(cat ~/.ssh/id_ed25519)" |
| Review | outside-project reads, escaping symlinks, unsupported wrappers |

Markdown pipe escapes are presentation only. Fixtures use actual pipelines.
Filesystem/environment fixtures establish positive facts; Session tests also
exercise all six approval examples through WithApprovalPolicy, without a
permissive test-only policy or scanner configuration.

## GET algorithm

Check installation on each GET. Missing Gitleaks means Review
(get.scanner_missing). Installed and successfully clean means Approve once
(get.scan_clean). Findings decline (get.secret_detected). Launch errors,
unsupported scanner capabilities or malformed reports decline (get.scanner_error); bounded
scanner timeout declines (get.scanner_timeout). Incomplete/dynamic data,
unsupported redirects and size/decode limits decline (get.validation_incomplete).
Cancellation is separate: Session returns no stale decision.

Screen the reported URL and all explicit headers/body inputs. This fulfills URL
leak screening; it does not require proof of implicit backend headers or all
backend traffic. HTTPOperation explicitly marks dynamic inputs and redirects.
Claude WebFetch follows redirects without exposing the next target, so installed
Gitleaks cannot produce a completed validation for that tool. Without Gitleaks
it still uses the human queue. A nonredirecting structured GET can pass.

Recognize literal curl GET under supported wrappers and compositions: -s/-S/-f
and long forms, -X/--request GET, -H/--header literal headers, and one URL. Retain
all duplicate header values for scanning. -L/--location marks incomplete
validation. Header-file input is incomplete; other curl flags/effects require
review. Dynamic curl data never executes to discover its value. Curl config
must be absent in known HOME/CURL_HOME/XDG locations, or disabled by a first
-q/--disable. Config files can otherwise add uninspected uploads or redirects.
No candidate curl process is run.

Feed raw JSON, raw URL, headers/body and bounded URL/HTML/base64 decoded variants
through scanner stdin. Limit combined input to 128 KiB and decoding to three
levels. Discover scanner functionality through bounded local help queries:
prefer `gitleaks stdin`, otherwise use `gitleaks detect --pipe`. Require explicit
config, JSON report, redaction and findings-exit-code support. Use boolean
`--redact` with either CLI; pass optional suppression-disable/native-decode flags
only when advertised. Native decoding requires suppression-disable support;
bounded application decoding applies to both profiles.

Version formatting is not an eligibility gate. Record a recognizable version
when available, otherwise `unavailable`; never audit arbitrary version output.
Record gitleaks-default-v2-decode3-no-inline-allow and the selected stdin or
detect-pipe profile with the outcome. Use
application-owned configuration extending the scanner's built-in default rules,
independent of repository rules, .gitleaksignore, inline allows or inherited
GITLEAKS_* variables. Raw and decoded input containing `gitleaks:allow`
(case-insensitive) declines with get.validation_incomplete before scanning. This
conservative restriction applies even to harmless occurrences: old scanners can
suppress findings based on this marker and have no disable flag. Never treat a
suppressed clean result as approval. Modern suppression-disable flags provide
additional protection when supported.

Request bytes never appear in scanner process arguments. A scanner executable
resolving inside the candidate project is rejected rather than executing repo code.

The scanner runs in a private temporary directory with isolated HOME/config,
redacted report flags, a three-second deadline, bounded stdout/report reads and
discarded stderr. Exit 0 requires a valid empty JSON array for approval. Exit
42/findings, mismatched exit/report semantics or invalid reports never approve.
Cancellation kills the process group and waits for the child; scratch files are
removed. The scanner is never installed automatically and no external scanning
service, destination allowlist or model risk gate is involved.

## Validation and replay

Tests cover required shell outcomes, executable/configuration facts, containment,
identical file decisions across builder, reviewer, architect, documenter, tester
and unspecified roles, GET missing/clean/finding/failure/malformed/timeout/incomplete
cases, encoded and
explicit inputs, actual fake-executable invocation, cancellation and Session
shutdown. Tests never execute candidate commands or send candidate HTTP traffic.

Private replay input stays outside the repository. 519 extracted requests were
available. Use reported Codex cwd; Claude Bash has no current cwd evidence, so
neither launch cwd nor an embedded cd is treated as complete backend evidence.
Participant roles are not approval inputs and require no replay assumptions.

| Requests | Approve | Decline | Review |
|---:|---:|---:|---:|
| 519 | 162 | 0 | 357 |

Reasons: file.project_contained 162; file.protected 3;
file.ignored_or_unknown 1; file.path_unvalidated 1; shell.cwd_unvalidated 197;
shell.startup_unvalidated 153; policy.evidence_incomplete 2. No scanner decision
was reached in this historical corpus: the literal GET occurs inside a login wrapper.

Sanitized representatives: Approve `Edit <project>/docs/<file>`;
Review `/bin/zsh -lc '<script>'`, `Bash '<script>' (cwd unknown)`, and
`Edit <project>/AGENTS.md`. There is no Decline representative.
These use the current historical project filesystem and local execution facts;
they do not establish historical filesystem state or successful execution.

The replay test accepts CODEROOM_APPROVAL_REPLAY and CODEROOM_APPROVAL_REPLAY_ROOT.
It prints only record numbers, outcomes and redacted reasons. Earlier role-based
replay analysis in the restart/review notes is superseded by these results.

Checks for the role-independent change passed: race-enabled approval, Session,
Claude and Codex tests; repository lint (zero issues); branding; and diff whitespace
validation. The public Session role table covers identical reads, writes and
protected/ignored/outside paths for all listed roles. No separate capability
enforcement, shell or Gitleaks behavior changed.

Earlier implementation checks: `go test ./...` and `make pre-commit` (branding,
goleak, lint and the full race suite) passed. An earlier full-race handoff timeout
passed five targeted reruns and the final full suite. Build passed. No live
backend integration tests were run.

The installed Ubuntu Gitleaks 8.16.0-1build2 was tested through the real evaluator
with fabricated input only. Its version output is a build placeholder; capability
detection selects detect-pipe and records version unavailable. An ordinary URL
approves; synthetic raw/base64 tokens decline as findings; raw, percent/base64
encoded and explicit header/body suppression markers decline as incomplete
validation. No HTTP is sent and the package does not need replacing. These
checks supersede the earlier version-gated smoke failure and are separate from
the 519 historical requests. Fake executables test both CLI profiles and failure
semantics without depending on an installed scanner.

Run only the local-scanner integration test with:
`go test -tags integration ./internal/approval -run '^TestInstalledGitleaksGET$' -v`.
It skips when Gitleaks is absent and never executes a candidate command or HTTP.
Compatibility validation passed: full unit suite, focused approval/Session race
tests, scanner-only integration tests with the race detector, regular and
integration-tag lint (zero issues), branding, goleak and diff whitespace checks.
A broader Session integration invocation was stopped and is not counted as
completed validation.

## Specific open work

- Claude Bash lacks authoritative per-request cwd and may persist cd across
  calls. The smallest useful backend improvement is reporting actual cwd with
  each approval, not assigning launch cwd in Session.
- WebFetch needs a nonredirecting mode or an interceptable redirect request to
  complete validation beyond its first URL. Implicit headers alone do not
  disable ordinary GET screening.
- Login shells need inspectable/trusted startup behavior. Zsh installations
  with executable system startup configuration remain review-only; portable
  discovery of custom compile-time zshenv locations is not implemented.
- Filesystem, executable, environment and Git facts are checked before approval;
  the existing backend sandbox remains necessary against concurrent changes.
- Broader options, revision grammar, glob/escape expansion, loops, redirections,
  test/build execution and writes through shell commands are outside this subset.
  They remain human-reviewed, not silently treated as implemented.
- Scanner rules detect known patterns, not every possible secret. Version/rule
  identity is recorded; detailed per-rule finding summaries and presentation
  redesign remain open work. Raw findings are never included in activity.
