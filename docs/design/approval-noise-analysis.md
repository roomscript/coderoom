# Approval noise: observed causes and proposed direction

Status: analysis with initial implementation, 2026-11-11. Historical observations
and estimates below are retained. Implemented behavior, replay results and
limitations are documented in approval-policy-test-cases.md.

## Evidence

Source: `/tmp/coderoom/agent-1793973442.log` (80,533,637 bytes).
Parsed incrementally, one line at a time; counted actual backend approval
requests, not mentions in conversation or repeated tool notifications.
Line numbers below refer to that local log. Raw content is not copied here.
The log covers work in another repository, pova; current coderoom source
explains the adapter boundaries but is not proof of the running binary version.

| Backend / action | Requests |
|---|---:|
| Claude Bash | 195 |
| Claude Edit | 144 |
| Claude Write | 23 |
| Claude WebSearch | 2 |
| Codex command execution | 155 |
| Total | 519 |

Claude received 361 allow and 3 deny responses. Codex received 151 accept,
2 acceptForSession and 2 cancel responses. Overall, 514/519 (99.0%) were
allowed. These are protocol responses, not proof that a human inspected each
request, that actions were safe, or that execution succeeded. We cannot
measure attention or time spent reviewing from this log.

## Causes

1. **Codex sandbox does not fit the assigned work.** Observed thread-start
   results report readOnly, networkAccess=false, approvalPolicy=on-request
   for Bill, Tim and Ada. Builder Bill consequently escalates ordinary edits,
   formatting, temporary-file tests and caches. The exact shell command
   `make check` receives 16 requests. Some checks include a network dependency
   audit; describing them all as offline would hide a real distinction.
2. **Claude asks about shell composition as well as effects.** Of 195 Bash
   requests, decision_reason_type is safetyCheck for 138, subcommandResults
   for 55, and absent for 2. These are backend reason labels, not complete
   explanations of each decision. Examples at lines 138, 692 and 15518
   perform repository inspection. Other requests execute arbitrary Python,
   modify files or remove temporary directories. A Bash label alone cannot
   distinguish them. Claude init messages mostly report default permission
   mode, with some later rex messages reporting acceptEdits.
3. **Repeated local edits are treated as separate permission decisions.**
   Claude generated 167 Edit/Write requests. All offered permission
   suggestions; only 47/195 Bash requests did. The other 148 offered none,
   so choosing the session option cannot reliably remove shell prompt noise.
   Current Claude code logs and accepts once if no session rule is offered.
4. **Current normalization discards decision evidence.**
   `internal/agent/approvals.go` exposes Kind, Ask and Options only. Claude's
   wire struct ignores decision_reason_type and classifier_approvable;
   approval formatting shortens commands. Codex formatting also shortens
   commands and omits the command reason. Neither prompt is suitable input
   to a reliable action classifier.

The five non-allow responses are not ground-truth danger labels. For example,
line 29211 inspects local machine networking/services, while lines 144400
and 148948 concern a fixture edit and local smoke check. They demonstrate
that relevance, scope and user intent matter alongside destructive effects.

## Recommended boundary

The useful split is **covered by an explicit policy** versus **needs a human
decision**. “Looks safe” is too weak a basis for automatically granting an
escape from the sandbox. Authorization and impact are separate questions.

| Action | Proposed treatment |
|---|---|
| Scoped repository inspection with known read-only semantics | Allow within the sandbox/policy |
| Builder edits inside authorized workspace paths | Allow within a workspace-write boundary, retaining protected-path exclusions |
| Known validation using bounded temporary/cache locations | Permit those resources; review any additional network or host access |
| Secrets, ignored local configuration, permission/policy files | Review unless explicitly covered by a narrower grant |
| Remote writes, deployments, infrastructure changes, publishing | Review unless the user has explicitly authorized that exact scope |
| Destructive Git/filesystem operations, privileged execution, sandbox escape | Review |
| Unknown tools, dynamic scripts, ambiguous shell effects | Review |

Names such as `test-*`, `make check`, `python3` or `gh` are not sufficient
allow rules. Repo code can change during a shared-worktree session. A test
can execute arbitrary code; a shell command can combine reads and writes.
Descriptions, backend suggestions and model risk assessments are evidence,
not independent authority. An explicit backend safety check should not be
silently overridden by a generic read-command prefix rule.

Start by configuring backend boundaries to match authorized work, especially
workspace writes for builders and bounded test scratch/cache access. Preserve
reviewer read-only restrictions. This reduces requests at their source and
keeps backend enforcement as the authority. The choice of writable paths,
protected paths and permitted network endpoints remains a user policy decision;
this analysis does not authorize changing the current configuration.

## Smallest follow-up design

1. Preserve full action evidence in adapter-normalized approval details:
   command and cwd, requested permission expansion, file targets when known,
   backend reason and backend identity. Keep Ask as presentation text.
   Unknown fields remain unknown; do not infer safety from missing evidence.
2. Show the actual permission delta and full inspectable action on prompts.
   Distinguish workspace edits, network access and host privilege. Do not
   replace the existing terminal escaping contract.
3. Configure explicit participant/backend policies at launch through the
   existing factory/options boundary. Verify effective settings from backend
   responses rather than assuming requested settings took effect.
4. Measure request volume and remaining reasons before adding an application
   classifier. If one is still needed, make it a pure evaluator before
   admission to the existing Session approval queue, consuming structured
   evidence and explicit grants. Unknown means ask. It returns accept-once
   or ask; it does not silently install broad session rules.
5. Record policy decisions with participant/request identity, matched rule,
   action and policy version. Expose a compact nonblocking activity summary
   and inspectable audit; reserve the blocking overlay for human decisions.

Session remains the single queue and resolution owner. An auto-covered
request returns its decision through the same listener; a review request
uses the existing FIFO pending entry and ID-checked resolution. Adapters
own protocol correlation and response writes. Classification introduces no
second pending queue or worker. A decision authorizes only the exact received
action; changed actions require another evaluation. Cancellation before
evaluation/response prevents a grant; cancellation after a response does not
undo execution already started. Backend lifecycle cancellation and withdrawal
continue to own pending-request cleanup; shutdown must not wait for human input.
Observers/audit callbacks must run outside locks. The current approvalHub
publishes while holding its mutex; any follow-up touching this path must
resolve that existing ordering/locking issue without introducing stale queue
presentation.

Alternatives: blanket auto-accept removes the control boundary; a model-only
classifier adds uncertainty and requires authorization context it does not
currently receive; a shell parser/prefix allowlist adds substantial complexity
and cannot establish the effects of arbitrary scripts. Backend permission
configuration plus explicit narrow grants is the recommended first step.

## Acceptance criteria for follow-up work

- Replayed counts match the table; every request has one correlated response.
- Authorized ordinary builder writes no longer require sandbox escape;
  reviewers retain their write restriction.
- A permitted scratch directory does not permit arbitrary host writes.
- Unknown, mixed-effect, secret-bearing and remote-mutating actions still ask.
- Changing a script, cwd, command arguments or requested permissions cannot
  reuse a narrower authorization incorrectly.
- Full command evidence survives normalization and remains inspectable safely.
- Covered actions are auditable without a blocking prompt.
- Cancellation, withdrawn requests, invalid choices, backend failure and
  shutdown preserve existing safe defaults and unblock all waiters.
- Queue observers run outside locks and preserve causal presentation order.

No automatic-safe percentage is claimed: obtaining one requires an agreed
policy and examination of full action effects/context, not just tool names.
No files under docs/for-humans were changed.

## First-rule replay estimate

User proposal: project file reads/writes, HTTP GET, and shell `cat`, `grep`,
`git show`, `git diff`, including commands inside shell wrappers.

A bounded offline inspection of the extracted requests gives:

| Rule | Matching requests |
|---|---:|
| Structured Claude Edit/Write with lexically project-contained absolute path | 166 |
| Explicit curl GET | 1 |
| Listed shell commands only, after shell-wrapper removal | 0 |
| Listed shell commands plus project-scoped `cd` | 3 |

Literal rules would therefore cover an estimated 167/519 (32.2%), leaving
352; supporting scoped `cd` would cover 170/519 (32.8%), leaving 349.
These are inspection estimates, not results from a production shell parser
or filesystem containment validator. Symlinks and actual HTTP behavior were
not checked. The curl request at line 113767 fetches upstream checksums and
follows redirects. WebSearch is not counted as an explicit GET.

The one other structured file write targets a helper under /tmp. Project
reads usually appear in compound commands containing `ls`, `sed`, `head`,
`echo`, `git status`, loops, or substitutions; the whole command cannot be
approved merely because one component matches. Codex edits are largely
shell scripts, so project-write intent alone does not establish their effects.

The 166 project writes include AGENTS.md and an ignored local.yml. A blanket
project-write rule would cover those too. Protected paths and role scope must
be settled before adopting that rule. The initial analysis proposed a destination/data scope for GET because it
can send secrets in URLs/headers or trigger endpoint side effects. The user
subsequently chose the Gitleaks decision policy documented below instead.
Keep HTTP permission separate from curl output-file writes and redirect scope.
Unwrapping a shell is useful, but approval requires parsing the complete
remaining syntax and rejecting unsupported constructs; never execute shell
expansions to discover what a command means. Git diff/show need trusted
execution configuration (external diff, textconv and pagers can run programs).


## Agreed GET policy (implemented)

The implementation now invokes local Gitleaks for supported GET inputs. See
approval-policy-test-cases.md for the active algorithm, backend cwd/redirect
limitations, validation evidence and role-independent replay totals.

Gitleaks is an optional runtime dependency, discovered when GET approval is
requested. If missing, ask the human. If installed, a complete clean scan
auto-approves once; findings or validation failures decline automatically.
Record a redacted reason for approval, decline or manual fallback. No extra
destination allowlist is required by this agreed rule. This replaces earlier
suggestions in this analysis that an unknown/secret-bearing GET should always
ask or that a clean scanner result was insufficient. Other action categories
retain their own rules. See approval-policy-test-cases.md for the decision
table, evidence scope, failure handling, auditing and lifecycle requirements.
