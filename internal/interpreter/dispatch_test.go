package interpreter

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func TestDispatch_registryCoversParserForms(t *testing.T) {
	examples := map[string]string{
		"invite": "/invite ada", "remove": "/remove ada", "cancel": "/cancel ada", "model": "/model ada opus",
		"handoff": "/handoff ada tim", "who": "/who", "help": "/help",
		"shell": "/shell true", "policy": "/policy enable send-notices",
		"def": "/def check /shell true", "loop": "/loop @ada work /until /check /max 1",
		"send": "@ada work", "broadcast": "work", "user-command": "/check",
	}
	// Syntax supplies this list independently of the runtime registry. A new
	// built-in must gain a dispatch example rather than silently use fallback.
	for _, name := range promptlang.BuiltinNames() {
		if _, ok := examples[name]; !ok {
			t.Errorf("parser built-in %s has no dispatch example", name)
		}
	}
	registry := newCommandRegistry(nil)
	for name, raw := range examples {
		t.Run(name, func(t *testing.T) {
			parsed, err := promptlang.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			command, ok := registry.Lookup(parsed)
			if !ok || command.Name() != name {
				t.Fatalf("no modular dispatch for %q: %#v", raw, command)
			}
		})
	}
}

func TestDispatch_missingRegistrationDoesNotExecuteLegacyHandler(t *testing.T) {
	events := make(chan Event, 32)
	interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	interp.commands = runtime.NewRegistry()
	if err := interp.Submit("  /who  "); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		rejected, ok := event.(InputRejected)
		return ok && rejected.Code == ErrorInvalidInput && rejected.Raw == "  /who  "
	})
	interp.Close()
	if records := interp.Snapshot().Room.Records; len(records) != 0 {
		t.Fatalf("unregistered command executed: records=%#v", records)
	}
	assertNoSubmissionOutcome(t, events)
}
