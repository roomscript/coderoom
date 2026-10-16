package monolith

import (
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func TestHandoffStage_completeSourceWork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		event     session.AgentMessage
		completes bool
	}{
		{name: "other participant", event: session.AgentMessage{Alias: testAlias("ben"), ExecutionID: 7, WorkCompleted: true}},
		{name: "unfinished output", event: session.AgentMessage{Alias: testAlias("ada"), ExecutionID: 7}},
		{name: "older turn", event: session.AgentMessage{Alias: testAlias("ada"), ExecutionID: 6, WorkCompleted: true}},
		{name: "current turn", event: session.AgentMessage{Alias: testAlias("ada"), ExecutionID: 7, WorkCompleted: true}, completes: true},
		{name: "newer turn", event: session.AgentMessage{Alias: testAlias("ada"), ExecutionID: 8, WorkCompleted: true}, completes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handoff := handoffStage{
				from: testAlias("ada"), to: testAlias("ben"),
				action:                promptlang.Handoff{FromAlias: located("ada"), ToAlias: located("ben")},
				sourceNeedsCompletion: true,
			}
			if got := handoff.completeSourceWork(tc.event, 7); got != tc.completes {
				t.Fatalf("source completion = %v, want %v", got, tc.completes)
			}
			if handoff.sourceNeedsCompletion == tc.completes {
				t.Fatalf("source still needs completion = %v", handoff.sourceNeedsCompletion)
			}
		})
	}
}
