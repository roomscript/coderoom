package promptlang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/roomscript/coderoom/internal/policy"
)

var noArgCommands = map[string]Statement{
	"/who":       Who{},
	"/help":      Help{},
	"/quit":      Quit{},
	"/debugview": DebugView{},
	"/debugrows": DebugRows{},
}

// Parse trims line and parses it into a Statement.
// It returns an error for malformed input or unknown slash commands.
func Parse(line string) (Statement, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("input is empty")
	}
	if strings.HasPrefix(line, "/") {
		return parseSlash(line)
	}
	if strings.HasPrefix(line, "@") {
		return parseSend(line[1:])
	}
	return Broadcast{Text: line}, nil
}

func parseSlash(line string) (Statement, error) {
	cmd, rest := cutToken(line)
	rest = strings.TrimSpace(rest)
	if statement, ok := noArgCommands[cmd]; ok {
		return parseNoArgCommand(cmd, rest, statement)
	}
	switch cmd {
	case "/invite", "/remove", "/cancel":
		return parseAliasStatement(cmd, rest)
	case "/handoff":
		fromAlias, toAlias, err := parseHandoffArgs(rest)
		if err != nil {
			return nil, err
		}
		return Handoff{FromAlias: fromAlias, ToAlias: toAlias}, nil
	case "/shell", "/policy":
		return parseRuntimeCommand(cmd, rest)
	case "/def":
		return parseDefinition(rest)
	case "/loop":
		return parseLoop(rest)
	default:
		return parseInvocation(cmd, rest)
	}
}

func parseRuntimeCommand(cmd, rest string) (Statement, error) {
	if cmd == "/policy" {
		return parsePolicy(rest)
	}
	if rest == "" {
		return nil, fmt.Errorf("usage: /shell <program>")
	}
	return Shell{Program: rest}, nil
}

func parsePolicy(rest string) (Statement, error) {
	action, name := cutToken(rest)
	policyName := policy.Name(strings.TrimSpace(name))
	if action != "enable" || (policyName != policy.SendNotices && policyName != policy.EchoInvites) {
		return nil, fmt.Errorf("usage: /policy enable <send-notices|echo-invites>")
	}
	return PolicyEnable{Name: policyName}, nil
}

func parseLoop(rest string) (Statement, error) {
	participantReference, remainder := cutToken(rest)
	participant := strings.TrimPrefix(participantReference, "@")
	if !strings.HasPrefix(participantReference, "@") || !isIdentifier(participant) {
		return nil, fmt.Errorf("invalid loop participant")
	}
	prompt, condition, maxTurns, err := parseLoopSuffix(remainder)
	if err != nil {
		return nil, err
	}
	return Loop{
		Participant: participant,
		Prompt:      prompt,
		Condition:   condition,
		MaxTurns:    maxTurns,
	}, nil
}

func parseLoopSuffix(input string) (string, string, int, error) {
	remainder, maxText := popToken(input)
	remainder, maxKeyword := popToken(remainder)
	remainder, conditionReference := popToken(remainder)
	prompt, untilKeyword := popToken(remainder)
	if maxKeyword != "/max" || untilKeyword != "/until" || strings.TrimSpace(prompt) == "" {
		return "", "", 0, fmt.Errorf("usage: /loop @<participant> <prompt> /until /<command> /max <turns>")
	}
	condition, err := parseCommandReference(conditionReference)
	if err != nil {
		return "", "", 0, err
	}
	maxTurns, err := parsePositiveInteger(maxText)
	if err != nil {
		return "", "", 0, err
	}
	return strings.TrimSpace(prompt), condition, maxTurns, nil
}

func parseCommandReference(reference string) (string, error) {
	name := strings.TrimPrefix(reference, "/")
	if !strings.HasPrefix(reference, "/") || !isIdentifier(name) || isReservedCommand(name) {
		return "", fmt.Errorf("invalid loop condition")
	}
	return name, nil
}

func parsePositiveInteger(text string) (int, error) {
	if !isDecimalInteger(text) {
		return 0, fmt.Errorf("invalid positive integer")
	}
	value, err := strconv.Atoi(text)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}

func isDecimalInteger(text string) bool {
	if text == "" {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func parseDefinition(rest string) (Statement, error) {
	name, body := cutToken(rest)
	if !isIdentifier(name) {
		return nil, fmt.Errorf("invalid command name")
	}
	bodyCommand, program := cutToken(strings.TrimSpace(body))
	if bodyCommand != "/shell" || strings.TrimSpace(program) == "" {
		return nil, fmt.Errorf("usage: /def <name> /shell <program>")
	}
	return CommandDefinition{
		Name: name,
		Body: Shell{Program: strings.TrimSpace(program)},
	}, nil
}

func parseAliasStatement(cmd, alias string) (Statement, error) {
	if alias == "" {
		return nil, fmt.Errorf("usage: %s <alias>", cmd)
	}
	switch cmd {
	case "/invite":
		return Invite{Alias: alias}, nil
	case "/remove":
		return Remove{Alias: alias}, nil
	default:
		return Cancel{Alias: alias}, nil
	}
}

func parseHandoffArgs(rest string) (string, string, error) {
	parts := strings.Fields(rest)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("usage: /handoff <from> <to>")
	}
	return parts[0], parts[1], nil
}

func parseNoArgCommand(cmd, rest string, statement Statement) (Statement, error) {
	if rest != "" {
		return nil, fmt.Errorf("%s does not accept arguments", cmd)
	}
	return statement, nil
}

func parseInvocation(cmd, rest string) (Statement, error) {
	name := strings.TrimPrefix(cmd, "/")
	if rest != "" || !isIdentifier(name) || isReservedCommand(name) {
		return nil, UnknownCommandError{Cmd: cmd}
	}
	return CommandInvocation{Name: name}, nil
}

func cutToken(input string) (string, string) {
	index := strings.IndexAny(input, " \t\r\n")
	if index < 0 {
		return input, ""
	}
	return input[:index], input[index+1:]
}

func popToken(input string) (string, string) {
	input = strings.TrimSpace(input)
	index := strings.LastIndexAny(input, " \t\r\n")
	if index < 0 {
		return "", input
	}
	return strings.TrimSpace(input[:index]), input[index+1:]
}

func isIdentifier(name string) bool {
	for index, char := range name {
		if isASCIILetter(char) || index > 0 && isIdentifierPart(char) {
			continue
		}
		return false
	}
	return name != ""
}

func isASCIILetter(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func isIdentifierPart(char rune) bool {
	return char >= '0' && char <= '9' || char == '-' || char == '_'
}

func isReservedCommand(name string) bool {
	switch name {
	case "invite", "remove", "cancel", "handoff", "who", "help", "quit",
		"policy", "shell", "def", "loop", "debugview", "debugrows":
		return true
	default:
		return false
	}
}

func parseSend(rest string) (Statement, error) {
	alias, text, ok := strings.Cut(rest, " ")
	alias = strings.TrimSpace(alias)
	if !ok || alias == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("usage: @<alias> <text>")
	}
	return Send{Alias: alias, Text: strings.TrimSpace(text)}, nil
}
