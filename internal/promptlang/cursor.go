package promptlang

import (
	"strings"
	"unicode"
)

// sourceCursor slices the original input instead of reconstructing token text.
// It scans only structural boundaries; payloads are never lexed as syntax.
type sourceCursor struct {
	source string
	start  int
	end    int
}

func (c sourceCursor) text() string { return c.source[c.start:c.end] }
func (c sourceCursor) span() Span   { return Span{Start: c.start, End: c.end} }
func (c sourceCursor) locatedText() Located[string] {
	return Located[string]{Value: c.text(), Span: c.span()}
}

func (c sourceCursor) trim() sourceCursor {
	left := strings.TrimLeftFunc(c.text(), unicode.IsSpace)
	c.start = c.end - len(left)
	c.end = c.start + len(strings.TrimRightFunc(left, unicode.IsSpace))
	return c
}

func (c sourceCursor) afterPrefix() sourceCursor {
	c.start++
	return c
}

func (c sourceCursor) token() (sourceCursor, sourceCursor) {
	index := strings.IndexAny(c.text(), " \t\r\n")
	if index < 0 {
		return c, sourceCursor{source: c.source, start: c.end, end: c.end}
	}
	token := sourceCursor{source: c.source, start: c.start, end: c.start + index}
	rest := sourceCursor{source: c.source, start: token.end + 1, end: c.end}
	return token, rest.trim()
}

func (c sourceCursor) field() (sourceCursor, sourceCursor) {
	c = c.trim()
	index := strings.IndexFunc(c.text(), unicode.IsSpace)
	if index < 0 {
		return c, sourceCursor{source: c.source, start: c.end, end: c.end}
	}
	token := sourceCursor{source: c.source, start: c.start, end: c.start + index}
	rest := sourceCursor{source: c.source, start: token.end, end: c.end}
	return token, rest.trim()
}

func (c sourceCursor) lastToken() (sourceCursor, sourceCursor) {
	c = c.trim()
	index := strings.LastIndexAny(c.text(), " \t\r\n")
	if index < 0 {
		return sourceCursor{source: c.source, start: c.start, end: c.start}, c
	}
	rest := sourceCursor{source: c.source, start: c.start, end: c.start + index}
	token := sourceCursor{source: c.source, start: rest.end + 1, end: c.end}
	return rest.trim(), token
}

func (c sourceCursor) diagnostic(code DiagnosticCode, message string) *Diagnostic {
	return &Diagnostic{Code: code, Span: c.span(), Message: message}
}

func (c sourceCursor) argumentDiagnostic(message string) *Diagnostic {
	if c.text() == "" {
		return c.diagnostic(DiagnosticMissingArgument, message)
	}
	return c.diagnostic(DiagnosticInvalidArgument, message)
}

func (c sourceCursor) identifierDiagnostic(message string) *Diagnostic {
	if c.text() == "" {
		return c.diagnostic(DiagnosticMissingArgument, message)
	}
	return c.diagnostic(DiagnosticInvalidIdentifier, message)
}
