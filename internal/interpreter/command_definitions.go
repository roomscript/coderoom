package interpreter

import "github.com/roomscript/coderoom/internal/promptlang"

// nativeCommandDefinition binds statement dispatch to its help metadata.
// UI-only debug statements are registered without native handlers or help.
type nativeCommandDefinition struct {
	name    string
	message bool
	help    []HelpEntry
	matches func(promptlang.Statement) bool
	submit  func(*interpreterModel, string, promptlang.Statement) instructionSequence
}

func defineNativeCommand[T promptlang.Statement](name string, handler func(*interpreterModel, string, T) instructionSequence, help ...HelpEntry) nativeCommandDefinition {
	definition := nativeCommandDefinition{
		name: name, help: help,
		matches: func(statement promptlang.Statement) bool { _, ok := statement.(T); return ok },
	}
	if handler != nil {
		definition.submit = func(m *interpreterModel, raw string, statement promptlang.Statement) instructionSequence {
			return handler(m, raw, statement.(T))
		}
	}
	return definition
}

var nativeCommandDefinitions []nativeCommandDefinition

func init() {
	nativeCommandDefinitions = []nativeCommandDefinition{
		defineNativeCommand("policy", (*interpreterModel).submitPolicyEnable,
			HelpEntry{Usage: "/policy enable send-notices", Description: "notify listeners after direct sends"},
			HelpEntry{Usage: "/policy enable echo-invites", Description: "use deterministic echo agents for invitations"}),
		defineNativeCommand("invite", (*interpreterModel).submitInvite, HelpEntry{Usage: "/invite <alias>", Description: "start an agent"}),
		defineNativeCommand("remove", (*interpreterModel).submitRemove, HelpEntry{Usage: "/remove <alias>", Description: "remove an agent"}),
		defineNativeCommand("cancel", (*interpreterModel).submitCancel, HelpEntry{Usage: "/cancel <alias>", Description: "interrupt an agent's current turn"}),
		defineNativeCommand("handoff", submitStage[promptlang.Handoff], HelpEntry{Usage: "/handoff <from> <to>", Description: "transfer latest output between agents"}),
		defineNativeCommand("shell", (*interpreterModel).prepareShell, HelpEntry{Usage: "/shell <program>", Description: "execute a shell program"}),
		defineNativeCommand("def", (*interpreterModel).defineShellCommand, HelpEntry{Usage: "/def <name> /shell <program>", Description: "define a shell-backed command"}),
		defineNativeCommand("", (*interpreterModel).prepareShellCommand, HelpEntry{Usage: "/<name>", Description: "invoke a defined command"}),
		defineNativeCommand("loop", submitLoop, HelpEntry{Usage: "/loop @<alias> <prompt> /until /<name> /max <turns>", Description: "run a bounded participant loop"}),
		defineNativeCommand("who", submitWho, HelpEntry{Usage: "/who", Description: "list agents"}),
		defineNativeCommand("help", submitHelp, HelpEntry{Usage: "/help", Description: "show this message"}),
		defineNativeCommand("quit", submitQuit, HelpEntry{Usage: "/quit", Description: "exit"}),
		defineNativeCommand[promptlang.DebugView]("debugview", nil),
		defineNativeCommand[promptlang.DebugRows]("debugrows", nil),
		defineMessageCommand(submitStage[promptlang.Send], HelpEntry{Usage: "@<alias> <text>", Description: "send to one agent"}),
		defineMessageCommand(submitStage[promptlang.Broadcast], HelpEntry{Usage: "<text>", Description: "broadcast to all agents"}),
	}
}

func defineMessageCommand[T promptlang.Statement](handler func(*interpreterModel, string, T) instructionSequence, help HelpEntry) nativeCommandDefinition {
	definition := defineNativeCommand("", handler, help)
	definition.message = true
	return definition
}

func submitStage[T promptlang.Statement](m *interpreterModel, raw string, statement T) instructionSequence {
	return m.workflows.stage.start(raw, statement)
}

func submitLoop(m *interpreterModel, raw string, statement promptlang.Loop) instructionSequence {
	return m.workflows.loop.start(raw, statement, m.commands)
}

func submitWho(m *interpreterModel, raw string, _ promptlang.Who) instructionSequence {
	return m.submitWho(raw)
}
func submitHelp(m *interpreterModel, raw string, _ promptlang.Help) instructionSequence {
	return m.submitHelp(raw)
}
func submitQuit(m *interpreterModel, raw string, _ promptlang.Quit) instructionSequence {
	return m.submitQuit(raw)
}
