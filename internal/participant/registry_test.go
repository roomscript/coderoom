package participant_test

import (
	"errors"
	"testing"
	"time"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/participant"
)

type fakeAgent struct{}

func (fakeAgent) Start() error { return nil }
func (fakeAgent) Send(string) (agent.StreamID, error) {
	return "", nil
}
func (fakeAgent) SendNotice(string) (agent.StreamID, error) { return "", nil }
func (fakeAgent) Read() (agent.Message, error)              { return agent.Message{}, errors.New("no messages") }
func (fakeAgent) Interrupt() error                          { return nil }
func (fakeAgent) Stop() error                               { return nil }

func newParticipant(alias string) *participant.Participant {
	p := participant.New(alias, "builder", participant.InitiativeManual)
	p.Status = participant.StatusIdle
	return p
}

func TestRegistry_AddAndGet(t *testing.T) {
	r := participant.NewRegistry()
	p := newParticipant("ada")

	if err := r.Add(p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := r.Get("ada")
	if !ok {
		t.Fatal("Get: expected participant, got nothing")
	}
	if got != p {
		t.Errorf("Get returned wrong participant")
	}
}

func TestRegistry_Add_nil(t *testing.T) {
	r := participant.NewRegistry()
	if err := r.Add(nil); err == nil {
		t.Fatal("expected error on nil participant, got nil")
	}
}

func TestRegistry_Add_emptyAlias(t *testing.T) {
	r := participant.NewRegistry()
	if err := r.Add(&participant.Participant{}); err == nil {
		t.Fatal("expected error on empty alias, got nil")
	}
}

func TestRegistry_Add_duplicateAlias(t *testing.T) {
	r := participant.NewRegistry()
	if err := r.Add(newParticipant("ada")); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := r.Add(newParticipant("ada")); err == nil {
		t.Fatal("expected error on duplicate alias, got nil")
	}
}

func TestRegistry_Add_assignsDeterministicUniqueColors(t *testing.T) {
	first := participant.NewRegistry()
	second := participant.NewRegistry()

	for _, alias := range []string{"ada", "turing", "hopper"} {
		firstParticipant := newParticipant(alias)
		secondParticipant := newParticipant(alias)
		if err := first.Add(firstParticipant); err != nil {
			t.Fatalf("first Add(%q): %v", alias, err)
		}
		if err := second.Add(secondParticipant); err != nil {
			t.Fatalf("second Add(%q): %v", alias, err)
		}
		if firstParticipant.Color != secondParticipant.Color {
			t.Fatalf("color for %q = %q and %q", alias, firstParticipant.Color, secondParticipant.Color)
		}
	}

	participants := first.List()
	seen := make(map[string]bool, len(participants))
	for _, p := range participants {
		if p.Color == "" {
			t.Fatalf("participant %q has no color", p.Alias)
		}
		if seen[p.Color] {
			t.Fatalf("color %q assigned more than once", p.Color)
		}
		seen[p.Color] = true
	}
}

func TestRegistry_Add_rejectedParticipantDoesNotConsumeColor(t *testing.T) {
	withRejection := participant.NewRegistry()
	baseline := participant.NewRegistry()

	first := newParticipant("ada")
	if err := withRejection.Add(first); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	if err := withRejection.Add(newParticipant("ada")); err == nil {
		t.Fatal("expected duplicate Add to fail")
	}
	next := newParticipant("turing")
	if err := withRejection.Add(next); err != nil {
		t.Fatalf("Add after rejection: %v", err)
	}

	if err := baseline.Add(newParticipant("ada")); err != nil {
		t.Fatalf("baseline first Add: %v", err)
	}
	baselineNext := newParticipant("turing")
	if err := baseline.Add(baselineNext); err != nil {
		t.Fatalf("baseline second Add: %v", err)
	}
	if next.Color != baselineNext.Color {
		t.Fatalf("color after rejection = %q, want %q", next.Color, baselineNext.Color)
	}
}

func TestRegistry_Remove_doesNotReleaseColor(t *testing.T) {
	registry := participant.NewRegistry()
	removed := newParticipant("ada")
	if err := registry.Add(removed); err != nil {
		t.Fatalf("Add removed participant: %v", err)
	}
	if err := registry.Remove("ada"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	replacement := newParticipant("turing")
	if err := registry.Add(replacement); err != nil {
		t.Fatalf("Add replacement: %v", err)
	}
	if replacement.Color == removed.Color {
		t.Fatalf("replacement reused removed color %q", removed.Color)
	}
}

func TestRegistry_Get_missing(t *testing.T) {
	r := participant.NewRegistry()
	_, ok := r.Get("nobody")
	if ok {
		t.Fatal("expected ok=false for unknown alias")
	}
}

func TestRegistry_Remove(t *testing.T) {
	r := participant.NewRegistry()
	if err := r.Add(newParticipant("ada")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Remove("ada"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := r.Get("ada"); ok {
		t.Error("participant still present after Remove")
	}
}

func TestRegistry_Remove_missing(t *testing.T) {
	r := participant.NewRegistry()
	if err := r.Remove("nobody"); err == nil {
		t.Fatal("expected error removing unknown alias, got nil")
	}
}

func TestRegistry_List(t *testing.T) {
	r := participant.NewRegistry()
	_ = r.Add(newParticipant("ada"))
	_ = r.Add(newParticipant("turing"))

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(list))
	}
	aliases := map[string]bool{}
	for _, p := range list {
		aliases[p.Alias] = true
	}
	for _, want := range []string{"ada", "turing"} {
		if !aliases[want] {
			t.Errorf("expected alias %q in list", want)
		}
	}
}

func TestRegistry_List_empty(t *testing.T) {
	r := participant.NewRegistry()
	if list := r.List(); len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestRegistry_ListAvailable_filtersByAgentAndStatus(t *testing.T) {
	r := participant.NewRegistry()
	addAvailableTestParticipant(t, r, "starting", participant.StatusStarting, false)
	addAvailableTestParticipant(t, r, "crashed", participant.StatusCrashed, true)
	addAvailableTestParticipant(t, r, "preparing", participant.StatusPreparing, true)
	addAvailableTestParticipant(t, r, "keepalive", participant.StatusKeepalive, true)
	addAvailableTestParticipant(t, r, "idle", participant.StatusIdle, true)
	addAvailableTestParticipant(t, r, "working", participant.StatusWorking, true)

	avail := r.ListAvailable()
	aliases := map[string]bool{}
	for _, p := range avail {
		aliases[p.Alias] = true
	}
	assertAvailableAlias(t, aliases, "starting", false)
	assertAvailableAlias(t, aliases, "crashed", false)
	assertAvailableAlias(t, aliases, "preparing", false)
	assertAvailableAlias(t, aliases, "keepalive", false)
	assertAvailableAlias(t, aliases, "idle", true)
	assertAvailableAlias(t, aliases, "working", true)
}

func addAvailableTestParticipant(t *testing.T, r *participant.Registry, alias string, status participant.Status, attachAgent bool) {
	t.Helper()
	p := newParticipant(alias)
	p.Status = status
	if attachAgent {
		p.Agent = fakeAgent{}
	}
	if err := r.Add(p); err != nil {
		t.Fatalf("Add(%q): %v", alias, err)
	}
}

func assertAvailableAlias(t *testing.T, aliases map[string]bool, alias string, want bool) {
	t.Helper()
	if aliases[alias] == want {
		return
	}
	if want {
		t.Fatalf("expected %q to be included in ListAvailable", alias)
	}
	t.Fatalf("expected %q to be excluded from ListAvailable", alias)
}

func TestRegistry_StatusListsAndPredicates(t *testing.T) {
	r := participant.NewRegistry()

	pStarting := newParticipant("starting")
	pStarting.Status = participant.StatusStarting

	pCrashed := newParticipant("crashed")
	pCrashed.Status = participant.StatusCrashed

	pWorking := newParticipant("working")
	pWorking.Status = participant.StatusWorking

	pKeepalive := newParticipant("keepalive")
	pKeepalive.Status = participant.StatusKeepalive

	_ = r.Add(pStarting)
	_ = r.Add(pCrashed)
	_ = r.Add(pWorking)
	_ = r.Add(pKeepalive)

	if !r.HasStarting() {
		t.Fatal("expected HasStarting true")
	}
	if !r.HasCrashed() {
		t.Fatal("expected HasCrashed true")
	}
	if !r.HasWorking() {
		t.Fatal("expected HasWorking true")
	}
	if !r.HasKeepalive() {
		t.Fatal("expected HasKeepalive true")
	}

	if len(r.ListStarting()) != 1 {
		t.Fatalf("expected 1 starting, got %d", len(r.ListStarting()))
	}
	if len(r.ListCrashed()) != 1 {
		t.Fatalf("expected 1 crashed, got %d", len(r.ListCrashed()))
	}
	if len(r.ListWorking()) != 1 {
		t.Fatalf("expected 1 working, got %d", len(r.ListWorking()))
	}
}

func TestParticipantSnapshot_copiesOpenStreams(t *testing.T) {
	p := newParticipant("ada")
	p.Status = participant.StatusWorking
	if err := p.TrackStream(agent.StreamID("out1")); err != nil {
		t.Fatalf("TrackStream: %v", err)
	}

	snap := p.Snapshot()
	if _, ok := snap.OpenStreams[agent.StreamID("out1")]; !ok {
		t.Fatal("expected snapshot to include tracked stream")
	}

	snap.OpenStreams[agent.StreamID("out2")] = struct{}{}
	if _, ok := p.OpenStreams[agent.StreamID("out2")]; ok {
		t.Fatal("expected snapshot stream mutation to not affect participant state")
	}
}

func TestParticipantTurnID_recordsAssignedSessionIdentity(t *testing.T) {
	p := newParticipant("ada")
	for _, want := range []uint64{41, 97} {
		if err := p.PrepareForWork(testNow()); err != nil {
			t.Fatalf("PrepareForWork: %v", err)
		}
		if err := p.BeginWorking(testNow(), agent.StreamID("anchor"), want); err != nil {
			t.Fatalf("BeginWorking: %v", err)
		}
		if got := p.TurnID; got != want {
			t.Fatalf("turn ID = %d, want %d", got, want)
		}
		if _, err := p.CloseStream(agent.StreamID("anchor")); err != nil {
			t.Fatalf("CloseStream: %v", err)
		}
		if err := p.BecomeIdle(testNow()); err != nil {
			t.Fatalf("BecomeIdle: %v", err)
		}
	}
}

func TestParticipantTurnID_survivesNonTurnRoundTrips(t *testing.T) {
	p := idleParticipantAfterTurn(t, 41)
	want := p.TurnID

	assertTurnIDSurvivesKeepalive(t, p, want)
	assertTurnIDSurvivesPrepareAbort(t, p, want)
}

func idleParticipantAfterTurn(t *testing.T, turnID uint64) *participant.Participant {
	t.Helper()
	p := newParticipant("ada")
	if err := p.PrepareForWork(testNow()); err != nil {
		t.Fatalf("PrepareForWork: %v", err)
	}
	if err := p.BeginWorking(testNow(), agent.StreamID("anchor"), turnID); err != nil {
		t.Fatalf("BeginWorking: %v", err)
	}
	if _, err := p.CloseStream(agent.StreamID("anchor")); err != nil {
		t.Fatalf("CloseStream: %v", err)
	}
	if err := p.BecomeIdle(testNow()); err != nil {
		t.Fatalf("BecomeIdle: %v", err)
	}
	return p
}

func assertTurnIDSurvivesKeepalive(t *testing.T, p *participant.Participant, want uint64) {
	t.Helper()
	if err := p.BeginKeepalive(testNow()); err != nil {
		t.Fatalf("BeginKeepalive: %v", err)
	}
	if err := p.FinishKeepalive(testNow()); err != nil {
		t.Fatalf("FinishKeepalive: %v", err)
	}
	if got := p.TurnID; got != want {
		t.Fatalf("turn ID after keepalive = %d, want %d", got, want)
	}
}

func assertTurnIDSurvivesPrepareAbort(t *testing.T, p *participant.Participant, want uint64) {
	t.Helper()
	if err := p.PrepareForWork(testNow()); err != nil {
		t.Fatalf("PrepareForWork: %v", err)
	}
	if err := p.AbortWork(testNow()); err != nil {
		t.Fatalf("AbortWork: %v", err)
	}
	if got := p.TurnID; got != want {
		t.Fatalf("turn ID after prepare abort = %d, want %d", got, want)
	}
}

func TestParticipantMarkIdle_rejectsOpenStreams(t *testing.T) {
	p := newParticipant("ada")
	const anchor = agent.StreamID("anchor1")
	if err := p.PrepareForWork(testNow()); err != nil {
		t.Fatalf("PrepareForWork: %v", err)
	}
	if err := p.BeginWorking(testNow(), anchor, 1); err != nil {
		t.Fatalf("BeginWorking: %v", err)
	}
	// Anchor is still open in OpenStreams — BecomeIdle must reject.
	if err := p.BecomeIdle(testNow()); err == nil {
		t.Fatal("expected BecomeIdle to reject open streams")
	}
}

func TestParticipantCloseStream_onlyAnchorTriggersIdle(t *testing.T) {
	p := newParticipant("ada")
	if err := p.PrepareForWork(testNow()); err != nil {
		t.Fatalf("PrepareForWork: %v", err)
	}
	if err := p.BeginWorking(testNow(), agent.StreamID("anchor"), 1); err != nil {
		t.Fatalf("BeginWorking: %v", err)
	}
	if err := p.TrackStream(agent.StreamID("out1")); err != nil {
		t.Fatalf("TrackStream out1: %v", err)
	}

	shouldIdle, err := p.CloseStream(agent.StreamID("out1"))
	if err != nil {
		t.Fatalf("CloseStream out1: %v", err)
	}
	if shouldIdle {
		t.Fatal("expected non-anchor close to keep participant working")
	}

	shouldIdle, err = p.CloseStream(agent.StreamID("anchor"))
	if err != nil {
		t.Fatalf("CloseStream anchor: %v", err)
	}
	if !shouldIdle {
		t.Fatal("expected anchor close to trigger idle")
	}
}

func testNow() time.Time { return time.Unix(123, 0) }
