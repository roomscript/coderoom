package promptlang_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestParse_slashCommands(t *testing.T) {
	tests := []struct {
		input string
		want  promptlang.Statement
	}{
		{"/invite ada", promptlang.Invite{Alias: "ada"}},
		{"/invite   ada  ", promptlang.Invite{Alias: "ada"}},
		{"/remove ada", promptlang.Remove{Alias: "ada"}},
		{"/cancel ada", promptlang.Cancel{Alias: "ada"}},
		{"/handoff ada turing", promptlang.Handoff{FromAlias: "ada", ToAlias: "turing"}},
		{"/policy enable send-notices", promptlang.PolicyEnable{Name: policy.SendNotices}},
		{"/policy enable echo-invites", promptlang.PolicyEnable{Name: policy.EchoInvites}},
		{"/shell go test ./...", promptlang.Shell{Program: "go test ./..."}},
		{`/shell echo "hello world" | tee out`, promptlang.Shell{Program: `echo "hello world" | tee out`}},
		{"/def tests /shell go test ./...", promptlang.CommandDefinition{Name: "tests", Body: promptlang.Shell{Program: "go test ./..."}}},
		{"/def check-tests_2 /shell go test ./...", promptlang.CommandDefinition{Name: "check-tests_2", Body: promptlang.Shell{Program: "go test ./..."}}},
		{"/def help /shell go test ./...", promptlang.CommandDefinition{Name: "help", Body: promptlang.Shell{Program: "go test ./..."}}},
		{"/tests", promptlang.CommandInvocation{Name: "tests"}},
		{"/check-tests_2", promptlang.CommandInvocation{Name: "check-tests_2"}},
		{"/who", promptlang.Who{}},
		{"/help", promptlang.Help{}},
		{"/quit", promptlang.Quit{}},
	}
	for _, tt := range tests {
		got, err := promptlang.Parse(tt.input)
		if err != nil {
			t.Errorf("Parse(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParse_sendAction(t *testing.T) {
	got, err := promptlang.Parse("@ada do the thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := promptlang.Send{Alias: "ada", Text: "do the thing"}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParse_loop(t *testing.T) {
	tests := []struct {
		input string
		want  promptlang.Loop
	}{
		{
			"/loop @ada make the tests pass /until /tests /max 3",
			promptlang.Loop{Participant: "ada", Prompt: "make the tests pass", Condition: "tests", MaxTurns: 3},
		},
		{
			"/loop @agent-2 discuss /max and /until markers /until /check_tests /max 12",
			promptlang.Loop{Participant: "agent-2", Prompt: "discuss /max and /until markers", Condition: "check_tests", MaxTurns: 12},
		},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := promptlang.Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse: unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Parse = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParse_loopErrors(t *testing.T) {
	tests := []string{
		"/loop",
		"/loop ada fix tests /until /tests /max 3",
		"/loop @ fix tests /until /tests /max 3",
		"/loop @ada /until /tests /max 3",
		"/loop @ada fix tests /max 3",
		"/loop @ada fix tests /until /max 3",
		"/loop @ada fix tests /until tests /max 3",
		"/loop @ada fix tests /until /help /max 3",
		"/loop @ada fix tests /until /tests",
		"/loop @ada fix tests /until /tests /max 0",
		"/loop @ada fix tests /until /tests /max -1",
		"/loop @ada fix tests /until /tests /max many",
		"/loop @ada fix tests /until /tests /max 999999999999999999999999",
		"/loop @ada fix tests /until /tests /max 3 trailing",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := promptlang.Parse(input); err == nil {
				t.Fatal("Parse: expected error")
			}
		})
	}
}

func TestParse_broadcast(t *testing.T) {
	got, err := promptlang.Parse("hello everyone")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (promptlang.Broadcast{Text: "hello everyone"}) {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestParse_trimming(t *testing.T) {
	got, err := promptlang.Parse("  /invite ada  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (promptlang.Invite{Alias: "ada"}) {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestParse_errors(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"/invite"},
		{"/invite   "},
		{"/remove"},
		{"/remove   "},
		{"/cancel"},
		{"/cancel   "},
		{"/handoff"},
		{"/handoff ada"},
		{"/handoff ada turing extra"},
		{"/policy"},
		{"/policy enable"},
		{"/policy disable send-notices"},
		{"/policy disable echo-invites"},
		{"/policy enable unknown"},
		{"/policy enable send-notices extra"},
		{"/shell"},
		{"/shell   "},
		{"/def"},
		{"/def tests"},
		{"/def tests /who"},
		{"/def tests /shell"},
		{"/def 1tests /shell go test ./..."},
		{"/def test! /shell go test ./..."},
		{"/tests extra"},
		{"/1tests"},
		{"/test!"},
		{"/"},
		{"/who extra"},
		{"@ada"},
		{"@ada   "},
		{"@ ada hi"}, // space between @ and alias
		{""},         // empty
		{"   "},      // whitespace only
	}
	for _, tt := range tests {
		_, err := promptlang.Parse(tt.input)
		if err == nil {
			t.Errorf("Parse(%q): expected error, got nil", tt.input)
		}
	}
}
