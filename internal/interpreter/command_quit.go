package interpreter

func (*interpreterModel) submitQuit(raw string) instructionSequence {
	return append(acceptedInputSequence(raw),
		publishSnapshotInstruction{},
		publishEventInstruction{event: SubmissionSucceeded{Raw: raw}},
		requestCloseInstruction{},
		shutdownSessionInstruction{},
		publishEventInstruction{event: ExitRequested{}},
	)
}
