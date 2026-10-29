package transcript

import (
	"errors"
	"testing"
)

func TestObserver_RecordsDirectionalSteps(t *testing.T) {
	obs := NewObserver(nil)

	obs.OnSend(`{"id":1,"method":"request","params":{"clientInfo":{"name":"coderoom"}}}`)
	obs.OnReceive(`{"id":1,"result":{"capabilities":{"experimentalApi":true}}}`)

	steps := obs.Steps()
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	if steps[0].Kind != "recv" || steps[0].Match == nil {
		t.Fatalf("first step = %#v, want recv with match", steps[0])
	}
	if steps[1].Kind != "send" || steps[1].Message == nil {
		t.Fatalf("second step = %#v, want send with message", steps[1])
	}
}

func TestObserverUsesSuppliedMatchRule(t *testing.T) {
	obs := NewObserver(func(payload Message) (ReplayMatch, error) { return ReplayMatch{"stable": payload["stable"]}, nil })
	obs.OnSend(`{"stable":"value","incidental":42}`)
	obs.OnReceive(`{"stable":"value","incidental":42}`)
	steps := obs.Steps()
	if len(steps) != 2 {
		t.Fatalf("steps = %+v", steps)
	}
	if len(steps[0].Match.(ReplayMatch)) != 1 || len(steps[1].Message.(Message)) != 2 {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestObserverRetainsMatchErrors(t *testing.T) {
	want := errors.New("invalid backend field")
	obs := NewObserver(func(Message) (ReplayMatch, error) { return nil, want })
	obs.OnSend(`{"field":true}`)
	if !errors.Is(obs.Err(), want) || len(obs.Steps()) != 0 {
		t.Fatalf("error=%v steps=%v", obs.Err(), obs.Steps())
	}
}

func TestObserverRejectsInvalidObjects(t *testing.T) {
	for _, raw := range []string{`invalid`, `[]`, `null`} {
		t.Run(raw, func(t *testing.T) {
			obs := NewObserver(nil)
			obs.OnSend(raw)
			if obs.Err() == nil || len(obs.Steps()) != 0 {
				t.Fatalf("error=%v steps=%v", obs.Err(), obs.Steps())
			}
		})
	}
}
