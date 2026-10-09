package interpreter

import (
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func TestHandoffStage_completeSourceTurn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		event     session.AgentMessage
		completes bool
	}{
		{name: "other participant", event: session.AgentMessage{Alias: "ben", TurnID: 7, TurnCompleted: true}},
		{name: "unfinished output", event: session.AgentMessage{Alias: "ada", TurnID: 7}},
		{name: "older turn", event: session.AgentMessage{Alias: "ada", TurnID: 6, TurnCompleted: true}},
		{name: "current turn", event: session.AgentMessage{Alias: "ada", TurnID: 7, TurnCompleted: true}, completes: true},
		{name: "newer turn", event: session.AgentMessage{Alias: "ada", TurnID: 8, TurnCompleted: true}, completes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handoff := handoffStage{
				action:                promptlang.Handoff{FromAlias: located("ada"), ToAlias: located("ben")},
				sourceNeedsCompletion: true,
			}
			if got := handoff.completeSourceTurn(tc.event, 7); got != tc.completes {
				t.Fatalf("source completion = %v, want %v", got, tc.completes)
			}
			if handoff.sourceNeedsCompletion == tc.completes {
				t.Fatalf("source still needs completion = %v", handoff.sourceNeedsCompletion)
			}
		})
	}
}
