package promptlang_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestParse_sourceRanges(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]promptlang.Span
	}{
		{"invite", "  /invite ada  ", map[string]promptlang.Span{"Span": {Start: 2, End: 13}, "Value.Alias.Span": {Start: 10, End: 13}}},
		{"remove", "  /remove ada  ", map[string]promptlang.Span{"Span": {Start: 2, End: 13}, "Value.Alias.Span": {Start: 10, End: 13}}},
		{"cancel", "  /cancel ada  ", map[string]promptlang.Span{"Span": {Start: 2, End: 13}, "Value.Alias.Span": {Start: 10, End: 13}}},
		{"handoff", "  /handoff ada turing  ", map[string]promptlang.Span{"Span": {Start: 2, End: 21}, "Value.FromAlias.Span": {Start: 11, End: 14}, "Value.ToAlias.Span": {Start: 15, End: 21}}},
		{"policy", "  /policy enable send-notices  ", map[string]promptlang.Span{"Span": {Start: 2, End: 29}, "Value.Name.Span": {Start: 17, End: 29}}},
		{"shell UTF-8", " \t/shell echo \"λ\" \n", map[string]promptlang.Span{"Span": {Start: 2, End: 18}, "Value.Program.Span": {Start: 9, End: 18}}},
		{"definition", "/def tests /shell true", map[string]promptlang.Span{"Span": {Start: 0, End: 22}, "Value.Name.Span": {Start: 5, End: 10}, "Value.Body.Span": {Start: 11, End: 22}, "Value.Body.Value.Program.Span": {Start: 18, End: 22}}},
		{"invocation", "  /tests  ", map[string]promptlang.Span{"Span": {Start: 2, End: 8}, "Value.Name.Span": {Start: 3, End: 8}}},
		{"loop", "/loop @ada fix /until /tests /max 3", map[string]promptlang.Span{"Span": {Start: 0, End: 35}, "Value.Participant.Span": {Start: 7, End: 10}, "Value.Prompt.Span": {Start: 11, End: 14}, "Value.Condition.Span": {Start: 23, End: 28}, "Value.MaxTurns.Span": {Start: 34, End: 35}}},
		{"send multiline UTF-8", "  @ada hello /help\nλ  ", map[string]promptlang.Span{"Span": {Start: 2, End: 21}, "Value.Alias.Span": {Start: 3, End: 6}, "Value.Text.Span": {Start: 7, End: 21}}},
		{"broadcast", "  hello @ada /shell λ  ", map[string]promptlang.Span{"Span": {Start: 2, End: 22}, "Value.Text.Span": {Start: 2, End: 22}}},
		{"who", "  /who  ", map[string]promptlang.Span{"Span": {Start: 2, End: 6}}},
		{"help", "  /help  ", map[string]promptlang.Span{"Span": {Start: 2, End: 7}}},
		{"quit", "  /quit  ", map[string]promptlang.Span{"Span": {Start: 2, End: 7}}},
		{"debug view", "  /debugview  ", map[string]promptlang.Span{"Span": {Start: 2, End: 12}}},
		{"debug rows", "  /debugrows  ", map[string]promptlang.Span{"Span": {Start: 2, End: 12}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statement, err := promptlang.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			got := statementSpans(reflect.ValueOf(statement))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("spans = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// Inspect the public AST contract, including the nested shell body, so the table
// checks every location field rather than silently leaving newly added fields out.
func statementSpans(value reflect.Value) map[string]promptlang.Span {
	spans := map[string]promptlang.Span{}
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		name := value.Type().Field(index).Name
		switch field.Type() {
		case reflect.TypeOf(promptlang.Span{}):
			spans[name] = field.Interface().(promptlang.Span)
		default:
			if field.Kind() == reflect.Interface {
				field = field.Elem()
			}
			if field.Kind() == reflect.Struct {
				for nestedName, span := range statementSpans(field) {
					spans[name+"."+nestedName] = span
				}
			}
		}
	}
	return spans
}

func TestParse_diagnostics(t *testing.T) {
	tests := []struct {
		raw  string
		code promptlang.DiagnosticCode
		span promptlang.Span
	}{
		{"   ", promptlang.DiagnosticEmptyInput, promptlang.Span{Start: 3, End: 3}},
		{"  /invite  ", promptlang.DiagnosticMissingArgument, promptlang.Span{Start: 9, End: 9}},
		{"/who extra", promptlang.DiagnosticUnexpectedInput, promptlang.Span{Start: 5, End: 10}},
		{"/def 1tests /shell true", promptlang.DiagnosticInvalidIdentifier, promptlang.Span{Start: 5, End: 11}},
		{"/def tests /who", promptlang.DiagnosticInvalidArgument, promptlang.Span{Start: 11, End: 15}},
		{"/policy enable unknown", promptlang.DiagnosticInvalidArgument, promptlang.Span{Start: 15, End: 22}},
		{"@ada", promptlang.DiagnosticMissingArgument, promptlang.Span{Start: 4, End: 4}},
		{"/shell", promptlang.DiagnosticMissingArgument, promptlang.Span{Start: 6, End: 6}},
		{"/handoff ada", promptlang.DiagnosticMissingArgument, promptlang.Span{Start: 12, End: 12}},
		{"/handoff ada ben extra", promptlang.DiagnosticUnexpectedInput, promptlang.Span{Start: 17, End: 22}},
		{"/tests extra", promptlang.DiagnosticUnknownCommand, promptlang.Span{Start: 7, End: 12}},
		{"/loop ada fix /until /tests /max 3", promptlang.DiagnosticInvalidIdentifier, promptlang.Span{Start: 6, End: 9}},
		{"/loop @ada fix /until /tests /max many", promptlang.DiagnosticInvalidArgument, promptlang.Span{Start: 34, End: 38}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			_, err := promptlang.Parse(tt.raw)
			var diagnostic *promptlang.Diagnostic
			if !errors.As(err, &diagnostic) {
				t.Fatalf("error = %v, want Diagnostic", err)
			}
			if diagnostic.Code != tt.code || diagnostic.Span != tt.span {
				t.Fatalf("diagnostic = %#v, want %s at %#v", diagnostic, tt.code, tt.span)
			}
		})
	}
}

func TestParse_unknownDiagnosticPreservesErrorIdentity(t *testing.T) {
	_, err := promptlang.Parse("/tests extra")
	var unknown promptlang.UnknownCommandError
	if !errors.As(err, &unknown) || unknown.Cmd != "/tests" {
		t.Fatalf("error = %v, want UnknownCommandError for /tests", err)
	}
}

func TestRegistry_retainsDefinitionSource(t *testing.T) {
	statement, err := promptlang.Parse("  /def tests /shell true  ")
	if err != nil {
		t.Fatal(err)
	}
	definition := statement.Value.(promptlang.CommandDefinition)
	registry := promptlang.NewRegistry()
	if err := registry.Define(definition); err != nil {
		t.Fatal(err)
	}
	body, err := registry.Resolve(promptlang.CommandInvocation{Name: located("tests")})
	if err != nil || body != definition.Body {
		t.Fatalf("body = %#v, error = %v; want %#v", body, err, definition.Body)
	}
	var diagnostic *promptlang.Diagnostic
	if err := registry.Define(definition); !errors.As(err, &diagnostic) || diagnostic.Code != promptlang.DiagnosticCommandExists || diagnostic.Span != definition.Name.Span {
		t.Fatalf("duplicate diagnostic = %#v, error = %v", diagnostic, err)
	}
}
