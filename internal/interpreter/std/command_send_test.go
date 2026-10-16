package std_test

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

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

// Participant facts and invocation-scoped terminal events are queued separately.
type sendDelivery struct {
	terminal           func(runtime.ExecutionEvent)
	deliveries         []func()
	notices            bool
	planner            *session.Session
	plans              []session.ParticipantSendPlan
	command            session.SendToParticipantCommand
	message            string
	notice             string
	err                error
	launches           int
	handlers           map[uint64]func(session.Event)
	mu                 sync.Mutex
	started            chan struct{}
	deliveredEvents    int
	duringParticipants []session.Event
	nextSubscription   uint64
	views              []participant.View
	completed          map[participant.Alias]session.ExecutionID
	queue              []session.Event
	duringExecute      *sendOutcome
	order              []string
}

type sendOutcome struct {
	Delivered, Failed, Unsent []string
	Err                       error
}

func (d *sendDelivery) CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan {
	plan := d.planner.CreateParticipantSendPlan(alias)
	d.plans = append(d.plans, plan)
	return plan
}

func initializeSendPlanner(t *testing.T, d *sendDelivery) {
	t.Helper()
	if d.started == nil {
		d.started = make(chan struct{}, 8)
	}
	if d.planner != nil {
		return
	}
	d.planner = session.New(session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return echo.New()
	}))
	t.Cleanup(d.planner.Shutdown)
	for _, alias := range []string{"ada", "tim"} {
		if _, err := d.planner.Execute(session.InviteCommand{Alias: alias}); err != nil {
			t.Fatal(err)
		}
	}
	if d.views == nil {
		for _, name := range []string{"ada", "tim"} {
			view, _ := d.planner.ParticipantByName(name)
			view.Status, view.StartupReady = participant.StatusIdle, true
			d.views = append(d.views, view)
		}
	}
	if d.notices {
		if _, err := d.planner.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
			t.Fatal(err)
		}
	}
}

func (d *sendDelivery) subscribeParticipants(handler func(session.Event)) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.order = append(d.order, "subscribe")
	if d.handlers == nil {
		d.handlers = make(map[uint64]func(session.Event))
	}
	d.nextSubscription++
	id := d.nextSubscription
	d.handlers[id] = handler
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.handlers, id)
	}
}

func (d *sendDelivery) Participants() []participant.View {
	d.mu.Lock()
	d.order = append(d.order, "participants")
	snapshot := slices.Clone(d.views)
	d.queue = append(d.queue, d.duringParticipants...)
	d.duringParticipants = nil
	d.mu.Unlock()
	d.deliverEvents()
	return snapshot
}

func (d *sendDelivery) SubscribeParticipants(handle func(session.Event)) (session.ParticipantSnapshot, func()) {
	unsubscribe := d.subscribeParticipants(handle)
	views := d.Participants()
	for index := range views {
		views[index].LastCompletedExecutionID = d.completed[views[index].Alias]
	}
	return session.ParticipantSnapshot{Participants: views}, unsubscribe
}

func (d *sendDelivery) Execute(native session.Command) error {
	command := native.(session.SendToParticipantCommand)
	d.mu.Lock()
	d.command = command
	d.order = append(d.order, "execute")
	d.launches++
	d.message, d.notice = command.Message, command.Notice
	during, err := d.duringExecute, d.err
	d.mu.Unlock()
	if during != nil {
		d.emit(*during)
	}
	d.started <- struct{}{}
	return err
}

func (d *sendDelivery) emit(outcome sendOutcome) {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := session.RoutingResult{Kind: session.RoutingParticipantSend, Err: outcome.Err}
	for _, group := range []struct {
		aliases []string
		status  session.DeliveryStatus
	}{
		{outcome.Delivered, session.DeliveryDelivered}, {outcome.Failed, session.DeliveryFailed}, {outcome.Unsent, session.DeliveryNotAttempted},
	} {
		for _, alias := range group.aliases {
			role := session.RecipientNotice
			if alias == d.command.Plan.PrimaryParticipant().Name {
				role = session.RecipientPrimary
			}
			result.Recipients = append(result.Recipients, session.RecipientResult{Alias: d.frozenAlias(alias), Role: role, Status: group.status})
		}
	}
	handle := d.terminal
	if handle != nil {
		d.deliveries = append(d.deliveries, func() { handle(runtime.DeliveryCompleted{Result: result}) })
	}
}

func (d *sendDelivery) deliverEvents() {
	defer func() {
		d.mu.Lock()
		deliveries := d.deliveries
		d.deliveries = nil
		d.mu.Unlock()
		for _, deliver := range deliveries {
			deliver()
		}
	}()
	for {
		event, handlers := d.takeEvent()
		if event == nil {
			return
		}
		for _, handle := range handlers {
			handle(event)
		}
	}
}

func (d *sendDelivery) takeEvent() (session.Event, []func(session.Event)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.deliveredEvents == len(d.queue) {
		return nil, nil
	}
	event := d.queue[d.deliveredEvents]
	d.deliveredEvents++
	var handlers []func(session.Event)
	for _, handle := range d.handlers {
		handlers = append(handlers, handle)
	}
	return event, handlers
}

func (d *sendDelivery) drain() {
	d.deliverEvents()
	d.mu.Lock()
	d.queue = nil
	d.deliveredEvents = 0
	d.mu.Unlock()
}

func waitForSendLaunch(t *testing.T, delivery *sendDelivery) {
	t.Helper()
	select {
	case <-delivery.started:
	case <-time.After(3 * time.Second):
		t.Fatal("send did not launch")
	}
}

func receiveSendCompletion(t *testing.T, queue <-chan runtime.CommandResult) runtime.CommandResult {
	t.Helper()
	select {
	case result := <-queue:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("send did not settle")
		return runtime.CommandResult{}
	}
}

func newSendDelivery(t *testing.T, notices bool) *sendDelivery {
	t.Helper()
	d := &sendDelivery{notices: notices}
	initializeSendPlanner(t, d)
	return d
}

func (d *sendDelivery) ParticipantByName(name string) (participant.View, bool) {
	for _, view := range d.views {
		if view.Alias.Name == name {
			return view, true
		}
	}
	return participant.View{}, false
}

func (d *sendDelivery) frozenAlias(name string) participant.Alias {
	for _, plan := range d.plans {
		for _, alias := range plan.Targets() {
			if alias.Name == name {
				return alias
			}
		}
	}
	if d.planner != nil {
		if view, ok := d.planner.ParticipantByName(name); ok {
			return view.Alias
		}
	}
	return participant.Alias{Name: name}
}

func newTestAlias(name string) participant.Alias {
	return participant.New(name, "builder", participant.InitiativeManual).Alias
}

func aliasNames(aliases []participant.Alias) []string {
	names := make([]string, len(aliases))
	for i, alias := range aliases {
		names[i] = alias.Name
	}
	return names
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
	invocation, err := (std.SendCommand{}).Prepare(parseSendStatement(t, raw), runtime.Context{Session: delivery, Participants: participantReader{views: delivery.views}})
	if err != nil {
		t.Fatal(err)
	}
	return invocation
}

func TestSendCommand_PrepareRetainsSessionPlan(t *testing.T) {
	delivery := &sendDelivery{}
	initializeSendPlanner(t, delivery)
	ctx := runtime.Context{Session: delivery, Participants: participantReader{views: delivery.views}}
	first, err := (std.SendCommand{}).Prepare(parseSendStatement(t, "@ada first"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.launches != 0 || len(delivery.plans) != 1 {
		t.Fatal("preparation did not select exactly once without delivery")
	}
	if _, err := delivery.planner.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	second, err := (std.SendCommand{}).Prepare(parseSendStatement(t, "@ada second"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Staging().ToInform) != 0 || !slices.Equal(second.Staging().ToInform, []participant.Alias{delivery.frozenAlias("tim")}) {
		t.Fatal("session policy change altered retained plan or was ignored by next preparation")
	}
	assertSendUsesRetainedPlan(t, first, delivery)
}

func assertSendUsesRetainedPlan(t *testing.T, invocation runtime.Invocation, delivery *sendDelivery) {
	t.Helper()
	// Staging is a detached readiness description, not a second delivery plan.
	invocation.Staging().WaitFor[0].Name = "changed"
	if err := invocation.Go(func(runtime.CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	defer invocation.Close()
	if !reflect.DeepEqual(delivery.command.Plan, delivery.plans[0]) || delivery.command.Plan.PrimaryParticipant().Name != "ada" || delivery.message != "first" {
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
			var noticesWant []participant.Alias
			if notices {
				noticesWant = []participant.Alias{delivery.frozenAlias("tim")}
			}
			if !slices.Equal(invocation.Staging().WaitFor, []participant.Alias{delivery.plans[0].PrimaryParticipant()}) || !slices.Equal(invocation.Staging().ToInform, noticesWant) {
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
		{name: "crashed primary", statement: parseSendStatement(t, "@ada work"), ctx: runtime.Context{Session: &sendDelivery{views: []participant.View{{Alias: newTestAlias("ada"), Status: participant.StatusCrashed}}}, Participants: participantReader{}}},
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

func TestSendCommand_TerminalEventIsRetainedUntilDrained(t *testing.T) {
	delivery := &sendDelivery{duringExecute: &sendOutcome{Delivered: []string{"ada"}}}
	invocation := prepareSendInvocation(t, delivery, "@ada work")
	completions := 0
	if err := invocation.Go(func(runtime.CommandResult) { completions++ }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(delivery.order, []string{"subscribe", "execute"}) || completions != 0 {
		t.Fatal("completion was not retained for its owner")
	}
	delivery.drain()
	if completions != 1 || len(delivery.handlers) != 0 {
		t.Fatal("completion did not release subscription")
	}
	delivery.emit(sendOutcome{Delivered: []string{"ada"}})
	delivery.drain()
	if completions != 1 {
		t.Fatal("duplicate event completed invocation twice")
	}
}

type otherPrimaryRoutingCase struct {
	name       string
	recipients func(*sendDelivery) []session.RecipientResult
}

func otherPrimaryRoutingCases() []otherPrimaryRoutingCase {
	return []otherPrimaryRoutingCase{
		{
			name: "different primary",
			recipients: func(delivery *sendDelivery) []session.RecipientResult {
				return []session.RecipientResult{
					{Alias: delivery.frozenAlias("tim"), Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
				}
			},
		},
		{
			name: "our alias is only a notice recipient",
			recipients: func(delivery *sendDelivery) []session.RecipientResult {
				return []session.RecipientResult{
					{Alias: delivery.frozenAlias("tim"), Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
					{Alias: delivery.frozenAlias("ada"), Role: session.RecipientNotice, Status: session.DeliveryDelivered},
				}
			},
		},
	}
}

func TestSendCommand_InvalidTerminalRoutingFails(t *testing.T) {
	for _, tt := range otherPrimaryRoutingCases() {
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
			delivery.complete(session.RoutingResult{Kind: session.RoutingParticipantSend, Recipients: tt.recipients(delivery)}, nil)
			if len(completions) != 1 || !errors.Is(completions[0].Err, runtime.ErrInvalidDeliveryOutcome) || len(completions[0].Records) != 0 {
				t.Fatalf("invalid terminal outcome = %#v", completions)
			}
			delivery.emit(sendOutcome{Delivered: []string{"ada"}})
			delivery.drain()
			if len(completions) != 1 {
				t.Fatal("invalid outcome left invocation waiting for another result")
			}

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
			if len(delivery.handlers) != 0 {
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
				staging.DiscardInform(delivery.frozenAlias(alias))
			}
			if delivery.launches != 0 {
				t.Fatal("discard delivered work")
			}
			if err := invocation.Go(func(runtime.CommandResult) { t.Fatal("unexpected inline completion") }); err != nil {
				t.Fatal(err)
			}
			assertDiscardedNoticePlan(t, delivery, tt.targets)
			if !slices.Equal(staging.ToInform, []participant.Alias{delivery.frozenAlias("tim")}) {
				t.Fatal("callback mutated frozen readiness description")
			}
		})
	}
}

func assertDiscardedNoticePlan(t *testing.T, delivery *sendDelivery, targets []string) {
	t.Helper()
	if !slices.Equal(aliasNames(delivery.command.Plan.Targets()), targets) {
		t.Fatalf("delivery targets = %v, want %v", delivery.command.Plan.Targets(), targets)
	}
	if len(delivery.plans) != 1 || !slices.Equal(delivery.plans[0].Targets(), []participant.Alias{delivery.frozenAlias("ada"), delivery.frozenAlias("tim")}) {
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
			invocation.Staging().DiscardInform(delivery.frozenAlias("tim"))
			if launch {
				assertDiscardedNoticePlan(t, delivery, []string{"ada", "tim"})
			}
			if !launch && (len(delivery.handlers) != 0 || delivery.launches != 0) {
				t.Fatal("closed invocation started work")
			}
		})
	}
}

func (d *sendDelivery) Subscribe(handle func(runtime.ExecutionEvent)) func() {
	d.mu.Lock()
	d.order = append(d.order, "subscribe")
	d.terminal = handle
	d.mu.Unlock()
	return func() { d.mu.Lock(); d.terminal = nil; d.mu.Unlock() }
}

func (d *sendDelivery) complete(result session.RoutingResult, err error) {
	d.mu.Lock()
	handle := d.terminal
	d.mu.Unlock()
	if handle == nil {
		return
	}
	if err != nil {
		handle(runtime.ExecutionFailed{Err: err})
		return
	}
	handle(runtime.DeliveryCompleted{Result: result})
}
