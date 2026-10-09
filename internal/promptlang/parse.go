package promptlang

import (
	"fmt"
	"slices"
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

// Parse reads one complete submission. Prompts and shell programs remain opaque;
// statement and argument spans refer to the original, untrimmed line.
func Parse(line string) (ParsedStatement, error) {
	input := sourceCursor{source: line, end: len(line)}.trim()
	statement, err := parseStatement(input)
	if err != nil {
		return ParsedStatement{}, err
	}
	return ParsedStatement{Value: statement, Span: input.span()}, nil
}

func parseStatement(input sourceCursor) (Statement, error) {
	if input.text() == "" {
		return nil, input.diagnostic(DiagnosticEmptyInput, "input is empty")
	}
	if strings.HasPrefix(input.text(), "/") {
		return parseSlash(input)
	}
	if strings.HasPrefix(input.text(), "@") {
		return parseSend(input)
	}
	return Broadcast{Text: input.locatedText()}, nil
}

// Built-in syntax is registered independently of runtime command handlers.
// Adding a form does not require changing Parse or the slash dispatch algorithm.
var builtinParsers = map[string]func(sourceCursor, sourceCursor) (Statement, error){
	"/invite":  parseAliasStatement,
	"/remove":  parseAliasStatement,
	"/cancel":  parseAliasStatement,
	"/handoff": parseHandoff,
	"/shell":   parseShell,
	"/policy":  parsePolicy,
	"/def":     parseDefinition,
	"/loop":    parseLoop,
}

// BuiltinNames returns the recognized built-in command names without slashes.
// It describes syntax, not which runtime implements each command.
func BuiltinNames() []string {
	names := make([]string, 0, len(noArgCommands)+len(builtinParsers))
	for name := range noArgCommands {
		names = append(names, strings.TrimPrefix(name, "/"))
	}
	for name := range builtinParsers {
		names = append(names, strings.TrimPrefix(name, "/"))
	}
	slices.Sort(names)
	return names
}

func parseSlash(input sourceCursor) (Statement, error) {
	command, rest := input.token()
	if statement, ok := noArgCommands[command.text()]; ok {
		if rest.text() != "" {
			return nil, rest.diagnostic(DiagnosticUnexpectedInput, command.text()+" does not accept arguments")
		}
		return statement, nil
	}
	if parser, ok := builtinParsers[command.text()]; ok {
		return parser(input, rest)
	}
	return parseInvocation(input, command, rest)
}

func parseAliasStatement(input, rest sourceCursor) (Statement, error) {
	command, _ := input.token()
	if rest.text() == "" {
		return nil, rest.diagnostic(DiagnosticMissingArgument, "usage: "+command.text()+" <alias>")
	}
	// Preserve the current grammar: these commands accept the complete remainder
	// as an alias. Identifier validation remains the session's responsibility.
	switch command.text() {
	case "/invite":
		return Invite{Alias: rest.locatedText()}, nil
	case "/remove":
		return Remove{Alias: rest.locatedText()}, nil
	default:
		return Cancel{Alias: rest.locatedText()}, nil
	}
}

func parseHandoff(_, rest sourceCursor) (Statement, error) {
	// Handoff historically uses Unicode whitespace between its two arguments.
	from, remainder := rest.field()
	to, extra := remainder.field()
	if from.text() == "" || to.text() == "" {
		return nil, to.diagnostic(DiagnosticMissingArgument, "usage: /handoff <from> <to>")
	}
	if extra.text() != "" {
		return nil, extra.diagnostic(DiagnosticUnexpectedInput, "usage: /handoff <from> <to>")
	}
	return Handoff{FromAlias: from.locatedText(), ToAlias: to.locatedText()}, nil
}

func parseShell(_, program sourceCursor) (Statement, error) {
	if program.text() == "" {
		return nil, program.diagnostic(DiagnosticMissingArgument, "usage: /shell <program>")
	}
	return Shell{Program: program.locatedText()}, nil
}

func parsePolicy(_, rest sourceCursor) (Statement, error) {
	action, name := rest.token()
	policyName := policy.Name(name.text())
	if action.text() != "enable" {
		return nil, action.argumentDiagnostic("usage: /policy enable <send-notices|echo-invites>")
	}
	if policyName != policy.SendNotices && policyName != policy.EchoInvites {
		return nil, name.argumentDiagnostic("usage: /policy enable <send-notices|echo-invites>")
	}
	return PolicyEnable{Name: Located[policy.Name]{Value: policyName, Span: name.span()}}, nil
}

func parseDefinition(_, rest sourceCursor) (Statement, error) {
	name, body := rest.token()
	if !isIdentifier(name.text()) {
		return nil, name.identifierDiagnostic("invalid command name")
	}
	command, program := body.token()
	if command.text() != "/shell" || program.text() == "" {
		return nil, body.argumentDiagnostic("usage: /def <name> /shell <program>")
	}
	return CommandDefinition{
		Name: name.locatedText(),
		Body: Located[Shell]{Value: Shell{Program: program.locatedText()}, Span: body.span()},
	}, nil
}

func parseInvocation(_ sourceCursor, command, rest sourceCursor) (Statement, error) {
	name := command.afterPrefix()
	if rest.text() != "" || !isIdentifier(name.text()) || isReservedCommand(name.text()) {
		cause := UnknownCommandError{Cmd: command.text()}
		site := name
		if rest.text() != "" {
			site = rest
		}
		return nil, &Diagnostic{Code: DiagnosticUnknownCommand, Span: site.span(), Message: cause.Error(), Cause: cause}
	}
	return CommandInvocation{Name: name.locatedText()}, nil
}

func parseSend(input sourceCursor) (Statement, error) {
	rest := input.afterPrefix()
	index := strings.IndexByte(rest.text(), ' ')
	if index < 0 {
		return nil, (sourceCursor{source: input.source, start: input.end, end: input.end}).diagnostic(DiagnosticMissingArgument, "usage: @<alias> <text>")
	}
	alias := (sourceCursor{source: input.source, start: rest.start, end: rest.start + index}).trim()
	text := (sourceCursor{source: input.source, start: rest.start + index + 1, end: rest.end}).trim()
	if alias.text() == "" {
		return nil, alias.diagnostic(DiagnosticMissingArgument, "usage: @<alias> <text>")
	}
	if text.text() == "" {
		return nil, text.diagnostic(DiagnosticMissingArgument, "usage: @<alias> <text>")
	}
	return Send{Alias: alias.locatedText(), Text: text.locatedText()}, nil
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

func parsePositiveInteger(text string) (int, error) {
	for _, char := range text {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("invalid positive integer")
		}
	}
	value, err := strconv.Atoi(text)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}
