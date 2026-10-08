# Code Review Guide

This document captures the coding style expected in this project. Use it to prime a reviewer (human or AI) before reviewing implementation code.

---

## Function Design

### Length

Target: one screen (~40-50 lines). If a function exceeds this, extract named helpers. This is a guideline, not a hard rule — Go's verbosity (error handling, type declarations) sometimes makes it unavoidable. Prefer splitting when it adds clarity, not just to hit a line count.

### Entry points read as algorithms

Top-level functions should describe *what* happens, not *how*. Each step should be a named call. A reader should understand the flow without reading the helpers.

**Avoid:**
```go
func (s *Session) Run(ctx context.Context) error {
    // ... 60 lines of inline routing, policy checks, lifecycle management ...
}
```

**Prefer:**
```go
func (s *Session) Run(ctx context.Context) error {
    if err := s.startParticipants(ctx); err != nil {
        return err
    }
    if err := s.router.Start(ctx); err != nil {
        return err
    }
    return s.controller.Loop(ctx)
}
```

Each helper fits on one screen and has a clear, single responsibility.

---

## Naming

### Functions

Use verb phrases that describe what the function does:
- `startParticipant`, `routeMessage`, `applyPolicy` — clear, unambiguous intent.
- Avoid noun phrases: `participantStarter`, `messageRouter`.

### Side effects

Function names must signal whether a side effect occurs:

- `Get*`, `Read*`, `Parse*`, `Compute*`, `Derive*` — pure by contract: no writes, no mutations, no I/O beyond reading.
- `Write*`, `Update*`, `Create*`, `Delete*`, `Start*`, `Stop*` — side effects are expected and signalled.

A function named `getX` that secretly writes a file or modifies shared state is a bug in the design, not just the implementation.

**Go note:** pointer receiver methods that mutate their own struct are idiomatic Go and exempt from this rule. The rule applies primarily to package-level functions and methods whose callers would not expect mutation.

### Variables

- Short, clear names. Avoid Hungarian notation.
- Booleans and predicates read as conditions: `isRunning`, `ok`, `found`.
- Idiomatic Go abbreviations are fine: `err`, `ctx`, `cfg`, `msg`, `id`.

---

## Idiomatic Go

### Error handling

Inline `if err != nil { return err }` at the call site. Do not wrap multiple steps in a helper just to reduce error-handling lines — that obscures which step failed.

### Early returns

Prefer early returns to reduce nesting. A guard at the top of a function is clearer than a deeply nested happy path.

```go
// Prefer
if condition {
    return err
}
// ... happy path

// Avoid
if !condition {
    // ... happy path (deeply nested)
}
```

---

## Testing

### Table-driven vs named test functions

Use **table-driven tests** when testing one function across multiple input variants that share the same setup structure:

```go
tests := []struct {
    name    string
    input   string
    want    string
    wantErr bool
}{
    {"valid input", "...", "...", false},
    {"empty input", "", "", true},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) { ... })
}
```

Use **named test functions** when each scenario has substantially different setup or context. Subtests (`t.Run`) inside named functions are fine when grouping related assertions, but a distinct function is clearer when the setup diverges.

### Error testing

Check presence or absence of error only — do not assert on error message strings. String matching is brittle: messages change, get wrapped, and vary by platform.

```go
// Prefer
if err == nil {
    t.Fatal("expected error")
}

// Avoid
if !strings.Contains(err.Error(), "process failed") {
    t.Fatalf("unexpected error: %v", err)
}
```

### Happy path assertions

Verify observable outputs, not just the absence of error. Checking `err == nil` alone does not confirm the output is correct.

### Fakes over mocks

Prefer hand-written fakes that implement the full interface over mocking libraries. A fake is explicit about what it does, requires no magic, and tends to surface interface design issues early. A need for complex mock setup is often a signal that the interface is too large or has mixed responsibilities.

### Integration tests

Tag integration tests with `//go:build integration`. They require external processes (Codex) and run via `make test-integration`, not the default `make test`.
The integration target runs packages sequentially, uncached, with streaming
verbose progress and a 10-minute
per-package timeout. Override it with
`make test-integration INTEGRATION_TEST_TIMEOUT=3m` when diagnosing a stalled run.
Live transcript scenarios report their current scenario and action.

---

## Notes

- This document will grow as more patterns are established. Add entries when a review surfaces a recurring style issue not already captured here.
- When in doubt, prefer the more readable option, even if it is slightly more verbose.
