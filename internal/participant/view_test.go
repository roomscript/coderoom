package participant_test

import (
	"testing"

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
