# Approval implementation review, 2026-11-12

Historical review: the findings below describe successive working-tree states.
The scanner incompatibility finding is resolved by the compatibility follow-up
at the end; it is not a current requirement to replace Ubuntu's package.

Scope: current working-tree implementation and Bill's handoff. Focused tests
for internal/approval, internal/session, internal/agent/claude and
internal/agent/codex passed during this review. Bill reports build and full
pre-commit checks passed; this review did not repeat those checks or run live
backend integration or the private replay.

## Assessment

The parser and scanner paths now exist and explicit outcomes/typed operations
make the package easier to follow. The remaining main product gap is reaching
those paths with real backend evidence. Unit/Session acceptance examples prove
the evaluator can approve; they do not prove the adapters supply those inputs
in normal operation or that the historical workflow loses shell prompts.

### Shell coverage does not yet cover the recorded workflow

Claude Bash normalization creates CommandOperation without Cwd. Evaluation
rejects an unknown cwd before inspecting the command, including an absolute
leading cd. Consequently all current Claude Bash approvals require review.
The historical Codex login wrappers use zsh -lc, which wrapperBody does not
allow. Non-login sh/bash -c forms are supported, with additional environment,
executable and Git configuration checks. Zsh supports only -f -c and requires
comment-only/absent system zshenv at the checked locations.

The six public Session examples use a supplied cwd and a controlled environment;
they do not exercise a real backend's normalization. Add backend-path acceptance
examples before claiming the shell feature solves recorded prompt noise.

Next work: obtain authoritative per-request Claude cwd if the backend provides
it. If unavailable, assess a constrained absolute leading-cd form separately,
without treating launch cwd as persistent shell cwd. Investigate whether Codex
can issue non-login commands through supported configuration. Do not simply
strip -l: startup code is part of execution.

### Installed Gitleaks can reduce GET availability

Missing scanner means human review; incompatible scanner means automatic
decline. The implementation accepts version strings matching 8.24 through 8.99,
then requires working stdin/report flags. Bill reports the installed binary
has an unrecognized build version and no stdin support; no real clean-scan
success has been demonstrated here. Fake executables cover process/report
behavior. Validate a supported real release with harmless synthetic input as
a separate deployment check. Do not change the agreed error-decline behavior
silently; make its reason visible and document compatible installation.

Claude WebFetch always marks FollowsRedirects, so installed Gitleaks yields
validation_incomplete without launching a scan, even if this particular URL
would not redirect. This is a capability-level restriction, not an observed
redirect. Literal curl in a supported wrapper can scan if its configuration
is known and -L is absent. curl -q disables implicit config; only a narrow
option vocabulary is supported. No redirect interception or candidate HTTP
execution is implemented.

### Replay must not be mistaken for a production benefit measurement

Strict replay: 0 approvals, 0 declines, 519 reviews. Unknown historical write
authority accounts for 167, missing cwd for 197, startup validation for 153 and
incomplete operations for 2. Unknown historical roles are an evidence limit,
not proof that current configured builders lack authority.

Counterfactual builder roles: 162 approvals (31.2%), all ordinary structured
file writes, and 357 reviews. The unchanged shell review counts show the
parser brings no extra savings to this recorded corpus under current evidence.
No replay GET reaches scanning, so the zero declines do not test that path.

Other deliberate restrictions include unsupported flags, globs, expansions,
loops, shell writes/redirections, outside paths, protected/ignored writes and
hard-linked write targets. Pre-execution filesystem/config checks cannot
eliminate concurrent changes; backend sandbox enforcement still matters.

## Recommended order

1. Establish one real backend shell approval that auto-approves through adapter,
   Session and evaluator, then a representative compound command. Fix evidence
   reachability before broadening the command vocabulary.
2. Demonstrate a clean scan with a supported real Gitleaks release and explain
   installed-but-incompatible errors. Keep fake tests for deterministic failures.
3. Resolve WebFetch redirect semantics as an explicit scope decision rather
   than presenting WebFetch as completed GET auto-approval.
4. Repeat replay with explicit documented role/cwd/startup assumptions and
   measure new sessions separately. Retain decision reason records for analysis.

This review does not establish that the shell/environment validation is a
complete security boundary. It identifies current behavior and integration
limits; no production changes were made.

## Ubuntu scanner compatibility check

Local package: gitleaks 8.16.0-1build2. Its version output is a build placeholder,
but `detect --pipe` supports stdin scanning, JSON reports, boolean `--redact`
and a configured findings exit code. A local synthetic check (no HTTP) produced
exit 0/empty report for a public URL and exit 42/one finding for a fabricated
GitHub-token-shaped query. Thus lacking the modern stdin subcommand does not
mean this scanner cannot implement URL screening.

Important compatibility finding: appending `gitleaks:allow` to the synthetic
query changed the result to exit 0/empty report. The old CLI has no advertised
ignore-gitleaks-allow flag. A legacy adapter must prevent candidate-controlled
inline suppression, with regression tests, before allowing clean results.
Investigate scanning a suppression-neutralized copy of each raw/decoded input
in addition to its original, or use application-owned rules with equivalent
coverage in a mode that cannot honor candidate suppressions. Do not blindly
retry with unsupported flags removed. Modern and legacy CLI profiles should
retain the same clean/finding/error semantics and isolated configuration.

Recommendation: preserve Ubuntu package support through a small capability-based
scanner adapter; reject unknown behavior explicitly. Do not require a semantic
version string when the distribution does not embed one. Record unavailable
version metadata honestly and the selected CLI/configuration profile. Replacement
with a modern release is an alternative, not a prerequisite established by the
original user requirement. This check was read-only apart from temporary scanner
files; no production integration was changed.

## Compatibility follow-up review

Reviewed the capability-selected modern stdin and legacy detect-pipe scanner
paths, raw/decoded suppression rejection, subprocess/report handling and tests.
No blocking correctness finding identified within this change's scope.

Independently reran approval and Session unit tests and the installed-scanner
integration test. All passed, including actual Ubuntu clean approval, raw and
base64 token detection, percent/base64 suppression rejection and explicit
header/body suppression checks. Modern process handling is tested with fake
executables; this review did not run a separate modern Gitleaks installation,
full repository checks or live backend integration.

Version formatting no longer gates approval. CLI profile and configuration
identity are recorded, with unavailable version metadata explicit. Legacy
scanning uses application-side bounded decoding; modern native decoding is
enabled only when the scanner can disable inline suppression. Marker rejection
also occurs before invoking either profile. Harmless marker occurrences decline
by design; this is a conservative false positive, not a bypass.

The change resolves local package compatibility only. Claude WebFetch redirect
handling, unknown shell cwd and login-shell coverage remain separate limitations.
Human fallback for missing Gitleaks and automatic decline for scanner failures
remain as agreed. No production code was modified by this review.
