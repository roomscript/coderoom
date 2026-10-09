package promptlang

import "strings"

func parseLoop(_, rest sourceCursor) (Statement, error) {
	reference, remainder := rest.token()
	participant := strings.TrimPrefix(reference.text(), "@")
	if !strings.HasPrefix(reference.text(), "@") || !isIdentifier(participant) {
		return nil, reference.identifierDiagnostic("invalid loop participant")
	}
	prompt, condition, bound, err := parseLoopSuffix(remainder)
	if err != nil {
		return nil, err
	}
	maxTurns, err := parsePositiveInteger(bound.text())
	if err != nil {
		return nil, bound.argumentDiagnostic(err.Error())
	}
	return Loop{
		Participant: reference.afterPrefix().locatedText(),
		Prompt:      prompt.locatedText(), Condition: condition.locatedText(),
		MaxTurns: Located[int]{Value: maxTurns, Span: bound.span()},
	}, nil
}

func parseLoopSuffix(input sourceCursor) (sourceCursor, sourceCursor, sourceCursor, error) {
	remainder, bound := input.lastToken()
	remainder, maxKeyword := remainder.lastToken()
	remainder, condition := remainder.lastToken()
	prompt, untilKeyword := remainder.lastToken()
	usage := "usage: /loop @<participant> <prompt> /until /<command> /max <turns>"
	if maxKeyword.text() != "/max" {
		return prompt, condition, bound, maxKeyword.argumentDiagnostic(usage)
	}
	if untilKeyword.text() != "/until" {
		return prompt, condition, bound, untilKeyword.argumentDiagnostic(usage)
	}
	if prompt.text() == "" {
		return prompt, condition, bound, prompt.diagnostic(DiagnosticMissingArgument, usage)
	}
	if !strings.HasPrefix(condition.text(), "/") {
		return prompt, condition, bound, condition.identifierDiagnostic("invalid loop condition")
	}
	condition = condition.afterPrefix()
	if !isIdentifier(condition.text()) || isReservedCommand(condition.text()) {
		return prompt, condition, bound, condition.identifierDiagnostic("invalid loop condition")
	}
	return prompt, condition, bound, nil
}
