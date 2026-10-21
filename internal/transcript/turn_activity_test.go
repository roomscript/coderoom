package transcript

import (
	"context"
	"testing"
	"time"
)

func TestTurnActivityObserverForwardsRecordingAndResets(t *testing.T) {
	recorder := NewObserver()
	activity := NewTurnActivityObserver(recorder)
	activity.OnSend(`{"id":1,"method":"test"}`)
	activity.OnReceive(`{"method":"item/started","params":{"item":{"type":"userMessage"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := activity.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	steps := recorder.Steps()
	if len(steps) != 2 || steps[0].Kind != "recv" || steps[1].Kind != "send" {
		t.Fatalf("forwarded steps=%+v", steps)
	}
	activity.OnReceive(`{"method":"item/started","params":{"item":{"type":"userMessage"}}}`)
	activity.Reset()
	emptyCtx, emptyCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer emptyCancel()
	if err := activity.Wait(emptyCtx); err == nil {
		t.Fatal("Reset retained old activation")
	}
}

func TestTurnActivityObserverIgnoresUnrelatedNotifications(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"turn started", `{"method":"turn/started","params":{"turn":{"id":"t1"}}}`},
		{"reasoning item", `{"method":"item/started","params":{"item":{"type":"reasoning"}}}`},
		{"user completion", `{"method":"item/completed","params":{"item":{"type":"userMessage"}}}`},
		{"malformed", `invalid`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activity := NewTurnActivityObserver(nil)
			activity.OnReceive(tt.raw)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := activity.Wait(ctx); err == nil {
				t.Fatal("unrelated event signalled activation")
			}
		})
	}
}
