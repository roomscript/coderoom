package std_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

type handoffHistory struct {
	source    session.HandoffSource
	completed participant.ExecutionID
	reads     int
}

func (s *handoffHistory) LatestOutput(participant.Alias) (session.HandoffSource, bool) {
	s.reads++
	return s.source, s.source.Text != ""
}

type handoffDelivery struct {
	sendDelivery
	handoff session.HandoffCommand
}

func (d *handoffDelivery) Execute(command session.Command) error {
	d.handoff = command.(session.HandoffCommand)
	d.launches++
	return d.err
}

func prepareHandoff(t *testing.T, d *handoffDelivery, source *handoffHistory) (runtime.Invocation, runtime.Context) {
	t.Helper()
	d.completed = map[participant.Alias]session.ExecutionID{d.views[0].Alias: 7}
	ctx := runtime.Context{Session: d, Participants: participantReader{views: d.views}, History: source}
	invocation, err := (std.HandoffCommand{}).Prepare(parseSendStatement(t, "/handoff ada tim"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	return invocation, ctx
}

func handoffViews() []participant.View {
	return []participant.View{
		{Alias: newTestAlias("ada"), Status: participant.StatusIdle, StartupReady: true},
		{Alias: newTestAlias("tim"), Status: participant.StatusIdle, StartupReady: true},
	}
}

func TestHandoffCommand_waitsForCanonicalSourceTurn(t *testing.T) {
	for _, status := range []participant.Status{participant.StatusIdle, participant.StatusWorking, participant.StatusPreparing} {
		t.Run(string(status), func(t *testing.T) {
			delivery := &handoffDelivery{sendDelivery: sendDelivery{views: handoffViews()}}
			delivery.views[0].Status = status
			source := &handoffHistory{completed: 6, source: session.HandoffSource{Text: "old output", RecordIndex: 2}}
			invocation, ctx := prepareHandoff(t, delivery, source)
			lastCompletedExecutionIDs := completionIDs{delivery.views[0].Alias: source.completed}
			ctx.CompletedExecutions = lastCompletedExecutionIDs
			lifecycle := runtime.NewInvocationLifecycle(invocation, ctx)
			defer lifecycle.Close(errors.New("cleanup"))
			advances := 0
			lifecycle.RequestAdvance = func() { advances++ }
			waiting, err := lifecycle.Start(func(runtime.CommandResult) {})
			if err != nil || !waiting || source.reads != 0 {
				t.Fatalf("Start = %v, %v; reads=%d", waiting, err, source.reads)
			}
			delivery.views[0].Status = participant.StatusIdle
			delivery.queue = append(delivery.queue, session.ParticipantStatusChanged{Alias: delivery.views[0].Alias, From: status, To: participant.StatusIdle})
			delivery.deliverEvents()
			lifecycle.Advance()
			if delivery.launches != 0 || source.reads != 0 {
				t.Fatal("idle launched before canonical projection")
			}
			// A reasoning flush or an older completed turn cannot satisfy this turn.
			lastCompletedExecutionIDs[delivery.views[0].Alias] = source.completed
			lifecycle.Advance()
			if delivery.launches != 0 {
				t.Fatal("unrelated projection launched handoff")
			}
			finishCompletedHandoff(t, lifecycle, delivery, source, advances, lastCompletedExecutionIDs)

		})
	}
}

func finishCompletedHandoff(t *testing.T, lifecycle *runtime.InvocationLifecycle, delivery *handoffDelivery, source *handoffHistory, advances int, lastCompletedExecutionIDs map[participant.Alias]participant.ExecutionID) {
	t.Helper()
	source.completed, source.source = 7, session.HandoffSource{Text: "fresh output", RecordIndex: 9}
	lastCompletedExecutionIDs[delivery.views[0].Alias] = source.completed
	if advances == 0 || delivery.launches != 0 {
		t.Fatal("projection advanced outside the owner lane")
	}
	if lifecycle.DiscardWaiting() {
		t.Fatal("discard defeated collected source readiness")
	}
	lifecycle.Advance()
	if delivery.launches != 1 || delivery.handoff.Source != source.source || source.reads != 1 {
		t.Fatalf("handoff=%#v, reads=%d", delivery.handoff, source.reads)
	}
	if len(delivery.handlers) != 0 {
		t.Fatal("running invocation retained readiness subscriptions")
	}
}

func TestHandoffCommand_frozenPrerequisites(t *testing.T) {
	delivery := &handoffDelivery{sendDelivery: sendDelivery{views: handoffViews()}}
	busy, starting, crashed := newTestAlias("busy"), newTestAlias("starting"), newTestAlias("crashed")
	delivery.views = append(delivery.views,
		participant.View{Alias: busy, Status: participant.StatusWorking, StartupReady: true},
		participant.View{Alias: starting, Status: participant.StatusStarting},
		participant.View{Alias: crashed, Status: participant.StatusCrashed, StartupReady: true})
	source := &handoffHistory{completed: 7, source: session.HandoffSource{Text: "prior output", RecordIndex: 4}}
	invocation, _ := prepareHandoff(t, delivery, source)
	defer invocation.Close()
	staging := invocation.Staging()
	required := []participant.Alias{delivery.views[0].Alias, delivery.views[1].Alias}
	if !slices.Equal(staging.WaitFor, required) || !slices.Equal(staging.ToInform, []participant.Alias{busy}) {
		t.Fatalf("staging=%#v", staging)
	}
	staging.DiscardInform(busy)
	delivery.views = append(delivery.views, participant.View{Alias: newTestAlias("late"), Status: participant.StatusWorking, StartupReady: true})
	if err := invocation.Go(func(runtime.CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(delivery.handoff.RequiredReadyAliases, required) || !slices.Equal(staging.ToInform, []participant.Alias{busy}) {
		t.Fatal("departure or join changed frozen prerequisites")
	}
}

func TestHandoffCommand_completion(t *testing.T) {
	failure := errors.New("notice failed")
	for _, variant := range []string{"success", "delivery failure", "protocol failure", "wrong recipient"} {
		t.Run(variant, func(t *testing.T) {
			delivery := &handoffDelivery{sendDelivery: sendDelivery{views: handoffViews()}}
			source := &handoffHistory{source: session.HandoffSource{Text: "output", RecordIndex: 4}}
			invocation, _ := prepareHandoff(t, delivery, source)
			defer invocation.Close()
			var results []runtime.CommandResult
			if err := invocation.Go(func(result runtime.CommandResult) { results = append(results, result) }); err != nil {
				t.Fatal(err)
			}
			to := delivery.views[1].Alias
			outcome := runtime.DeliveryCompleted{Result: session.RoutingResult{Kind: session.RoutingHandoff, Recipients: []session.RecipientResult{{Alias: to, Role: session.RecipientHandoff, Status: session.DeliveryDelivered}}}}
			outcome, expected := handoffTerminalVariant(variant, outcome, failure)

			handle := delivery.terminal
			if variant == "protocol failure" {
				handle(runtime.ExecutionFailed{Err: failure})
			} else {
				handle(outcome)
			}
			handle(outcome)
			if len(results) != 1 || !errors.Is(results[0].Err, expected) {
				t.Fatalf("completion=%#v", results)
			}
			assertHandoffCompletion(t, results[0], expected)

		})
	}
}

func assertHandoffCompletion(t *testing.T, result runtime.CommandResult, expected error) {
	t.Helper()
	if expected != nil {
		if len(result.Records) != 0 {
			t.Fatal("failed handoff committed input or completion")
		}
		return
	}
	if len(result.Records) != 1 || !slices.Equal(result.Records[0].Routing, []string{"tim"}) {
		t.Fatalf("completion=%#v", result)
	}
}

func handoffTerminalVariant(variant string, outcome runtime.DeliveryCompleted, failure error) (runtime.DeliveryCompleted, error) {
	expected := error(nil)
	switch variant {
	case "delivery failure":
		outcome.Result.Err = failure
		outcome.Result.Recipients[0].Status = session.DeliveryFailed
		expected = failure
	case "protocol failure":
		expected = failure
	case "wrong recipient":
		outcome.Result.Recipients[0].Alias = newTestAlias("tim")
		expected = runtime.ErrInvalidDeliveryOutcome
	}
	return outcome, expected
}

func TestHandoffCommand_missingSourceAndCleanup(t *testing.T) {
	for _, action := range []string{"no output", "discard", "close", "source departure", "destination departure"} {
		t.Run(action, func(t *testing.T) {
			delivery := &handoffDelivery{sendDelivery: sendDelivery{views: handoffViews()}}
			source := &handoffHistory{completed: 7}
			if action != "no output" {
				delivery.views[0].Status = participant.StatusWorking
			}
			invocation, ctx := prepareHandoff(t, delivery, source)
			lastCompletedExecutionIDs := completionIDs{delivery.views[0].Alias: source.completed}
			ctx.CompletedExecutions = lastCompletedExecutionIDs
			lifecycle := runtime.NewInvocationLifecycle(invocation, ctx)
			defer lifecycle.Close(errors.New("cleanup"))
			var results []runtime.CommandResult
			waiting, err := lifecycle.Start(func(result runtime.CommandResult) { results = append(results, result) })
			if action == "no output" {
				if err == nil || waiting {
					t.Fatalf("Start error=%v", err)
				}
			} else {
				if !waiting || err != nil {
					t.Fatalf("Start=%v, %v", waiting, err)
				}
				finishHandoffWait(t, lifecycle, delivery, action, &results)
			}

			if delivery.launches != 0 || len(delivery.handlers) != 0 {
				t.Fatal("terminal source wait retained subscriptions or launched")
			}
		})
	}
}

func finishHandoffWait(t *testing.T, lifecycle *runtime.InvocationLifecycle, delivery *handoffDelivery, action string, results *[]runtime.CommandResult) {
	t.Helper()
	switch action {
	case "discard":
		if !lifecycle.DiscardWaiting() {
			t.Fatal("source wait could not be discarded")
		}
	case "close":
		lifecycle.Close(errors.New("shutdown"))
	default:
		index := 0
		if action == "destination departure" {
			index = 1
		}
		alias := delivery.views[index].Alias
		delivery.queue = append(delivery.queue, session.AgentStopped{Alias: alias})
		delivery.deliverEvents()
		lifecycle.Advance()
		var unavailable *runtime.RequiredParticipantUnavailableError
		if len(*results) != 1 || !errors.As((*results)[0].Err, &unavailable) || unavailable.Alias != alias {
			t.Fatalf("results=%#v", results)
		}
	}
}

type completionIDs map[participant.Alias]participant.ExecutionID

func (ids completionIDs) LastCompletedExecutionID(alias participant.Alias) participant.ExecutionID {
	return ids[alias]
}
