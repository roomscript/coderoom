package claude

// ProtocolObserver receives every raw JSON line as it flows over stdio,
// before any parsing. Implementations must be fast; avoid operations that
// can block for non-trivial time (network calls, contested locks). A log
// file write is acceptable. msg is the raw JSON without the trailing newline.
type ProtocolObserver interface {
	OnSend(msg string)
	OnReceive(msg string)
}

// noopObserver is the default ProtocolObserver when none is provided.
type noopObserver struct{}

func (noopObserver) OnSend(string)    {}
func (noopObserver) OnReceive(string) {}
