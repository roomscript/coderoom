package monolith

import "github.com/roomscript/coderoom/internal/promptlang"

type submissionOutcome struct {
	statement promptlang.ParsedStatement
	raw       string
	operation string
}
