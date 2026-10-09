package interpreter

import "github.com/roomscript/coderoom/internal/promptlang"

func submitDebugView(_ *interpreterModel, raw string, _ promptlang.DebugView) instructionSequence {
	return debugRequestSequence(raw, DebugActionView)
}

func submitDebugRows(_ *interpreterModel, raw string, _ promptlang.DebugRows) instructionSequence {
	return debugRequestSequence(raw, DebugActionRows)
}

func debugRequestSequence(raw string, action DebugAction) instructionSequence {
	return append(acceptedInputSequence(raw),
		publishEventInstruction{event: DebugRequested{Action: action}},
		publishEventInstruction{event: SubmissionSucceeded{Raw: raw}},
	)
}
