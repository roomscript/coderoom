package interpreter

func (*interpreterModel) submitHelp(raw string) instructionSequence {
	return append(acceptedInputSequence(raw),
		publishSnapshotInstruction{},
		publishEventInstruction{event: helpListing()},
		publishEventInstruction{event: SubmissionSucceeded{Raw: raw}},
	)
}

func helpListing() HelpListed {
	var listing HelpListed
	for _, definition := range nativeCommandDefinitions {
		if definition.message {
			listing.Messages = append(listing.Messages, definition.help...)
			continue
		}
		listing.Commands = append(listing.Commands, definition.help...)
	}
	return listing
}
