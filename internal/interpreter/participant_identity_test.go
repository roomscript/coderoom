package interpreter

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

func TestStageWorkflow_replacementEventsCannotReleaseFrozenParticipants(t *testing.T) {
	for _, kind := range []string{"send", "broadcast", "handoff"} {
		t.Run(kind, func(t *testing.T) {
			old := testAlias("ada")
			replacement := participant.New(old.Name, "", participant.InitiativeManual).Alias
			stage := stageWorkflow{}
			states := []participantState{{alias: old, status: participant.StatusWorking}, {alias: testAlias("ben"), status: participant.StatusIdle}}
			switch kind {
			case "send":
				planStageForTest(t, &stage, promptlang.Send{Alias: located(old.Name), Text: located("work")}, []string{old.Name}, states)
			case "broadcast":
				planStageForTest(t, &stage, promptlang.Broadcast{Text: located("work")}, []string{old.Name}, states)
			case "handoff":
				stage.start("/handoff ada ben", promptlang.Handoff{FromAlias: located(old.Name), ToAlias: located("ben")})
				stage.prepareHandoff(handoffPlanResult{target: stage.active.pending, from: old, to: testAlias("ben"), participants: states})
			}
			before := stage.snapshot()
			for _, event := range []session.Event{
				session.AgentReady{Alias: replacement},
				session.ParticipantStatusChanged{Alias: replacement, To: participant.StatusIdle},
				session.AgentMessage{Alias: replacement, TurnCompleted: true},
				session.AgentStopped{Alias: replacement},
				session.AgentCrashed{Alias: replacement},
			} {
				stage.handleSessionEvent(event)
				if !reflect.DeepEqual(stage.snapshot(), before) {
					t.Fatalf("replacement event %T changed frozen stage", event)
				}
			}
			stage.handleSessionEvent(session.AgentStopped{Alias: old})
			if stage.active != nil {
				t.Fatal("original departure did not discard stage")
			}
		})
	}
}

func TestHandoffStage_completionKeepsFrozenInstances(t *testing.T) {
	from, to := testAlias("ada"), testAlias("ben")
	replacement := participant.New(from.Name, "", participant.InitiativeManual).Alias
	h := handoffStage{from: from, to: to, sourceNeedsCompletion: true}
	if h.completeSourceTurn(session.AgentMessage{Alias: replacement, TurnCompleted: true, TurnID: 9}, 8) {
		t.Fatal("replacement output completed original source")
	}
	h.captureCompletion(session.HandoffDelivered{FromAlias: replacement, ToAlias: to})
	if h.completed != nil {
		t.Fatal("replacement delivery completed original handoff")
	}
	if !h.completeSourceTurn(session.AgentMessage{Alias: from, TurnCompleted: true, TurnID: 8}, 8) {
		t.Fatal("original output did not complete source")
	}
	h.captureCompletion(session.HandoffDelivered{FromAlias: from, ToAlias: to})
	if h.completed == nil {
		t.Fatal("original delivery did not complete handoff")
	}
}

func TestLoopWorkflow_retainsPrimaryAcrossIterations(t *testing.T) {
	old := testAlias("ada")
	replacement := participant.New(old.Name, "", participant.InitiativeManual).Alias
	workflow := loopWorkflow{active: &loopState{generation: 1, statement: testLoopStatement(2), primary: old, phase: loopWaitingForParticipant, turns: 1}}
	for _, event := range []session.Event{
		session.ParticipantStatusChanged{Alias: replacement, To: participant.StatusIdle},
		session.AgentStopped{Alias: replacement}, session.AgentCrashed{Alias: replacement},
	} {
		if actions := workflow.handleSessionEvent(event); len(actions) != 0 {
			t.Fatalf("replacement %T advanced loop", event)
		}
	}
	actions := workflow.handleSessionEvent(session.ParticipantStatusChanged{Alias: old, To: participant.StatusIdle})
	condition := actions[0].(startShellInstruction)
	actions = workflow.resumeOnConditionResult(shellOutcome{target: condition.target, result: shell.Result{Status: shell.StatusFailure}})
	var request createPlanAndExecuteParticipantSendRequest
	for _, action := range actions {
		if dispatch, ok := action.(executeSessionInstruction); ok {
			request = dispatch.request.(createPlanAndExecuteParticipantSendRequest)
		}
	}
	if request.alias != old {
		t.Fatalf("next turn primary = %v, want original %v", request.alias, old)
	}
}

func TestLoopWorkflow_originalDepartureDuringConditionStopsLoop(t *testing.T) {
	for _, event := range []session.Event{session.AgentStopped{Alias: testAlias("ada")}, session.AgentCrashed{Alias: testAlias("ada")}} {
		workflow := loopWorkflow{active: &loopState{generation: 1, primary: testAlias("ada"), phase: loopEvaluating}}
		workflow.handleSessionEvent(event)
		if workflow.active != nil {
			t.Fatalf("%T left loop active during condition", event)
		}
	}
}

func TestInterpreterModel_handoffSourceAndSnapshotsKeepInstances(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	old := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	model.ApplySessionEvent(session.AgentReady{Alias: old})
	model.ApplySessionEvent(session.AgentMessage{Alias: old, TurnCompleted: true, Msg: agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "old output"}}})
	model.ApplySessionEvent(session.AgentStopped{Alias: old})
	model.ApplySessionEvent(session.AgentReady{Alias: replacement})
	model.ApplySessionEvent(session.AgentMessage{Alias: old, Msg: agent.Message{Mode: agent.ModeStream, StreamID: "late", Content: agent.Output{Text: "late output"}}})
	if _, ok := model.ReadHandoffSource(replacement); ok {
		t.Fatal("replacement inherited original output")
	}
	snapshot := cloneSnapshot(Snapshot{Room: model.Snapshot().room})
	if len(snapshot.Room.Members) != 1 || snapshot.Room.Members[0] != replacement || !snapshot.Room.Departed[old] || snapshot.Room.Departed[replacement] {
		t.Fatalf("snapshot lost instance ownership: %#v", snapshot.Room)
	}
	if len(snapshot.Room.OpenStreams) != 0 {
		t.Fatal("delayed original output reopened stream")
	}
	snapshot.Room.Departed[replacement] = true
	snapshot.Room.Members[0] = old
	original := model.Snapshot().room
	if original.Departed[replacement] || original.Members[0] != replacement {
		t.Fatal("snapshot mutation changed model")
	}
}

type identityReadyObserver struct{ ready chan participant.Alias }

func (o identityReadyObserver) OnEvent(event session.Event) {
	if ready, ok := event.(session.AgentReady); ok {
		o.ready <- ready.Alias
	}
}

func inviteIdentityParticipant(t *testing.T, sess *session.Session, ready <-chan participant.Alias, name string) participant.Alias {
	t.Helper()
	if err := sess.Execute(session.InviteCommand{Alias: name}); err != nil {
		t.Fatal(err)
	}
	select {
	case alias := <-ready:
		if alias.Name != name {
			t.Fatalf("ready alias = %v, want %s", alias, name)
		}
		return alias
	case <-time.After(time.Second):
		t.Fatal("participant did not become ready")
		return participant.Alias{}
	}
}

func TestInterpreterExecutor_frozenRequestsRejectSameNameReplacement(t *testing.T) {
	ready := make(chan participant.Alias, 8)
	sess := session.New(session.WithObserver(identityReadyObserver{ready}), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return echo.New()
	}))
	t.Cleanup(sess.Shutdown)
	old := inviteIdentityParticipant(t, sess, ready, "ada")
	to := inviteIdentityParticipant(t, sess, ready, "ben")
	executor := interpreterExecutor{session: sess}
	plan := executor.createParticipantSendPlan("ada")
	broadcast := executor.planBroadcast()
	if err := sess.Execute(session.RemoveCommand{Alias: old}); err != nil {
		t.Fatal(err)
	}
	replacement := inviteIdentityParticipant(t, sess, ready, "ada")
	if executor.participantByName("ada") != replacement {
		t.Fatal("name lookup did not resolve replacement")
	}
	requests := []sessionRequest{
		executePlannedParticipantSendRequest{plan: plan, message: "work"},
		createPlanAndExecuteParticipantSendRequest{alias: old, message: "next turn"},
		broadcastRequest{aliases: broadcast, text: "broadcast"},
		cancelRequest{alias: old},
		handoffRequest{fromAlias: old, toAlias: to, source: session.HandoffSource{Text: "context"}},
	}
	for _, request := range requests {
		if err := executor.executeSessionRequest(request); err == nil {
			t.Fatalf("%T accepted departed instance", request)
		}
	}
	view, ok := sess.Participant(replacement)
	if !ok || view.Status != participant.StatusIdle {
		t.Fatalf("replacement received stale work: %#v", view)
	}
}

type identityPlanningSession struct {
	*session.Session
	plans []session.ParticipantSendPlan
}

func (s *identityPlanningSession) Execute(command session.Command) error {
	s.plans = append(s.plans, command.(session.SendToParticipantCommand).Plan)
	return nil
}

func TestInterpreterExecutor_loopTurnReplansNoticesForOriginalPrimary(t *testing.T) {
	ready := make(chan participant.Alias, 8)
	sess := session.New(session.WithObserver(identityReadyObserver{ready}), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return echo.New()
	}))
	t.Cleanup(sess.Shutdown)
	primary := inviteIdentityParticipant(t, sess, ready, "ada")
	originalNotice := inviteIdentityParticipant(t, sess, ready, "ben")
	if err := sess.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	recording := &identityPlanningSession{Session: sess}
	executor := interpreterExecutor{session: recording}
	request := createPlanAndExecuteParticipantSendRequest{alias: primary, message: "work"}
	if err := executor.executeSessionRequest(request); err != nil {
		t.Fatal(err)
	}
	if err := sess.Execute(session.RemoveCommand{Alias: originalNotice}); err != nil {
		t.Fatal(err)
	}
	replacementNotice := inviteIdentityParticipant(t, sess, ready, "ben")
	if err := executor.executeSessionRequest(request); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(recording.plans[0].Targets(), []participant.Alias{primary, originalNotice}) || !slices.Equal(recording.plans[1].Targets(), []participant.Alias{primary, replacementNotice}) {
		t.Fatal("turns did not preserve primary and reselect notice instances")
	}
}
