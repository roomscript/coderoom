package std_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// Each fake represents one request-scoped host handle. Events are queued and
// delivered only when the host drains them, including events emitted by Execute.
type sendDelivery struct {
	alias         string
	notices       bool
	planner       *session.Session
	plans         []session.ParticipantSendPlan
	command       session.SendToParticipantCommand
	message       string
	notice        string
	err           error
	launches      int
	handler       func(session.Event)
	queue         []session.Event
	duringExecute *sendOutcome
	order         []string
}

type sendOutcome struct {
	Delivered, Failed, Unsent []string
	Err                       error
}

func (d *sendDelivery) CreateParticipantSendPlan(alias string) session.ParticipantSendPlan {
	plan := d.planner.CreateParticipantSendPlan(alias)
	d.plans = append(d.plans, plan)
	return plan
}

func initializeSendPlanner(t *testing.T, d *sendDelivery) {
	t.Helper()
	if d.planner != nil {
		return
	}
	d.planner = session.New(session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return echo.New()
	}))
	t.Cleanup(d.planner.Shutdown)
	for _, alias := range []string{"ada", "tim"} {
		if err := d.planner.Execute(session.InviteCommand{Alias: alias}); err != nil {
			t.Fatal(err)
		}
	}
	if d.notices {
		if err := d.planner.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
			t.Fatal(err)
		}
	}
}

func (d *sendDelivery) Subscribe(handler func(session.Event)) func() {
	d.order = append(d.order, "subscribe")
	d.handler = handler
	return func() { d.handler = nil }
}

func (d *sendDelivery) Execute(command session.Command) error {
	request := command.(session.SendToParticipantCommand)
	d.command = request
	d.order = append(d.order, "execute")
	d.launches++
	d.alias = request.Plan.PrimaryParticipant()
	d.message, d.notice = request.Message, request.Notice
	if d.duringExecute != nil {
		d.emit(*d.duringExecute)
	}
	return d.err
}

func (d *sendDelivery) emit(outcome sendOutcome) {
	result := session.RoutingResult{Kind: session.RoutingParticipantSend, Err: outcome.Err}
	for _, group := range []struct {
		aliases []string
		status  session.DeliveryStatus
	}{
		{outcome.Delivered, session.DeliveryDelivered}, {outcome.Failed, session.DeliveryFailed}, {outcome.Unsent, session.DeliveryNotAttempted},
	} {
		for _, alias := range group.aliases {
			role := session.RecipientNotice
			if alias == d.alias {
				role = session.RecipientPrimary
			}
			result.Recipients = append(result.Recipients, session.RecipientResult{Alias: alias, Role: role, Status: group.status})
		}
	}
	d.queue = append(d.queue, session.RoutingCompleted{Result: result})
}

func (d *sendDelivery) drain() {
	events := d.queue
	d.queue = nil
	for _, event := range events {
		if d.handler != nil {
			d.handler(event)
		}
	}
}

func sendParticipants() []participant.View {
	return []participant.View{
		{Alias: "ada", Status: participant.StatusIdle, StartupReady: true},
		{Alias: "tim", Status: participant.StatusIdle, StartupReady: true},
	}
}

func parseSendStatement(t *testing.T, raw string) promptlang.ParsedStatement {
	t.Helper()
	parsed, err := promptlang.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func prepareSendInvocation(t *testing.T, delivery *sendDelivery, raw string) runtime.Invocation {
	t.Helper()
	initializeSendPlanner(t, delivery)
	invocation, err := (std.SendCommand{}).Prepare(parseSendStatement(t, raw), runtime.Context{Session: delivery, Participants: participantReader{views: sendParticipants()}})
	if err != nil {
		t.Fatal(err)
	}
	return invocation
}

func TestSendCommand_PrepareRetainsSessionPlan(t *testing.T) {
	delivery := &sendDelivery{}
	initializeSendPlanner(t, delivery)
	ctx := runtime.Context{Session: delivery, Participants: participantReader{views: sendParticipants()}}
	first, err := (std.SendCommand{}).Prepare(parseSendStatement(t, "@ada first"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.launches != 0 || len(delivery.plans) != 1 {
		t.Fatal("preparation did not select exactly once without delivery")
	}
	if err := delivery.planner.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	second, err := (std.SendCommand{}).Prepare(parseSendStatement(t, "@ada second"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Staging().ToInform) != 0 || !slices.Equal(second.Staging().ToInform, []string{"tim"}) {
		t.Fatal("session policy change altered retained plan or was ignored by next preparation")
	}
	assertSendUsesRetainedPlan(t, first, delivery)
}

func assertSendUsesRetainedPlan(t *testing.T, invocation runtime.Invocation, delivery *sendDelivery) {
	t.Helper()
	// Staging is a detached readiness description, not a second delivery plan.
	invocation.Staging().WaitFor[0] = "changed"
	if err := invocation.Go(func(runtime.CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	defer invocation.Close()
	if !reflect.DeepEqual(delivery.command.Plan, delivery.plans[0]) || delivery.alias != "ada" || delivery.message != "first" {
		t.Fatal("execution did not retain the original session plan and payload")
	}
	if len(delivery.plans) != 2 {
		t.Fatal("execution selected recipients again")
	}
}

func TestSendCommand_StagingDescribesSessionPlan(t *testing.T) {
	for _, notices := range []bool{false, true} {
		t.Run(map[bool]string{false: "notices disabled", true: "notices enabled"}[notices], func(t *testing.T) {
			delivery := &sendDelivery{notices: notices}
			invocation := prepareSendInvocation(t, delivery, "@ada work")
			var noticesWant []string
			if notices {
				noticesWant = []string{"tim"}
			}
			if !slices.Equal(invocation.Staging().WaitFor, []string{delivery.plans[0].PrimaryParticipant()}) || !slices.Equal(invocation.Staging().ToInform, noticesWant) {
				t.Fatal("staging differs from session-selected recipients")
			}
		})
	}
}

func TestSendCommand_DeliveryResults(t *testing.T) {
	failure := errors.New("delivery failed")
	for _, tt := range []struct {
		name        string
		result      sendOutcome
		wantRecords int
	}{
		{name: "success", result: sendOutcome{Delivered: []string{"ada", "tim"}}, wantRecords: 1},
		{name: "notice failure after primary delivery", result: sendOutcome{Delivered: []string{"ada"}, Failed: []string{"tim"}, Err: failure}, wantRecords: 1},
		{name: "primary failure skips notices", result: sendOutcome{Failed: []string{"ada"}, Unsent: []string{"tim"}, Err: failure}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := "  @ada   hello /help\nλ  "
			delivery := &sendDelivery{notices: true}
			invocation := prepareSendInvocation(t, delivery, raw)
			results := make(chan runtime.CommandResult, 1)
			// Direct invocation execution tests delivery only; lifecycle staging is unwired.
			if err := invocation.Go(func(result runtime.CommandResult) { results <- result }); err != nil {
				t.Fatal(err)
			}
			plan := delivery
			if plan.message != "hello /help\nλ" || plan.notice != "@ada: hello /help\nλ" || len(results) != 0 {
				t.Fatal("send changed the payload or completed before delivery")
			}
			plan.emit(tt.result)
			plan.drain()
			completion := <-results
			assertSendResult(t, completion, tt.result, raw, tt.wantRecords)
		})
	}
}

func assertSendResult(t *testing.T, completion runtime.CommandResult, delivery sendOutcome, raw string, wantRecords int) {
	t.Helper()
	if !errors.Is(completion.Err, delivery.Err) || len(completion.Records) != wantRecords {
		t.Fatalf("command result = %#v", completion)
	}
	if wantRecords == 0 {
		return
	}
	want := room.Record{
		Kind: room.KindUserInput, Text: raw,
		Routing: slices.Clone(delivery.Delivered), FailedRouting: slices.Clone(delivery.Failed), UnsentRouting: slices.Clone(delivery.Unsent),
	}
	if !reflect.DeepEqual(completion.Records[0], want) {
		t.Fatalf("record = %#v, want %#v", completion.Records[0], want)
	}
}

func TestSendCommand_PrepareFailures(t *testing.T) {
	withoutSource := parseSendStatement(t, "@ada work")
	withoutSource.Raw = ""
	for _, tt := range []struct {
		name      string
		statement promptlang.ParsedStatement
		ctx       runtime.Context
	}{
		{name: "wrong statement", statement: parseSendStatement(t, "/who")},
		{name: "missing delivery", statement: parseSendStatement(t, "@ada work")},
		{name: "missing source", statement: withoutSource, ctx: runtime.Context{Session: &sendDelivery{}}},
		{name: "missing participant reads", statement: parseSendStatement(t, "@ada work"), ctx: runtime.Context{Session: &sendDelivery{}}},
		{name: "missing primary", statement: parseSendStatement(t, "@ada work"), ctx: runtime.Context{Session: &sendDelivery{}, Participants: participantReader{}}},
		{name: "crashed primary", statement: parseSendStatement(t, "@ada work"), ctx: runtime.Context{Session: &sendDelivery{}, Participants: participantReader{views: []participant.View{{Alias: "ada", Status: participant.StatusCrashed}}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			invocation, err := (std.SendCommand{}).Prepare(tt.statement, tt.ctx)
			if err == nil || invocation != nil {
				t.Fatal("expected preparation failure")
			}
		})
	}
}

func TestSendCommand_LaunchFailure(t *testing.T) {
	delivery := &sendDelivery{}
	invocation := prepareSendInvocation(t, delivery, "@ada work")
	failure := errors.New("launch failed")
	delivery.err = failure
	if err := invocation.Go(func(runtime.CommandResult) { t.Fatal("unexpected completion") }); !errors.Is(err, failure) {
		t.Fatalf("launch error = %v", err)
	}
}

func TestSendCommand_LifecycleRejectsUnimplementedStaging(t *testing.T) {
	delivery := &sendDelivery{}
	lifecycle := runtime.NewInvocationLifecycle(prepareSendInvocation(t, delivery, "@ada work"))
	if err := lifecycle.Start(func(runtime.CommandResult) {}); !errors.Is(err, runtime.ErrStagingUnsupported) {
		t.Fatalf("staging error = %v", err)
	}
	if delivery.launches != 0 {
		t.Fatal("lifecycle bypassed staging")
	}
}

func TestSendCommand_ContinuationIsRegisteredBeforeExecute(t *testing.T) {
	delivery := &sendDelivery{duringExecute: &sendOutcome{Delivered: []string{"ada"}}}
	invocation := prepareSendInvocation(t, delivery, "@ada work")
	completions := 0
	if err := invocation.Go(func(runtime.CommandResult) { completions++ }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(delivery.order, []string{"subscribe", "execute"}) || completions != 0 {
		t.Fatal("continuation was not registered before admission, or ran inline")
	}
	delivery.drain()
	if completions != 1 || delivery.handler != nil {
		t.Fatal("completion did not release subscription")
	}
	delivery.emit(sendOutcome{Delivered: []string{"ada"}})
	delivery.drain()
	if completions != 1 {
		t.Fatal("duplicate event completed invocation twice")
	}
}

func TestSendCommand_ContinuationIsRequestScoped(t *testing.T) {
	first, second := &sendDelivery{}, &sendDelivery{}
	a := prepareSendInvocation(t, first, "@ada first")
	b := prepareSendInvocation(t, second, "@ada second")
	counts := [2]int{}
	if err := a.Go(func(runtime.CommandResult) { counts[0]++ }); err != nil {
		t.Fatal(err)
	}
	if err := b.Go(func(runtime.CommandResult) { counts[1]++ }); err != nil {
		t.Fatal(err)
	}
	// A nonterminal fact for the first request cannot complete either invocation.
	first.queue = append(first.queue, session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingBroadcast}})
	first.drain()
	if counts != [2]int{} {
		t.Fatal("unrelated event completed invocation")
	}
	second.emit(sendOutcome{Delivered: []string{"ada"}})
	second.drain()
	if counts != [2]int{0, 1} {
		t.Fatal("same-alias result reached wrong invocation")
	}
	a.Close()
}

func TestSendCommand_IgnoresOtherPrimaryRouting(t *testing.T) {
	for _, tt := range []struct {
		name       string
		recipients []session.RecipientResult
	}{
		{
			name: "different primary",
			recipients: []session.RecipientResult{
				{Alias: "tim", Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
			},
		},
		{
			name: "our alias is only a notice recipient",
			recipients: []session.RecipientResult{
				{Alias: "tim", Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
				{Alias: "ada", Role: session.RecipientNotice, Status: session.DeliveryDelivered},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			delivery := &sendDelivery{}
			invocation := prepareSendInvocation(t, delivery, "@ada work")
			defer invocation.Close()
			var completions []runtime.CommandResult
			if err := invocation.Go(func(result runtime.CommandResult) {
				completions = append(completions, result)
			}); err != nil {
				t.Fatal(err)
			}
			delivery.queue = append(delivery.queue, session.RoutingCompleted{Result: session.RoutingResult{
				Kind: session.RoutingParticipantSend, Recipients: tt.recipients,
			}})
			delivery.drain()
			if len(completions) != 0 || delivery.handler == nil {
				t.Fatal("another primary's routing result completed or closed our invocation")
			}
			delivery.queue = append(delivery.queue, session.RoutingCompleted{Result: session.RoutingResult{
				Kind: session.RoutingParticipantSend,
				Recipients: []session.RecipientResult{
					{Alias: "ada", Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
				},
			}})
			delivery.drain()
			if len(completions) != 1 || delivery.handler != nil {
				t.Fatal("our primary's routing result did not complete and release the invocation")
			}
			assertSendResult(t, completions[0], sendOutcome{Delivered: []string{"ada"}}, "@ada work", 1)
		})
	}
}

func TestSendCommand_CloseSuppressesQueuedEvents(t *testing.T) {
	for _, admissionFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "discard", true: "failed admission"}[admissionFails], func(t *testing.T) {
			delivery := &sendDelivery{duringExecute: &sendOutcome{Delivered: []string{"ada"}}}
			if admissionFails {
				delivery.err = errors.New("admission failed")
			}
			invocation := prepareSendInvocation(t, delivery, "@ada work")
			err := invocation.Go(func(runtime.CommandResult) { t.Fatal("closed invocation resumed") })
			if (err != nil) != admissionFails {
				t.Fatalf("admission error: %v", err)
			}
			invocation.Close()
			invocation.Close()
			delivery.drain()
			if delivery.handler != nil {
				t.Fatal("subscription retained after close")
			}
		})
	}
}

func TestSendCommand_StagingDiscardInformUpdatesPlan(t *testing.T) {
	for _, tt := range []struct {
		name     string
		departed []string
		targets  []string
	}{
		{name: "no departures", targets: []string{"ada", "tim"}},
		{name: "notice departed", departed: []string{"tim"}, targets: []string{"ada"}},
		{name: "repeated departure", departed: []string{"tim", "tim"}, targets: []string{"ada"}},
		{name: "unrelated alias", departed: []string{"ben"}, targets: []string{"ada", "tim"}},
		{name: "cannot discard primary", departed: []string{"ada"}, targets: []string{"ada", "tim"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			delivery := &sendDelivery{notices: true}
			invocation := prepareSendInvocation(t, delivery, "@ada work")
			defer invocation.Close()
			staging := invocation.Staging()
			for _, alias := range tt.departed {
				staging.DiscardInform(alias)
			}
			if delivery.launches != 0 {
				t.Fatal("discard delivered work")
			}
			if err := invocation.Go(func(runtime.CommandResult) { t.Fatal("unexpected inline completion") }); err != nil {
				t.Fatal(err)
			}
			assertDiscardedNoticePlan(t, delivery, tt.targets)
			if !slices.Equal(staging.ToInform, []string{"tim"}) {
				t.Fatal("callback mutated frozen readiness description")
			}
		})
	}
}

func assertDiscardedNoticePlan(t *testing.T, delivery *sendDelivery, targets []string) {
	t.Helper()
	if !slices.Equal(delivery.command.Plan.Targets(), targets) {
		t.Fatalf("delivery targets = %v, want %v", delivery.command.Plan.Targets(), targets)
	}
	if len(delivery.plans) != 1 || !slices.Equal(delivery.plans[0].Targets(), []string{"ada", "tim"}) {
		t.Fatal("discard changed the original plan or caused reselection")
	}
}

func TestSendCommand_DiscardInformAfterLaunchOrCloseIsIgnored(t *testing.T) {
	for _, launch := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "launched"}[launch], func(t *testing.T) {
			delivery := &sendDelivery{notices: true}
			invocation := prepareSendInvocation(t, delivery, "@ada work")
			defer invocation.Close()
			if launch {
				if err := invocation.Go(func(runtime.CommandResult) {}); err != nil {
					t.Fatal(err)
				}
			} else {
				invocation.Close()
			}
			invocation.Staging().DiscardInform("tim")
			if launch {
				assertDiscardedNoticePlan(t, delivery, []string{"ada", "tim"})
			}
			if !launch && (delivery.handler != nil || delivery.launches != 0) {
				t.Fatal("closed invocation started work")
			}
		})
	}
}
