package interpreter

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func (m *interpreterModel) submitHelp(raw string) instructionSequence {
	return append(acceptedInputSequence(raw),
		publishSnapshotInstruction{},
		publishEventInstruction{event: m.helpListing()},
		publishEventInstruction{event: SubmissionSucceeded{Raw: raw}},
	)
}

func helpListing() HelpListed { return moduleHelpListing(newModuleRegistry()) }

func (m *interpreterModel) helpListing() HelpListed { return moduleHelpListing(m.modules) }

func moduleHelpListing(registry *runtime.Registry) HelpListed {
	var listing HelpListed
	listed := map[string]bool{}
	for _, definition := range nativeCommandDefinitions {
		if definition.module != nil {
			command, ok := registry.Lookup(promptlang.ParsedStatement{Value: definition.module.Statement()})
			if ok {
				listing.Commands = append(listing.Commands, HelpEntry{Usage: command.Usage(), Description: command.Description()})
				listed[command.Name()] = true
			}
			continue
		}
		if definition.message {
			listing.Messages = append(listing.Messages, definition.help...)
			continue
		}
		listing.Commands = append(listing.Commands, definition.help...)
	}
	for _, command := range registry.Entries() {
		if !listed[command.Name()] {
			listing.Commands = append(listing.Commands, HelpEntry{Usage: command.Usage(), Description: command.Description()})
		}
	}
	return listing
}
