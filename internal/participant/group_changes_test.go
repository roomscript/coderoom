package participant_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
)

func TestGroup_SubscriptionStartsAtSnapshotBoundary(t *testing.T) {
	g := participant.NewGroup()
	old := newParticipant("ada")
	old.StartupReady = true
	if err := g.Add(old); err != nil {
		t.Fatal(err)
	}
	var changes []participant.Change
	initial, subscription := g.Subscribe(func(change participant.Change) { changes = append(changes, change) })
	defer g.Unsubscribe(subscription)
	before := g.Snapshot()
	if err := g.Remove(old.Alias.Name); err != nil {
		t.Fatal(err)
	}
	replacement := participant.New(old.Alias.Name, "", participant.InitiativeManual)
	replacement.BeginStartup(testNow())
	if err := g.Add(replacement); err != nil {
		t.Fatal(err)
	}
	delivery := g.CaptureChanges(before)
	var lateChanges []participant.Change
	current, late := g.Subscribe(func(change participant.Change) { lateChanges = append(lateChanges, change) })
	defer g.Unsubscribe(late)
	delivery.Deliver()
	assertGroupReplacementChanges(t, changes, old.Alias, replacement.Alias)
	if len(lateChanges) != 0 {
		t.Fatal("later subscription received changes from before its snapshot")
	}
	assertGroupSnapshots(t, initial, current, old.Alias, replacement)
}

func assertGroupSnapshots(t *testing.T, initial, current []participant.View, old participant.Alias, replacement *participant.Participant) {
	t.Helper()
	if len(initial) != 1 || initial[0].Alias != old || !initial[0].StartupReady {
		t.Fatalf("initial snapshot changed after replacement: %#v", initial)
	}
	if len(current) != 1 || current[0].Alias != replacement.Alias {
		t.Fatalf("later snapshot = %#v", current)
	}
	current[0].Role = "changed"
	if replacement.Role != "" {
		t.Fatal("snapshot mutation changed Group membership state")
	}
}

func TestGroup_SubscriptionSeesNewMemberBeforeStartup(t *testing.T) {
	g := participant.NewGroup()
	var changes []participant.Change
	_, subscription := g.Subscribe(func(change participant.Change) { changes = append(changes, change) })
	defer g.Unsubscribe(subscription)
	before := g.Snapshot()
	p := participant.New("ada", "", participant.InitiativeManual)
	if err := g.Add(p); err != nil {
		t.Fatal(err)
	}
	g.CaptureChanges(before).Deliver()
	if len(changes) != 1 || changes[0].Kind != participant.StatusChanged || changes[0].After.Alias != p.Alias {
		t.Fatalf("new member was not introduced: %#v", changes)
	}
}

func assertGroupReplacementChanges(t *testing.T, changes []participant.Change, old, replacement participant.Alias) {
	t.Helper()
	if len(changes) != 2 {
		t.Fatalf("replacement changes = %#v", changes)
	}
	facts := make(map[participant.ChangeKind]participant.Change)
	for _, change := range changes {
		facts[change.Kind] = change
	}
	introduction, removal := facts[participant.StatusChanged], facts[participant.Removed]
	if introduction.After.Alias != replacement || introduction.After.Status != participant.StatusStarting || removal.Before.Alias != old {
		t.Fatalf("replacement facts did not identify introduction and removal: %#v", changes)
	}
}

func TestGroup_UnsubscribeFromCallbackDropsNextFact(t *testing.T) {
	g := participant.NewGroup()
	var changes []participant.Change
	var subscription *participant.Subscription
	_, subscription = g.Subscribe(func(change participant.Change) {
		changes = append(changes, change)
		g.Unsubscribe(subscription)
	})
	defer g.Unsubscribe(subscription)
	before := g.Snapshot()
	p := newParticipant("ada")
	p.StartupReady = true
	if err := g.Add(p); err != nil {
		t.Fatal(err)
	}
	// Introduction captures both status and readiness. Unsubscribing after the
	// first fact must drop the readiness fact, even though both belong to one change.
	g.CaptureChanges(before).Deliver()
	if len(changes) != 1 || changes[0].Kind != participant.StatusChanged || changes[0].After.Alias != p.Alias {
		t.Fatalf("changes after unsubscribe = %#v", changes)
	}
}

func TestGroup_CloseDropsCapturedDeliveryAndPreventsSubscriptions(t *testing.T) {
	g := participant.NewGroup()
	var changes []participant.Change
	_, subscription := g.Subscribe(func(change participant.Change) { changes = append(changes, change) })
	defer g.Unsubscribe(subscription)
	before := g.Snapshot()
	p := newParticipant("ada")
	if err := g.Add(p); err != nil {
		t.Fatal(err)
	}
	delivery := g.CaptureChanges(before)
	g.CloseSubscriptions()
	delivery.Deliver()
	initial, late := g.Subscribe(func(change participant.Change) { changes = append(changes, change) })
	defer g.Unsubscribe(late)
	if len(initial) != 1 || initial[0].Alias != p.Alias {
		t.Fatalf("closed Group lost inspection state: %#v", initial)
	}
	if len(changes) != 0 || late != nil {
		t.Fatal("closed Group delivered captured facts or accepted a subscription")
	}
}
