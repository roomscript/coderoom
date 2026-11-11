# Approval policy: initial validation cases

Status: proposed cases, 2026-11-11. These are expected decisions, not executed
tests. Production behavior is unchanged. Companion: approval-noise-analysis.md.

## Decision contract

Classify the whole received action as accept-once, decline or needs-review.
The GET policy below can decline automatically. In the
tables, Review means decline automatic approval, not deny the user's action.
Unknown syntax, missing context, unsupported options and exceeded parser
limits require review. Return a stable reason and the offending component.
Never execute commands, substitutions or HTTP requests during classification.

Initial shell vocabulary: cat, grep, ls, head, echo, git show, git diff,
git status, plus cd to a contained project directory. Validate arguments and
paths, not just executable names. Input includes full command, cwd and project
root. Trusted executable resolution and execution configuration are prerequisites
to auto-approval: a repository executable named cat is not the system utility.

Accept pipelines, sequential lists, && and || only if every branch passes,
including branches that appear unreachable. Track cd through each branch;
when cwd differs on possible paths, validate subsequent paths against every
possible cwd or require review. Initially reject loops, functions, assignments,
background jobs, redirections, substitutions, globs and unsupported shell
constructs. Literal quoted metacharacters are ordinary arguments.

Peel known shell wrappers with a real syntax parser and a bounded recursion
depth. A non-login wrapper with supported flags and exactly one literal command
is eligible. Login shells may execute startup files: the logged zsh -lc form
needs an explicitly trusted startup environment before it can qualify. Do not
drop shell flags, positional arguments or environment changes to make it match.

For examples below, cwd is /project; all named project files exist and are
regular files, directories contain no escaping symlinks, executable resolution
is trusted, and Git has trusted configuration without external commands. Git
status additionally needs optional index writes suppressed if enforcing a
strict read-only role. Shell file writes via redirection remain review-only in
this first iteration; structured file operations have their own containment rule.

## Commands expected to auto-approve

| Command | Reason |
|---|---|
| `ls` | List project cwd |
| `ls -la docs` | Supported listing options, contained operand |
| `cat docs/design.md` | Contained file read |
| `grep -n -e 'approval' docs/design.md` | Literal search |
| `head -n 40 docs/design.md` | Bounded read |
| `git status --short` | Supported status form |
| `git diff --stat` | Supported diff form |
| `git diff --cached -- docs/design.md` | Supported staged inspection |
| `git show HEAD:docs/design.md` | Supported object inspection |
| `echo 'checking approval policy'` | Literal output |
| `echo '$(rm -rf .)'` | Single quotes make substitution literal |
| `echo 'a; b > c'` | Quoted punctuation is data |
| `cat docs/design.md \| head -n 20` | Both pipeline commands pass |
| `ls docs && head -n 20 docs/design.md` | Both commands pass |
| `git status --short; git diff --stat; echo done` | Entire list passes |
| `grep -n -e approval docs/design.md \| head -n 10; git diff --stat` | Pipeline and following command pass |
| `grep -q -e approval docs/design.md \|\| echo absent` | Both conditional branches pass |
| `cd docs && ls && head -n 20 design.md` | Cwd stays contained |
| `/bin/sh -c 'git status --short && git diff --stat'` | Supported literal wrapper |
| `/bin/bash -c '/bin/sh -c "ls docs"'` | Supported bounded nesting |

Markdown escapes around pipe characters are presentation only; fixture strings
must contain actual pipe characters.

## Commands expected to require review

| Command / context | Reason |
|---|---|
| `ls; rm -rf docs` | Unapproved second command |
| `git diff && git push` | Remote mutation |
| `echo ok \| sh` | Pipeline executes unapproved shell input |
| `cat docs/design.md > /tmp/copy` | Redirection not covered |
| `git diff --output=/tmp/change.patch` | Command-specific write option |
| `echo "$(cat ~/.ssh/id_ed25519)"` | Substitution and outside read |
| ``echo `id` `` | Command substitution |
| `cat <(curl https://example.com)` | Process substitution |
| `cat ../private.txt` | Outside project |
| `cat docs/link-to-outside` | Resolved path escapes project |
| `grep -r approval docs` | Recursive traversal not initially supported |
| `ls -R .` | Recursive traversal not initially supported |
| `head -n 20 /proc/self/environ` | Outside project |
| `cat docs/*` | Glob expansion not initially supported |
| `ls "$HOME"` | Dynamic expansion not initially supported |
| `PATH=./tools cat docs/design.md` | Executable resolution changed |
| `./cat docs/design.md` | Untrusted executable |
| `git -c core.pager=./helper show HEAD` | Execution configuration changed |
| `git diff --ext-diff` | External execution |
| `git show --textconv HEAD:docs/design.md` | External execution |
| `git status` with untrusted fsmonitor configuration | Git may execute a helper |
| `cd /tmp && ls` | Cwd leaves project |
| `cd docs; cat design.md` when cd can fail | Must validate both possible cwd states |
| `echo "$TOKEN"` | Dynamic environment disclosure |
| `echo -e '\\033]52;c;payload\\a'` | Escape interpretation not covered |
| `ls &` | Background execution not covered |
| `for f in docs/*; do cat "$f"; done` | Loop and expansions not covered |
| `/bin/sh -c "$COMMAND"` | Nonliteral wrapper body |
| `/bin/sh -c 'echo "$0"' secret` | Wrapper positional arguments not covered |
| `/bin/zsh -lc 'ls'` with unknown startup configuration | Startup code can run before ls |
| `sh -c 'ls; python3 change.py'` | Unsafe inner list remains unsafe |
| Malformed quotes, unsupported syntax, excessive nesting | Parse failure/limit |

## HTTP GET leak screening

Decision agreed with the user: Gitleaks is an optional runtime dependency,
checked when a GET approval request arrives. coderoom remains usable without
it; do not install or download it automatically.

1. Identify a GET request and obtain the complete outbound request evidence.
2. Check whether the local Gitleaks executable is installed.
3. If missing, use the existing human approval flow. Record why manual approval
   was requested.
4. If installed, validate locally with Gitleaks. A completed scan with no
   findings auto-approves once. A finding or an unsuccessful validation
   declines the request. Record the approval or decline reason.

“Gitleaks approves” means a successfully completed scan with no findings,
not a textual recommendation from the scanner. Distinguish findings from
execution failures. Timeout, launch failure after discovery, unsupported
version, malformed result, input truncation or decoding/size limits are
validation failures and decline; they do not silently become clean results or
fall back to a prompt. Only a missing installation takes the manual path.

The user explicitly accepts secret-scanner coverage as the GET decision
criterion to reduce approval exhaustion. A clean result is sufficient for this
GET rule; there is no additional destination allowlist or subjective model
review gate. This supersedes the earlier proposed destination-policy gate.
It does not claim that every secret or confidential value can be detected,
or that every GET endpoint is free of side effects.

Gitleaks supports stdin, recursive decoding and redacted output. Source:
https://github.com/gitleaks/gitleaks#usage. Supply raw and bounded decoded
request components, including host, path, query and outbound headers. Do not
execute expansions to discover a URL or credentials. Missing/dynamic evidence
cannot yield a completed validation: with Gitleaks installed, decline with
get.validation_incomplete. Scan output goes through a structured adapter;
validate supported version/exit/report semantics in tests.

Use application-owned scanner configuration, independent of repository rules,
ignore files, inline suppression and inherited configuration overrides. Bound
input and execution time. Feed candidates through stdin; never use an external
scanning service. Findings and audit records must not disclose candidate
secrets. The tool version, rule configuration identity and decode limit must
be recorded so a clean result has an inspectable meaning.

Authorize only the request that was validated. Redirected requests need their
own validation before dispatch. If a transport cannot expose a redirect before
following it, automatic validation is incomplete. Shell parsing still checks
additional effects: a GET combined with file output, another command or an
upload does not make the entire shell action eligible automatically.

| Condition | Decision | Reason code |
|---|---|---|
| Gitleaks absent | Human approval | get.scanner_missing |
| Installed, complete successful scan, no findings | Accept once | get.scan_clean |
| Installed, detected secret in URL or headers | Decline | get.secret_detected |
| Installed, detected encoded secret | Decline | get.secret_detected |
| Installed, launch/runtime failure or incompatible version | Decline | get.scanner_error |
| Installed, scanner timeout | Decline | get.scanner_timeout |
| Installed, incomplete request, unsupported redirect or scan limit | Decline | get.validation_incomplete |
| Clean GET plus unsupported shell effects | Human review of whole action | shell.unsupported_effect |

Each automatic decision records participant/request correlation, GET method,
redacted request identity, decision, reason code, scanner version/configuration
identity and, for findings, rule IDs with redacted locations. Display a compact
nonblocking reason in activity history; retain inspectable details without raw
URLs, tokens or headers. Human decisions after scanner_missing retain the
existing approval lifecycle and record their human origin.

Keep evaluation before the Session pending queue; missing-scanner requests
join the existing FIFO queue, and automatic responses return through the same
listener. Adapters retain response correlation. Scanner work must be bounded
and cancellation-aware, outside Session locks; cancellation/shutdown stops the
scanner, reaps its child process and sends no stale approval. Do not add a
second pending approval queue. A cancellation is not a scanner denial; record
it separately if auditing abandoned evaluations.

## Verification plan

Implement table-driven decision tests from these cases, then add parser-specific
adversarial variants: quoting, escaped newlines, comments, separators, wrapper
flags, option abbreviations, positional arguments and nested contexts. Check
reason codes and offending component, not error-string text. Cover every GET
decision-table row with fake scanner outcomes, plus raw/encoded synthetic
secrets, configuration isolation, redaction, cancellation and child cleanup. All tests classify
strings without running their contents or making network requests.

Use filesystem fixtures separately for containment, symlinks, nonexistent write
targets, protected paths and path changes. Classification alone cannot prevent
a symlink/config/script changing between decision and execution; runtime sandbox
enforcement must retain that boundary.

Replay the extracted 519 requests offline, producing counts and a redacted list
of accept/review reasons. Manually inspect all new accepts; compare policies with
only one rule changed at a time. Keep raw private log content outside the repo.
Do not infer actual safety from agreement with historical human responses.

Runnable prototype scope has been proposed to the user separately: playground
only, no command execution or HTTP, no production integration. Await that scope
agreement before writing prototype code. Record findings under docs/playground.
