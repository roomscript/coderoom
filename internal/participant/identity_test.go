package participant_test

import (
	"fmt"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestNew_createsUniqueUUIDAliases(t *testing.T) {
	seen := make(map[participant.ID]bool)
	for range 1000 {
		alias := participant.New("ada", "builder", participant.InitiativeManual).Alias
		if alias.Name != "ada" || alias.ID == (participant.ID{}) {
			t.Fatalf("invalid alias: %+v", alias)
		}
		if seen[alias.ID] {
			t.Fatalf("duplicate ID: %x", alias.ID)
		}
		seen[alias.ID] = true
		if alias.ID[6]>>4 != 4 || alias.ID[8]>>6 != 2 {
			t.Fatalf("ID is not a UUID v4: %x", alias.ID)
		}
	}
}

func TestAlias_survivesRegistrationAndLifecycle(t *testing.T) {
	p := participant.New("ada", "builder", participant.InitiativeManual)
	want := p.Alias
	group := participant.NewGroup()
	steps := []struct {
		name string
		run  func() error
	}{
		{"registration", func() error { return group.Add(p) }},
		{"startup", func() error { p.BeginStartup(testNow()); return nil }},
		{"attachment", func() error { return p.AttachAgent(fakeAgent{}, testNow()) }},
		{"idle", func() error { return p.CommitIdle(testNow()) }},
		{"ready", p.SessionReady},
		{"preparation", func() error { return p.PrepareForWork(testNow(), 1) }},
		{"work", func() error { return p.BeginWorking(testNow(), agent.StreamID("anchor")) }},
		{"completion", func() error {
			if _, err := p.CloseStream("anchor"); err != nil {
				return fmt.Errorf("close anchor stream: %w", err)
			}
			return nil
		}},
		{"completed idle", func() error { return p.BecomeIdle(testNow()) }},
		{"next preparation", func() error { return p.PrepareForWork(testNow(), 1) }},
		{"next work", func() error { return p.BeginWorking(testNow(), "next") }},
		{"cancellation", func() error { return p.AbortWork(testNow()) }},
		{"maintenance", func() error { return p.BeginKeepalive(testNow()) }},
		{"maintenance completion", func() error { return p.FinishKeepalive(testNow()) }},
		{"crash", func() error { p.Crash(testNow()); return nil }},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if err := step.run(); err != nil {
				t.Fatalf("transition: %v", err)
			}
			if p.Alias != want || p.Snapshot().Alias != want {
				t.Fatalf("alias changed: %+v, want %+v", p.Alias, want)
			}
		})
	}
}

func TestGroup_sameNameInstancesHaveDistinctAliases(t *testing.T) {
	first := participant.NewGroup()
	second := participant.NewGroup()
	original := participant.New("ada", "builder", participant.InitiativeManual)
	other := participant.New("ada", "builder", participant.InitiativeManual)
	if err := first.Add(original); err != nil {
		t.Fatal(err)
	}
	if err := second.Add(other); err != nil {
		t.Fatal(err)
	}
	if original.Alias.ID == other.Alias.ID {
		t.Fatal("same-name participants in separate registries share an ID")
	}
	if err := first.Remove("ada"); err != nil {
		t.Fatal(err)
	}
	replacement := participant.New("ada", "builder", participant.InitiativeManual)
	want := replacement.Alias
	if err := first.Add(replacement); err != nil {
		t.Fatal(err)
	}
	current, ok := first.Get("ada")
	if !ok || current.Alias != want || current.Alias.ID == original.Alias.ID {
		t.Fatal("same-name replacement did not retain its new identity")
	}
}
