package participant_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent/echo"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestView_lifecyclePredicates(t *testing.T) {
	type permissions struct{ sendable, routable, ready, active, cancellable, removable bool }
	tests := []struct {
		name         string
		status       participant.Status
		startupReady bool
		want         permissions
	}{
		{name: "starting", status: participant.StatusStarting},
		{name: "attached", status: participant.StatusAttached},
		{name: "idle before startup ready", status: participant.StatusIdle, want: permissions{routable: true}},
		{name: "idle ready", status: participant.StatusIdle, startupReady: true, want: permissions{sendable: true, routable: true, ready: true, cancellable: true, removable: true}},
		{name: "preparing", status: participant.StatusPreparing, startupReady: true, want: permissions{sendable: true, active: true, cancellable: true, removable: true}},
		{name: "working", status: participant.StatusWorking, startupReady: true, want: permissions{sendable: true, routable: true, active: true, cancellable: true, removable: true}},
		{name: "keepalive", status: participant.StatusKeepalive, startupReady: true, want: permissions{removable: true}},
		{name: "changing model", status: participant.StatusChangingModel, startupReady: true, want: permissions{removable: true}},
		{name: "crashed after startup", status: participant.StatusCrashed, startupReady: true, want: permissions{removable: true}},
		{name: "failed startup", status: participant.StatusCrashed, want: permissions{removable: true}},
		{name: "unknown status", startupReady: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := participant.View{Status: tt.status, StartupReady: tt.startupReady}
			got := permissions{view.IsSendable(), view.IsRoutable(), view.IsReadyForWork(), view.HasActiveTurn(), view.IsCancellable(), view.IsRemovable()}
			if got != tt.want {
				t.Fatalf("permissions = %+v, want %+v", got, tt.want)
			}
			runtime := participant.Participant{View: view}
			if runtime.IsSendable() || runtime.IsCancellable() {
				t.Fatal("runtime allowed work without an agent handle")
			}
			runtime.Agent = echo.New()
			if runtime.IsSendable() != view.IsSendable() || runtime.IsCancellable() != view.IsCancellable() {
				t.Fatal("runtime disagrees with view predicates")
			}
		})
	}
}

func TestParticipant_modelChangeTransitions(t *testing.T) {
	begin := func(p *participant.Participant) error { return p.BeginModelChange(time.Now()) }
	finish := func(p *participant.Participant) error { return p.FinishModelChange(time.Now()) }
	work := func(p *participant.Participant) error { return p.PrepareForWork(time.Now(), 1) }
	keepalive := func(p *participant.Participant) error { return p.BeginKeepalive(time.Now()) }
	tests := []struct {
		name    string
		from    participant.Status
		run     func(*participant.Participant) error
		wantErr error
		want    participant.Status
	}{
		{"begin from idle", participant.StatusIdle, begin, nil, participant.StatusChangingModel},
		{"begin while working", participant.StatusWorking, begin, participant.ErrNotIdle, participant.StatusWorking},
		{"begin during keepalive", participant.StatusKeepalive, begin, participant.ErrNotIdle, participant.StatusKeepalive},
		{"begin during another change", participant.StatusChangingModel, begin, participant.ErrNotIdle, participant.StatusChangingModel},
		{"finish a change", participant.StatusChangingModel, finish, nil, participant.StatusIdle},
		{"finish without a change", participant.StatusIdle, finish, participant.ErrNotChangingModel, participant.StatusIdle},
		{"work during a change", participant.StatusChangingModel, work, participant.ErrWorkAlreadyStarted, participant.StatusChangingModel},
		{"keepalive during a change", participant.StatusChangingModel, keepalive, participant.ErrNotIdle, participant.StatusChangingModel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := participant.New("ada", "", participant.InitiativeManual)
			p.Status = tt.from
			p.StartupReady = true
			if err := tt.run(p); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if p.Status != tt.want {
				t.Fatalf("status = %s, want %s", p.Status, tt.want)
			}
		})
	}
}
