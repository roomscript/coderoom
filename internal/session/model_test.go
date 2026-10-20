package session_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// modelAgent is a mockAgent with a scripted ModelSelector. The test pushes
// ModelsListed and ModelSelected itself, so it controls their content and
// timing.
type modelAgent struct {
	*mockAgent
	settings   agent.Settings
	modelsErr  error
	selectErr  error
	listings   chan struct{}
	selections chan selection
}

type selection struct {
	opt    agent.ModelOption
	effort string
}

func newModelAgent(settings agent.Settings) *modelAgent {
	return &modelAgent{
		mockAgent:  newMockAgent(),
		settings:   settings,
		listings:   make(chan struct{}, 8),
		selections: make(chan selection, 8),
	}
}

func (a *modelAgent) Settings() agent.Settings { return a.settings }

func (a *modelAgent) Models() error {
	if a.modelsErr != nil {
		return a.modelsErr
	}
	a.listings <- struct{}{}
	return nil
}

func (a *modelAgent) SelectModel(opt agent.ModelOption, effort string) error {
	if a.selectErr != nil {
		return a.selectErr
	}
	a.selections <- selection{opt: opt, effort: effort}
	return nil
}

func (a *modelAgent) pushListing(listed agent.ModelsListed) {
	a.ch <- agent.Message{Mode: agent.ModeSingle, Content: listed}
}

func (a *modelAgent) pushSelected(selected agent.ModelSelected) {
	a.ch <- agent.Message{Mode: agent.ModeSingle, Content: selected}
}

func (a *modelAgent) awaitSelection(t *testing.T) selection {
	t.Helper()
	select {
	case sel := <-a.selections:
		return sel
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SelectModel")
		return selection{}
	}
}

var testCatalog = agent.ModelsListed{Models: []agent.ModelOption{
	{Name: "default", ID: "model-big", Efforts: []string{"low", "high"}},
	{Name: "big", ID: "model-big", Efforts: []string{"low", "high"}},
	{Name: "small", ID: "model-small"},
}}

// startModelParticipant invites ada backed by a, and waits until she joined.
func startModelParticipant(t *testing.T, a agent.Agent) (*session.Session, *testObserver) {
	t.Helper()
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	return s, obs
}

func changeModel(s *session.Session, model, effort string) error {
	if _, err := s.Execute(session.ChangeModelCommand{Alias: resolvedAlias(s, "ada"), Model: model, Effort: effort}); err != nil {
		return fmt.Errorf("change model: %w", err)
	}
	return nil
}

func listModels(s *session.Session) error {
	if _, err := s.Execute(session.ListModelsCommand{Alias: resolvedAlias(s, "ada")}); err != nil {
		return fmt.Errorf("list models: %w", err)
	}
	return nil
}

func viewOf(t *testing.T, s *session.Session) participant.View {
	t.Helper()
	view, ok := s.ParticipantByName("ada")
	if !ok {
		t.Fatal("ada not found")
	}
	return view
}

func TestInvite_recordsStartupSettings(t *testing.T) {
	a := newModelAgent(agent.Settings{Backend: "ignored", Model: "model-big", Effort: "low"})
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	invite(t, s, "ada")

	ready := mustReceive[session.AgentReady](t, obs.ch)

	want := agent.Settings{Backend: "fake", Model: "model-big", Effort: "low"}
	if ready.Settings != want {
		t.Fatalf("AgentReady.Settings = %+v, want %+v", ready.Settings, want)
	}
	if got := viewOf(t, s).Settings; got != want {
		t.Fatalf("view settings = %+v, want %+v", got, want)
	}
}

func TestListModels_publishesListingWithSettings(t *testing.T) {
	a := newModelAgent(agent.Settings{Model: "model-big", Effort: "low"})
	s, obs := startModelParticipant(t, a)

	if err := listModels(s); err != nil {
		t.Fatalf("ListModelsCommand: %v", err)
	}
	a.pushListing(testCatalog)

	listed := mustReceive[session.ModelsListed](t, obs.ch)
	if len(listed.Models) != len(testCatalog.Models) || listed.Models[1].Name != "big" {
		t.Fatalf("Models = %+v, want the catalog", listed.Models)
	}
	if listed.Settings.Model != "model-big" || listed.Status != participant.StatusIdle {
		t.Fatalf("ModelsListed = %+v, want current settings while idle", listed)
	}
}

func TestChangeModel_appliesTheBackendOutcome(t *testing.T) {
	a := newModelAgent(agent.Settings{Model: "model-small"})
	s, obs := startModelParticipant(t, a)

	if err := changeModel(s, "big", "high"); err != nil {
		t.Fatalf("ChangeModelCommand: %v", err)
	}
	expectParticipantStatus(t, s, "ada", participant.StatusChangingModel, "after admission")
	a.pushListing(testCatalog)

	sel := a.awaitSelection(t)
	if sel.opt.Name != "big" || sel.effort != "high" {
		t.Fatalf("SelectModel(%+v, %q), want big with high", sel.opt, sel.effort)
	}
	a.pushSelected(agent.ModelSelected{Settings: agent.Settings{Backend: "fake", Model: "model-big", Effort: "high"}})

	finished := mustReceive[session.ModelChangeFinished](t, obs.ch)
	want := agent.Settings{Backend: "fake", Model: "model-big", Effort: "high"}
	if finished.Err != nil || finished.Settings != want {
		t.Fatalf("ModelChangeFinished = %+v, want %+v without error", finished, want)
	}
	view := viewOf(t, s)
	if view.Status != participant.StatusIdle || view.Settings != want {
		t.Fatalf("view = %s %+v, want idle with %+v", view.Status, view.Settings, want)
	}
}

func TestChangeModel_matchesByNameBeforeID(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		wantName string
	}{
		{"name", "big", "big"},
		{"ID shared by aliases picks the first", "model-big", "default"},
		{"ID of an entry without alias", "model-small", "small"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newModelAgent(agent.Settings{})
			s, _ := startModelParticipant(t, a)

			if err := changeModel(s, tt.model, ""); err != nil {
				t.Fatalf("ChangeModelCommand: %v", err)
			}
			a.pushListing(testCatalog)

			if sel := a.awaitSelection(t); sel.opt.Name != tt.wantName {
				t.Fatalf("selected %q, want %q", sel.opt.Name, tt.wantName)
			}
		})
	}
}

func TestChangeModel_rejectsAgainstTheListing(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		effort  string
		listing agent.ModelsListed
	}{
		{"unknown model", "bigg", "", testCatalog},
		{"unsupported effort", "big", "max", testCatalog},
		{"effort on a model without efforts", "small", "low", testCatalog},
		{"listing failed", "big", "", agent.ModelsListed{Err: errors.New("no catalog")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := agent.Settings{Backend: "fake", Model: "model-small"}
			a := newModelAgent(before)
			s, obs := startModelParticipant(t, a)

			if err := changeModel(s, tt.model, tt.effort); err != nil {
				t.Fatalf("ChangeModelCommand: %v", err)
			}
			a.pushListing(tt.listing)

			finished := mustReceive[session.ModelChangeFinished](t, obs.ch)
			if finished.Err == nil {
				t.Fatal("expected an error")
			}
			if finished.Settings != before || viewOf(t, s).Settings != before {
				t.Fatalf("settings = %+v, want unchanged %+v", finished.Settings, before)
			}
			expectParticipantStatus(t, s, "ada", participant.StatusIdle, "after rejection")
			select {
			case sel := <-a.selections:
				t.Fatalf("SelectModel called with %+v", sel)
			default:
			}
		})
	}
}

func TestChangeModel_reportsAPartialOutcome(t *testing.T) {
	a := newModelAgent(agent.Settings{Model: "model-small", Effort: ""})
	s, obs := startModelParticipant(t, a)

	if err := changeModel(s, "big", "high"); err != nil {
		t.Fatalf("ChangeModelCommand: %v", err)
	}
	a.pushListing(testCatalog)
	a.awaitSelection(t)
	a.pushSelected(agent.ModelSelected{Settings: agent.Settings{Backend: "fake", Model: "model-big"}, Err: errors.New("effort failed")})

	finished := mustReceive[session.ModelChangeFinished](t, obs.ch)
	if finished.Err == nil {
		t.Fatal("expected an error")
	}
	if got := viewOf(t, s).Settings; got.Model != "model-big" || got.Effort != "" {
		t.Fatalf("settings = %+v, want the established model and an unknown effort", got)
	}
}

func TestChangeModel_rejectedWhileBusy(t *testing.T) {
	a := newModelAgent(agent.Settings{})
	s, _ := startModelParticipant(t, a)

	if err := changeModel(s, "big", ""); err != nil {
		t.Fatalf("first ChangeModelCommand: %v", err)
	}
	if err := changeModel(s, "small", ""); err == nil {
		t.Fatal("expected the second change to be rejected while the first runs")
	}
	if _, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "hi"}); err == nil {
		t.Fatal("expected work to be rejected during a model change")
	}
}

func TestModelCommands_requireTheCapability(t *testing.T) {
	s, _ := startModelParticipant(t, newMockAgent())

	if err := listModels(s); err == nil {
		t.Fatal("expected ListModelsCommand to fail without ModelSelector")
	}
	if err := changeModel(s, "big", ""); err == nil {
		t.Fatal("expected ChangeModelCommand to fail without ModelSelector")
	}
	expectParticipantStatus(t, s, "ada", participant.StatusIdle, "after rejection")
}

func TestChangeModel_startFailures(t *testing.T) {
	t.Run("listing does not start", func(t *testing.T) {
		a := newModelAgent(agent.Settings{})
		a.modelsErr = errors.New("write failed")
		s, _ := startModelParticipant(t, a)

		if err := changeModel(s, "big", ""); err == nil {
			t.Fatal("expected an error")
		}
		expectParticipantStatus(t, s, "ada", participant.StatusIdle, "after a failed start")
	})
	t.Run("selection does not start", func(t *testing.T) {
		a := newModelAgent(agent.Settings{})
		a.selectErr = errors.New("turn in progress")
		s, obs := startModelParticipant(t, a)

		if err := changeModel(s, "big", ""); err != nil {
			t.Fatalf("ChangeModelCommand: %v", err)
		}
		a.pushListing(testCatalog)

		if finished := mustReceive[session.ModelChangeFinished](t, obs.ch); finished.Err == nil {
			t.Fatal("expected an error")
		}
		expectParticipantStatus(t, s, "ada", participant.StatusIdle, "after a failed selection")
	})
}

func TestModelsListed_answersItsOwnRequest(t *testing.T) {
	a := newModelAgent(agent.Settings{Backend: "fake", Model: "model-small"})
	s, obs := startModelParticipant(t, a)

	// A read request is outstanding when the change starts; its failed
	// listing must not abort the change.
	if err := listModels(s); err != nil {
		t.Fatalf("ListModelsCommand: %v", err)
	}
	if err := changeModel(s, "big", ""); err != nil {
		t.Fatalf("ChangeModelCommand: %v", err)
	}
	a.pushListing(agent.ModelsListed{Err: errors.New("busy")})
	if listed := mustReceive[session.ModelsListed](t, obs.ch); listed.Err == nil {
		t.Fatal("expected the read request to get the failed listing")
	}
	a.pushListing(testCatalog)

	if sel := a.awaitSelection(t); sel.opt.Name != "big" {
		t.Fatalf("selected %q, want big", sel.opt.Name)
	}
}

func TestListModels_worksDuringAChange(t *testing.T) {
	a := newModelAgent(agent.Settings{Backend: "fake"})
	s, obs := startModelParticipant(t, a)

	if err := changeModel(s, "big", ""); err != nil {
		t.Fatalf("ChangeModelCommand: %v", err)
	}
	if err := listModels(s); err != nil {
		t.Fatalf("ListModelsCommand during a change: %v", err)
	}
	a.pushListing(testCatalog)
	a.pushListing(testCatalog)

	listed := mustReceive[session.ModelsListed](t, obs.ch)
	if listed.Status != participant.StatusChangingModel {
		t.Fatalf("Status = %s, want changing-model", listed.Status)
	}
}

func TestModelSelected_withoutAChangeIsDropped(t *testing.T) {
	before := agent.Settings{Backend: "fake", Model: "model-small"}
	a := newModelAgent(before)
	s, obs := startModelParticipant(t, a)

	a.pushSelected(agent.ModelSelected{Settings: agent.Settings{Backend: "fake", Model: "model-big"}})
	if err := listModels(s); err != nil {
		t.Fatalf("ListModelsCommand: %v", err)
	}
	a.pushListing(testCatalog)

	// The listing is read after the stray outcome, so by now it was handled.
	listed := mustReceive[session.ModelsListed](t, obs.ch)
	if listed.Settings != before || viewOf(t, s).Settings != before {
		t.Fatalf("settings = %+v, want unchanged %+v", listed.Settings, before)
	}
}

func TestChangeModel_removalEndsTheChange(t *testing.T) {
	a := newModelAgent(agent.Settings{Model: "model-small"})
	replacement := newModelAgent(agent.Settings{Model: "model-replacement"})
	obs := newTestObserver()
	agents := []agent.Agent{a, replacement}
	s := newSession(t, session.WithObserver(obs), session.WithAgentFactory(sequenceFactory(agents)))
	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	if err := changeModel(s, "big", ""); err != nil {
		t.Fatalf("ChangeModelCommand: %v", err)
	}
	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}); err != nil {
		t.Fatalf("remove during a change: %v", err)
	}
	mustReceive[session.AgentStopped](t, obs.ch)
	invite(t, s, "ada")
	ready := mustReceive[session.AgentReady](t, obs.ch)

	if ready.Settings.Model != "model-replacement" {
		t.Fatalf("replacement settings = %+v, want its own startup settings", ready.Settings)
	}
	expectParticipantStatus(t, s, "ada", participant.StatusIdle, "replacement")
}

func TestShutdown_duringAChange(t *testing.T) {
	a := newModelAgent(agent.Settings{})
	s, _ := startModelParticipant(t, a)

	if err := changeModel(s, "big", ""); err != nil {
		t.Fatalf("ChangeModelCommand: %v", err)
	}
	done := make(chan struct{})
	go func() {
		s.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Shutdown blocked during a model change")
	}
}

// sequenceFactory hands out agents in invitation order.
func sequenceFactory(agents []agent.Agent) session.AgentFactory {
	next := 0
	return func(*session.Session, roomconfig.ParticipantConfig) (agent.Agent, error) {
		a := agents[next]
		next++
		return a, nil
	}
}
