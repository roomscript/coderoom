# coderoom Prompt Language

## Status

This document defines the target prompt language for coderoom. The
implementation currently supports a subset of the design; the remaining work
is listed under Implementation Status and tracked in
[GitHub issue #46](https://github.com/roomscript/coderoom/issues/46).

The canonical program is:

```text
/def tests = /shell go test ./...

/def fix @agent $task = /seq
  @agent task
  /tests
/end

/fix ada make the tests pass without weakening them
```

## Design Goals

- Preserve natural participant prompts as the primary interaction.
- Let users extend coderoom through reusable commands.
- Support runtime-managed completion conditions and bounded agent loops.
- Let commands accept participant aliases and free-form prompts.
- Keep parameters local to an invocation without adding global variables.
- Keep built-in and user-defined commands in one command namespace.
- Treat prompts as opaque content rather than strings or language syntax.
- Make multiline definitions easy to paste and diagnose.

## Language Forms

coderoom recognizes participant sends, broadcasts, built-in commands, and
user-defined commands:

```text
@ada inspect the parser                 # direct send
Please inspect the parser               # broadcast
/shell go test ./...                    # built-in command
/tests                                  # user-defined command
```

An agent response is always content. coderoom only interprets language syntax
submitted explicitly by the user.

## Grammar

The grammar uses simplified EBNF. Shell programs and prompt arguments consume
raw remaining input where noted.

```ebnf
input                  = statement ;

statement              = participant_send
                       | broadcast
                       | builtin_command
                       | user_command ;

participant_send       = "@", identifier, prompt ;
broadcast              = prompt ;
prompt                 = ? remaining non-empty input ? ;
quoted_prompt_literal  = '"', prompt_literal_character,
                         { prompt_literal_character }, '"' ;
prompt_literal_character = ? any character except '"' or newline ? ;

builtin_command        = invite
                       | remove
                       | cancel
                       | handoff
                       | who
                       | help
                       | quit
                       | policy
                       | shell
                       | define
                       | loop ;

invite                 = "/invite", identifier ;
remove                 = "/remove", identifier ;
cancel                 = "/cancel", identifier ;
handoff                = "/handoff", identifier, identifier ;
who                    = "/who" ;
help                   = "/help" ;
quit                   = "/quit" ;
policy                 = "/policy", "enable", policy_name ;
policy_name            = "send-notices" | "echo-invites" ;

shell                  = "/shell", shell_program ;

define                 = "/def", identifier, { alias_parameter },
                         [ prompt_parameter ], "=", definition_body ;
alias_parameter        = "@", identifier ;
prompt_parameter       = "$", identifier ;

definition_body        = executable_statement
                       | concurrent_block
                       | sequential_block ;
concurrent_block       = "/do", newline, statement_block, end_marker ;
sequential_block       = "/seq", newline, statement_block, end_marker ;
statement_block        = { blank_line }, block_statement, newline,
                         { blank_line | block_statement, newline } ;
block_statement        = whitespace, executable_statement ;
end_marker             = [ whitespace ], "/end", [ whitespace ] ;

executable_statement   = participant_send
                       | broadcast
                       | executable_builtin
                       | user_command ;
executable_builtin     = invite | remove | cancel | handoff | who | shell
                       | loop ;

user_command           = command_reference, { alias_argument },
                         [ prompt_argument ] ;
command_reference      = "/", identifier ;
alias_argument         = identifier ;
prompt_argument        = ? remaining non-empty input ? ;

loop                   = loop_single | loop_sequential | loop_concurrent ;
loop_single            = "/loop", "@", identifier, loop_prompt,
                         loop_control ;
loop_sequential        = "/loop", "/seq", newline, statement_block,
                         loop_end_marker ;
loop_concurrent        = "/loop", "/do", newline, statement_block,
                         loop_end_marker ;
loop_end_marker        = [ whitespace ], "/end", loop_control,
                         [ whitespace ] ;
loop_control           = "/until", command_reference, "/max", integer ;
loop_prompt            = ? non-empty text before the terminal /until clause ? ;
shell_program          = ? remaining non-empty input ? ;

identifier             = identifier_start, { identifier_part } ;
identifier_start       = letter ;
identifier_part        = letter | digit | "-" | "_" ;
integer                = digit, { digit } ;
letter                 = "A" | ... | "Z" | "a" | ... | "z" ;
digit                  = "0" | ... | "9" ;
whitespace             = { " " | "\\t" } ;
blank_line             = whitespace, newline ;
newline                = "\\n" | "\\r\\n" ;
```

Identifiers are case-sensitive. Command names are written without a slash in a
definition and with a slash at invocation. Contextual parameter resolution in
definition bodies is described below; it does not change the surface grammar
of participant sends or broadcasts. `quoted_prompt_literal` is recognized only
when resolving the complete prompt field of a definition-body statement.

## Commands and Namespaces

Built-ins and user-defined commands occupy one command namespace for the
running room:

```text
/help
/tests
/review
```

A user definition cannot replace a built-in or an existing definition.
Participant aliases are runtime participant references, not entries in the
command namespace.

`do`, `seq`, and `end` are reserved structural names and cannot be defined as
user commands.

The command namespace is global within the running room. This describes
visibility, not persistence. Persisting commands in room or project files is a
separate design question.

## `/policy` and `/invite`

Participant sends do not notify the other participants by default. A room may
opt into those listener notices explicitly:

```text
/policy enable send-notices
```

`send-notices` affects only the notices generated for a direct `@alias` send.
It does not turn broadcasts, handoffs, or private sends into notices, and it
does not notify the addressed participant twice. This avoids waking unrelated
participants merely because another participant was addressed.

`/invite` assigns an alias and starts a participant:

```text
/invite ada
```

By default it uses the Codex adapter and Codex's configured model. Selecting a
different CLI or model through `/invite` is outside this version.

Scripts can opt into deterministic participants before inviting anyone:

```text
/policy enable echo-invites
/invite ada
/invite tester
```

While `echo-invites` is enabled, every `/invite` creates an echo participant
that returns each received prompt unchanged and then completes its turn. The
echo adapter uses the same participant and session lifecycle as Codex.

Policies are room-local, non-persistent, and idempotent. They are valid only as
top-level statements, not inside a definition or block, and cannot be disabled
during the room. `echo-invites` must be enabled before the first participant is
invited and affects participant creation only: `/shell` remains a real
workspace command and no sandbox is implied. `send-notices` may be enabled at
any time and affects subsequent direct sends.

## Command Definitions

`/def` associates a command name and signature with an unevaluated body.
Defining a command does not execute it.

### Single-command bodies

A single executable statement may follow `=` on the definition line:

```text
/def tests = /shell go test ./...
/def ask @agent $question = @agent question
```

The definition returns that statement's result unchanged.

### `/do` blocks

`/do` groups multiple statements under the runtime's normal scheduling model:

```text
/def inspect @builder @reviewer $task = /do
  @builder task
  @reviewer task

  /shell go test ./...
/end
```

The block executor submits statements in source order without waiting for each
to complete before submitting the next. Source order guarantees submission
order only; it does not guarantee effect order. Independent work may overlap,
while runtime constraints still prevent incompatible work such as concurrent
turns for one participant. A command that depends on an earlier command's
effect must use `/seq`.

`/do` is a concurrent execution primitive, not a reuse of the composer's
single staged submission. Its executor owns all pending child executions,
schedules them independently of composer staging, and completes only after
every submitted child completes.

### `/seq` blocks

`/seq` adds a completion barrier between statements:

```text
/def review-and-test @reviewer $task = /seq
  @reviewer task

  /shell go test ./...
/end
```

Each statement starts only after the previous statement has completed. A
participant call therefore waits for the participant's entire turn before the
next statement starts.

For both block forms, `/do` or `/seq` must be the final token on the opening
line. `/end` appears on its own line and allows surrounding whitespace. Blank
lines do not terminate a block and are ignored during execution. A block must
contain at least one executable statement.

`/end` is only special while parsing a multiline construct. At top level it is
an unexpected command. Definitions and standalone block literals cannot appear
as child statements. A loop is an executable command, so its body may contain
another loop, including one with its own `/seq` or `/do` body. Composition may
also produce runtime nesting when a block invokes a command whose body is
another block.

### Statement boundaries

A block is a sequence of statements, not an implicit multiline string:

- Each non-blank physical line is one statement.
- Leading indentation used to format the body is ignored.
- Each statement must be valid independently.

The complete definition is submitted to the parser in one composer submission;
the parser does not retain a line-by-line collection mode. Ctrl+C clears the
unsubmitted draft. A missing `/end` is a parse error, and the draft should
remain available for correction.

## Parameters

Parameters are immutable bindings local to one invocation. They do not
introduce assignment, global variables, or a general value namespace.

A definition may declare zero or more alias parameters followed by at most one
prompt parameter:

```text
/def compare @author @reviewer $task = /do
  @author task
  @reviewer task
/end
```

Parameter names must be unique across the signature.

### Alias parameters

An alias parameter is declared with `@`:

```text
/def ask @participant $question = @participant question
/ask ada What should we change?
```

The signature establishes the argument type, so its invocation argument is the
bare alias `ada`, not `@ada`. Alias parameters supply participant-bearing
fields in body statements, including participant-call targets and compatible
arguments forwarded to built-in or user-defined commands. They cannot be
substituted into command names, shell programs, or prompt content.

`@name` in a body remains ordinary participant-call syntax, but `name` must
match an alias parameter in the definition's signature. The bound alias then
supplies the call target. Definitions cannot capture participants from the
running room by literal alias:

```text
/def notify-ada $message = @ada message
```

This is invalid because `@ada` is absent from the signature. Declare the
participant dependency instead:

```text
/def notify @recipient $message = @recipient message
/notify ada deployment finished
```

Participant existence or absence is checked according to the expanded command
when it executes, not when the definition is stored.

### Prompt parameters

A prompt parameter is declared with `$` and forwarded by name as the complete
prompt of a participant call or broadcast:

```text
/def review @reviewer $task = @reviewer task
/review turing inspect the parser and identify ambiguous cases
```

It binds the trimmed, non-empty remainder of the invocation. It does not need
quotes. Because it consumes the remainder, a definition may have at most one
prompt parameter and it must be last.

A body prompt whose complete trimmed content is an unquoted identifier matching
a declared prompt parameter forwards that prompt unchanged. A complete prompt
enclosed in double quotes is always literal and the delimiting quotes are not
part of the prompt:

```text
/def example @agent $task = @agent task
/def literal-example @agent $task = @agent "task"
```

The first definition forwards `task`; the second sends the literal prompt
`task`. Any other prompt text is also literal, so `@agent Please handle task`
does not reference the parameter. An empty or whitespace-only quoted prompt is
invalid because participant sends and broadcasts require content. Quoting is
only a disambiguator for a whole prompt literal in a definition body; it does
not introduce a general string value. Embedded quote and escape syntax are
deferred with richer prompt literals.

The parameter can be forwarded more than once, but it cannot be interpolated
into or combined with literal text. Prompt construction and modification
require a future prompt-operation design.

A prompt parameter can also be forwarded as a broadcast:

```text
/def announce $message = message
/announce Preserve $HOME and the literal ${value}
```

This resolves to a broadcast containing the invocation prompt unchanged. The
participant set is resolved when the broadcast executes. If no participant is
routable, execution uses the same error path as an ordinary broadcast.

A prompt is not a general string or shell argument. Parameter names have no
meaning inside a shell program:

```text
/def run $arguments = /shell go test $arguments
```

Here `$arguments` in the shell program retains its ordinary shell meaning; it
does not refer to the prompt parameter. The definition may warn that its prompt
parameter is unused, but prompt contents are never inserted into the program.

Future scalar values, prompt operations, or command arguments must define their
own syntax and semantics rather than broadening the meaning of `$prompt`.

### Forwarding through composed commands

Alias and prompt parameters may be forwarded unchanged into compatible
argument positions of another user-defined command:

```text
/def review @reviewer $task = @reviewer task

/def pipeline @agent $work = /seq
  /review agent work
  /tests
/end
```

Here `agent` supplies `/review`'s alias argument and `work` supplies its complete
prompt argument. Resolution uses the callee's signature and typed parameter
bindings; it does not turn either value into source text.

Only unchanged forwarding is supported. Prompt concatenation, interpolation,
construction, inspection, and decomposition are deferred to a future prompt
operations design.

## Structural Expansion

Invocation binds arguments and produces executable statement values. It does
not concatenate source text and parse it again.

For example:

```text
/def ask @participant $question = @participant question
/ask ada Is this API boundary correct?
```

resolves conceptually to:

```go
Send{
    Alias: "ada",
    Text:  "Is this API boundary correct?",
}
```

The body refers to a prompt parameter only when the entire prompt field is its
bare parameter name. Prompt contents are never inspected, interpolated, or
reparsed. They cannot introduce another command or change the send target,
even when they begin with `/`, `@`, or `$`.

Each invocation receives a fresh parameter scope. The bindings are discarded
when the invocation completes.

## Command Results

Every executable command produces an internal result:

```text
CommandResult {
    status:    success | failure | cancelled
    exit_code: optional integer
    output:    text
    error:     text
    children:  ordered command results
}
```

The result is runtime state. It is rendered in the room and may be consumed by
control-flow commands, but it is not an assignable value.

A single-command definition returns its command's result unchanged. `/do` and
`/seq` blocks retain each child result and derive one aggregate status:

- Explicitly cancelling the block produces `cancelled`.
- Otherwise, any failed or cancelled child makes the aggregate `failure`.
- Otherwise, the aggregate is `success`.

Both block forms attempt every statement; scheduling is independent of failure
policy. Child output remains attached to the child command and is not
duplicated into aggregate output.

A participant call completes when the participant's turn completes. Normal
turn completion produces `success`, failure to start or a crash produces
`failure`, and interruption produces `cancelled`. The runtime does not infer a
semantic result from participant prose. A future result-producing participant
protocol may enrich this behavior without changing definition syntax.

Other executable statements produce results as follows:

| Statement | Completion and result |
|---|---|
| Broadcast | Completes after every targeted participant turn. No routable participants or any child that cannot start or crashes produces `failure`; otherwise it succeeds. |
| `/policy` | Completes immediately when the policy change is accepted; invalid placement or state produces `failure`. |
| `/invite` | Completes successfully when the participant is ready; startup failure produces `failure`. |
| `/remove` | Completes successfully when removal finishes; an invalid target or stop failure produces `failure`. |
| `/cancel` | Completes when the interrupt request is accepted; rejection produces `failure`. It does not wait for the target turn's eventual outcome. |
| `/handoff` | Completes after the destination participant turn; source resolution or destination-turn failure produces `failure`. |
| `/who` | Completes immediately with the participant list as output. |
| `/loop` | Uses the loop result rules defined below. |
| User command | Binding, resolution, or recursion errors produce `failure`; otherwise it returns its body result. |

Cancellation of a child participant call contributes a failed child to its
enclosing block; it does not mean the block itself was cancelled. Block-level
`cancelled` is currently an internal runtime/lifecycle result, for example when
the room shuts down. There is no user-facing command for cancelling an active
block in this version. Ctrl+C only clears an unsubmitted composer draft.

## `/shell`

`/shell` executes the remaining input in the coderoom workspace:

```text
/shell go test ./...
/shell echo "hello world" | tee output.txt
```

The program is passed to the shell as written after trimming whitespace
immediately following `/shell`.

- Exit code zero produces `success`.
- A non-zero exit code produces `failure`.
- Failure to start produces `failure` without an exit code.
- User cancellation produces `cancelled`.

The invocation, status, standard output, and standard error are visible in the
room. Parameter interpolation into shell programs is not supported.

## `/loop`

`/loop` repeatedly executes a body until a user-defined command succeeds or
the iteration bound is reached. Its body may be a single participant send, a
sequential block, or a concurrent block:

```text
/def tests = /shell go test ./...
/loop @ada make the tests pass /until /tests /max 3

/loop /seq
  @ada fix the problem
  @ben verify the fix
/end /until /tests /max 3
```

The single-send form retains the concise existing syntax. In a block form,
`/end /until ... /max ...` is one contextual terminator; a plain `/end` does
not terminate the loop. Lexical block nesting remains prohibited. Immediate
top-level loop bodies may use literal participant aliases because, unlike
definition bodies, they do not capture variables.

`/loop` has do-while semantics. The runtime executes the complete body before
the first condition evaluation. After the body completes successfully, it
invokes the condition. A successful condition ends the loop; otherwise another
body iteration starts unless `/max` body iterations have completed. A failed
or cancelled body stops the loop without evaluating the condition again.

The condition must be parameterless because `/until` does not supply command
arguments. The runtime invokes the command and ends the loop when its aggregate
result is `success`; the loop participant does not self-report completion.

The condition need not be deterministic. A shell-only command offers a
deterministic condition when its underlying program is deterministic. A
condition may also call participants, but an ordinary participant call succeeds
when its turn completes, regardless of what its prose says.

Loops may be nested directly or through command composition. A `/do` body may
also start multiple loop executions concurrently. Each loop owns its iteration
bound, condition evaluations, cancellation propagation, and result subtree;
the surrounding block treats the complete loop as one child command. Command
recursion remains prohibited independently of loop nesting.

The loop result is `success` when the condition succeeds, `failure` when a body
fails or the final condition is still failing after `/max`, and `cancelled`
when the loop execution is cancelled. It retains each body result followed by
its condition result as children in execution order. If a body fails, that
failure is the final child; if the loop reaches `/max`, the final condition
result is the final child.

### Open question: failed-condition feedback

The current single-participant loop can include evidence from a failed
condition in the participant's next prompt. A multi-command body has no
obvious equivalent recipient or delivery mechanism. Before multi-command loop
bodies are implemented, decide whether that feedback is forwarded at all; who
receives it in `/seq` and `/do`; whether the author selects recipients; and
whether delivery mutates prompts, creates a separate runtime notice, or uses a
future result-producing participant protocol. This question does not change
the result ordering or do-while semantics above.

## Command Composition and Recursion

A command body may invoke another user-defined command, but that command must
already exist. The definition is checked immediately against the callee's
signature, including argument count, types, and forwarded parameter
compatibility. The body stores a resolved command reference rather than an
unresolved name.

Definitions are immutable. Because every dependency points to an earlier
definition, the command graph is acyclic by construction: direct self-reference
is rejected and indirect recursion cannot be formed through valid definitions.
A command body may itself be a `/do` or `/seq` block, but definitions and
additional block literals cannot appear as child statements within that body.
Runtime block nesting remains possible through command composition.

The ordering rule applies to every user-command reference in a body, including
a `/loop` condition. When a definition contains a loop, its condition command
must already exist and have a parameterless signature; both requirements are
validated before the containing definition is stored.

The runtime still tracks the active invocation stack as a defensive invariant.
Encountering a repeated command fails rather than recursing indefinitely:

```text
cannot invoke /one: recursive invocation through /one -> /two -> /one
```

Definition order is therefore significant:

```text
/def pipeline = /tests
/def tests = /shell go test ./...
```

The first definition is rejected because `/tests` does not exist yet. Define
dependencies before the commands that compose them.

## Validation and Errors

Definition-time validation rejects:

- a missing `=` or empty body
- malformed or duplicate parameters
- more than one prompt parameter
- an alias parameter after a prompt parameter
- a participant reference not declared as an alias parameter
- parameter forwarding into an incompatible field or argument
- an unterminated quoted prompt literal
- an empty or whitespace-only quoted prompt literal
- nested definitions
- direct self-reference
- a reference to a command that is not yet defined
- reserved or previously defined command names
- a `/do` or `/seq` block without `/end`

Invocation-time validation rejects:

- missing alias arguments
- invalid alias identifiers
- a missing or empty prompt argument
- arguments supplied to a parameterless command
- arguments that cannot be bound to the signature

Errors should identify the command and parameter when known:

```text
definition /review: expected '=' after parameter $task
definition /review: $task consumes the remaining input and must be last
definition /review is missing the closing /end for its /seq block
definition /notify-ada: participant @ada is not declared in the parameter list
cannot define /pipeline: /review is not defined; define dependencies first
/review argument 1 (@reviewer) must be an alias such as ada
/review requires prompt parameter $task
```

An incomplete block error points back to its opening `/do` or `/seq`. Because a
definition is submitted as one input, the parser does not retain partial
definition state after reporting the error.

## Implementation Shape

### Current parser and source contract (#39)

`internal/promptlang` owns syntax, typed AST nodes and parsing diagnostics.
Parsing is independent of runtime validation and dispatch.

`Parse` reads one complete submission with a source-aware cursor. Built-in forms
have named parsers registered in a syntax catalog, independently of execution
handlers. Scanning is contextual: prompt and shell payloads remain opaque,
including embedded command markers, quotes and newlines. The existing single-send
loop recognizes its control clause from the end of the input.

`Parse` returns `ParsedStatement`, an alias for `Located[Statement]`. Its `Value`
contains command content and its `Span` covers the whole statement. Spans are
half-open UTF-8 byte offsets into the original, untrimmed submission. Named
arguments use `Located[T]` to keep each value and its span together: aliases,
definition and invocation names, prompt and shell text, policy names and loop
bounds. For example, `send.Alias.Value` and `send.Alias.Span` describe one argument.
A definition's shell body uses `Located[Shell]`. An empty
span identifies an insertion point. Runtime-created values may have zero spans.

`Diagnostic` carries a stable `Code`, a `Span`, display text and an optional
underlying error. Syntax categories distinguish empty input, missing arguments,
unexpected input, invalid identifiers, invalid arguments and unknown commands.
Registry validation supplies reserved, duplicate and undefined-command categories;
the same diagnostic type can carry application error categories. Wrapping preserves
`errors.Is` and `errors.As` identities and existing displayed error text.

This implements the current forms only. In particular, `/def name /shell program`
remains the supported definition syntax; parameters, `=`, and blocks below are
future work. Current validation rules and whitespace behavior remain unchanged.

### Future language extensions

The implementation preserves the separation between parsing, resolution, and
execution:

1. Parse definitions into typed parameter declarations and one of the three
   body forms.
2. Parse `/do` and `/seq` blocks from one complete composer submission.
3. Resolve composed command references against definitions already in the
   registry and validate their arguments before storing the definition.
4. Parse a user invocation into its command name and unbound remainder.
5. Resolve the command, bind its signature, and structurally expand its body.
6. Execute the resulting statement or schedule the block through the existing
   session and UI paths. The `/do` executor owns multiple pending children and
   completion tracking independently of composer staging.

Invocation parsing depends on the resolved signature, while the current
`Parse` function is registry-independent. Basic parsing should therefore
retain the unbound invocation remainder. Signature-aware binding belongs in
resolution.

Typed AST nodes represent alias targets, opaque prompt forwarding, and block
scheduling. Raw string replacement is not part of the design.

## Implementation Status

Currently implemented:

- participant sends and broadcasts
- built-in room commands
- direct shell execution
- parameterless shell-backed command definitions and invocations
- bounded loops with a parameterless command condition
- `/policy enable send-notices`
- `/policy enable echo-invites` and the echo backend

Not yet implemented:

- `/loop` bodies using `/seq` or `/do`
- the `=` definition boundary
- `/do` and `/seq` blocks
- alias and prompt parameters
- structural parameter binding and expansion
- unchanged parameter forwarding through composed commands
- aggregate block results and runtime recursion protection

## Non-goals

This design does not introduce:

- global variables or assignment
- general string, number, boolean, list, or result values
- parameterized shell programs
- named, optional, or default arguments
- multiple or non-final prompt parameters
- multiline prompt literals
- nested or lexical command definitions
- recursive commands
- conditionals, returns, or early exit
- command overloading or redefinition
- persistent or project-level definitions

## Open Questions

- Should an invoked definition render an outer command record in addition to
  the records produced by its statements?
- Which future syntax should represent prompt operations, scalar strings, or
  escaped command arguments without changing the opaque meaning of `$prompt`?
