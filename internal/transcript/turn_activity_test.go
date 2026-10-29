package transcript

import (
	"context"
	"testing"
	"time"
)

func TestTurnActivityObserverForwardsRecordingAndResets(t *testing.T) {
	recorder := NewObserver(nil)
	activity := NewTurnActivityObserver(recorder, func(raw string) bool { return raw == `{"active":true}` })
	activity.OnSend(`{"id":1,"method":"test"}`)
	activity.OnReceive(`{"active":true}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := activity.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	steps := recorder.Steps()
	if len(steps) != 2 || steps[0].Kind != "recv" || steps[1].Kind != "send" {
		t.Fatalf("forwarded steps=%+v", steps)
	}
	activity.OnReceive(`{"active":true}`)
	activity.Reset()
	emptyCtx, emptyCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer emptyCancel()
	if err := activity.Wait(emptyCtx); err == nil {
		t.Fatal("Reset retained old activation")
	}
}

func TestTurnActivityObserverIgnoresUnrelatedNotifications(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"unrelated", `{"other":true}`}, {"malformed", `invalid`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activity := NewTurnActivityObserver(nil, func(raw string) bool { return raw == `{"active":true}` })
			activity.OnReceive(tt.raw)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := activity.Wait(ctx); err == nil {
				t.Fatal("unrelated event signalled activation")
			}
		})
	}
}
