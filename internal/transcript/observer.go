package transcript

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Message is a decoded protocol JSON object. Field values retain their JSON encoding.
type Message = map[string]json.RawMessage

// ReplayMatch is the expected subset of a protocol message checked during replay.
type ReplayMatch = map[string]json.RawMessage

// Observer captures raw JSON-RPC traffic as replayable transcript steps.
// It satisfies protocol observer interfaces by method set.
type Observer struct {
	mu                sync.Mutex
	steps             []Step
	createReplayMatch func(Message) (ReplayMatch, error)
	err               error
}

// NewObserver records protocol messages. createReplayMatch receives decoded outbound
// objects and returns the expected subset, omitting only unstable fields. It must
// not mutate its input or perform I/O; unrecognized messages should match in full.
// A nil callback matches outbound objects in full. Inbound responses stay intact.
// Recording errors are retained by Err.
func NewObserver(createReplayMatch func(Message) (ReplayMatch, error)) *Observer {
	return &Observer{createReplayMatch: createReplayMatch}
}

// Err returns the first recording error, if any.
func (o *Observer) Err() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.err
}

// OnSend records one outbound JSON-RPC line as a replay "recv" step.
func (o *Observer) OnSend(msg string) {
	o.append("recv", msg)
}

// OnReceive records one inbound JSON-RPC line as a replay "send" step.
func (o *Observer) OnReceive(msg string) {
	o.append("send", msg)
}

// Steps returns a stable snapshot of the recorded transcript steps.
func (o *Observer) Steps() []Step {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Step, len(o.steps))
	copy(out, o.steps)
	return out
}

func (o *Observer) append(kind string, raw string) {
	step, err := o.createStep(kind, raw)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		if o.err == nil {
			o.err = err
		}
		return
	}
	o.steps = append(o.steps, step)
}

func (o *Observer) createStep(kind, raw string) (Step, error) {
	var payload Message
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return Step{}, fmt.Errorf("decode protocol message: %w", err)
	}
	if payload == nil {
		return Step{}, fmt.Errorf("expected protocol JSON object")
	}
	step := Step{Kind: kind}
	if kind != "recv" {
		step.Message = payload
		return step, nil
	}
	match := payload
	if o.createReplayMatch != nil {
		var err error
		match, err = o.createReplayMatch(payload)
		if err != nil {
			return Step{}, fmt.Errorf("create replay match: %w", err)
		}
	}
	step.Match = match
	return step, nil
}
