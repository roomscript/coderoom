package std_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

type broadcastDelivery struct {
	sendDelivery
	broadcast session.BroadcastCommand
}

func (d *broadcastDelivery) Execute(command session.Command) error {
	d.broadcast = command.(session.BroadcastCommand)
	return d.err
}

func TestBroadcastCommand_preparation(t *testing.T) {
	ada, ben := newTestAlias("ada"), newTestAlias("ben")
	for _, input := range []struct {
		name                                     string
		statement                                promptlang.Statement
		raw                                      string
		views                                    []participant.View
		missingSession, missingReader, wantError bool
	}{
		{name: "startup and busy are retained", statement: promptlang.Broadcast{}, raw: "work", views: []participant.View{{Alias: ben, Status: participant.StatusStarting}, {Alias: ada, Status: participant.StatusWorking}}},
		{name: "crashed excluded", statement: promptlang.Broadcast{}, raw: "work", views: []participant.View{{Alias: ada, Status: participant.StatusIdle}, {Alias: ben, Status: participant.StatusCrashed}}},
		{name: "empty", statement: promptlang.Broadcast{}, raw: "work", wantError: true},
		{name: "all crashed", statement: promptlang.Broadcast{}, raw: "work", views: []participant.View{{Alias: ada, Status: participant.StatusCrashed}}, wantError: true},
		{name: "wrong statement", statement: promptlang.Who{}, raw: "/who", wantError: true},
		{name: "missing input", statement: promptlang.Broadcast{}, wantError: true},
		{name: "missing session", statement: promptlang.Broadcast{}, raw: "work", missingSession: true, wantError: true},
		{name: "missing reader", statement: promptlang.Broadcast{}, raw: "work", missingReader: true, wantError: true},
	} {
		t.Run(input.name, func(t *testing.T) {
			delivery := &broadcastDelivery{}
			ctx := runtime.Context{Session: delivery, Participants: participantReader{views: input.views}}
			if input.missingSession {
				ctx.Session = nil
			}
			if input.missingReader {
				ctx.Participants = nil
			}
			parsed := promptlang.ParsedStatement{Located: promptlang.Located[promptlang.Statement]{Value: input.statement}, Raw: input.raw}
			invocation, err := (std.BroadcastCommand{}).Prepare(parsed, ctx)
			if (err != nil) != input.wantError {
				t.Fatalf("Prepare error = %v", err)
			}
			if err != nil {
				return
			}
			defer invocation.Close()
			assertBroadcastPreparation(t, invocation, ada, ben, input.views)
		})
	}
}

func TestBroadcastCommand_deliveryAndClose(t *testing.T) {
	ada, ben := newTestAlias("ada"), newTestAlias("ben")
	delivery := &broadcastDelivery{}
	invocation, err := (std.BroadcastCommand{}).Prepare(parseSendStatement(t, "work"), runtime.Context{Session: delivery, Participants: participantReader{views: []participant.View{{Alias: ada}, {Alias: ben}}}})
	if err != nil {
		t.Fatal(err)
	}
	staging := invocation.Staging()
	staging.DiscardInform(ben)
	var results []runtime.CommandResult
	if err := invocation.Go(func(result runtime.CommandResult) { results = append(results, result) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(delivery.broadcast.Aliases, []participant.Alias{ada}) || !slices.Equal(staging.ToInform, []participant.Alias{ada, ben}) {
		t.Fatal("departure changed readiness description or failed to remove delivery recipient")
	}
	handle := delivery.complete
	outcome := session.RoutingResult{Kind: session.RoutingBroadcast, Recipients: []session.RecipientResult{{Alias: ada, Role: session.RecipientBroadcast, Status: session.DeliveryDelivered}}}
	handle(outcome, nil)
	handle(outcome, nil)
	invocation.Close()
	handle(outcome, nil)
	assertBroadcastResult(t, results)
	if err := invocation.Go(func(runtime.CommandResult) {}); !errors.Is(err, runtime.ErrInvocationNotPrepared) {
		t.Fatalf("closed Go = %v", err)
	}
	if len(delivery.handlers) != 0 {
		t.Fatal("delivery subscription retained")
	}
}

func assertBroadcastPreparation(t *testing.T, invocation runtime.Invocation, ada, ben participant.Alias, views []participant.View) {
	t.Helper()

	staging := invocation.Staging()
	if len(staging.WaitFor) != 0 || staging.ToInform[0] != ada {
		t.Fatalf("staging = %#v", staging)
	}
	if len(views) > 1 && views[0].Alias == ben && !slices.Equal(staging.ToInform, []participant.Alias{ada, ben}) {
		t.Fatalf("startup/busy selection = %#v", staging)
	}
}

func assertBroadcastResult(t *testing.T, results []runtime.CommandResult) {
	t.Helper()
	if len(results) != 1 || len(results[0].Records) != 1 || results[0].Records[0].Text != "work" || !slices.Equal(results[0].Records[0].Routing, []string{"ada"}) {
		t.Fatalf("results = %#v", results)
	}
}

func TestBroadcastCommand_InvalidTerminalOutcomeFails(t *testing.T) {
	alias := newTestAlias("ada")
	for _, test := range []struct {
		name   string
		result session.RoutingResult
	}{
		{"wrong kind", session.RoutingResult{Kind: session.RoutingParticipantSend}},
		{"missing recipients", session.RoutingResult{Kind: session.RoutingBroadcast}},
		{"replacement instance", session.RoutingResult{Kind: session.RoutingBroadcast, Recipients: []session.RecipientResult{{Alias: newTestAlias("ada"), Role: session.RecipientBroadcast}}}},
		{"wrong role", session.RoutingResult{Kind: session.RoutingBroadcast, Recipients: []session.RecipientResult{{Alias: alias, Role: session.RecipientNotice}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			delivery := &broadcastDelivery{}
			invocation, err := (std.BroadcastCommand{}).Prepare(parseSendStatement(t, "work"), runtime.Context{Session: delivery, Participants: participantReader{views: []participant.View{{Alias: alias}}}})
			if err != nil {
				t.Fatal(err)
			}
			defer invocation.Close()
			var results []runtime.CommandResult
			if err := invocation.Go(func(result runtime.CommandResult) { results = append(results, result) }); err != nil {
				t.Fatal(err)
			}
			delivery.complete(test.result, nil)
			if len(results) != 1 || !errors.Is(results[0].Err, runtime.ErrInvalidDeliveryOutcome) || len(results[0].Records) != 0 {
				t.Fatalf("invalid completion = %#v", results)
			}
			delivery.complete(session.RoutingResult{Kind: session.RoutingBroadcast}, nil)
			if len(results) != 1 {
				t.Fatal("invalid terminal result left broadcast waiting")
			}
		})
	}
}

func (d *broadcastDelivery) CreateExecution() runtime.SessionExecution { return d }
