package interpreter

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/session"
)

type workflowGatewaySession struct {
	planner    *session.Session
	executed   []session.Command
	executeErr error
	barrier    []participant.Participant
	routable   []participant.Participant
}

func (s *workflowGatewaySession) Execute(command session.Command) error {
	s.executed = append(s.executed, command)
	return s.executeErr
}

func (*workflowGatewaySession) AddObserver(session.Observer) {}

func (s *workflowGatewaySession) PlanSharedSend(alias string) session.SharedSendPlan {
	return s.planner.PlanSharedSend(alias)
}

func (*workflowGatewaySession) Roster() []participant.View { return nil }

func (*workflowGatewaySession) Participant(string) (participant.Participant, bool) {
	return participant.Participant{}, false
}

func (s *workflowGatewaySession) RoutableParticipants() []participant.Participant {
	return append([]participant.Participant(nil), s.routable...)
}

func (s *workflowGatewaySession) BarrierParticipants() []participant.Participant {
	return append([]participant.Participant(nil), s.barrier...)
}

func (*workflowGatewaySession) Shutdown() {}

func TestSessionGateway_mapsRequests(t *testing.T) {
	planner := session.New()
	t.Cleanup(planner.Shutdown)
	plan := planner.PlanSharedSend("ada")
	source := session.HandoffSource{Text: "context", RecordIndex: 7}
	tests := []struct {
		name    string
		request sessionRequest
		want    session.Command
	}{
		{
			name: "plan and execute shared send",
			request: planAndExecuteSharedSendRequest{
				alias: "ada", directText: "fix it", listenersText: "@ada: fix it",
			},
			want: session.SharedSendCommand{
				Plan: plan, TextDirect: "fix it", TextListeners: "@ada: fix it",
			},
		},
		{
			name: "execute frozen shared send",
			request: executePlannedSharedSendRequest{
				plan: plan, directText: "fix it", listenersText: "notice",
			},
			want: session.SharedSendCommand{Plan: plan, TextDirect: "fix it", TextListeners: "notice"},
		},
		{name: "broadcast", request: broadcastRequest{text: "hello"}, want: session.BroadcastCommand{Text: "hello"}},
		{
			name: "handoff",
			request: handoffRequest{
				fromAlias: "ada", toAlias: "turing", idleAliases: []string{"ada", "turing"}, source: source,
			},
			want: session.HandoffCommand{
				FromAlias: "ada", ToAlias: "turing", IdleAliases: []string{"ada", "turing"}, Source: source,
			},
		},
		{name: "cancel", request: cancelRequest{alias: "ada"}, want: session.CancelCommand{Alias: "ada"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateway := &workflowGatewaySession{planner: planner}
			interp := &Interpreter{session: gateway}
			if err := interp.executeSessionRequest(tt.request); err != nil {
				t.Fatalf("executeSessionRequest: %v", err)
			}
			if len(gateway.executed) != 1 || !reflect.DeepEqual(gateway.executed[0], tt.want) {
				t.Fatalf("executed = %#v, want %#v", gateway.executed, tt.want)
			}
		})
	}
}

func TestSessionGateway_preservesDeliveryError(t *testing.T) {
	want := &session.DeliveryError{Delivered: []string{"ada"}, Err: errors.New("turing failed")}
	gateway := &workflowGatewaySession{planner: session.New(), executeErr: want}
	t.Cleanup(gateway.planner.Shutdown)
	interp := &Interpreter{session: gateway}

	got := interp.executeSessionRequest(broadcastRequest{text: "hello"})
	if got != want {
		t.Fatalf("error identity changed: got %v, want %v", got, want)
	}
	if aliases := session.DeliveredAliases(got); !slices.Equal(aliases, []string{"ada"}) {
		t.Fatalf("delivered aliases = %v, want [ada]", aliases)
	}
}

func TestSessionGateway_planningCompletionPreservesTargetAndTargets(t *testing.T) {
	planner := session.New()
	t.Cleanup(planner.Shutdown)
	target := workflowRef{kind: workflowStage, generation: 4, requestID: 30}
	interp := &Interpreter{session: &workflowGatewaySession{planner: planner}}

	items, _ := interp.applyEffect(planSharedSendEffect{target: target, alias: "ada"})
	if len(items) != 1 {
		t.Fatalf("completion items = %d, want 1", len(items))
	}
	completion := items[0].(completionItem).completion.(sharedSendPlanCompletion)
	if completion.target != target {
		t.Fatalf("target = %#v, want %#v", completion.target, target)
	}
	if !slices.Equal(completion.result.targets, []string{"ada"}) {
		t.Fatalf("targets = %v, want [ada]", completion.result.targets)
	}
	if !reflect.DeepEqual(completion.result.plan, planner.PlanSharedSend("ada")) {
		t.Fatal("completion did not preserve the frozen plan")
	}
}

func TestSessionGateway_participantInspectionIsDetachedAndCorrelated(t *testing.T) {
	ada := participant.New("ada", "builder", participant.InitiativeManual)
	ada.Status = participant.StatusPreparing
	if err := ada.BeginWorking(time.Now(), agent.StreamID("turn"), 42); err != nil {
		t.Fatalf("BeginWorking: %v", err)
	}
	turing := participant.New("turing", "reviewer", participant.InitiativeManual)
	turing.Status = participant.StatusIdle
	target := workflowRef{kind: workflowStage, generation: 4, requestID: 29}
	gateway := &workflowGatewaySession{
		planner: session.New(), barrier: []participant.Participant{ada.Snapshot()},
		routable: []participant.Participant{turing.Snapshot()},
	}
	t.Cleanup(gateway.planner.Shutdown)
	interp := &Interpreter{session: gateway}

	items, _ := interp.applyEffect(readParticipantStateEffect{target: target})
	if len(items) != 1 {
		t.Fatalf("completion items = %d, want 1", len(items))
	}
	completion := items[0].(completionItem).completion.(participantStateCompletion)
	if completion.target != target {
		t.Fatalf("target = %#v, want %#v", completion.target, target)
	}
	if got := completion.result.barrier; !reflect.DeepEqual(got, []participantState{{
		alias: "ada", status: participant.StatusWorking, turnID: 42,
	}}) {
		t.Fatalf("barrier state = %#v", got)
	}
	if got := completion.result.routable; !reflect.DeepEqual(got, []participantState{{
		alias: "turing", status: participant.StatusIdle,
	}}) {
		t.Fatalf("routable state = %#v", got)
	}
	ada.Status = participant.StatusCrashed
	if completion.result.barrier[0].status != participant.StatusWorking {
		t.Fatal("participant completion retained live participant state")
	}
}
