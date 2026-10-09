package interpreter

func (*interpreterModel) submitWho(raw string) instructionSequence {
	return append(acceptedInputSequence(raw), readParticipantsInstruction{raw: raw})
}
