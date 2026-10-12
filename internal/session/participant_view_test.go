package session

import (
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent/echo"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestParticipantViews_preserveStartupReadinessAndDetach(t *testing.T) {
	s := New()
	t.Cleanup(s.Shutdown)
	constructed := registerIdleBeforeReadyParticipant(t, s)
	before, ok := s.ParticipantByName("ada")
	if !ok || before.Status != participant.StatusIdle || before.StartupReady {
		t.Fatalf("view = %#v, %v", before, ok)
	}
	assertViewIdentity(t, before, constructed)
	all := s.Participants()
	if len(all) != 1 || all[0] != before {
		t.Fatalf("list differs from lookup: %#v", all)
	}
	if err := s.updateParticipant(resolvedAda(s), func(p *participant.Participant) (Event, error) { return nil, p.SessionReady() }); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ParticipantByName("ada")
	if !after.StartupReady || before.StartupReady || all[0].StartupReady {
		t.Fatal("startup readiness was missing or changed an earlier snapshot")
	}
	assertViewIdentity(t, after, constructed)
	assertParticipantViewsDetached(t, s, all, after)
}

func assertViewIdentity(t *testing.T, view participant.View, want participant.Alias) {
	t.Helper()
	if want.ID == (participant.ID{}) || view.Alias != want {
		t.Fatalf("view alias = %+v, want non-zero identity %+v", view.Alias, want)
	}
}

func TestParticipantViews_includeAllStatesAndTurnIdentity(t *testing.T) {
	s := New()
	t.Cleanup(s.Shutdown)
	statuses := []participant.Status{participant.StatusStarting, participant.StatusAttached, participant.StatusIdle, participant.StatusPreparing, participant.StatusWorking, participant.StatusKeepalive, participant.StatusCrashed}
	for _, status := range statuses {
		p := participant.New(string(status), "", participant.InitiativeManual)
		p.Status = status
		p.StartupReady = status == participant.StatusIdle || status == participant.StatusWorking
		p.TurnID = 17
		if err := s.addParticipant(p); err != nil {
			t.Fatal(err)
		}
	}
	values := s.Participants()
	if len(values) != len(statuses) {
		t.Fatalf("participants = %d, want %d", len(values), len(statuses))
	}
	for _, value := range values {
		lookup, ok := s.Participant(value.Alias)
		if !ok || lookup != value || value.TurnID != 17 {
			t.Fatalf("inconsistent view: %#v / %#v", value, lookup)
		}
	}
}

func registerIdleBeforeReadyParticipant(t *testing.T, s *Session) participant.Alias {
	t.Helper()
	p := participant.New("ada", "builder", participant.InitiativeManual)
	now := time.Now()
	p.BeginStartup(now)
	if err := p.AttachAgent(echo.New(), now); err != nil {
		t.Fatal(err)
	}
	if err := p.CommitIdle(now); err != nil {
		t.Fatal(err)
	}
	if err := s.addParticipant(p); err != nil {
		t.Fatal(err)
	}
	return p.Alias
}

func assertParticipantViewsDetached(t *testing.T, s *Session, all []participant.View, after participant.View) {
	t.Helper()
	all[0].Alias.Name = "changed"
	after.Alias.Name = "changed"
	current, ok := s.ParticipantByName("ada")
	if !ok || current.Alias.Name != "ada" {
		t.Fatal("snapshot mutation changed session")
	}
	if _, ok := s.ParticipantByName("unknown"); ok {
		t.Fatal("unknown alias was present")
	}
}
