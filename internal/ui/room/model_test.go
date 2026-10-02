package room

import (
	"bytes"
	"errors"
	"testing"
)

func TestWriteOSC52_encodesClipboardPayload(t *testing.T) {
	var out bytes.Buffer
	if err := writeOSC52(&out, "hi"); err != nil {
		t.Fatalf("writeOSC52 returned error: %v", err)
	}
	if got, want := out.String(), "\x1b]52;c;aGk=\a"; got != want {
		t.Fatalf("OSC52 payload = %q, want %q", got, want)
	}
}

func TestDefaultClipboardWriter_prefersSystemClipboard(t *testing.T) {
	previous := systemClipboardWrite
	t.Cleanup(func() { systemClipboardWrite = previous })

	calls := 0
	systemClipboardWrite = func(text string) error {
		calls++
		if text != "hello" {
			t.Fatalf("clipboard text = %q, want %q", text, "hello")
		}
		return nil
	}

	var out bytes.Buffer
	if err := defaultClipboardWriter(&out)("hello"); err != nil {
		t.Fatalf("defaultClipboardWriter returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("system clipboard calls = %d, want 1", calls)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no OSC52 fallback on system clipboard success, got %q", out.String())
	}
}

func TestDefaultClipboardWriter_fallsBackToOSC52(t *testing.T) {
	previous := systemClipboardWrite
	t.Cleanup(func() { systemClipboardWrite = previous })

	systemClipboardWrite = func(string) error {
		return errors.New("clipboard unavailable")
	}

	var out bytes.Buffer
	if err := defaultClipboardWriter(&out)("hi"); err != nil {
		t.Fatalf("defaultClipboardWriter returned error: %v", err)
	}
	if got, want := out.String(), "\x1b]52;c;aGk=\a"; got != want {
		t.Fatalf("OSC52 fallback payload = %q, want %q", got, want)
	}
}

func TestNew_setsSystemClipboardRead(t *testing.T) {
	m := NewPresenter(nil, "")
	if m.clipboardRead == nil {
		t.Fatal("expected clipboardRead to be initialized")
	}
}
