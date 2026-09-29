package interpreter

func (*interpreterModel) submitHelp(raw string) instructionSequence {
	return append(acceptedInputSequence(raw),
		publishSnapshotInstruction{},
		publishEventInstruction{event: helpListing()},
		publishEventInstruction{event: SubmissionSucceeded{Raw: raw}},
	)
}

func helpListing() HelpListed {
	return HelpListed{
		Commands: []HelpEntry{
			{Usage: "/policy enable send-notices", Description: "notify listeners after direct sends"},
			{Usage: "/policy enable echo-invites", Description: "use deterministic echo agents for invitations"},
			{Usage: "/invite <alias>", Description: "start an agent"},
			{Usage: "/remove <alias>", Description: "remove an agent"},
			{Usage: "/cancel <alias>", Description: "interrupt an agent's current turn"},
			{Usage: "/handoff <from> <to>", Description: "transfer latest output between agents"},
			{Usage: "/shell <program>", Description: "execute a shell program"},
			{Usage: "/def <name> /shell <program>", Description: "define a shell-backed command"},
			{Usage: "/<name>", Description: "invoke a defined command"},
			{Usage: "/loop @<alias> <prompt> /until /<name> /max <turns>", Description: "run a bounded participant loop"},
			{Usage: "/who", Description: "list agents"},
			{Usage: "/help", Description: "show this message"},
			{Usage: "/quit", Description: "exit"},
		},
		Messages: []HelpEntry{
			{Usage: "@<alias> <text>", Description: "send to one agent"},
			{Usage: "<text>", Description: "broadcast to all agents"},
		},
	}
}
