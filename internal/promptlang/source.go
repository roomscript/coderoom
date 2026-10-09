package promptlang

// Span is a half-open range of UTF-8 byte offsets in the original submission.
// An empty range identifies an insertion point, for example a missing argument.
type Span struct {
	Start int
	End   int
}

// Located keeps a semantic value and its source range together.
// Runtime-created values may leave Span zero.
type Located[T any] struct {
	Value T
	Span  Span
}

// ParsedStatement pairs statement content with its range in the original input.
type ParsedStatement = Located[Statement]

// DiagnosticCode identifies a diagnostic independently of its display text.
type DiagnosticCode string

// Stable syntax diagnostic categories. Runtime diagnostics may use application
// error codes, preserving the same source-location contract.
const (
	DiagnosticEmptyInput        DiagnosticCode = "empty_input"
	DiagnosticMissingArgument   DiagnosticCode = "missing_argument"
	DiagnosticUnexpectedInput   DiagnosticCode = "unexpected_input"
	DiagnosticInvalidIdentifier DiagnosticCode = "invalid_identifier"
	DiagnosticInvalidArgument   DiagnosticCode = "invalid_argument"
	DiagnosticUnknownCommand    DiagnosticCode = "unknown_command"
	DiagnosticReservedCommand   DiagnosticCode = "reserved_command"
	DiagnosticCommandExists     DiagnosticCode = "command_exists"
	DiagnosticUndefinedCommand  DiagnosticCode = "undefined_command"
)

// Diagnostic reports a categorized error at a source range. Cause preserves
// existing error identities through errors.Is and errors.As.
type Diagnostic struct {
	Code    DiagnosticCode
	Span    Span
	Message string
	Cause   error
}

func (d *Diagnostic) Error() string { return d.Message }
func (d *Diagnostic) Unwrap() error { return d.Cause }

func diagnosticForError(code DiagnosticCode, span Span, cause error) *Diagnostic {
	return &Diagnostic{Code: code, Span: span, Message: cause.Error(), Cause: cause}
}
